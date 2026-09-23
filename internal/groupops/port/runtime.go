package port

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"
)

type RunTrigger string

const (
	RunTriggerDue       RunTrigger = "run_due"
	RunTriggerBroadcast RunTrigger = "broadcast"
	RunTriggerWebhook   RunTrigger = "webhook"
)

type ExecutionState string

const (
	ExecutionAccepted         ExecutionState = "accepted"
	ExecutionProviderAccepted ExecutionState = "provider_accepted"
	ExecutionDeliveryProven   ExecutionState = "delivery_proven"
	ExecutionOutcomeUnknown   ExecutionState = "outcome_unknown"
	ExecutionReconciled       ExecutionState = "reconciled"
	ExecutionFinalFailed      ExecutionState = "final_failed"
)

type RuntimeSafety struct {
	ProviderExecutionEligible bool `json:"provider_execution_eligible"`
	RealExternalCallExecuted  bool `json:"real_external_call_executed"`
	ProviderAccepted          bool `json:"provider_accepted"`
	DeliveryProven            bool `json:"delivery_proven"`
}

func DisabledRuntimeSafety() RuntimeSafety { return RuntimeSafety{} }

// DispatchEnabledRuntimeSafety describes only whether this runtime can accept
// a new dispatch intent. It never asserts that a Provider call occurred.
func DispatchEnabledRuntimeSafety() RuntimeSafety {
	// Local EER acceptance is not Provider eligibility. The current
	// composition deliberately installs the provider-disabled adapter, so this
	// projection must remain false even when the runtime can persist an intent.
	return RuntimeSafety{}
}

type RunDuePreview struct {
	PlanID            int64      `json:"plan_id,string"`
	PlanStatus        PlanStatus `json:"plan_status"`
	SnapshotRevision  int64      `json:"snapshot_revision"`
	EvaluatedAt       time.Time  `json:"evaluated_at"`
	DueExecutionCount int32      `json:"due_execution_count"`
	NextDueAt         *time.Time `json:"next_due_at,omitempty"`
	Blockers          []string   `json:"blockers"`
	RuntimeSafety
}

type Run struct {
	ID                   int64      `json:"run_id,string"`
	PlanID               int64      `json:"plan_id,string"`
	Trigger              RunTrigger `json:"trigger"`
	PlanRevision         int64      `json:"plan_revision"`
	ScheduledFor         time.Time  `json:"scheduled_for"`
	AcceptedAt           time.Time  `json:"accepted_at"`
	AcceptedBy           string     `json:"accepted_by"`
	WebhookPayloadDigest string     `json:"-"`
}

type Execution struct {
	ID                            int64          `json:"execution_id,string"`
	RunID                         int64          `json:"run_id,string"`
	PlanID                        int64          `json:"plan_id,string"`
	PlanRevision                  int64          `json:"plan_revision"`
	NodeID                        int64          `json:"node_id,string"`
	NodePosition                  int32          `json:"node_position"`
	TargetReference               string         `json:"target_reference"`
	TargetDigest                  string         `json:"target_digest"`
	ContentDigest                 string         `json:"content_digest"`
	MaterialDigest                string         `json:"material_digest"`
	ExternalEffectID              string         `json:"external_effect_id"`
	State                         ExecutionState `json:"state"`
	ProviderAccepted              bool           `json:"provider_accepted"`
	DeliveryProven                bool           `json:"delivery_proven"`
	DeliveryStatus                *int           `json:"delivery_status,omitempty"`
	AttemptCount                  int32          `json:"attempt_count"`
	ProviderReceiptPresent        bool           `json:"provider_receipt_present"`
	ReconciliationEvidencePresent bool           `json:"reconciliation_evidence_present"`
	ScheduledFor                  time.Time      `json:"scheduled_for"`
	CreatedAt                     time.Time      `json:"created_at"`
	UpdatedAt                     time.Time      `json:"updated_at"`
}

// ProviderDeliveryReadCommand contains no Provider identifiers. The Group
// Ops receipt freezes msgid and sender before the trusted reader is called.
type ProviderDeliveryReadCommand struct {
	ExecutionID    int64
	ActorID        int64
	IdempotencyKey string
}

type ExecutionIntentState string

