package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	ordomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/payment"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	alipaysdk "github.com/smartwalle/alipay/v3"
	"github.com/smartwalle/nsign"
)

// TestPostgreSQLAlipayRefundCallbackUsesRefundTimestamp exercises the real
// Alipay callback handler, composed Payment/Order/entitlement UoW and a private
// PostgreSQL 16 database. The SDK key and all Provider results are synthetic;
// no Provider worker runs and no funds are touched.
func TestPostgreSQLAlipayRefundCallbackUsesRefundTimestamp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	requireLocalPostgreSQL16(t, ctx)
	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 110*time.Second, productExternalPushChromiumFixtureOptions{
		enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true,
	})
	callbackServer, signer := pay02AlipayCallbackServer(t, fixture.application.paymentReconciliation)
	t.Cleanup(callbackServer.Close)

	// Service-period full refund proves that the selected Provider timestamp
	// also reaches the existing entitlement revocation path inside the same UoW.
	period := pay02CreateAlipayCheckout(t, fixture, fixture.serviceProductID, "service_period", "pay02-service-period-checkout-0001")
	periodAmount := pay02ReadPaymentAmount(t, fixture, period.PaymentID)
	if periodAmount != 12800 {
		t.Fatalf("service-period fixture amount_minor=%d, want 12800", periodAmount)
	}
	periodPaymentAt := pay02NextPaymentTime(t, fixture, period.PaymentID)
	periodTradeNo := "pay02-provider-period-trade-0001"
	if status, body := pay02PostAlipayForm(t, callbackServer, pay02SignedPaymentForm(t, signer, "pay02-period-paid-notify-0001", period.MerchantOrder, periodTradeNo, periodAmount, periodPaymentAt)); status != http.StatusOK || body != "success" {
		t.Fatalf("signed service-period payment callback status=%d body=%q", status, body)
	}
	pay02AssertServiceEntitlement(t, fixture, period.OrderID, "active", nil, 0)

	periodRefundNo := "pay02-period-refund-0001"
	periodRefund := pay02RequestAlipayRefund(t, fixture, period.PaymentID, periodAmount, periodRefundNo, "pay02-period-refund-key-0001")
	periodAcceptedAt := pay02SetSyntheticRefundAcceptedAt(t, fixture, periodRefund.ID, periodPaymentAt)
	periodRefundAt := periodAcceptedAt.Add(time.Minute).Truncate(time.Second)
	refundForm := pay02SignedRefundForm(t, signer, "pay02-period-refund-notify-0001", period.MerchantOrder, periodTradeNo, periodRefundNo, periodAmount, periodAmount, &periodPaymentAt, periodRefundAt, periodRefundAt.Add(time.Minute))

	// Signature failure must stop before Payment/Order/entitlement persistence.
	invalidSignature := pay02CopyValues(refundForm)
	invalidSignature.Set("notify_id", "pay02-period-invalid-signature-0001")
	invalidSignature.Set("refund_amount", "127.99")
	invalidStatus, invalidBody := pay02PostAlipayForm(t, callbackServer, invalidSignature)
	invalidDigest := sha256.Sum256([]byte(invalidSignature.Get("notify_id")))
	invalidFacts := pay02ReadAlipayRefundFacts(t, fixture, period.OrderID, periodRefund.ID, invalidDigest[:], periodRefundAt)
	t.Logf("PAY02_INVALID_SIGNATURE status=%d body=%q refund_status=%s callback_receipts=%d settlement_audits=%d", invalidStatus, invalidBody, invalidFacts.RefundStatus, invalidFacts.CallbackCount, invalidFacts.PaymentSettlementCount)
	if invalidStatus != http.StatusUnauthorized || invalidFacts.RefundStatus != string(paymentdomain.RefundEffectAccepted) || !pay02NoSettlementWrites(invalidFacts) {
		t.Fatalf("invalid signature changed refund facts: status=%d body=%q SQL=%+v", invalidStatus, invalidBody, invalidFacts)
	}

	// gmt_refund itself is authoritative: a stale refund time remains a conflict
	// even when notify_time is later and the original payment time is present.
	staleForm := pay02SignedRefundForm(t, signer, "pay02-period-stale-refund-notify-0001", period.MerchantOrder, periodTradeNo, periodRefundNo, periodAmount, periodAmount, &periodPaymentAt, periodPaymentAt, periodRefundAt.Add(time.Minute))
	staleStatus, staleBody := pay02PostAlipayForm(t, callbackServer, staleForm)
	staleDigest := sha256.Sum256([]byte(staleForm.Get("notify_id")))
	staleFacts := pay02ReadAlipayRefundFacts(t, fixture, period.OrderID, periodRefund.ID, staleDigest[:], periodPaymentAt)
	t.Logf("PAY02_OLD_REFUND_TIME status=%d body=%q payment_at=%s refund_at=%s notify_at=%s SQL=%+v", staleStatus, staleBody, periodPaymentAt.Format(time.RFC3339), periodPaymentAt.Format(time.RFC3339), periodRefundAt.Add(time.Minute).Format(time.RFC3339), staleFacts)
	if staleStatus != http.StatusConflict || staleFacts.RefundStatus != string(paymentdomain.RefundEffectAccepted) || !pay02NoSettlementWrites(staleFacts) {
		t.Fatalf("stale gmt_refund did not reject without writes: status=%d body=%q SQL=%+v", staleStatus, staleBody, staleFacts)
	}

	// The defect reproduction shape: one verified form contains both an old
	// payment time and a later refund time. The callback must settle on gmt_refund.
	refundStatus, refundBody := pay02PostAlipayForm(t, callbackServer, refundForm)
	refundDigest := sha256.Sum256([]byte(refundForm.Get("notify_id")))
	refundFacts := pay02ReadAlipayRefundFacts(t, fixture, period.OrderID, periodRefund.ID, refundDigest[:], periodRefundAt)
	t.Logf("PAY02_DUAL_TIMESTAMP status=%d body=%q gmt_payment=%s gmt_refund=%s accepted_at=%s SQL=%+v", refundStatus, refundBody, periodPaymentAt.Format(time.RFC3339), periodRefundAt.Format(time.RFC3339), periodAcceptedAt.Format(time.RFC3339Nano), refundFacts)
	if refundStatus != http.StatusOK || refundBody != "success" || !pay02SettledAt(refundFacts, periodRefundAt, periodPaymentAt, periodAmount, "refunded", 1) {
		t.Fatalf("dual-timestamp callback did not settle at gmt_refund: status=%d body=%q SQL=%+v", refundStatus, refundBody, refundFacts)
	}
	pay02AssertServiceEntitlement(t, fixture, period.OrderID, "refunded", &periodRefundAt, periodAmount)

	// Replaying the same signed notification returns success without a second
	// settlement, Order transition, or entitlement refund receipt.
	replayStatus, replayBody := pay02PostAlipayForm(t, callbackServer, refundForm)
	replayFacts := pay02ReadAlipayRefundFacts(t, fixture, period.OrderID, periodRefund.ID, refundDigest[:], periodRefundAt)
	t.Logf("PAY02_REPLAY status=%d body=%q SQL=%+v", replayStatus, replayBody, replayFacts)
	if replayStatus != http.StatusOK || replayBody != "success" || !pay02SettledAt(replayFacts, periodRefundAt, periodPaymentAt, periodAmount, "refunded", 1) {
		t.Fatalf("refund replay produced a second or inconsistent settlement: status=%d body=%q SQL=%+v", replayStatus, replayBody, replayFacts)
	}
	pay02AssertServiceEntitlement(t, fixture, period.OrderID, "refunded", &periodRefundAt, periodAmount)

	// Compatibility form without gmt_refund: use the signed notify_time, never
	// the older gmt_payment. Unit-level signed normalization is also covered.
	fallback := pay02CreateAlipayCheckout(t, fixture, fixture.productID, "standard", "pay02-fallback-checkout-0001")
	fallbackAmount := pay02ReadPaymentAmount(t, fixture, fallback.PaymentID)
	fallbackPaymentAt := pay02NextPaymentTime(t, fixture, fallback.PaymentID)
	fallbackTradeNo := "pay02-provider-fallback-trade-0001"
	if status, body := pay02PostAlipayForm(t, callbackServer, pay02SignedPaymentForm(t, signer, "pay02-fallback-paid-notify-0001", fallback.MerchantOrder, fallbackTradeNo, fallbackAmount, fallbackPaymentAt)); status != http.StatusOK || body != "success" {
		t.Fatalf("signed fallback payment callback status=%d body=%q", status, body)
	}
	fallbackRefundNo := "pay02-fallback-refund-0001"
	fallbackRefund := pay02RequestAlipayRefund(t, fixture, fallback.PaymentID, 300, fallbackRefundNo, "pay02-fallback-refund-key-0001")
	fallbackAcceptedAt := pay02SetSyntheticRefundAcceptedAt(t, fixture, fallbackRefund.ID, fallbackPaymentAt)
	fallbackAt := fallbackAcceptedAt.Add(2 * time.Minute).Truncate(time.Second)
	fallbackForm := pay02SignedRefundForm(t, signer, "pay02-fallback-refund-notify-0001", fallback.MerchantOrder, fallbackTradeNo, fallbackRefundNo, fallbackAmount, 300, &fallbackPaymentAt, time.Time{}, fallbackAt)
	if _, exists := fallbackForm["gmt_refund"]; exists {
		t.Fatal("fallback compatibility form unexpectedly contains gmt_refund")
	}
	fallbackStatus, fallbackBody := pay02PostAlipayForm(t, callbackServer, fallbackForm)
	fallbackDigest := sha256.Sum256([]byte(fallbackForm.Get("notify_id")))
	fallbackFacts := pay02ReadAlipayRefundFacts(t, fixture, fallback.OrderID, fallbackRefund.ID, fallbackDigest[:], fallbackAt)
	t.Logf("PAY02_NOTIFY_TIME_FALLBACK status=%d body=%q gmt_payment=%s notify_time=%s SQL=%+v", fallbackStatus, fallbackBody, fallbackPaymentAt.Format(time.RFC3339), fallbackAt.Format(time.RFC3339), fallbackFacts)
	if fallbackStatus != http.StatusOK || fallbackBody != "success" || !pay02SettledAt(fallbackFacts, fallbackAt, fallbackPaymentAt, 300, "partially_refunded", 1) {
		t.Fatalf("refund callback without gmt_refund did not use notify_time: status=%d body=%q SQL=%+v", fallbackStatus, fallbackBody, fallbackFacts)
	}

	// A synthetic lost-response attempt puts the refund into outcome_unknown.
	// The signed callback then completes that existing refund without a real
	// Provider call or worker retry.
	unknownProductID := pay02CreateSyntheticStandardProduct(t, fixture, "pay02-unknown-standard-product")
	unknown := pay02CreateAlipayCheckout(t, fixture, unknownProductID, "standard", "pay02-unknown-checkout-0001")
	unknownAmount := pay02ReadPaymentAmount(t, fixture, unknown.PaymentID)
	unknownPaymentAt := pay02NextPaymentTime(t, fixture, unknown.PaymentID)
	unknownTradeNo := "pay02-provider-unknown-trade-0001"
	if status, body := pay02PostAlipayForm(t, callbackServer, pay02SignedPaymentForm(t, signer, "pay02-unknown-paid-notify-0001", unknown.MerchantOrder, unknownTradeNo, unknownAmount, unknownPaymentAt)); status != http.StatusOK || body != "success" {
		t.Fatalf("signed unknown-case payment callback status=%d body=%q", status, body)
	}
	unknownRefundNo := "pay02-unknown-refund-0001"
	unknownRefund := pay02RequestAlipayRefund(t, fixture, unknown.PaymentID, 400, unknownRefundNo, "pay02-unknown-refund-key-0001")
	unknownAcceptedAt := pay02SetSyntheticRefundAcceptedAt(t, fixture, unknownRefund.ID, unknownPaymentAt)
	unknownAdapter := &pay02UnknownAlipayRefundAdapter{}
	if err := pay02RunUnknownAlipayRefundEffect(t, ctx, fixture, unknownRefund.ID, unknownAdapter); err != nil {
		t.Fatalf("run synthetic outcome_unknown attempt: %v", err)
	}
	if unknownAdapter.calls != 1 || unknownAdapter.realExternalCall {
		t.Fatalf("unknown adapter evidence calls=%d real_external_call=%t", unknownAdapter.calls, unknownAdapter.realExternalCall)
	}
	var unknownRefundState, unknownEffectState string
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT refund.status,effect.state FROM payment_refunds refund JOIN external_effects effect ON effect.id=refund.external_effect_id WHERE refund.id=$1`, unknownRefund.ID).Scan(&unknownRefundState, &unknownEffectState); err != nil {
		t.Fatal(err)
	}
	if unknownRefundState != string(paymentdomain.RefundOutcomeUnknown) || unknownEffectState != string(effectport.StateUnknown) {
		t.Fatalf("synthetic unknown path states refund=%q effect=%q", unknownRefundState, unknownEffectState)
	}
	unknownAt := unknownAcceptedAt.Add(3 * time.Minute).Truncate(time.Second)
	unknownForm := pay02SignedRefundForm(t, signer, "pay02-unknown-refund-notify-0001", unknown.MerchantOrder, unknownTradeNo, unknownRefundNo, unknownAmount, 400, &unknownPaymentAt, unknownAt, unknownAt.Add(time.Minute))
	unknownStatus, unknownBody := pay02PostAlipayForm(t, callbackServer, unknownForm)
	unknownDigest := sha256.Sum256([]byte(unknownForm.Get("notify_id")))
	unknownFacts := pay02ReadAlipayRefundFacts(t, fixture, unknown.OrderID, unknownRefund.ID, unknownDigest[:], unknownAt)
	t.Logf("PAY02_OUTCOME_UNKNOWN_CALLBACK status=%d body=%q refund_accepted_at=%s refund_at=%s adapter_calls=%d real_external_call=%t SQL=%+v", unknownStatus, unknownBody, unknownAcceptedAt.Format(time.RFC3339Nano), unknownAt.Format(time.RFC3339), unknownAdapter.calls, unknownAdapter.realExternalCall, unknownFacts)
	if unknownStatus != http.StatusOK || unknownBody != "success" || !pay02SettledAt(unknownFacts, unknownAt, unknownPaymentAt, 400, "partially_refunded", 1) {
		t.Fatalf("verified callback did not settle outcome_unknown refund: status=%d body=%q SQL=%+v", unknownStatus, unknownBody, unknownFacts)
	}
	var finalEffectState string
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT state FROM external_effects WHERE id=(SELECT external_effect_id FROM payment_refunds WHERE id=$1)`, unknownRefund.ID).Scan(&finalEffectState); err != nil || finalEffectState != string(effectport.StateUnknown) {
		t.Fatalf("callback unexpectedly rewrote EER attempt state=%q err=%v", finalEffectState, err)
	}
}

