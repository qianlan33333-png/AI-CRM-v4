package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWeChatShopProductsReadOnlineAndExactSKU(t *testing.T) {
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
			return shopResponse(`{"errcode":0,"product":{"product_id":90071992547409931234,"title":"课程","status":5,"skus":[{"sku_id":90071992547409939876,"sale_price":0,"sku_attrs":[{"value":"月度"}]}]}}`), nil
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
}
