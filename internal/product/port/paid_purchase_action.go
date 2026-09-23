package port

import (
	"context"
	"encoding/json"
	"time"
)

// PaidPurchaseActionMode is the buyer-facing action frozen when an eligible
// native checkout first settles. It is deliberately limited to locally
// rendered QR guidance and a safe redirect; neither mode is a Provider write.
type PaidPurchaseActionMode string

const (
	PaidPurchaseActionNone     PaidPurchaseActionMode = "none"
	PaidPurchaseActionQR       PaidPurchaseActionMode = "qr"
	PaidPurchaseActionRedirect PaidPurchaseActionMode = "redirect"
)

// PaidPurchaseAction is a Product-owned immutable snapshot. Payment authorizes
// the caller before exposing it, so this type does not carry a Customer,
// identity, session, Provider identifier, or External Effect payload.
type PaidPurchaseAction struct {
	OrderPaidEventID int64
	OrderID          int64
	ProductID        ID
	ProductVersion   int64
	SourceDigest     [32]byte `json:"-"`
	Enabled          bool
	Mode             PaidPurchaseActionMode
	LeadChannelID    int64
	LeadQRTitle      string
	LeadQRSubtitle   string
	RedirectURL      string
	// CompletionTarget holds the configured server-side URL Link source only
	// inside Product's paid-action snapshot. Payment may ask Product to resolve
	// it after it authorizes the exact paid checkout; it never returns it to a
	// browser.
	CompletionTarget json.RawMessage
	// CheckoutSnapshot marks actions frozen at native checkout creation. Older
	// paid-action rows intentionally keep false so their existing guidance
	// fallback remains available.
	CheckoutSnapshot bool
	TagState         string
	CreatedAt        time.Time
}

// PaidPurchaseActionReader is bound at composition to Payment's authenticated
// checkout-status route. Product owns the stored snapshot; Payment remains the
// sole owner of the trusted payer-session authorization.
type PaidPurchaseActionReader interface {
	ReadPaidPurchaseAction(context.Context, int64) (PaidPurchaseAction, error)
}

// PaidPurchaseGuidanceReader is read-only presentation, not a settlement snapshot.
type PaidPurchaseGuidanceReader interface {
	ReadPaidPurchaseGuidance(context.Context, int64) (PaidPurchaseAction, error)
}

// PaidPurchaseURLLinkResolver performs the optional legacy URL Link read only
// after Payment has authorized the exact paid checkout. It returns a safe
// destination, never the configured source URL or its credentials.
type PaidPurchaseURLLinkResolver interface {
	ResolvePaidPurchaseURLLink(context.Context, PaidPurchaseAction) (string, error)
}
