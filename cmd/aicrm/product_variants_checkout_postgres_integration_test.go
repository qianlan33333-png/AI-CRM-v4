package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
)

func TestPostgreSQLVariantCheckoutTransactionsAndFrozenRecovery(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	pool := f.application.pool.Native()
	if _, err := pool.Exec(f.ctx, `UPDATE products SET variants_enabled=true WHERE id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	var a, b int64
	for i, pair := range []struct {
		name  string
		price int64
	}{{"单人版", 9900}, {"三人成行版", 99900}} {
		var id int64
		if err := pool.QueryRow(f.ctx, `INSERT INTO product_variants(product_id,name,description,attributes,price_minor,position) VALUES($1,$2,'购买时说明','[{"key":"版本","value":"A"}]',$3,$4) RETURNING id`, f.productID, pair.name, pair.price, i).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			a = id
		} else {
			b = id
		}
	}
	session := issuePublicCommerceTrustedH5Session(t, f)
	post := func(variant, version int64, key string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"product_id":%d,"product_kind":"standard","variant_id":%d,"variant_version":%d,"beneficiary_selection":"payer_self","coupon_claim_id":0,"checkout_session_binding":%q}`, f.productID, variant, version, paymentport.CheckoutSessionBinding(session.token))
		request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
		out := httptest.NewRecorder()
		f.application.handler.ServeHTTP(out, request)
		return out
	}
	for _, pair := range [][2]int64{{0, 0}, {b, 2}, {b + 100, 1}} {
		out := post(pair[0], pair[1], fmt.Sprintf("variant-invalid-%d-%d-00001", pair[0], pair[1]))
		if out.Code != 409 || !strings.Contains(out.Body.String(), "variant_selection_changed") {
			t.Fatalf("invalid %v: %d %s", pair, out.Code, out.Body)
		}
	}
	var before int
	if err := pool.QueryRow(f.ctx, `SELECT count(*) FROM payments`).Scan(&before); err != nil || before != 0 {
		t.Fatal("rejected payment leak", before, err)
	}
	created := post(b, 1, "variant-composed-checkout-0001")
	var fact struct {
		OrderID  int64  `json:"order_id"`
		Merchant string `json:"merchant_order_no"`
		Variant  struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"variant"`
	}
	if created.Code != 202 || json.Unmarshal(created.Body.Bytes(), &fact) != nil || fact.OrderID < 1 || fact.Variant.ID != b || fact.Variant.Name != "三人成行版" {
		t.Fatalf("create=%d %s", created.Code, created.Body)
	}
	var gross, payable int64
	var name string
	var quantity int
	if err := pool.QueryRow(f.ctx, `SELECT s.gross_amount_minor,s.payable_amount_minor,i.variant_snapshot->>'name',i.quantity FROM order_checkout_snapshots s JOIN order_items i ON i.order_id=s.order_id WHERE s.order_id=$1`, fact.OrderID).Scan(&gross, &payable, &name, &quantity); err != nil || gross != 99900 || payable != 99900 || name != "三人成行版" || quantity != 1 {
		t.Fatal("frozen selection", gross, payable, name, quantity, err)
	}
	if out := post(a, 1, "variant-composed-checkout-0001"); out.Code != 409 {
		t.Fatalf("key changed selection=%d %s", out.Code, out.Body)
	}
	if _, err := pool.Exec(f.ctx, `UPDATE product_variants SET name='已修改',price_minor=1,version=version+1,removed_at=now() WHERE id=$1`, b); err != nil {
		t.Fatal(err)
	}
	if out := post(b, 1, "variant-composed-checkout-0001"); out.Code != 202 {
		t.Fatalf("old command replay=%d %s", out.Code, out.Body)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/"+fact.Merchant, nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
	out := httptest.NewRecorder()
	f.application.handler.ServeHTTP(out, request)
	if out.Code != 202 || !strings.Contains(out.Body.String(), `"amount_minor":99900`) || !strings.Contains(out.Body.String(), "三人成行版") {
		t.Fatalf("frozen authorized read=%d %s", out.Code, out.Body)
	}
	unauthed := httptest.NewRecorder()
	f.application.handler.ServeHTTP(unauthed, httptest.NewRequest(http.MethodGet, request.URL.String(), nil))
	if unauthed.Code != 401 {
		t.Fatal("untrusted order read", unauthed.Code)
	}
	service := f.application.paymentDistribution.(*paymentapp.Service)
	callback := paymentprovider.CallbackResult{Kind: "payment", MerchantOrderNo: fact.Merchant, ProviderTransactionReference: "tx-variant-paid", AmountMinor: 99900, Currency: "CNY", AppID: "wx-public-commerce-h5", OccurredAt: time.Now().UTC(), ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", "tx-variant-paid")), EventDigest: sha256.Sum256([]byte("variant-event")), BodyDigest: sha256.Sum256([]byte("variant-body"))}
	for i := 0; i < 2; i++ {
		if err := service.ApplyVerifiedCallback(f.ctx, callback); err != nil {
			t.Fatal("exact callback replay", i, err)
		}
	}
	session = issuePublicCommerceTrustedH5SessionWithKey(t, f, "variant-renewed-session-0001")
	if out := post(a, 1, "variant-owned-new-checkout-0001"); out.Code != 409 || !strings.Contains(out.Body.String(), "already_purchased") {
		t.Fatalf("parent gate=%d %s", out.Code, out.Body)
	}
	var payments, paid int
	if err := pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payments),(SELECT count(*) FROM order_paid_events)`).Scan(&payments, &paid); err != nil || payments != 1 || paid != 1 {
		t.Fatal("duplicate effects", payments, paid, err)
	}
}

