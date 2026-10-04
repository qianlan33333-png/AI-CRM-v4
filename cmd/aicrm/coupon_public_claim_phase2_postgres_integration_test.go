package main

import (
	"encoding/json"
	"fmt"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Actual composition: admin auth/CSRF -> public share -> trusted OneID session
// -> claim -> eligible-product read, with persisted limit/receipt invariants.
func TestPostgreSQLCouponPublicClaimHTTPReplayAndHolder(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 60*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	session, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	trusted := issuePublicCommerceTrustedH5SessionWithKey(t, f, "coupon-public-composed-holder-0001")
	now := time.Now().UTC()
	target := fmt.Sprintf("standard_product:%d", f.productID)
	body := map[string]any{"name": "public-http-coupon", "discount_amount_total": 100, "total_issue_limit": 1, "per_user_issue_limit": 1, "claim_starts_at": now.Add(-time.Hour).Format(time.RFC3339), "claim_ends_at": now.Add(time.Hour).Format(time.RFC3339), "validity_mode": "relative_days", "relative_validity_days": 3, "target_refs": []string{target}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	created := productExternalPushAdminMutation(t, f.application.handler, "POST", "/api/admin/coupons", string(raw), session, csrf, "coupon-public-create-0001")
	object := commerceFundsObject(t, created, 200)
	id := commerceFundsInt(t, object, "coupon_id")
	path := fmt.Sprintf("/api/admin/coupons/%d", id)
	published := productExternalPushAdminMutation(t, f.application.handler, "POST", path+"/publish", "{}", session, csrf, "coupon-public-publish-0001")
	if published.Code != 200 {
		t.Fatalf("publish=%d %s", published.Code, published.Body.String())
	}
	share := productExternalPushAdminMutation(t, f.application.handler, "GET", path+"/share", "", session, csrf, "")
	slug := commerceFundsString(t, commerceFundsObject(t, share, 200), "public_slug")
	request := func(method, path, body, key string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("User-Agent", "fixture MicroMessenger/8.0")
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if authenticated {
			req.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: trusted.token})
		}
		out := httptest.NewRecorder()
		f.application.handler.ServeHTTP(out, req)
		return out
	}
	if page := request("GET", "/c/"+slug, "", "", true); page.Code != 200 || !strings.Contains(page.Body.String(), "public-http-coupon") {
		t.Fatalf("page=%d", page.Code)
	}
	claimPath := "/api/h5/coupons/" + slug + "/claim"
	key := "coupon-public-claim-replay-0001"
	if denied := request("POST", claimPath, "{}", key, false); denied.Code != 401 {
		t.Fatalf("anonymous=%d %s", denied.Code, denied.Body.String())
	}
	if denied := request("POST", claimPath, `{"customer_id":999}`, key, true); denied.Code != 400 {
		t.Fatalf("tamper=%d", denied.Code)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- request("POST", claimPath, "{}", key, true) }()
	}
	wg.Wait()
	close(results)
	var firstClaim map[string]any
	for out := range results {
		claim := commerceFundsObject(t, out, 200)["claim"].(map[string]any)
		if firstClaim == nil {
			firstClaim = claim
		} else if claim["claim_id"] != firstClaim["claim_id"] || claim["valid_until"] != firstClaim["valid_until"] {
			t.Fatal("concurrent replay changed snapshot")
		}
	}
	replay := commerceFundsObject(t, request("POST", claimPath, "{}", key, true), 200)["claim"].(map[string]any)
	if replay["claim_id"] != firstClaim["claim_id"] {
		t.Fatal("replay changed claim")
	}
	if limit := request("POST", claimPath, "{}", key+"-new", true); limit.Code != 409 {
		t.Fatalf("issue limit=%d %s", limit.Code, limit.Body.String())
	}
	available := commerceFundsObject(t, request("GET", "/api/h5/coupons/available?target_ref="+target, "", "", true), 200)
	items := available["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["claim_id"] != firstClaim["claim_id"] {
		t.Fatalf("eligible list=%v", items)
	}
	var claims, issued, holder, receipts int64
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT issued_count FROM coupon_rules WHERE id=$1),(SELECT customer_id FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT count(*) FROM coupon_claim_operation_receipts WHERE operation='claim')`, id).Scan(&claims, &issued, &holder, &receipts); err != nil || claims != 1 || issued != 1 || holder != trusted.payerCustomerID || receipts != 1 {
		t.Fatalf("claims=%d issued=%d holder=%d receipts=%d err=%v", claims, issued, holder, receipts, err)
	}
}
