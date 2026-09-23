package port

import (
	"context"
	"time"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	// SidebarClaimableDefaultLimit keeps the Coupon-owned directory smaller
	// than the legacy host's unbounded accumulation. The sidebar HTTP adapter
	// may choose a lower default, but every Catalog request remains bounded.
	SidebarClaimableDefaultLimit  int32 = 20
	SidebarClaimableMaximumLimit  int32 = 50
	SidebarClaimableMaximumOffset int32 = 1_000_000
)

// SidebarClaimableQuery is a bounded read of public Coupon definitions. It
// does not accept a status filter: callers must receive the existing Coupon
// availability state instead of silently treating a directory as an eligible
// claim set.
type SidebarClaimableQuery struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

// SidebarClaimableTarget is a display-only target projection. It contains no
// Product storage, checkout, order, entitlement, or customer detail.
type SidebarClaimableTarget struct {
	Title       string                        `json:"title"`
	ProductType productport.ProductOptionType `json:"product_type"`
}

// SidebarClaimableItem is a public Coupon definition projected for a scoped
// sidebar customer. AvailabilityStatus is Coupon's established global state;
// UserLimitReached is only that customer's durable claim-count fact. The
// public claim handler remains the only authority that can accept a claim.
// PublicSlug is optional because only an explicit Coupon share action may
// create it; a Host must never synthesize a public URL for an empty slug.
type SidebarClaimableItem struct {
	CouponID           ID                       `json:"coupon_id"`
	Name               string                   `json:"name"`
	DiscountMinor      int64                    `json:"discount_minor"`
	Currency           string                   `json:"currency"`
	Targets            []SidebarClaimableTarget `json:"targets"`
	ClaimEndsAt        time.Time                `json:"claim_ends_at"`
	PublicSlug         string                   `json:"public_slug,omitempty"`
	AvailabilityStatus string                   `json:"availability_status"`
	UserLimitReached   bool                     `json:"user_limit_reached"`
}

type SidebarClaimablePage struct {
	Items  []SidebarClaimableItem `json:"items"`
	Total  int64                  `json:"total"`
	Limit  int32                  `json:"limit"`
	Offset int32                  `json:"offset"`
}

// SidebarClaimableCatalog is Coupon-owned. The customer ID must be an already
// trusted canonical Customer supplied by the sidebar boundary; this Port does
// not resolve identities, allocate a claim, reserve capacity, or mutate a
// public slug.
type SidebarClaimableCatalog interface {
	ListSidebarClaimable(context.Context, int64, SidebarClaimableQuery) (SidebarClaimablePage, error)
	// ReadSidebarClaimable resolves one exact visible item for an authenticated
	// sidebar customer. It never creates a slug, claim, reservation, or stock
	// allocation.
	ReadSidebarClaimable(context.Context, int64, ID) (SidebarClaimableItem, error)
}

type CustomerCoupon struct {
	ClaimID       int64      `json:"claim_id"`
	CouponID      int64      `json:"coupon_id"`
	Name          string     `json:"name"`
	DiscountMinor int64      `json:"discount_minor"`
	Currency      string     `json:"currency"`
	Status        string     `json:"status"`
	ClaimNoMasked string     `json:"claim_no_masked,omitempty"`
	ClaimedAt     time.Time  `json:"claimed_at"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	RedeemedAt    *time.Time `json:"redeemed_at,omitempty"`
}

type CustomerCouponPage struct {
	Items []CustomerCoupon `json:"items"`
	Total int64            `json:"total"`
}

type CustomerCouponReader interface {
	ListCustomerCoupons(context.Context, int64, int32) (CustomerCouponPage, error)
}

// AdminCouponClaim is a masked, local Customer projection for the frozen
// coupon-data page. It exposes no channel identifier, payment or order fact.
type AdminCouponClaim struct {
	ClaimID       int64      `json:"claim_id"`
	CustomerID    int64      `json:"customer_id"`
	CouponID      int64      `json:"coupon_id"`
	Status        string     `json:"status"`
	ClaimNoMasked string     `json:"claim_no_masked,omitempty"`
	ClaimedAt     time.Time  `json:"claimed_at"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	RedeemedAt    *time.Time `json:"redeemed_at,omitempty"`
}

type AdminCouponClaimPage struct {
	Items  []AdminCouponClaim `json:"items"`
	Total  int64              `json:"total"`
	Limit  int32              `json:"limit"`
	Offset int32              `json:"offset"`
}

// CouponClaimAdminReader is Coupon-owned. It is deliberately separate from
// the Customer sidebar reader so an admin page cannot infer customer identity.
type CouponClaimAdminReader interface {
	ListCouponClaims(context.Context, ID, int32, int32) (AdminCouponClaimPage, error)
}

type HistoricalCustomerCoupon struct {
	SourceSystem  string
	SourceKey     string
	CustomerID    int64
	CouponID      int64
	Status        string
	ClaimNoMasked string
	ClaimedAt     time.Time
	ValidFrom     *time.Time
	ValidUntil    *time.Time
	RedeemedAt    *time.Time
	SourceDigest  [32]byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type HistoricalCustomerCouponImporter interface {
	ImportHistoricalCustomerCoupon(context.Context, HistoricalCustomerCoupon) (CustomerCoupon, bool, error)
}
