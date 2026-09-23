package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	config "github.com/qianlan33333-png/AI-CRM-v3/internal/config"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrInvalidAppSettingsRequest = errors.New("invalid app settings request")

type SettingMetadata struct {
	Key         configport.Key `json:"key"`
	Label       string         `json:"label"`
	Mode        string         `json:"mode"`
	InputType   string         `json:"input_type"`
	Description string         `json:"description"`
}

type EditableSettingRow struct {
	SettingMetadata
	Value          string `json:"value"`
	DisplayValue   string `json:"display_value"`
	Configured     bool   `json:"configured"`
	Source         string `json:"source"`
	Version        string `json:"version"`
	UpdatedAt      string `json:"updated_at"`
	LastModifiedAt string `json:"last_modified_at"`
	LastModifiedBy string `json:"last_modified_by"`
	LastActionType string `json:"last_action_type"`
}

type MaskedSettingRow struct {
	SettingMetadata
	Configured bool `json:"configured"`
	Masked     bool `json:"masked"`
}

type SummaryCard struct {
	Label       string `json:"label"`
	Value       int    `json:"value"`
	Description string `json:"description"`
}
type AuditEntry struct {
	ID         int64          `json:"id"`
	Operator   string         `json:"operator"`
	ActionType string         `json:"action_type"`
	TargetID   configport.Key `json:"target_id"`
	CreatedAt  string         `json:"created_at"`
}
type SettingsProjection struct {
	Rows         []any                              `json:"rows"`
	MetadataMap  map[configport.Key]SettingMetadata `json:"metadata_map"`
	SummaryCards []SummaryCard                      `json:"summary_cards"`
	AuditEntries []AuditEntry                       `json:"audit_entries"`
}
type SettingsListInput struct {
	Search string
	Scope  string
}

type SecretConfiguredSnapshot struct {
	DatabaseURL, WeComSecret, WeComCallbackToken, WeComCallbackAESKey bool
	AIAPIKey, AuthJWTSecret, ExtensionAPIKeyPepper, WebhookMasterKey  bool
}

type settingsProjectionRepository interface {
	ListAppSettings(context.Context) ([]configport.ProjectionSetting, error)
	ListAppSettingsAudit(context.Context) ([]configport.ProjectionAudit, error)
}

type SettingsCompatibilityService struct {
	uow     platformport.UnitOfWork
	repo    settingsProjectionRepository
	manager configport.Service
	secrets SecretConfiguredSnapshot
}

func NewSettingsCompatibilityService(uow platformport.UnitOfWork, repo settingsProjectionRepository, manager configport.Service, secrets SecretConfiguredSnapshot) *SettingsCompatibilityService {
	return &SettingsCompatibilityService{uow: uow, repo: repo, manager: manager, secrets: secrets}
}

var catalog = []SettingMetadata{
	{configport.WeComCorpID, "wecom.corp_id", "editable", "text", ""},
	{configport.WeComAgentID, "wecom.agent_id", "editable", "number", ""},
	{configport.OutboundRatePerSecond, "outbound.rate_per_second", "editable", "number", ""},
	{configport.OutboundMaxAttempts, "outbound.max_attempts", "editable", "number", ""},
	{configport.DatabaseURL, "database.url", "masked", "password", ""},
	{configport.WeComSecret, "wecom.secret", "masked", "password", ""},
	{configport.WeComCallbackToken, "wecom.callback_token", "masked", "password", ""},
	{configport.WeComCallbackAESKey, "wecom.callback_aes_key", "masked", "password", ""},
	{configport.AIAPIKey, "ai.api_key", "masked", "password", ""},
	{configport.AuthJWTSecret, "auth.jwt_secret", "masked", "password", ""},
	{configport.ExtensionAPIKeyPepper, "extension.api_key_pepper", "masked", "password", ""},
	{configport.WebhookMasterKey, "gateway.webhook_master_key", "masked", "password", ""},
}

