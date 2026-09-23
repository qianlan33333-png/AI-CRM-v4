package app

import (
	"context"
	"sort"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type qualificationPaymentReader interface {
	DistributionPaymentStateWithin(context.Context, int64) (paymentport.DistributionPaymentState, error)
}

// QualificationService combines immutable Order evidence, locked OneID roots,
// and Payment's refund projection. It has no direct cross-domain persistence
// access. Any unknown identity, evidence read, or refund outcome fails closed.
type QualificationService struct {
	lineage  identityport.LockedCanonicalLineageReader
	orders   orderport.QualificationPurchaseReader
	payments qualificationPaymentReader
	now      func() time.Time
}

func NewQualificationService(lineage identityport.LockedCanonicalLineageReader, orders orderport.QualificationPurchaseReader, payments qualificationPaymentReader) (*QualificationService, error) {
	if lineage == nil || orders == nil || payments == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &QualificationService{lineage: lineage, orders: orders, payments: payments, now: time.Now}, nil
}

func (s *QualificationService) CheckWithin(ctx context.Context, customerID, productID int64, productType distributiondomain.ProductType) (distributiondomain.Qualification, error) {
	if s == nil || s.lineage == nil || s.orders == nil || s.payments == nil || customerID < 1 || productID < 1 || !productType.Valid() {
		return distributiondomain.Qualification{}, distributionport.ErrQualification
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return distributiondomain.Qualification{}, distributionport.ErrQualification
	}
	now := s.now().UTC()
	if now.IsZero() {
		return distributiondomain.Qualification{}, distributionport.ErrQualification
	}
	roots, err := s.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(customerID))
	if err != nil || len(roots) == 0 || len(roots) > 100 {
		return unavailableQualification(now, "identity_evidence_unavailable"), nil
	}
	ids := make([]int64, 0, len(roots))
	allowed := make(map[int64]struct{}, len(roots))
	for _, root := range roots {
		id := int64(root)
		if id < 1 {
			return unavailableQualification(now, "identity_evidence_unavailable"), nil
		}
		allowed[id] = struct{}{}
	}
	if _, found := allowed[customerID]; !found {
		return distributiondomain.Qualification{State: distributiondomain.QualificationConflict, Reason: "identity_lineage_conflict", CheckedAt: now}, nil
	}
	for id := range allowed {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	evidence, err := s.orders.ListQualificationPurchaseEvidenceWithin(ctx, orderport.QualificationPurchaseQuery{CustomerIDs: ids, ProductID: productID, ProductType: string(productType)})
	if err != nil {
		return unavailableQualification(now, "purchase_evidence_unavailable"), nil
	}
	if len(evidence) == 0 {
		return distributiondomain.Qualification{State: distributiondomain.QualificationIneligible, Reason: "no_valid_purchase", CheckedAt: now}, nil
	}
	var eligibleRef string
	pendingRefund := false
	unavailableEvidence := false
	missingPaymentConfirmation := false
	for _, candidate := range evidence {
		if candidate.OrderID < 1 || candidate.OrderItemLine < 1 || candidate.ProductID != productID || candidate.ItemPaidMinor < 1 || candidate.PaymentConfirmedAt.IsZero() || (candidate.RecordOrigin != "native" && candidate.RecordOrigin != "history") {
			// A malformed candidate is no purchase proof. It cannot erase an
			// independent, complete item later in this same result set.
			continue
		}
		if _, payerOK := allowed[candidate.PayerCustomerID]; !payerOK {
			continue
		}
		if _, beneficiaryOK := allowed[candidate.BeneficiaryCustomerID]; !beneficiaryOK {
			continue
		}
		payment, paymentErr := s.payments.DistributionPaymentStateWithin(ctx, candidate.OrderID)
		if paymentErr != nil || !payment.ConfirmedPaid {
			// This item cannot prove a qualification, but a later independent
			// purchase can. A total Port failure is returned above; a per-order
			// unavailable mapping is retained as a fail-closed fallback only.
			unavailableEvidence = true
			continue
		}
		if payment.ConfirmedPaidAt.IsZero() {
			// Payment is known paid, but the immutable Provider confirmation time
			// is absent. Keep the gate closed and preserve this distinct recovery
			// path: only a signed Provider query can restore the fact.
			missingPaymentConfirmation = true
			continue
		}
		if !payment.ConfirmedPaidAt.UTC().Equal(candidate.PaymentConfirmedAt.UTC()) {
			unavailableEvidence = true
			continue
		}
		if payment.SuccessfulRefundMinor > 0 {
			continue
		}
		if payment.RefundExposure || payment.RequestedRefundMinor > 0 || payment.ProcessingRefundMinor > 0 || payment.OutcomeUnknownRefundMinor > 0 {
			pendingRefund = true
			continue
		}
		ref := candidate.Reference()
		if ref == "" || strings.TrimSpace(ref) != ref {
			return unavailableQualification(now, "purchase_evidence_unavailable"), nil
		}
		eligibleRef = ref
	}
	if eligibleRef != "" {
		return distributiondomain.Qualification{State: distributiondomain.QualificationEligible, Reason: "valid_paid_purchase", EvidenceRef: eligibleRef, CheckedAt: now}, nil
	}
	if pendingRefund {
		return distributiondomain.Qualification{State: distributiondomain.QualificationSuspended, Reason: "refund_outcome_pending", CheckedAt: now}, nil
	}
	if unavailableEvidence {
		return unavailableQualification(now, "payment_evidence_unavailable"), nil
	}
	if missingPaymentConfirmation {
		return unavailableQualification(now, "payment_confirmation_missing"), nil
	}
	return distributiondomain.Qualification{State: distributiondomain.QualificationIneligible, Reason: "no_valid_purchase", CheckedAt: now}, nil
}

// PaymentStateWithin exposes only Payment's locked, read-only settlement
// projection to Distribution workers. It exists so a refund recheck uses the
// authoritative cumulative successful-refund total rather than adding a
// callback delta that may be replayed or arrive out of order.
func (s *QualificationService) PaymentStateWithin(ctx context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	if s == nil || s.payments == nil || orderID < 1 {
		return paymentport.DistributionPaymentState{}, distributionport.ErrQualification
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return paymentport.DistributionPaymentState{}, distributionport.ErrQualification
	}
	return s.payments.DistributionPaymentStateWithin(ctx, orderID)
}

// MatchesTrustedCustomerWithin proves that both frozen purchase parties remain
// in one distributor's locked canonical lineage. It is used only to scope a
// refund fanout before CheckWithin performs the full payment/qualification
// decision; raw customer-ID equality is never used for this boundary.
func (s *QualificationService) MatchesTrustedCustomerWithin(ctx context.Context, customerID, payerCustomerID, beneficiaryCustomerID int64) (bool, error) {
	if s == nil || s.lineage == nil || customerID < 1 || payerCustomerID < 1 || beneficiaryCustomerID < 1 {
		return false, distributionport.ErrQualification
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return false, distributionport.ErrQualification
	}
	roots, err := s.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(customerID))
	if err != nil || len(roots) == 0 || len(roots) > 100 {
		return false, distributionport.ErrQualification
	}
	seen := make(map[int64]struct{}, len(roots))
	for _, root := range roots {
		if root < 1 {
			return false, distributionport.ErrQualification
		}
		seen[int64(root)] = struct{}{}
	}
	_, payerTrusted := seen[payerCustomerID]
	_, beneficiaryTrusted := seen[beneficiaryCustomerID]
	return payerTrusted && beneficiaryTrusted, nil
}

func unavailableQualification(now time.Time, reason string) distributiondomain.Qualification {
	if now.IsZero() {
		now = time.Unix(1, 0).UTC()
	}
	return distributiondomain.Qualification{State: distributiondomain.QualificationUnavailable, Reason: reason, CheckedAt: now}
}
