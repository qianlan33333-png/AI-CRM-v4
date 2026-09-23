package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var ErrInvalidPolicy = errors.New("invalid automation policy")

type PolicyLifecycle string

const (
	PolicyPaused   PolicyLifecycle = "paused"
	PolicyActive   PolicyLifecycle = "active"
	PolicyArchived PolicyLifecycle = "archived"
)

type Policy struct {
	ID               int64           `json:"id"`
	Code             string          `json:"code"`
	Name             string          `json:"name"`
	Lifecycle        PolicyLifecycle `json:"lifecycle"`
	Version          int64           `json:"version"`
	CurrentVersionID *int64          `json:"current_version_id,omitempty"`
	CreatedBy        int64           `json:"created_by"`
	UpdatedBy        int64           `json:"updated_by"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	ArchivedAt       *time.Time      `json:"archived_at,omitempty"`
}
type QuietHours struct {
	Timezone string `json:"timezone"`
	Start    string `json:"start"`
	End      string `json:"end"`
}
type PolicyVersion struct {
	ID              int64                      `json:"id"`
	PolicyID        int64                      `json:"policy_id"`
	Version         int64                      `json:"version"`
	PackageID       segmentport.PackageID      `json:"package_id"`
	TriggerKind     automationport.TriggerKind `json:"trigger_kind"`
	TriggerEnabled  bool                       `json:"trigger_enabled"`
	ActionKind      automationport.ActionKind  `json:"action_kind"`
	ActionConfig    json.RawMessage            `json:"action_config"`
	QuietHours      json.RawMessage            `json:"quiet_hours"`
	SingleRunLimit  int                        `json:"single_run_limit"`
	ApprovalStaffID *int64                     `json:"approval_staff_id,omitempty"`
	Digest          [32]byte                   `json:"-"`
	CreatedBy       int64                      `json:"created_by"`
	CreatedAt       time.Time                  `json:"created_at"`
}

var policyCode = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,119}$`)

