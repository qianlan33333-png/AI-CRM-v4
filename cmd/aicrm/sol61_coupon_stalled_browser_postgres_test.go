package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
)

// Local actual browser + composition + PostgreSQL. A committed claim response
// is held before headers or during its body. No provider worker or payment
// command runs; external browser traffic is blocked in the harness.
func TestPostgreSQLCouponStalledBrowserRecoverySol61(t *testing.T) {
	if os.Getenv("AICRM_REQUIRE_SOL61_FRONTEND") != "1" {
		t.Skip("set AICRM_REQUIRE_SOL61_FRONTEND=1 for the local actual browser diagnostic")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 240*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	trusted := issuePublicCommerceTrustedH5SessionWithKey(t, f, "sol61-coupon-stalled-session-0001")
	otherFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-public-commerce-h5", Value: "sol61-coupon-other-openid", Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	otherUnion, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:public-commerce-fixture-platform", Value: "sol61-coupon-other-union", Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.application.paymentSession.IssueTrusted(f.ctx, paymentsession.IssueCommand{Fact: otherFact, UnionID: otherUnion, IdempotencyKey: "sol61-coupon-other-session-0001"})
	if err != nil || int64(other.PayerCustomerID) == trusted.payerCustomerID {
		t.Fatalf("distinct local session fixture err=%v", err)
	}
	admin, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	target := fmt.Sprintf("standard_product:%d", f.productID)
	type heldClaim struct {
		ID         int64
		Slug, Mode string
		Release    chan struct{}
		Requests   int
		Released   bool
	}
	claims := make(map[string]*heldClaim)
	var mutex sync.Mutex
	for _, mode := range []string{"fetch", "body", "switch"} {
		now := time.Now().UTC()
		body, _ := json.Marshal(map[string]any{"name": "恢复验证券-" + mode, "discount_amount_total": 100, "total_issue_limit": 10, "per_user_issue_limit": 3, "claim_starts_at": now.Add(-time.Hour).Format(time.RFC3339), "claim_ends_at": now.Add(time.Hour).Format(time.RFC3339), "validity_mode": "relative_days", "relative_validity_days": 3, "target_refs": []string{target}})
		created := commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", "/api/admin/coupons", string(body), admin, csrf, "sol61-coupon-create-"+mode), 200)
		id := commerceFundsInt(t, created, "coupon_id")
		couponPath := fmt.Sprintf("/api/admin/coupons/%d", id)
		commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", couponPath+"/publish", "{}", admin, csrf, "sol61-coupon-publish-"+mode), 200)
		slug := commerceFundsString(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "GET", couponPath+"/share", "", admin, csrf, ""), 200), "public_slug")
		claims[slug] = &heldClaim{ID: id, Slug: slug, Mode: mode, Release: make(chan struct{})}
	}
	original := f.application.handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/__sol61__/away" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><title>恢复验证</title><body>使用浏览器返回查看原领券页。</body></html>`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/__sol61__/release" {
			mutex.Lock()
			item := claims[r.URL.Query().Get("slug")]
			if item != nil && !item.Released {
				item.Released = true
				close(item.Release)
			}
			mutex.Unlock()
			w.WriteHeader(204)
			return
		}
		var item *heldClaim
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/h5/coupons/") && strings.HasSuffix(r.URL.Path, "/claim") {
			slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/h5/coupons/"), "/claim")
			mutex.Lock()
			item = claims[slug]
			if item != nil {
				item.Requests++
				if item.Requests != 1 || item.Mode == "switch" {
					item = nil
				}
			}
			mutex.Unlock()
		}
		if item == nil {
			original.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		original.ServeHTTP(recorder, r)
		if recorder.Code != 200 {
			t.Errorf("first committed claim returned %d", recorder.Code)
		}
		body := recorder.Body.Bytes()
		for key, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		cut := 0
		if item.Mode == "body" {
			w.WriteHeader(recorder.Code)
			cut = len(body) / 2
			_, _ = w.Write(body[:cut])
			w.(http.Flusher).Flush()
		}
		select {
		case <-item.Release:
		case <-r.Context().Done():
			return
		}
		if item.Mode == "fetch" {
			w.WriteHeader(recorder.Code)
		}
		_, _ = w.Write(body[cut:])
	})
	defer func() {
		mutex.Lock()
		defer mutex.Unlock()
		for _, item := range claims {
			if !item.Released {
				item.Released = true
				close(item.Release)
			}
		}
	}()
	var slugs = make(map[string]string)
	for _, item := range claims {
		slugs[item.Mode] = item.Slug
	}
	rawSlugs, _ := json.Marshal(slugs)
	dir := filepath.Join(os.Getenv("AICRM_PHASE2_EVIDENCE_DIR"), "sol61-coupon-stalled", os.Getenv("SOL61_BROWSER_ENGINE"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(filepath.Dir(f.script), "sol61_coupon_stalled_browser.mjs")
	command := exec.CommandContext(f.ctx, "node", script)
	command.Env = append(os.Environ(), "SOL61_BROWSER_BASE="+f.server.URL, "SOL61_BROWSER_DIR="+dir, "SOL61_BROWSER_SLUGS="+string(rawSlugs), "SOL61_BROWSER_SESSION="+trusted.token, "SOL61_BROWSER_OTHER_SESSION="+other.Token, "SOL61_BROWSER_COOKIE="+paymentport.TrustedSessionCookieName)
	output, runErr := command.CombinedOutput()
	_ = os.WriteFile(filepath.Join(dir, "browser.log"), output, 0600)
	// Read final business records even on a red browser assertion. A hung
	// screen cannot be mistaken for an uncommitted command.
	for _, item := range claims {
		var count, holder, maximumHolder, issued int64
		err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT COALESCE(min(customer_id),0) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT COALESCE(max(customer_id),0) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT issued_count FROM coupon_rules WHERE id=$1)`, item.ID).Scan(&count, &holder, &maximumHolder, &issued)
		if item.Mode == "switch" {
			if err != nil || count != 0 || issued != 0 {
				t.Errorf("identity switch original-intent guard: PG claim=%d issued=%d holder-is-B=%t err=%v", count, issued, holder == int64(other.PayerCustomerID), err)
			} else {
				t.Logf("identity switch: PG claim=0 issued=0; HTTP requests=%d", item.Requests)
			}
			continue
		}
		if err != nil || count != 2 || holder != trusted.payerCustomerID || maximumHolder != trusted.payerCustomerID || issued != 2 {
			t.Errorf("%s PG count=%d all-holders-correct=%t issued=%d err=%v", item.Mode, count, holder == trusted.payerCustomerID && maximumHolder == trusted.payerCustomerID, issued, err)
		} else {
			t.Logf("%s: PG claim=2 issued=2 correct canonical holder (one original replay plus explicit new claim); HTTP requests=%d", item.Mode, item.Requests)
		}
	}
	if runErr != nil || !strings.Contains(string(output), `"status":"PASS"`) {
		t.Fatalf("coupon stalled browser contract err=%v output=%s evidence=%s", runErr, output, dir)
	}
}

// Acknowledgement recovery belongs to the original committed receipt even
// when the owner has since stopped or archived the rule. A fresh intent must
// still be rejected by Coupon's existing authoritative status checks.
func TestPostgreSQLCouponPublicReplayAfterTerminalRuleSol61(t *testing.T) {
	if os.Getenv("AICRM_REQUIRE_SOL61_FRONTEND") != "1" {
		t.Skip("set AICRM_REQUIRE_SOL61_FRONTEND=1 for the local composed HTTP diagnostic")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 120*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	trusted := issuePublicCommerceTrustedH5SessionWithKey(t, f, "sol61-coupon-terminal-session-0001")
	admin, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	for _, terminal := range []string{"stopped", "archived"} {
		t.Run(terminal, func(t *testing.T) {
			now := time.Now().UTC()
			body, _ := json.Marshal(map[string]any{"name": "领取回执验证券-" + terminal, "discount_amount_total": 100, "total_issue_limit": 10, "per_user_issue_limit": 3, "claim_starts_at": now.Add(-time.Hour).Format(time.RFC3339), "claim_ends_at": now.Add(time.Hour).Format(time.RFC3339), "validity_mode": "relative_days", "relative_validity_days": 3, "target_refs": []string{fmt.Sprintf("standard_product:%d", f.productID)}})
			created := commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", "/api/admin/coupons", string(body), admin, csrf, "sol61-coupon-terminal-create-"+terminal), 200)
			id := commerceFundsInt(t, created, "coupon_id")
			path := fmt.Sprintf("/api/admin/coupons/%d", id)
			commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", path+"/publish", "{}", admin, csrf, "sol61-coupon-terminal-publish-"+terminal), 200)
			slug := commerceFundsString(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "GET", path+"/share", "", admin, csrf, ""), 200), "public_slug")
			claim := func(key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/api/h5/coupons/"+slug+"/claim", strings.NewReader("{}"))
				r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: trusted.token})
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", key)
				r.Header.Set("X-AICRM-Coupon-Session-Binding", paymentport.CheckoutSessionBinding(trusted.token))
				w := httptest.NewRecorder()
				f.application.handler.ServeHTTP(w, r)
				return w
			}
			key := "sol61-terminal-original-claim-" + terminal
			first := claim(key)
			firstValue := commerceFundsObject(t, first, 200)
			firstClaim, ok := firstValue["claim"].(map[string]any)
			if !ok {
				t.Fatalf("missing first claim")
			}
			firstID := commerceFundsInt(t, firstClaim, "claim_id")
			if terminal == "stopped" {
				commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", path+"/stop", "{}", admin, csrf, "sol61-coupon-terminal-stop"), 200)
			} else {
				current := commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "GET", path, "", admin, csrf, ""), 200)["coupon"].(map[string]any)
				version := commerceFundsInt(t, current, "version")
				commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "DELETE", path, fmt.Sprintf(`{"expected_version":%d}`, version), admin, csrf, "sol61-coupon-terminal-archive"), 200)
			}
			replayed := claim(key)
			if replayed.Code != 200 {
				t.Errorf("historical original receipt blocked after %s: status=%d body=%s", terminal, replayed.Code, replayed.Body.String())
			} else {
				value := commerceFundsObject(t, replayed, 200)["claim"].(map[string]any)
				if commerceFundsInt(t, value, "claim_id") != firstID {
					t.Errorf("replay changed original claim")
				}
				if replayed.Body.String() != first.Body.String() {
					t.Errorf("terminal replay changed the original public receipt snapshot")
				}
			}
			fresh := claim("sol61-terminal-fresh-claim-" + terminal)
			if fresh.Code != 409 {
				t.Errorf("fresh claim accepted after %s: status=%d body=%s", terminal, fresh.Code, fresh.Body.String())
			}
			var count, issued, receipts, holder int64
			var status string
			err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT issued_count FROM coupon_rules WHERE id=$1),(SELECT count(*) FROM coupon_claim_operation_receipts WHERE claim_id=$2),(SELECT customer_id FROM coupon_customer_claims WHERE id=$2),(SELECT status FROM coupon_rules WHERE id=$1)`, id, firstID).Scan(&count, &issued, &receipts, &holder, &status)
			if err != nil || count != 1 || issued != 1 || receipts != 1 || holder != trusted.payerCustomerID || status != terminal {
				t.Errorf("PG after terminal replay/fresh: claim=%d issued=%d original-receipts=%d holder-correct=%t status=%s err=%v", count, issued, receipts, holder == trusted.payerCustomerID, status, err)
			}
			t.Logf("%s: same-key HTTP=%d fresh-key HTTP=%d PG claim=%d issued=%d original-receipts=%d", terminal, replayed.Code, fresh.Code, count, issued, receipts)
		})
	}
}

