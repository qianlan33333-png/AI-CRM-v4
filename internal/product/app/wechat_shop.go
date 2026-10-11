package app

import (
	"context"
	"encoding/json"
	"fmt"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"time"
)

type ShopCatalog struct {
	Store  productport.ShopCatalog
	Events productport.EventAppender
}

func (s ShopCatalog) ShopSKUCoordinatesWithin(ctx context.Context, id productport.ID) (productport.ShopSKU, bool, error) {
	return s.Store.ShopSKUCoordinatesWithin(ctx, id)
}

func (s ShopCatalog) ResolveShopSKUWithin(ctx context.Context, app, product, sku string) (productport.ID, bool, error) {
	return s.Store.ResolveShopSKUWithin(ctx, app, product, sku)
}
func (s ShopCatalog) ImportShopSKUWithin(ctx context.Context, sku productport.ShopSKU, run string, at time.Time) (productport.ID, bool, error) {
	id, added, err := s.Store.ImportShopSKUWithin(ctx, sku, run, at)
	if err == nil && added {
		raw, _ := json.Marshal(map[string]any{"product_id": id, "source": "wechat_shop"})
		_, err = s.Events.Append(ctx, productport.Event{Type: productport.EventProductCreated, Payload: raw, OccurredAt: at, IdempotencyKey: fmt.Sprintf("shop-product:%d", id)})
	}
	return id, added, err
}
