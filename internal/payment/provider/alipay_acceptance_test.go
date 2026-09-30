package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	alipaysdk "github.com/smartwalle/alipay/v3"
	"github.com/smartwalle/nsign"
)

// The two SDK clients use ephemeral test keys. No merchant credential or
// external Alipay endpoint is involved in this provider contract fixture.
func alipayContractFixture(t *testing.T, gateway string) (*Alipay, *alipaysdk.Client) {
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
	provider, err := NewAlipay(AlipayConfig{
		Enabled: true, Production: true, AppID: "test-alipay-app", PrivateKey: privatePEM,
		AlipayPublicKey: publicPEM, Gateway: gateway,
		NotifyURL: "https://example.test/api/public/alipay/callback",
		ReturnURL: "https://example.test/h5/result.html",
	})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := alipaysdk.New("test-alipay-app", privatePEM, true)
	if err != nil {
		t.Fatal(err)
	}
	return provider, signer
}

func TestAlipayEncryptedWebLinksKeepCiphertextAndPaymentReturn(t *testing.T) {
	provider, _ := alipayContractFixture(t, "")
	key := base64.StdEncoding.EncodeToString([]byte("0123456789ABCDEF"))
	if err := provider.client.SetEncryptKey(key); err != nil {
		t.Fatal(err)
	}
	for _, build := range []func(context.Context, WebPayRequest) (string, error){provider.BuildWapPay, provider.BuildPagePay} {
		raw, err := build(context.Background(), WebPayRequest{MerchantOrderNo: "merchant-encrypted", Subject: "Synthetic", TotalAmount: "9.90"})
		if err != nil {
			t.Fatal(err)
		}
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		query := target.Query()
		ciphertext, err := base64.StdEncoding.DecodeString(query.Get("biz_content"))
		if err != nil || len(ciphertext) == 0 || len(ciphertext)%16 != 0 || query.Get("encrypt_type") != "AES" || query.Get("return_url") != "https://example.test/pay/alipay/return" {
			t.Fatalf("SDK encryption/return contract failed: %v", err)
		}
		if json.Valid([]byte(query.Get("biz_content"))) {
			t.Fatal("encrypted content treated as plaintext JSON")
		}
	}
}

func TestAlipayAcceptanceSignedCallbackRejectsTamperAndWrongApp(t *testing.T) {
	provider, signer := alipayContractFixture(t, "")
	values := url.Values{
		"app_id": {"test-alipay-app"}, "notify_id": {"notify-test-1"},
		"out_trade_no": {"merchant-test-1"}, "trade_no": {"trade-test-1"},
		"trade_status": {"TRADE_SUCCESS"}, "total_amount": {"9.90"},
		"notify_time": {"2026-09-23 12:00:00"}, "sign_type": {"RSA2"},
	}
	sign := func(v url.Values) {
		t.Helper()
		signature, err := signer.SignValues(v, nsign.WithIgnore("sign", "sign_type", "alipay_cert_sn"))
		if err != nil {
			t.Fatal(err)
		}
		v.Set("sign", base64.StdEncoding.EncodeToString(signature))
	}
	sign(values)
	got, err := provider.VerifyValues(context.Background(), values)
	if err != nil || got.Kind != "payment" || got.MerchantOrderNo != "merchant-test-1" || got.AmountMinor != 990 || got.ProviderTransactionReference != "trade-test-1" {
		t.Fatalf("signed callback normalization failed: kind=%q merchant=%q amount=%d err=%v", got.Kind, got.MerchantOrderNo, got.AmountMinor, err)
	}
	tampered := url.Values{}
	for key, entries := range values {
		tampered[key] = append([]string(nil), entries...)
	}
	tampered.Set("total_amount", "99.90")
	if _, err := provider.VerifyValues(context.Background(), tampered); err == nil {
		t.Fatal("tampered amount accepted")
	}
	wrongApp := url.Values{}
	for key, entries := range values {
		wrongApp[key] = append([]string(nil), entries...)
	}
	wrongApp.Set("app_id", "other-app")
	sign(wrongApp)
	if _, err := provider.VerifyValues(context.Background(), wrongApp); err == nil {
		t.Fatal("valid signature for wrong app accepted")
	}
}

