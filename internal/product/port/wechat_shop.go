package port

import (
	"context"
	"time"
)

const SourceWeChatShop = "wechat_shop"

type ShopSKU struct {
	AppID, ProductID, SKUID, Name string
	PriceMinor                    int64
}

// ShopCatalog owns bindings and product definitions. Calls join the caller's UoW.
type ShopCatalog interface {
	ImportShopSKUWithin(context.Context, ShopSKU, string, time.Time) (ID, bool, error)
	ResolveShopSKUWithin(context.Context, string, string, string) (ID, bool, error)
	ShopSKUCoordinatesWithin(context.Context, ID) (ShopSKU, bool, error)
}

type ShopProductWakeup interface {
	WakeShopProductWithin(context.Context, ID) error
}
