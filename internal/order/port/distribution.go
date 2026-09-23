package port

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// QualificationPurchaseQuery is a server-only Distribution request. It asks
// Order for payment-confirmed, exact-product item facts. Refund finality is
// intentionally absent: Payment owns that projection and Distribution must
// combine both Ports before calling a purchase eligible.
type QualificationPurchaseQuery struct {
	// CustomerIDs is the locked, de-duplicated canonical lineage supplied by
	// Identity. Order neither resolves identity nor guesses a merge root.
	CustomerIDs []int64
	ProductID   int64
	ProductType string
}

type QualificationPurchaseEvidence struct {
	OrderID, ProductID                     int64
	OrderItemLine                          int32
	PayerCustomerID, BeneficiaryCustomerID int64
	ItemPaidMinor                          int64
	PaymentConfirmedAt                     time.Time
	RecordOrigin                           string
}

func (e QualificationPurchaseEvidence) Reference() string {
	return "order:" + strconv.FormatInt(e.OrderID, 10) + ":item:" + strconv.FormatInt(int64(e.OrderItemLine), 10)
}

// QualificationPurchaseReader is the dedicated evidence Port. It must never
// be implemented by the generic Owned/purchase reader: it needs two trusted
// customer relationships, exact product type, durable payment confirmation,
// actual item payment and a complete immutable checkout snapshot.
type QualificationPurchaseReader interface {
	ListQualificationPurchaseEvidenceWithin(context.Context, QualificationPurchaseQuery) ([]QualificationPurchaseEvidence, error)
}

// HistoricalQualificationEvidence is produced only by the dedicated
// migration/import verifier after it has proved the same customer, product,
// payment and refund facts required of a native purchase. It is not an admin
// or public write API and cannot create a current payment effect.
type HistoricalQualificationEvidence struct {
	OrderID, ProductID                     int64
	OrderItemLine                          int32
	PayerCustomerID, BeneficiaryCustomerID int64
	ItemPaidMinor                          int64
	ProductType, SourceProductCode         string
	PaymentConfirmedAt                     time.Time
	// SourceOrderDigest binds the product mapping to the exact immutable
	// commerce-history order row. SourceDigest binds this controlled mapping
	// row itself; neither is caller commentary or an Owned/manual-note field.
	SourceOrderDigest [32]byte
	SourceDigest      [32]byte
}

// Valid checks only the immutable shape retained by Order. It deliberately
// does not establish identity, payment confirmation, or refund finality;
// those facts must be read through the dedicated Identity and Payment ports.
func (e HistoricalQualificationEvidence) Valid() bool {
	return e.OrderID > 0 && e.OrderItemLine > 0 && e.ProductID > 0 &&
		e.PayerCustomerID > 0 && e.BeneficiaryCustomerID > 0 &&
		e.ItemPaidMinor > 0 && (e.ProductType == "standard_product" || e.ProductType == "service_period") &&
		len(e.SourceProductCode) >= 1 && len(e.SourceProductCode) <= 200 &&
		e.SourceProductCode == strings.TrimSpace(e.SourceProductCode) &&
		!e.PaymentConfirmedAt.IsZero() && e.PaymentConfirmedAt.Location() == time.UTC &&
		e.SourceOrderDigest != ([32]byte{}) && e.SourceDigest != ([32]byte{})
}

type HistoricalQualificationEvidenceImporter interface {
	ImportHistoricalQualificationEvidenceWithin(context.Context, HistoricalQualificationEvidence) error
}

// QualificationRefundEvidence is the narrow immutable Order fact a refund
// worker needs before it can re-evaluate promotion eligibility. Native rows
// come from the immutable checkout/first-paid snapshots; history rows come
// only from a previously verifier-accepted qualification mapping. It never
// exposes a generic historical Order row or an Owned/manual-note
// approximation.
type QualificationRefundEvidence struct {
	OrderID, ProductID                     int64
	OrderItemLine                          int32
	ProductType                            string
	PayerCustomerID, BeneficiaryCustomerID int64
	ItemPaidMinor                          int64
	PaymentConfirmedAt                     time.Time
	RecordOrigin                           string
}

func (e QualificationRefundEvidence) Valid() bool {
	return e.OrderID > 0 && e.OrderItemLine > 0 && e.ProductID > 0 &&
		e.PayerCustomerID > 0 && e.BeneficiaryCustomerID > 0 && e.ItemPaidMinor > 0 &&
		(e.ProductType == "standard_product" || e.ProductType == "service_period") &&
		!e.PaymentConfirmedAt.IsZero() && (e.RecordOrigin == "native" || e.RecordOrigin == "history")
}

// QualificationRefundEvidenceByOrderReader is a transaction-bound refund
// seam. Callers must still re-check current Identity and Payment facts before
// changing commission state. The union is required for refund-opened and
// refund-final-failed events, whose Payment event has no checkout snapshot.
type QualificationRefundEvidenceByOrderReader interface {
	ListQualificationRefundEvidenceByOrderWithin(context.Context, int64) ([]QualificationRefundEvidence, error)
}

// HistoricalQualificationRefundEvidenceByOrderReader is retained briefly for
// callers migrating to QualificationRefundEvidenceByOrderReader. It exposes
// the same value shape but only a legacy method name; new refund consumers
// must use the union reader above.
type HistoricalQualificationRefundEvidence = QualificationRefundEvidence

type HistoricalQualificationEvidenceByOrderReader interface {
	ListHistoricalQualificationEvidenceByOrderWithin(context.Context, int64) ([]HistoricalQualificationRefundEvidence, error)
}

// HistoricalQualificationEvidenceVerifier is composition-owned. It is used
// only by an explicit migration/import command before Order writes an
// append-only history mapping. The verifier combines trusted Identity lineage
// and Payment finality/refund facts through their stable ports; Order never
// treats a historical order row or caller-provided customer IDs as proof.
type HistoricalQualificationEvidenceVerifier interface {
	VerifyHistoricalQualificationEvidenceWithin(context.Context, HistoricalQualificationEvidence) error
}
