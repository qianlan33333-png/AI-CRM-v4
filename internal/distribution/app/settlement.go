package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/riverqueue/river"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const (
	dueCheckRetryDelay   = 15 * time.Minute
	settlementQueryDelay = 5 * time.Minute
)

type settlementStore interface {
	ReadSettlementContextWithin(context.Context, int64) (distributionstore.SettlementContext, error)
	ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	ReadLatestSettlementByCommissionWithin(context.Context, int64, bool) (distributionstore.Settlement, error)
	InsertSettlementWithin(context.Context, distributionstore.Settlement) (distributionstore.Settlement, bool, error)
	AcceptSettlementWithin(context.Context, distributionstore.Settlement, int64) (distributionstore.Settlement, error)
	UpdateSettlementStateWithin(context.Context, distributionstore.Settlement, int64) (distributionstore.Settlement, error)
	UpdateCommissionWithin(context.Context, distributiondomain.Commission, int64) (distributiondomain.Commission, error)
	AppendCommissionAdjustmentWithin(context.Context, distributionstore.CommissionAdjustment) error
	InsertExceptionWithin(context.Context, distributionstore.Exception) (distributionstore.Exception, error)
	FindOpenExceptionWithin(context.Context, int64, string) (distributionstore.Exception, error)
	FindExceptionByEvidenceWithin(context.Context, int64, string, string) (distributionstore.Exception, error)
	HasExceptionWithReasonWithin(context.Context, int64, string, string) (bool, error)
	ResolveOpenBusinessExceptionsWithin(context.Context, int64, time.Time) ([]distributionstore.Exception, error)
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// SettlementService owns the durable due-check state machine. It uses Payment
// only through the stable Port: provider reconciliation happens before this
// service opens a write transaction, while the resulting reservation and EER
// instruction are accepted in the same UoW as Distribution's state changes.
type SettlementService struct {
	uow           platformport.UnitOfWork
	store         settlementStore
	qualification *QualificationService
	payment       paymentport.DistributionSettlementPort
	now           func() time.Time
}

func NewSettlementService(uow platformport.UnitOfWork, store settlementStore, qualification *QualificationService, payment paymentport.DistributionSettlementPort) (*SettlementService, error) {
	if uow == nil || store == nil || qualification == nil || payment == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &SettlementService{uow: uow, store: store, qualification: qualification, payment: payment, now: time.Now}, nil
}

// RunCommissionDueCheck is safe to replay. It either creates one immutable
// Payment instruction, reconciles that exact instruction, or leaves a durable
// hold/exception for a later authoritative retry. It never switches provider
// order numbers after an outcome is unknown.
func (s *SettlementService) RunCommissionDueCheck(ctx context.Context, commissionID int64) error {
	if s == nil || s.uow == nil || s.store == nil || s.qualification == nil || s.payment == nil || commissionID < 1 {
		return distributionport.ErrUnavailable
	}
	initial, settlement, err := s.load(ctx, commissionID)
	if err != nil {
		return err
	}
	if initial.Commission.PaidMinor == 0 && deadlineImminent(settlement.DeadlineAt, s.now().UTC()) {
		if err = s.recordDeadlineWarning(ctx, initial, settlement, *settlement.DeadlineAt); err != nil {
			return err
		}
	}
	switch initial.Commission.Status {
	case distributiondomain.CommissionPaid, distributiondomain.CommissionCancelled:
		return s.releaseRemaining(ctx, initial, settlement)
	case distributiondomain.CommissionZero:
		return nil
	case distributiondomain.CommissionSettling, distributiondomain.CommissionException:
		if settlement.InstructionReference == "" {
			// A deadline or pre-submit exception has no provider operation to
			// query. It remains a durable manual-payable exception, not an
			// endlessly retried pseudo-reconciliation.
			return nil
		}
		// This is the only provider-network operation in this worker, and it is
		// deliberately outside the following Distribution transaction.
		instruction, reconcileErr := s.payment.ReconcileProfitSharing(ctx, settlement.InstructionReference)
		if reconcileErr != nil {
			return river.JobSnooze(settlementQueryDelay)
		}
		return s.applyReconciliation(ctx, commissionID, instruction)
	}

	// Payment's first snapshot is read outside the write UoW. Accepting the
	// split below repeats Payment's own authoritative reserve check inside its
	// UoW, so this display/preflight value never authorizes a stale payment.
	state, err := s.payment.DistributionPaymentState(ctx, initial.Commission.OrderID)
	if err != nil {
		return river.JobSnooze(dueCheckRetryDelay)
	}
	return s.prepareSettlement(ctx, commissionID, state)
}

func (s *SettlementService) load(ctx context.Context, commissionID int64) (distributionstore.SettlementContext, distributionstore.Settlement, error) {
	var contextValue distributionstore.SettlementContext
	var settlement distributionstore.Settlement
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		contextValue, err = s.store.ReadSettlementContextWithin(tx, commissionID)
		if err != nil {
			return err
		}
		settlement, err = s.store.ReadLatestSettlementByCommissionWithin(tx, commissionID, false)
		if errors.Is(err, distributionport.ErrNotFound) {
			settlement = distributionstore.Settlement{}
			return nil
		}
		return err
	})
	return contextValue, settlement, err
}

