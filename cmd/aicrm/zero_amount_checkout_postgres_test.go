package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func zeroCheckoutGET(f *productExternalPushChromiumFixture, token, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	w := httptest.NewRecorder()
	f.application.handler.ServeHTTP(w, r)
	return w
}
func assertZeroCheckoutCounts(t *testing.T, f *productExternalPushChromiumFixture, want int) {
	t.Helper()
	var orders, receipts, events, actions, payments, refunds, effects, redemptions int
	err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders WHERE provider='no_payment'),(SELECT count(*) FROM order_operation_receipts WHERE operation='create'),(SELECT count(*) FROM order_paid_events),(SELECT count(*) FROM product_paid_purchase_actions),(SELECT count(*) FROM payments),(SELECT count(*) FROM payment_refunds),(SELECT count(*) FROM external_effects WHERE owner='payment'),(SELECT count(*) FROM coupon_order_redemptions)`).Scan(&orders, &receipts, &events, &actions, &payments, &refunds, &effects, &redemptions)
	if err != nil || orders != want || receipts != want || events != want || actions != want || payments+refunds+effects+redemptions != 0 {
		t.Fatalf("zero facts orders=%d receipts=%d events=%d actions=%d payments=%d refunds=%d effects=%d coupons=%d err=%v", orders, receipts, events, actions, payments, refunds, effects, redemptions, err)
	}
}
func TestPostgreSQLZeroCheckoutAtomicReplayOwnershipAndPaidExclusion(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 120*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	p := f.application.pool.Native()
	session := issuePublicCommerceTrustedH5Session(t, f)
	if _, err := p.Exec(f.ctx, `UPDATE products SET price_minor=0 WHERE id=ANY($1::bigint[])`, []int64{f.productID, f.serviceProductID}); err != nil {
		t.Fatal(err)
	}
	setPublicCheckoutActionProjection(t, f, f.productID, legacyRedirectProjection(false, "h5", "https://after.example.test/free-complete", "", "", ""))
	for _, tc := range []struct {
		kind       string
		id, amount int64
	}{{"standard", f.productID, 1}, {"service_period", f.serviceProductID, 0}} {
		w := expectedCheckoutRequest(f, session.token, "zero-rejected-"+tc.kind+"-0001", tc.amount, tc.id, tc.kind, 0)
		if w.Code != 409 {
			t.Fatalf("reject %s status=%d body=%s", tc.kind, w.Code, w.Body.String())
		}
		assertZeroCheckoutCounts(t, f, 0)
	}
	// Inject failures into the existing event, fulfillment and receipt steps.
	// Their earlier inserts and session selection must all roll back.
	for _, table := range []string{"order_paid_events", "product_paid_purchase_actions", "order_operation_receipts"} {
		if _, err := p.Exec(f.ctx, `CREATE OR REPLACE FUNCTION zero_fixture_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected zero checkout failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		operation := "INSERT"
		if table == "order_operation_receipts" {
			operation = "UPDATE"
		}
		if _, err := p.Exec(f.ctx, "CREATE TRIGGER zero_fixture_fail BEFORE "+operation+" ON "+table+" FOR EACH ROW EXECUTE FUNCTION zero_fixture_fail()"); err != nil {
			t.Fatal(err)
		}
		w := expectedCheckoutRequest(f, session.token, "zero-rollback-"+table+"-0001", 0, f.productID, "standard", 0)
		if w.Code != 503 {
			t.Fatalf("failure at %s status=%d body=%s", table, w.Code, w.Body.String())
		}
		if _, err := p.Exec(f.ctx, "DROP TRIGGER zero_fixture_fail ON "+table); err != nil {
			t.Fatal(err)
		}
		assertZeroCheckoutCounts(t, f, 0)
	}
	const key = "zero-concurrent-original-0001"
	responses := make([]*httptest.ResponseRecorder, 6)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = expectedCheckoutRequest(f, session.token, key, 0, f.productID, "standard", 0)
		}(i)
	}
	wg.Wait()
	var result struct {
		OrderID  int64  `json:"order_id"`
		Merchant string `json:"merchant_order_no"`
		Required bool   `json:"payment_required"`
		Amount   int64  `json:"amount_minor"`
		Provider string `json:"provider"`
		Status   string `json:"status"`
	}
	for _, w := range responses {
		if w.Code != 200 || !bytes.Equal(responses[0].Body.Bytes(), w.Body.Bytes()) {
			t.Fatalf("same-key status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if err := json.Unmarshal(responses[0].Body.Bytes(), &result); err != nil || result.OrderID < 1 || result.Merchant == "" || result.Required || result.Amount != 0 || result.Provider != "no_payment" || result.Status != "paid" || bytes.Contains(responses[0].Body.Bytes(), []byte(`"payment_id"`)) || bytes.Contains(responses[0].Body.Bytes(), []byte(`"effect_id"`)) {
		t.Fatalf("free result=%+v body=%s err=%v", result, responses[0].Body.String(), err)
	}
	originalBody := append([]byte(nil), responses[0].Body.Bytes()...)
	assertZeroCheckoutCounts(t, f, 1)
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = expectedCheckoutRequest(f, session.token, fmt.Sprintf("zero-second-key-%04d", i), 0, f.productID, "standard", 0)
		}(i)
	}
	wg.Wait()
	for _, w := range responses {
		if w.Code != 409 || !bytes.Contains(w.Body.Bytes(), []byte("already_purchased")) {
			t.Fatalf("different-key status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// The original completed receipt survives price/config/lifecycle changes.
	if _, err := p.Exec(f.ctx, `UPDATE products SET name='后续草稿名称',price_minor=9900,legacy_admin_projection=jsonb_set(legacy_admin_projection,'{enabled}','false') WHERE id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	replay := expectedCheckoutRequest(f, session.token, key, 0, f.productID, "standard", 0)
	if replay.Code != 200 || !bytes.Equal(replay.Body.Bytes(), originalBody) {
		t.Fatalf("mutable catalog changed replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	changed := expectedCheckoutRequest(f, session.token, key, 1, f.productID, "standard", 0)
	if changed.Code != 409 {
		t.Fatalf("payload drift accepted: %s", changed.Body.String())
	}

	var productCode string
	if e := p.QueryRow(f.ctx, `SELECT product_code FROM products WHERE id=$1`, f.productID).Scan(&productCode); e != nil {
		t.Fatal(e)
	}
	recoveryPage := zeroCheckoutGET(f, session.token, "/pay/"+url.PathEscape(productCode))
	if recoveryPage.Code != 200 || !bytes.Contains(recoveryPage.Body.Bytes(), []byte(`data-product-kind="standard"`)) || bytes.Contains(recoveryPage.Body.Bytes(), []byte("后续草稿名称")) {
		t.Fatalf("disabled product recovery page status=%d", recoveryPage.Code)
	}
	numericRecovery := zeroCheckoutGET(f, session.token, fmt.Sprintf("/pay/%d", f.productID))
	if numericRecovery.Code != 200 {
		t.Fatal("legacy numeric zero checkout cannot recover")
	}
	anonymous := httptest.NewRecorder()
	f.application.handler.ServeHTTP(anonymous, httptest.NewRequest("GET", "/pay/"+url.PathEscape(productCode), nil))
	if anonymous.Code != 404 {
		t.Fatal("disabled product exposed anonymously")
	}
	owned := zeroCheckoutGET(f, session.token, fmt.Sprintf("/api/v1/wechat-pay/purchase-status?product_type=standard&product_id=%d", f.productID))
	if owned.Code != 200 || !bytes.Contains(owned.Body.Bytes(), []byte(`"payment_required":false`)) {
		t.Fatalf("disabled owned read status=%d body=%s", owned.Code, owned.Body.String())
	}
	status := zeroCheckoutGET(f, session.token, "/api/v1/wechat-pay/checkouts/"+result.Merchant)
	if status.Code != 200 || !bytes.Contains(status.Body.Bytes(), []byte(`"payment_required":false`)) || !bytes.Contains(status.Body.Bytes(), []byte("free-complete")) {
		t.Fatalf("free restore status=%d body=%s", status.Code, status.Body.String())
	}
	service := f.application.paymentDistribution.(*paymentapp.Service)
	// The same receipt is readable even when the server disables money writes.
	cmd := paymentport.CreateCommand{ProductID: f.productID, ProductType: "standard", Provider: "wechat_pay", BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf, SessionToken: session.token, CheckoutSessionBinding: paymentport.CheckoutSessionBinding(session.token), ActorScope: "public-checkout", IdempotencyKey: key, ExpectedAmountMinor: new(int64)}
	if got, err := service.CreateCheckout(f.ctx, cmd, false); err != nil || got.Payment != nil || got.OrderID != result.OrderID {
		t.Fatalf("disabled provider local replay result=%+v err=%v", got, err)
	}
	if _, err := service.RequestRefund(f.ctx, paymentport.RefundCommand{PaymentID: result.OrderID, AmountMinor: 0, RefundNo: "zero-refund-rejected", ActorScope: "test", IdempotencyKey: "zero-refund-rejected-0001"}); err == nil {
		t.Fatal("zero refund accepted")
	}
	order, err := f.application.nativeOrders.Get(f.ctx, result.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := orderdomain.Restore(order)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := aggregate.ApplySettlement(order.Version, orderdomain.StatusRefunded, 0, time.Now().UTC()); err == nil {
		t.Fatal("zero equality became full refund")
	}
	uow, _ := pg.NewUnitOfWork(f.application.pool)
	repo, _ := orderstore.NewPostgreSQL(p, uow)
	if err = uow.Within(f.ctx, func(tx context.Context) error {
		sales, e := repo.ReadPaidProductOrdersWithin(tx, []orderport.ProductSalesKey{{ProductID: f.productID, ProductCode: order.Items[0].ProductCode}})
		if e != nil {
			return e
		}
		if len(sales) != 0 {
			t.Error("free order counted as paid sale")
		}
		audience, e := repo.PaidAudienceOrders(tx, time.Now())
		if e != nil {
			return e
		}
		if len(audience) != 0 {
			t.Error("free order entered paid audience")
		}
		ids, e := repo.ListPaidSaleOrderIDsWithin(tx, 0, 50)
		if e != nil {
			return e
		}
		if len(ids) != 0 {
			t.Error("free order entered sale backfill")
		}
		evidence, e := repo.ListQualificationPurchaseEvidenceWithin(tx, orderport.QualificationPurchaseQuery{CustomerIDs: []int64{session.payerCustomerID}, ProductID: f.productID, ProductType: "standard_product"})
		if e != nil {
			return e
		}
		if len(evidence) != 0 {
			t.Error("free qualification evidence returned")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Original-session binding rejects a renewed session on an old request.
	renewed := issuePublicCommerceTrustedH5SessionWithKey(t, f, "zero-renewed-session-0001")
	if restored, e := service.GetNoPaymentCheckout(f.ctx, result.Merchant, renewed.token); e != nil || restored.OrderID != result.OrderID {
		t.Fatalf("verified payer renewal cannot read original free order: %v", e)
	}
	raw, _ := json.Marshal(map[string]any{"product_id": f.productID, "product_kind": "standard", "beneficiary_selection": "payer_self", "coupon_claim_id": 0, "expected_amount_minor": 0, "checkout_session_binding": paymentport.CheckoutSessionBinding(session.token)})
	r := httptest.NewRequest("POST", "/api/v1/wechat-pay/checkouts", bytes.NewReader(raw))
	r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: renewed.token})
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("Content-Type", "application/json")
	rejected := httptest.NewRecorder()
	f.application.handler.ServeHTTP(rejected, r)
	if rejected.Code != 409 || !bytes.Contains(rejected.Body.Bytes(), []byte("session_mismatch")) {
		t.Fatalf("renewed session reused old command: %s", rejected.Body.String())
	}
	fact, _ := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-public-commerce-h5", Value: "zero-attacker-openid", Source: "payment-h5-oauth"})
	strangerUnion, _ := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:public-commerce-fixture-platform", Value: "zero-attacker-union", Source: "payment-h5-oauth"})
	stranger, e := f.application.paymentSession.IssueTrusted(f.ctx, paymentsession.IssueCommand{Fact: fact, UnionID: strangerUnion, IdempotencyKey: "zero-stranger-session-0001"})
	if e != nil {
		t.Fatal(e)
	}
	strangerPage := zeroCheckoutGET(f, stranger.Token, "/pay/"+url.PathEscape(productCode))
	if strangerPage.Code != 404 {
		t.Fatal("disabled zero snapshot exposed to another customer")
	}
	// Selecting payer_self does not grant another customer's order.
	if e = uow.Within(f.ctx, func(tx context.Context) error {
		_, err := f.application.paymentSession.SelectPayerSelfWithin(tx, stranger.Token, time.Now())
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = service.GetNoPaymentCheckout(f.ctx, result.Merchant, stranger.Token); e == nil {
		t.Fatal("another customer read free checkout")
	}
	forged := bytes.ReplaceAll(raw, []byte(`"product_kind":"standard"`), []byte(`"product_kind":"standard","provider":"no_payment"`))
	r = httptest.NewRequest("POST", "/api/v1/wechat-pay/checkouts", bytes.NewReader(forged))
	r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
	r.Header.Set("Idempotency-Key", "zero-forged-provider-0001")
	r.Header.Set("Content-Type", "application/json")
	rejected = httptest.NewRecorder()
	f.application.handler.ServeHTTP(rejected, r)
	if rejected.Code != 409 {
		t.Fatal("browser selected internal no-payment provider")
	}
	exported, e := f.application.nativeOrders.ExportCSV(f.ctx, orderport.ListQuery{Provider: orderdomain.ProviderNoPayment, Limit: 50}, 1, "zero-export-0000001")
	if e != nil {
		t.Fatal(e)
	}
	rows, e := csv.NewReader(bytes.NewReader(exported.Content)).ReadAll()
	if e != nil || len(rows) != 2 || rows[1][4] != "" || rows[1][6] != "" || rows[1][7] != "0.00" {
		t.Fatalf("free export lost zero/empty payment facts: rows=%v err=%v", rows, e)
	}
	if e = uow.Within(f.ctx, func(tx context.Context) error {
		self, err := repo.GetSelfWithin(tx, result.OrderID, []int64{session.payerCustomerID}, false)
		if err != nil {
			return err
		}
		if self.ConfirmedPaidAt != nil {
			t.Error("free receipt fabricated payment time")
		}
		read, err := repo.GetExternalRead(tx, result.OrderID, []int64{session.payerCustomerID})
		if err != nil {
			return err
		}
		if read.IsPaid || read.PaidAt != nil || read.Amount.AmountMinor != 0 {
			t.Error("free read counted as payment")
		}
		paid := true
		list, err := repo.ListExternalRead(tx, orderport.ExternalReadQuery{CustomerIDs: []int64{session.payerCustomerID}, IsPaid: &paid, Limit: 10})
		if err != nil {
			return err
		}
		if len(list.Items) != 0 {
			t.Error("free matched paid filter")
		}
		paid = false
		list, err = repo.ListExternalRead(tx, orderport.ExternalReadQuery{CustomerIDs: []int64{session.payerCustomerID}, IsPaid: &paid, Limit: 10})
		if err != nil {
			return err
		}
		if len(list.Items) != 1 {
			t.Error("free missing from nonpaid filter")
		}
		_, err = f.application.nativeOrders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: result.OrderID, OccurredAt: time.Now(), ReceiptKey: "zero-spoof-paid-0001", ProviderTransactionNo: "forged"})
		if !errors.Is(err, orderport.ErrConflict) {
			t.Error("provider settlement modified no-payment order")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// A fresh free product also creates locally with money writes disabled.
	var extra int64
	if e = p.QueryRow(f.ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('zero-disabled-money','无需支付',0,'CNY',0,1,$1::jsonb) RETURNING id`, legacyRedirectProjection(false, "h5", "https://after.example.test/no-payment", "", "", "")).Scan(&extra); e != nil {
		t.Fatal(e)
	}
	newSession := issuePublicCommerceTrustedH5SessionWithKey(t, f, "zero-disabled-money-session-0001")
	cmd.ProductID = extra
	cmd.SessionToken = newSession.token
	cmd.CheckoutSessionBinding = paymentport.CheckoutSessionBinding(newSession.token)
	cmd.IdempotencyKey = "zero-disabled-money-new-0001"
	if got, e := service.CreateCheckout(f.ctx, cmd, false); e != nil || got.Payment != nil || got.OrderID < 1 || got.Status != "paid" {
		t.Fatalf("disabled money fresh checkout=%+v err=%v", got, e)
	}
	assertZeroCheckoutCounts(t, f, 2)
}

// Held only for native browser acceptance. The local browser uses the same
// verified synthetic session and full composition as the HTTP tests above.
func TestPostgreSQLZeroCheckoutBrowserJourney(t *testing.T) {
	receipt := os.Getenv("AICRM_ZERO_BROWSER_RECEIPT")
	if receipt == "" {
		t.Skip("set local browser receipt path for interactive acceptance")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 15*time.Minute, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	session := issuePublicCommerceTrustedH5Session(t, f)
	if _, err := f.application.pool.Native().Exec(f.ctx, `UPDATE products SET price_minor=0 WHERE id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	setPublicCheckoutActionProjection(t, f, f.productID, `{"schema_version":1,"status":"enabled","enabled":true,"require_mobile":false,"contact_collection_level":"none","purchase_action_enabled":false,"wecom_tagging":{},"slices":[]}`)
	// The loopback test harness supplies its synthetic trusted session and maps
	// only its own browser Origin to the composed fixture's TLS origin. No real
	// OAuth, customer credentials, insecure TLS bypass or Provider is involved.
	localOrigin := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if origin != localOrigin {
				w.WriteHeader(403)
				return
			}
			r.Header.Set("Origin", f.server.URL)
		}
		r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
		f.application.handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	localOrigin = server.URL
	var code string
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT product_code FROM products WHERE id=$1`, f.productID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	t.Log("Zero amount composed browser URL: " + server.URL + "/pay/" + url.PathEscape(code))
	timer := time.NewTicker(200 * time.Millisecond)
	defer timer.Stop()
	disabled := false
	for {
		select {
		case <-f.ctx.Done():
			t.Fatal("browser acceptance timed out")
		case <-timer.C:
			if !disabled {
				if _, err := os.Stat(receipt + ".disable"); err == nil {
					if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE products SET name='后续草稿名称',price_minor=9900,legacy_admin_projection=jsonb_set(legacy_admin_projection,'{enabled}','false') WHERE id=$1`, f.productID); err != nil {
						t.Fatal(err)
					}
					disabled = true
					t.Log("Zero amount fixture product disabled; original owned page remains readable")
				}
			}
			if _, err := os.Stat(receipt); err == nil {
				assertZeroCheckoutCounts(t, f, 1)
				return
			}
		}
	}
}

func TestPostgreSQLZeroCheckoutExternalIntentJoinsTransaction(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	session := issuePublicCommerceTrustedH5Session(t, f)
	p := f.application.pool.Native()
	if _, err := p.Exec(f.ctx, `UPDATE products SET price_minor=0,legacy_admin_projection=jsonb_set(jsonb_set(legacy_admin_projection,'{require_mobile}','true'),'{contact_collection_level}','"mobile"') WHERE id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	admin, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	config := productExternalPushAdminMutation(t, f.application.handler, http.MethodPut, fmt.Sprintf("/api/admin/wechat-pay/products/%d/external-push", f.productID), `{"enabled":true,"webhook_url":"https://commerce-browser.invalid","configuration_reference":"browser-push-target","push_type":"member_open","day":null,"frequency":null,"expires_at_ts":null,"remark":"free fixture","custom_params":{},"expected_revision":0}`, admin, csrf, "zero-config-push-0001")
	if config.Code != 200 {
		t.Fatalf("push config status=%d body=%s", config.Code, config.Body.String())
	}
	request := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"product_id": f.productID, "product_kind": "standard", "beneficiary_selection": "payer_self", "coupon_claim_id": 0, "expected_amount_minor": 0, "mobile": "+8613800000000", "contact_collection_level": "mobile", "checkout_session_binding": paymentport.CheckoutSessionBinding(session.token)})
		r := httptest.NewRequest("POST", "/api/v1/wechat-pay/checkouts", bytes.NewReader(body))
		r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
		r.Header.Set("Idempotency-Key", "zero-external-transaction-0001")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.application.handler.ServeHTTP(w, r)
		return w
	}
	if _, err := p.Exec(f.ctx, `CREATE FUNCTION zero_fixture_effect_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected effect failure'; END $$;CREATE TRIGGER zero_fixture_effect_fail BEFORE INSERT ON external_effects FOR EACH ROW EXECUTE FUNCTION zero_fixture_effect_fail()`); err != nil {
		t.Fatal(err)
	}
	failed := request()
	if failed.Code != 503 {
		t.Fatalf("external acceptance failure status=%d body=%s", failed.Code, failed.Body.String())
	}
	assertZeroCheckoutCounts(t, f, 0)
	var intents int
	if err := p.QueryRow(f.ctx, `SELECT count(*) FROM outbound_commerce_push_intents`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("split intent count=%d err=%v", intents, err)
	}
	if _, err := p.Exec(f.ctx, `DROP TRIGGER zero_fixture_effect_fail ON external_effects`); err != nil {
		t.Fatal(err)
	}
	success := request()
	if success.Code != 200 {
		t.Fatalf("free with outbound status=%d body=%s", success.Code, success.Body.String())
	}
	replay := request()
	if !bytes.Equal(success.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("outbound replay drift")
	}
	assertZeroCheckoutCounts(t, f, 1)
	var result struct {
		Merchant string `json:"merchant_order_no"`
	}
	if err := json.Unmarshal(success.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/admin/order-deliveries/"+result.Merchant+"?provider=no_payment", nil)
	r.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: admin})
	w := httptest.NewRecorder()
	f.application.handler.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("commerce_product_push")) {
		t.Fatalf("free outbound detail status=%d body=%s", w.Code, w.Body.String())
	}
	var effects, attempts int
	if err := p.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM outbound_commerce_push_intents WHERE effect_id IS NOT NULL),(SELECT count(*) FROM external_effects WHERE owner='outbound'),(SELECT COALESCE(sum(attempt_count),0) FROM external_effects)`).Scan(&intents, &effects, &attempts); err != nil || intents != 1 || effects != 1 || attempts != 0 {
		t.Fatalf("outbound accepted intents=%d effects=%d attempts=%d err=%v", intents, effects, attempts, err)
	}
}
