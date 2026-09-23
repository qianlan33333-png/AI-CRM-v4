package paymenthttp

import (
	"context"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type purchaseAppStub struct {
	appStub
	err   error
	calls int
}

func (a *purchaseAppStub) PurchaseStatus(context.Context, string, string, int64) (paymentport.PurchaseState, error) {
	a.calls++
	return paymentport.PurchaseState{State: "owned", CanPurchase: false, PaidOrderID: 125}, a.err
}
func TestPurchaseStatusUsesTrustedSessionAndReturnsNoIdentity(t *testing.T) {
	for _, name := range []string{"owned", "missing_cookie", "expired_session", "invalid_session", "post"} {
		t.Run(name, func(t *testing.T) {
			a := &purchaseAppStub{}
			h, _ := NewHandler(a, nil, securityStub{}, true)
			method := "GET"
			if name == "post" {
				method = "POST"
			}
			r := httptest.NewRequest(method, "/api/v1/wechat-pay/purchase-status?product_type=standard&product_id=5", nil)
			if name != "missing_cookie" {
				r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000005"})
			}
			if name == "expired_session" || name == "invalid_session" {
				a.err = paymentport.ErrSessionRequired
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 200
			if name == "missing_cookie" || a.err != nil {
				want = 401
			}
			if name == "post" {
				want = 405
			}
			if w.Code != want {
				t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "customer") || strings.Contains(w.Body.String(), "pays_") {
				t.Fatal("response exposed identity/session")
			}
			if name == "owned" && (!strings.Contains(w.Body.String(), `"purchase_state":"owned"`) || w.Header().Get("Cache-Control") != "no-store") {
				t.Fatal(w.Body.String())
			}
		})
	}
}

type checkoutReadyApp struct {
	appStub
	ready bool
}

func (a *checkoutReadyApp) CanCreateCheckout(context.Context, string) (bool, error) {
	return a.ready, nil
}
func TestCheckoutSessionSeparatesReadIdentityFromNewPurchaseReadiness(t *testing.T) {
	for _, ready := range []bool{true, false} {
		a := &checkoutReadyApp{ready: ready}
		h, _ := NewHandler(a, nil, securityStub{}, true)
		r := httptest.NewRequest("GET", "/api/v1/wechat-pay/checkout-session", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000005"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := `"can_create_checkout":false`
		if ready {
			expected = `"can_create_checkout":true`
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), expected) || !strings.Contains(w.Body.String(), "checkout_session_binding") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}

func TestOwnedPurchaseStatusReturnsOriginalCompletionActionWithoutOrderID(t *testing.T) {
	app := &purchaseAppStub{}
	h, _ := NewHandler(app, nil, securityStub{}, true)
	action := &paidPurchaseActionReaderStub{action: productport.PaidPurchaseAction{OrderID: 125, Enabled: true, Mode: productport.PaidPurchaseActionQR, LeadChannelID: 7, LeadQRTitle: "领取资料"}}
	if err := h.SetPaidPurchaseActionReader(action, paidPurchaseLeadQRStub{value: channelport.PublicLeadQRCode{URL: "https://example.test/qr"}}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/v1/wechat-pay/purchase-status?product_type=standard&product_id=5", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000005"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || action.order != 125 || !strings.Contains(w.Body.String(), "https://example.test/qr") || strings.Contains(w.Body.String(), "order_id") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

type currentGuidanceStub struct {
	*paidPurchaseActionReaderStub
	guidanceCalls int
}

func (s *currentGuidanceStub) ReadPaidPurchaseGuidance(_ context.Context, id int64) (productport.PaidPurchaseAction, error) {
	s.guidanceCalls++
	return productport.PaidPurchaseAction{OrderID: id, Enabled: true, Mode: productport.PaidPurchaseActionQR, LeadChannelID: 7}, nil
}
func TestOwnedPurchaseUsesReadOnlyGuidanceFallback(t *testing.T) {
	app := &purchaseAppStub{}
	h, _ := NewHandler(app, nil, securityStub{}, true)
	reader := &currentGuidanceStub{paidPurchaseActionReaderStub: &paidPurchaseActionReaderStub{action: productport.PaidPurchaseAction{OrderID: 125, Mode: productport.PaidPurchaseActionNone}}}
	_ = h.SetPaidPurchaseActionReader(reader, paidPurchaseLeadQRStub{value: channelport.PublicLeadQRCode{URL: "https://example.test/current-qr"}})
	r := httptest.NewRequest("GET", "/api/v1/wechat-pay/purchase-status?product_type=standard&product_id=5", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000005"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || reader.guidanceCalls != 1 || reader.order != 0 || !strings.Contains(w.Body.String(), "current-qr") {
		t.Fatalf("status %d body %s calls %d", w.Code, w.Body.String(), reader.guidanceCalls)
	}
}
