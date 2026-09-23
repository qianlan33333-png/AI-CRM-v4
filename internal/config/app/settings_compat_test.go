package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type compatibilityUoW struct{}

func (compatibilityUoW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type projectionStub struct {
	settings []configport.ProjectionSetting
	audits   []configport.ProjectionAudit
	err      error
}

func (stub *projectionStub) ListAppSettings(context.Context) ([]configport.ProjectionSetting, error) {
	return stub.settings, stub.err
}
func (stub *projectionStub) ListAppSettingsAudit(context.Context) ([]configport.ProjectionAudit, error) {
	return stub.audits, stub.err
}

type settingManagerStub struct {
	commands []configport.SetCommand
	err      error
}

func (stub *settingManagerStub) Get(context.Context, configport.Key) (configport.Setting, error) {
	return configport.Setting{}, errors.New("unexpected Get")
}
func (stub *settingManagerStub) Set(_ context.Context, command configport.SetCommand) (configport.Setting, error) {
	stub.commands = append(stub.commands, command)
	return configport.Setting{}, stub.err
}

func TestSettingsProjectionFreezesTwelveKeyDTOAndSecretBoundary(t *testing.T) {
	updated := time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC)
	repo := &projectionStub{
		settings: []configport.ProjectionSetting{{Key: configport.WeComCorpID, Value: []byte(`"corp"`), UpdatedAt: updated, LastActionType: "update", LastModifiedBy: "admin:7", LastModifiedAt: &updated}},
		audits: []configport.ProjectionAudit{
			{ID: 9, Operator: "admin:7", ActionType: "update", TargetID: configport.WeComCorpID, CreatedAt: updated},
			{ID: 8, Operator: "admin:7", ActionType: "create", TargetID: configport.WeComCorpID, CreatedAt: updated.Add(-time.Minute)},
		},
	}
	service := NewSettingsCompatibilityService(compatibilityUoW{}, repo, &settingManagerStub{}, SecretConfiguredSnapshot{DatabaseURL: true})
	got, err := service.List(context.Background(), SettingsListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 12 || len(got.MetadataMap) != 12 || len(got.AuditEntries) != 2 {
		t.Fatalf("sizes rows=%d metadata=%d audit=%d", len(got.Rows), len(got.MetadataMap), len(got.AuditEntries))
	}
	wantCards := []SummaryCard{{"可直接编辑", 4, "可以直接修改的设置项"}, {"敏感信息", 8, "只显示掩码的设置项"}, {"已配置", 2, "当前已经配置完成的设置项"}}
	if !reflect.DeepEqual(got.SummaryCards, wantCards) {
		t.Fatalf("summary=%#v", got.SummaryCards)
	}
	encoded, err := json.Marshal(got.Rows[4])
	if err != nil {
		t.Fatal(err)
	}
	var secret map[string]any
	if err = json.Unmarshal(encoded, &secret); err != nil {
		t.Fatal(err)
	}
	wantSecretKeys := []string{"configured", "description", "input_type", "key", "label", "masked", "mode"}
	for _, key := range wantSecretKeys {
		if _, ok := secret[key]; !ok {
			t.Fatalf("secret missing %s: %s", key, encoded)
		}
	}
	if len(secret) != len(wantSecretKeys) || secret["configured"] != true || secret["masked"] != true {
		t.Fatalf("secret DTO leaked/drifted: %s", encoded)
	}
	editable := got.Rows[0].(EditableSettingRow)
	if editable.Value != "corp" || editable.LastActionType != "update" || editable.Source != "app_settings" || editable.Version != "" {
		t.Fatalf("editable=%#v", editable)
	}
	empty := got.Rows[1].(EditableSettingRow)
	if empty.Value != "" || empty.DisplayValue != "" || empty.Configured || empty.Source != "config" || empty.Version != "" || empty.UpdatedAt != "" || empty.LastModifiedAt != "" || empty.LastModifiedBy != "" || empty.LastActionType != "empty" {
		t.Fatalf("empty editable defaults=%#v", empty)
	}
}

func TestSettingsProjectionFilteringCountsOnlyReturnedRows(t *testing.T) {
	service := NewSettingsCompatibilityService(compatibilityUoW{}, &projectionStub{}, &settingManagerStub{}, SecretConfiguredSnapshot{DatabaseURL: true})
	got, err := service.List(context.Background(), SettingsListInput{Search: "wecom.", Scope: "masked"})
	if err != nil {
		t.Fatal(err)
	}
	want := []SummaryCard{{"可直接编辑", 0, "可以直接修改的设置项"}, {"敏感信息", 3, "只显示掩码的设置项"}, {"已配置", 0, "当前已经配置完成的设置项"}}
	if len(got.Rows) != 3 || !reflect.DeepEqual(got.SummaryCards, want) {
		t.Fatalf("rows/cards=%d %#v", len(got.Rows), got.SummaryCards)
	}
	if _, err = service.List(context.Background(), SettingsListInput{Scope: "all"}); !errors.Is(err, ErrInvalidAppSettingsRequest) {
		t.Fatalf("invalid scope err=%v", err)
	}
	empty, err := service.List(context.Background(), SettingsListInput{Search: "no-such-setting"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"rows":[]`) || !strings.Contains(string(encoded), `"audit_entries":[]`) {
		t.Fatalf("empty arrays drifted to null: %s", encoded)
	}
}

func TestSavePrevalidatesAllInputsAndUsesPrincipalDerivedMetadata(t *testing.T) {
	manager := &settingManagerStub{}
	service := NewSettingsCompatibilityService(compatibilityUoW{}, &projectionStub{}, manager, SecretConfiguredSnapshot{})
	err := service.Save(context.Background(), SaveSettingsInput{Actor: "admin:7", RequestID: "request-123", Values: map[string][]string{
		"wecom.corp_id": {"corp"}, "outbound.max_attempts": {"3"}, "database.url": {""},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.commands) != 2 {
		t.Fatalf("commands=%#v", manager.commands)
	}
	for _, command := range manager.commands {
		if command.Actor != "admin:7" || command.RequestID != "request-123:"+string(command.Key) {
			t.Fatalf("metadata=%#v", command)
		}
	}
	manager.commands = nil
	err = service.Save(context.Background(), SaveSettingsInput{Actor: "admin:7", RequestID: "request-124", Values: map[string][]string{"wecom.corp_id": {"corp"}, "database.url": {"must-not-pass"}}})
	if !errors.Is(err, configport.ErrSecretSetting) || len(manager.commands) != 0 {
		t.Fatalf("secret err/commands=%v %#v", err, manager.commands)
	}
	err = service.Save(context.Background(), SaveSettingsInput{Actor: "admin:7", RequestID: "request-125", Values: map[string][]string{"future.key": {"x"}}})
	if !errors.Is(err, ErrInvalidAppSettingsRequest) || len(manager.commands) != 0 {
		t.Fatalf("unknown err/commands=%v %#v", err, manager.commands)
	}
	err = service.Save(context.Background(), SaveSettingsInput{Actor: "admin:7", RequestID: "request-126", Values: map[string][]string{"database.url": {" \t "}}})
	if err != nil || len(manager.commands) != 0 {
		t.Fatalf("trim-blank secret err/commands=%v %#v", err, manager.commands)
	}
	err = service.Save(context.Background(), SaveSettingsInput{Actor: "admin:7", RequestID: "request-127", Values: map[string][]string{"wecom.corp_id": {"corp", "duplicate"}}})
	if !errors.Is(err, ErrInvalidAppSettingsRequest) || len(manager.commands) != 0 {
		t.Fatalf("duplicate err/commands=%v %#v", err, manager.commands)
	}
}
