package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCatalogValidChannelAndImmutableCode(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	channel, err := NewChannel(CreateChannel{Code: "autumn-2026", Status: StatusInactive, Config: validCatalogConfig()}, now)
	if err != nil {
		t.Fatalf("NewChannel: %v", err)
	}
	if channel.ConfigVersion != 1 || channel.Version != 1 || channel.CreatedAt.Location() != time.UTC {
		t.Fatalf("unexpected initial versions/time: %+v", channel)
	}
	updatedConfig := validCatalogConfig()
	updatedConfig.Name = "秋季活动（二期）"
	updated, err := channel.Update(UpdateChannel{ExpectedVersion: 1, Code: channel.Code, Status: StatusActive, Config: updatedConfig}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != 2 || updated.ConfigVersion != 2 || !updated.CanPublish() {
		t.Fatalf("unexpected updated channel: %+v", updated)
	}
	_, err = updated.Update(UpdateChannel{ExpectedVersion: 2, Code: "changed", Status: StatusActive, Config: updatedConfig}, now.Add(2*time.Hour))
	if !errors.Is(err, ErrImmutableCode) {
		t.Fatalf("expected immutable code, got %v", err)
	}
	_, err = updated.Update(UpdateChannel{ExpectedVersion: 1, Code: updated.Code, Status: StatusActive, Config: updatedConfig}, now.Add(2*time.Hour))
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
}

func TestCatalogArchivedCanEditAndExplicitlyReactivate(t *testing.T) {
	now := time.Now()
	channel, err := NewChannel(CreateChannel{Code: "archived", Status: StatusArchived, Config: validCatalogConfig()}, now)
	if err != nil {
		t.Fatal(err)
	}
	config := channel.Config
	config.Assignment.Assignees = []Assignee{{StaffID: 42, Priority: 1, Ratio: 100}}
	edited, err := channel.Update(UpdateChannel{ExpectedVersion: 1, Code: channel.Code, Status: StatusArchived, Config: config}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if edited.CanPublish() || edited.Status != StatusArchived || edited.Version != 2 || edited.ConfigVersion != 2 || edited.Config.Assignment.Assignees[0].StaffID != 42 {
		t.Fatalf("unexpected archived edit: %+v", edited)
	}
	active, err := edited.Update(UpdateChannel{ExpectedVersion: 2, Code: edited.Code, Status: StatusActive, Config: edited.Config}, now.Add(2*time.Second))
	if err != nil || !active.CanPublish() {
		t.Fatalf("explicit reactivation: %+v %v", active, err)
	}
	_, err = edited.Update(UpdateChannel{ExpectedVersion: 1, Code: edited.Code, Status: StatusArchived, Config: config}, now.Add(2*time.Second))
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale archived edit: %v", err)
	}
}

func TestCatalogValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateChannel)
	}{
		{name: "blank name", mutate: func(value *CreateChannel) { value.Config.Name = "" }},
		{name: "untrimmed name", mutate: func(value *CreateChannel) { value.Config.Name = " 渠道" }},
		{name: "invalid code", mutate: func(value *CreateChannel) { value.Code = "bad code" }},
		{name: "invalid status", mutate: func(value *CreateChannel) { value.Status = "deleted" }},
		{name: "mismatched carrier", mutate: func(value *CreateChannel) { value.Config.Carrier = CarrierLink }},
		{name: "http link", mutate: func(value *CreateChannel) { value.Config.LinkURL = "http://unsafe.example.test" }},
		{name: "url credentials", mutate: func(value *CreateChannel) { value.Config.LinkURL = "https://user@example.test/path" }},
		{name: "too much welcome", mutate: func(value *CreateChannel) { value.Config.WelcomeMessage = strings.Repeat("好", 10001) }},
		{name: "duplicate media", mutate: func(value *CreateChannel) { value.Config.Media.Images = []int64{1, 1} }},
		{name: "tag snapshot missing", mutate: func(value *CreateChannel) { value.Config.EntryTagID = 9; value.Config.EntryTagName = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := CreateChannel{Code: "valid-code", Status: StatusInactive, Config: validCatalogConfig()}
			test.mutate(&value)
			if _, err := NewChannel(value, time.Now()); !errors.Is(err, ErrInvalidChannel) {
				t.Fatalf("expected invalid channel, got %v", err)
			}
		})
	}
}

func TestAssignmentValidation(t *testing.T) {
	validRatio := Assignment{Mode: AssignmentMulti, Strategy: StrategyRatio, OverflowPolicy: "fallback_owner", Assignees: []Assignee{{StaffID: 1, Priority: 1, Ratio: 60}, {StaffID: 2, Priority: 2, Ratio: 40}}}
	validCap := Assignment{Mode: AssignmentMulti, Strategy: StrategyCapSwitch, Assignees: []Assignee{{StaffID: 1, Priority: 1, MaxScans24h: 20}, {StaffID: 2, Priority: 2, MaxScans24h: 30}}}
	for _, assignment := range []Assignment{validRatio, validCap, {Mode: AssignmentSingle, Strategy: StrategyRatio, Assignees: []Assignee{{StaffID: 1, Priority: 1, Ratio: 100}}}} {
		if err := ValidateAssignment(assignment); err != nil {
			t.Fatalf("valid assignment rejected: %v", err)
		}
	}
	invalid := []Assignment{
		{Mode: AssignmentMulti, Strategy: StrategyRatio},
		{Mode: AssignmentSingle, Strategy: StrategyRatio, Assignees: validRatio.Assignees},
		{Mode: AssignmentMulti, Strategy: StrategyRatio, Assignees: []Assignee{{StaffID: 1, Priority: 1, Ratio: 99}}},
		{Mode: AssignmentMulti, Strategy: StrategyCapSwitch, Assignees: []Assignee{{StaffID: 1, Priority: 1}}},
		{Mode: AssignmentMulti, Strategy: StrategyRatio, Assignees: []Assignee{{StaffID: 1, Priority: 1, Ratio: 50}, {StaffID: 1, Priority: 2, Ratio: 50}}},
		{Mode: AssignmentMulti, Strategy: StrategyRatio, Assignees: []Assignee{{StaffID: 1, Priority: 2, Ratio: 100}}},
	}
	for index, assignment := range invalid {
		if !errors.Is(ValidateAssignment(assignment), ErrInvalidAssignment) {
			t.Fatalf("invalid assignment %d accepted", index)
		}
	}
}

func validCatalogConfig() Config {
	return Config{
		Type: ChannelQRCode, Carrier: CarrierQRCode, Name: "秋季活动", SceneValue: "autumn",
		QRCodeURL: "https://work.weixin.qq.com/q/example", WelcomeMessage: "欢迎加入",
		Media:      MediaReferences{Images: []int64{1}, MiniPrograms: []int64{2}, Attachments: []int64{3}, GroupInvites: []int64{4}},
		EntryTagID: 9, EntryTagName: "新客", EntryTagGroupName: "来源",
		Assignment: Assignment{Mode: AssignmentSingle, Strategy: StrategyRatio, Assignees: []Assignee{{StaffID: 7, Priority: 1, Ratio: 100}}},
	}
}
