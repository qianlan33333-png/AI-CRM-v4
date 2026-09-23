package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type checkoutRecoveryProvisioner struct {
	identityID int64
	customerID customerdomain.CustomerID
}

func (p checkoutRecoveryProvisioner) ProvisionVerifiedIdentity(_ context.Context, command identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	if !command.Fact.Valid() || len(command.IdempotencyKey) < 16 {
		return identityport.ProvisionResult{}, errors.New("unverified payment session")
	}
	return identityport.ProvisionResult{IdentityID: p.identityID, CustomerID: p.customerID}, nil
}

type checkoutRecoveryProductReader struct{ product productport.CheckoutProduct }

func (reader checkoutRecoveryProductReader) ReadCheckoutProductWithin(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.CheckoutProduct, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil || kind != reader.product.ProductType || id != reader.product.ID {
		return productport.CheckoutProduct{}, errors.New("checkout product unavailable")
	}
	return reader.product, nil
}

// TestPostgreSQLPublicCheckoutResponseLossRejectsRenewedSessionReplay uses the
// actual Payment application, Payment/Order stores, Payment sessions, durable
// External Effects acceptance and Payment HTTP handler. It proves that a
// committed response-lost checkout remains one merchant order when the same
// customer completes OAuth again: the old checkpoint binding is rejected
// before Create, and even a forged current binding cannot bypass the existing
// Payment receipt's full session-bound payload.
func TestPostgreSQLPublicCheckoutResponseLossRejectsRenewedSessionReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	effectsModule := effects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}

	var customerID int64
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	sessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 71, customerID: customerdomain.CustomerID(customerID)}, customerstore.NewPostgreSQL(), paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	productReader := checkoutRecoveryProductReader{product: productport.CheckoutProduct{ID: 7, ProductType: productport.ProductOptionStandard, Code: "course-7", Name: "恢复测试商品", PriceMinor: 990, Currency: "CNY", Version: 3}}
	paymentService := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orderapp.NewService(uow, orders), sessions, effectStore, effectStore)
	if err = paymentService.SetCheckoutProductReader(productReader); err != nil {
		t.Fatal(err)
	}
	paymentHandler, err := paymenthttp.NewHandler(paymentService, nil, checkoutJourneySecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	loss := &checkoutJourneyResponseLoss{next: paymentHandler}

	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-h5", Value: "opaque-openid", Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	unionFact, _ := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:fixture", Value: "fixture-union", Source: "payment-h5-oauth"})
	first, err := sessions.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: fact, UnionID: unionFact, IdempotencyKey: "checkout-recovery-oauth-first-0001"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := sessions.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: fact, UnionID: unionFact, IdempotencyKey: "checkout-recovery-oauth-second-001"})
	if err != nil {
		t.Fatal(err)
	}
	key := "checkout-recovery-response-loss-0001"
	firstBinding := paymentport.CheckoutSessionBinding(first.Token)
	firstRequest := checkoutRecoveryRequest(t, first.Token, key, firstBinding)
	firstResponse := httptest.NewRecorder()
	loss.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusServiceUnavailable || !strings.Contains(firstResponse.Body.String(), "response_lost") {
		t.Fatalf("lost create status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// The original session and marker replay exactly one existing merchant
	// order after the dropped response.
	replayed := httptest.NewRecorder()
	loss.ServeHTTP(replayed, checkoutRecoveryRequest(t, first.Token, key, firstBinding))
	if replayed.Code != http.StatusAccepted {
		t.Fatalf("exact replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}
	var replayBody struct {
		MerchantOrderNo string `json:"merchant_order_no"`
	}
	if err = json.Unmarshal(replayed.Body.Bytes(), &replayBody); err != nil || replayBody.MerchantOrderNo == "" {
		t.Fatalf("replayed response=%s err=%v", replayed.Body.String(), err)
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// A different trusted session for the same authoritative customer sees an
	// opaque different marker. Sending the stored old one stops in the HTTP
	// boundary before payment Create can mutate anything.
	secondBindingResponse := httptest.NewRecorder()
	secondBindingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	secondBindingRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: second.Token})
	paymentHandler.ServeHTTP(secondBindingResponse, secondBindingRequest)
	if secondBindingResponse.Code != http.StatusOK || strings.Contains(secondBindingResponse.Body.String(), second.Token) {
		t.Fatalf("renewed session binding status=%d body=%s", secondBindingResponse.Code, secondBindingResponse.Body.String())
	}
	var bindingBody struct {
		Binding string `json:"checkout_session_binding"`
	}
	if err = json.Unmarshal(secondBindingResponse.Body.Bytes(), &bindingBody); err != nil || bindingBody.Binding == "" || bindingBody.Binding == firstBinding {
		t.Fatalf("renewed binding=%q err=%v", bindingBody.Binding, err)
	}
	oldMarker := httptest.NewRecorder()
	paymentHandler.ServeHTTP(oldMarker, checkoutRecoveryRequest(t, second.Token, key, firstBinding))
	if oldMarker.Code != http.StatusConflict || !strings.Contains(oldMarker.Body.String(), "session_mismatch") {
		t.Fatalf("old session marker status=%d body=%s", oldMarker.Code, oldMarker.Body.String())
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// Replacing the local marker with the new opaque value still cannot create a
	// second payment: Payment's persisted receipt includes the original trusted
	// session-bound command payload and returns a factual conflict.
	forgedMarker := httptest.NewRecorder()
	paymentHandler.ServeHTTP(forgedMarker, checkoutRecoveryRequest(t, second.Token, key, bindingBody.Binding))
	if forgedMarker.Code != http.StatusConflict || !strings.Contains(forgedMarker.Body.String(), `"conflict"`) {
		t.Fatalf("forged current marker status=%d body=%s", forgedMarker.Code, forgedMarker.Body.String())
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// A callback settles the real Payment/Order aggregate. Its JSAPI handoff is
	// deliberately expired afterward: terminal checkout reads must return the
	// persisted paid fact rather than reject the payer for an old prepay expiry.
	eventDigest := sha256.Sum256([]byte("checkout-recovery-terminal-event-0001"))
	bodyDigest := sha256.Sum256([]byte("checkout-recovery-terminal-body-0001"))
	if err = paymentService.ApplyVerifiedCallback(ctx, paymentprovider.CallbackResult{
		Kind:                         "payment",
		ProviderTransactionReference: "tx-public-checkout-recovery", MerchantOrderNo: replayBody.MerchantOrderNo,
		AmountMinor:               990,
		Currency:                  "CNY",
		OccurredAt:                time.Now().UTC(),
		ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", "tx-public-checkout-recovery")),
		EventDigest:               eventDigest,
		BodyDigest:                bodyDigest,
	}); err != nil {
		t.Fatalf("settle response-lost payment: %v", err)
	}
	var paymentID, effectID int64
	if err = pool.QueryRow(ctx, `SELECT id,external_effect_id FROM payments WHERE merchant_order_no=$1`, replayBody.MerchantOrderNo).Scan(&paymentID, &effectID); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(-time.Minute)
	if _, err = pool.Exec(ctx, `INSERT INTO payment_handoffs(payment_id,effect_id,payload,payload_digest,expires_at,created_at) VALUES($1,$2,'{"expired":true}'::jsonb,$3,$4,$5)`, paymentID, effectID, "sha256:"+strings.Repeat("a", 64), expiresAt, expiresAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	// The second OAuth session is newly issued and consequently unresolved. Its
	// verified payer identity/customer nevertheless authorizes an exact read of
	// the original merchant order; no browser beneficiary value is involved.
	if second.BeneficiarySelection != paymentport.BeneficiarySelectionUnresolved || second.BeneficiaryCustomerID != 0 {
		t.Fatalf("renewed session must begin unresolved: %+v", second)
	}
	terminal := httptest.NewRecorder()
	terminalRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/"+replayBody.MerchantOrderNo, nil)
	terminalRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: second.Token})
	paymentHandler.ServeHTTP(terminal, terminalRequest)
	if terminal.Code != http.StatusAccepted || !strings.Contains(terminal.Body.String(), `"status":"paid"`) || strings.Contains(terminal.Body.String(), `"handoff"`) {
		t.Fatalf("renewed same-payer terminal recovery status=%d body=%s", terminal.Code, terminal.Body.String())
	}
	if cookies := terminal.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("terminal recovery must retain the current trusted payer cookie for the exact-order reload: %+v", cookies)
	}
	// A browser reload keeps that short-lived trusted session and may re-read
	// only this terminal order. It must not re-open the payment mutation or
	// revive an expired handoff.
	reloaded := httptest.NewRecorder()
	reloadedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/"+replayBody.MerchantOrderNo, nil)
	reloadedRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: second.Token})
	paymentHandler.ServeHTTP(reloaded, reloadedRequest)
	if reloaded.Code != http.StatusAccepted || !strings.Contains(reloaded.Body.String(), `"status":"paid"`) || strings.Contains(reloaded.Body.String(), `"handoff"`) {
		t.Fatalf("terminal same-order reload status=%d body=%s", reloaded.Code, reloaded.Body.String())
	}
	if cookies := reloaded.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("terminal same-order reload must not replace the trusted payer cookie: %+v", cookies)
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// Another authoritative customer, even with a valid Payment session, cannot
	// read the merchant order. This also verifies the allow-read exception does
	// not become a canonical-customer or browser-claim bypass.
	var otherCustomerID int64
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&otherCustomerID); err != nil {
		t.Fatal(err)
	}
	otherSessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 72, customerID: customerdomain.CustomerID(otherCustomerID)}, customerstore.NewPostgreSQL(), paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	otherFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-h5", Value: "opaque-other-openid", Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	otherUnion, _ := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:fixture", Value: "other-fixture-union", Source: "payment-h5-oauth"})
	other, err := otherSessions.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: otherFact, UnionID: otherUnion, IdempotencyKey: "checkout-recovery-oauth-other-0001"})
	if err != nil {
		t.Fatal(err)
	}
	forbidden := httptest.NewRecorder()
	forbiddenRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/"+replayBody.MerchantOrderNo, nil)
	forbiddenRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: other.Token})
	paymentHandler.ServeHTTP(forbidden, forbiddenRequest)
	if forbidden.Code != http.StatusConflict || !strings.Contains(forbidden.Body.String(), `"conflict"`) || strings.Contains(forbidden.Body.String(), replayBody.MerchantOrderNo) {
		t.Fatalf("other payer terminal read status=%d body=%s", forbidden.Code, forbidden.Body.String())
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)
}

