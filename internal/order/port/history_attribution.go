package port

import (
	"context"
	"crypto/sha256"
	"time"
)

type HistoricalAttributionOutcome string

const (
	AttributionLinked                  HistoricalAttributionOutcome = "linked"
	AttributionAlreadyLinked           HistoricalAttributionOutcome = "already_linked"
	AttributionSourceIdentityMissing   HistoricalAttributionOutcome = "source_identity_missing"
	AttributionSourceIdentityNotFound  HistoricalAttributionOutcome = "source_identity_not_found"
	AttributionSourceIdentityAmbiguous HistoricalAttributionOutcome = "source_external_identity_ambiguous"
	AttributionTargetIdentityNotFound  HistoricalAttributionOutcome = "target_identity_not_found"
	AttributionTargetIdentityConflict  HistoricalAttributionOutcome = "target_identity_conflict"
	AttributionOrderNotFound           HistoricalAttributionOutcome = "order_not_found"
	AttributionOrderReferenceConflict  HistoricalAttributionOutcome = "order_reference_conflict"
	AttributionOrderPayerConflict      HistoricalAttributionOutcome = "order_payer_conflict"
)

type HistoricalAttributionCommand struct {
	RunID           int64
	SourceKey       string
	OrderReference  string
	EvidenceDigest  [sha256.Size]byte
	Outcome         HistoricalAttributionOutcome
	PayerCustomerID int64
	PayerIdentityID int64
	OccurredAt      time.Time
}

type HistoricalAttributionResult struct {
	Outcome         HistoricalAttributionOutcome `json:"outcome"`
	OrderID         int64                        `json:"order_id,omitempty"`
	PayerCustomerID int64                        `json:"payer_customer_id,omitempty"`
	PayerIdentityID int64                        `json:"payer_identity_id,omitempty"`
	Replayed        bool                         `json:"replayed"`
}

// HistoricalAttributionWriter is transaction-bound. It updates only the
// payer of a history/effect-ineligible Order and records the receipt, audit and
// outbox in the same PostgreSQL Unit of Work.
type HistoricalAttributionWriter interface {
	RecordHistoricalAttributionWithin(context.Context, HistoricalAttributionCommand) (HistoricalAttributionResult, error)
}
