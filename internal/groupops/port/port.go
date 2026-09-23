// Package port defines the local-only Group Ops contract.
package port

import (
	"encoding/json"
	"time"
)

type PlanStatus string

const (
	PlanDraft    PlanStatus = "draft"
	PlanActive   PlanStatus = "active"
	PlanPaused   PlanStatus = "paused"
	PlanArchived PlanStatus = "archived"
)

// PlanType controls how a plan obtains its message content. A zero value is
// retained for already-persisted standard plans; new callers should use the
// explicit standard value when they mean scheduled node content.
const (
	PlanTypeStandard = "standard"
	PlanTypeWebhook  = "webhook"
)

type NodeKind string

const (
	NodeMessage NodeKind = "message"
	NodeDelay   NodeKind = "delay"
)

// Safety is deliberately included in every successful Group Ops response.
// The local package has no provider, runtime, webhook client, or send path.
type Safety struct {
	ProviderExecutionEligible bool `json:"provider_execution_eligible"`
	RealExternalCallExecuted  bool `json:"real_external_call_executed"`
}

func LocalSafety() Safety { return Safety{} }

type Plan struct {
	Type      string     `json:"plan_type,omitempty"`
	ID        int64      `json:"plan_id,string"`
	Name      string     `json:"name"`
	Status    PlanStatus `json:"status"`
	Revision  int64      `json:"revision"`
	CreatedBy int64      `json:"created_by"`
	UpdatedBy int64      `json:"updated_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Owner     PlanOwner  `json:"owner"`
}

// PlanOwner is the read-only responsible-member projection for a plan. It
// joins the plan's persisted local staff key to the Group Ops-owned directory;
// it does not create or resolve a customer identity.
type PlanOwner struct {
	StaffID              int64  `json:"staff_id,omitempty"`
	SenderUserID         string `json:"sender_userid,omitempty"`
	DisplayName          string `json:"display_name,omitempty"`
	NameSource           string `json:"name_source,omitempty"`
	ProfileReadState     string `json:"profile_read_state,omitempty"`
	ProfileReadErrorCode string `json:"profile_read_error_code,omitempty"`
}

type PlanListItem struct {
	Plan
	QueueCount      int64 `json:"queue_count"`
	BoundGroupCount int64 `json:"bound_group_count"`
}

type Member struct {
	StaffID int64 `json:"staff_id"`
}

type GroupAsset struct {
	ID       int64  `json:"group_asset_id,string"`
	AssetRef string `json:"asset_reference"`
}

type Node struct {
	ID                int64        `json:"node_id,string"`
	Position          int32        `json:"position"`
	Kind              NodeKind     `json:"kind"`
	DayIndex          int32        `json:"day_index,omitempty"`
	ScheduledTime     string       `json:"scheduled_time,omitempty"`
	TriggerTimeLabel  string       `json:"trigger_time_label,omitempty"`
	ActionTitle       string       `json:"action_title,omitempty"`
	Status            string       `json:"status,omitempty"`
	ScheduleSemantics string       `json:"schedule_semantics,omitempty"`
	MessageText       string       `json:"message_text,omitempty"`
	DelayMinutes      int32        `json:"delay_minutes,omitempty"`
	MaterialRef       string       `json:"material_reference,omitempty"`
	MaterialPlan      MaterialPlan `json:"material_plan"`
}

type MaterialReference struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

type MaterialPlan struct {
	References []MaterialReference `json:"references"`
}

type CapturedMaterialReference struct {
	Kind         string `json:"kind"`
	ID           int64  `json:"id"`
	SourceDigest string `json:"source_digest"`
}

type MaterialSourceSnapshot struct {
	References []CapturedMaterialReference `json:"references"`
	Snapshot   json.RawMessage             `json:"-"`
}

type PreparedMaterial struct {
	Snapshot   json.RawMessage
	Digest     string
	ReadyUntil time.Time
}

const (
	WebhookPathTemplate       = "/api/automation/group-ops/webhooks/{webhook_key}"
	WebhookSignatureAlgorithm = "HMAC-SHA256"
	WebhookSignatureHeader    = "X-AICRM-Signature"
	WebhookTimestampHeader    = "X-AICRM-Timestamp"
	WebhookNonceHeader        = "X-AICRM-Event-Id"
	WebhookClientIDHeader     = "X-AICRM-Client-Id"
	WebhookClientID           = "aicrm-webhook-group-ops"
)

// WebhookDescriptor contains the public integration contract only. It never
// contains a credential, token, signing secret, payload, or provider result.
type WebhookDescriptor struct {
	Configured         bool   `json:"configured"`
	Reference          string `json:"reference,omitempty"`
	Path               string `json:"path,omitempty"`
	URL                string `json:"url,omitempty"`
	SignatureAlgorithm string `json:"signature_algorithm,omitempty"`
	SignatureHeader    string `json:"signature_header,omitempty"`
	TimestampHeader    string `json:"timestamp_header,omitempty"`
	NonceHeader        string `json:"nonce_header,omitempty"`
	ClientIDHeader     string `json:"client_id_header,omitempty"`
	ClientID           string `json:"client_id,omitempty"`
	Description        string `json:"description"`
	Safety
}

type Detail struct {
	Plan              Plan              `json:"plan"`
	Members           []Member          `json:"members"`
	GroupAssets       []GroupAsset      `json:"group_assets"`
	Nodes             []Node            `json:"nodes"`
	WebhookDescriptor WebhookDescriptor `json:"webhook_descriptor"`
	Safety
}

type PlanPage struct {
	Items   []PlanListItem `json:"items"`
	Total   int64          `json:"total"`
	Limit   int32          `json:"limit"`
	Offset  int32          `json:"offset"`
	HasMore bool           `json:"has_more"`
	Safety
}

type MemberPage struct {
	Items   []Member `json:"items"`
	Total   int64    `json:"total"`
	Limit   int32    `json:"limit"`
	Offset  int32    `json:"offset"`
	HasMore bool     `json:"has_more"`
	Safety
}

type GroupAssetPage struct {
	Items   []GroupAsset `json:"items"`
	Total   int64        `json:"total"`
	Limit   int32        `json:"limit"`
	Offset  int32        `json:"offset"`
	HasMore bool         `json:"has_more"`
	Safety
}

type NodePage struct {
	Items   []Node `json:"items"`
	Total   int64  `json:"total"`
	Limit   int32  `json:"limit"`
	Offset  int32  `json:"offset"`
	HasMore bool   `json:"has_more"`
	Safety
}

type ContentValidation struct {
	Valid           bool     `json:"valid"`
	IssueCodes      []string `json:"issue_codes"`
	PreviewLines    []string `json:"preview_lines"`
	NodeCount       int32    `json:"node_count"`
	GroupAssetCount int32    `json:"group_asset_count"`
	Safety
}

type CreatePlanCommand struct {
	Name           string
	Actor          int64
	IdempotencyKey string
}

type UpdatePlanCommand struct {
	PlanType         string
	PlanID           int64
	ExpectedRevision int64
	Name             string
	// OwnerStaffIDSet preserves the update contract for legacy multi-member
	// plans: absent means retain their complete member set. Present replaces it
	// atomically with the standard UI's single responsible employee.
	OwnerStaffID    int64
	OwnerStaffIDSet bool
	Actor           int64
	IdempotencyKey  string
}

type TransitionCommand struct {
	PlanID           int64
	ExpectedRevision int64
	Actor            int64
	IdempotencyKey   string
}

type MemberCommand struct {
	PlanID           int64
	ExpectedRevision int64
	StaffID          int64
	Actor            int64
	IdempotencyKey   string
}

type GroupAssetCommand struct {
	PlanID           int64
	ExpectedRevision int64
	AssetRef         string
	Actor            int64
	IdempotencyKey   string
}

type NodeCreateCommand struct {
	PlanID           int64
	ExpectedRevision int64
	Position         int32
	Kind             NodeKind
	DayIndex         int32
	ScheduledTime    string
	TriggerTimeLabel string
	ActionTitle      string
	Status           string
	MessageText      string
	DelayMinutes     int32
	MaterialRef      string
	MaterialPlan     MaterialPlan
	Actor            int64
	IdempotencyKey   string
}

type NodeUpdateCommand struct {
	PlanID           int64
	NodeID           int64
	ExpectedRevision int64
	Position         int32
	Kind             NodeKind
	DayIndex         int32
	ScheduledTime    string
	TriggerTimeLabel string
	ActionTitle      string
	Status           string
	MessageText      string
	DelayMinutes     int32
	MaterialRef      string
	MaterialPlan     MaterialPlan
	Actor            int64
	IdempotencyKey   string
}

type NodeDeleteCommand struct {
	PlanID           int64
	NodeID           int64
	ExpectedRevision int64
	Actor            int64
	IdempotencyKey   string
}

type WebhookDescriptorCommand struct {
	PlanID           int64
	ExpectedRevision int64
	Reference        string
	Actor            int64
	IdempotencyKey   string
}
