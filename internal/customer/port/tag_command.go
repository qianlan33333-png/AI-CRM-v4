package port

import (
	"context"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type TagMutation string

const (
	TagAdd    TagMutation = "add"
	TagRemove TagMutation = "remove"
)

var (
	ErrTagCommandInvalid     = errors.New("customer tag command invalid")
	ErrTagCommandUnavailable = errors.New("customer tag command unavailable")
	ErrTagCommandConflict    = errors.New("customer tag command conflict")
)

// TagCommandTarget has one canonical customer and the complete mark_tag
// add/remove set. A command therefore makes at most one WeCom mark_tag call per
// customer. Tag IDs are local catalog IDs; provider identifiers never enter it.
type TagCommandTarget struct {
	CustomerID   customerdomain.CustomerID
	StaffID      int64
	AddTagIDs    []int64
	RemoveTagIDs []int64
}

// FrozenTagCommandTarget is returned by a trusted Composition adapter while the
// command transaction is open. BindingDigest commits the exact local-tag to
// provider-tag mapping selected at acceptance; the dispatch adapter must refuse
// a changed mapping rather than silently retargeting a pending effect.
type FrozenTagCommandTarget struct {
	TagCommandTarget
	BindingDigest string
	TargetDigest  string
}

type TagCommand struct {
	ActorAdminUserID int64
	Source           string
	SourceRef        string
	IdempotencyKey   string
	Targets          []TagCommandTarget
	OccurredAt       time.Time
}

type TagCommandLine struct {
	ID               int64                     `json:"id"`
	CustomerID       customerdomain.CustomerID `json:"customer_id"`
	StaffID          int64                     `json:"staff_id"`
	AddTagIDs        []int64                   `json:"add_tag_ids"`
	RemoveTagIDs     []int64                   `json:"remove_tag_ids"`
	BindingDigest    string                    `json:"binding_digest,omitempty"`
	TargetDigest     string                    `json:"target_digest,omitempty"`
	EffectRef        string                    `json:"effect_ref,omitempty"`
	AcceptReceiptRef string                    `json:"accept_receipt_ref,omitempty"`
	QueueReceiptRef  string                    `json:"queue_receipt_ref,omitempty"`
	State            string                    `json:"state"`
	RejectReason     string                    `json:"reject_reason,omitempty"`
	// ResultReason is a fixed, safe outcome code. It never contains a Provider response, identifier, or transport error.
	ResultReason string `json:"result_reason,omitempty"`
}

type TagCommandResult struct {
	ID         int64            `json:"id"`
	Source     string           `json:"source,omitempty"`
	State      string           `json:"state"`
	OccurredAt time.Time        `json:"occurred_at,omitempty"`
	UpdatedAt  time.Time        `json:"updated_at,omitempty"`
	Lines      []TagCommandLine `json:"lines"`
}

type TagCommandSubmitter interface {
	SubmitTagCommand(context.Context, TagCommand) (TagCommandResult, error)
	SubmitTagCommandWithin(context.Context, TagCommand) (TagCommandResult, error)
}

// TagCommandPreviewer only reads trusted eligibility at preview time. Confirm
// always freezes again during acceptance, so a preview never authorizes a send.
type TagCommandPreviewer interface {
	PreviewTagCommand(context.Context, TagCommand) (TagCommandResult, error)
}

// TagCommandHistoryReader exposes durable local command observations; it has no
// Provider payload or raw WeCom identifiers.
type TagCommandHistoryReader interface {
	ListTagCommands(context.Context, customerdomain.CustomerID, int) ([]TagCommandResult, error)
}

type TagCommandDispatch struct {
	EffectRef     string
	Source        string
	CustomerID    customerdomain.CustomerID
	StaffID       int64
	AddTagIDs     []int64
	RemoveTagIDs  []int64
	BindingDigest string
	TargetDigest  string
}

type TagCommandDispatchReader interface {
	ReadTagCommandDispatch(context.Context, string) (TagCommandDispatch, error)
}

type TagCommandCompletion struct {
	EffectRef, State, ResultDigest string
	ResultReason                   string
	Attempt                        int32
	Generation, Fence              int64
	CompletedAt                    time.Time
}
type TagCommandCompletionWriter interface {
	CompleteTagCommand(context.Context, TagCommandCompletion) error
}

// TagCommandTargetGate only exposes local IDs and a binding digest to Customer.
// It selects an eligible following staff, verifies the trusted identity and
// active Tag binding, and produces a frozen fact without exposing provider IDs.
type TagCommandTargetGate interface {
	FreezeTagCommandTarget(context.Context, TagCommandTarget) (FrozenTagCommandTarget, error)
}
