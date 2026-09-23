package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type settlementUOWStub struct{}

func (settlementUOWStub) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(platformpostgres.BindTransaction(ctx, distributionNoopTx{}))
}

type settlementPaymentStub struct {
	capability  paymentport.SettlementCapability
	state       paymentport.DistributionPaymentState
	instruction paymentport.ProfitSharingInstruction
	accepted    paymentport.ProfitSharingRequest
	reconciled  paymentport.ProfitSharingInstruction
	unfreeze    paymentport.ProfitSharingUnfreeze
	unfreezes   int
	unfreezeReq []paymentport.ProfitSharingUnfreezeRequest
	readiness   paymentport.ReceiverReadiness
}

func (s *settlementPaymentStub) SettlementCapability(context.Context) (paymentport.SettlementCapability, error) {
	if s.capability.Enabled || s.capability.Reason != "" {
		return s.capability, nil
	}
	return paymentport.SettlementCapability{Enabled: true}, nil
}

func (s *settlementPaymentStub) PrepareProfitSharingReceiverWithin(context.Context, paymentport.ReceiverPreparation) (paymentport.ReceiverReadiness, error) {
	return paymentport.ReceiverReadiness{}, errors.New("not used")
}
func (s *settlementPaymentStub) ReceiverReadiness(context.Context, int64, string) (paymentport.ReceiverReadiness, error) {
	if s.readiness.AppID != "" || s.readiness.Reference != "" || !s.readiness.UpdatedAt.IsZero() {
		return s.readiness, nil
	}
	return paymentport.ReceiverReadiness{Ready: true, Reference: "receiver_1", AppID: "app-1", OutcomeKnown: true, UpdatedAt: time.Now().UTC()}, nil
}
func (s *settlementPaymentStub) ReceiverReadinessWithin(ctx context.Context, customerID int64, appID string) (paymentport.ReceiverReadiness, error) {
	return s.ReceiverReadiness(ctx, customerID, appID)
}
func (s *settlementPaymentStub) DistributionPaymentState(context.Context, int64) (paymentport.DistributionPaymentState, error) {
	return s.state, nil
}
func (s *settlementPaymentStub) DistributionPaymentStateWithin(ctx context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	return s.DistributionPaymentState(ctx, orderID)
}
func (s *settlementPaymentStub) AcceptProfitSharingWithin(_ context.Context, request paymentport.ProfitSharingRequest) (paymentport.ProfitSharingInstruction, error) {
	s.accepted = request
	return s.instruction, nil
}
func (s *settlementPaymentStub) GetProfitSharing(context.Context, string) (paymentport.ProfitSharingInstruction, error) {
	return s.instruction, nil
}
func (s *settlementPaymentStub) ReconcileProfitSharing(context.Context, string) (paymentport.ProfitSharingInstruction, error) {
	return s.reconciled, nil
}
func (s *settlementPaymentStub) ReconcileProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingUnfreeze, error) {
	return s.unfreeze, nil
}
func (s *settlementPaymentStub) CancelUnsubmittedProfitSharingWithin(context.Context, string, string, paymentport.ProfitSharingCancellationActor) (paymentport.ProfitSharingInstruction, error) {
	return paymentport.ProfitSharingInstruction{}, errors.New("not used")
}
func (s *settlementPaymentStub) UnfreezeProfitSharingRemainingWithin(_ context.Context, request paymentport.ProfitSharingUnfreezeRequest) (paymentport.ProfitSharingUnfreeze, error) {
	s.unfreezes++
	s.unfreezeReq = append(s.unfreezeReq, request)
	return s.unfreeze, nil
}

type settlementStoreStub struct {
	context     distributionstore.SettlementContext
	distributor distributiondomain.Distributor
	receiver    distributionport.ReceiverReadiness
	settlement  distributionstore.Settlement
	exceptions  []distributionstore.Exception
	adjustments []distributionstore.CommissionAdjustment
	audits      []string
}