type pay02AlipayCallbackSigner struct {
	signer *alipaysdk.Client
}

func pay02AlipayCallbackServer(t *testing.T, app paymenthttp.Application) (*httptest.Server, *pay02AlipayCallbackSigner) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	provider, err := paymentprovider.NewAlipay(paymentprovider.AlipayConfig{
		Enabled: true, Production: true, AppID: "virtual-alipay-test-app", PrivateKey: privatePEM,
		AlipayPublicKey: publicPEM, NotifyURL: "https://example.test/api/public/alipay/callback", ReturnURL: "https://example.test/pay/alipay/return",
	})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := alipaysdk.New("virtual-alipay-test-app", privatePEM, true)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := paymenthttp.NewHandler(app, nil, &alipayCallbackRegressionRequestSecurity{}, true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetAlipayCallbackVerifier(provider); err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(handler), &pay02AlipayCallbackSigner{signer: signer}
}

func pay02CreateAlipayCheckout(t *testing.T, fixture *productExternalPushChromiumFixture, productID int64, productKind, key string) alipayCheckoutCreateResult {
	t.Helper()
	session := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, key+"-session")
	binding := publicAlipayCheckoutBinding(t, fixture, session.token)
	body := fmt.Sprintf(`{"product_id":%d,"product_kind":%q,"provider":"alipay","channel":"alipay_wap","beneficiary_selection":"payer_self","checkout_session_binding":%q}`, productID, productKind, binding)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
	fixture.application.handler.ServeHTTP(response, request)
	var created alipayCheckoutCreateResult
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &created) != nil || created.OrderID < 1 || created.PaymentID < 1 || created.MerchantOrder == "" {
		t.Fatalf("create Alipay %s checkout status=%d body=%s", productKind, response.Code, response.Body.String())
	}
	return created
}

