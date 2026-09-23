package port

import (
	"context"
	"encoding/json"
	"time"
)

// ServicePeriodLifecycle is a local Product lifecycle fact. It does not imply
// that a product is saleable, that payment is configured, or that an
// entitlement has taken effect outside the local database.
type ServicePeriodLifecycle string

const (
	ServicePeriodDraft    ServicePeriodLifecycle = "draft"
	ServicePeriodEnabled  ServicePeriodLifecycle = "enabled"
	ServicePeriodDisabled ServicePeriodLifecycle = "disabled"
	ServicePeriodArchived ServicePeriodLifecycle = "archived"
)

// ServicePeriodProduct is the closed legacy projection over the authoritative
// products row. It intentionally excludes legacy_admin_projection, actor
// identity, provider material, receipts, and other opaque metadata.
type ServicePeriodProduct struct {
	ServiceProductID ID                     `json:"service_product_id"`
	ProductCode      string                 `json:"product_code"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	PriceMinor       int64                  `json:"price_minor"`
	Currency         string                 `json:"currency"`
	DurationDays     int32                  `json:"duration_days"`
	StockQuantity    int32                  `json:"stock_quantity"`
	Images           []string               `json:"images"`
	AdminProjection  json.RawMessage        `json:"admin_projection"`
	Lifecycle        ServicePeriodLifecycle `json:"lifecycle"`
	Enabled          bool                   `json:"enabled"`
	Archived         bool                   `json:"archived"`
	Version          int64                  `json:"version"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

type ServicePeriodPage struct {
	OK     bool                   `json:"ok"`
	Items  []ServicePeriodProduct `json:"items"`
	Total  int64                  `json:"total"`
	Limit  int32                  `json:"limit"`
	Offset int32                  `json:"offset"`
}

type CreateServicePeriodProductCommand struct {
	ProductCode        string
	Name               string
	Description        string
	PriceMinor         int64
	Currency           string
	DurationDays       int32
	StockQuantity      int32
	Images             []string
	AdminProjection    json.RawMessage
	Actor              int64
	IdempotencyKey     string
	DistributionPolicy *DistributionPolicy
}

type UpdateServicePeriodProductCommand struct {
	ID                 ID
	ExpectedVersion    int64
	Name               string
	Description        string
	PriceMinor         int64
	Currency           string
	DurationDays       int32
	StockQuantity      int32
	Images             []string
	AdminProjection    json.RawMessage
	Actor              int64
	IdempotencyKey     string
	DistributionPolicy *DistributionPolicy
}

type SetServicePeriodProductEnabledCommand struct {
	ID              ID
	ExpectedVersion int64
	Enabled         bool
	Actor           int64
	IdempotencyKey  string
}

type CopyServicePeriodProductCommand struct {
	ID              ID
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
}

type ArchiveServicePeriodProductCommand struct {
	ID              ID
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
}

// ServicePeriodApplication is the local-only legacy lifecycle contract used by
// the HTTP adapter. Implementations must keep every write in one UnitOfWork.
type ServicePeriodApplication interface {
	ListServicePeriodProducts(context.Context, int32, int32) (ServicePeriodPage, error)
	GetServicePeriodProduct(context.Context, ID) (ServicePeriodProduct, error)
	CreateServicePeriodProduct(context.Context, CreateServicePeriodProductCommand) (ServicePeriodProduct, error)
	UpdateServicePeriodProduct(context.Context, UpdateServicePeriodProductCommand) (ServicePeriodProduct, error)
	SetServicePeriodProductEnabled(context.Context, SetServicePeriodProductEnabledCommand) (ServicePeriodProduct, error)
	CopyServicePeriodProduct(context.Context, CopyServicePeriodProductCommand) (ServicePeriodProduct, error)
	ArchiveServicePeriodProduct(context.Context, ArchiveServicePeriodProductCommand) (ServicePeriodProduct, error)
}

// ServicePeriodPublicReader resolves only an enabled service-period product by
// its exact public code. It deliberately has no numeric-ID fallback, so an
// ordinary Product alias can never select a service-period Product.
// Payment uses the returned immutable checkout facts in its own transaction.
type ServicePeriodPublicReader interface {
	ReadPublicServicePeriodByCode(context.Context, string) (CheckoutProduct, error)
}

// ServicePeriodPublicPresentationReader returns a strict-code public
// presentation for an existing service-period item even when it is disabled.
// Its availability boolean is the only lifecycle fact exposed to the public
// Host so it can render the donor's unavailable card while keeping checkout
// closed. It has no numeric fallback and never reads ordinary products.
type ServicePeriodPublicPresentationReader interface {
	ReadServicePeriodPublicPresentationByCode(context.Context, string) (CheckoutProduct, bool, error)
}
