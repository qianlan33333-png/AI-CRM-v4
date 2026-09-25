package port

import (
	"context"
	"errors"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var ErrInvalidMachineActor = errors.New("invalid AI Assistant machine actor")

// MachineActor is the canonical subject for an authenticated Open client. It
// deliberately carries no compatibility staff/admin ID: only a validated
// machine:<client_id> reference may identify a machine caller.
type MachineActor struct {
	Kind      string
	Reference string
	StaffID   int64
}

const MachineActorKind = "machine"

// MachineActorFromAuthenticatedPrincipal accepts the exact subject produced by
// Access after authentication. It does not resolve or map that client to a
// human account.
func MachineActorFromAuthenticatedPrincipal(reference string) (MachineActor, error) {
	if !strings.HasPrefix(reference, "machine:") {
		return MachineActor{}, ErrInvalidMachineActor
	}
	return MachineActorFromClientID(strings.TrimPrefix(reference, "machine:"))
}

func MachineActorFromClientID(clientID string) (MachineActor, error) {
	if !validMachineClientID(clientID) {
		return MachineActor{}, ErrInvalidMachineActor
	}
	return MachineActor{Kind: MachineActorKind, Reference: "machine:" + clientID}, nil
}

func (a MachineActor) Valid() bool {
	return a.Kind == MachineActorKind && a.StaffID == 0 && strings.HasPrefix(a.Reference, "machine:") && validMachineClientID(strings.TrimPrefix(a.Reference, "machine:"))
}

func validMachineClientID(clientID string) bool {
	if clientID == "" || len(clientID) > 160 || strings.TrimSpace(clientID) != clientID {
		return false
	}
	for _, value := range clientID {
		if !(value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '-' || value == '_' || value == '.') {
			return false
		}
	}
	return true
}

// MachineCreatePlanCommand is distinct from the legacy human command so that
// adding a machine subject cannot alter existing human receipt serialization.
type MachineCreatePlanCommand struct {
	Actor          MachineActor            `json:"-"`
	IdempotencyKey string                  `json:"-"`
	Name           string                  `json:"name"`
	SourceKind     string                  `json:"source_kind"`
	SourceDigest   effectport.Digest       `json:"source_digest"`
	Recipients     []RecipientCandidate    `json:"recipients"`
	Package        *MachinePackageMetadata `json:"package,omitempty"`
	OccurredAt     time.Time               `json:"-"`
}

func (c MachineCreatePlanCommand) Valid() bool {
	if !c.Actor.Valid() || len(c.IdempotencyKey) < 8 || len(c.IdempotencyKey) > 200 || strings.TrimSpace(c.IdempotencyKey) != c.IdempotencyKey || strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 || strings.TrimSpace(c.SourceKind) == "" || len(c.SourceKind) > 80 || !effectport.ValidDigest(c.SourceDigest) || len(c.Recipients) == 0 || len(c.Recipients) > MaxRecipients || c.OccurredAt.IsZero() {
		return false
	}
	for _, recipient := range c.Recipients {
		if !recipient.Valid() {
			return false
		}
	}
	if c.SourceKind == "scrm_workbench" {
		if c.Package == nil || !c.Package.Valid() || c.Package.MemberCount != len(c.Recipients) || len(c.Recipients) > 10 {
			return false
		}
	} else if c.Package != nil {
		return false
	}
	return true
}

// Metadata is frozen with the plan and contributes to the existing machine
// idempotency digest. It contains no provider identity or message body.
type MachinePackageMetadata struct {
	AudiencePackageID  string `json:"audience_package_id"`
	AudienceVersion    string `json:"audience_version"`
	CopyPackageID      string `json:"copy_package_id"`
	CopyVersion        string `json:"copy_version"`
	StrategyVersion    string `json:"strategy_version"`
	ProductFactVersion string `json:"product_fact_version"`
	SourceFingerprint  string `json:"source_fingerprint"`
	ApprovalRevision   string `json:"approval_revision"`
	ClientReference    string `json:"client_reference"`
	MemberCount        int    `json:"member_count"`
}

func (m MachinePackageMetadata) Valid() bool {
	for _, value := range []string{m.AudiencePackageID, m.AudienceVersion, m.CopyPackageID, m.CopyVersion, m.StrategyVersion, m.ProductFactVersion, m.ApprovalRevision, m.ClientReference} {
		if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
			return false
		}
	}
	return effectport.ValidDigest(effectport.Digest(m.SourceFingerprint)) && m.MemberCount >= 1 && m.MemberCount <= 10
}

