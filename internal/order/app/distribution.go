package app

import (
	"context"
	"errors"
	"sort"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type qualificationPurchaseStore interface {
	ListQualificationPurchaseEvidenceWithin(context.Context, orderport.QualificationPurchaseQuery) ([]orderport.QualificationPurchaseEvidence, error)
}

type historicalQualificationEvidenceStore interface {
	ImportHistoricalQualificationEvidenceWithin(context.Context, orderport.HistoricalQualificationEvidence) error
}

type qualificationRefundEvidenceByOrderStore interface {
	ListQualificationRefundEvidenceByOrderWithin(context.Context, int64) ([]orderport.QualificationRefundEvidence, error)
}

type historicalQualificationEvidenceByOrderStore interface {
	ListHistoricalQualificationEvidenceByOrderWithin(context.Context, int64) ([]orderport.HistoricalQualificationRefundEvidence, error)
}

type historicalQualificationEvidenceVerifier interface {
	VerifyHistoricalQualificationEvidenceWithin(context.Context, orderport.HistoricalQualificationEvidence) error
}

// HistoricalQualificationEvidenceVerifier is the one controlled bridge from a
// historical Order item to the evidence projection used by Distribution.  It
// has no Store and no Provider dependency: customer lineage and payment/refund
// facts remain owned by their respective domains and are read through stable
// ports inside the caller's already-open PostgreSQL transaction.
type HistoricalQualificationEvidenceVerifier struct {
	lineage  identityport.LockedCanonicalLineageReader
	payments paymentport.DistributionSettlementPort
}

func NewHistoricalQualificationEvidenceVerifier(lineage identityport.LockedCanonicalLineageReader, payments paymentport.DistributionSettlementPort) *HistoricalQualificationEvidenceVerifier {
	return &HistoricalQualificationEvidenceVerifier{lineage: lineage, payments: payments}
}

// VerifyHistoricalQualificationEvidenceWithin proves every condition which a
// native qualification purchase would need.  Caller-supplied customer IDs,
// payment time, and amount never become authoritative merely because they are
// present in a legacy row: they must agree with locked Identity and Payment
// projections.  Any unreadable or incomplete fact fails closed.
func (v *HistoricalQualificationEvidenceVerifier) VerifyHistoricalQualificationEvidenceWithin(ctx context.Context, evidence orderport.HistoricalQualificationEvidence) error {
	if v == nil || v.lineage == nil || v.payments == nil || !evidence.Valid() {
		return orderport.ErrConflict
	}
	// Keep the shared lock order aligned with Distribution qualification:
	// Identity lineage before the Payment row.  Payment refund commands only
	// take the latter, while concurrent qualification checks take both in this
	// order, so an import replay cannot invert their locks.
	lineage, err := v.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(evidence.PayerCustomerID))
	if err != nil {
		return orderport.ErrUnavailable
	}
	if !trustedSameCanonicalLineage(lineage, evidence.BeneficiaryCustomerID) {
		return orderport.ErrConflict
	}
	state, err := v.payments.DistributionPaymentStateWithin(ctx, evidence.OrderID)
	if err != nil {
		return historicalQualificationPaymentError(err)
	}
	if !state.ConfirmedPaid || state.PayerCustomerID != evidence.PayerCustomerID ||
		state.BeneficiaryCustomerID != evidence.BeneficiaryCustomerID || state.OriginalMinor < evidence.ItemPaidMinor ||
		state.ConfirmedPaidAt.IsZero() || !state.ConfirmedPaidAt.UTC().Equal(evidence.PaymentConfirmedAt.UTC()) ||
		state.RefundExposure || state.SuccessfulRefundMinor != 0 || state.RequestedRefundMinor != 0 ||
		state.ProcessingRefundMinor != 0 || state.OutcomeUnknownRefundMinor != 0 {
		return orderport.ErrConflict
	}
	return nil
}

func historicalQualificationPaymentError(err error) error {
	switch {
	case errors.Is(err, paymentport.ErrInvalid), errors.Is(err, paymentport.ErrConflict), errors.Is(err, paymentport.ErrNotFound):
		return orderport.ErrConflict
	default:
		return orderport.ErrUnavailable
	}
}

func trustedSameCanonicalLineage(lineage []customerdomain.CustomerID, beneficiaryID int64) bool {
	if len(lineage) == 0 || beneficiaryID < 1 {
		return false
	}
	seen := make(map[customerdomain.CustomerID]struct{}, len(lineage))
	matched := false
	for _, customerID := range lineage {
		if customerID < 1 {
			return false
		}
		if _, duplicate := seen[customerID]; duplicate {
			return false
		}
		seen[customerID] = struct{}{}
		if int64(customerID) == beneficiaryID {
			matched = true
		}
	}
	return matched
}