func pay02CreateSyntheticStandardProduct(t *testing.T, fixture *productExternalPushChromiumFixture, productCode string) int64 {
	t.Helper()
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`
	var productID int64
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES($1,'PAY-02 synthetic standard product','Isolated Alipay refund callback fixture',9900,'CNY',10,1,$2::jsonb) RETURNING id`, productCode, projection).Scan(&productID)
	if err != nil {
		t.Fatalf("create synthetic standard product %q: %v", productCode, err)
	}
	return productID
}

func pay02ReadPaymentAmount(t *testing.T, fixture *productExternalPushChromiumFixture, paymentID int64) int64 {
	t.Helper()
	var amount int64
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT amount_minor FROM payments WHERE id=$1`, paymentID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	return amount
}

func pay02RequestAlipayRefund(t *testing.T, fixture *productExternalPushChromiumFixture, paymentID, amount int64, refundNo, key string) paymentdomain.Refund {
	t.Helper()
	refund, err := fixture.application.paymentReconciliation.RequestRefund(fixture.ctx, paymentport.RefundCommand{
		PaymentID: paymentID, AmountMinor: amount, RefundNo: refundNo, Reason: "synthetic PAY-02 callback timestamp test",
		ActorScope: "admin:pay02", IdempotencyKey: key,
	})
	if err != nil || refund.Status != paymentdomain.RefundEffectAccepted {
		t.Fatalf("request synthetic Alipay refund no=%s status=%s err=%v", refundNo, refund.Status, err)
	}
	return refund
}

func pay02NextPaymentTime(t *testing.T, fixture *productExternalPushChromiumFixture, paymentID int64) time.Time {
	t.Helper()
	var paymentUpdatedAt time.Time
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT updated_at FROM payments WHERE id=$1`, paymentID).Scan(&paymentUpdatedAt); err != nil {
		t.Fatal(err)
	}
	// Alipay's form timestamp has second precision. Choose the next whole
	// second after the accepted checkout state so Payment.Settle sees a
	// monotonic event; then the private fixture anchors refund acceptance later.
	return paymentUpdatedAt.UTC().Truncate(time.Second).Add(time.Second)
}

