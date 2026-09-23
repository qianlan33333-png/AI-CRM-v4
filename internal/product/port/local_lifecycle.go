package port

import (
	"context"
	"time"
)

// LocalProductLifecycle is the CRM-local state used by the legacy WeChat-pay
// product controls. It deliberately says nothing about provider configuration,
// purchase availability, or payment/entitlement effects.
type LocalProductLifecycle string

const (
	LocalProductDraft    LocalProductLifecycle = "draft"
	LocalProductEnabled  LocalProductLifecycle = "enabled"
	LocalProductDisabled LocalProductLifecycle = "disabled"
	// LocalProductArchived is a retained terminal state. It removes a product
	// from normal owner discovery and every new-sale entry point without
	// deleting immutable order and receipt facts.
	LocalProductArchived LocalProductLifecycle = "archived"
)

// LocalProduct is a closed projection for local lifecycle operations. The
// legacy_admin_projection is intentionally not exposed as an opaque browser
// contract; the write service preserves it only through its typed Product
// repository boundary.
type LocalProduct struct {
	ID            ID                    `json:"id"`
	ProductCode   string                `json:"product_code"`
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	PriceMinor    int64                 `json:"price_minor"`
	Currency      string                `json:"currency"`
	StockQuantity int32                 `json:"stock_quantity"`
	Images        []string              `json:"images"`
	CreatedBy     int64                 `json:"created_by"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Lifecycle     LocalProductLifecycle `json:"lifecycle"`
	Enabled       bool                  `json:"enabled"`
	Version       int64                 `json:"version"`
}

type SetLocalProductEnabledCommand struct {
	ID              ID
	ExpectedVersion int64
	Enabled         bool
	Actor           int64
	IdempotencyKey  string
}

type CopyLocalProductCommand struct {
	ID              ID
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
}

type DeleteLocalProductCommand struct {
	ID              ID
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
}

type ArchiveLocalProductCommand struct {
	ID              ID
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
}

type DeleteLocalProductResult struct {
	ProductID ID   `json:"product_id"`
	Deleted   bool `json:"deleted"`
}

// LocalProductShare contains a same-origin public product URL. Rendering a QR
// is a browser concern and does not send a Provider request.
type LocalProductShare struct {
	ProductID   ID                    `json:"product_id"`
	ProductCode string                `json:"product_code"`
	Lifecycle   LocalProductLifecycle `json:"lifecycle"`
	Available   bool                  `json:"available"`
	Reason      string                `json:"reason,omitempty"`
	PurchaseURL string                `json:"purchase_url,omitempty"`
	QRCodeURL   string                `json:"qr_code_url,omitempty"`
}

// LocalProductLifecycleApplication is the transport-neutral contract exposed
// to a legacy HTTP adapter. Implementations are local-only and must not call a
// payment provider or claim that a product is purchasable.
type LocalProductLifecycleApplication interface {
	SetLocalProductEnabled(context.Context, SetLocalProductEnabledCommand) (LocalProduct, error)
	CopyLocalProduct(context.Context, CopyLocalProductCommand) (LocalProduct, error)
	ArchiveLocalProduct(context.Context, ArchiveLocalProductCommand) (LocalProduct, error)
	// DeleteLocalProduct remains the narrowly scoped legacy physical-delete
	// operation for internal maintenance only. Owner-facing DELETE routes archive.
	DeleteLocalProduct(context.Context, DeleteLocalProductCommand) (DeleteLocalProductResult, error)
	ShareLocalProduct(context.Context, ID) (LocalProductShare, error)
}