func TestPostgreSQLZeroVariantCheckoutFreezesSelection(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	pool := f.application.pool.Native()
	if _, err := pool.Exec(f.ctx, `UPDATE products SET variants_enabled=true WHERE id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(f.ctx, `INSERT INTO product_variants(product_id,name,price_minor,position) VALUES($1,'免费体验版',0,0) RETURNING id`, f.productID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	session := issuePublicCommerceTrustedH5Session(t, f)
	body := fmt.Sprintf(`{"product_id":%d,"product_kind":"standard","variant_id":%d,"variant_version":1,"beneficiary_selection":"payer_self","coupon_claim_id":0,"expected_amount_minor":0,"checkout_session_binding":%q}`, f.productID, id, paymentport.CheckoutSessionBinding(session.token))
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "zero-variant-frozen-checkout-0001")
		req.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
		out := httptest.NewRecorder()
		f.application.handler.ServeHTTP(out, req)
		return out
	}
	out := post()
	var fact struct {
		Merchant string `json:"merchant_order_no"`
		OrderID  int64  `json:"order_id"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &fact) != nil || fact.OrderID < 1 || !strings.Contains(out.Body.String(), `"payment_required":false`) || !strings.Contains(out.Body.String(), "免费体验版") {
		t.Fatalf("free variant response: %d %s", out.Code, out.Body)
	}
	if _, err := pool.Exec(f.ctx, `UPDATE product_variants SET name='改名',price_minor=99900,version=version+1,removed_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if replay := post(); replay.Code != 200 || !strings.Contains(replay.Body.String(), "免费体验版") {
		t.Fatalf("free variant replay: %d %s", replay.Code, replay.Body)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/"+fact.Merchant, nil)
	req.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
	read := httptest.NewRecorder()
	f.application.handler.ServeHTTP(read, req)
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"amount_minor":0`) || !strings.Contains(read.Body.String(), "免费体验版") {
		t.Fatalf("free variant snapshot: %d %s", read.Code, read.Body)
	}
	var payments int
	if err := pool.QueryRow(f.ctx, `SELECT count(*) FROM payments WHERE order_id=$1`, fact.OrderID).Scan(&payments); err != nil || payments != 0 {
		t.Fatal("free variant created payment", payments, err)
	}
}