func (s *SettlementService) prepareSettlement(ctx context.Context, commissionID int64, state paymentport.DistributionPaymentState) error {
	now := s.now().UTC()
	var retry bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		value, err := s.store.ReadSettlementContextWithin(tx, commissionID)
		if err != nil {
			return err
		}
		commission := value.Commission
		if commission.Status != distributiondomain.CommissionPending && commission.Status != distributiondomain.CommissionHeld {
			return nil
		}
		if now.Before(commission.DueAt) {
			if warningErr := s.recordDeadlineWarningWithin(tx, commission, distributionstore.Settlement{}, state.DeadlineAt, state.OriginalPaymentRef, now); warningErr != nil {
				return warningErr
			}
			retry = true
			return nil
		}
		if warningErr := s.recordDeadlineWarningWithin(tx, commission, distributionstore.Settlement{}, state.DeadlineAt, state.OriginalPaymentRef, now); warningErr != nil {
			return warningErr
		}
		if !state.ConfirmedPaid || state.OriginalPaymentRef == "" || state.RefundExposure {
			return s.holdWithin(tx, commission, "payment_or_refund_pending", now)
		}
		if state.SuccessfulRefundMinor > 0 {
			next, repriceErr := commission.RepriceFromPaidMinor(commission.Version, commission.OriginalItemPaidMinor, state.SuccessfulRefundMinor, now)
			if repriceErr != nil {
				return distributionport.ErrConflict
			}
			if next.CurrentPayableMinor != commission.CurrentPayableMinor {
				delta := next.CurrentPayableMinor - commission.CurrentPayableMinor
				if err = s.store.AppendCommissionAdjustmentWithin(tx, distributionstore.CommissionAdjustment{CommissionID: commission.ID, Kind: "buyer_refund", DeltaMinor: delta, ResultingPayableMinor: next.CurrentPayableMinor, Reason: "buyer_refund", SourceRef: state.OriginalPaymentRef, OccurredAt: now}); err != nil {
					return err
				}
			}
			commission, err = s.store.UpdateCommissionWithin(tx, next, value.Commission.Version)
			if err != nil {
				return err
			}
			if commission.Status == distributiondomain.CommissionCancelled {
				return s.auditWithin(tx, "distribution.commission_cancelled.v1", "commission", commission.ID, "worker:distribution-due", map[string]any{"reason": "buyer_refund", "payment_reference": state.OriginalPaymentRef}, now)
			}
		}
		distributor, receiver, err := s.store.ReadDistributorWithin(tx, commission.DistributorID, true)
		if err != nil {
			return err
		}
		qualification, err := s.qualification.CheckWithin(tx, distributor.CustomerID, value.ProductID, value.ProductType)
		if err != nil || qualification.State == distributiondomain.QualificationUnavailable || qualification.State == distributiondomain.QualificationSuspended || qualification.State == distributiondomain.QualificationConflict {
			return s.holdWithin(tx, commission, "qualification_unavailable", now)
		}
		if qualification.State == distributiondomain.QualificationIneligible {
			next, cancelErr := commission.CancelUnsettled(commission.Version, "qualification_revoked", now)
			if cancelErr != nil {
				return distributionport.ErrConflict
			}
			if err = s.store.AppendCommissionAdjustmentWithin(tx, distributionstore.CommissionAdjustment{CommissionID: commission.ID, Kind: "qualification_revoke", DeltaMinor: next.CurrentPayableMinor - commission.CurrentPayableMinor, ResultingPayableMinor: next.CurrentPayableMinor, Reason: "qualification_revoked", SourceRef: value.Attribution.QualificationEvidenceRef, OccurredAt: now}); err != nil {
				return err
			}
			if _, err = s.store.UpdateCommissionWithin(tx, next, commission.Version); err != nil {
				return err
			}
			return s.auditWithin(tx, "distribution.commission_cancelled.v1", "commission", commission.ID, "worker:distribution-due", map[string]any{"reason": "qualification_revoked"}, now)
		}
		readiness, err := s.payment.ReceiverReadinessWithin(tx, distributor.CustomerID, receiver.AppID)
		if err != nil || !readiness.Ready {
			return s.holdWithin(tx, commission, "receiver_unavailable", now)
		}
		if !state.SplitCapable || state.DeadlineAt.IsZero() || !state.DeadlineAt.After(now) {
			return s.markExceptionWithin(tx, commission, distributionstore.Settlement{}, "settlement_deadline", "split_deadline_unavailable", state.OriginalPaymentRef, now)
		}
		if state.AvailableMinor < commission.CurrentPayableMinor {
			return s.holdWithin(tx, commission, "split_funds_unavailable", now)
		}
		if commission.Status == distributiondomain.CommissionHeld {
			commission, err = commission.Resume(commission.Version, now)
			if err != nil {
				return distributionport.ErrConflict
			}
			commission, err = s.store.UpdateCommissionWithin(tx, commission, commission.Version-1)
			if err != nil {
				return err
			}
		}
		next, beginErr := commission.BeginSettlement(commission.Version, now)
		if beginErr != nil {
			return distributionport.ErrConflict
		}
		// The reference is an immutable Payment idempotency binding. Keep a
		// descriptive fixed prefix so even the first database IDs satisfy the
		// Store's minimum reference length without relying on database sequence
		// growth.
		settlementRef := "dstl_commission_" + strconv.FormatInt(commission.ID, 10)
		planned, _, err := s.store.InsertSettlementWithin(tx, distributionstore.Settlement{CommissionID: commission.ID, Reference: settlementRef, AmountMinor: next.CurrentPayableMinor, Currency: "CNY", OriginalPaymentReference: state.OriginalPaymentRef, State: "planned", DeadlineAt: &state.DeadlineAt, Version: 1, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		instruction, err := s.payment.AcceptProfitSharingWithin(tx, paymentport.ProfitSharingRequest{SettlementRef: settlementRef, OriginalPaymentRef: state.OriginalPaymentRef, RecipientCustomerID: distributor.CustomerID, AmountMinor: next.CurrentPayableMinor, Currency: "CNY", IdempotencyKey: "distribution.settlement:" + strconv.FormatInt(commission.ID, 10), SourceDigest: effectport.Hash("distribution.settlement.v1", strconv.FormatInt(commission.ID, 10)), PayloadDigest: effectport.Hash("distribution.settlement.payload.v1", settlementRef, state.OriginalPaymentRef, strconv.FormatInt(next.CurrentPayableMinor, 10)), PolicyDigest: effectport.Hash("distribution.policy.v1", strconv.FormatInt(value.Attribution.PolicyVersion, 10), strconv.Itoa(int(value.Attribution.CommissionRateBasisPoints)), strconv.Itoa(int(value.Attribution.WaitDays)))})
		if err != nil {
			return err
		}
		planned.InstructionReference, planned.EffectReference, planned.State, planned.DeadlineAt, planned.Version, planned.UpdatedAt = instruction.Reference, instruction.EffectRef, "accepted", deadlinePointer(instruction.DeadlineAt), planned.Version+1, now
		if _, err = s.store.AcceptSettlementWithin(tx, planned, planned.Version-1); err != nil {
			return err
		}
		if _, err = s.store.UpdateCommissionWithin(tx, next, commission.Version); err != nil {
			return err
		}
		return s.auditWithin(tx, "distribution.settlement_accepted.v1", "settlement", planned.ID, "worker:distribution-due", map[string]any{"commission_id": commission.ID, "instruction_reference": instruction.Reference, "amount_minor": next.CurrentPayableMinor}, now)
	})
	if err != nil {
		return err
	}
	if retry {
		return river.JobSnooze(time.Minute)
	}
	return river.JobSnooze(settlementQueryDelay)
}