const (
	ExecutionIntentWaitingForPredecessor ExecutionIntentState = "waiting"
	ExecutionIntentMaterialPending       ExecutionIntentState = "material_pending"
	ExecutionIntentReadyToAccept         ExecutionIntentState = "ready_to_accept"
	ExecutionIntentAccepted              ExecutionIntentState = "accepted"
	ExecutionIntentFinalFailed           ExecutionIntentState = "final_failed"
)

type ExecutionIntent struct {
	ID               int64                `json:"intent_id,string"`
	ExternalEffectID string               `json:"external_effect_id,omitempty"`
	NodeID           int64                `json:"node_id,string"`
	NodePosition     int32                `json:"node_position"`
	TargetReference  string               `json:"target_reference"`
	ScheduledFor     time.Time            `json:"scheduled_for"`
	State            ExecutionIntentState `json:"state"`
	ManualBlocker    bool                 `json:"manual_blocker"`
}

type RunSummary struct {
	Run              Run               `json:"run"`
	Executions       []Execution       `json:"executions"`
	Accepted         int32             `json:"accepted"`
	ProviderAccepted int32             `json:"provider_accepted_count"`
	DeliveryProven   int32             `json:"delivery_proven_count"`
	OutcomeUnknown   int32             `json:"outcome_unknown"`
	Reconciled       int32             `json:"reconciled"`
	FinalFailed      int32             `json:"final_failed"`
	MaterialPending  int32             `json:"material_pending_count"`
	PendingIntents   []ExecutionIntent `json:"pending_intents"`
	RuntimeSafety
}

type ExecutionPage struct {
	Items   []Execution `json:"items"`
	Total   int64       `json:"total"`
	Limit   int32       `json:"limit"`
	Offset  int32       `json:"offset"`
	HasMore bool        `json:"has_more"`
	RuntimeSafety
}

type GroupDirectoryItem struct {
	ExternalMemberCount *int32    `json:"external_member_count"`
	ChatReference       string    `json:"chat_reference"`
	OwnerStaffID        int64     `json:"owner_staff_id"`
	DisplayName         string    `json:"display_name"`
	MemberCount         int32     `json:"member_count"`
	RefreshedAt         time.Time `json:"refreshed_at"`
}

type GroupDirectoryPage struct {
	CatalogSync *CatalogStatus       `json:"catalog_sync,omitempty"`
	Items       []GroupDirectoryItem `json:"items"`
	Total       int64                `json:"total"`
	Limit       int32                `json:"limit"`
	Offset      int32                `json:"offset"`
	HasMore     bool                 `json:"has_more"`
	RuntimeSafety
}

type OperationMember struct {
	StaffID              int64      `json:"staff_id,omitempty"`
	SenderUserID         string     `json:"sender_userid"`
	DisplayName          string     `json:"display_name"`
	Active               bool       `json:"active"`
	NameSource           string     `json:"name_source,omitempty"`
	ProfileReadState     string     `json:"profile_read_state,omitempty"`
	ProfileReadErrorCode string     `json:"profile_read_error_code,omitempty"`
	ProfileRefreshedAt   *time.Time `json:"profile_refreshed_at,omitempty"`
}

type OperationMemberPage struct {
	Scope                string            `json:"scope"`
	Items                []OperationMember `json:"items"`
	PageSize             int32             `json:"page_size"`
	ProfileReadState     string            `json:"profile_read_state,omitempty"`
	ProfileReadErrorCode string            `json:"profile_read_error_code,omitempty"`
	RuntimeSafety
}

type RunDueCommand struct {
	PlanID         int64
	ActorID        int64
	IdempotencyKey string
}

type AcceptPlanCommand struct {
	PlanID         int64
	Trigger        RunTrigger
	AcceptedBy     string
	IdempotencyKey string
}

// WebhookInboundCommand is the strict, signed request body accepted by a
// Webhook plan. Group references are the plan-bound opaque identifiers, never
// display names or provider chat IDs supplied ad hoc by a caller.
type WebhookInboundCommand struct {
	WebhookReference     string           `json:"webhook_reference"`
	TargetChatReferences []string         `json:"target_chat_references"`
	Messages             []WebhookMessage `json:"messages"`
}