func (s *settlementStoreStub) ReadSettlementContextWithin(context.Context, int64) (distributionstore.SettlementContext, error) {
	return s.context, nil
}
func (s *settlementStoreStub) ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	return s.distributor, s.receiver, nil
}
func (s *settlementStoreStub) ReadLatestSettlementByCommissionWithin(context.Context, int64, bool) (distributionstore.Settlement, error) {
	if s.settlement.ID == 0 {
		return distributionstore.Settlement{}, distributionport.ErrNotFound
	}
	return s.settlement, nil
}
func (s *settlementStoreStub) InsertSettlementWithin(_ context.Context, value distributionstore.Settlement) (distributionstore.Settlement, bool, error) {
	if s.settlement.ID != 0 {
		return s.settlement, false, nil
	}
	value.ID = 71
	s.settlement = value
	return value, true, nil
}
func (s *settlementStoreStub) AcceptSettlementWithin(_ context.Context, value distributionstore.Settlement, _ int64) (distributionstore.Settlement, error) {
	s.settlement = value
	return value, nil
}
func (s *settlementStoreStub) UpdateSettlementStateWithin(_ context.Context, value distributionstore.Settlement, _ int64) (distributionstore.Settlement, error) {
	s.settlement = value
	return value, nil
}
func (s *settlementStoreStub) UpdateCommissionWithin(_ context.Context, value distributiondomain.Commission, _ int64) (distributiondomain.Commission, error) {
	s.context.Commission = value
	return value, nil
}

func (s *settlementStoreStub) AppendCommissionAdjustmentWithin(_ context.Context, value distributionstore.CommissionAdjustment) error {
	s.adjustments = append(s.adjustments, value)
	return nil
}
func (s *settlementStoreStub) InsertExceptionWithin(_ context.Context, value distributionstore.Exception) (distributionstore.Exception, error) {
	value.ID = int64(len(s.exceptions) + 1)
	s.exceptions = append(s.exceptions, value)
	return value, nil
}
func (s *settlementStoreStub) FindOpenExceptionWithin(_ context.Context, commissionID int64, kind string) (distributionstore.Exception, error) {
	for _, value := range s.exceptions {
		if value.CommissionID == commissionID && value.Kind == kind && value.Status == "open" {
			return value, nil
		}
	}
	return distributionstore.Exception{}, distributionport.ErrNotFound
}
func (s *settlementStoreStub) FindExceptionByEvidenceWithin(_ context.Context, commissionID int64, kind, evidence string) (distributionstore.Exception, error) {
	for _, value := range s.exceptions {
		if value.CommissionID == commissionID && value.Kind == kind && value.EvidenceReference == evidence {
			return value, nil
		}
	}
	return distributionstore.Exception{}, distributionport.ErrNotFound
}
func (s *settlementStoreStub) HasExceptionWithReasonWithin(_ context.Context, commissionID int64, kind, reason string) (bool, error) {
	for _, value := range s.exceptions {
		if value.CommissionID == commissionID && value.Kind == kind && value.Reason == reason {
			return true, nil
		}
	}
	return false, nil
}
func (s *settlementStoreStub) ResolveOpenBusinessExceptionsWithin(_ context.Context, commissionID int64, at time.Time) ([]distributionstore.Exception, error) {
	resolved := []distributionstore.Exception{}
	for index := range s.exceptions {
		value := &s.exceptions[index]
		if value.CommissionID != commissionID || value.Status != "open" || (value.Kind != "buyer_refund_after_paid" && value.Kind != "qualification_revoked_after_paid") {
			continue
		}
		value.Status, value.Version, value.UpdatedAt = "resolved", value.Version+1, at.UTC()
		resolved = append(resolved, *value)
	}
	return resolved, nil
}
func (s *settlementStoreStub) AppendAuditWithin(_ context.Context, event string, _ string, _ int64, _ string, _ any, _ time.Time) error {
	s.audits = append(s.audits, event)
	return nil
}
func (*settlementStoreStub) AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error {
	return nil
}

func TestCommissionDueAcceptsOneInstructionThenConfirmsExactReceiver(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	if err := service.RunCommissionDueCheck(context.Background(), 41); err == nil {
		t.Fatal("accepted split must keep a durable reconcile job")
	} else {
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) {
			t.Fatalf("first due error=%v, want snooze", err)
		}
	}
	if store.context.Commission.Status != distributiondomain.CommissionSettling || store.settlement.InstructionReference != "psinst_1" || payment.accepted.AmountMinor != 100 || payment.accepted.RecipientCustomerID != 9 {
		t.Fatalf("commission=%+v settlement=%+v request=%+v", store.context.Commission, store.settlement, payment.accepted)
	}
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", ReceiverConfirmedSuccess: true, OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("receiver confirmation=%v", err)
	}
	if store.context.Commission.Status != distributiondomain.CommissionPaid || store.context.Commission.PaidMinor != 100 || store.settlement.State != "receiver_succeeded" || payment.unfreezes != 1 {
		t.Fatalf("final commission=%+v settlement=%+v", store.context.Commission, store.settlement)
	}
}