func pay02SetSyntheticRefundAcceptedAt(t *testing.T, fixture *productExternalPushChromiumFixture, refundID int64, paymentAt time.Time) time.Time {
	t.Helper()
	acceptedAt := paymentAt.Add(10 * time.Minute)
	result, err := fixture.application.pool.Native().Exec(fixture.ctx, `UPDATE payment_refunds SET updated_at=$2 WHERE id=$1 AND status=$3`, refundID, acceptedAt, paymentdomain.RefundEffectAccepted)
	if err != nil {
		t.Fatalf("set synthetic private-PG refund acceptance time: %v", err)
	}
	if rows := result.RowsAffected(); rows != 1 {
		t.Fatalf("set synthetic private-PG refund acceptance time affected=%d", rows)
	}
	t.Logf("PAY02_SYNTHETIC_TIMELINE payment_at=%s refund_accepted_at=%s (private PG fixture timestamp only)", paymentAt.Format(time.RFC3339), acceptedAt.Format(time.RFC3339))
	return acceptedAt
}

func pay02SignedPaymentForm(t *testing.T, signer *pay02AlipayCallbackSigner, notifyID, merchantOrder, tradeNo string, amount int64, occurredAt time.Time) url.Values {
	t.Helper()
	values := url.Values{
		"app_id": {"virtual-alipay-test-app"}, "notify_id": {notifyID}, "out_trade_no": {merchantOrder},
		"trade_no": {tradeNo}, "trade_status": {"TRADE_SUCCESS"}, "total_amount": {pay02AmountString(amount)},
		"gmt_payment": {pay02CSTString(occurredAt)}, "notify_time": {pay02CSTString(occurredAt)}, "sign_type": {"RSA2"},
	}
	return pay02SignValues(t, signer, values)
}

