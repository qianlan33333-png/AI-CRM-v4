package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func expectedCheckoutRequest(f *productExternalPushChromiumFixture, token, key string, expected int64, productID int64, kind string, claimID int64) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"product_id": productID, "product_kind": kind, "beneficiary_selection": "payer_self", "coupon_claim_id": claimID, "expected_amount_minor": expected, "checkout_session_binding": paymentport.CheckoutSessionBinding(token)})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Idempotency-Key", key)
	r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	w := httptest.NewRecorder()
	f.application.handler.ServeHTTP(w, r)
	return w
}

func seedExpectedCheckoutCoupon(t *testing.T, f *productExternalPushChromiumFixture, customer, productID, face int64) int64 {
	t.Helper()
	p := f.application.pool.Native()
	var rule, claim int64
	e := p.QueryRow(f.ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,relative_validity_days,created_by,updated_by) VALUES('期待金额测试券',$1,'CNY','published',10,10,1,now()-interval '1 hour',now()+interval '1 day','relative_days',3,1,1) RETURNING id`, face).Scan(&rule)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(f.ctx, `INSERT INTO coupon_rule_targets(coupon_id,target_ref,position) VALUES($1,$2,0)`, rule, fmt.Sprintf("standard_product:%d", productID)); e != nil {
		t.Fatal(e)
	}
	e = p.QueryRow(f.ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claimed_at,source_digest,valid_from,valid_until,created_at,updated_at) VALUES('floor-fixture',$1,$2,$3,'available',now(),decode(repeat('ab',32),'hex'),now()-interval '1 hour',now()+interval '1 day',now(),now()) RETURNING id`, fmt.Sprintf("rule-%d", rule), customer, rule).Scan(&claim)
	if e != nil {
		t.Fatal(e)
	}
	return claim
}

func TestPostgreSQLCheckoutExpectedAmountRollsBackOrderCouponAndPayment(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	session := issuePublicCommerceTrustedH5Session(t, f)
	claim := seedExpectedCheckoutCoupon(t, f, session.payerCustomerID, f.productID, 20000)
	wrong := expectedCheckoutRequest(f, session.token, "expected-price-drift-0001", 100, f.productID, "standard", claim)
	if wrong.Code != 409 || !bytes.Contains(wrong.Body.Bytes(), []byte(`"price_changed"`)) {
		t.Fatalf("price rejection status=%d body=%s", wrong.Code, wrong.Body.String())
	}
	var orders, payments, receipts, redemptions, prepay int
	var status string
	e := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders WHERE record_origin='native'),(SELECT count(*) FROM payments),(SELECT count(*) FROM order_operation_receipts),(SELECT count(*) FROM coupon_order_redemptions),(SELECT count(*) FROM external_effects WHERE kind='wechat_pay_prepay_v1'),(SELECT status FROM coupon_customer_claims WHERE id=$1)`, claim).Scan(&orders, &payments, &receipts, &redemptions, &prepay, &status)
	if e != nil || orders != 0 || payments != 0 || receipts != 0 || redemptions != 0 || prepay != 0 || status != "available" {
		t.Fatalf("rollback orders=%d payments=%d receipts=%d redemptions=%d prepay=%d claim=%s err=%v", orders, payments, receipts, redemptions, prepay, status, e)
	}
	correct := expectedCheckoutRequest(f, session.token, "expected-price-correct-0001", 1, f.productID, "standard", claim)
	if correct.Code != 202 {
		t.Fatalf("floor checkout status=%d body=%s", correct.Code, correct.Body.String())
	}
	var gross, discount, payable, payment int64
	e = f.application.pool.Native().QueryRow(f.ctx, `SELECT c.gross_amount_minor,c.discount_amount_minor,c.payable_amount_minor,p.amount_minor FROM order_checkout_snapshots c JOIN payments p ON p.order_id=c.order_id`).Scan(&gross, &discount, &payable, &payment)
	if e != nil || gross != 9900 || discount != 9899 || payable != 1 || payment != 1 {
		t.Fatalf("frozen amounts gross=%d discount=%d payable=%d payment=%d err=%v", gross, discount, payable, payment, e)
	}
	replay := expectedCheckoutRequest(f, session.token, "expected-price-correct-0001", 1, f.productID, "standard", claim)
	if replay.Code != 202 || !bytes.Equal(correct.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("replay status=%d", replay.Code)
	}

	var created struct {
		Merchant string `json:"merchant_order_no"`
	}
	if e = json.Unmarshal(correct.Body.Bytes(), &created); e != nil {
		t.Fatal(e)
	}
	settlePublicCheckoutForCompletionAction(t, f, f.application.paymentDistribution.(*paymentapp.Service), created.Merchant, 1, "coupon-floor-one-cent")
	var face, actual, settled int64
	e = f.application.pool.Native().QueryRow(f.ctx, `SELECT rule.discount_amount_total,r.discount_amount_minor,r.payable_amount_minor,c.status FROM coupon_customer_claims c JOIN coupon_rules rule ON rule.id=c.coupon_id JOIN coupon_order_redemptions r ON r.claim_id=c.id WHERE c.id=$1`, claim).Scan(&face, &actual, &settled, &status)
	if e != nil || face != 20000 || actual != 9899 || settled != 1 || status != "redeemed" {
		t.Fatalf("coupon finality face=%d actual=%d payable=%d status=%s err=%v", face, actual, settled, status, e)
	}
}