func (s *SettlementService) applyReconciliation(ctx context.Context, commissionID int64, instruction paymentport.ProfitSharingInstruction) error {
	now := s.now().UTC()
	unknown := false
	finalized := false
	err := s.uow.Within(ctx, func(tx context.Context) error {
		value, err := s.store.ReadSettlementContextWithin(tx, commissionID)
		if err != nil {
			return err
		}
		settlement, err := s.store.ReadLatestSettlementByCommissionWithin(tx, commissionID, true)
		if err != nil {
			return err
		}
		if settlement.InstructionReference != instruction.Reference || instruction.AmountMinor != settlement.AmountMinor || instruction.Currency != settlement.Currency {
			return distributionport.ErrConflict
		}
		commission := value.Commission
		if instruction.ReceiverConfirmedSuccess {
			if !(commission.Status == distributiondomain.CommissionException && commission.PaidMinor > 0) {
				next, transitionErr := commission.ConfirmReceiverPaid(commission.Version, instruction.AmountMinor, now)
				if transitionErr != nil {
					return distributionport.ErrConflict
				}
				if _, err = s.store.UpdateCommissionWithin(tx, next, commission.Version); err != nil {
					return err
				}
			}
			settlement.State, settlement.DeadlineAt, settlement.Version, settlement.UpdatedAt = "receiver_succeeded", deadlinePointer(instruction.DeadlineAt), settlement.Version+1, now
			if _, err = s.store.UpdateSettlementStateWithin(tx, settlement, settlement.Version-1); err != nil {
				return err
			}
			if err = s.auditWithin(tx, "distribution.settlement_paid.v1", "commission", commission.ID, "worker:distribution-due", map[string]any{"settlement_reference": settlement.Reference, "instruction_reference": instruction.Reference, "paid_minor": instruction.AmountMinor}, now); err != nil {
				return err
			}
			finalized = true
			return nil
		}
		state := "outcome_unknown"
		reason := "settlement_outcome_unknown"
		businessCancellation := false
		if instruction.OutcomeKnown {
			// A receiver-specific CLOSED detail proves that this Provider
			// instruction did not pay. It does not erase the merchant's
			// commission obligation: only a separately established refund or
			// qualification revocation may make that amount non-payable.
			if businessReason := s.closedInstructionBusinessCancellationWithin(tx, value); businessReason != "" {
				state, reason, businessCancellation = "cancelled", businessReason, true
			} else {
				state, reason = "exception", paymentSettlementFailureReason(instruction.FailureClass)
			}
		} else {
			unknown = true
		}
		settlement.State, settlement.DeadlineAt, settlement.Version, settlement.UpdatedAt = state, deadlinePointer(instruction.DeadlineAt), settlement.Version+1, now
		if _, err = s.store.UpdateSettlementStateWithin(tx, settlement, settlement.Version-1); err != nil {
			return err
		}
		if instruction.OutcomeKnown {
			if businessCancellation {
				next, transitionErr := commission.ConfirmInstructionUnpaid(commission.Version, reason, now)
				if transitionErr != nil {
					return distributionport.ErrConflict
				}
				if _, err = s.store.UpdateCommissionWithin(tx, next, commission.Version); err != nil {
					return err
				}
				if err = s.resolveBusinessExceptionsWithin(tx, commission.ID, reason, now); err != nil {
					return err
				}
				if next.CurrentPayableMinor != commission.CurrentPayableMinor {
					if err = s.store.AppendCommissionAdjustmentWithin(tx, distributionstore.CommissionAdjustment{CommissionID: commission.ID, Kind: "qualification_revoke", DeltaMinor: next.CurrentPayableMinor - commission.CurrentPayableMinor, ResultingPayableMinor: next.CurrentPayableMinor, Reason: reason, SourceRef: value.Attribution.QualificationEvidenceRef, OccurredAt: now}); err != nil {
						return err
					}
				}
				if err = s.auditWithin(tx, "distribution.commission_cancelled.v1", "commission", commission.ID, "worker:distribution-due", map[string]any{"reason": reason, "instruction_reference": instruction.Reference}, now); err != nil {
					return err
				}
			} else if err = s.markExceptionWithin(tx, commission, settlement, "settlement_not_paid", reason, instruction.Reference, now); err != nil {
				return err
			}
			finalized = true
			return nil
		}
		return s.markExceptionWithin(tx, commission, settlement, "settlement_unknown", reason, instruction.Reference, now)
	})
	if err != nil {
		return err
	}
	if unknown {
		return river.JobSnooze(settlementQueryDelay)
	}
	if finalized {
		var value distributionstore.SettlementContext
		var settlement distributionstore.Settlement
		if err = s.uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			value, readErr = s.store.ReadSettlementContextWithin(tx, commissionID)
			if readErr != nil {
				return readErr
			}
			settlement, readErr = s.store.ReadLatestSettlementByCommissionWithin(tx, commissionID, false)
			return readErr
		}); err != nil {
			return err
		}
		return s.releaseRemaining(ctx, value, settlement)
	}
	return nil
}

