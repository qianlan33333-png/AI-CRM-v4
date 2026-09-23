package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// refundStore is limited to Distribution-owned frozen commission facts. Order
// has already persisted its exact successful-refund transition before calling
// this consumer; no Payment provider call or cross-domain table access occurs
// here.
type refundStore interface {
	ListRefundRecheckContextsWithin(context.Context, int64, []distributionstore.QualificationProductScope) ([]distributionstore.SettlementContext, error)
	ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	ReadLatestSettlementByCommissionWithin(context.Context, int64, bool) (distributionstore.Settlement, error)
	UpdateCommissionWithin(context.Context, distributiondomain.Commission, int64) (distributiondomain.Commission, error)
	AppendCommissionAdjustmentWithin(context.Context, distributionstore.CommissionAdjustment) error
	InsertExceptionWithin(context.Context, distributionstore.Exception) (distributionstore.Exception, error)
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// RefundService consumes an already-final Order refund fact. It adjusts every
// commission attributed to the buyer order and separately rechecks every
// frozen attribution whose qualification evidence names the refunded order.
// That evidence-reference fanout covers native and verified-history purchases
// without guessing from raw payer/beneficiary IDs; QualificationService owns
// the canonical OneID lineage and decides whether another valid purchase keeps
// promotion qualification alive.
type RefundService struct {
	uow                   platformport.UnitOfWork
	store                 refundStore
	dueTasks              DueCheckEnqueuer
	recheckTasks          RefundRecheckEnqueuer
	qualification         *QualificationService
	qualificationEvidence orderport.QualificationRefundEvidenceByOrderReader
	now                   func() time.Time
}

func NewRefundService(uow platformport.UnitOfWork, store refundStore, dueTasks DueCheckEnqueuer, recheckTasks RefundRecheckEnqueuer, qualification *QualificationService, qualificationEvidence orderport.QualificationRefundEvidenceByOrderReader) (*RefundService, error) {
	if uow == nil || store == nil || dueTasks == nil || recheckTasks == nil || qualification == nil || qualificationEvidence == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &RefundService{uow: uow, store: store, dueTasks: dueTasks, recheckTasks: recheckTasks, qualification: qualification, qualificationEvidence: qualificationEvidence, now: time.Now}, nil
}

func (s *RefundService) ConsumeRefundSettlementWithin(ctx context.Context, event orderport.RefundSettlementEvent) error {
	if s == nil || s.recheckTasks == nil || !event.Valid() {
		return distributionport.ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return distributionport.ErrUnavailable
	}
	// Payment/Order calls this while holding its funding lock. Do not scan
	// Distribution, Identity, or a qualifying Payment here; a durable internal
	// task replays authoritative facts after this transaction commits.
	return s.recheckTasks.EnqueueRefundRecheckWithin(ctx, RefundRecheckJobArgs{OrderID: event.Order.ID, State: "successful", OccurredAt: event.OccurredAt.UTC(), ReceiptKey: event.ReceiptKey})
}

// RunRefundRecheck executes after the originating Payment/Order UoW commits.
// It is the sole place that locks Distribution commission rows and performs a
// OneID/Payment qualification recheck, so refund callbacks cannot deadlock on
// cross-domain locks or be rejected by an unrelated lookup failure.
func (s *RefundService) RunRefundRecheck(ctx context.Context, args RefundRecheckJobArgs) error {
	if s == nil || s.uow == nil || s.store == nil || s.qualification == nil || !args.Valid() {
		return distributionport.ErrUnavailable
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		switch args.State {
		case "successful":
			return s.applySuccessfulRefundRecheckWithin(tx, args)
		case "opened", "final_failed":
			return s.applyRefundExposureRecheckWithin(tx, args)
		default:
			return distributionport.ErrConflict
		}
	})
}

