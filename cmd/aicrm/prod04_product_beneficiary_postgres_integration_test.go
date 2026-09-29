package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
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
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type prod04MatrixProducts map[productport.ID]productport.CheckoutProduct

func (products prod04MatrixProducts) ReadCheckoutProductWithin(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.CheckoutProduct, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return productport.CheckoutProduct{}, err
	}
	product, found := products[id]
	if !found || product.ProductType != kind {
		return productport.CheckoutProduct{}, errors.New("PROD-04 synthetic product not found")
	}
	return product, nil
}

func (products prod04MatrixProducts) ReadPublicServicePeriodByCode(_ context.Context, code string) (productport.CheckoutProduct, error) {
	for _, product := range products {
		if product.ProductType == productport.ProductOptionServicePeriod && product.Code == code {
			return product, nil
		}
	}
	return productport.CheckoutProduct{}, errors.New("PROD-04 synthetic public product not found")
}

// TestPROD04PostgreSQLAdminAssistedBeneficiaryIsolation exercises the existing
// trusted admin-assisted session and payment settlement paths with synthetic
// Provider callbacks. It deliberately does not invent a public gift flow or a
// single order with multiple beneficiaries.
func TestPROD04PostgreSQLAdminAssistedBeneficiaryIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}

	ordersRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, ordersRepository)
	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(ordersRepository)
	if err != nil {
		t.Fatal(err)
	}
	if err = orders.SetServicePeriodEntitlementCoordinator(fulfillment); err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	if err = effects.NewModuleRegistration().RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectRepository, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}

	var payerID, beneficiaryB, beneficiaryC, expiryCustomer int64
	for _, target := range []*int64{&payerID, &beneficiaryB, &beneficiaryC, &expiryCustomer} {
		if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(target); err != nil {
			t.Fatal(err)
		}
	}

	standardProduct := productport.CheckoutProduct{ID: 171, ProductType: productport.ProductOptionStandard, Code: "prod04-standard", Name: "PROD-04 普通商品", PriceMinor: 1200, Currency: "CNY", Version: 1}
	serviceProduct := productport.CheckoutProduct{ID: 172, ProductType: productport.ProductOptionServicePeriod, Code: "prod04-period", Name: "PROD-04 周期商品", PriceMinor: 1600, Currency: "CNY", Version: 1, ServicePeriodDurationDays: 31}
	products := prod04MatrixProducts{standardProduct.ID: standardProduct, serviceProduct.ID: serviceProduct}
	identityFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:prod04-local", Value: "synthetic-payer-a", Source: "prod04.local-provider"})
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := paymentsession.NewPostgreSQL()
	sessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 901, customerID: customerdomain.CustomerID(payerID)}, customerstore.NewPostgreSQL(), sessionStore, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orders, sessions, effectRepository, effectRepository)
	if err = payments.SetCheckoutProductReader(products); err != nil {
		t.Fatal(err)
	}
	if err = payments.SetPaymentChannelAppIDs("prod04-app", ""); err != nil {
		t.Fatal(err)
	}

	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	apiKey := []byte("0123456789abcdef0123456789abcdef")
	verifier, err := paymentprovider.NewCallbackVerifier(map[string]*rsa.PublicKey{"local-platform": &platformKey.PublicKey}, apiKey, "prod04-app", "prod04-merchant")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := paymenthttp.NewHandler(payments, verifier, commerceFundsSecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetTrustedSessionIssuer(commerceFundsSessionVerifier{fact: identityFact}, sessions); err != nil {
		t.Fatal(err)
	}

	createCheckout := func(beneficiaryID int64, product productport.CheckoutProduct, sessionKey, checkoutKey string) (int64, int64, string) {
		t.Helper()
		issued, issueErr := sessions.IssueTrusted(ctx, paymentsession.IssueCommand{BeneficiaryCustomerID: customerdomain.CustomerID(beneficiaryID), AdminAssisted: true, Fact: identityFact, IdempotencyKey: sessionKey})
		if issueErr != nil || issued.Token == "" || issued.BeneficiaryCustomerID != customerdomain.CustomerID(beneficiaryID) || issued.BeneficiarySelection != paymentport.BeneficiarySelectionAdminAssisted {
			t.Fatalf("issue trusted PROD-04 session beneficiary=%d issued=%+v err=%v", beneficiaryID, issued, issueErr)
		}
		cookie := &http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: issued.Token}
		bindingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
		bindingRequest.AddCookie(cookie)
		bindingResponse := httptest.NewRecorder()
		handler.ServeHTTP(bindingResponse, bindingRequest)
		binding := commerceFundsString(t, commerceFundsObject(t, bindingResponse, http.StatusOK), "checkout_session_binding")
		requestBody := commerceFundsJSON(t, map[string]any{"product_id": product.ID, "product_kind": string(product.ProductType), "checkout_session_binding": binding})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", bytes.NewReader(requestBody))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", checkoutKey)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		object := commerceFundsObject(t, response, http.StatusAccepted)
		return commerceFundsInt(t, object, "order_id"), commerceFundsInt(t, object, "payment_id"), commerceFundsString(t, object, "merchant_order_no")
	}
	applyPayment := func(merchant, eventID, transaction string, amount int64, occurredAt time.Time) {
		t.Helper()
		body, headers := commerceFundsSignedCallback(t, platformKey, apiKey, eventID, "TRANSACTION.SUCCESS", map[string]any{"appid": "prod04-app", "mchid": "prod04-merchant", "out_trade_no": merchant, "transaction_id": transaction, "trade_state": "SUCCESS", "success_time": occurredAt.Format(time.RFC3339Nano), "amount": map[string]any{"total": amount, "currency": "CNY"}})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", body, headers))
		if response.Code != http.StatusOK {
			t.Fatalf("PROD-04 synthetic payment callback status=%d body=%s", response.Code, response.Body.String())
		}
	}

	// A normal product sale keeps its immutable type but never creates service-period state.
	standardOrder, standardPayment, standardMerchant := createCheckout(beneficiaryB, standardProduct, "prod04-session-standard-0001", "prod04-checkout-standard-0001")
	applyPayment(standardMerchant, "prod04-event-standard-0001", "prod04-tx-standard-0001", standardProduct.PriceMinor, time.Now().UTC().Truncate(time.Microsecond))
	var standardPayer, standardBeneficiary int64
	var standardStatus, standardType string
	var standardDuration int32
	if err = pool.QueryRow(ctx, `SELECT o.payer_customer_id,o.beneficiary_customer_id,o.status,s.product_type,s.service_period_duration_days FROM orders o JOIN order_checkout_snapshots s ON s.order_id=o.id WHERE o.id=$1`, standardOrder).Scan(&standardPayer, &standardBeneficiary, &standardStatus, &standardType, &standardDuration); err != nil || standardPayer != payerID || standardBeneficiary != beneficiaryB || standardStatus != "paid" || standardType != "standard_product" || standardDuration != 0 {
		t.Fatalf("ordinary product order readback payer=%d beneficiary=%d status=%q type=%q days=%d err=%v", standardPayer, standardBeneficiary, standardStatus, standardType, standardDuration, err)
	}
	var standardGrants int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND source_order_id=$1`, standardOrder).Scan(&standardGrants); err != nil || standardGrants != 0 {
		t.Fatalf("ordinary product created entitlement grants=%d err=%v", standardGrants, err)
	}

	// Two separate trusted sessions can prebind separate recipients, but each order has exactly one beneficiary.
	periodOrderB, periodPaymentB, merchantB := createCheckout(beneficiaryB, serviceProduct, "prod04-session-b-0001", "prod04-checkout-b-0001")
	periodOrderC, periodPaymentC, merchantC := createCheckout(beneficiaryC, serviceProduct, "prod04-session-c-0001", "prod04-checkout-c-0001")
	paidAtB := time.Now().UTC().Truncate(time.Microsecond)
	paidAtC := paidAtB.Add(time.Second)
	applyPayment(merchantB, "prod04-event-b-0001", "prod04-tx-b-0001", serviceProduct.PriceMinor, paidAtB)
	applyPayment(merchantC, "prod04-event-c-0001", "prod04-tx-c-0001", serviceProduct.PriceMinor, paidAtC)
	for _, expected := range []struct {
		orderID, beneficiary int64
	}{{periodOrderB, beneficiaryB}, {periodOrderC, beneficiaryC}} {
		var payer, beneficiary int64
		var orderStatus, productType string
		var productID int64
		var duration int32
		if err = pool.QueryRow(ctx, `SELECT o.payer_customer_id,o.beneficiary_customer_id,o.status,s.product_type,s.product_id,s.service_period_duration_days FROM orders o JOIN order_checkout_snapshots s ON s.order_id=o.id WHERE o.id=$1`, expected.orderID).Scan(&payer, &beneficiary, &orderStatus, &productType, &productID, &duration); err != nil || payer != payerID || beneficiary != expected.beneficiary || orderStatus != "paid" || productType != "service_period" || productID != int64(serviceProduct.ID) || duration != serviceProduct.ServicePeriodDurationDays {
			t.Fatalf("period order %d readback payer=%d beneficiary=%d status=%q product=%q/%d days=%d err=%v", expected.orderID, payer, beneficiary, orderStatus, productType, productID, duration, err)
		}
		var entitlementCount, grantReceiptCount int
		if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM order_service_entitlements WHERE customer_id=$1 AND service_product_id=$2 AND last_order_id=$3),(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND source_order_id=$3)`, expected.beneficiary, serviceProduct.ID, expected.orderID).Scan(&entitlementCount, &grantReceiptCount); err != nil || entitlementCount != 1 || grantReceiptCount != 1 {
			t.Fatalf("period order %d entitlement=%d grant receipts=%d err=%v", expected.orderID, entitlementCount, grantReceiptCount, err)
		}
	}
	var payerEntitlements int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM order_service_entitlements WHERE customer_id=$1 AND service_product_id=$2`, payerID, serviceProduct.ID).Scan(&payerEntitlements); err != nil || payerEntitlements != 0 {
		t.Fatalf("payer unexpectedly received recipient entitlement count=%d err=%v", payerEntitlements, err)
	}

	// A successful refund against B's order must not alter C's independent aggregate.
	refundNo := commerceFundsRequestRefund(t, handler, periodPaymentB, serviceProduct.PriceMinor, "prod04-refund-b-0001", "prod04-refund-key-b-0001", "prod04-tx-b-0001")
	refundAt := time.Now().UTC().Truncate(time.Microsecond)
	refundBody, refundHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "prod04-event-refund-b-0001", "REFUND.SUCCESS", map[string]any{"appid": "prod04-app", "mchid": "prod04-merchant", "out_refund_no": refundNo, "refund_id": "prod04-provider-refund-b-0001", "refund_status": "SUCCESS", "success_time": refundAt.Format(time.RFC3339Nano), "amount": map[string]any{"refund": serviceProduct.PriceMinor, "total": serviceProduct.PriceMinor, "currency": "CNY"}})
	refundResponse := httptest.NewRecorder()
	handler.ServeHTTP(refundResponse, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", refundBody, refundHeaders))
	if refundResponse.Code != http.StatusOK {
		t.Fatalf("PROD-04 synthetic refund callback status=%d body=%s", refundResponse.Code, refundResponse.Body.String())
	}
	var statusB, statusC string
	if err = pool.QueryRow(ctx, `SELECT status FROM order_service_entitlements WHERE customer_id=$1 AND service_product_id=$2`, beneficiaryB, serviceProduct.ID).Scan(&statusB); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM order_service_entitlements WHERE customer_id=$1 AND service_product_id=$2`, beneficiaryC, serviceProduct.ID).Scan(&statusC); err != nil || statusB != "refunded" || statusC != "active" {
		t.Fatalf("cross-customer refund isolation B=%q C=%q err=%v", statusB, statusC, err)
	}
	var refundOrderStatus string
	var refundBeneficiary int64
	if err = pool.QueryRow(ctx, `SELECT o.status,o.beneficiary_customer_id FROM orders o WHERE o.id=$1`, periodOrderB).Scan(&refundOrderStatus, &refundBeneficiary); err != nil || refundOrderStatus != "refunded" || refundBeneficiary != beneficiaryB {
		t.Fatalf("refunded order state=%v beneficiary=%d err=%v", refundOrderStatus, refundBeneficiary, err)
	}
	if standardPayment < 1 || periodPaymentC < 1 {
		t.Fatalf("missing control payments: standard=%d beneficiary-c=%d", standardPayment, periodPaymentC)
	}

	// A synthetic expired row is fixture state, not a new paid order or timer. Read it through the existing public projection backed by Order's PG port.
	old := time.Now().UTC().AddDate(0, 0, -40).Truncate(time.Microsecond)
	expiredDigest := sha256.Sum256([]byte("prod04-expired-entitlement-fixture"))
	if _, err = pool.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('prod04-test-fixture','expired-period',$1,$2,$3,'active',$4,$5,'',$6,$4,$4)`, expiryCustomer, serviceProduct.ID, serviceProduct.Name, old.Add(-24*time.Hour), old, expiredDigest[:]); err != nil {
		t.Fatal(err)
	}
	expirySessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 902, customerID: customerdomain.CustomerID(expiryCustomer)}, customerstore.NewPostgreSQL(), sessionStore, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expiryFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:prod04-local", Value: "synthetic-expiry-customer", Source: "prod04.local-provider"})
	if err != nil {
		t.Fatal(err)
	}
	expirySession, err := expirySessions.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: expiryFact, IdempotencyKey: "prod04-expiry-session-0001"})
	if err != nil || expirySession.Token == "" {
		t.Fatalf("issue expiry state session=%+v err=%v", expirySession, err)
	}
	entitlementReader, err := orderapp.NewEntitlementApplication(uow, ordersRepository)
	if err != nil {
		t.Fatal(err)
	}
	publicHandler, err := producthttp.NewServicePeriodPublicHandler(products)
	if err != nil {
		t.Fatal(err)
	}
	if err = publicHandler.SetTrustedPublicState(uow, sessions, entitlementReader); err != nil {
		t.Fatal(err)
	}
	stateRequest := httptest.NewRequest(http.MethodGet, "/api/h5/service-period-products/"+serviceProduct.Code, nil)
	stateRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: expirySession.Token})
	stateResponse := httptest.NewRecorder()
	publicHandler.ServeHTTP(stateResponse, stateRequest)
	var state struct {
		Authenticated bool `json:"authenticated"`
		Entitlement   struct {
			Status        string `json:"status"`
			RemainingDays int    `json:"remaining_days"`
		} `json:"entitlement"`
		CTA string `json:"cta_text"`
	}
	if err = json.Unmarshal(stateResponse.Body.Bytes(), &state); err != nil || stateResponse.Code != http.StatusOK || !state.Authenticated || state.Entitlement.Status != "expired" || state.Entitlement.RemainingDays != 0 || state.CTA != "重新开通" {
		t.Fatalf("expired public projection status=%d body=%s parsed=%+v err=%v", stateResponse.Code, stateResponse.Body.String(), state, err)
	}

	var attempts, realExternalCalls int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE real_external_call_executed) FROM external_effect_attempts`).Scan(&attempts, &realExternalCalls); err != nil || attempts != 0 || realExternalCalls != 0 {
		t.Fatalf("PROD-04 unexpectedly attempted an external effect attempts=%d real_calls=%d err=%v", attempts, realExternalCalls, err)
	}
}
