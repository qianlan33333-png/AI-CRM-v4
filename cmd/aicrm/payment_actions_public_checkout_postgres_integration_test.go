package main

import (
	"bytes"
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

// TestPostgreSQLPaymentActionsPublicCheckoutCompletionParity uses the real
// composed public Host, its trusted H5 session, the native checkout HTTP
// boundary and a verified payment callback. It proves that the target saved at
// settlement survives a later product edit, that terminal refreshes only
// read the same paid action, and that a URL Link stays a same-origin route
// until Product resolves its legacy fallback server-side.
//
// OneID: the fixture creates the H5 session through Payment's composed,
// verified-identity path; the browser never supplies an identity. Persistence:
// checkout snapshot, Order settlement and Product's paid action share their
// normal PostgreSQL Unit of Work. URL Link resolution is a Provider read after
// that transaction and never creates a new payment or action.
func TestPostgreSQLPaymentActionsPublicCheckoutCompletionParity(t *testing.T) {
	fixture := newPublicCommerceChromiumFixture(t, 90*time.Second)
	session := issuePublicCommerceTrustedH5Session(t, fixture)
	paymentService, ok := fixture.application.paymentDistribution.(*paymentapp.Service)
	if !ok || paymentService == nil {
		t.Fatal("composition did not retain the native Payment application")
	}

	const checkoutH5URL = "https://after.example.test/frozen-h5"
	const paidH5URL = "https://after.example.test/edited-after-checkout"
	setPublicCheckoutActionProjection(t, fixture, fixture.productID, legacyRedirectProjection(false, "h5", checkoutH5URL, "", "", ""))
	h5Merchant := createPublicCheckoutForCompletionAction(t, fixture, session.token, fixture.productID, "standard", "payment-actions-public-h5-create-0001")
	// The first paid event uses Product's current action, then freezes that
	// action for later status reads even if the Product changes again.
	setPublicCheckoutActionProjection(t, fixture, fixture.productID, legacyRedirectProjection(false, "h5", paidH5URL, "", "", ""))
	settlePublicCheckoutForCompletionAction(t, fixture, paymentService, h5Merchant, 9900, "payment-actions-public-h5")
	setPublicCheckoutActionProjection(t, fixture, fixture.productID, legacyRedirectProjection(false, "h5", "https://after.example.test/edited-after-payment", "", "", ""))

	h5Status := readPublicCheckoutCompletionAction(t, fixture, session.token, h5Merchant)
	if h5Status.Status != "paid" || h5Status.Action.State != "available" || h5Status.Action.Mode != "redirect" || h5Status.Action.RedirectURL != paidH5URL {
		t.Fatalf("frozen H5 paid action status=%+v", h5Status)
	}
	assertPublicCompletionActionFrozen(t, fixture, h5Merchant, paidH5URL, false)
	assertPublicCompletionRefreshIsReadOnly(t, fixture, session.token, h5Merchant, 1)

	const fallbackURL = "https://after.example.test/url-link-fallback"
	linkSession := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "payment-actions-public-url-link-session-001")
	setPublicCheckoutActionProjection(t, fixture, fixture.serviceProductID, legacyRedirectProjection(true, "url_link", "", "https://source.invalid/legacy-url-link", "result.destination", fallbackURL))
	linkMerchant := createPublicCheckoutForCompletionAction(t, fixture, linkSession.token, fixture.serviceProductID, "service_period", "payment-actions-public-url-link-create-1")
	settlePublicCheckoutForCompletionAction(t, fixture, paymentService, linkMerchant, 12800, "payment-actions-public-url-link")
	// A later edit cannot replace the already persisted URL Link receipt.
	setPublicCheckoutActionProjection(t, fixture, fixture.serviceProductID, legacyRedirectProjection(true, "h5", "https://after.example.test/edited-service", "", "", ""))

	linkStatus := readPublicCheckoutCompletionAction(t, fixture, linkSession.token, linkMerchant)
	wantResolverPath := "/api/v1/wechat-pay/checkouts/" + linkMerchant + "/completion-target"
	if linkStatus.Status != "paid" || linkStatus.Action.State != "available" || linkStatus.Action.Mode != "redirect" || linkStatus.Action.RedirectURL != wantResolverPath || strings.Contains(linkStatus.Raw, "source.invalid") {
		t.Fatalf("URL Link paid action must expose only same-origin resolver status=%+v", linkStatus)
	}
	assertPublicCompletionActionFrozen(t, fixture, linkMerchant, fallbackURL, true)
	resolver := httptest.NewRecorder()
	resolverRequest := httptest.NewRequest(http.MethodGet, wantResolverPath, nil)
	resolverRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: linkSession.token})
	fixture.application.handler.ServeHTTP(resolver, resolverRequest)
	if resolver.Code != http.StatusFound || resolver.Header().Get("Location") != fallbackURL || bytes.Contains(resolver.Body.Bytes(), []byte("source.invalid")) {
		t.Fatalf("same-origin URL Link resolver status=%d location=%q body=%s", resolver.Code, resolver.Header().Get("Location"), resolver.Body.String())
	}
	assertPublicCompletionRefreshIsReadOnly(t, fixture, linkSession.token, linkMerchant, 2)
}

type publicCompletionActionStatus struct {
	Status string
	Action struct {
		State       string `json:"state"`
		Mode        string `json:"mode"`
		RedirectURL string `json:"redirect_url"`
	} `json:"completion_action"`
	Raw string
}