// WebhookMessage describes the single WeCom-representable message shape:
// optional text first, followed by ordered Media-backed attachments. Images
// and files use existing local Media IDs. A miniprogram is resolved by a
// Media-owned adapter from its AppID/path before Group Ops freezes the local
// material reference.
type WebhookMessage struct {
	Type          string `json:"type"`
	Text          string `json:"text,omitempty"`
	AppID         string `json:"appid,omitempty"`
	Path          string `json:"path,omitempty"`
	Title         string `json:"title,omitempty"`
	MiniProgramID int64  `json:"miniprogram_id,omitempty"`
	ImageID       int64  `json:"image_id,omitempty"`
	AttachmentID  int64  `json:"attachment_id,omitempty"`
}

type ManualReconcileCommand struct {
	ExecutionID    int64
	ActorID        int64
	IdempotencyKey string
	Generation     int64
	Fence          int64
	LeaseExpiresAt time.Time
	EvidenceDigest string
	DeliveryProven bool
}

type ExternalReconcileCommand struct {
	EffectID       string
	ReceiptKey     string
	EvidenceDigest string
	ActorID        int64
	Generation     int64
	Fence          int64
	LeaseExpiresAt time.Time
}

// ExternalReconciler is a composition adapter around the EER control port.
// Group Ops never imports the EER store or its HTTP/worker packages.
type ExternalReconciler interface {
	ReconcileExternalEffect(context.Context, ExternalReconcileCommand) error
}

type ExecutionOutcomeCommand struct {
	ExecutionID           int64
	State                 ExecutionState
	ProviderAccepted      bool
	DeliveryProven        bool
	ProviderReceiptDigest string
	AttemptCount          int32
}

type GroupRefreshCommand struct {
	OwnerStaffID   int64
	ActorID        int64
	Limit          int32
	IdempotencyKey string
}

type OperationMemberRefreshCommand struct {
	ActorID        int64
	PageSize       int32
	IdempotencyKey string
}

type GroupDirectorySource interface {
	ListOwnedGroups(context.Context, int64, int32) (GroupDirectorySnapshot, error)
	RefreshOperationMembers(context.Context, int32) ([]OperationMember, error)
}

// WebhookReplayStore is an owner-local replay claim. Implementations store
// only opaque event/payload digests and must not persist signatures or bodies.
type WebhookReplayStore interface {
	ClaimWebhookReplay(context.Context, string, string, [sha256.Size]byte, [sha256.Size]byte, time.Time) (bool, error)
}

// GroupDirectorySnapshot may replace a local owner projection only when
// Complete is true. A partial provider page is never a deletion authority.
type GroupDirectorySnapshot struct {
	Items    []GroupDirectoryItem
	Complete bool
}

// ExecutionSenderResolver freezes the verified active owner of one local
// group target. It never chooses a plan member or process-wide default.
type ExecutionSenderResolver interface {
	ResolveExecutionSender(context.Context, string) (string, bool, error)
}

// MaterialSnapshotResolver is the Group Ops side of the Media boundary. It
// accepts the persisted ordered material plan and returns the immutable
// provider-shaped snapshot plus its content digest. The implementation is
// supplied by Composition through Media's SourceCapturer/Freezer ports; the
// Group Ops store never reads Media tables or resolves mutable references.
type MaterialSnapshotResolver interface {
	ResolveMaterialSnapshot(context.Context, MaterialPlan, time.Time) (json.RawMessage, string, error)
}

type MaterialIntentSnapshotResolver interface {
	MaterialSnapshotResolver
	ResolveMaterialIntentSnapshot(context.Context, MaterialPlan, time.Time) (json.RawMessage, string, json.RawMessage, string, error)
}

// ExecutionTargetOwnerResolver is the Group Ops side of sender resolution.
// It returns only the local staff owner of an opaque group target; the
// Composition Root combines it with the Access staff port to obtain an
// active sender ID. This keeps the Group Ops store from reading admin_users.
type ExecutionTargetOwnerResolver interface {
	ResolveExecutionOwner(context.Context, string) (int64, bool, error)
}

// Runtime persistence records are owned by Group Ops.  They live in the
// stable port so the application can coordinate plan snapshots and EER
// acceptance without importing the PostgreSQL store package.
type ExecutionKey struct {
	NodeID          int64
	TargetReference string
}

