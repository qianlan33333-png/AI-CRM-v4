package port

import (
	"context"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"time"
)

type ProductPage struct {
	IDs  []string
	Next string
}
type Product struct {
	ID     string
	Online bool
	SKUs   []productport.ShopSKU
}
type ProductReader interface {
	ListShopProducts(context.Context, string) (ProductPage, error)
	ReadShopProduct(context.Context, string) (Product, error)
}
type RefreshStatus struct {
	ID            int64      `json:"id"`
	State         string     `json:"state"`
	Added         int64      `json:"added"`
	Existing      int64      `json:"existing"`
	Failed        int64      `json:"failed"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}
type ProductRefresh interface {
	SubmitProductRefresh(context.Context) (RefreshStatus, error)
	ProductRefreshStatus(context.Context) (RefreshStatus, error)
}
