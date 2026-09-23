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

// HistoricalAudienceProductReader validates an exact retained order-item code
// when its original product no longer exists in the current catalog. It never
// resolves titles, aliases, external identities or creates a product.
type HistoricalAudienceProductReader interface {
	HistoricalAudienceProductCodeExists(context.Context, string) (bool, error)
}