type RunReservation struct {
	PlanID               int64
	Trigger              RunTrigger
	SourceKeyDigest      [sha256.Size]byte
	WebhookPayloadDigest string
	PlanRevision         int64
	ScheduledFor         time.Time
	AcceptedAt           time.Time
	AcceptedBy           string
}

type ExecutionDraft struct {
	RunID, PlanID, PlanRevision, NodeID int64
	NodePosition                        int32
	TargetReference, SenderUserID       string
	TargetDigest                        string
	ContentSnapshot                     json.RawMessage
	ContentDigest                       string
	MaterialSnapshot                    json.RawMessage
	MaterialDigest                      string
	MaterialSourceSnapshot              json.RawMessage
	MaterialSourceDigest                string
	ExecutionKeyDigest                  [sha256.Size]byte
	ExternalEffectID                    string
	IntentID                            int64
	ScheduledFor, CreatedAt             time.Time
}

// RuntimeStore is the minimum local persistence surface needed by the
// runtime. Implementations must join the caller's PostgreSQL transaction for
// ReserveRun/InsertExecution and outcome projection.
type RuntimeStore interface {
	ListExecutionKeys(context.Context, int64, int64) ([]ExecutionKey, error)
	FindRunBySourceKey(context.Context, int64, RunTrigger, [sha256.Size]byte) (Run, bool, error)
	ReserveRun(context.Context, RunReservation) (Run, error)
	CreateExecutionIntents(context.Context, []ExecutionDraft) ([]ExecutionIntent, error)
	InitialExecutionIntents(context.Context, int64) ([]ExecutionDraft, error)
	ClaimNextExecutionIntent(context.Context, string) (ExecutionDraft, bool, error)
	HaltExecutionIntent(context.Context, int64) error
	BindAcceptedExecutionIntent(context.Context, int64, string) error
	InsertExecution(context.Context, ExecutionDraft) (Execution, error)
	GetExecution(context.Context, int64) (Execution, error)
	ListExecutions(context.Context, int64, int32, int32) ([]Execution, int64, error)
	ReadRunSummary(context.Context, int64) (RunSummary, error)
	RecordExecutionOutcome(context.Context, int64, ExecutionState, bool, bool, string, int32, time.Time) (Execution, error)
	ReconcileExecution(context.Context, int64, string, bool, time.Time) (Execution, error)
	RecordGroupMessageDelivery(context.Context, GroupMessageReceipt, string) error
	FindPlanByWebhookReference(context.Context, string) (int64, error)
	// ListDirectoryGroups is a local authorised projection read. `query` is a
	// bounded plain-text group name/reference search; callers must not use it to
	// infer or resolve a customer identity.
	ListDirectoryGroups(context.Context, int64, string, int32, int32) ([]GroupDirectoryItem, int64, error)
	ReplaceDirectoryGroups(context.Context, int64, []GroupDirectoryItem, time.Time) error
	RecordDirectoryRefresh(context.Context, string, int64, int64, [sha256.Size]byte, string, int32, bool, time.Time) error
	CompleteEffect(context.Context, string, ExecutionState, bool, bool, string, int32, time.Time) error
}

// EligibleStaffReader is a local employee projection used by the operation
// members picker. It has no customer or external-recipient semantics.
type EligibleStaffReader interface {
	ListEligibleStaff(context.Context) ([]OperationMember, error)
}

// OperationMemberDirectoryStore is the Group Ops-owned display projection for
// the already-authorized operation-member set. It deliberately contains no
// Access roles or identity records: Access continues to own staff activation
// and sender bindings, while WeCom profile reads only enrich this presentation
// data after the follow-user list has established eligibility.
type OperationMemberDirectoryStore interface {
	ListOperationMemberDirectory(context.Context) ([]OperationMember, error)
	ReplaceOperationMemberDirectory(context.Context, []OperationMember, time.Time) error
}

// ExecutionContinuationEnqueuer inserts a unique, effect-bound shared River
// job in the caller's current transaction. It owns no retry or lease state.
type ExecutionContinuationEnqueuer interface {
	EnqueueGroupOpsContinuationWithin(context.Context, string) error
}