func legacyRedirectProjection(servicePeriod bool, targetType, h5URL, sourceURL, responseKey, fallbackURL string) string {
	target := map[string]any{
		"enabled": true, "target_type": targetType, "h5_url": h5URL,
		"url_link":     map[string]any{"enabled": targetType == "url_link", "source_url": sourceURL, "response_url_key": responseKey},
		"fallback_url": fallbackURL,
	}
	status, button := "enabled", "立即购买"
	if servicePeriod {
		status, button = "service_period_enabled", "立即续费"
	}
	projection := map[string]any{
		"schema_version": 1, "status": status, "enabled": true, "buy_button_text": button, "require_mobile": false,
		"lead_program_id": nil, "lead_channel_id": nil, "lead_qr_title": "", "lead_qr_subtitle": "",
		"completion_redirect_enabled": false, "completion_redirect_url": "", "completion_target": target,
		"purchase_action_enabled": true, "purchase_action_mode": "redirect", "wecom_tagging": map[string]any{}, "slices": []any{},
	}
	encoded, _ := json.Marshal(projection)
	return string(encoded)
}

func setPublicCheckoutActionProjection(t *testing.T, fixture *productExternalPushChromiumFixture, productID int64, projection string) {
	t.Helper()
	command, err := fixture.application.pool.Native().Exec(fixture.ctx, `UPDATE products SET legacy_admin_projection=$2::jsonb, version=version+1, updated_at=now() WHERE id=$1`, productID, projection)
	if err != nil || command.RowsAffected() != 1 {
		t.Fatalf("update public completion product id=%d changed=%d err=%v", productID, command.RowsAffected(), err)
	}
}

func createPublicCheckoutForCompletionAction(t *testing.T, fixture *productExternalPushChromiumFixture, token string, productID int64, productKind, idempotencyKey string) string {
	t.Helper()
	binding := httptest.NewRecorder()
	bindingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	bindingRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	fixture.application.handler.ServeHTTP(binding, bindingRequest)
	var sessionBody struct {
		Binding string `json:"checkout_session_binding"`
	}
	if binding.Code != http.StatusOK || json.Unmarshal(binding.Body.Bytes(), &sessionBody) != nil || sessionBody.Binding == "" {
		t.Fatalf("public checkout binding status=%d body=%s", binding.Code, binding.Body.String())
	}
	body := fmt.Sprintf(`{"product_id":%d,"product_kind":%q,"beneficiary_selection":"payer_self","coupon_claim_id":0,"checkout_session_binding":%q}`, productID, productKind, sessionBody.Binding)
	created := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	fixture.application.handler.ServeHTTP(created, request)
	var result struct {
		MerchantOrderNo string `json:"merchant_order_no"`
	}
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &result) != nil || result.MerchantOrderNo == "" {
		t.Fatalf("create public checkout kind=%s status=%d body=%s", productKind, created.Code, created.Body.String())
	}
	return result.MerchantOrderNo
}

func settlePublicCheckoutForCompletionAction(t *testing.T, fixture *productExternalPushChromiumFixture, service *paymentapp.Service, merchant string, amount int64, label string) {
	t.Helper()
	eventDigest := sha256.Sum256([]byte(label + "-event"))
	bodyDigest := sha256.Sum256([]byte(label + "-body"))
	transaction := "tx-" + label
	err := service.ApplyVerifiedCallback(fixture.ctx, paymentprovider.CallbackResult{
		Kind: "payment", MerchantOrderNo: merchant, ProviderTransactionReference: transaction,
		AmountMinor: amount, Currency: "CNY", AppID: "wx-public-commerce-h5", OccurredAt: time.Now().UTC(),
		ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", transaction)), EventDigest: eventDigest, BodyDigest: bodyDigest,
	})
	if err != nil {
		t.Fatalf("settle composed public checkout %s: %v", label, err)
	}
}

func readPublicCheckoutCompletionAction(t *testing.T, fixture *productExternalPushChromiumFixture, token, merchant string) publicCompletionActionStatus {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/"+merchant, nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	fixture.application.handler.ServeHTTP(response, request)
	var body publicCompletionActionStatus
	body.Raw = response.Body.String()
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("read paid public checkout merchant=%s status=%d body=%s", merchant, response.Code, response.Body.String())
	}
	return body
}

func assertPublicCompletionActionFrozen(t *testing.T, fixture *productExternalPushChromiumFixture, merchant, target string, hasURLLink bool) {
	t.Helper()
	var redirect string
	var targetRaw json.RawMessage
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT action.redirect_url,action.completion_target
FROM product_paid_purchase_actions action
JOIN orders ON orders.id=action.order_id
WHERE orders.merchant_order_no=$1`, merchant).Scan(&redirect, &targetRaw)
	if err != nil || redirect != target || (hasURLLink != bytes.Contains(targetRaw, []byte(`"source_url"`))) {
		t.Fatalf("frozen paid action merchant=%s redirect=%q target=%s urlLink=%t err=%v", merchant, redirect, targetRaw, hasURLLink, err)
	}
}

func assertPublicCompletionRefreshIsReadOnly(t *testing.T, fixture *productExternalPushChromiumFixture, token, merchant string, expectedActions int) {
	t.Helper()
	first := readPublicCheckoutCompletionAction(t, fixture, token, merchant)
	second := readPublicCheckoutCompletionAction(t, fixture, token, merchant)
	if first.Raw != second.Raw {
		t.Fatalf("paid checkout refresh changed response first=%s second=%s", first.Raw, second.Raw)
	}
	var payments, orders, actions int
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT
	(SELECT count(*) FROM payments),
	(SELECT count(*) FROM orders WHERE record_origin='native'),
	(SELECT count(*) FROM product_paid_purchase_actions)`).Scan(&payments, &orders, &actions)
	if err != nil || payments != expectedActions || orders != expectedActions || actions != expectedActions {
		t.Fatalf("paid checkout refresh must not create records payments=%d orders=%d actions=%d expected=%d err=%v", payments, orders, actions, expectedActions, err)
	}
}