func pay02SignedRefundForm(t *testing.T, signer *pay02AlipayCallbackSigner, notifyID, merchantOrder, tradeNo, refundNo string, totalAmount, amount int64, paymentAt *time.Time, refundAt time.Time, notifyAt time.Time) url.Values {
	t.Helper()
	values := url.Values{
		"app_id": {"virtual-alipay-test-app"}, "notify_id": {notifyID}, "out_trade_no": {merchantOrder},
		"trade_no": {tradeNo}, "trade_status": {"TRADE_SUCCESS"}, "total_amount": {pay02AmountString(totalAmount)},
		"out_request_no": {refundNo}, "refund_amount": {pay02AmountString(amount)},
		"notify_time": {pay02CSTString(notifyAt)}, "sign_type": {"RSA2"},
	}
	if paymentAt != nil {
		values.Set("gmt_payment", pay02CSTString(*paymentAt))
	}
	if !refundAt.IsZero() {
		values.Set("gmt_refund", pay02CSTString(refundAt))
	}
	return pay02SignValues(t, signer, values)
}

func pay02SignValues(t *testing.T, signer *pay02AlipayCallbackSigner, values url.Values) url.Values {
	t.Helper()
	signature, err := signer.signer.SignValues(values, nsign.WithIgnore("sign", "sign_type", "alipay_cert_sn"))
	if err != nil {
		t.Fatal(err)
	}
	values.Set("sign", base64.StdEncoding.EncodeToString(signature))
	return values
}