// MachinePlan exposes review progress only. It intentionally does not expose
// delivery/execution state, because an approved review is not Provider proof.
type MachinePlan struct {
	ID              PlanID      `json:"id"`
	ReviewState     ReviewState `json:"review_state"`
	Version         int64       `json:"version"`
	TargetCount     int         `json:"target_count"`
	PendingCount    int         `json:"pending_count"`
	ApprovedCount   int         `json:"approved_count"`
	RejectedCount   int         `json:"rejected_count"`
	IneligibleCount int         `json:"ineligible_count"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

// MachineOperationState describes the plan's actual review or execution
// phase. It does not claim Provider delivery proof: that remains a
// recipient-level External Effects fact.
type MachineOperationState string

const (
	MachineOperationPendingReview     MachineOperationState = "pending_review"
	MachineOperationPartiallyApproved MachineOperationState = "partially_approved"
	MachineOperationApproved          MachineOperationState = "approved"
	MachineOperationRejected          MachineOperationState = "rejected"
	MachineOperationDispatching       MachineOperationState = "dispatching"
	MachineOperationNeedsAttention    MachineOperationState = "needs_attention"
	MachineOperationOutcomeUnknown    MachineOperationState = "outcome_unknown"
	MachineOperationCompletedFailures MachineOperationState = "completed_with_failures"
	MachineOperationCompleted         MachineOperationState = "completed"
)

// MachineExecutionSummary retains the only execution facts needed to avoid
// collapsing an unknown Provider outcome into a generic attention state.
// It is read inside the same UoW as the creator-scoped plan projection.
type MachineExecutionSummary struct {
	OutcomeUnknownCount   int
	RetryableFailureCount int
}

type MachineRecipientResult struct {
	RecipientID      int64
	CustomerID       int64
	ReviewState      string
	ExecutionState   string
	ProviderAccepted bool
	DeliveryProven   bool
	EffectID         string
	UpdatedAt        time.Time
}

// MachineOperationStatus keeps review approval distinct from execution. For
// example, an approved plan with no queued recipient remains "approved", not
// "completed"; an outcome_unknown recipient is surfaced explicitly.
type MachineOperationStatus struct {
	PlanID                PlanID                `json:"plan_id"`
	ReviewState           ReviewState           `json:"review_state"`
	OperationState        MachineOperationState `json:"operation_state"`
	Version               int64                 `json:"version"`
	TargetCount           int                   `json:"target_count"`
	OutcomeUnknownCount   int                   `json:"outcome_unknown_count"`
	RetryableFailureCount int                   `json:"retryable_failure_count"`
	CreatedAt             time.Time             `json:"created_at"`
	UpdatedAt             time.Time             `json:"updated_at"`
}

type MachineCreatePlanResult struct {
	Plan     MachinePlan `json:"plan"`
	Replayed bool        `json:"replayed"`
}

type MachineIntake interface {
	CreateMachinePlan(context.Context, MachineCreatePlanCommand) (MachineCreatePlanResult, error)
}

// MachineTransactionalIntake shares the caller's already-bound PostgreSQL
// transaction. It never opens a nested Unit of Work.
type MachineTransactionalIntake interface {
	CreateMachinePlanWithin(context.Context, MachineCreatePlanCommand) (MachineCreatePlanResult, error)
}

type MachineReader interface {
	GetMachinePlan(context.Context, MachineActor, PlanID) (MachinePlan, error)
	GetMachineOperationStatus(context.Context, MachineActor, PlanID) (MachineOperationStatus, error)
	GetMachineRecipientResults(context.Context, MachineActor, PlanID) ([]MachineRecipientResult, error)
}

// MachineEvent is persisted through the same atomic audit/outbox boundary as
// a human plan event, with the machine reference retained for attribution.
type MachineEvent struct {
	Type           string
	AggregateID    PlanID
	RecipientID    RecipientID
	Actor          MachineActor
	IdempotencyKey string
	Payload        []byte
	OccurredAt     time.Time
}