func (s *SettlementService) resolveBusinessExceptionsWithin(ctx context.Context, commissionID int64, cancellationReason string, now time.Time) error {
	exceptions, err := s.store.ResolveOpenBusinessExceptionsWithin(ctx, commissionID, now)
	if err != nil {
		return err
	}
	for _, exception := range exceptions {
		if err = s.auditWithin(ctx, "distribution.exception_resolved.v1", "exception", exception.ID, "worker:distribution-due", map[string]any{"commission_id": commissionID, "reason": cancellationReason, "exception_kind": exception.Kind, "resolution": "commission_cancelled"}, now); err != nil {
			return err
		}
	}
	return nil
}

// paymentSettlementFailureReason keeps a terminal provider diagnostic within
// Payment's finite stable-port vocabulary. Empty and unknown classes retain
// the existing generic fact rather than inventing a Provider explanation.
func paymentSettlementFailureReason(value string) string {
	switch value {
	case "receiver_account_abnormal", "receiver_relation_removed", "receiver_high_risk", "receiver_real_name_unverified", "merchant_permission_revoked", "receiver_receipt_limit", "payer_account_abnormal", "invalid_split_request":
		return "payment_" + value
	default:
		return "settlement_not_paid"
	}
}

// closedInstructionBusinessCancellationWithin identifies the only existing
// business facts that can end the distributor obligation after an exact
// provider CLOSED result. Provider rejection alone never belongs here.
func (s *SettlementService) closedInstructionBusinessCancellationWithin(ctx context.Context, value distributionstore.SettlementContext) string {
	commission := value.Commission
	if commission.CurrentPayableMinor == 0 && commission.SuccessfulRefundMinor == commission.OriginalItemPaidMinor {
		return "buyer_refund"
	}
	// Qualification may later become eligible again after a new purchase. The
	// commission is nevertheless tied to the earlier submitted instruction, so
	// only the durable, affirmative revocation fact for this commission may
	// cancel it. A same-kind unavailable or conflicted qualification check is
	// deliberately not evidence of revocation.
	hasRevocation, err := s.store.HasExceptionWithReasonWithin(ctx, commission.ID, "qualification_revoked_after_paid", "qualification_revoked_after_paid")
	if err != nil || !hasRevocation {
		return ""
	}
	return "qualification_revoked"
}