func (s *RefundService) applySuccessfulRefundRecheckWithin(ctx context.Context, args RefundRecheckJobArgs) error {
	now := args.OccurredAt.UTC()
	source := refundSource(eventRef(args.OrderID, args.ReceiptKey))
	targets, err := s.qualificationRefundTargetsWithin(ctx, args.OrderID)
	if err != nil {
		return err
	}
	contexts, err := s.store.ListRefundRecheckContextsWithin(ctx, args.OrderID, qualificationProductScopes(targets))
	if err != nil {
		return err
	}
	// Lock Distribution rows before reading shared Identity and Payment facts.
	// This single globally ordered lock set is shared by buyer and qualification
	// rechecks; the originating Payment transaction only queued this job.
	byOrder := make([]distributionstore.SettlementContext, 0)
	seen := make(map[int64]struct{})
	for _, value := range contexts {
		if value.Commission.OrderID == args.OrderID {
			byOrder = append(byOrder, value)
		}
		for _, target := range targets {
			if value.ProductID != target.productID || value.ProductType != target.productType {
				continue
			}
			if _, duplicate := seen[value.Commission.ID]; duplicate {
				continue
			}
			matches, matchErr := s.matchesQualificationRefundTargetWithin(ctx, value, target)
			if matchErr != nil {
				return matchErr
			}
			if !matches {
				continue
			}
			seen[value.Commission.ID] = struct{}{}
			if err = s.applyQualificationRefundWithin(ctx, value, args.ReceiptKey, source, now); err != nil {
				return err
			}
		}
	}
	// Qualification rechecks acquire Identity then their qualifying Payment
	// rows. Only after those complete may this worker lock the refunded buyer
	// payment to obtain its cumulative amount. That preserves the global
	// commission -> identity -> payment order used by due checks.
	state, err := s.qualification.PaymentStateWithin(ctx, args.OrderID)
	if err != nil || !state.ConfirmedPaid {
		return distributionport.ErrUnavailable
	}
	for _, value := range byOrder {
		if err = s.applyBuyerRefundWithin(ctx, value, state.SuccessfulRefundMinor, args.ReceiptKey, source, now); err != nil {
			return err
		}
	}
	return nil
}

func qualificationProductScopes(targets []qualificationRefundTarget) []distributionstore.QualificationProductScope {
	result := make([]distributionstore.QualificationProductScope, 0, len(targets))
	for _, target := range targets {
		result = append(result, distributionstore.QualificationProductScope{ProductID: target.productID, ProductType: target.productType})
	}
	return result
}

type qualificationRefundTarget struct {
	productID                              int64
	productType                            distributiondomain.ProductType
	payerCustomerID, beneficiaryCustomerID int64
}

func (s *RefundService) qualificationRefundTargetsWithin(ctx context.Context, orderID int64) ([]qualificationRefundTarget, error) {
	if s.qualificationEvidence == nil || orderID < 1 {
		return nil, distributionport.ErrUnavailable
	}
	seen := map[string]struct{}{}
	result := make([]qualificationRefundTarget, 0, 2)
	add := func(value orderport.QualificationRefundEvidence) {
		kind := distributiondomain.ProductType(value.ProductType)
		if value.ProductID < 1 || !kind.Valid() || value.PayerCustomerID < 1 || value.BeneficiaryCustomerID < 1 {
			return
		}
		key := strconv.FormatInt(value.ProductID, 10) + ":" + string(kind) + ":" + strconv.FormatInt(value.PayerCustomerID, 10) + ":" + strconv.FormatInt(value.BeneficiaryCustomerID, 10)
		if _, found := seen[key]; found {
			return
		}
		seen[key] = struct{}{}
		result = append(result, qualificationRefundTarget{productID: value.ProductID, productType: kind, payerCustomerID: value.PayerCustomerID, beneficiaryCustomerID: value.BeneficiaryCustomerID})
	}
	evidence, err := s.qualificationEvidence.ListQualificationRefundEvidenceByOrderWithin(ctx, orderID)
	if err != nil {
		return nil, err
	}
	for _, value := range evidence {
		if value.OrderID != orderID || !value.Valid() {
			return nil, distributionport.ErrUnavailable
		}
		add(value)
	}
	return result, nil
}

