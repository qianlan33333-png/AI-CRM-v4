package port

import (
	"context"
	"crypto/sha256"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// SalesMetric is stored on the Referral-owned product campaign configuration.
// Both measures are retained on each sale event so changing the display sort
// never rewrites historical facts.
type SalesMetric string

const (
	SalesMetricAmount SalesMetric = "amount"
	SalesMetricOrders SalesMetric = "orders"
)

func (m SalesMetric) Valid() bool { return m == SalesMetricAmount || m == SalesMetricOrders }

// ProductSalePaidEvent is the versioned, server-fact event accepted from Order.
// It contains the frozen activity and promotion attribution; it never asks
// Referral to inspect Distribution or infer a current relationship.
type ProductSalePaidEvent struct {
	PaidEventID, OrderID, OrderVersion     int64
	ProductID                              int64
	ProductType                            string
	CampaignID                             int64
	PromotionCustomerID                    int64 // zero means this checkout had no promoter
	BuyerCustomerID, BeneficiaryCustomerID int64
	PaidAmountMinor                        int64
	Currency                               string
	ActivityContextDigest                  [sha256.Size]byte
	PromotionContextDigest                 [sha256.Size]byte
	SourceDigest                           [sha256.Size]byte
	OccurredAt                             time.Time
}

func (e ProductSalePaidEvent) Valid() bool {
	return e.PaidEventID > 0 && e.OrderID > 0 && e.OrderVersion > 0 && e.ProductID > 0 &&
		(e.ProductType == "standard_product" || e.ProductType == "service_period") && e.CampaignID > 0 &&
		e.BuyerCustomerID > 0 && e.BeneficiaryCustomerID > 0 && e.PaidAmountMinor > 0 && e.Currency == "CNY" &&
		e.ActivityContextDigest != ([sha256.Size]byte{}) && e.SourceDigest != ([sha256.Size]byte{}) && !e.OccurredAt.IsZero()
}

// ProductSaleRefundEvent is emitted only after Payment/Order has a confirmed
// refund settlement.  Replays use the same ReceiptKey and append at most one
// compensation row; an unknown or in-flight refund is not accepted here.
type ProductSaleRefundEvent struct {
	OrderID, ProductID                     int64
	ProductType                            string
	RefundedAmountMinor                    int64
	PaidEventID                            int64
	BuyerCustomerID, BeneficiaryCustomerID int64
	ReceiptKey                             string
	SourceDigest                           [sha256.Size]byte
	OccurredAt                             time.Time
}

func (e ProductSaleRefundEvent) Valid() bool {
	return e.OrderID > 0 && e.ProductID > 0 && (e.ProductType == "standard_product" || e.ProductType == "service_period") &&
		e.RefundedAmountMinor > 0 && e.PaidEventID >= 0 && e.BuyerCustomerID > 0 && e.BeneficiaryCustomerID > 0 &&
		e.ReceiptKey != "" && e.SourceDigest != ([sha256.Size]byte{}) && !e.OccurredAt.IsZero()
}

// ProductSalePaidConsumer and ProductSaleRefundConsumer are joined to
// Order's existing PaidEvent/RefundSettlementEvent fanout in Composition.
// They must run in the caller's PostgreSQL UoW; no private queue or retry loop
// is permitted in Referral.
type ProductSalePaidConsumer interface {
	ConsumeProductSalePaidWithin(context.Context, ProductSalePaidEvent) error
}

type ProductSaleRefundConsumer interface {
	ConsumeProductSaleRefundWithin(context.Context, ProductSaleRefundEvent) error
}

// ProductSaleContext is the checkout-time server resolution of an activity
// context. It is intentionally separate from CurrentRelationshipReader.
type ProductSaleContext struct {
	CampaignID, ProductID int64
	ProductType           string
	SalesMetric           SalesMetric
	ExpiresAt             time.Time
}

type ProductActivityContext struct {
	ContextDigest         [sha256.Size]byte
	CampaignID, ProductID int64
	ProductType           string
	SalesMetric           SalesMetric
	State                 string
	ExpiresAt, CreatedAt  time.Time
}

// ProductSaleContextReader is a Referral-owned lookup used by the checkout
// coordinator. The raw activity token stays at the trusted boundary; only its
// digest and the resulting campaign/product facts are frozen in Order.
type ProductSaleContextReader interface {
	ReadProductSaleContextWithin(context.Context, [sha256.Size]byte, int64, string, time.Time) (ProductSaleContext, error)
}

// ProductSaleCheckoutAttribution is called beside Distribution's existing
// checkout coordinator. It freezes the activity context and promoter snapshot
// in Referral-owned storage; payment confirmation later consumes the durable
// Order event and never re-derives a relationship.
type ProductSaleCheckoutAttribution = orderport.ProductSaleCheckoutContextCommand

type ProductSaleCheckoutCoordinator interface {
	RecordProductSaleCheckoutAttributionWithin(context.Context, ProductSaleCheckoutAttribution) error
}

type ProductSaleCheckoutSnapshot struct {
	OrderID, OrderVersion, CampaignID, ProductID int64
	ProductType                                  string
	ProductCode, ProductName                     string
	ProductVersion                               int64
	PromotionCustomerID                          int64
	PromotionCredentialRef                       string
	PolicyVersion                                int64
	CommissionRateBasisPoints                    int32
	WaitDays                                     int32
	BuyerCustomerID, BeneficiaryCustomerID       int64
	ActivityContextDigest                        [sha256.Size]byte
	PromotionContextDigest                       [sha256.Size]byte
	CreatedAt                                    time.Time
}
