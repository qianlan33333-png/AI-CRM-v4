package port

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrInvalidProductOptionQuery  = errors.New("invalid product option query")
	ErrSaleableProductNotFound    = errors.New("saleable product not found")
	ErrSaleableProductUnavailable = errors.New("saleable product unavailable")
)

// ProductOptionType is the only product classification exposed to another
// domain for selecting a Product target. It is deliberately narrower than
// Product and contains no customer, order, entitlement, or provider facts.
type ProductOptionType string

const (
	ProductOptionStandard      ProductOptionType = "standard"
	ProductOptionServicePeriod ProductOptionType = "service_period"
	ProductOptionAll           ProductOptionType = "all"
	ProductOptionDefaultLimit                    = int32(50)
	ProductOptionMaximumLimit                    = int32(100)
	ProductOptionMaximumOffset                   = int32(1_000_000)
)

// ProductOptionQuery is a bounded read request for another domain's
// selection UI. ProductType may be standard, service_period, or all; an
// empty type is normalized to all by the Product application. q matches the
// Product code or display name and is never interpreted as SQL.
type ProductOptionQuery struct {
	Q           string            `json:"q,omitempty"`
	ProductType ProductOptionType `json:"product_type,omitempty"`
	Limit       int32             `json:"limit,omitempty"`
	Offset      int32             `json:"offset,omitempty"`
}

// ProductOption is the minimum target-selection projection. Prices are local
// CNY minor units: non-CNY rows are not eligible for this cross-domain
// projection and are omitted by the Product-owned implementation.
type ProductOption struct {
	ID          ID                `json:"id"`
	Code        string            `json:"code"`
	ProductType ProductOptionType `json:"product_type"`
	Name        string            `json:"name"`
	PriceMinor  int64             `json:"price_minor"`
	Currency    string            `json:"currency"`
	// CoverURL is Product's public first image for a message card. A consumer
	// that needs a card must reject an empty value rather than inventing a
	// generic cover.
	CoverURL string `json:"cover_url,omitempty"`
}

type ProductOptionPage struct {
	Items  []ProductOption `json:"items"`
	Total  int64           `json:"total"`
	Limit  int32           `json:"limit"`
	Offset int32           `json:"offset"`
}

// ProductOptionReader is the canonical cross-domain Product port. Consumers
// must not import Product app/store/http packages or query products directly.
// The implementation owns filtering, pagination, currency eligibility, and
// the stable projection.
type ProductOptionReader interface {
	ListProductOptions(context.Context, ProductOptionQuery) (ProductOptionPage, error)
}

// ProductTargetReader validates one already-selected local Product target for
// a rule in another domain.  It intentionally returns the same bounded
// projection as the chooser: consumers cannot read Product tables or infer
// lifecycle, order, entitlement, or provider state.
type ProductTargetReader interface {
	ReadProductTarget(context.Context, ProductOptionType, ID) (ProductOption, error)
}

// ProductTargetBatchMaximum covers the largest valid Coupon list page: 200
// rules, each with at most 100 persisted targets. It remains a hard bound for
// a single Product-owned SQL read; callers never fan it out into N target
// reads.
const ProductTargetBatchMaximum = 20_000

// ProductTargetReference identifies one persisted target without exposing a
// Product store or lifecycle implementation to another domain.
type ProductTargetReference struct {
	ProductType ProductOptionType `json:"product_type"`
	ID          ID                `json:"id"`
}

// ProductTargetLookup is the minimal historical target presentation. It
// deliberately exposes only a current display name plus existence; a Coupon
// list does not need Product price, lifecycle, inventory, or provider data.
// Found=false is reserved for a Product-owned not-found fact; an unavailable
// Product read is returned as an error so callers do not present it as a
// deleted product.
type ProductTargetLookup struct {
	Reference ProductTargetReference `json:"reference"`
	Name      string                 `json:"name,omitempty"`
	Found     bool                   `json:"found"`
}

// ProductTargetBatchReader resolves a bounded set of existing Coupon-like
// targets. It keeps product-name projection and not-found classification with
// Product, while consumers retain only their own target references.
type ProductTargetBatchReader interface {
	ReadProductTargets(context.Context, []ProductTargetReference) ([]ProductTargetLookup, error)
}

// SidebarShareProduct is the narrow current-lifecycle projection needed to
// form a sidebar product news card. It is deliberately separate from the
// generic target reader: a coupon may retain a historical target while an
// unpublished product must never be shared from the sidebar.
type SidebarShareProduct struct {
	ID          ID
	Code        string
	ProductType ProductOptionType
	Name        string
	CoverURL    string
}

// SidebarProductShareReader resolves one product at send-intent creation
// time. Implementations must reject drafts, disabled products, and archived
// service-period products from their current Product lifecycle.
type SidebarProductShareReader interface {
	ReadSidebarShareProduct(context.Context, ProductOptionType, ID) (SidebarShareProduct, error)
}

// CheckoutProduct is the immutable minimum required to freeze a sale into an
// Order. The Within method requires the caller's existing PostgreSQL UoW so a
// concurrent lifecycle/price change cannot be observed across transactions.
type PublicDetailMedia struct {
	ImageID int64
	Width   int32
	Height  int32
}

type CheckoutProduct struct {
	ID            ID
	ProductType   ProductOptionType
	Code          string
	Name          string
	PriceMinor    int64
	Currency      string
	Version       int64
	RequireMobile bool
	// ContactCollectionLevel controls the buyer contact fields collected by
	// the public checkout. It is none, mobile, or shipping_address.
	ContactCollectionLevel string
	// Images are public detail media owned by Product; checkout persists none of them.
	Images []string
	// DetailMedia is restricted to legacy-admin slices that Product owns.
	DetailMedia            []PublicDetailMedia
	LeadChannelID          int64
	LeadQRTitle            string
	LeadQRSubtitle         string
	CompletionBlocksLeadQR bool
	// PostPurchaseAction is Product's canonical, buyer-facing action snapshot.
	// Payment carries it unchanged into Order's immutable checkout fact; it is
	// never supplied by the browser or interpreted by Order.
	PostPurchaseAction json.RawMessage
	// ServicePeriodDurationDays is positive only for a service_period item.
	// It is frozen by the Order checkout snapshot before payment begins.
	ServicePeriodDurationDays int32
}

type CheckoutProductReader interface {
	ReadCheckoutProductWithin(context.Context, ProductOptionType, ID) (CheckoutProduct, error)
}
