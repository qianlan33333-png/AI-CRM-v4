package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Source composition only seeds disposable owner facts. All checkout, status,
// page, action and statistics acceptance assertions use the exact installed binary
// and its Host behind the existing TLS proxy, with outbound networking denied.
func TestPostgreSQLZeroAmountInstalledCheckout(t *testing.T) {
	input := testconfig.InstalledProduct()
	diagnostic := input.Binary == "" && os.Getenv("AICRM_ZERO_INSTALLED_FIXTURE_DIAGNOSTIC") == "1"
	if input.Binary == "" {
		if os.Getenv("AICRM_REQUIRE_ZERO_INSTALLED") == "1" {
			t.Fatal("installed zero checkout requires exact binary/Host inputs")
		}
		if !diagnostic {
			t.Skip("installed package inputs required")
		}
	}
	db, e := url.Parse(os.Getenv("AICRM_DATABASE_URL"))
	if e != nil || db.Hostname() != "127.0.0.1" && db.Hostname() != "localhost" && db.Hostname() != "::1" || !strings.HasPrefix(strings.TrimPrefix(db.Path, "/"), "aicrm_test_") {
		t.Fatal("explicit loopback disposable database required")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 120*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true})
	session := issuePublicCommerceTrustedH5Session(t, f)
	admin, _ := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	p := f.application.pool.Native()
	if _, e = p.Exec(f.ctx, `UPDATE products SET price_minor=0 WHERE id=$1`, f.productID); e != nil {
		t.Fatal(e)
	}
	setPublicCheckoutActionProjection(t, f, f.productID, legacyRedirectProjection(false, "h5", "https://after.example.test/installed-free", "", "", ""))
	useInstalledPeriodicContactFixtureWithEnvironment(t, f, []string{"AICRM_EFFECTS_PROVIDER_ENABLED=false", "AICRM_GROUP_OPS_PROVIDER_ENABLED=false", "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost,::1"})
	// All business requests use HTTP -> existing TLS proxy -> package binary.
	// Diagnostic mode uses the same transport but reports source mode explicitly.
	client := f.server.Client()
	f.application.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, err := http.NewRequestWithContext(r.Context(), r.Method, f.server.URL+r.URL.RequestURI(), r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header = r.Header.Clone()
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		if _, err = io.Copy(w, response.Body); err != nil {
			t.Fatal(err)
		}
	})
	const key = "installed-zero-checkout-0001"
	first := expectedCheckoutRequest(f, session.token, key, 0, f.productID, "standard", 0)
	if first.Code != 200 || !bytes.Contains(first.Body.Bytes(), []byte(`"payment_required":false`)) || !bytes.Contains(first.Body.Bytes(), []byte("installed-free")) {
		t.Fatalf("installed free checkout status=%d body=%s", first.Code, first.Body.String())
	}
	var result struct {
		OrderID  int64  `json:"order_id"`
		Merchant string `json:"merchant_order_no"`
	}
	if e = json.Unmarshal(first.Body.Bytes(), &result); e != nil || result.OrderID < 1 {
		t.Fatal("installed order identity unavailable")
	}
	assertZeroCheckoutCounts(t, f, 1)
	replay := expectedCheckoutRequest(f, session.token, key, 0, f.productID, "standard", 0)
	if replay.Code != 200 || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("installed same-key replay drift")
	}
	other := expectedCheckoutRequest(f, session.token, "installed-zero-other-key-0001", 0, f.productID, "standard", 0)
	if other.Code != 409 || !bytes.Contains(other.Body.Bytes(), []byte("already_purchased")) {
		t.Fatal("installed duplicate free ownership")
	}
	status := zeroCheckoutGET(f, session.token, "/api/v1/wechat-pay/checkouts/"+result.Merchant)
	if status.Code != 200 || !bytes.Contains(status.Body.Bytes(), []byte(`"amount_minor":0`)) || !bytes.Contains(status.Body.Bytes(), []byte("installed-free")) {
		t.Fatal("installed completion/action cannot restore")
	}
	// Server-side installed statistics must exclude the new completion fact.
	request := httptest.NewRequest("GET", "/api/admin/wechat-pay/products", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: admin})
	stats := httptest.NewRecorder()
	f.application.handler.ServeHTTP(stats, request)
	var board struct {
		Items []struct {
			ID   int64 `json:"id"`
			Paid int64 `json:"paid_order_count"`
			Sold int64 `json:"sold_count"`
		} `json:"items"`
	}
	if stats.Code != 200 || json.Unmarshal(stats.Body.Bytes(), &board) != nil {
		t.Fatalf("installed stats status=%d", stats.Code)
	}
	found := false
	for _, row := range board.Items {
		if row.ID == f.productID {
			found = true
			if row.Paid != 0 || row.Sold != 0 {
				t.Fatal("installed free order entered paid statistics")
			}
		}
	}
	if !found {
		t.Fatal("installed statistics omitted product")
	}
	var code string
	if e = p.QueryRow(f.ctx, `SELECT product_code FROM products WHERE id=$1`, f.productID).Scan(&code); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(f.ctx, `UPDATE products SET name='后续草稿名称',price_minor=9900,legacy_admin_projection=jsonb_set(legacy_admin_projection,'{enabled}','false') WHERE id=$1`, f.productID); e != nil {
		t.Fatal(e)
	}
	replay = expectedCheckoutRequest(f, session.token, key, 0, f.productID, "standard", 0)
	if replay.Code != 200 || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("installed changed/disabled product changed original result")
	}
	page := zeroCheckoutGET(f, session.token, "/pay/"+url.PathEscape(code))
	if page.Code != 200 || bytes.Contains(page.Body.Bytes(), []byte("后续草稿名称")) || !bytes.Contains(page.Body.Bytes(), []byte(`data-v3-public-commerce`)) {
		t.Fatal("installed frozen page cannot recover")
	}
	anonymous := httptest.NewRecorder()
	f.application.handler.ServeHTTP(anonymous, httptest.NewRequest("GET", "/pay/"+url.PathEscape(code), nil))
	if anonymous.Code != 404 {
		t.Fatal("installed disabled product exposed anonymously")
	}
	assertZeroCheckoutCounts(t, f, 1)
	// A second priced product proves the one-cent floor and rejection rollback.
	payer := issuePublicCommerceTrustedH5SessionWithKey(t, f, "installed-floor-session-0001")
	var paidProduct int64
	if e = p.QueryRow(f.ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('installed-floor','安装包券下限',9900,'CNY',0,1,$1::jsonb) RETURNING id`, legacyRedirectProjection(false, "h5", "https://after.example.test/installed-floor", "", "", "")).Scan(&paidProduct); e != nil {
		t.Fatal(e)
	}
	claim := seedExpectedCheckoutCoupon(t, f, payer.payerCustomerID, paidProduct, 20000)
	wrong := expectedCheckoutRequest(f, payer.token, "installed-floor-drift-0001", 100, paidProduct, "standard", claim)
	if wrong.Code != 409 || !bytes.Contains(wrong.Body.Bytes(), []byte("price_changed")) {
		t.Fatal("installed price drift accepted")
	}
	assertZeroCheckoutCounts(t, f, 1)
	paid := expectedCheckoutRequest(f, payer.token, "installed-floor-one-cent-0001", 1, paidProduct, "standard", claim)
	if paid.Code != 202 {
		t.Fatalf("installed floor status=%d body=%s", paid.Code, paid.Body.String())
	}
	var gross, discount, payable, payment int64
	e = p.QueryRow(f.ctx, `SELECT c.gross_amount_minor,c.discount_amount_minor,c.payable_amount_minor,p.amount_minor FROM order_checkout_snapshots c JOIN payments p ON p.order_id=c.order_id WHERE c.product_id=$1`, paidProduct).Scan(&gross, &discount, &payable, &payment)
	if e != nil || gross != 9900 || discount != 9899 || payable != 1 || payment != 1 {
		t.Fatal("installed frozen coupon/payment amounts disagree")
	}
	var freePayments, freeEffects int
	e = p.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payments WHERE order_id=$1),(SELECT count(*) FROM external_effect_attempts)`, result.OrderID).Scan(&freePayments, &freeEffects)
	if e != nil || freePayments != 0 || freeEffects != 0 {
		t.Fatalf("installed unexpected payment/provider attempt: %d/%d err=%v", freePayments, freeEffects, e)
	}
	if diagnostic {
		t.Log("zero installed fixture wiring diagnostic: PASS; source composition only, installed acceptance remains unverified")
		return
	}
	t.Log(fmt.Sprintf("installed_zero_amount: PASS source_sha=%s binary_sha256=%s; free completion/action/replay/disabled recovery and paid statistics exclusion; coupon minimum one cent; no Provider attempt", input.SHA, input.BinarySHA256))
}
