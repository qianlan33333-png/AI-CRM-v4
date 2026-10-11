package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"github.com/riverqueue/river"
)

type browserShopReader struct{ fail atomic.Bool }

func (r *browserShopReader) ListShopProducts(context.Context, string) (shopport.ProductPage, error) {
	if r.fail.Load() {
		return shopport.ProductPage{}, errors.New("synthetic Provider unavailable")
	}
	return shopport.ProductPage{IDs: []string{"browser-source"}}, nil
}
func (r *browserShopReader) ReadShopProduct(context.Context, string) (shopport.Product, error) {
	return shopport.Product{ID: "browser-source", Online: true, SKUs: []productport.ShopSKU{{AppID: "browser-shop", ProductID: "browser-source", SKUID: "90071992547409939876", Name: "微信小店浏览器课程", PriceMinor: 100}}}, nil
}

// Real browser + Access/CSRF + actual Product/Order/River PostgreSQL. Provider
// reads are synthetic; no real Shop, Worker dispatch or activation is enabled.
func TestPostgreSQLShopRefreshRecoveryAndOrderChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("explicit Chromium journey required")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 3*time.Minute, productExternalPushChromiumFixtureOptions{deferEffectsWorker: true})
	s := f.application.shopSync
	s.Enabled = true
	s.AppID = "browser-shop"
	r := &browserShopReader{}
	s.Reader = r
	worker := &wechatshop.ProductWorker{Service: s}
	first, e := s.SubmitProductRefresh(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.Work(f.ctx, &river.Job[wechatshop.ProductArgs]{Args: wechatshop.ProductArgs{RunID: first.ID}}); e != nil {
		t.Fatal(e)
	}
	var productID int64
	if e = f.application.pool.Native().QueryRow(f.ctx, `SELECT product_id FROM product_wechat_shop_skus WHERE app_id='browser-shop'`).Scan(&productID); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if e = s.UOW.Within(f.ctx, func(tx context.Context) error {
		return s.Orders.ImportShopOrderWithin(tx, orderport.ShopOrder{AppID: "browser-shop", ID: "90071992547409931234", TransactionID: "browser-shop-tx", CreatedAt: now.Add(-time.Hour), PaidAt: now, UpdatedAt: now, PaidMinor: 85, RefundedMinor: 85, Lines: []orderport.ShopOrderLine{{ProductID: "browser-source", SKUID: "90071992547409939876", Name: "微信小店浏览器课程", UnitMinor: 100, PaidMinor: 80, Quantity: 2}}}, now.Add(time.Second))
	}); e != nil {
		t.Fatal(e)
	}
	actual := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/__shop_fixture__/failure" && req.Method == http.MethodPost {
			r.fail.Store(true)
			w.WriteHeader(204)
			return
		}
		if req.URL.Path == "/__shop_fixture__/run" && req.Method == http.MethodPost {
			if req.URL.Query().Get("recover") == "1" {
				r.fail.Store(false)
			}
			status, e := s.ProductRefreshStatus(req.Context())
			if e == nil {
				e = worker.Work(req.Context(), &river.Job[wechatshop.ProductArgs]{Args: wechatshop.ProductArgs{RunID: status.ID}})
			}
			if e != nil {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
			return
		}
		actual.ServeHTTP(w, req)
	})
	command := exec.CommandContext(f.ctx, "node", filepath.Join("cmd", "aicrm", "wechat_shop_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_SHOP_TEST_URL="+f.server.URL, "AICRM_SHOP_TEST_PRODUCT="+strconv.FormatInt(productID, 10))
	output, e := command.CombinedOutput()
	if e != nil || !strings.Contains(string(output), "wechat_shop_chromium: PASS") {
		t.Fatalf("Shop browser journey: %v output=%s", e, strings.TrimSpace(string(output)))
	}
	var count int
	if e = f.application.pool.Native().QueryRow(f.ctx, `SELECT count(*) FROM product_wechat_shop_skus WHERE app_id='browser-shop'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("browser recovery duplicated SKU", e)
	}
	status, e := s.ProductRefreshStatus(f.ctx)
	if e != nil || status.State != "succeeded" || status.Existing != 1 || status.LastSuccessAt == nil {
		t.Fatal("success receipt disappeared with UI", e)
	}
	t.Log(strings.TrimSpace(string(output)))
}