func checkoutRecoveryRequest(t *testing.T, token, key, binding string) *http.Request {
	t.Helper()
	body := `{"product_id":7,"product_kind":"standard","beneficiary_selection":"payer_self","coupon_claim_id":0,"checkout_session_binding":"` + binding + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	return request
}

func assertCheckoutRecoveryCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, expected int) {
	t.Helper()
	var orders, payments, receipts, effects int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM payments),(SELECT count(*) FROM payment_operation_receipts WHERE operation='create'),(SELECT count(*) FROM external_effects WHERE owner='payment')`).Scan(&orders, &payments, &receipts, &effects); err != nil || orders != expected || payments != expected || receipts != expected || effects != expected {
		t.Fatalf("orders=%d payments=%d receipts=%d effects=%d expected=%d err=%v", orders, payments, receipts, effects, expected, err)
	}
}

func (p checkoutRecoveryProvisioner) ProvisionVerifiedOAuthSubject(ctx context.Context, c identityport.OAuthSubjectCommand) (identityport.OAuthSubjectResult, error) {
	if !c.UnionID.Valid() {
		return identityport.OAuthSubjectResult{}, paymentsession.ErrInvalid
	}
	result, err := p.ProvisionVerifiedIdentity(ctx, identityport.ProvisionCommand{Fact: c.OpenID, IdempotencyKey: c.EventID})
	return identityport.OAuthSubjectResult{ProvisionResult: result}, err
}