func TestAlipayAcceptanceSignedCallbackKindSpecificTimestamps(t *testing.T) {
	provider, signer := alipayContractFixture(t, "")
	sign := func(values url.Values) {
		t.Helper()
		signature, err := signer.SignValues(values, nsign.WithIgnore("sign", "sign_type", "alipay_cert_sn"))
		if err != nil {
			t.Fatal(err)
		}
		values.Set("sign", base64.StdEncoding.EncodeToString(signature))
	}
	copyValues := func(source url.Values) url.Values {
		result := make(url.Values, len(source))
		for key, entries := range source {
			result[key] = append([]string(nil), entries...)
		}
		return result
	}
	refundValues := url.Values{
		"app_id": {"test-alipay-app"}, "notify_id": {"notify-kind-specific-refund-1"},
		"out_trade_no": {"merchant-test-1"}, "trade_no": {"trade-test-1"},
		"out_request_no": {"refund-test-1"}, "refund_amount": {"1.20"},
		"trade_status": {"TRADE_SUCCESS"}, "gmt_payment": {"2026-09-30 15:48:58"},
		"gmt_refund": {"2026-09-30 16:08:58"}, "notify_time": {"2026-09-30 16:18:58"}, "sign_type": {"RSA2"},
	}
	wantPaymentAt := time.Date(2026, 9, 30, 7, 48, 58, 0, time.UTC)
	wantRefundAt := time.Date(2026, 9, 30, 8, 8, 58, 0, time.UTC)
	wantNotifyAt := time.Date(2026, 9, 30, 8, 18, 58, 0, time.UTC)
	verify := func(values url.Values, kind string, want time.Time) {
		t.Helper()
		sign(values)
		got, err := provider.VerifyValues(context.Background(), values)
		if err != nil || got.Kind != kind || !got.OccurredAt.Equal(want) {
			t.Fatalf("signed %s timestamp got kind=%q occurred_at=%s want=%s err=%v", kind, got.Kind, got.OccurredAt, want, err)
		}
	}

	// Payment keeps its existing precedence even if a refund field is present.
	paymentValues := url.Values{
		"app_id": {"test-alipay-app"}, "notify_id": {"notify-kind-specific-payment-1"},
		"out_trade_no": {"merchant-test-1"}, "trade_no": {"trade-test-1"},
		"trade_status": {"TRADE_SUCCESS"}, "total_amount": {"9.90"},
		"gmt_payment": {"2026-09-30 15:48:58"}, "gmt_refund": {"2026-09-30 16:08:58"},
		"notify_time": {"2026-09-30 16:18:58"}, "sign_type": {"RSA2"},
	}
	verify(paymentValues, "payment", wantPaymentAt)

	verify(copyValues(refundValues), "refund", wantRefundAt)
	refundOnly := copyValues(refundValues)
	delete(refundOnly, "gmt_payment")
	refundOnly.Set("notify_id", "notify-kind-specific-refund-only-1")
	verify(refundOnly, "refund", wantRefundAt)

	notifyFallback := copyValues(refundValues)
	delete(notifyFallback, "gmt_refund")
	notifyFallback.Set("notify_id", "notify-kind-specific-refund-fallback-1")
	verify(notifyFallback, "refund", wantNotifyAt)

	// If neither refund time is usable, preserve receive-time fallback and do
	// not substitute the original payment timestamp.
	receiveFallback := copyValues(refundValues)
	delete(receiveFallback, "gmt_refund")
	delete(receiveFallback, "notify_time")
	receiveFallback.Set("notify_id", "notify-kind-specific-refund-receive-time-1")
	sign(receiveFallback)
	started := time.Now().UTC()
	got, err := provider.VerifyValues(context.Background(), receiveFallback)
	finished := time.Now().UTC()
	if err != nil || got.Kind != "refund" || got.OccurredAt.Before(started) || got.OccurredAt.After(finished) || got.OccurredAt.Equal(wantPaymentAt) {
		t.Fatalf("refund receive-time fallback got kind=%q occurred_at=%s payment_at=%s err=%v", got.Kind, got.OccurredAt, wantPaymentAt, err)
	}
}

