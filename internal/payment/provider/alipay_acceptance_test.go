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

func TestAlipayAcceptanceWebCheckoutAndSignedQueryRefund(t *testing.T) {
	var provider *Alipay
	var signer *alipaysdk.Client
	tamperResponse := false
	missingPaidDate := false
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
			field, body = "alipay_trade_fastpay_refund_query_response", `{"code":"10000","out_request_no":"refund-test-1","trade_no":"trade-test-1","total_amount":"9.90","refund_amount":"1.20","refund_status":"REFUND_SUCCESS"}`
		default:
			t.Errorf("unexpected method %q", r.Form.Get("method"))
			w.WriteHeader(http.StatusBadRequest)
			return
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
		if err != nil || decodeErr != nil || content.Subject != request.Subject || parsed.Query().Get("method") != name || parsed.Query().Get("sign") == "" || parsed.Query().Get("notify_url") != "https://example.test/api/public/alipay/callback" {
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
	refundQuery, err := provider.QueryRefund(context.Background(), "refund-test-1")
	if err != nil || refundQuery.Status != "REFUND_SUCCESS" || refundQuery.AmountMinor != 120 || refundQuery.TotalMinor != 990 {
		t.Fatalf("signed refund query: status=%q amount=%d total=%d err=%v", refundQuery.Status, refundQuery.AmountMinor, refundQuery.TotalMinor, err)
	}
	missingPaidDate = true
	if _, err := provider.QueryPayment(context.Background(), "merchant-test-1"); err == nil {
		t.Fatal("successful query without payment time accepted")
	}
	missingPaidDate = false
	tamperResponse = true
	if _, err := provider.QueryPayment(context.Background(), "merchant-test-1"); err == nil {
		t.Fatal("tampered provider response accepted")
	}
}
