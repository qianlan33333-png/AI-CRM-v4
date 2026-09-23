// Package domain contains payment and external-result state values.
package domain

type EffectState string

const (
	EffectAccepted        EffectState = "accepted"
	EffectQueued          EffectState = "queued"
	EffectAttempted       EffectState = "attempted"
	EffectExecuted        EffectState = "executed"
	EffectOutcomeUnknown  EffectState = "outcome_unknown"
	EffectRetryableFailed EffectState = "retryable_failed"
	EffectFinalFailed     EffectState = "final_failed"
	EffectReconciled      EffectState = "reconciled"
)

func (state EffectState) Valid() bool {
	switch state {
	case EffectAccepted, EffectQueued, EffectAttempted, EffectExecuted, EffectOutcomeUnknown, EffectRetryableFailed, EffectFinalFailed, EffectReconciled:
		return true
	default:
		return false
	}
}

type Status string

const (
	StatusAwaitingPrepay  Status = "awaiting_prepay"
	StatusAwaitingPayment Status = "awaiting_payment"
	StatusPaid            Status = "paid"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
)

type RefundStatus string

const (
	RefundRequested      RefundStatus = "requested"
	RefundEffectAccepted RefundStatus = "effect_accepted"
	RefundOutcomeUnknown RefundStatus = "outcome_unknown"
	RefundCompleted      RefundStatus = "completed"
	RefundFinalFailed    RefundStatus = "final_failed"
)

// Historical non-success states are frozen evidence; they are never executable.
const (
	RefundHistoryRequested  RefundStatus = "history_requested"
	RefundHistoryProcessing RefundStatus = "history_processing"
	RefundHistoryFailed     RefundStatus = "history_failed"
	RefundHistoryClosed     RefundStatus = "history_closed"
)

func (s RefundStatus) HistoricalImportable() bool {
	return s == RefundCompleted || s == RefundHistoryRequested || s == RefundHistoryProcessing || s == RefundHistoryFailed || s == RefundHistoryClosed
}