func TestCommissionDueUnknownStaysQueryableAndCanRecoverToPaid(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	_ = service.RunCommissionDueCheck(context.Background(), 41)
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", OutcomeKnown: false, UpdatedAt: now.Add(time.Minute)}
	err := service.RunCommissionDueCheck(context.Background(), 41)
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) || store.context.Commission.Status != distributiondomain.CommissionException || store.settlement.State != "outcome_unknown" || len(store.exceptions) != 1 {
		t.Fatalf("unknown err=%v commission=%+v settlement=%+v exceptions=%+v", err, store.context.Commission, store.settlement, store.exceptions)
	}
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", ReceiverConfirmedSuccess: true, OutcomeKnown: true, UpdatedAt: now.Add(2 * time.Minute)}
	if err = service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("recovered provider result=%v", err)
	}
	if store.context.Commission.Status != distributiondomain.CommissionPaid || store.context.Commission.PaidMinor != 100 || payment.unfreezes != 1 {
		t.Fatalf("recovered commission=%+v", store.context.Commission)
	}
}

func TestCommissionDueClosedInstructionPreservesCommissionObligationAndRecordsException(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	_ = service.RunCommissionDueCheck(context.Background(), 41)
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", FailureClass: "receiver_receipt_limit", OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("confirmed not-paid settlement=%v", err)
	}
	if got := store.context.Commission; got.Status != distributiondomain.CommissionException || got.PaidMinor != 0 || got.CurrentPayableMinor != 100 || got.ExceptionReason != "payment_receiver_receipt_limit" || payment.unfreezes != 1 {
		t.Fatalf("closed instruction must preserve payable obligation and unfreeze reserve: %+v unfreezes=%d", got, payment.unfreezes)
	}
	if len(store.exceptions) != 1 || store.exceptions[0].Kind != "settlement_not_paid" || store.exceptions[0].Reason != "payment_receiver_receipt_limit" || store.exceptions[0].AmountMinor != 100 {
		t.Fatalf("closed instruction exception=%+v", store.exceptions)
	}
	if len(store.adjustments) != 0 {
		t.Fatalf("provider CLOSED cannot create a negative commission adjustment: %+v", store.adjustments)
	}
}

func TestCommissionDueClosedInstructionReplayDoesNotReopenResolvedSameEvidence(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	_ = service.RunCommissionDueCheck(context.Background(), 41)
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", FailureClass: "receiver_receipt_limit", OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("first confirmed not-paid settlement=%v", err)
	}
	store.exceptions[0].Status = "resolved"
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("replayed confirmed not-paid settlement=%v", err)
	}
	if len(store.exceptions) != 1 || store.exceptions[0].Status != "resolved" || store.exceptions[0].EvidenceReference != "psinst_1" {
		t.Fatalf("same terminal evidence must not reopen a resolved exception: %+v", store.exceptions)
	}
}

func TestCommissionDueClosedInstructionCancelsOnlyForExistingFullRefundFact(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	_ = service.RunCommissionDueCheck(context.Background(), 41)
	refunded, err := store.context.Commission.RecordRefundAfterSubmission(store.context.Commission.Version, store.context.Commission.OriginalItemPaidMinor, "buyer_refund_after_paid", now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	store.context.Commission = refunded
	store.exceptions = append(store.exceptions, distributionstore.Exception{ID: 41, CommissionID: store.context.Commission.ID, SettlementID: store.settlement.ID, Kind: "buyer_refund_after_paid", Status: "open", Reason: "buyer_refund_after_paid", EvidenceReference: "refund:buyer", ActorScope: "order-refund:17", Version: 1, CreatedAt: now, UpdatedAt: now})
	service.now = func() time.Time { return now.Add(time.Minute) }
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", FailureClass: "receiver_account_abnormal", OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err = service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("full-refund closed result=%v", err)
	}
	if got := store.context.Commission; got.Status != distributiondomain.CommissionCancelled || got.CurrentPayableMinor != 0 || got.CancelReason != "buyer_refund" {
		t.Fatalf("existing full refund must be the only cancellation proof: %+v", got)
	}
	if got := store.context.Commission; got.ExceptionReason != "" {
		t.Fatalf("cancellation must not retain a stale exception reason: %+v", got)
	}
	if len(store.exceptions) != 1 || store.exceptions[0].Status != "resolved" || store.exceptions[0].Kind != "buyer_refund_after_paid" || len(store.adjustments) != 0 || payment.unfreezes != 1 {
		t.Fatalf("full-refund cancellation must resolve the business exception without a provider-loss adjustment: exceptions=%+v adjustments=%+v unfreezes=%d", store.exceptions, store.adjustments, payment.unfreezes)
	}
}