func NewPolicy(code, name string, actor int64, now time.Time) (Policy, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	name = strings.TrimSpace(name)
	if !policyCode.MatchString(code) || name == "" || len([]rune(name)) > 200 || actor < 1 || now.IsZero() {
		return Policy{}, ErrInvalidPolicy
	}
	return Policy{Code: code, Name: name, Lifecycle: PolicyPaused, Version: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}, nil
}
func NewPolicyVersion(policyID, version int64, packageID segmentport.PackageID, trigger automationport.TriggerKind, action automationport.ActionKind, actionConfig, quietHours json.RawMessage, limit int, approval *int64, actor int64, now time.Time) (PolicyVersion, error) {
	if policyID < 1 || version < 1 || packageID < 1 || actor < 1 || now.IsZero() || limit < 1 || limit > 100000 || approval == nil || *approval < 1 {
		return PolicyVersion{}, ErrInvalidPolicy
	}
	triggerEnabled := trigger == automationport.TriggerAudienceMemberEnteredV1
	if trigger != automationport.TriggerAudienceMemberEnteredV1 && trigger != automationport.TriggerCustomerTagAppliedV1 {
		return PolicyVersion{}, ErrInvalidPolicy
	}
	actionConfig, err := canonicalAction(action, actionConfig)
	if err != nil {
		return PolicyVersion{}, err
	}
	quietHours, err = canonicalQuietHours(quietHours)
	if err != nil {
		return PolicyVersion{}, err
	}
	digestInput, _ := json.Marshal([]any{packageID, trigger, triggerEnabled, action, actionConfig, quietHours, limit, *approval})
	return PolicyVersion{PolicyID: policyID, Version: version, PackageID: packageID, TriggerKind: trigger, TriggerEnabled: triggerEnabled, ActionKind: action, ActionConfig: actionConfig, QuietHours: quietHours, SingleRunLimit: limit, ApprovalStaffID: approval, Digest: sha256.Sum256(digestInput), CreatedBy: actor, CreatedAt: now}, nil
}
func canonicalAction(kind automationport.ActionKind, raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	switch kind {
	case automationport.ActionRecord:
		var in struct {
			RecordType string `json:"record_type"`
		}
		if decoder.Decode(&in) != nil || strings.TrimSpace(in.RecordType) == "" || len(in.RecordType) > 100 {
			return nil, ErrInvalidPolicy
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, ErrInvalidPolicy
		}
		return json.Marshal(in)
	case automationport.ActionOutboundMessage:
		var in struct {
			AgentID int64 `json:"agent_id"`
		}
		if decoder.Decode(&in) != nil || in.AgentID < 1 {
			return nil, ErrInvalidPolicy
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, ErrInvalidPolicy
		}
		return json.Marshal(in)
	default:
		return nil, ErrInvalidPolicy
	}
}
func canonicalQuietHours(raw json.RawMessage) (json.RawMessage, error) {
	var in QuietHours
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.Timezone == "" || len(in.Timezone) > 100 || !clock(in.Start) || !clock(in.End) || in.Start == in.End {
		return nil, ErrInvalidPolicy
	}
	if _, zoneErr := time.LoadLocation(in.Timezone); zoneErr != nil {
		return nil, ErrInvalidPolicy
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidPolicy
	}
	return json.Marshal(in)
}
func clock(v string) bool {
	if len(v) != 5 || v[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", v)
	return err == nil
}

type Enrollment struct {
	ID                int64                     `json:"id"`
	PolicyID          int64                     `json:"policy_id"`
	PolicyVersionID   int64                     `json:"policy_version_id"`
	SourceEventDigest [32]byte                  `json:"-"`
	CustomerID        int64                     `json:"customer_id"`
	ActionKind        automationport.ActionKind `json:"action_kind"`
	ActionSnapshot    json.RawMessage           `json:"action_snapshot"`
	ActionDigest      [32]byte                  `json:"-"`
	State             string                    `json:"state"`
	CreatedAt         time.Time                 `json:"created_at"`
}
type RunPreview struct {
	ID                     int64     `json:"id"`
	PackageID              int64     `json:"package_id"`
	PackageVersion         int64     `json:"package_version"`
	SnapshotID             int64     `json:"snapshot_id"`
	ConfigurationVersionID int64     `json:"configuration_version_id"`
	AgentID                int64     `json:"agent_id"`
	AgentPublishedVersion  int64     `json:"agent_published_version"`
	BindingVersion         int64     `json:"binding_version"`
	SenderSetVersion       int64     `json:"sender_set_version"`
	TargetCount            int64     `json:"target_count"`
	SkippedCount           int64     `json:"skipped_count"`
	RuntimeConfigObserved  bool      `json:"runtime_config_observed"`
	RuntimeConfigRevision  int64     `json:"runtime_config_revision,omitempty"`
	MaxRecipientsPerRun    int       `json:"max_recipients_per_run,omitempty"`
	PreviewDigest          [32]byte  `json:"-"`
	CreatedBy              int64     `json:"created_by"`
	CreatedAt              time.Time `json:"created_at"`
	ExpiresAt              time.Time `json:"expires_at"`
}
type RuntimeRun struct {
	ID                    int64                             `json:"id"`
	PolicyID              int64                             `json:"policy_id,omitempty"`
	PolicyVersion         int64                             `json:"policy_version,omitempty"`
	PackageID             int64                             `json:"package_id"`
	PackageVersion        int64                             `json:"package_version"`
	SnapshotID            int64                             `json:"snapshot_id"`
	AgentID               int64                             `json:"agent_id"`
	AgentPublishedVersion int64                             `json:"agent_published_version"`
	AIPlanID              int64                             `json:"ai_plan_id,omitempty"`
	AIPlanState           string                            `json:"ai_plan_state,omitempty"`
	BindingVersion        int64                             `json:"binding_version"`
	SenderSetVersion      int64                             `json:"sender_set_version"`
	RuntimeConfigObserved bool                              `json:"runtime_config_observed"`
	RuntimeConfigRevision int64                             `json:"runtime_config_revision,omitempty"`
	MaxRecipientsPerRun   int                               `json:"max_recipients_per_run,omitempty"`
	PreviewDigest         [32]byte                          `json:"-"`
	State                 automationport.RunState           `json:"state"`
	TargetCount           int64                             `json:"target_count"`
	SkippedCount          int64                             `json:"skipped_count"`
	OutcomeUnknownCount   int64                             `json:"outcome_unknown_count"`
	CreatedBy             int64                             `json:"created_by"`
	CreatedAt             time.Time                         `json:"created_at"`
	UpdatedAt             time.Time                         `json:"updated_at"`
	Generation            automationport.GenerationProgress `json:"generation"`
}

type RunReconciliation struct {
	ID             int64     `json:"id"`
	RunID          int64     `json:"run_id"`
	RecipientID    int64     `json:"recipient_id"`
	EffectID       string    `json:"effect_id"`
	Generation     int64     `json:"generation"`
	Fence          int64     `json:"fence"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	EvidenceDigest [32]byte  `json:"-"`
	Resolution     string    `json:"resolution"`
	ActorID        int64     `json:"actor_id"`
	ReceiptDigest  [32]byte  `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}
type RuntimeRecipient struct {
	ID            int64                         `json:"id"`
	RunID         int64                         `json:"run_id"`
	CustomerID    int64                         `json:"customer_id"`
	SenderStaffID int64                         `json:"sender_staff_id"`
	State         automationport.RecipientState `json:"state"`
	SkipCode      string                        `json:"skip_code,omitempty"`
	EffectID      string                        `json:"effect_id,omitempty"`
	CreatedAt     time.Time                     `json:"created_at"`
	UpdatedAt     time.Time                     `json:"updated_at"`
}

// GenerationItem is Automation-owned persisted work. Context and prompt text
// are kept only in this owner table; External Effects receives its four
// digests, while the provider adapter re-reads this immutable dispatch record.
type GenerationItem struct {
	ID                    int64
	RunID                 int64
	CustomerID            int64
	SenderStaffID         int64
	AgentID               int64
	AgentPublishedVersion int64
	AgentCode             string
	RolePrompt            string
	TaskPrompt            string
	Context               automationport.GenerationContext
	ModelPolicy           automationport.GenerationModelPolicy
	SourceDigest          [32]byte
	TargetDigest          [32]byte
	PayloadDigest         [32]byte
	PolicyDigest          [32]byte
	ReceiptKeyDigest      [32]byte
	EffectID              string
	State                 string
	GeneratedText         string
	ResultDigest          [32]byte
	FailureCode           string
	AttemptCount          int32
	CreatedAt             time.Time
	UpdatedAt             time.Time
	CompletedAt           *time.Time
}