func (s *RefundService) matchesQualificationRefundTargetWithin(ctx context.Context, value distributionstore.SettlementContext, target qualificationRefundTarget) (bool, error) {
	distributor, _, err := s.store.ReadDistributorWithin(ctx, value.Commission.DistributorID, true)
	if err != nil {
		return false, err
	}
	return s.qualification.MatchesTrustedCustomerWithin(ctx, distributor.CustomerID, target.payerCustomerID, target.beneficiaryCustomerID)
}

func (s *RefundService) applyBuyerRefundWithin(ctx context.Context, value distributionstore.SettlementContext, cumulative int64, receiptKey, source string, now time.Time) error {
	commission := value.Commission
	if cumulative < commission.SuccessfulRefundMinor || cumulative > commission.OriginalItemPaidMinor {
		return distributionport.ErrConflict
	}
	// River retries and duplicate provider observations carry the same immutable
	// refund fact. The cumulative amount is already recorded, so a replay must
	// not append another adjustment or exception.
	if cumulative == commission.SuccessfulRefundMinor {
		return nil
	}

	switch commission.Status {
	case distributiondomain.CommissionPending, distributiondomain.CommissionHeld:
		next, err := commission.RepriceFromPaidMinor(commission.Version, commission.OriginalItemPaidMinor, cumulative, now)
		if err != nil {
			return distributionport.ErrConflict
		}
		if err = s.appendRefundAdjustmentWithin(ctx, commission, next, source, now); err != nil {
			return err
		}
		if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
			return err
		}
		eventType := "distribution.commission_refund_adjusted.v1"
		if next.Status == distributiondomain.CommissionCancelled {
			eventType = "distribution.commission_cancelled.v1"
		}
		if err = s.recordWithin(ctx, eventType, next.ID, receiptKey, map[string]any{"order_id": commission.OrderID, "receipt_key": receiptKey, "successful_refund_minor": cumulative, "current_payable_minor": next.CurrentPayableMinor}, now); err != nil {
			return err
		}
		return s.dueTasks.EnqueueCommissionDueWithin(ctx, next.ID, now)

	case distributiondomain.CommissionSettling, distributiondomain.CommissionPaid, distributiondomain.CommissionException:
		return s.recordPostSubmissionBuyerRefundWithin(ctx, value, cumulative, receiptKey, source, now)

	case distributiondomain.CommissionCancelled, distributiondomain.CommissionZero:
		next, err := commission.RecordRefundAfterFinalization(commission.Version, cumulative, now)
		if err != nil {
			return distributionport.ErrConflict
		}
		if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
			return err
		}
		if err = s.recordWithin(ctx, "distribution.commission_refund_recorded.v1", next.ID, receiptKey, map[string]any{"order_id": commission.OrderID, "receipt_key": receiptKey, "successful_refund_minor": cumulative, "terminal_status": next.Status}, now); err != nil {
			return err
		}
		if next.Status == distributiondomain.CommissionCancelled {
			return s.dueTasks.EnqueueCommissionDueWithin(ctx, next.ID, now)
		}
		return nil
	default:
		return distributionport.ErrConflict
	}
}

