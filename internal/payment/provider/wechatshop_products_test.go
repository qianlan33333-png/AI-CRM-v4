package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWeChatShopProductsReadOnlineAndExactSKU(t *testing.T) {
	longName := false
	client := doerFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case shopTokenPath:
			return shopResponse(`{"access_token":"fixture-token","expires_in":7200}`), nil
		case "/channels/ec/product/list/get":
			if !strings.Contains(string(body), `"status":5`) {
				t.Fatal("not on sale")
			}
			return shopResponse(`{"errcode":0,"product_ids":[90071992547409931234],"next_key":""}`), nil
		case "/channels/ec/product/get":
			if !strings.Contains(string(body), `"data_type":1`) {
				t.Fatal("not online")
			}
			if longName {
				raw, _ := json.Marshal(map[string]any{"errcode": 0, "product": map[string]any{"product_id": "90071992547409931234", "title": strings.Repeat("课", 55), "status": 5, "skus": []any{map[string]any{"sku_id": "90071992547409939876", "sale_price": 100, "sku_attrs": []any{map[string]string{"attr_value": strings.Repeat("年", 50)}}}}}})
				return shopResponse(string(raw)), nil
			}
			return shopResponse(`{"errcode":0,"product":{"product_id":90071992547409931234,"title":"课程","status":5,"skus":[{"sku_id":90071992547409939876,"sale_price":0,"sku_attrs":[{"attr_value":"月度"}]}]}}`), nil
		}
		t.Fatal("unexpected path")
		return nil, nil
	})
	p, _ := NewWeChatShop(ShopConfig{Enabled: true, AppID: "test-shop", AppSecret: "fixture", APIBaseURL: "https://api.weixin.qq.com"}, loaderStub{}, client)
	page, e := p.ListShopProducts(context.Background(), "")
	if e != nil || len(page.IDs) != 1 || page.IDs[0] != "90071992547409931234" {
		t.Fatal(page, e)
	}
	product, e := p.ReadShopProduct(context.Background(), page.IDs[0])
	if e != nil || product.SKUs[0].SKUID != "90071992547409939876" || product.SKUs[0].Name != "课程 / 月度" || product.SKUs[0].PriceMinor != 0 {
		t.Fatal(product, e)
	}
	longName = true
	product, e = p.ReadShopProduct(context.Background(), page.IDs[0])
	if e != nil || len(product.SKUs[0].Name) > 200 || !utf8.ValidString(product.SKUs[0].Name) || !strings.HasPrefix(product.SKUs[0].Name, strings.Repeat("课", 55)) || product.SKUs[0].SKUID != "90071992547409939876" {
		t.Fatal("long online SKU cannot fit the existing catalog", e)
	}
}
