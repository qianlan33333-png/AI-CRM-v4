package port

import (
	"context"
	"time"
)

// ClaimCommand contains a canonical Customer reference supplied by a trusted
// host adapter. It deliberately accepts no channel identity and cannot create
// or resolve a Customer.
type ClaimCommand struct {
	CouponID         ID
	HolderCustomerID int64
	ActorScope       string
	IdempotencyKey   string
	ClaimedAt        time.Time
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
