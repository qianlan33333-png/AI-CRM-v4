package app

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type historicalLineageStub struct {
	lineage []customerdomain.CustomerID
	err     error
	seenID  customerdomain.CustomerID
}

func (s *historicalLineageStub) LockedCanonicalLineage(_ context.Context, id customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	s.seenID = id
	return s.lineage, s.err
}

var _ identityport.LockedCanonicalLineageReader = (*historicalLineageStub)(nil)

type historicalPaymentStub struct {
	paymentport.DistributionSettlementPort
	state  paymentport.DistributionPaymentState
	err    error
	seenID int64
}

func (s *historicalPaymentStub) DistributionPaymentStateWithin(_ context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	s.seenID = orderID
	return s.state, s.err
}

func verifiedHistoricalEvidence() orderport.HistoricalQualificationEvidence {
	return orderport.HistoricalQualificationEvidence{
		OrderID:               71,
		OrderItemLine:         1,
		ProductID:             9,
		ProductType:           "standard_product",
		SourceProductCode:     "course-9",
		PayerCustomerID:       41,
		BeneficiaryCustomerID: 42,
		ItemPaidMinor:         100,
		PaymentConfirmedAt:    time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC),
		SourceOrderDigest:     [32]byte{2},
		SourceDigest:          [32]byte{1},
	}
}

func verifiedHistoricalPayment(evidence orderport.HistoricalQualificationEvidence) paymentport.DistributionPaymentState {
	return paymentport.DistributionPaymentState{
		ConfirmedPaid:         true,
		OriginalMinor:         evidence.ItemPaidMinor,
		PayerCustomerID:       evidence.PayerCustomerID,
		BeneficiaryCustomerID: evidence.BeneficiaryCustomerID,
		ConfirmedPaidAt:       evidence.PaymentConfirmedAt,
	}
}

func TestHistoricalQualificationEvidenceVerifierRequiresTrustedExactFacts(t *testing.T) {
	evidence := verifiedHistoricalEvidence()
	lineage := &historicalLineageStub{lineage: []customerdomain.CustomerID{41, 42}}
	payments := &historicalPaymentStub{state: verifiedHistoricalPayment(evidence)}
	verifier := NewHistoricalQualificationEvidenceVerifier(lineage, payments)
	if err := verifier.VerifyHistoricalQualificationEvidenceWithin(context.Background(), evidence); err != nil {
		t.Fatalf("trusted evidence rejected: %v", err)
	}
	if lineage.seenID != customerdomain.CustomerID(evidence.PayerCustomerID) || payments.seenID != evidence.OrderID {
		t.Fatalf("lineage=%d payment order=%d", lineage.seenID, payments.seenID)
	}

	cases := []struct {
		name   string
		mutate func(*orderport.HistoricalQualificationEvidence, *paymentport.DistributionPaymentState, *historicalLineageStub)
	}{
		{name: "unconfirmed", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.ConfirmedPaid = false
		}},
		{name: "different payment payer", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.PayerCustomerID++
		}},
		{name: "different payment beneficiary", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.BeneficiaryCustomerID++
		}},
		{name: "smaller confirmed amount", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.OriginalMinor--
		}},
		{name: "missing payment confirmation time", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.ConfirmedPaidAt = time.Time{}
		}},
		{name: "different payment confirmation time", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.ConfirmedPaidAt = state.ConfirmedPaidAt.Add(time.Second)
		}},
		{name: "successful refund", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.SuccessfulRefundMinor = 1
		}},
		{name: "requested refund", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.RequestedRefundMinor = 1
		}},
		{name: "processing refund", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.ProcessingRefundMinor = 1
		}},
		{name: "unknown refund", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.OutcomeUnknownRefundMinor = 1
		}},
		{name: "refund exposure", mutate: func(_ *orderport.HistoricalQualificationEvidence, state *paymentport.DistributionPaymentState, _ *historicalLineageStub) {
			state.RefundExposure = true
		}},
		{name: "different canonical person", mutate: func(_ *orderport.HistoricalQualificationEvidence, _ *paymentport.DistributionPaymentState, lineage *historicalLineageStub) {
			lineage.lineage = []customerdomain.CustomerID{41, 99}
		}},
		{name: "unreadable lineage", mutate: func(_ *orderport.HistoricalQualificationEvidence, _ *paymentport.DistributionPaymentState, lineage *historicalLineageStub) {
			lineage.err = errors.New("identity read failed")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := evidence
			candidatePayment := verifiedHistoricalPayment(candidate)
			candidateLineage := &historicalLineageStub{lineage: []customerdomain.CustomerID{41, 42}}
			test.mutate(&candidate, &candidatePayment, candidateLineage)
			candidateVerifier := NewHistoricalQualificationEvidenceVerifier(candidateLineage, &historicalPaymentStub{state: candidatePayment})
			if err := candidateVerifier.VerifyHistoricalQualificationEvidenceWithin(context.Background(), candidate); err == nil {
				t.Fatal("untrusted evidence was accepted")
			}
		})
	}
}

type historicalImportStoreStub struct {
	*memoryStore
	imports        []orderport.HistoricalQualificationEvidence
	refundEvidence []orderport.HistoricalQualificationRefundEvidence
}

func (s *historicalImportStoreStub) ImportHistoricalQualificationEvidenceWithin(_ context.Context, evidence orderport.HistoricalQualificationEvidence) error {
	s.imports = append(s.imports, evidence)
	return nil
}

func (s *historicalImportStoreStub) ListHistoricalQualificationEvidenceByOrderWithin(_ context.Context, _ int64) ([]orderport.HistoricalQualificationRefundEvidence, error) {
	return append([]orderport.HistoricalQualificationRefundEvidence(nil), s.refundEvidence...), nil
}

func TestHistoricalQualificationEvidenceImportCallsVerifierBeforeOrderMapping(t *testing.T) {
	evidence := verifiedHistoricalEvidence()
	store := &historicalImportStoreStub{memoryStore: newMemoryStore()}
	service := NewService(directUOW{}, store)
	if err := service.SetHistoricalQualificationEvidenceVerifier(NewHistoricalQualificationEvidenceVerifier(
		&historicalLineageStub{lineage: []customerdomain.CustomerID{41, 42}},
		&historicalPaymentStub{state: verifiedHistoricalPayment(evidence)},
	)); err != nil {
		t.Fatal(err)
	}
	if err := service.ImportHistoricalQualificationEvidenceWithin(context.Background(), evidence); err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(store.imports) != 1 || store.imports[0] != evidence {
		t.Fatalf("imports=%+v", store.imports)
	}
}

func TestHistoricalQualificationEvidenceByOrderDoesNotReadGenericHistory(t *testing.T) {
	store := &historicalImportStoreStub{memoryStore: newMemoryStore(), refundEvidence: []orderport.HistoricalQualificationRefundEvidence{{
		OrderID:               71,
		OrderItemLine:         1,
		ProductID:             9,
		ProductType:           "standard_product",
		PayerCustomerID:       41,
		BeneficiaryCustomerID: 42,
		ItemPaidMinor:         100,
		PaymentConfirmedAt:    time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC),
	}}}
	service := NewService(directUOW{}, store)
	evidence, err := service.ListHistoricalQualificationEvidenceByOrderWithin(context.Background(), 71)
	if err != nil || len(evidence) != 1 || evidence[0].ProductID != 9 || evidence[0].PayerCustomerID != 41 || evidence[0].BeneficiaryCustomerID != 42 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
}
