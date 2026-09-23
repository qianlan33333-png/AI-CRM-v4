package app

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type refundEvidenceStub struct {
	rows map[int64][]orderport.QualificationRefundEvidence
	err  error
}

func (s refundEvidenceStub) ListQualificationRefundEvidenceByOrderWithin(_ context.Context, orderID int64) ([]orderport.QualificationRefundEvidence, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]orderport.QualificationRefundEvidence(nil), s.rows[orderID]...), nil
}

type refundDueStub struct{ ids []int64 }

func (s *refundDueStub) EnqueueCommissionDueWithin(_ context.Context, commissionID int64, _ time.Time) error {
	s.ids = append(s.ids, commissionID)
	return nil
}

type refundRecheckStub struct{ args []RefundRecheckJobArgs }

func (s *refundRecheckStub) EnqueueRefundRecheckWithin(_ context.Context, args RefundRecheckJobArgs) error {
	s.args = append(s.args, args)
	return nil
}

type refundStoreStub struct {
	contexts    map[int64]distributionstore.SettlementContext
	settlements map[int64]distributionstore.Settlement
	adjustments []distributionstore.CommissionAdjustment
	exceptions  []distributionstore.Exception
	listCalls   int
}

func (s *refundStoreStub) ListRefundRecheckContextsWithin(_ context.Context, orderID int64, scopes []distributionstore.QualificationProductScope) ([]distributionstore.SettlementContext, error) {
	s.listCalls++
	return s.match(func(value distributionstore.SettlementContext) bool {
		if value.Commission.OrderID == orderID {
			return true
		}
		for _, scope := range scopes {
			if value.ProductID == scope.ProductID && value.ProductType == scope.ProductType {
				return true
			}
		}
		return false
	}), nil
}

func (s *refundStoreStub) match(matches func(distributionstore.SettlementContext) bool) []distributionstore.SettlementContext {
	result := make([]distributionstore.SettlementContext, 0)
	for _, value := range s.contexts {
		if matches(value) {
			result = append(result, value)
		}
	}
	return result
}

func (*refundStoreStub) ReadDistributorWithin(_ context.Context, distributorID int64, _ bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	if distributorID != 8 {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrNotFound
	}
	return distributiondomain.Distributor{ID: 8, CustomerID: 9, PublicNo: "D123456", AgreementVersion: "v1", Enabled: true, RegisteredAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Version: 1}, distributionport.ReceiverReadiness{}, nil
}

func (s *refundStoreStub) ReadLatestSettlementByCommissionWithin(_ context.Context, commissionID int64, _ bool) (distributionstore.Settlement, error) {
	value, found := s.settlements[commissionID]
	if !found {
		return distributionstore.Settlement{}, distributionport.ErrNotFound
	}
	return value, nil
}

func (s *refundStoreStub) UpdateCommissionWithin(_ context.Context, value distributiondomain.Commission, expectedVersion int64) (distributiondomain.Commission, error) {
	current, found := s.contexts[value.ID]
	if !found || current.Commission.Version != expectedVersion {
		return distributiondomain.Commission{}, distributionport.ErrConflict
	}
	current.Commission = value
	s.contexts[value.ID] = current
	return value, nil
}

func (s *refundStoreStub) AppendCommissionAdjustmentWithin(_ context.Context, value distributionstore.CommissionAdjustment) error {
	s.adjustments = append(s.adjustments, value)
	return nil
}

func (s *refundStoreStub) InsertExceptionWithin(_ context.Context, value distributionstore.Exception) (distributionstore.Exception, error) {
	value.ID = int64(len(s.exceptions) + 1)
	s.exceptions = append(s.exceptions, value)
	return value, nil
}

func (*refundStoreStub) AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error {
	return nil
}
func (*refundStoreStub) AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error {
	return nil
}

