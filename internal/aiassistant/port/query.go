package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type ReviewState string

const (
	ReviewPending    ReviewState = "pending_review"
	ReviewApproved   ReviewState = "approved"
	ReviewRejected   ReviewState = "rejected"
	ReviewIneligible ReviewState = "ineligible"
)

type PlanState string

const (
	PlanPendingReview         PlanState = "pending_review"
	PlanPartiallyApproved     PlanState = "partially_approved"
	PlanApproved              PlanState = "approved"
	PlanRejected              PlanState = "rejected"
	PlanDispatching           PlanState = "dispatching"
	PlanNeedsAttention        PlanState = "needs_attention"
	PlanCompletedWithFailures PlanState = "completed_with_failures"
	PlanCompleted             PlanState = "completed"
)

type ExecutionState string

const (
	ExecutionNotAccepted      ExecutionState = "not_accepted"
	ExecutionAccepted         ExecutionState = "accepted"
	ExecutionQueued           ExecutionState = "queued"
	ExecutionAttempted        ExecutionState = "attempted"
	ExecutionProviderAccepted ExecutionState = "provider_accepted"
	ExecutionRetryableFailed  ExecutionState = "retryable_failed"
	ExecutionOutcomeUnknown   ExecutionState = "outcome_unknown"
	ExecutionReconciled       ExecutionState = "reconciled"
	ExecutionFinalFailed      ExecutionState = "final_failed"
	ExecutionDeliveryProven   ExecutionState = "delivery_proven"
)

type Plan struct {
	ID                  PlanID            `json:"id"`
	Name                string            `json:"name"`
	SourceKind          string            `json:"source_kind"`
	SourceDigest        effectport.Digest `json:"source_digest"`
	State               PlanState         `json:"state"`
	Version             int64             `json:"version"`
	TargetCount         int               `json:"target_count"`
	PendingCount        int               `json:"pending_count"`
	ApprovedCount       int               `json:"approved_count"`
	RejectedCount       int               `json:"rejected_count"`
	IneligibleCount     int               `json:"ineligible_count"`
	NeedsAttentionCount int               `json:"needs_attention_count"`
	CreatedBy           int64             `json:"created_by"`
	CreatedActorKind    string            `json:"-"`
	CreatedActorRef     string            `json:"-"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

type Recipient struct {
	DeferredTarget   *DeferredTarget           `json:"deferred_target,omitempty"`
	ID               RecipientID               `json:"id"`
	PlanID           PlanID                    `json:"plan_id"`
	CustomerID       customerdomain.CustomerID `json:"customer_id"`
	CustomerName     string                    `json:"customer_name"`
	OneIDLabel       string                    `json:"oneid_label"`
	StaffID          int64                     `json:"staff_id"`
	StaffDisplayName string                    `json:"staff_display_name"`
	ReviewState      ReviewState               `json:"review_state"`
	ExecutionState   ExecutionState            `json:"execution_state"`
	Version          int64                     `json:"version"`
	ContentVersionID ContentVersionID          `json:"content_version_id"`
	EffectID         string                    `json:"effect_id,omitempty"`
	UpdatedAt        time.Time                 `json:"updated_at"`
}

type ContentVersion struct {
	ID          ContentVersionID  `json:"id"`
	RecipientID RecipientID       `json:"recipient_id"`
	Version     int64             `json:"version"`
	Digest      effectport.Digest `json:"digest"`
	Blocks      []ContentBlock    `json:"blocks"`
	CreatedAt   time.Time         `json:"created_at"`
}

type PlanListQuery struct {
	Keyword string
	State   PlanState
	Cursor  string
	Limit   int
}

type PlanPage struct {
	Items      []Plan `json:"items"`
	NextCursor string `json:"next_cursor"`
}

type RecipientPageQuery struct {
	PlanID PlanID
	State  ReviewState
	Cursor string
	Limit  int
}

type RecipientPage struct {
	Items      []Recipient `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

type Reader interface {
	ListPlans(context.Context, PlanListQuery) (PlanPage, error)
	GetPlan(context.Context, PlanID) (Plan, error)
	ListRecipients(context.Context, RecipientPageQuery) (RecipientPage, error)
	GetRecipient(context.Context, PlanID, RecipientID) (Recipient, ContentVersion, error)
}

type UpdateContentCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	RecipientID     RecipientID
	ExpectedVersion int64
	Blocks          []ContentBlock
}

type ReviewRecipientCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	RecipientID     RecipientID
	ExpectedVersion int64
	Decision        ReviewState
	Reason          string
}

type PreviewApprovalCommand struct {
	Actor           Actor
	PlanID          PlanID
	ExpectedVersion int64
}

type ApprovalPreview struct {
	PlanID        PlanID            `json:"plan_id"`
	PlanVersion   int64             `json:"plan_version"`
	EligibleCount int               `json:"eligible_count"`
	PreviewDigest effectport.Digest `json:"preview_digest"`
}

type EffectBinding struct {
	RecipientID      RecipientID    `json:"recipient_id"`
	EffectID         string         `json:"effect_id"`
	State            ExecutionState `json:"state"`
	Generation       int64          `json:"generation"`
	Fence            int64          `json:"fence"`
	AttemptCount     int32          `json:"attempt_count"`
	ProviderAccepted bool           `json:"provider_accepted"`
	DeliveryProven   bool           `json:"delivery_proven"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type ApprovePlanCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	ExpectedVersion int64
	PreviewDigest   effectport.Digest
}

type RejectPlanCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	ExpectedVersion int64
	Reason          string
}

type ReconcileEffectCommand struct {
	Actor          Actor
	IdempotencyKey string
	EffectID       string
	Generation     int64
	Fence          int64
	EvidenceDigest effectport.Digest
	Reason         string
}

type Reviewer interface {
	UpdateContent(context.Context, UpdateContentCommand) (ContentVersion, error)
	ReviewRecipient(context.Context, ReviewRecipientCommand) (Recipient, error)
	PreviewApproval(context.Context, PreviewApprovalCommand) (ApprovalPreview, error)
	ApprovePlan(context.Context, ApprovePlanCommand) (Plan, error)
	RejectPlan(context.Context, RejectPlanCommand) (Plan, error)
	ReconcileEffect(context.Context, ReconcileEffectCommand) (Recipient, error)
}

type EffectCompletionProjector interface {
	CompleteExternalEffect(context.Context, string, ExecutionState, bool, bool, effectport.Digest, int32, int64, int64, time.Time) error
}

type OutboundPayloadReader interface {
	LoadOutboundContent(context.Context, string, effectport.Digest) (ContentVersion, error)
}
