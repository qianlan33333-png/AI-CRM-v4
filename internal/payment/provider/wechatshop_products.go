package provider

import (
	"context"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"strings"
	"unicode/utf8"
)

func (p *ShopProvider) shopRead(ctx context.Context, path string, request, response any) error {
	if p == nil || !p.config.Enabled {
		return ErrInvalidConfig
	}
	raw, _, err := p.postWithToken(ctx, path, request, true)
	if err != nil {
		return ErrInvalidShopResponse
	} // do not expose token-bearing transport errors
	var envelope struct {
		ErrCode exactShopInteger `json:"errcode"`
	}
	if !decodeShopObject(raw, &envelope) || !envelope.ErrCode.set || envelope.ErrCode.value != 0 || !decodeShopObject(raw, response) {
		return ErrInvalidShopResponse
	}
	return nil
}
func (p *ShopProvider) ListShopProducts(ctx context.Context, next string) (shopport.ProductPage, error) {
	var response struct {
		Products []exactShopReference `json:"product_ids"`
		Next     string               `json:"next_key"`
	}
	err := p.shopRead(ctx, "/channels/ec/product/list/get", map[string]any{"status": 5, "page_size": 30, "next_key": next}, &response)
	if err != nil {
		return shopport.ProductPage{}, err
	}
	if response.Products == nil {
		return shopport.ProductPage{}, ErrInvalidShopResponse
	}
	page := shopport.ProductPage{Next: response.Next}
	for _, id := range response.Products {
		page.IDs = append(page.IDs, id.value)
	}
	return page, nil
}
func (p *ShopProvider) ReadShopProduct(ctx context.Context, id string) (shopport.Product, error) {
	var response struct {
		Product struct {
			ID     exactShopReference `json:"product_id"`
			Title  string             `json:"title"`
			Status exactShopInteger   `json:"status"`
			SKUs   []struct {
				ID    exactShopReference `json:"sku_id"`
				Price exactShopInteger   `json:"sale_price"`
				Attrs []struct {
					Value string `json:"attr_value"`
				} `json:"sku_attrs"`
			} `json:"skus"`
		} `json:"product"`
	}
	if err := p.shopRead(ctx, "/channels/ec/product/get", map[string]any{"product_id": id, "data_type": 1}, &response); err != nil {
		return shopport.Product{}, err
	}
	v := response.Product
	if !v.ID.set || v.ID.value != id || !v.Status.set {
		return shopport.Product{}, ErrInvalidShopResponse
	}
	result := shopport.Product{ID: id, Online: v.Status.value == 5}
	if !result.Online {
		return result, nil
	}
	if strings.TrimSpace(v.Title) == "" || len(v.SKUs) == 0 {
		return result, ErrInvalidShopResponse
	}
	for _, s := range v.SKUs {
		if !s.ID.set || !s.Price.set || s.Price.value < 0 {
			return shopport.Product{}, ErrInvalidShopResponse
		}
		name := v.Title
		for _, a := range s.Attrs {
			if a.Value != "" {
				name += " / " + a.Value
			}
		}
		// Product's existing name contract is 200 UTF-8 bytes. Source SKU IDs
		// remain exact even when a long title plus attributes exceeds it.
		if len(name) > 200 {
			name = name[:200]
			for !utf8.ValidString(name) {
				name = name[:len(name)-1]
			}
		}
		result.SKUs = append(result.SKUs, productport.ShopSKU{AppID: p.config.AppID, ProductID: id, SKUID: s.ID.value, Name: name, PriceMinor: s.Price.value})
	}
	return result, nil
}
