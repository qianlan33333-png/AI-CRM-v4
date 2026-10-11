package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWeChatShopRejectsNonIdentifierJSON(t *testing.T) {
	for _, raw := range []string{"null", "true", "false", "{}", "[]", "-1", "1.5", "1e3", `""`, `" ID "`} {
		t.Run(raw, func(t *testing.T) {
			var ref exactShopReference
			if json.Unmarshal([]byte(raw), &ref) == nil {
				t.Fatal("non-identifier accepted", raw)
			}
		})
	}
	for _, raw := range []string{"0", "90071992547409931234", `"90071992547409931234"`, `"refund-1"`} {
		t.Run("valid-"+raw, func(t *testing.T) {
			var ref exactShopReference
			if json.Unmarshal([]byte(raw), &ref) != nil || !ref.set || ref.value != strings.Trim(raw, `"`) {
				t.Fatal("exact source identifier lost")
			}
		})
	}
}

func shopAdversarialReader(t *testing.T, response string) *ShopProvider {
	t.Helper()
	p, e := NewWeChatShop(ShopConfig{Enabled: true, AppID: "shop-app", AppSecret: "fixture", APIBaseURL: "https://api.weixin.qq.com"}, loaderStub{}, doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == shopTokenPath {
			return shopResponse(`{"access_token":"fixture-token","expires_in":7200}`), nil
		}
		return shopResponse(response), nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestWeChatShopIncompletePagesDoNotMeanEndOfWindow(t *testing.T) {
	cases := []struct {
		name, response string
		products       bool
	}{
		{"missing-product-list", `{"errcode":0}`, true},
		{"null-product-list", `{"errcode":0,"product_ids":null}`, true},
		{"missing-order-list", `{"errcode":0,"has_more":false}`, false},
		{"null-order-list", `{"errcode":0,"order_id_list":null,"has_more":false}`, false},
		{"missing-terminal-flag", `{"errcode":0,"order_id_list":[]}`, false},
		{"null-terminal-flag", `{"errcode":0,"order_id_list":[],"has_more":null}`, false},
		{"missing-cursor", `{"errcode":0,"order_id_list":["order"],"has_more":true}`, false},
		{"missing-success-code", `{"order_id_list":[],"has_more":false}`, false},
		{"rate-limited", `{"errcode":45009,"errmsg":"fixture-sensitive"}`, false},
		{"expired-token-after-renewal", `{"errcode":40014}`, true},
		{"duplicate-terminal-flag", `{"errcode":0,"order_id_list":[],"has_more":true,"has_more":false}`, false},
		{"escaped-duplicate-flag", `{"errcode":0,"order_id_list":[],"has_more":true,"has_\u006dore":false}`, false},
		{"duplicate-source-id", `{"errcode":0,"product_ids":[{}],"next_key":""}`, true},
		{"non-json", `<html>upstream failure</html>`, true},
		{"truncated-json", `{"errcode":0,"order_id_list":["order"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := shopAdversarialReader(t, tc.response)
			var e error
			if tc.products {
				_, e = p.ListShopProducts(context.Background(), "")
			} else {
				_, e = p.ListShopOrders(context.Background(), time.Unix(1700000000, 0), time.Unix(1700086400, 0), "")
			}
			if e == nil {
				t.Fatal("incomplete/error page silently accepted as complete")
			}
			if strings.Contains(e.Error(), "fixture-sensitive") || strings.Contains(e.Error(), "fixture-token") {
				t.Fatal("unsafe provider error")
			}
		})
	}
	for _, products := range []bool{true, false} {
		raw := `{"errcode":0,"order_id_list":[],"has_more":false}`
		if products {
			raw = `{"errcode":0,"product_ids":[],"next_key":""}`
		}
		p := shopAdversarialReader(t, raw)
		if products {
			if v, e := p.ListShopProducts(context.Background(), ""); e != nil || len(v.IDs) != 0 {
				t.Fatal("valid empty product page", e)
			}
		} else {
			if v, e := p.ListShopOrders(context.Background(), time.Unix(1700000000, 0), time.Unix(1700086400, 0), ""); e != nil || len(v.IDs) != 0 || v.HasMore {
				t.Fatal("valid empty order page", e)
			}
		}
	}
}

func TestWeChatShopMalformedPaidDetailCannotBeSkipped(t *testing.T) {
	for _, raw := range []string{
		`{"errcode":0,"order":{"order_id":"order","status":20}}`,
		`{"errcode":0,"order":{"order_id":"order","status":20,"order_detail":{"pay_info":{"payment_method":1,"pay_time":0}}}}`,
		`{"errcode":0,"order":{"order_id":"order","create_time":1700000000,"order_detail":{"price_info":{"order_price":100},"pay_info":{"pay_time":1700000001},"product_infos":[{"product_id":"p","sku_id":"s","title":"fixture","sale_price":100,"real_price":100,"sku_cnt":1}]}}}`,
		`{"errcode":0,"order":{"order_id":"order","create_time":1700000000,"order_detail":{"price_info":{"order_price":100},"pay_info":{"payment_method":1,"pay_time":1700000001},"product_infos":[{"product_id":"p","sku_id":"s","title":"fixture","sale_price":100,"real_price":100,"sku_cnt":1}]},"aftersale_detail":{"aftersale_order_list":[{}]}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, e := shopAdversarialReader(t, raw).ReadShopOrder(context.Background(), "order"); e == nil {
				t.Fatal("malformed paid/refund fact accepted")
			}
		})
	}
	for _, status := range []string{"10", "250"} {
		p := shopAdversarialReader(t, `{"errcode":0,"order":{"order_id":"order","status":`+status+`}}`)
		if v, e := p.ReadShopOrder(context.Background(), "order"); e != nil || !v.PaidAt.IsZero() {
			t.Fatal("unpaid/cancelled without a payment should remain ineligible", e)
		}
	}
}

func TestWeChatShopReadTokenRenewalAndConcurrentCache(t *testing.T) {
	var tokens, reads atomic.Int32
	p, e := NewWeChatShop(ShopConfig{Enabled: true, AppID: "shop-app", AppSecret: "fixture", APIBaseURL: "https://api.weixin.qq.com"}, loaderStub{}, doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == shopTokenPath {
			tokens.Add(1)
			return shopResponse(`{"access_token":"fixture-token","expires_in":7200}`), nil
		}
		reads.Add(1)
		return shopResponse(`{"errcode":0,"product_ids":[],"next_key":""}`), nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if _, e := p.ListShopProducts(context.Background(), ""); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if tokens.Load() != 1 || reads.Load() != 16 {
		t.Fatal("token cache concurrency", tokens.Load(), reads.Load())
	}
	reads.Store(0)
	p.client = doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == shopTokenPath {
			tokens.Add(1)
			return shopResponse(`{"access_token":"fresh-token","expires_in":7200}`), nil
		}
		if reads.Add(1) == 1 {
			return shopResponse(`{"errcode":40014}`), nil
		}
		if r.URL.Query().Get("access_token") != "fresh-token" {
			t.Fatal("stale token reused")
		}
		return shopResponse(`{"errcode":0,"product_ids":[],"next_key":""}`), nil
	})
	if _, e := p.ListShopProducts(context.Background(), ""); e != nil || tokens.Load() != 2 || reads.Load() != 2 {
		t.Fatal("one refresh/read retry", e)
	}
}

func TestWeChatShopReadHTTPFailuresRemainSafe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		err    error
		body   string
	}{
		{"timeout", 0, errors.New("deadline access_token=fixture-token phone=13800138000"), ""},
		{"unauthorized", 401, nil, `{"errcode":0,"product_ids":[]}`},
		{"rate-limit", 429, nil, `{"errcode":0,"product_ids":[]}`},
		{"upstream", 503, nil, `{"errcode":0,"product_ids":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := shopAdversarialReader(t, "")
			p.client = doerFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == shopTokenPath {
					return shopResponse(`{"access_token":"fixture-token","expires_in":7200}`), nil
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, tc.err
			})
			if _, e := p.ListShopProducts(context.Background(), ""); e == nil || strings.Contains(e.Error(), "fixture-token") || strings.Contains(e.Error(), "13800138000") {
				t.Fatal("unsafe failure", e)
			}
		})
	}
}

func FuzzWeChatShopReferenceJSON(f *testing.F) {
	for _, v := range []string{"null", "false", "{}", "[]", "-1", "0", "90071992547409931234", `"sku-1"`, `"\u0000"`} {
		f.Add([]byte(v))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var ref exactShopReference
		if json.Unmarshal(raw, &ref) != nil {
			return
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var source any
		if dec.Decode(&source) != nil {
			t.Fatal("accepted invalid JSON")
		}
		switch v := source.(type) {
		case string:
			if ref.value != v {
				t.Fatal("string ID changed")
			}
		case json.Number:
			if ref.value != v.String() {
				t.Fatal("large integer ID changed")
			}
			for _, c := range v.String() {
				if c < '0' || c > '9' {
					t.Fatal("non-integer identifier accepted")
				}
			}
		default:
			t.Fatal("non-identifier JSON type accepted")
		}
	})
}