func TestCommissionDueClosedInstructionCancelsForPersistentQualificationRevocation(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	_ = service.RunCommissionDueCheck(context.Background(), 41)
	store.exceptions = append(store.exceptions, distributionstore.Exception{ID: 41, CommissionID: store.context.Commission.ID, SettlementID: store.settlement.ID, Kind: "qualification_revoked_after_paid", Status: "open", Reason: "qualification_revoked_after_paid", EvidenceReference: "refund:qualification", ActorScope: "order-refund:17", Version: 1, CreatedAt: now, UpdatedAt: now})
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", FailureClass: "receiver_account_abnormal", OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("qualification-revoked closed result=%v", err)
	}
	if got := store.context.Commission; got.Status != distributiondomain.CommissionCancelled || got.CurrentPayableMinor != 0 || got.CancelReason != "qualification_revoked" {
		t.Fatalf("current qualification revocation must cancel independently of provider failure class: %+v", got)
	}
	if len(store.adjustments) != 1 || store.adjustments[0].Kind != "qualification_revoke" || store.adjustments[0].Reason != "qualification_revoked" || store.adjustments[0].DeltaMinor != -100 {
		t.Fatalf("qualification cancellation adjustment=%+v", store.adjustments)
	}
	if len(store.exceptions) != 1 || store.exceptions[0].Status != "resolved" || store.exceptions[0].Reason != "qualification_revoked_after_paid" {
		t.Fatalf("qualification cancellation must retain but close its durable evidence: %+v", store.exceptions)
	}
}

func TestCommissionDueClosedInstructionDoesNotTreatUnavailableQualificationEvidenceAsRevocation(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	_ = service.RunCommissionDueCheck(context.Background(), 41)
	// The refund worker records this same kind when qualification evidence
	// cannot be read. It is not the affirmative revocation fact required to
	// cancel an already-submitted split, even after it is resolved.
	store.exceptions = append(store.exceptions, distributionstore.Exception{ID: 41, CommissionID: store.context.Commission.ID, SettlementID: store.settlement.ID, Kind: "qualification_revoked_after_paid", Status: "resolved", Reason: "qualification_evidence_unavailable_after_submission", EvidenceReference: "refund:qualification", ActorScope: "order-refund:17", Version: 1, CreatedAt: now, UpdatedAt: now})
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", FailureClass: "receiver_account_abnormal", OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("unavailable-qualification closed result=%v", err)
	}
	if got := store.context.Commission; got.Status != distributiondomain.CommissionException || got.CurrentPayableMinor != 100 || got.CancelReason != "" || got.ExceptionReason != "payment_receiver_account_abnormal" {
		t.Fatalf("unavailable qualification evidence must retain obligation: %+v", got)
	}
	if len(store.adjustments) != 0 || len(store.exceptions) != 2 || store.exceptions[1].Kind != "settlement_not_paid" {
		t.Fatalf("unavailable qualification evidence cannot cancel or adjust: adjustments=%+v exceptions=%+v", store.adjustments, store.exceptions)
	}
}

func TestPaymentSettlementFailureReasonAcceptsOnlyPaymentSafeClasses(t *testing.T) {
	for _, value := range []struct{ input, want string }{
		{"receiver_account_abnormal", "payment_receiver_account_abnormal"},
		{"receiver_relation_removed", "payment_receiver_relation_removed"},
		{"receiver_high_risk", "payment_receiver_high_risk"},
		{"receiver_real_name_unverified", "payment_receiver_real_name_unverified"},
		{"merchant_permission_revoked", "payment_merchant_permission_revoked"},
		{"receiver_receipt_limit", "payment_receiver_receipt_limit"},
		{"payer_account_abnormal", "payment_payer_account_abnormal"},
		{"invalid_split_request", "payment_invalid_split_request"},
		{"", "settlement_not_paid"},
		{"provider raw message", "settlement_not_paid"},
	} {
		if got := paymentSettlementFailureReason(value.input); got != value.want {
			t.Fatalf("payment failure=%q reason=%q want=%q", value.input, got, value.want)
		}
	}
}

