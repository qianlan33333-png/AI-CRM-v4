package port

import (
	"context"
	"errors"
	"time"
)

// ErrClaimNotEligible is returned by the OrderCouponCoordinator when an
// explicitly selected claim cannot be reserved for the requested checkout.
// Infrastructure failures use other errors so callers can retain 5xx
// semantics.
var ErrClaimNotEligible = errors.New("coupon claim not eligible for checkout")

// ClaimCommand carries the Host's immutable trusted identity handle for a
// public claim; Coupon asks Identity for its current owner within the UoW.
// Internal callers may supply their existing canonical Customer fact. This
// command accepts no raw channel identifier and never provisions a Customer.
type ClaimCommand struct {
	CouponID         ID
	HolderCustomerID int64
	// HolderIdentityID comes only from the Host's trusted session. Public
	// commands revalidate its current owner within Coupon's write UoW.
	// Zero retains the existing internal numeric-holder command contract.
	HolderIdentityID int64 `json:"-"`
	ActorScope       string
	IdempotencyKey   string
	ClaimedAt        time.Time
	// RecoveryOnly is selected by the Host for an unresolved original intent.
	// It may return this holder's retained receipt, but never issue a new claim.
	// It is not part of the immutable business payload or a browser identity.
	RecoveryOnly bool `json:"-"`
}

type ClaimApplication interface {
	Claim(context.Context, ClaimCommand) (CustomerCoupon, error)
}

// ReservationSnapshot is the immutable pricing fact an Order stores with its
// checkout snapshot. ReservationRef is opaque outside Coupon.
type ReservationSnapshot struct {
	// CouponApplied distinguishes an automatic checkout with no eligible
	// coupon from a real reservation. In the former case ReservationRef and
	// coupon fields are empty, while Product/pricing remain an explicit
	// original-price snapshot for Order to persist.
	CouponApplied       bool
	ReservationRef      string
	ClaimID             int64
	CouponID            ID
	ProductID           int64
	ProductType         string
	ProductCode         string
	RuleVersion         int64
	GrossAmountMinor    int64
	DiscountAmountMinor int64
	PayableAmountMinor  int64
	Currency            string
}

// ReserveCommand is passed by Order inside its pre-existing PostgreSQL UoW.
// When ClaimID is zero Coupon follows the donor's deterministic automatic
// selection order. ProductType is a stable Product selection value, not a
// table name.
type ReserveCommand struct {
	HolderCustomerID int64
	ClaimID          int64
	ProductID        int64
	ProductCode      string
	ProductType      string
	GrossAmountMinor int64
	Currency         string
	OrderReference   string
	ActorScope       string
	IdempotencyKey   string
	ReservedAt       time.Time
}

// ConsumeCommand accepts only payment-owner authoritative settlement facts.
type ConsumeCommand struct {
	ReservationRef     string
	OrderReference     string
	SettledAmountMinor int64
	SettledCurrency    string
	ActorScope         string
	IdempotencyKey     string
	SettledAt          time.Time
}

// ReleaseCommand is only for a payment-owner authoritative close fact.
// Unknown outcomes must not reach this method.
type ReleaseCommand struct {
	ReservationRef string
	OrderReference string
	CloseReason    string
	ActorScope     string
	IdempotencyKey string
	ClosedAt       time.Time
}

// OrderCouponCoordinator is the only cross-domain write seam from Order to
// Coupon. Its methods require a transaction-carrying context; implementations
// reject an unbound context rather than opening an independent commit.
type OrderCouponCoordinator interface {
	ReserveWithin(context.Context, ReserveCommand) (ReservationSnapshot, error)
	ConsumeWithin(context.Context, ConsumeCommand) (ReservationSnapshot, error)
	ReleaseWithin(context.Context, ReleaseCommand) (ReservationSnapshot, error)
}
