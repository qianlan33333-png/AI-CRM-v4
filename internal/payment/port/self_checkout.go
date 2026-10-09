package port

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

// SelfCheckoutReader exposes Payment-owned facts inside the caller's UoW.
// Authorization remains the trusted session/OneID consumer's responsibility.
// It supplies no handoff or capability to create a new checkout.
type SelfCheckoutReader interface {
	SelfCheckoutWithin(context.Context, int64) (domain.Payment, error)
}