func (service *SettingsCompatibilityService) List(ctx context.Context, input SettingsListInput) (projection SettingsProjection, err error) {
	if service == nil || service.uow == nil || service.repo == nil || len(input.Search) > 200 || strings.TrimSpace(input.Search) != input.Search || (input.Scope != "" && input.Scope != "editable" && input.Scope != "masked") {
		return SettingsProjection{}, ErrInvalidAppSettingsRequest
	}
	var stored []configport.ProjectionSetting
	var audits []configport.ProjectionAudit
	err = service.uow.Within(ctx, func(tx context.Context) error {
		var listErr error
		stored, listErr = service.repo.ListAppSettings(tx)
		if listErr != nil {
			return listErr
		}
		audits, listErr = service.repo.ListAppSettingsAudit(tx)
		return listErr
	})
	if err != nil {
		return SettingsProjection{}, err
	}
	byKey := make(map[configport.Key]configport.ProjectionSetting, len(stored))
	for _, item := range stored {
		byKey[item.Key] = item
	}
	projection.Rows = make([]any, 0, len(catalog))
	projection.MetadataMap = make(map[configport.Key]SettingMetadata, len(catalog))
	projection.AuditEntries = make([]AuditEntry, 0, len(audits))
	editable, masked, configured := 0, 0, 0
	for _, metadata := range catalog {
		projection.MetadataMap[metadata.Key] = metadata
		if input.Scope != "" && input.Scope != metadata.Mode {
			continue
		}
		needle := strings.ToLower(input.Search)
		if needle != "" && !strings.Contains(strings.ToLower(string(metadata.Key)+" "+metadata.Label+" "+metadata.Description), needle) {
			continue
		}
		if metadata.Mode == "masked" {
			value := service.secretConfigured(metadata.Key)
			projection.Rows = append(projection.Rows, MaskedSettingRow{SettingMetadata: metadata, Configured: value, Masked: true})
			masked++
			if value {
				configured++
			}
			continue
		}
		row := EditableSettingRow{SettingMetadata: metadata, Source: "config", LastActionType: "empty"}
		if item, ok := byKey[metadata.Key]; ok {
			var value string
			if metadata.InputType == "text" {
				if json.Unmarshal(item.Value, &value) != nil {
					return SettingsProjection{}, fmt.Errorf("%w: stored value", ErrInvalidAppSettingsRequest)
				}
			} else {
				var number int64
				if json.Unmarshal(item.Value, &number) != nil {
					return SettingsProjection{}, fmt.Errorf("%w: stored value", ErrInvalidAppSettingsRequest)
				}
				value = strconv.FormatInt(number, 10)
			}
			row.Value = value
			row.DisplayValue = value
			row.Configured = true
			row.Source = "app_settings"
			row.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339Nano)
			row.LastModifiedBy = item.LastModifiedBy
			row.LastActionType = item.LastActionType
			if item.LastModifiedAt != nil {
				row.LastModifiedAt = item.LastModifiedAt.UTC().Format(time.RFC3339Nano)
			}
		}
		projection.Rows = append(projection.Rows, row)
		editable++
		if row.Configured {
			configured++
		}
	}
	projection.SummaryCards = []SummaryCard{{"可直接编辑", editable, "可以直接修改的设置项"}, {"敏感信息", masked, "只显示掩码的设置项"}, {"已配置", configured, "当前已经配置完成的设置项"}}
	for _, item := range audits {
		projection.AuditEntries = append(projection.AuditEntries, AuditEntry{item.ID, item.Operator, item.ActionType, item.TargetID, item.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return projection, nil
}

func (service *SettingsCompatibilityService) secretConfigured(key configport.Key) bool {
	switch key {
	case configport.DatabaseURL:
		return service.secrets.DatabaseURL
	case configport.WeComSecret:
		return service.secrets.WeComSecret
	case configport.WeComCallbackToken:
		return service.secrets.WeComCallbackToken
	case configport.WeComCallbackAESKey:
		return service.secrets.WeComCallbackAESKey
	case configport.AIAPIKey:
		return service.secrets.AIAPIKey
	case configport.AuthJWTSecret:
		return service.secrets.AuthJWTSecret
	case configport.ExtensionAPIKeyPepper:
		return service.secrets.ExtensionAPIKeyPepper
	case configport.WebhookMasterKey:
		return service.secrets.WebhookMasterKey
	default:
		return false
	}
}

type SaveSettingsInput struct {
	Values    map[string][]string
	Actor     string
	RequestID string
}

func (service *SettingsCompatibilityService) Save(ctx context.Context, input SaveSettingsInput) error {
	if service == nil || service.manager == nil || input.Actor == "" || input.RequestID == "" {
		return ErrInvalidAppSettingsRequest
	}
	commands := make([]configport.SetCommand, 0, 4)
	known := make(map[configport.Key]SettingMetadata, len(catalog))
	for _, item := range catalog {
		known[item.Key] = item
	}
	for rawKey, values := range input.Values {
		key := configport.Key(rawKey)
		metadata, ok := known[key]
		if !ok || len(values) != 1 {
			return ErrInvalidAppSettingsRequest
		}
		value := values[0]
		if metadata.Mode == "masked" {
			if strings.TrimSpace(value) != "" {
				return configport.ErrSecretSetting
			}
			continue
		}
		var raw json.RawMessage
		if metadata.InputType == "number" {
			raw = json.RawMessage(value)
		} else {
			raw, _ = json.Marshal(value)
		}
		canonical, validateErr := config.ValidateSetting(key, raw)
		if validateErr != nil {
			return validateErr
		}
		commands = append(commands, configport.SetCommand{Key: key, Value: canonical, Actor: input.Actor, RequestID: input.RequestID + ":" + rawKey})
	}
	if len(commands) == 0 {
		return nil
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Key < commands[j].Key })
	type requestItem struct {
		Key   configport.Key  `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	items := make([]requestItem, 0, len(commands))
	for _, command := range commands {
		items = append(items, requestItem{Key: command.Key, Value: command.Value})
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if batch, ok := service.manager.(interface {
		SetManyWithRequest(context.Context, string, string, []byte, []configport.SetCommand) error
	}); ok {
		return batch.SetManyWithRequest(ctx, input.Actor, input.RequestID, payload, commands)
	}
	if batch, ok := service.manager.(interface {
		SetMany(context.Context, []configport.SetCommand) error
	}); ok {
		return batch.SetMany(ctx, commands)
	}
	for _, command := range commands { // test-double compatibility only
		if _, err := service.manager.Set(ctx, command); err != nil {
			return err
		}
	}
	return nil
}