func refundContext(t *testing.T, orderID, commissionID int64, status distributiondomain.CommissionStatus, paidMinor int64, productID int64, now time.Time) distributionstore.SettlementContext {
	t.Helper()
	attribution := distributiondomain.Attribution{
		ID: 11, OrderID: orderID, OrderItemLine: 1, ProductCode: "p-77", ProductName: "Frozen Product",
		DistributorID: 8, PromotionCredentialID: 2, QualificationEvidenceRef: "order:3:item:1",
		QualificationState: distributiondomain.QualificationEligible, PolicyVersion: 1, CommissionRateBasisPoints: 2000,
		WaitDays: 7, AttributedAt: now.Add(-2 * time.Hour),
	}
	commission, err := distributiondomain.NewCommission(attribution, 1000, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	commission.ID, commission.Status, commission.Version, commission.UpdatedAt = commissionID, status, 2, now.Add(-time.Minute)
	if status == distributiondomain.CommissionPaid {
		commission.PaidMinor = paidMinor
	}
	if status == distributiondomain.CommissionHeld {
		commission.HoldReason = "buyer_refund_pending"
	}
	return distributionstore.SettlementContext{Commission: commission, Attribution: attribution, ProductID: productID, ProductType: distributiondomain.ProductTypeStandard}
}

func refundServiceFixture(t *testing.T, store *refundStoreStub, evidence refundEvidenceStub, purchase []orderport.QualificationPurchaseEvidence, states map[int64]paymentport.DistributionPaymentState, now time.Time) (*RefundService, *refundDueStub, *refundRecheckStub) {
	t.Helper()
	qualification, err := NewQualificationService(
		lineageStub{roots: []customerdomain.CustomerID{9}},
		qualificationOrderStub{evidence: purchase},
		qualificationPaymentStub{states: states},
	)
	if err != nil {
		t.Fatal(err)
	}
	qualification.now = func() time.Time { return now }
	due := &refundDueStub{}
	rechecks := &refundRecheckStub{}
	service, err := NewRefundService(settlementUOWStub{}, store, due, rechecks, qualification, evidence)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return service, due, rechecks
}

func TestRefundRecheckUsesAuthoritativeCumulativeRefundAndPreservesPaidFact(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	value := refundContext(t, 17, 41, distributiondomain.CommissionPaid, 200, 77, now)
	store := &refundStoreStub{contexts: map[int64]distributionstore.SettlementContext{41: value}, settlements: map[int64]distributionstore.Settlement{41: {ID: 71, CommissionID: 41, AmountMinor: 200}}}
	service, _, _ := refundServiceFixture(t, store, refundEvidenceStub{rows: map[int64][]orderport.QualificationRefundEvidence{}}, nil, map[int64]paymentport.DistributionPaymentState{17: {ConfirmedPaid: true, SuccessfulRefundMinor: 300}}, now)

	if err := service.RunRefundRecheck(context.Background(), RefundRecheckJobArgs{OrderID: 17, State: "successful", OccurredAt: now, ReceiptKey: "refund-success-0001"}); err != nil {
		t.Fatal(err)
	}
	first := store.contexts[41].Commission
	if first.Status != distributiondomain.CommissionException || first.PaidMinor != 200 || first.SuccessfulRefundMinor != 300 || first.CurrentPayableMinor != 140 || len(store.exceptions) != 1 || store.exceptions[0].AmountMinor != 60 {
		t.Fatalf("first refund commission=%+v exceptions=%+v", first, store.exceptions)
	}

	states := map[int64]paymentport.DistributionPaymentState{17: {ConfirmedPaid: true, SuccessfulRefundMinor: 400}}
	service, _, _ = refundServiceFixture(t, store, refundEvidenceStub{rows: map[int64][]orderport.QualificationRefundEvidence{}}, nil, states, now.Add(time.Minute))
	if err := service.RunRefundRecheck(context.Background(), RefundRecheckJobArgs{OrderID: 17, State: "successful", OccurredAt: now.Add(time.Minute), ReceiptKey: "refund-success-0002"}); err != nil {
		t.Fatal(err)
	}
	second := store.contexts[41].Commission
	if second.PaidMinor != 200 || second.SuccessfulRefundMinor != 400 || second.CurrentPayableMinor != 120 || len(store.adjustments) != 2 || store.adjustments[0].DeltaMinor != -60 || store.adjustments[1].DeltaMinor != -20 || store.exceptions[1].AmountMinor != 80 {
		t.Fatalf("second refund commission=%+v adjustments=%+v exceptions=%+v", second, store.adjustments, store.exceptions)
	}

	// The same immutable terminal fact may be replayed after a worker retry.
	// Cumulative state makes it a no-op rather than a second financial entry.
	if err := service.RunRefundRecheck(context.Background(), RefundRecheckJobArgs{OrderID: 17, State: "successful", OccurredAt: now.Add(2 * time.Minute), ReceiptKey: "refund-success-replay"}); err != nil {
		t.Fatal(err)
	}
	if replay := store.contexts[41].Commission; replay.SuccessfulRefundMinor != 400 || len(store.adjustments) != 2 || len(store.exceptions) != 2 {
		t.Fatalf("same cumulative refund must not duplicate facts: commission=%+v adjustments=%+v exceptions=%+v", replay, store.adjustments, store.exceptions)
	}
}

func TestQualificationRefundKeepsAlternativePurchaseThenCancelsAfterLastOne(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	value := refundContext(t, 50, 41, distributiondomain.CommissionPending, 0, 77, now)
	store := &refundStoreStub{contexts: map[int64]distributionstore.SettlementContext{41: value}, settlements: map[int64]distributionstore.Settlement{}}
	purchase := []orderport.QualificationPurchaseEvidence{
		{OrderID: 3, OrderItemLine: 1, ProductID: 77, PayerCustomerID: 9, BeneficiaryCustomerID: 9, ItemPaidMinor: 1000, PaymentConfirmedAt: now.Add(-2 * time.Hour), RecordOrigin: "native"},
		{OrderID: 4, OrderItemLine: 1, ProductID: 77, PayerCustomerID: 9, BeneficiaryCustomerID: 9, ItemPaidMinor: 1000, PaymentConfirmedAt: now.Add(-time.Hour), RecordOrigin: "native"},
	}
	evidence := refundEvidenceStub{rows: map[int64][]orderport.QualificationRefundEvidence{
		3: {{OrderID: 3, OrderItemLine: 1, ProductID: 77, ProductType: "standard_product", PayerCustomerID: 9, BeneficiaryCustomerID: 9, ItemPaidMinor: 1000, PaymentConfirmedAt: now.Add(-2 * time.Hour), RecordOrigin: "native"}},
		4: {{OrderID: 4, OrderItemLine: 1, ProductID: 77, ProductType: "standard_product", PayerCustomerID: 9, BeneficiaryCustomerID: 9, ItemPaidMinor: 1000, PaymentConfirmedAt: now.Add(-time.Hour), RecordOrigin: "native"}},
	}}
	states := map[int64]paymentport.DistributionPaymentState{
		3: {ConfirmedPaid: true, ConfirmedPaidAt: now.Add(-2 * time.Hour), SuccessfulRefundMinor: 1000},
		4: {ConfirmedPaid: true, ConfirmedPaidAt: now.Add(-time.Hour)},
	}
	service, _, _ := refundServiceFixture(t, store, evidence, purchase, states, now)
	if err := service.RunRefundRecheck(context.Background(), RefundRecheckJobArgs{OrderID: 3, State: "successful", OccurredAt: now, ReceiptKey: "qualification-a-refund"}); err != nil {
		t.Fatal(err)
	}
	if got := store.contexts[41].Commission; got.Status != distributiondomain.CommissionPending {
		t.Fatalf("replacement purchase should retain qualification: %+v", got)
	}

	states[4] = paymentport.DistributionPaymentState{ConfirmedPaid: true, ConfirmedPaidAt: now.Add(-time.Hour), SuccessfulRefundMinor: 1000}
	service, _, _ = refundServiceFixture(t, store, evidence, purchase, states, now.Add(time.Minute))
	if err := service.RunRefundRecheck(context.Background(), RefundRecheckJobArgs{OrderID: 4, State: "successful", OccurredAt: now.Add(time.Minute), ReceiptKey: "qualification-b-refund"}); err != nil {
		t.Fatal(err)
	}
	if got := store.contexts[41].Commission; got.Status != distributiondomain.CommissionCancelled || got.CancelReason != "qualification_revoked" {
		t.Fatalf("last qualification refund must cancel unsettled commission: %+v", got)
	}
}

func TestRefundFinalFailureDoesNotResumeWhileAnotherRefundIsExposed(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	value := refundContext(t, 17, 41, distributiondomain.CommissionHeld, 0, 77, now)
	store := &refundStoreStub{contexts: map[int64]distributionstore.SettlementContext{41: value}, settlements: map[int64]distributionstore.Settlement{}}
	service, due, _ := refundServiceFixture(t, store, refundEvidenceStub{rows: map[int64][]orderport.QualificationRefundEvidence{}}, nil, map[int64]paymentport.DistributionPaymentState{17: {ConfirmedPaid: true, RefundExposure: true, RequestedRefundMinor: 20}}, now)
	if err := service.RunRefundRecheck(context.Background(), RefundRecheckJobArgs{OrderID: 17, RefundID: 9, State: "final_failed", OccurredAt: now, ReceiptKey: "refund-failed-0001"}); err != nil {
		t.Fatal(err)
	}
	if got := store.contexts[41].Commission; got.Status != distributiondomain.CommissionHeld || got.HoldReason != "buyer_refund_pending" || len(due.ids) != 1 {
		t.Fatalf("in-flight sibling refund must retain hold: %+v due=%v", got, due.ids)
	}
}

func TestRefundConsumersOnlyPersistRecheckPointersInOriginatingTransaction(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	store := &refundStoreStub{contexts: map[int64]distributionstore.SettlementContext{}, settlements: map[int64]distributionstore.Settlement{}}
	service, _, rechecks := refundServiceFixture(t, store, refundEvidenceStub{rows: map[int64][]orderport.QualificationRefundEvidence{}}, nil, map[int64]paymentport.DistributionPaymentState{}, now)
	ctx := platformpostgres.BindTransaction(context.Background(), distributionNoopTx{})
	orderEvent := orderport.RefundSettlementEvent{Order: orderdomain.Snapshot{ID: 17, RecordOrigin: orderdomain.RecordOriginNative}, RefundedDelta: 10, OccurredAt: now, ReceiptKey: "refund-origin-0001"}
	if err := service.ConsumeRefundSettlementWithin(ctx, orderEvent); err != nil {
		t.Fatal(err)
	}
	if err := service.ConsumeRefundExposureWithin(ctx, paymentport.RefundExposureEvent{OrderID: 17, RefundID: 7, State: paymentport.RefundExposureOpened, OccurredAt: now, ReceiptKey: "refund-open-0001"}); err != nil {
		t.Fatal(err)
	}
	if len(rechecks.args) != 2 || store.listCalls != 0 || rechecks.args[0].State != "successful" || rechecks.args[1].State != "opened" {
		t.Fatalf("callbacks must only enqueue durable rechecks: args=%+v list_calls=%d", rechecks.args, store.listCalls)
	}
}

var _ refundStore = (*refundStoreStub)(nil)
var _ orderport.QualificationRefundEvidenceByOrderReader = refundEvidenceStub{}
var _ DueCheckEnqueuer = (*refundDueStub)(nil)
var _ RefundRecheckEnqueuer = (*refundRecheckStub)(nil)
