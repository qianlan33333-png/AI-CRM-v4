package port

import (
	"context"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

type VariantSnapshot = domain.VariantSnapshot
type VariantAttribute = domain.VariantAttribute

// Caller must authorize the order through its trusted payment session first.
type CheckoutVariantReader interface {
	ReadCheckoutVariantWithin(context.Context, int64) (*VariantSnapshot, error)
}