// releaseRemaining submits the Payment-owned unfreeze intent with a stable
// key after a paid or cancelled commission. The provider query happens after
// the acceptance UoW; a non-final result is snoozed under the same reference.
func (s *SettlementService) releaseRemaining(ctx context.Context, value distributionstore.SettlementContext, settlement distributionstore.Settlement) error {
	originalReference := settlement.OriginalPaymentReference
	if originalReference == "" {
		state, err := s.payment.DistributionPaymentState(ctx, value.Commission.OrderID)
		if err != nil {
			return river.JobSnooze(dueCheckRetryDelay)
		}
		originalReference = state.OriginalPaymentRef
	}
	if originalReference == "" {
		return river.JobSnooze(dueCheckRetryDelay)
	}
	var unfreeze paymentport.ProfitSharingUnfreeze
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		// The unfreeze key identifies this one commission finalization. Its
		// payload digest therefore contains only immutable bindings; a later
		// exception marker or buyer-refund adjustment must replay the original
		// Payment instruction rather than turn the same key into a conflict.
		unfreeze, err = s.payment.UnfreezeProfitSharingRemainingWithin(tx, paymentport.ProfitSharingUnfreezeRequest{OriginalPaymentRef: originalReference, Reason: "distribution_commission_finalized", IdempotencyKey: "distribution.unfreeze:" + strconv.FormatInt(value.Commission.ID, 10), SourceDigest: effectport.Hash("distribution.unfreeze.v1", strconv.FormatInt(value.Commission.ID, 10)), PayloadDigest: effectport.Hash("distribution.unfreeze.payload.v1", strconv.FormatInt(value.Commission.ID, 10), originalReference), PolicyDigest: effectport.Hash("distribution.unfreeze.policy.v1")})
		return err
	})
	if err != nil {
		if errors.Is(err, paymentport.ErrNothingToUnfreeze) {
			return nil
		}
		return river.JobSnooze(dueCheckRetryDelay)
	}
	confirmed, err := s.payment.ReconcileProfitSharingUnfreeze(ctx, unfreeze.Reference)
	if err != nil || !confirmed.OutcomeKnown {
		return river.JobSnooze(settlementQueryDelay)
	}
	if confirmed.State != "succeeded" {
		// Provider-final failure is an explicit after-payment exception. The
		// record remains queryable by the original reference; it is never
		// presented as a successfully released balance.
		var markErr error
		if value.Commission.Status == distributiondomain.CommissionCancelled {
			// A cancellation has already removed the distributor obligation. A
			// failed release of the payment reserve is a separate funds-control
			// exception, not a reason to resurrect that commission as payable.
			markErr = s.recordCancelledUnfreezeFailure(ctx, value, settlement, confirmed.Reference)
		} else {
			markErr = s.markException(ctx, value, settlement, "unfreeze_final_failed", "unfreeze_final_failed", confirmed.Reference)
		}
		if markErr != nil {
			return markErr
		}
	}
	return nil
}

