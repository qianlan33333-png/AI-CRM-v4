package paymenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestCheckoutStatusAlwaysReturnsFrozenPaymentAmount(t *testing.T) {
	for _, status := range []domain.Status{domain.StatusAwaitingPrepay, domain.StatusAwaitingPayment, domain.StatusPaid} {
		t.Run(string(status), func(t *testing.T) {
			app := &appStub{handoff: paymentport.Handoff{PaymentID: 7, MerchantOrder: "M-7", Status: status, AmountMinor: 789, Currency: "CNY"}}
			h, _ := NewHandler(app, nil, securityStub{}, true)
			r := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-7", nil)
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var response struct {
				AmountMinor int64  `json:"amount_minor"`
				Currency    string `json:"currency"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.AmountMinor != 789 || response.Currency != "CNY" || (w.Code != 200 && w.Code != 202) {
				t.Fatalf("status=%d response=%+v err=%v", w.Code, response, err)
			}
		})
	}
}