func TestCommissionDueUnfreezeFailureRemainsExceptionInsteadOfCompletion(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	_ = service.RunCommissionDueCheck(context.Background(), 41)
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", ReceiverConfirmedSuccess: true, OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	payment.unfreeze = paymentport.ProfitSharingUnfreeze{Reference: "psunfreeze_1", State: "exception", OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("unfreeze final failure=%v", err)
	}
	if got := store.context.Commission; got.Status != distributiondomain.CommissionException || got.PaidMinor != 100 || len(store.exceptions) != 1 || store.exceptions[0].Kind != "unfreeze_final_failed" || store.exceptions[0].UnpaidDueMinor != 0 || store.exceptions[0].AmountMinor != 0 || store.exceptions[0].AlreadyPaidMinor != 100 {
		t.Fatalf("unfreeze failure must retain paid fact and exception: commission=%+v exceptions=%+v", got, store.exceptions)
	}
	// The retry keeps its original logical Payment instruction even though the
	// Commission is now an exception. A mutable status in the payload would
	// turn this same key into a Payment conflict.
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("unfreeze replay after exception=%v", err)
	}
	if len(payment.unfreezeReq) != 2 || payment.unfreezeReq[0].IdempotencyKey != payment.unfreezeReq[1].IdempotencyKey || payment.unfreezeReq[0].PayloadDigest != payment.unfreezeReq[1].PayloadDigest {
		t.Fatalf("unfreeze retry drifted: %+v", payment.unfreezeReq)
	}
}

func TestCommissionDueCancelledUnfreezeFailureKeepsZeroPayableAndReplays(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	cancelled, err := store.context.Commission.CancelUnsettled(store.context.Commission.Version, "qualification_revoked", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	store.context.Commission = cancelled
	store.settlement = distributionstore.Settlement{ID: 71, CommissionID: cancelled.ID, Reference: "dstl_commission_41", AmountMinor: 100, Currency: "CNY", OriginalPaymentReference: "payref_17", State: "cancelled", Version: 1, CreatedAt: now, UpdatedAt: now}
	payment.unfreeze = paymentport.ProfitSharingUnfreeze{Reference: "psunfreeze_1", State: "exception", OutcomeKnown: true, UpdatedAt: now.Add(time.Minute)}
	if err = service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("cancelled unfreeze final failure=%v", err)
	}
	if got := store.context.Commission; got.Status != distributiondomain.CommissionCancelled || got.CurrentPayableMinor != 0 || got.PaidMinor != 0 {
		t.Fatalf("unfreeze failure must not resurrect cancelled commission: %+v", got)
	}
	if len(store.exceptions) != 1 || store.exceptions[0].Kind != "unfreeze_final_failed" || store.exceptions[0].UnpaidDueMinor != 0 || store.exceptions[0].AmountMinor != 0 {
		t.Fatalf("cancelled unfreeze exception=%+v", store.exceptions)
	}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err != nil {
		t.Fatalf("cancelled unfreeze replay=%v", err)
	}
	if len(store.exceptions) != 1 || len(payment.unfreezeReq) != 2 || payment.unfreezeReq[0].IdempotencyKey != payment.unfreezeReq[1].IdempotencyKey || payment.unfreezeReq[0].PayloadDigest != payment.unfreezeReq[1].PayloadDigest {
		t.Fatalf("cancelled unfreeze replay drift: exceptions=%+v requests=%+v", store.exceptions, payment.unfreezeReq)
	}
}

func TestCommissionDueMarksImminentDeadlineWithoutEndingObligation(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	payment.state.DeadlineAt = now.Add(20 * time.Hour)
	payment.readiness = paymentport.ReceiverReadiness{Ready: false, AppID: "app-1", Reference: "receiver_1", OutcomeKnown: true, UpdatedAt: now}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err == nil {
		t.Fatal("receiver-unavailable due check must remain scheduled")
	} else {
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) {
			t.Fatalf("deadline warning error=%v, want snooze", err)
		}
	}
	if got := store.context.Commission; got.Status != distributiondomain.CommissionHeld || got.CurrentPayableMinor != 100 {
		t.Fatalf("deadline warning must retain obligation: %+v", got)
	}
	if len(store.exceptions) != 1 || store.exceptions[0].Kind != "settlement_deadline_imminent" || store.exceptions[0].UnpaidDueMinor != 0 || store.exceptions[0].AmountMinor != 0 {
		t.Fatalf("deadline warning=%+v", store.exceptions)
	}
	if err := service.RunCommissionDueCheck(context.Background(), 41); err == nil {
		t.Fatal("held receiver remains scheduled")
	}
	if len(store.exceptions) != 1 {
		t.Fatalf("deadline warning must be idempotent: %+v", store.exceptions)
	}
}