// recordCancelledUnfreezeFailure preserves a cancelled commission's zero
// payable balance while recording the still-reserved merchant funds as a
// queryable exception. Replays use the same Payment unfreeze reference and do
// not append another exception fact.
func (s *SettlementService) recordCancelledUnfreezeFailure(ctx context.Context, value distributionstore.SettlementContext, settlement distributionstore.Settlement, evidence string) error {
	return s.uow.Within(ctx, func(tx context.Context) error {
		locked, err := s.store.ReadSettlementContextWithin(tx, value.Commission.ID)
		if err != nil {
			return err
		}
		if locked.Commission.Status != distributiondomain.CommissionCancelled || locked.Commission.CurrentPayableMinor != 0 {
			return distributionport.ErrConflict
		}
		if _, err = s.store.FindOpenExceptionWithin(tx, locked.Commission.ID, "unfreeze_final_failed"); err == nil {
			return nil
		} else if !errors.Is(err, distributionport.ErrNotFound) {
			return err
		}
		now := s.now().UTC()
		exception, err := s.store.InsertExceptionWithin(tx, distributionstore.Exception{CommissionID: locked.Commission.ID, SettlementID: settlement.ID, Kind: "unfreeze_final_failed", Status: "open", UnpaidDueMinor: 0, AlreadyPaidMinor: locked.Commission.PaidMinor, AmountMinor: 0, Reason: "unfreeze_final_failed", EvidenceReference: evidence, ActorScope: "worker:distribution-due", Version: 1, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		return s.auditWithin(tx, "distribution.unfreeze_failure_opened.v1", "exception", exception.ID, "worker:distribution-due", map[string]any{"commission_id": locked.Commission.ID, "unfreeze_reference": evidence}, now)
	})
}

func (s *SettlementService) holdWithin(ctx context.Context, commission distributiondomain.Commission, reason string, now time.Time) error {
	if commission.Status == distributiondomain.CommissionHeld {
		return nil
	}
	next, err := commission.Hold(commission.Version, reason, now)
	if err != nil {
		return distributionport.ErrConflict
	}
	if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
		return err
	}
	return s.auditWithin(ctx, "distribution.commission_held.v1", "commission", commission.ID, "worker:distribution-due", map[string]any{"reason": reason}, now)
}

func (s *SettlementService) markException(ctx context.Context, value distributionstore.SettlementContext, settlement distributionstore.Settlement, kind, reason, evidence string) error {
	return s.uow.Within(ctx, func(tx context.Context) error {
		locked, err := s.store.ReadSettlementContextWithin(tx, value.Commission.ID)
		if err != nil {
			return err
		}
		return s.markExceptionWithin(tx, locked.Commission, settlement, kind, reason, evidence, s.now().UTC())
	})
}

func (s *SettlementService) markExceptionWithin(ctx context.Context, commission distributiondomain.Commission, settlement distributionstore.Settlement, kind, reason, evidence string, now time.Time) error {
	next := commission
	if commission.Status != distributiondomain.CommissionException {
		var err error
		next, err = commission.MarkException(commission.Version, reason, now)
		if err != nil {
			return distributionport.ErrConflict
		}
		if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
			return err
		}
	}
	// Replaying the exact terminal instruction after a human has resolved its
	// exception must not silently reopen the same fact. Different evidence may
	// still be recorded once there is no open exception for this kind.
	if _, err := s.store.FindExceptionByEvidenceWithin(ctx, commission.ID, kind, evidence); err == nil {
		return nil
	} else if !errors.Is(err, distributionport.ErrNotFound) {
		return err
	}
	if _, err := s.store.FindOpenExceptionWithin(ctx, commission.ID, kind); err == nil {
		return nil
	} else if !errors.Is(err, distributionport.ErrNotFound) {
		return err
	}
	unpaid := maxInt64(next.CurrentPayableMinor-next.PaidMinor, 0)
	exception, err := s.store.InsertExceptionWithin(ctx, distributionstore.Exception{CommissionID: commission.ID, SettlementID: settlement.ID, Kind: kind, Status: "open", UnpaidDueMinor: unpaid, AlreadyPaidMinor: next.PaidMinor, AmountMinor: unpaid, Reason: reason, EvidenceReference: evidence, ActorScope: "worker:distribution-due", Version: 1, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return err
	}
	return s.auditWithin(ctx, "distribution.exception_opened.v1", "exception", exception.ID, "worker:distribution-due", map[string]any{"commission_id": commission.ID, "reason": reason}, now)
}

func deadlineImminent(deadline *time.Time, now time.Time) bool {
	if deadline == nil || deadline.IsZero() || now.IsZero() {
		return false
	}
	remaining := deadline.UTC().Sub(now.UTC())
	return remaining > 0 && remaining <= 24*time.Hour
}

// recordDeadlineWarningWithin is a non-terminal operational exception. It
// preserves the current unpaid obligation and lets due checks continue; it
// never changes the commission status or bypasses the configured wait period.
func (s *SettlementService) recordDeadlineWarningWithin(ctx context.Context, commission distributiondomain.Commission, settlement distributionstore.Settlement, deadline time.Time, evidence string, now time.Time) error {
	if commission.PaidMinor > 0 || !deadlineImminent(deadlinePointer(deadline), now) {
		return nil
	}
	_, err := s.store.FindOpenExceptionWithin(ctx, commission.ID, "settlement_deadline_imminent")
	if err == nil {
		return nil
	}
	if !errors.Is(err, distributionport.ErrNotFound) {
		return err
	}
	// This is a warning, not a monetary adjustment or a manual-payment claim.
	// The Commission itself remains the sole source for the outstanding amount.
	remaining := maxInt64(commission.CurrentPayableMinor-commission.PaidMinor, 0)
	exception, err := s.store.InsertExceptionWithin(ctx, distributionstore.Exception{CommissionID: commission.ID, SettlementID: settlement.ID, Kind: "settlement_deadline_imminent", Status: "open", UnpaidDueMinor: 0, AlreadyPaidMinor: 0, AmountMinor: 0, Reason: "split_deadline_within_24h", EvidenceReference: evidence, ActorScope: "worker:distribution-due", Version: 1, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return err
	}
	return s.auditWithin(ctx, "distribution.settlement_deadline_imminent.v1", "exception", exception.ID, "worker:distribution-due", map[string]any{"commission_id": commission.ID, "deadline_at": deadline.UTC(), "current_unpaid_minor": remaining}, now)
}

func (s *SettlementService) recordDeadlineWarning(ctx context.Context, value distributionstore.SettlementContext, settlement distributionstore.Settlement, deadline time.Time) error {
	return s.uow.Within(ctx, func(tx context.Context) error {
		locked, err := s.store.ReadSettlementContextWithin(tx, value.Commission.ID)
		if err != nil {
			return err
		}
		return s.recordDeadlineWarningWithin(tx, locked.Commission, settlement, deadline, settlement.OriginalPaymentReference, s.now().UTC())
	})
}

func (s *SettlementService) auditWithin(ctx context.Context, eventType, aggregateType string, aggregateID int64, actor string, payload any, at time.Time) error {
	if err := s.store.AppendAuditWithin(ctx, eventType, aggregateType, aggregateID, actor, payload, at); err != nil {
		return err
	}
	return s.store.AppendOutboxWithin(ctx, eventType, eventType+":"+strconv.FormatInt(aggregateID, 10)+":"+strconv.FormatInt(at.UnixNano(), 10), aggregateID, payload, at)
}

func deadlinePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}

var _ DueCheckApplication = (*SettlementService)(nil)