func TestAlipayAcceptanceWebCheckoutAndSignedQueryRefund(t *testing.T) {
	var provider *Alipay
	var signer *alipaysdk.Client
	tamperResponse := false
	missingPaidDate := false
	refundQueryBody := ""
	refundBody := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var field, body string
		switch r.Form.Get("method") {
		case "alipay.trade.query":
			field, body = "alipay_trade_query_response", `{"code":"10000","out_trade_no":"merchant-test-1","trade_no":"trade-test-1","trade_status":"TRADE_SUCCESS","total_amount":"9.90","send_pay_date":"2026-09-26 16:13:50"}`
		case "alipay.trade.refund":
			field, body = "alipay_trade_refund_response", `{"code":"10000","out_trade_no":"merchant-test-1","trade_no":"trade-test-1","refund_fee":"1.20","fund_change":"Y"}`
		case "alipay.trade.fastpay.refund.query":
			var query map[string]string
			if json.Unmarshal([]byte(r.Form.Get("biz_content")), &query) != nil || query["out_trade_no"] != "merchant-test-1" || query["out_request_no"] != "refund-test-1" {
				t.Error("refund query omitted original order/refund keys")
			}
			field, body = "alipay_trade_fastpay_refund_query_response", `{"code":"10000","out_request_no":"refund-test-1","out_trade_no":"merchant-test-1","trade_no":"trade-test-1","total_amount":"9.90","refund_amount":"1.20","refund_status":"REFUND_SUCCESS"}`
		default:
			t.Errorf("unexpected method %q", r.Form.Get("method"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Form.Get("method") == "alipay.trade.fastpay.refund.query" && refundQueryBody != "" {
			body = refundQueryBody
		}
		if r.Form.Get("method") == "alipay.trade.refund" && refundBody != "" {
			body = refundBody
		}
		if missingPaidDate {
			body = strings.Replace(body, `,"send_pay_date":"2026-09-26 16:13:50"`, "", 1)
		}
		signature, err := signer.SignBytes([]byte(body))
		if err != nil {
			t.Errorf("sign fixture response: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if tamperResponse {
			body = strings.Replace(body, "9.90", "99.90", 1)
			body = strings.Replace(body, "1.20", "1.21", 1)
		}
		_, _ = fmt.Fprintf(w, "{\"%s\":%s,\"sign\":%q}", field, body, base64.StdEncoding.EncodeToString(signature))
	}))
	defer server.Close()
	provider, signer = alipayContractFixture(t, server.URL)
	request := WebPayRequest{MerchantOrderNo: "merchant-test-1", Subject: "课程 A&B=+%中文/订单", TotalAmount: "9.90"}
	for name, build := range map[string]func(context.Context, WebPayRequest) (string, error){"alipay.trade.wap.pay": provider.BuildWapPay, "alipay.trade.page.pay": provider.BuildPagePay} {
		checkoutURL, err := build(context.Background(), request)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		parsed, err := url.Parse(checkoutURL)
		var content struct {
			Subject string `json:"subject"`
		}
		decodeErr := json.Unmarshal([]byte(parsed.Query().Get("biz_content")), &content)
		if err != nil || decodeErr != nil || content.Subject != request.Subject || parsed.Query().Get("method") != name || parsed.Query().Get("sign") == "" || parsed.Query().Get("notify_url") != "https://example.test/api/public/alipay/callback" || parsed.Query().Get("return_url") != "https://example.test/pay/alipay/return" {
			t.Fatalf("%s checkout artifact invalid: err=%v", name, err)
		}
	}
	query, err := provider.QueryPayment(context.Background(), "merchant-test-1")
	if err != nil || query.TradeStatus != "TRADE_SUCCESS" || query.AmountMinor != 990 || query.TradeNo != "trade-test-1" || !query.OccurredAt.Equal(time.Date(2026, 9, 26, 8, 13, 50, 0, time.UTC)) {
		t.Fatalf("signed payment query: status=%q amount=%d err=%v", query.TradeStatus, query.AmountMinor, err)
	}
	refund, err := provider.Refund(context.Background(), RefundRequest{MerchantOrderNo: "merchant-test-1", RefundRequestNo: "refund-test-1", RefundAmount: "1.20"})
	if err != nil || !refund.FundChanged || refund.RefundAmount != "1.20" {
		t.Fatalf("signed refund: changed=%t amount=%q err=%v", refund.FundChanged, refund.RefundAmount, err)
	}
	refundQuery, err := provider.QueryRefund(context.Background(), "merchant-test-1", "refund-test-1")
	if err != nil || refundQuery.Status != "REFUND_SUCCESS" || refundQuery.AmountMinor != 120 || refundQuery.TotalMinor != 990 {
		t.Fatalf("signed refund query: status=%q amount=%d total=%d err=%v", refundQuery.Status, refundQuery.AmountMinor, refundQuery.TotalMinor, err)
	}
	for _, body := range []string{
		`{"code":"40004","out_trade_no":"merchant-test-1","out_request_no":"refund-test-1","trade_no":"trade-test-1","total_amount":"9.90","refund_amount":"1.20","refund_status":"REFUND_SUCCESS"}`,
		`{"code":"10000","out_trade_no":"other","out_request_no":"refund-test-1","trade_no":"trade-test-1","total_amount":"9.90","refund_amount":"1.20","refund_status":"REFUND_SUCCESS"}`,
		`{"code":"10000","out_trade_no":"merchant-test-1","out_request_no":"other","trade_no":"trade-test-1","total_amount":"9.90","refund_amount":"1.20","refund_status":"REFUND_SUCCESS"}`,
		`{"code":"10000","out_trade_no":"merchant-test-1","out_request_no":"refund-test-1","total_amount":"9.90","refund_amount":"1.20","refund_status":"REFUND_SUCCESS"}`,
	} {
		refundQueryBody = body
		if _, err := provider.QueryRefund(context.Background(), "merchant-test-1", "refund-test-1"); err == nil {
			t.Fatal("mismatched or incomplete refund result accepted")
		}
	}
	refundQueryBody = `{"code":"10000"}`
	pending, err := provider.QueryRefund(context.Background(), "merchant-test-1", "refund-test-1")
	if err != nil || pending.Status != "" || pending.RefundDigest != "" {
		t.Fatalf("empty successful query must remain pending: %v", err)
	}
	refundQueryBody = ""
	refundBody = `{"code":"10000","out_trade_no":"other","trade_no":"trade-test-1","refund_fee":"1.20","fund_change":"Y"}`
	if _, err := provider.Refund(context.Background(), RefundRequest{MerchantOrderNo: "merchant-test-1", RefundRequestNo: "refund-test-1", RefundAmount: "1.20"}); err == nil {
		t.Fatal("wrong refund order accepted")
	}
	refundBody = `{"code":"10000","out_trade_no":"merchant-test-1","trade_no":"trade-test-1","refund_fee":"9.90","fund_change":"Y"}`
	if _, err := provider.Refund(context.Background(), RefundRequest{MerchantOrderNo: "merchant-test-1", RefundRequestNo: "refund-test-1", RefundAmount: "1.20"}); err == nil {
		t.Fatal("wrong refund amount accepted")
	}
	refundBody = ""
	missingPaidDate = true
	if _, err := provider.QueryPayment(context.Background(), "merchant-test-1"); err == nil {
		t.Fatal("successful query without payment time accepted")
	}
	missingPaidDate = false
	tamperResponse = true
	if _, err := provider.QueryPayment(context.Background(), "merchant-test-1"); err == nil {
		t.Fatal("tampered provider response accepted")
	}
	if _, err := provider.QueryRefund(context.Background(), "merchant-test-1", "refund-test-1"); err == nil {
		t.Fatal("tampered refund query accepted")
	}
	if _, err := provider.Refund(context.Background(), RefundRequest{MerchantOrderNo: "merchant-test-1", RefundRequestNo: "refund-test-1", RefundAmount: "1.20"}); err == nil {
		t.Fatal("tampered refund response accepted")
	}
}