func TestCommissionDueMarksImminentDeadlineWhileSplitIsInFlight(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	store, payment, service := settlementFixture(t, now)
	settling, err := store.context.Commission.BeginSettlement(store.context.Commission.Version, now)
	if err != nil {
		t.Fatal(err)
	}
	store.context.Commission = settling
	deadline := now.Add(20 * time.Hour)
	store.settlement = distributionstore.Settlement{ID: 71, CommissionID: settling.ID, Reference: "dstl_commission_41", AmountMinor: 100, Currency: "CNY", OriginalPaymentReference: "payref_17", InstructionReference: "psinst_1", EffectReference: "eer_1", State: "accepted", DeadlineAt: &deadline, Version: 2, CreatedAt: now, UpdatedAt: now}
	payment.reconciled = paymentport.ProfitSharingInstruction{Reference: "psinst_1", AmountMinor: 100, Currency: "CNY", OutcomeKnown: false, UpdatedAt: now.Add(time.Minute)}

	err = service.RunCommissionDueCheck(context.Background(), 41)
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("in-flight query must stay durable: %v", err)
	}
	if len(store.exceptions) != 2 || store.exceptions[0].Kind != "settlement_deadline_imminent" || store.exceptions[0].AmountMinor != 0 || store.exceptions[1].Kind != "settlement_unknown" {
		t.Fatalf("in-flight deadline warning and outcome exception=%+v", store.exceptions)
	}
}

func settlementFixture(t *testing.T, now time.Time) (*settlementStoreStub, *settlementPaymentStub, *SettlementService) {
	t.Helper()
	attribution := distributiondomain.Attribution{ID: 31, OrderID: 17, OrderItemLine: 1, ProductCode: "p-17", ProductName: "Frozen Product", DistributorID: 8, PromotionCredentialID: 2, QualificationEvidenceRef: "order:3:item:1", QualificationState: distributiondomain.QualificationEligible, PolicyVersion: 4, CommissionRateBasisPoints: 1000, WaitDays: 0, AttributedAt: now.Add(-time.Hour)}
	commission, err := distributiondomain.NewCommission(attribution, 1000, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	commission.ID = 41
	store := &settlementStoreStub{context: distributionstore.SettlementContext{Commission: commission, Attribution: attribution, ProductID: 77, ProductType: distributiondomain.ProductTypeStandard}, distributor: distributiondomain.Distributor{ID: 8, CustomerID: 9, PublicNo: "D123456", AgreementVersion: "v1", Enabled: true, RegisteredAt: now.Add(-time.Hour), Version: 1}, receiver: distributionport.ReceiverReadiness{Ready: true, AppID: "app-1", Reference: "receiver_1", CheckedAt: now}}
	payment := &settlementPaymentStub{state: paymentport.DistributionPaymentState{OriginalPaymentRef: "payref_17", ConfirmedPaid: true, ConfirmedPaidAt: now.Add(-time.Hour), SplitCapable: true, DeadlineAt: now.Add(48 * time.Hour), OriginalMinor: 1000, AvailableMinor: 1000}, instruction: paymentport.ProfitSharingInstruction{Reference: "psinst_1", SettlementRef: "dstl_commission_41", OriginalPaymentRef: "payref_17", AmountMinor: 100, Currency: "CNY", EffectRef: "eer_1", DeadlineAt: now.Add(48 * time.Hour), UpdatedAt: now}, unfreeze: paymentport.ProfitSharingUnfreeze{Reference: "psunfreeze_1", State: "succeeded", OutcomeKnown: true, UpdatedAt: now}}
	qualification, err := NewQualificationService(lineageStub{roots: []customerdomain.CustomerID{9}}, qualificationOrderStub{evidence: []orderport.QualificationPurchaseEvidence{{OrderID: 3, OrderItemLine: 1, ProductID: 77, PayerCustomerID: 9, BeneficiaryCustomerID: 9, ItemPaidMinor: 1000, PaymentConfirmedAt: now.Add(-time.Hour), RecordOrigin: "native"}}}, payment)
	if err != nil {
		t.Fatal(err)
	}
	qualification.now = func() time.Time { return now }
	service, err := NewSettlementService(settlementUOWStub{}, store, qualification, payment)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return store, payment, service
}

var _ paymentport.DistributionSettlementPort = (*settlementPaymentStub)(nil)
var _ settlementStore = (*settlementStoreStub)(nil)