func TestPostgreSQLCouponRenewedSessionBrowserRecoverySol61(t *testing.T) {
	if os.Getenv("AICRM_REQUIRE_SOL61_FRONTEND") != "1" {
		t.Skip("set AICRM_REQUIRE_SOL61_FRONTEND=1 for the local actual browser diagnostic")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 120*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	trusted := issuePublicCommerceTrustedH5SessionWithKey(t, f, "sol61-coupon-renew-original-session-0001")
	admin, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	now := time.Now().UTC()
	body, _ := json.Marshal(map[string]any{"name": "原身份重新授权验证券", "discount_amount_total": 100, "total_issue_limit": 10, "per_user_issue_limit": 3, "claim_starts_at": now.Add(-time.Hour).Format(time.RFC3339), "claim_ends_at": now.Add(time.Hour).Format(time.RFC3339), "validity_mode": "relative_days", "relative_validity_days": 3, "target_refs": []string{fmt.Sprintf("standard_product:%d", f.productID)}})
	id := commerceFundsInt(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", "/api/admin/coupons", string(body), admin, csrf, "sol61-coupon-renew-create-0001"), 200), "coupon_id")
	path := fmt.Sprintf("/api/admin/coupons/%d", id)
	commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", path+"/publish", "{}", admin, csrf, "sol61-coupon-renew-publish-0001"), 200)
	slug := commerceFundsString(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "GET", path+"/share", "", admin, csrf, ""), 200), "public_slug")
	original := f.application.handler
	var mutex sync.Mutex
	requests := 0
	var firstSnapshot string
	recoveredSnapshotMatched := false
	var renewed publicCommerceTrustedH5Session
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/__sol61__/away" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><title>原身份恢复验证</title><body>使用原生返回核对原领取。</body></html>`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/__sol61__/renew-same-subject" {
			mutex.Lock()
			defer mutex.Unlock()
			if firstSnapshot == "" || renewed.token != "" {
				http.Error(w, "fixture sequence invalid", 409)
				return
			}
			renewed = issuePublicCommerceTrustedH5SessionWithKey(t, f, "sol61-coupon-renew-new-session-0001")
			if renewed.payerCustomerID != trusted.payerCustomerID || renewed.token == trusted.token {
				t.Errorf("new trusted token changed verified subject")
				http.Error(w, "fixture subject invalid", 500)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: renewed.token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/h5/coupons/"+slug+"/claim" {
			mutex.Lock()
			requests++
			first := requests == 1
			mutex.Unlock()
			if first {
				recorder := httptest.NewRecorder()
				original.ServeHTTP(recorder, r)
				if recorder.Code != 200 {
					t.Errorf("first real claim status=%d", recorder.Code)
					http.Error(w, "fixture commit failed", 500)
					return
				}
				mutex.Lock()
				firstSnapshot = recorder.Body.String()
				expirePublicCommerceTrustedSession(t, f, trusted.token)
				mutex.Unlock()
				// End this local response before sending its committed receipt.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("local response loss: %v", err)
					return
				}
				_ = conn.Close()
				return
			}
			recorder := httptest.NewRecorder()
			original.ServeHTTP(recorder, r)
			if recorder.Code == 200 {
				mutex.Lock()
				recoveredSnapshotMatched = recorder.Body.String() == firstSnapshot
				mutex.Unlock()
				if !recoveredSnapshotMatched {
					t.Errorf("renewed trusted session changed the original public receipt snapshot")
				}
			}
			for key, values := range recorder.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(recorder.Body.Bytes())
			return
		}
		original.ServeHTTP(w, r)
	})
	dir := filepath.Join(os.Getenv("AICRM_PHASE2_EVIDENCE_DIR"), "sol61-coupon-renewed", os.Getenv("SOL61_BROWSER_ENGINE"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(f.ctx, "node", filepath.Join(filepath.Dir(f.script), "sol61_coupon_renewed_browser.mjs"))
	command.Env = append(os.Environ(), "SOL61_BROWSER_BASE="+f.server.URL, "SOL61_BROWSER_DIR="+dir, "SOL61_BROWSER_SLUG="+slug, "SOL61_BROWSER_SESSION="+trusted.token, "SOL61_BROWSER_COOKIE="+paymentport.TrustedSessionCookieName)
	output, runErr := command.CombinedOutput()
	_ = os.WriteFile(filepath.Join(dir, "browser.log"), output, 0600)
	var count, holder, issued, receipts int64
	err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT COALESCE(min(customer_id),0) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT issued_count FROM coupon_rules WHERE id=$1),(SELECT count(*) FROM coupon_claim_operation_receipts receipt JOIN coupon_customer_claims claim ON claim.id=receipt.claim_id WHERE claim.coupon_id=$1)`, id).Scan(&count, &holder, &issued, &receipts)
	if err != nil || count != 1 || holder != trusted.payerCustomerID || issued != 1 || receipts != 1 {
		t.Errorf("renewed original intent PG: claim=%d issued=%d holder-correct=%t receipts=%d err=%v", count, issued, holder == trusted.payerCustomerID, receipts, err)
	}
	t.Logf("renewed same verified customer: new-token=%t customer-unchanged=%t PG claim=%d issued=%d receipts=%d HTTP requests=%d", renewed.token != "" && renewed.token != trusted.token, renewed.payerCustomerID == trusted.payerCustomerID, count, issued, receipts, requests)
	if runErr == nil && !recoveredSnapshotMatched {
		t.Errorf("no exact original receipt recovered")
	}
	if runErr != nil || !strings.Contains(string(output), `"status":"PASS"`) {
		t.Fatalf("renewed coupon browser err=%v output=%s evidence=%s", runErr, output, dir)
	}
}

func TestPostgreSQLCouponRecoveryOnlyMissAndOtherHolderSol61(t *testing.T) {
	if os.Getenv("AICRM_REQUIRE_SOL61_FRONTEND") != "1" {
		t.Skip("set AICRM_REQUIRE_SOL61_FRONTEND=1 for the local composed HTTP diagnostic")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 120*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, deferEffectsWorker: true})
	trusted := issuePublicCommerceTrustedH5SessionWithKey(t, f, "sol61-coupon-readonly-session-0001")
	openID, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-public-commerce-h5", Value: "sol61-readonly-other-openid", Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	unionID, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:public-commerce-fixture-platform", Value: "sol61-readonly-other-unionid", Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.application.paymentSession.IssueTrusted(f.ctx, paymentsession.IssueCommand{Fact: openID, UnionID: unionID, IdempotencyKey: "sol61-coupon-readonly-other-session-0001"})
	if err != nil || int64(other.PayerCustomerID) == trusted.payerCustomerID {
		t.Fatalf("distinct readonly fixture holder err=%v", err)
	}
	admin, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	now := time.Now().UTC()
	body, _ := json.Marshal(map[string]any{"name": "只确认原收据验证券", "discount_amount_total": 100, "total_issue_limit": 10, "per_user_issue_limit": 3, "claim_starts_at": now.Add(-time.Hour).Format(time.RFC3339), "claim_ends_at": now.Add(time.Hour).Format(time.RFC3339), "validity_mode": "relative_days", "relative_validity_days": 3, "target_refs": []string{fmt.Sprintf("standard_product:%d", f.productID)}})
	id := commerceFundsInt(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", "/api/admin/coupons", string(body), admin, csrf, "sol61-coupon-readonly-create-0001"), 200), "coupon_id")
	path := fmt.Sprintf("/api/admin/coupons/%d", id)
	commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "POST", path+"/publish", "{}", admin, csrf, "sol61-coupon-readonly-publish-0001"), 200)
	slug := commerceFundsString(t, commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "GET", path+"/share", "", admin, csrf, ""), 200), "public_slug")
	claim := func(token, key string, mode []string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/h5/coupons/"+slug+"/claim", strings.NewReader("{}"))
		r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("X-AICRM-Coupon-Session-Binding", paymentport.CheckoutSessionBinding(trusted.token))
		for _, value := range mode {
			r.Header.Add("X-AICRM-Coupon-Recovery-Only", value)
		}
		w := httptest.NewRecorder()
		f.application.handler.ServeHTTP(w, r)
		return w
	}
	counts := func() [4]int64 {
		var out [4]int64
		if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coupon_customer_claims),(SELECT count(*) FROM coupon_claim_operation_receipts),(SELECT count(*) FROM coupon_claim_audit_events),(SELECT count(*) FROM coupon_claim_outbox)`).Scan(&out[0], &out[1], &out[2], &out[3]); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := counts()
	key := "sol61-readonly-original-intent-0001"
	for _, token := range []string{trusted.token, other.Token} {
		if response := claim(token, key, []string{"true"}); response.Code != 409 {
			t.Errorf("readonly miss created intent: status=%d body=%s", response.Code, response.Body.String())
		}
		if after := counts(); after != before {
			t.Fatalf("readonly miss changed claim/receipt/audit/outbox counts: before=%v after=%v", before, after)
		}
	}
	first := claim(trusted.token, key, nil)
	commerceFundsObject(t, first, 200)
	committed := counts()
	for i := range committed {
		if committed[i] != before[i]+1 {
			t.Errorf("original claim did not commit exactly one artifact: before=%v after=%v", before, committed)
			break
		}
	}
	for _, scenario := range []struct {
		name, token, key string
		mode             []string
		want             int
	}{
		{"other holder", other.Token, key, []string{"true"}, 409},
		{"fresh readonly miss", trusted.token, "sol61-readonly-fresh-miss-0001", []string{"true"}, 409},
		{"invalid false", trusted.token, "sol61-readonly-invalid-false-0001", []string{"false"}, 400},
		{"invalid empty", trusted.token, "sol61-readonly-invalid-empty-0001", []string{""}, 400},
		{"invalid duplicate", trusted.token, "sol61-readonly-invalid-duplicate-0001", []string{"true", "true"}, 400},
		{"original receipt", trusted.token, key, []string{"true"}, 200},
	} {
		response := claim(scenario.token, scenario.key, scenario.mode)
		if response.Code != scenario.want {
			t.Errorf("%s: status=%d body=%s", scenario.name, response.Code, response.Body.String())
		}
		if scenario.want == 200 && response.Body.String() != first.Body.String() {
			t.Errorf("readonly confirmation changed original receipt snapshot")
		}
		if after := counts(); after != committed {
			t.Fatalf("%s changed claim/receipt/audit/outbox counts: before=%v after=%v", scenario.name, committed, after)
		}
	}
	var issued, wrongHolder int64
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT issued_count FROM coupon_rules WHERE id=$1),(SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1 AND customer_id<>$2)`, id, trusted.payerCustomerID).Scan(&issued, &wrongHolder); err != nil || issued != 1 || wrongHolder != 0 {
		t.Errorf("readonly final issued=%d other-holder=%d err=%v", issued, wrongHolder, err)
	}
	t.Logf("readonly A/B misses: zero artifacts; accepted original only; invalid headers400; B0claim; claim/receipt/audit/outbox deltas=%v issued=%d", [4]int64{committed[0] - before[0], committed[1] - before[1], committed[2] - before[2], committed[3] - before[3]}, issued)
}