func (s *RefundService) recordPostSubmissionBuyerRefundWithin(ctx context.Context, value distributionstore.SettlementContext, cumulative int64, receiptKey, source string, now time.Time) error {
	commission := value.Commission
	next, err := commission.RecordRefundAfterSubmission(commission.Version, cumulative, "buyer_refund_after_paid", now)
	if err != nil {
		return distributionport.ErrConflict
	}
	if err = s.appendRefundAdjustmentWithin(ctx, commission, next, source, now); err != nil {
		return err
	}
	if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
		return err
	}
	settlement, settlementErr := s.store.ReadLatestSettlementByCommissionWithin(ctx, commission.ID, false)
	if settlementErr != nil && !errors.Is(settlementErr, distributionport.ErrNotFound) {
		return settlementErr
	}
	amount := afterSalesHandlingMinor(next, "buyer_refund_after_paid")
	exception, err := s.store.InsertExceptionWithin(ctx, distributionstore.Exception{
		CommissionID: commission.ID, SettlementID: settlement.ID, Kind: "buyer_refund_after_paid", Status: "open",
		UnpaidDueMinor: maxInt64(next.CurrentPayableMinor-next.PaidMinor, 0), AlreadyPaidMinor: next.PaidMinor,
		AmountMinor: amount, Reason: "buyer_refund_after_paid", EvidenceReference: source,
		ActorScope: "order-refund:" + strconv.FormatInt(commission.OrderID, 10), Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	if err = s.recordWithin(ctx, "distribution.exception_opened.v1", exception.ID, receiptKey, map[string]any{"commission_id": commission.ID, "reason": "buyer_refund_after_paid", "receipt_key": receiptKey, "successful_refund_minor": cumulative, "current_payable_minor": next.CurrentPayableMinor, "paid_minor": next.PaidMinor, "exception_minor": amount}, now); err != nil {
		return err
	}
	return s.dueTasks.EnqueueCommissionDueWithin(ctx, commission.ID, now)
}

func (s *RefundService) appendRefundAdjustmentWithin(ctx context.Context, before, after distributiondomain.Commission, source string, now time.Time) error {
	if after.CurrentPayableMinor == before.CurrentPayableMinor {
		return nil
	}
	return s.store.AppendCommissionAdjustmentWithin(ctx, distributionstore.CommissionAdjustment{
		CommissionID: before.ID, Kind: "buyer_refund", DeltaMinor: after.CurrentPayableMinor - before.CurrentPayableMinor,
		ResultingPayableMinor: after.CurrentPayableMinor, Reason: "buyer_refund", SourceRef: source, OccurredAt: now,
	})
}

func (s *RefundService) applyQualificationRefundWithin(ctx context.Context, value distributionstore.SettlementContext, receiptKey, source string, now time.Time) error {
	distributor, _, err := s.store.ReadDistributorWithin(ctx, value.Commission.DistributorID, true)
	if err != nil {
		return err
	}
	qualification, err := s.qualification.CheckWithin(ctx, distributor.CustomerID, value.ProductID, value.ProductType)
	if err != nil {
		// A refund callback must never make the already-persisted Order refund
		// fail. Treat unreadable identity/payment proof as an explicit hold.
		qualification = distributiondomain.Qualification{State: distributiondomain.QualificationUnavailable, Reason: "qualification_evidence_unavailable", CheckedAt: now}
	}
	commission := value.Commission
	switch qualification.State {
	case distributiondomain.QualificationEligible:
		// A different exact-product paid purchase remains valid. The refunded
		// evidence is historical context, not a reason to cancel this payout.
		return nil
	case distributiondomain.QualificationSuspended, distributiondomain.QualificationUnavailable, distributiondomain.QualificationConflict:
		if commission.Status == distributiondomain.CommissionPending {
			next, holdErr := commission.Hold(commission.Version, "qualification_refund_evidence_pending", now)
			if holdErr != nil {
				return distributionport.ErrConflict
			}
			if _, holdErr = s.store.UpdateCommissionWithin(ctx, next, commission.Version); holdErr != nil {
				return holdErr
			}
			if holdErr = s.recordWithin(ctx, "distribution.commission_held.v1", next.ID, receiptKey, map[string]any{"reason": qualification.Reason, "qualification_refund_receipt": receiptKey}, now); holdErr != nil {
				return holdErr
			}
			return s.dueTasks.EnqueueCommissionDueWithin(ctx, next.ID, now)
		}
		if commission.Status == distributiondomain.CommissionHeld || commission.Status == distributiondomain.CommissionCancelled || commission.Status == distributiondomain.CommissionZero {
			return nil
		}
		return s.markQualificationExceptionWithin(ctx, value, receiptKey, source, "qualification_evidence_unavailable_after_submission", now)
	case distributiondomain.QualificationIneligible:
		switch commission.Status {
		case distributiondomain.CommissionPending, distributiondomain.CommissionHeld:
			next, cancelErr := commission.CancelUnsettled(commission.Version, "qualification_revoked", now)
			if cancelErr != nil {
				return distributionport.ErrConflict
			}
			if cancelErr = s.store.AppendCommissionAdjustmentWithin(ctx, distributionstore.CommissionAdjustment{CommissionID: commission.ID, Kind: "qualification_revoke", DeltaMinor: -commission.CurrentPayableMinor, ResultingPayableMinor: 0, Reason: "qualification_revoked", SourceRef: source, OccurredAt: now}); cancelErr != nil {
				return cancelErr
			}
			if _, cancelErr = s.store.UpdateCommissionWithin(ctx, next, commission.Version); cancelErr != nil {
				return cancelErr
			}
			if cancelErr = s.recordWithin(ctx, "distribution.commission_cancelled.v1", next.ID, receiptKey, map[string]any{"reason": "qualification_revoked", "qualification_order_refund_receipt": receiptKey}, now); cancelErr != nil {
				return cancelErr
			}
			return s.dueTasks.EnqueueCommissionDueWithin(ctx, next.ID, now)
		case distributiondomain.CommissionSettling, distributiondomain.CommissionPaid, distributiondomain.CommissionException:
			return s.markQualificationExceptionWithin(ctx, value, receiptKey, source, "qualification_revoked_after_paid", now)
		case distributiondomain.CommissionCancelled, distributiondomain.CommissionZero:
			return nil
		default:
			return distributionport.ErrConflict
		}
	default:
		return distributionport.ErrConflict
	}
}

func (s *RefundService) markQualificationExceptionWithin(ctx context.Context, value distributionstore.SettlementContext, receiptKey, source, reason string, now time.Time) error {
	commission := value.Commission
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
	settlement, settlementErr := s.store.ReadLatestSettlementByCommissionWithin(ctx, commission.ID, false)
	if settlementErr != nil && !errors.Is(settlementErr, distributionport.ErrNotFound) {
		return settlementErr
	}
	exception, err := s.store.InsertExceptionWithin(ctx, distributionstore.Exception{
		CommissionID: commission.ID, SettlementID: settlement.ID, Kind: "qualification_revoked_after_paid", Status: "open",
		UnpaidDueMinor: maxInt64(next.CurrentPayableMinor-next.PaidMinor, 0), AlreadyPaidMinor: next.PaidMinor,
		AmountMinor: afterSalesHandlingMinor(next, reason), Reason: reason, EvidenceReference: source,
		ActorScope: "order-refund:" + strconv.FormatInt(commission.OrderID, 10), Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	if err = s.recordWithin(ctx, "distribution.exception_opened.v1", exception.ID, receiptKey, map[string]any{"commission_id": commission.ID, "reason": reason, "qualification_refund_receipt": receiptKey, "paid_minor": next.PaidMinor}, now); err != nil {
		return err
	}
	return s.dueTasks.EnqueueCommissionDueWithin(ctx, commission.ID, now)
}

// afterSalesHandlingMinor is the amount already paid to a distributor that a
// later refund or durable qualification revocation leaves without a lawful
// commission basis. Unpaid instructions are deliberately zero here: they
// remain settlement exceptions rather than being misrepresented as recovered
// or merchant-absorbed money.
func afterSalesHandlingMinor(commission distributiondomain.Commission, reason string) int64 {
	if commission.PaidMinor < 1 {
		return 0
	}
	if reason == "qualification_revoked_after_paid" {
		return commission.PaidMinor
	}
	return maxInt64(commission.PaidMinor-commission.CurrentPayableMinor, 0)
}

func (s *RefundService) recordWithin(ctx context.Context, eventType string, aggregateID int64, receiptKey string, payload any, now time.Time) error {
	source := "order-refund:" + receiptKey
	if err := s.store.AppendAuditWithin(ctx, eventType, "commission", aggregateID, source, payload, now); err != nil {
		return err
	}
	return s.store.AppendOutboxWithin(ctx, eventType, eventType+":"+strconv.FormatInt(aggregateID, 10)+":"+receiptKey, aggregateID, payload, now)
}

func eventRef(orderID int64, receiptKey string) string {
	return "order:" + strconv.FormatInt(orderID, 10) + ":refund:" + receiptKey
}

func refundSource(value string) string {
	if len(value) <= 200 {
		return value
	}
	return value[:200]
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func absoluteMinor(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

var _ orderport.RefundSettlementConsumer = (*RefundService)(nil)

// ConsumeRefundExposureWithin reacts to a Payment-owned in-flight refund fact.
// It is intentionally separate from successful refund repricing: requested,
// processing and unknown states pause distribution obligations but never alter
// the successful-refund total. Payment invokes it in the same UoW as its own
// refund state/receipt/effect acceptance.
func (s *RefundService) ConsumeRefundExposureWithin(ctx context.Context, event paymentport.RefundExposureEvent) error {
	if s == nil || s.recheckTasks == nil || !event.Valid() {
		return distributionport.ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return distributionport.ErrUnavailable
	}
	return s.recheckTasks.EnqueueRefundRecheckWithin(ctx, RefundRecheckJobArgs{OrderID: event.OrderID, RefundID: event.RefundID, State: string(event.State), OccurredAt: event.OccurredAt.UTC(), ReceiptKey: event.ReceiptKey})
}

func (s *RefundService) applyRefundExposureRecheckWithin(ctx context.Context, args RefundRecheckJobArgs) error {
	event := paymentport.RefundExposureEvent{OrderID: args.OrderID, RefundID: args.RefundID, State: paymentport.RefundExposureState(args.State), OccurredAt: args.OccurredAt, ReceiptKey: args.ReceiptKey}
	targets, err := s.qualificationRefundTargetsWithin(ctx, event.OrderID)
	if err != nil {
		return err
	}
	contexts, err := s.store.ListRefundRecheckContextsWithin(ctx, event.OrderID, qualificationProductScopes(targets))
	if err != nil {
		return err
	}
	byOrder := make([]distributionstore.SettlementContext, 0)
	seen := make(map[int64]struct{})
	for _, value := range contexts {
		if value.Commission.OrderID == event.OrderID {
			byOrder = append(byOrder, value)
		}
		for _, target := range targets {
			if value.ProductID != target.productID || value.ProductType != target.productType {
				continue
			}
			if _, duplicate := seen[value.Commission.ID]; duplicate {
				continue
			}
			matches, matchErr := s.matchesQualificationRefundTargetWithin(ctx, value, target)
			if matchErr != nil {
				return matchErr
			}
			if !matches {
				continue
			}
			seen[value.Commission.ID] = struct{}{}
			if err = s.applyQualificationRefundExposureWithin(ctx, value, event); err != nil {
				return err
			}
		}
	}
	for _, value := range byOrder {
		if err = s.applyBuyerRefundExposureWithin(ctx, value, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *RefundService) applyBuyerRefundExposureWithin(ctx context.Context, value distributionstore.SettlementContext, event paymentport.RefundExposureEvent) error {
	commission := value.Commission
	now := event.OccurredAt.UTC()
	switch event.State {
	case paymentport.RefundExposureOpened:
		if commission.Status == distributiondomain.CommissionPending {
			next, err := commission.Hold(commission.Version, "buyer_refund_pending", now)
			if err != nil {
				return distributionport.ErrConflict
			}
			if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
				return err
			}
			if err = s.recordWithin(ctx, "distribution.commission_held.v1", next.ID, event.ReceiptKey, map[string]any{"reason": "buyer_refund_pending", "refund_id": event.RefundID}, now); err != nil {
				return err
			}
		}
		if commission.Status != distributiondomain.CommissionCancelled && commission.Status != distributiondomain.CommissionZero {
			return s.dueTasks.EnqueueCommissionDueWithin(ctx, commission.ID, now)
		}
		return nil
	case paymentport.RefundExposureFinalFailed:
		state, stateErr := s.qualification.PaymentStateWithin(ctx, commission.OrderID)
		if stateErr != nil {
			return stateErr
		}
		if state.RefundExposure || state.RequestedRefundMinor > 0 || state.ProcessingRefundMinor > 0 || state.OutcomeUnknownRefundMinor > 0 {
			if commission.Status != distributiondomain.CommissionCancelled && commission.Status != distributiondomain.CommissionZero {
				return s.dueTasks.EnqueueCommissionDueWithin(ctx, commission.ID, now)
			}
			return nil
		}
		if commission.Status == distributiondomain.CommissionHeld && (commission.HoldReason == "buyer_refund_pending" || commission.HoldReason == "payment_or_refund_pending") {
			next, err := commission.Resume(commission.Version, now)
			if err != nil {
				return distributionport.ErrConflict
			}
			if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
				return err
			}
			if err = s.recordWithin(ctx, "distribution.commission_resumed.v1", next.ID, event.ReceiptKey, map[string]any{"reason": "buyer_refund_final_failed", "refund_id": event.RefundID}, now); err != nil {
				return err
			}
		}
		if commission.Status != distributiondomain.CommissionCancelled && commission.Status != distributiondomain.CommissionZero {
			return s.dueTasks.EnqueueCommissionDueWithin(ctx, commission.ID, now)
		}
		return nil
	default:
		return distributionport.ErrConflict
	}
}

func (s *RefundService) applyQualificationRefundExposureWithin(ctx context.Context, value distributionstore.SettlementContext, event paymentport.RefundExposureEvent) error {
	commission := value.Commission
	now := event.OccurredAt.UTC()
	if event.State == paymentport.RefundExposureOpened {
		distributor, _, err := s.store.ReadDistributorWithin(ctx, commission.DistributorID, true)
		if err != nil {
			return err
		}
		qualification, checkErr := s.qualification.CheckWithin(ctx, distributor.CustomerID, value.ProductID, value.ProductType)
		if checkErr == nil && qualification.State == distributiondomain.QualificationEligible {
			// The affected promoter has another valid purchase. A same-product
			// refund for somebody else, or for an older replacement purchase,
			// cannot unnecessarily pause their commission.
			return nil
		}
		if commission.Status == distributiondomain.CommissionPending {
			next, err := commission.Hold(commission.Version, "qualification_refund_evidence_pending", now)
			if err != nil {
				return distributionport.ErrConflict
			}
			if _, err = s.store.UpdateCommissionWithin(ctx, next, commission.Version); err != nil {
				return err
			}
			if err = s.recordWithin(ctx, "distribution.commission_held.v1", next.ID, event.ReceiptKey, map[string]any{"reason": "qualification_refund_evidence_pending", "refund_id": event.RefundID}, now); err != nil {
				return err
			}
		}
		if commission.Status != distributiondomain.CommissionCancelled && commission.Status != distributiondomain.CommissionZero {
			return s.dueTasks.EnqueueCommissionDueWithin(ctx, commission.ID, now)
		}
		return nil
	}
	if event.State != paymentport.RefundExposureFinalFailed {
		return distributionport.ErrConflict
	}
	// A final failure can restore qualification, but only an authoritative
	// recheck may resume it. Other holds remain untouched and get a due recheck.
	distributor, _, err := s.store.ReadDistributorWithin(ctx, commission.DistributorID, true)
	if err != nil {
		return err
	}
	qualification, err := s.qualification.CheckWithin(ctx, distributor.CustomerID, value.ProductID, value.ProductType)
	if err == nil && qualification.State == distributiondomain.QualificationEligible && commission.Status == distributiondomain.CommissionHeld && commission.HoldReason == "qualification_refund_evidence_pending" {
		next, resumeErr := commission.Resume(commission.Version, now)
		if resumeErr != nil {
			return distributionport.ErrConflict
		}
		if _, resumeErr = s.store.UpdateCommissionWithin(ctx, next, commission.Version); resumeErr != nil {
			return resumeErr
		}
		if resumeErr = s.recordWithin(ctx, "distribution.commission_resumed.v1", next.ID, event.ReceiptKey, map[string]any{"reason": "qualification_refund_final_failed", "refund_id": event.RefundID}, now); resumeErr != nil {
			return resumeErr
		}
	}
	if commission.Status != distributiondomain.CommissionCancelled && commission.Status != distributiondomain.CommissionZero {
		return s.dueTasks.EnqueueCommissionDueWithin(ctx, commission.ID, now)
	}
	return nil
}

var _ paymentport.RefundExposureConsumer = (*RefundService)(nil)
