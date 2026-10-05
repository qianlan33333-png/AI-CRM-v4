package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Local composed pages and PostgreSQL; no provider worker. Fixture state changes
// model committed facts only. Checkout POSTs are intercepted in the browser;
// coupon/refund acceptance calls reach real HTTP/application/PG and EER.
func TestPostgreSQLCommerceRecoveryWebKit(t *testing.T) {
	if os.Getenv("AICRM_REQUIRE_PRODUCT_WEBKIT_PHASE2") != "1" {
		t.Skip("requires local WebKit fixtures")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 240*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	a := issuePublicCommerceTrustedH5SessionWithKey(t, f, "recovery-webkit-session-a-0001")
	fact, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-public-commerce-h5", Value: "recovery-webkit-other-openid", Source: "payment-h5-oauth"})
	if e != nil {
		t.Fatal(e)
	}
	union, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:public-commerce-fixture-platform", Value: "recovery-webkit-other-union", Source: "payment-h5-oauth"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.application.paymentSession.IssueTrusted(f.ctx, paymentsession.IssueCommand{Fact: fact, UnionID: union, IdempotencyKey: "recovery-webkit-session-b-0001"})
	if e != nil {
		t.Fatal(e)
	}
	if int64(b.PayerCustomerID) == a.payerCustomerID {
		t.Fatal("fixture subjects must differ")
	}
	admin, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	pool := f.application.pool.Native()
	var payerIdentityID int64
	if err := pool.QueryRow(f.ctx, `SELECT id FROM customer_identities WHERE customer_id=$1 AND kind='oa_openid' LIMIT 1`, a.payerCustomerID).Scan(&payerIdentityID); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []struct{ name, expected, root, driver string }{{"webkit15.4", "15.4", os.Getenv("AICRM_PHASE2_OLD_PLAYWRIGHT_ROOT"), os.Getenv("AICRM_PHASE2_OLD_WEBKIT_DRIVER")}, {"webkit26.0", "26.0", os.Getenv("AICRM_PHASE2_MODERN_PLAYWRIGHT_ROOT"), os.Getenv("AICRM_PHASE2_MODERN_WEBKIT_DRIVER")}} {
		t.Run(engine.name, func(t *testing.T) {
			standardCode, serviceCode := "recovery-"+engine.name+"-standard", "recovery-"+engine.name+"-period"
			standardID, serviceID := seedProductWebKitPhase2Products(t, f, standardCode, serviceCode)
			now := time.Now().UTC()
			digest := sha256.Sum256([]byte(serviceCode))
			_, e = pool.Exec(f.ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('recovery-webkit',$1,$2,$3,'私有周期权益','active',$4,$5,'PRIVATE-RECOVERY-REMARK',$6,$4,$4)`, serviceCode, a.payerCustomerID, serviceID, now.Add(-time.Hour), now.Add(30*24*time.Hour), digest[:])
			if e != nil {
				t.Fatal(e)
			}
			var ownedOrder, refundOrder int64
			for _, order := range []struct {
				no     string
				id     *int64
				amount int64
				origin string
			}{{"owned-" + engine.name, &ownedOrder, 1234, "history"}, {"v3pay_recovery_" + engine.name, &refundOrder, 1000, "native"}} {
				e = pool.QueryRow(f.ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','recovery-webkit',$1,$1,$2,$2,$3,'CNY','paid',$4,$5,$6,$7,$7) RETURNING id`, order.no, a.payerCustomerID, order.amount, order.origin, order.origin == "native", func() []byte {
					if order.origin == "native" {
						return nil
					}
					return digest[:]
				}(), now).Scan(order.id)
				if e != nil {
					t.Fatal(e)
				}
				_, e = pool.Exec(f.ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,$3,'恢复验收商品',$4,1,$4)`, *order.id, func() int64 {
					if order.origin == "native" {
						return f.productID
					}
					return standardID
				}(), standardCode, order.amount)
				if e != nil {
					t.Fatal(e)
				}
			}
			refundMerchant := "v3pay_recovery_" + engine.name
			txref := "tx-recovery-" + engine.name
			if _, err := pool.Exec(f.ctx, `UPDATE orders SET provider_transaction_no=$1 WHERE id=$2`, txref, refundOrder); err != nil {
				t.Fatal(err)
			}
			_, e = pool.Exec(f.ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,provider_transaction_reference,provider_transaction_digest,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,$3,$4,$4,1000,'CNY','paid',$5,$6,1,$7,$7)`, refundOrder, refundMerchant, payerIdentityID, a.payerCustomerID, txref, effectport.Hash("wechatpay.transaction", txref), now)
			if e != nil {
				t.Fatal(e)
			}
			body, _ := json.Marshal(map[string]any{"name": "恢复验收券", "discount_amount_total": 100, "total_issue_limit": 10, "per_user_issue_limit": 3, "claim_starts_at": now.Add(-time.Hour).Format(time.RFC3339), "claim_ends_at": now.Add(time.Hour).Format(time.RFC3339), "validity_mode": "relative_days", "relative_validity_days": 3, "target_refs": []string{fmt.Sprintf("standard_product:%d", standardID)}})
			couponID := commerceFundsInt(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", "/api/admin/coupons", string(body), admin, csrf, "recovery-coupon-create-"+engine.name), 200), "coupon_id")
			couponPath := fmt.Sprintf("/api/admin/coupons/%d", couponID)
			commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", couponPath+"/publish", "{}", admin, csrf, "recovery-coupon-publish-"+engine.name), 200)
			slug := commerceFundsString(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "GET", couponPath+"/share", "", admin, csrf, ""), 200), "public_slug")
			owner := f.application.handler
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && r.URL.Path == "/__fixture__/period" {
					status, end := "active", now.Add(120*24*time.Hour)
					switch r.URL.Query().Get("state") {
					case "expired":
						end = now.Add(-time.Hour)
					case "refunded":
						status = "refunded"
						end = now.Add(-time.Hour)
					case "active":
						end = now.Add(30 * 24 * time.Hour)
					case "renewed":
					default:
						http.Error(w, "invalid fixture", 400)
						return
					}
					_, err := pool.Exec(r.Context(), `UPDATE order_service_entitlements SET status=$1,end_at=$2 WHERE customer_id=$3 AND service_product_id=$4`, status, end, a.payerCustomerID, serviceID)
					if err != nil {
						http.Error(w, "fixture update failed", 500)
						return
					}
					w.WriteHeader(204)
					return
				}
				if r.Method == "POST" && r.URL.Path == "/__fixture__/standard-refund" {
					_, err := pool.Exec(r.Context(), `UPDATE orders SET status='refunded',refunded_minor=amount_minor WHERE id=$1`, ownedOrder)
					if err != nil {
						http.Error(w, "fixture update failed", 500)
						return
					}
					w.WriteHeader(204)
					return
				}
				owner.ServeHTTP(w, r)
			})
			local := f.server
			dir := filepath.Join(os.Getenv("AICRM_PHASE2_EVIDENCE_DIR"), "risk-recovery-webkit", engine.name)
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			script := filepath.Join(filepath.Dir(f.script), "commerce_recovery_webkit.mjs")
			cmd := exec.CommandContext(f.ctx, os.Getenv("AICRM_PHASE2_NODE"), script)
			cmd.Env = append(os.Environ(), "RECOVERY_BASE_URL="+local.URL, "RECOVERY_ROOT="+engine.root, "RECOVERY_DRIVER="+engine.driver, "RECOVERY_ENGINE="+engine.expected, "RECOVERY_DIR="+dir, "RECOVERY_A="+a.token, "RECOVERY_B="+b.Token, "RECOVERY_PAYMENT_COOKIE="+paymentport.TrustedSessionCookieName, "RECOVERY_ADMIN="+admin, "RECOVERY_CSRF="+csrf, "RECOVERY_ADMIN_COOKIE="+accesshttp.SessionCookieName, "RECOVERY_CSRF_COOKIE="+accesshttp.CSRFCookieName, "RECOVERY_STANDARD_CODE="+standardCode, "RECOVERY_STANDARD_ID="+fmt.Sprint(standardID), "RECOVERY_SERVICE_CODE="+serviceCode, "RECOVERY_COUPON_SLUG="+slug, "RECOVERY_REFUND_MERCHANT="+refundMerchant, "RECOVERY_TXREF="+txref)
			output, err := cmd.CombinedOutput()
			_ = os.WriteFile(filepath.Join(dir, "browser.log"), output, 0600)
			if err != nil {
				t.Fatalf("browser=%v output=%s", err, output)
			}
			var receipt struct{ Status, Engine string }
			if e = json.Unmarshal(output, &receipt); e != nil || receipt.Status != "PASS" || receipt.Engine != engine.expected {
				t.Fatalf("receipt=%s", output)
			}
			var claims, holder, refunds, effects int64
			e = pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT customer_id FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT count(*) FROM payment_refunds WHERE payment_id IN (SELECT id FROM payments WHERE order_id=$2)),(SELECT count(DISTINCT external_effect_id) FROM payment_refunds WHERE payment_id IN (SELECT id FROM payments WHERE order_id=$2))`, couponID, refundOrder).Scan(&claims, &holder, &refunds, &effects)
			if e != nil || claims != 1 || holder != a.payerCustomerID || refunds != 1 || effects != 1 {
				t.Fatalf("claims=%d holder=%d refunds=%d effects=%d err=%v", claims, holder, refunds, effects, e)
			}
			t.Logf("%s receipt/PG: claim=1 correct holder; refund=1 effect=1; no provider worker; %s", engine.name, dir)
		})
	}
}
