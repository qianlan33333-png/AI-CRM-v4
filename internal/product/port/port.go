package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrProductReadNotFound    = errors.New("product read projection not found")
	ErrProductReadUnavailable = errors.New("product read projection unavailable")
	// ErrProductConflict is returned by a Product repository when a unique
	// local fact or compare-and-swap precondition cannot be satisfied.  The
	// application layer maps it to its stable conflict contract without making
	// the store import the application package.
	ErrProductConflict = errors.New("product persistence conflict")
)

type ID int64

type Product struct {
	ID                    ID                    `json:"id"`
	ProductCode           string                `json:"product_code"`
	Name                  string                `json:"name"`
	Description           string                `json:"description"`
	PriceMinor            int64                 `json:"price_minor"`
	Currency              string                `json:"currency"`
	StockQuantity         int32                 `json:"stock_quantity"`
	Images                []string              `json:"images"`
	CreatedBy             int64                 `json:"created_by"`
	CreatedAt             time.Time             `json:"created_at"`
	UpdatedAt             time.Time             `json:"updated_at"`
	Version               int64                 `json:"version"`
	LocalLifecycle        LocalProductLifecycle `json:"local_lifecycle,omitempty"`
	LegacyAdminProjection json.RawMessage       `json:"legacy_admin_projection"`
	PaidOrderCount        int64                 `json:"paid_order_count"`
	RefundOrderCount      int64                 `json:"refund_order_count"`
	SoldCount             int64                 `json:"sold_count"`
}

type SalesKey struct {
	ProductID   ID
	ProductCode string
}

type SalesSummary struct {
	PaidOrderCount   int64
	RefundOrderCount int64
	SoldCount        int64
}

// SalesSummaryReader is implemented at the composition boundary from stable
// Order and Payment read ports. Product never reads their tables directly.
type SalesSummaryReader interface {
	ReadSalesSummariesWithin(context.Context, []SalesKey) (map[ID]SalesSummary, error)
}

// DistributionPolicy is Product's transport-neutral policy snapshot. A nil command field means an older update caller intentionally leaves policy unchanged.
type DistributionPolicy struct {
	Enabled                   bool
	CommissionRateBasisPoints int32
	WaitDays                  int32
	ExpectedVersion           int64
}

func DefaultDistributionPolicy() DistributionPolicy {
	return DistributionPolicy{Enabled: false, CommissionRateBasisPoints: 0, WaitDays: 7, ExpectedVersion: 0}
}

type CreateCommand struct {
	ProductCode, Name, Description, Currency, IdempotencyKey string
	PriceMinor                                               int64
	StockQuantity                                            int32
	Images                                                   []string
	LegacyAdminProjection                                    json.RawMessage
	Actor                                                    int64
	DistributionPolicy                                       *DistributionPolicy
}

type UpdateCommand struct {
	ID                                          ID
	ExpectedVersion                             int64
	Name, Description, Currency, IdempotencyKey string
	PriceMinor                                  int64
	StockQuantity                               int32
	Images                                      []string
	LegacyAdminProjection                       json.RawMessage
	Actor                                       int64
	DistributionPolicy                          *DistributionPolicy
}

type Page struct {
	Items      []Product `json:"items"`
	NextCursor string    `json:"next_cursor"`
}

type LegacyPage struct {
	Items  []Product
	Total  int64
	Limit  int32
	Offset int32
}

type Reader interface {
	ReadProduct(context.Context, ID) (Product, error)
}
