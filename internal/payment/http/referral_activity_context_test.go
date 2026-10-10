package paymenthttp

import (
	"crypto/sha256"
	"encoding/base64"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReferralActivityContextCookieRequiresOpaqueTokenShape(t *testing.T) {
	digest := sha256.Sum256([]byte("activity"))
	for _, prefix := range []string{"rpa_", "rfi_"} {
		token := prefix + base64.RawURLEncoding.EncodeToString(digest[:])
		if !validReferralActivityContext(token) {
			t.Fatalf("server-issued %s token should be accepted", prefix)
		}
	}
	for _, invalid := range []string{"", "campaign:3", "dpc_" + base64.RawURLEncoding.EncodeToString(digest[:]), "rfi_" + base64.RawURLEncoding.EncodeToString([]byte("short")), "rpa_" + base64.RawURLEncoding.EncodeToString([]byte("short")), "rfi_" + strings.Repeat("!", 43)} {
		if validReferralActivityContext(invalid) {
			t.Fatalf("invalid activity context accepted: %q", invalid)
		}
	}
}

func TestCheckoutForwardsOpaqueReferralInvitationOnlyFromCookie(t *testing.T) {
	digest := sha256.Sum256([]byte("paid-invitation"))
	invitation := "rfi_" + base64.RawURLEncoding.EncodeToString(digest[:])
	for _, provider := range []string{"wechat-pay", "alipay"} {
		t.Run(provider, func(t *testing.T) {
			app := &appStub{}
			handler, _ := NewHandler(app, nil, securityStub{}, true, true, true)
			session := "pays_session_token_0000000001"
			binding := paymentport.CheckoutSessionBinding(session)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/"+provider+"/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "referral-cookie-forwarding")
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session})
			request.AddCookie(&http.Cookie{Name: "aicrm_referral_activity_context", Value: invitation})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusAccepted || app.createCalls != 1 || app.create.ReferralActivityContext != invitation {
				t.Fatalf("status=%d forwarded=%t body=%s", response.Code, app.create.ReferralActivityContext == invitation, response.Body.String())
			}
			forged := httptest.NewRequest(http.MethodPost, "/api/v1/"+provider+"/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`","referral_activity_context":"`+invitation+`"}`))
			forged.Header = request.Header.Clone()
			forged.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session})
			rejected := httptest.NewRecorder()
			handler.ServeHTTP(rejected, forged)
			if rejected.Code != http.StatusBadRequest || app.createCalls != 1 {
				t.Fatal("body-created invitation authority accepted")
			}
		})
	}
}
