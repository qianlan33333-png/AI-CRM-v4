package port

import (
	"context"
	"time"
)

// PublicCoupon is the Coupon-owned state exposed by a stable public slug. It
// carries no channel identifier, payment, order or entitlement fact.
type PublicCoupon struct {
	Coupon
	PublicSlug string `json:"public_slug"`
}

type PublicCouponShare struct {
	CouponID   ID     `json:"coupon_id"`
	PublicSlug string `json:"public_slug"`
	URL        string `json:"url"`
}

// PublicCouponClaimState is a holder-scoped public projection. It has no
// claim reference, order, identity or payment data; it renders only the
// frozen page's already-claimed and user-limit state.
type PublicCouponClaimState struct{ ClaimCount int64 }

// PublicCouponApplication owns public-rule lookup, trusted-holder available
// claim reads, and the one-time durable slug assignment initiated by an admin
// share action. It never resolves or creates a Customer.
type PublicCouponApplication interface {
	GetPublicCoupon(context.Context, string) (PublicCoupon, error)
	PublicClaimState(context.Context, int64, ID) (PublicCouponClaimState, error)
	ListAvailableClaims(context.Context, int64, string, time.Time) ([]CustomerCoupon, error)
	EnsurePublicShare(context.Context, ID, int64) (PublicCouponShare, error)
}