// SetHistoricalQualificationEvidenceVerifier wires the dedicated import
// verifier once at composition. Until then historical evidence is deliberately
// unavailable rather than being inferred from a history row.
func (s *Service) SetHistoricalQualificationEvidenceVerifier(verifier historicalQualificationEvidenceVerifier) error {
	if s == nil || verifier == nil || s.historicalEvidenceVerifier != nil {
		return orderport.ErrConflict
	}
	s.historicalEvidenceVerifier = verifier
	return nil
}

// ListQualificationPurchaseEvidenceWithin exposes Order's dedicated evidence
// projection to Distribution while preserving the caller's current UoW. It is
// never substituted with StandardPurchaseReader/Owned.
func (s *Service) ListQualificationPurchaseEvidenceWithin(ctx context.Context, query orderport.QualificationPurchaseQuery) ([]orderport.QualificationPurchaseEvidence, error) {
	if !ready(s) || query.ProductID < 1 || (query.ProductType != "standard_product" && query.ProductType != "service_period") {
		return nil, orderport.ErrConflict
	}
	query.CustomerIDs = uniquePositiveCustomerIDs(query.CustomerIDs)
	if len(query.CustomerIDs) == 0 || len(query.CustomerIDs) > 100 {
		return nil, orderport.ErrConflict
	}
	store, ok := s.store.(qualificationPurchaseStore)
	if !ok {
		return nil, orderport.ErrUnavailable
	}
	rows, err := store.ListQualificationPurchaseEvidenceWithin(ctx, query)
	if err != nil {
		return nil, classify(err)
	}
	return rows, nil
}

// ImportHistoricalQualificationEvidenceWithin is intentionally a migration
// seam. It joins the caller's Order UoW: locked Payment, locked Identity, and
// append-only Order mapping facts must observe one PostgreSQL transaction. It
// neither infers evidence nor starts money movement.
func (s *Service) ImportHistoricalQualificationEvidenceWithin(ctx context.Context, evidence orderport.HistoricalQualificationEvidence) error {
	if !ready(s) || s.historicalEvidenceVerifier == nil {
		return orderport.ErrUnavailable
	}
	store, ok := s.store.(historicalQualificationEvidenceStore)
	if !ok {
		return orderport.ErrUnavailable
	}
	if err := s.historicalEvidenceVerifier.VerifyHistoricalQualificationEvidenceWithin(ctx, evidence); err != nil {
		return classify(err)
	}
	return classify(store.ImportHistoricalQualificationEvidenceWithin(ctx, evidence))
}

// ListQualificationRefundEvidenceByOrderWithin returns every immutable Order
// product fact that a refund can affect: native checkout/first-paid evidence
// and previously verifier-accepted historical mappings. It is deliberately
// unavailable for generic history rows, Owned flags, and manual notes.
func (s *Service) ListQualificationRefundEvidenceByOrderWithin(ctx context.Context, orderID int64) ([]orderport.QualificationRefundEvidence, error) {
	if !ready(s) || orderID < 1 {
		return nil, orderport.ErrConflict
	}
	store, ok := s.store.(qualificationRefundEvidenceByOrderStore)
	if !ok {
		return nil, orderport.ErrUnavailable
	}
	rows, err := store.ListQualificationRefundEvidenceByOrderWithin(ctx, orderID)
	if err != nil {
		return nil, classify(err)
	}
	return rows, nil
}

// ListHistoricalQualificationEvidenceByOrderWithin is retained for callers
// which explicitly need only verifier-accepted historical mappings. New
// refund consumers must use ListQualificationRefundEvidenceByOrderWithin.
func (s *Service) ListHistoricalQualificationEvidenceByOrderWithin(ctx context.Context, orderID int64) ([]orderport.HistoricalQualificationRefundEvidence, error) {
	if !ready(s) || orderID < 1 {
		return nil, orderport.ErrConflict
	}
	store, ok := s.store.(historicalQualificationEvidenceByOrderStore)
	if !ok {
		return nil, orderport.ErrUnavailable
	}
	rows, err := store.ListHistoricalQualificationEvidenceByOrderWithin(ctx, orderID)
	if err != nil {
		return nil, classify(err)
	}
	return rows, nil
}

func uniquePositiveCustomerIDs(values []int64) []int64 {
	result := append([]int64(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	write := 0
	for _, value := range result {
		if value < 1 || write > 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

var _ orderport.QualificationPurchaseReader = (*Service)(nil)
var _ orderport.HistoricalQualificationEvidenceImporter = (*Service)(nil)
var _ orderport.QualificationRefundEvidenceByOrderReader = (*Service)(nil)
var _ orderport.HistoricalQualificationEvidenceByOrderReader = (*Service)(nil)
var _ orderport.HistoricalQualificationEvidenceVerifier = (*HistoricalQualificationEvidenceVerifier)(nil)
