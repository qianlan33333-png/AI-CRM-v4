package port

import "context"

// CheckoutMobileReader returns only the Order-owned frozen checkout contact.
// Absence is distinct from unavailable/corrupt encrypted data. Values are PII.
type CheckoutMobileReader interface {
	ReadCheckoutMobileWithin(context.Context, int64) (string, bool, error)
}
