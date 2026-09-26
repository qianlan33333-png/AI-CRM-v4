package paymenthttp

import (
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlipayRefundUsesOwnProviderSwitchAndVerifiedTransaction(t *testing.T) {
	for _, test := range []struct {
		name, confirmation, provider, key string
		enabled                           bool
		want                              int
	}{
		{"accepted", "ALI-TRADE-9", "alipay", "alipay-refund-test-key", true, 202},
		{"merchant_not_transaction", "M-9", "alipay", "alipay-refund-test-key", true, 400},
		{"wrong_provider", "ALI-TRADE-9", "wechat", "alipay-refund-test-key", true, 400},
		{"missing_key", "ALI-TRADE-9", "alipay", "", true, 400},
		{"disabled", "ALI-TRADE-9", "alipay", "alipay-refund-test-key", false, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := &appStub{payment: domain.Payment{ID: 9, Provider: domain.ProviderAlipay, MerchantOrderNo: "M-9", Status: domain.StatusPaid, ProviderTransactionDigest: string(effectport.Hash("alipay.transaction", "ALI-TRADE-9"))}}
			handler, _ := NewHandler(application, nil, securityStub{}, false, false, test.enabled)
			request := httptest.NewRequest(http.MethodPost, "/api/admin/alipay/orders/M-9/refunds", strings.NewReader(`{"provider":"`+test.provider+`","order_no":"M-9","refund_amount_total":120,"reason":"客户申请","transaction_id_confirmation":"`+test.confirmation+`","checked":true}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", test.key)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.want == 202 {
				if application.refundCalls != 1 || application.refund.PaymentID != 9 || application.refund.AmountMinor != 120 {
					t.Fatal("wrong refund command")
				}
			} else if application.refundCalls != 0 {
				t.Fatal("invalid request reached refund service")
			}
		})
	}
}

func TestAlipayRefundScopedReadsAndRecovery(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, false, false, true)
	for _, path := range []string{"/api/admin/refunds?provider=alipay&order_no=M-9", "/api/admin/refunds/recovery?provider=alipay&order_no=M-9"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Idempotency-Key", "alipay-refund-test-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
	if application.listProvider != domain.ProviderAlipay || application.listMerchant != "M-9" || application.recoveryProvider != domain.ProviderAlipay || application.recoveryMerchant != "M-9" || application.listAllCalls != 0 {
		t.Fatal("read escaped original provider/order scope")
	}
}

func TestAlipayRefundRequiresAdministrativeWriteRole(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, recoverySecurityStub{}, false, false, true)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/alipay/orders/M-9/refunds", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || application.refundCalls != 0 {
		t.Fatal("unprivileged actor reached refund service")
	}
}
