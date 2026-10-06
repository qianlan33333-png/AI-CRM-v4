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
// share action. It accepts no raw channel identifier and never provisions a
// Customer; trusted public subject reads use the separate Identity application.
type PublicCouponApplication interface {
	GetPublicCoupon(context.Context, string) (PublicCoupon, error)
	PublicClaimState(context.Context, int64, ID) (PublicCouponClaimState, error)
	ListAvailableClaims(context.Context, int64, string, time.Time) ([]CustomerCoupon, error)
	EnsurePublicShare(context.Context, ID, int64) (PublicCouponShare, error)
}

// PublicCouponIdentityApplication reads a trusted session's immutable handle
// and its Coupon facts in one transaction. The legacy numeric-holder port
// remains available to internal callers that already own canonical facts.
type PublicCouponIdentityApplication interface {
	PublicClaimStateForIdentity(context.Context, int64, ID) (PublicCouponClaimState, error)
	ListAvailableClaimsForIdentity(context.Context, int64, string, time.Time) ([]CustomerCoupon, error)
}