func pay02CopyValues(values url.Values) url.Values {
	copyValues := make(url.Values, len(values))
	for key, entries := range values {
		copyValues[key] = append([]string(nil), entries...)
	}
	return copyValues
}

func pay02PostAlipayForm(t *testing.T, server *httptest.Server, values url.Values) (int, string) {
	t.Helper()
	response, err := server.Client().PostForm(server.URL+"/api/public/alipay/callback", values)
	if err != nil {
		t.Fatalf("POST signed synthetic Alipay callback: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

type pay02AlipayRefundFacts struct {
	RefundStatus           string
	RefundAmount           int64
	RefundUpdatedAt        time.Time
	PaymentStatus          string
	PaidConfirmedAt        *time.Time
	OrderStatus            string
	OrderRefundedMinor     int64
	OrderUpdatedAt         time.Time
	CallbackCount          int64
	PaymentSettlementCount int64
	PaymentAuditAt         sql.NullTime
	PaymentOutboxAt        sql.NullTime
	OperationReceiptAt     sql.NullTime
	OrderRefundCount       int64
	OrderRefundAt          sql.NullTime
	OrderAuditAt           sql.NullTime
	OrderOutboxAt          sql.NullTime
}

func pay02ReadAlipayRefundFacts(t *testing.T, fixture *productExternalPushChromiumFixture, orderID, refundID int64, eventDigest []byte, eventAt time.Time) pay02AlipayRefundFacts {
	t.Helper()
	var facts pay02AlipayRefundFacts
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `
SELECT refund.status,refund.amount_minor,refund.updated_at,payment.status,payment.paid_confirmed_at,
       order_row.status,order_row.refunded_minor,order_row.updated_at,
       (SELECT count(*) FROM payment_callback_receipts WHERE provider='alipay' AND event_digest=$3),
       (SELECT count(*) FROM payment_audit_events WHERE event_type='payment.refund_settled' AND aggregate_id=refund.id AND occurred_at=$4),
       (SELECT max(occurred_at) FROM payment_audit_events WHERE event_type='payment.refund_settled' AND aggregate_id=refund.id),
       (SELECT max(occurred_at) FROM payment_outbox WHERE event_type='payment.refund_settled' AND aggregate_id=refund.id),
       (SELECT max(created_at) FROM payment_operation_receipts WHERE operation='callback' AND actor_scope='provider' AND result_kind='refund' AND result_id=refund.id),
       (SELECT count(*) FROM order_status_history WHERE order_id=order_row.id AND to_status IN ('partially_refunded','refunded') AND occurred_at=$4),
       (SELECT max(occurred_at) FROM order_status_history WHERE order_id=order_row.id AND to_status IN ('partially_refunded','refunded')),
       (SELECT max(occurred_at) FROM order_audit_events WHERE order_id=order_row.id AND event_type='order.status_changed' AND payload->>'status' IN ('partially_refunded','refunded')),
       (SELECT max(occurred_at) FROM order_outbox WHERE aggregate_id=order_row.id AND event_type='order.status_changed' AND payload->>'status' IN ('partially_refunded','refunded'))
FROM payment_refunds refund JOIN payments payment ON payment.id=refund.payment_id JOIN orders order_row ON order_row.id=$1
WHERE refund.id=$2`, orderID, refundID, eventDigest, eventAt).Scan(
		&facts.RefundStatus, &facts.RefundAmount, &facts.RefundUpdatedAt, &facts.PaymentStatus, &facts.PaidConfirmedAt,
		&facts.OrderStatus, &facts.OrderRefundedMinor, &facts.OrderUpdatedAt,
		&facts.CallbackCount, &facts.PaymentSettlementCount, &facts.PaymentAuditAt,
		&facts.PaymentOutboxAt, &facts.OperationReceiptAt, &facts.OrderRefundCount,
		&facts.OrderRefundAt, &facts.OrderAuditAt, &facts.OrderOutboxAt,
	)
	if err != nil {
		t.Fatalf("read Alipay refund callback SQL facts: %v", err)
	}
	return facts
}

func pay02NoSettlementWrites(facts pay02AlipayRefundFacts) bool {
	return facts.CallbackCount == 0 && facts.PaymentSettlementCount == 0 && !facts.PaymentAuditAt.Valid && !facts.PaymentOutboxAt.Valid && !facts.OperationReceiptAt.Valid && facts.OrderRefundCount == 0 && !facts.OrderRefundAt.Valid && !facts.OrderAuditAt.Valid && !facts.OrderOutboxAt.Valid
}

func pay02SettledAt(facts pay02AlipayRefundFacts, expectedAt, expectedPaymentAt time.Time, amount int64, orderStatus string, transitions int64) bool {
	return facts.RefundStatus == string(paymentdomain.RefundCompleted) && facts.RefundAmount == amount && facts.RefundUpdatedAt.Equal(expectedAt) &&
		facts.PaymentStatus == string(paymentdomain.StatusPaid) && facts.PaidConfirmedAt != nil && facts.PaidConfirmedAt.Equal(expectedPaymentAt) &&
		facts.OrderStatus == orderStatus && facts.OrderRefundedMinor == amount && facts.OrderUpdatedAt.Equal(expectedAt) &&
		facts.CallbackCount == 1 && facts.PaymentSettlementCount == 1 && facts.PaymentAuditAt.Valid && facts.PaymentAuditAt.Time.Equal(expectedAt) &&
		facts.PaymentOutboxAt.Valid && facts.PaymentOutboxAt.Time.Equal(expectedAt) && facts.OperationReceiptAt.Valid && facts.OperationReceiptAt.Time.Equal(expectedAt) &&
		facts.OrderRefundCount == transitions && facts.OrderRefundAt.Valid && facts.OrderRefundAt.Time.Equal(expectedAt) &&
		facts.OrderAuditAt.Valid && facts.OrderAuditAt.Time.Equal(expectedAt) && facts.OrderOutboxAt.Valid && facts.OrderOutboxAt.Time.Equal(expectedAt)
}

func pay02AssertServiceEntitlement(t *testing.T, fixture *productExternalPushChromiumFixture, orderID int64, wantStatus string, wantAt *time.Time, wantRefundAmount int64) {
	t.Helper()
	var status string
	var endAt, updatedAt time.Time
	var grantReceipts, refundReceipts int64
	var refundAmount int64
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `
SELECT entitlement.status,entitlement.end_at,entitlement.updated_at,
       (SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE source_order_id=$1 AND operation='grant'),
       (SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE source_order_id=$1 AND operation='refund'),
       coalesce((SELECT max(refund_amount_minor) FROM order_entitlement_fulfillment_receipts WHERE source_order_id=$1 AND operation='refund'),0)
FROM order_service_entitlements entitlement WHERE entitlement.last_order_id=$1`, orderID).
		Scan(&status, &endAt, &updatedAt, &grantReceipts, &refundReceipts, &refundAmount)
	if err != nil {
		t.Fatalf("read service-period entitlement order=%d: %v", orderID, err)
	}
	wantRefundReceipts := int64(0)
	if wantAt != nil {
		wantRefundReceipts = 1
	}
	if status != wantStatus || grantReceipts != 1 || refundReceipts != wantRefundReceipts || refundAmount != wantRefundAmount {
		t.Fatalf("service-period entitlement status=%s grants=%d refunds=%d refund_amount=%d", status, grantReceipts, refundReceipts, refundAmount)
	}
	if wantAt != nil && (!endAt.Equal(*wantAt) || !updatedAt.Equal(*wantAt)) {
		t.Fatalf("service-period entitlement end/updated_at=%s/%s want=%s", endAt, updatedAt, *wantAt)
	}
}

type pay02UnknownAlipayRefundAdapter struct {
	calls            int
	realExternalCall bool
}

func (adapter *pay02UnknownAlipayRefundAdapter) Execute(_ context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	adapter.calls++
	if envelope.Owner != effectport.OwnerPayment || envelope.Kind != effectport.KindAlipayRefund || attempt.Number < 1 {
		return effectport.AdapterResult{}, fmt.Errorf("unexpected PAY-02 test effect owner=%s kind=%s attempt=%d", envelope.Owner, envelope.Kind, attempt.Number)
	}
	return effectport.AdapterResult{
		Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("pay02.synthetic.response-lost", string(envelope.PayloadDigest)),
		CallAttempted: true, RealExternalCallExecuted: false,
	}, nil
}

type pay02UnusedOrderCoordinator struct{}

func (pay02UnusedOrderCoordinator) ReservePaymentWithin(context.Context, int64) (ordomain.Snapshot, error) {
	return ordomain.Snapshot{}, fmt.Errorf("unexpected order reservation in PAY-02 refund effect completion")
}

func (pay02UnusedOrderCoordinator) CreatePaymentOrderWithin(context.Context, orderport.PaymentOrderCommand) (ordomain.Snapshot, error) {
	return ordomain.Snapshot{}, fmt.Errorf("unexpected order creation in PAY-02 refund effect completion")
}

func (pay02UnusedOrderCoordinator) SettlePaymentWithin(context.Context, orderport.PaymentSettlementCommand) (ordomain.Snapshot, error) {
	return ordomain.Snapshot{}, fmt.Errorf("unexpected order settlement in PAY-02 refund effect completion")
}

func pay02RunUnknownAlipayRefundEffect(t *testing.T, ctx context.Context, fixture *productExternalPushChromiumFixture, refundID int64, adapter *pay02UnknownAlipayRefundAdapter) error {
	t.Helper()
	pool := fixture.application.pool.Native()
	workers := river.NewWorkers()
	worker := effects.NewWorker(nil, adapter)
	if err := river.AddWorkerSafely[effects.EffectJobArgs](workers, worker); err != nil {
		return err
	}
	if err := river.AddWorkerSafely[paymentmodule.ReconciliationJobArgs](workers, paymentmodule.NewReconciliationWorker()); err != nil {
		return err
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		return err
	}
	repository, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		return err
	}
	reconciliationEnqueuer, err := paymentmodule.NewRiverReconciliationEnqueuer(insertClient)
	if err != nil {
		return err
	}
	completionSink, err := paymentmodule.NewCompletionSink(paymentstore.NewPostgreSQL(), reconciliationEnqueuer, pay02UnusedOrderCoordinator{})
	if err != nil {
		return err
	}
	if err = repository.SetCompletionSink(completionSink); err != nil {
		return err
	}
	if err = worker.BindRepository(repository); err != nil {
		return err
	}
	var effectID, generation, riverJobID int64
	if err = pool.QueryRow(ctx, `SELECT effect.id,effect.generation,effect_job.river_job_id FROM payment_refunds refund JOIN external_effects effect ON effect.id=refund.external_effect_id JOIN external_effect_jobs effect_job ON effect_job.effect_id=effect.id AND effect_job.generation=effect.generation WHERE refund.id=$1`, refundID).Scan(&effectID, &generation, &riverJobID); err != nil {
		return err
	}
	job := &river.Job[effects.EffectJobArgs]{JobRow: &rivertype.JobRow{ID: riverJobID}, Args: effects.EffectJobArgs{EffectID: effectID, Generation: generation}}
	return worker.Work(ctx, job)
}

func pay02AmountString(amount int64) string { return fmt.Sprintf("%d.%02d", amount/100, amount%100) }

func pay02CSTString(value time.Time) string {
	return value.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05")
}
