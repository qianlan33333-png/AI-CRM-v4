package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// PaidAudienceOrder is the minimum immutable business fact needed by the
// legacy paid-order audience condition. It intentionally has no beneficiary
// or provider identifiers.
type PaidAudienceOrder struct {
	OrderID     int64
	CustomerID  customerdomain.CustomerID
	ProductCode string
	// OwnerReference is the immutable owner recorded by the order source.  It
	// is deliberately empty when that source did not preserve ownership; the
	// audience adapter must not substitute a current WeCom relationship.
	OwnerReference string
	// PaidAt is nil when this historical order has no trustworthy payment-time
	// evidence. It can match an unbounded paid audience, never a time window.
	PaidAt *time.Time
}

type PaidAudienceReader interface {
	PaidAudienceOrders(context.Context, time.Time) ([]PaidAudienceOrder, error)
}

// PaidAudiencePurchase is one order's first immutable paid transition, even
// when the order is no longer currently paid. A nil PaidAt is an explicit
// unknown for a current paid order with no trustworthy history timestamp.
type PaidAudiencePurchase struct {
	OrderID     int64
	CustomerID  customerdomain.CustomerID
	ProductCode string
	PaidAt      *time.Time
}

// PaidAudiencePurchaseHistoryReader is a narrow history read used only by an
// explicitly configured first-purchase audience rule.
type PaidAudiencePurchaseHistoryReader interface {
	PaidAudiencePurchaseHistory(context.Context, []string, time.Time) ([]PaidAudiencePurchase, error)
}

// HistoricalAudienceProductReader validates an exact retained order-item code
// when its original product no longer exists in the current catalog. It never
// resolves titles, aliases, external identities or creates a product.
type HistoricalAudienceProductReader interface {
	HistoricalAudienceProductCodeExists(context.Context, string) (bool, error)
}
