package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type distributionNoopTx struct{}

func (distributionNoopTx) Begin(context.Context) (pgx.Tx, error) { return distributionNoopTx{}, nil }
func (distributionNoopTx) Commit(context.Context) error          { return nil }
func (distributionNoopTx) Rollback(context.Context) error        { return nil }
func (distributionNoopTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not used")
}
func (distributionNoopTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (distributionNoopTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (distributionNoopTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not used")
}
func (distributionNoopTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not used")
}
func (distributionNoopTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not used")
}
func (distributionNoopTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (distributionNoopTx) Conn() *pgx.Conn                                  { return nil }

type lineageStub struct{ roots []customerdomain.CustomerID }

func (s lineageStub) LockedCanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return append([]customerdomain.CustomerID(nil), s.roots...), nil
}

var _ identityport.LockedCanonicalLineageReader = lineageStub{}

type qualificationOrderStub struct {
	evidence []orderport.QualificationPurchaseEvidence
}

func (s qualificationOrderStub) ListQualificationPurchaseEvidenceWithin(context.Context, orderport.QualificationPurchaseQuery) ([]orderport.QualificationPurchaseEvidence, error) {
	return append([]orderport.QualificationPurchaseEvidence(nil), s.evidence...), nil
}

type qualificationPaymentStub struct {
	states map[int64]paymentport.DistributionPaymentState
	errs   map[int64]error
}

func (s qualificationPaymentStub) DistributionPaymentState(_ context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	if err := s.errs[orderID]; err != nil {
		return paymentport.DistributionPaymentState{}, err
	}
	return s.states[orderID], nil
}

func (s qualificationPaymentStub) DistributionPaymentStateWithin(ctx context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	return s.DistributionPaymentState(ctx, orderID)
}

func qualificationContext() context.Context {
	return platformpostgres.BindTransaction(context.Background(), distributionNoopTx{})
}

func TestQualificationUsesAlternativeEvidenceWhenOnePaymentMappingUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	service, err := NewQualificationService(lineageStub{roots: []customerdomain.CustomerID{11, 12}}, qualificationOrderStub{evidence: []orderport.QualificationPurchaseEvidence{
		{OrderID: 100, OrderItemLine: 1, ProductID: 9, PayerCustomerID: 11, BeneficiaryCustomerID: 11, ItemPaidMinor: 1000, PaymentConfirmedAt: now, RecordOrigin: "history"},
		{OrderID: 101, OrderItemLine: 1, ProductID: 9, PayerCustomerID: 12, BeneficiaryCustomerID: 12, ItemPaidMinor: 800, PaymentConfirmedAt: now, RecordOrigin: "native"},
	}}, qualificationPaymentStub{states: map[int64]paymentport.DistributionPaymentState{101: {ConfirmedPaid: true, ConfirmedPaidAt: now}}, errs: map[int64]error{100: errors.New("unmapped")}})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	got, err := service.CheckWithin(qualificationContext(), 11, 9, distributiondomain.ProductTypeStandard)
	if err != nil || got.State != distributiondomain.QualificationEligible || got.EvidenceRef != "order:101:item:1" {
		t.Fatalf("qualification=%+v err=%v", got, err)
	}
}

func TestQualificationFailsClosedWhenEveryCandidateHasUnknownPaymentEvidence(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	service, err := NewQualificationService(lineageStub{roots: []customerdomain.CustomerID{11}}, qualificationOrderStub{evidence: []orderport.QualificationPurchaseEvidence{{OrderID: 100, OrderItemLine: 1, ProductID: 9, PayerCustomerID: 11, BeneficiaryCustomerID: 11, ItemPaidMinor: 1000, PaymentConfirmedAt: now, RecordOrigin: "history"}}}, qualificationPaymentStub{errs: map[int64]error{100: errors.New("unmapped")}})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	got, err := service.CheckWithin(qualificationContext(), 11, 9, distributiondomain.ProductTypeStandard)
	if err != nil || got.State != distributiondomain.QualificationUnavailable || got.Reason != "payment_evidence_unavailable" {
		t.Fatalf("qualification=%+v err=%v", got, err)
	}
}

func TestQualificationDoesNotLetPendingRefundHideAlternativeValidPurchase(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	service, err := NewQualificationService(lineageStub{roots: []customerdomain.CustomerID{11}}, qualificationOrderStub{evidence: []orderport.QualificationPurchaseEvidence{
		{OrderID: 100, OrderItemLine: 1, ProductID: 9, PayerCustomerID: 11, BeneficiaryCustomerID: 11, ItemPaidMinor: 1000, PaymentConfirmedAt: now, RecordOrigin: "native"},
		{OrderID: 101, OrderItemLine: 1, ProductID: 9, PayerCustomerID: 11, BeneficiaryCustomerID: 11, ItemPaidMinor: 900, PaymentConfirmedAt: now, RecordOrigin: "native"},
	}}, qualificationPaymentStub{states: map[int64]paymentport.DistributionPaymentState{100: {ConfirmedPaid: true, ConfirmedPaidAt: now, RefundExposure: true}, 101: {ConfirmedPaid: true, ConfirmedPaidAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	got, err := service.CheckWithin(qualificationContext(), 11, 9, distributiondomain.ProductTypeStandard)
	if err != nil || got.State != distributiondomain.QualificationEligible || got.EvidenceRef != "order:101:item:1" {
		t.Fatalf("qualification=%+v err=%v", got, err)
	}
}

func TestQualificationRejectsHistoricalPaymentWithoutImmutableConfirmation(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	service, err := NewQualificationService(lineageStub{roots: []customerdomain.CustomerID{11}}, qualificationOrderStub{evidence: []orderport.QualificationPurchaseEvidence{{OrderID: 100, OrderItemLine: 1, ProductID: 9, PayerCustomerID: 11, BeneficiaryCustomerID: 11, ItemPaidMinor: 1000, PaymentConfirmedAt: now, RecordOrigin: "history"}}}, qualificationPaymentStub{states: map[int64]paymentport.DistributionPaymentState{100: {ConfirmedPaid: true}}})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	got, err := service.CheckWithin(qualificationContext(), 11, 9, distributiondomain.ProductTypeStandard)
	if err != nil || got.State != distributiondomain.QualificationUnavailable || got.Reason != "payment_confirmation_missing" {
		t.Fatalf("qualification=%+v err=%v", got, err)
	}
}
