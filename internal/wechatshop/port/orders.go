package port

import (
	"context"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"time"
)

type OrderPage struct {
	IDs     []string
	Next    string
	HasMore bool
}
type OrderReader interface {
	ListShopOrders(context.Context, time.Time, time.Time, string) (OrderPage, error)
	ReadShopOrder(context.Context, string) (orderport.ShopOrder, error)
}
type Callback struct {
	OrderID                 string
	EventDigest, BodyDigest [32]byte
	Relevant                bool
}
type CallbackVerifier interface {
	VerifyURL(context.Context, map[string]string) (string, error)
	VerifyEvent(context.Context, []byte, map[string]string) (Callback, error)
}
