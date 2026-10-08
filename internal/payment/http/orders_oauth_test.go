package paymenthttp

import (
	"context"
	"encoding/base64"
	paymenth5oauth "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type successfulOrdersOAuthStub struct {
	completes int
}

func (successfulOrdersOAuthStub) Enabled() bool                                 { return true }
func (successfulOrdersOAuthStub) Start(context.Context, string) (string, error) { return "", nil }
func (stub *successfulOrdersOAuthStub) Complete(context.Context, string, string) (session.Issued, string, error) {
	stub.completes++
	return session.Issued{Token: "pays_h5_session_token_00000001", ExpiresAt: time.Now().Add(time.Minute), Channel: "h5_official_account"}, "/my/orders", nil
}
func (successfulOrdersOAuthStub) RecoverReturnPath(context.Context, string) (string, error) {
	return "/my/orders", nil
}

func TestOrdersOAuthFailuresReturnWithoutAutomaticLoop(t *testing.T) {
	h, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
	request := httptest.NewRequest("GET", "/api/h5/wechat-pay/oauth/start?return_url=%2Fmy%2Forders", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 303 || response.Header().Get("Location") != "/my/orders?oauth_error=unavailable" {
		t.Fatal("disabled entry recovery", response.Code, response.Header().Get("Location"))
	}
	for _, conflict := range []bool{false, true} {
		oauth := &h5OAuthStub{enabled: true, returnPath: "/my/orders"}
		want := "/my/orders?oauth_error=denied"
		if conflict {
			oauth.completeError = paymenth5oauth.ErrIdentityConflict
			want = "/my/orders?oauth_error=conflict"
		}
		h, _ = NewHandler(&appStub{}, nil, securityStub{}, true)
		h.SetH5OAuth(oauth)
		path := "/api/h5/wechat-pay/oauth/callback?state=opaque"
		if conflict {
			path += "&code=opaque"
		}
		request = httptest.NewRequest("GET", path, nil)
		request.AddCookie(&http.Cookie{Name: h5OAuthReturnCookieName, Value: base64.RawURLEncoding.EncodeToString([]byte("/my/orders"))})
		response = httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 303 || response.Header().Get("Location") != want {
			t.Fatal("denial/conflict", response.Code, response.Header().Get("Location"))
		}
	}
}

func TestOrdersOAuthSuccessfulCallbackReturnsToFixedOrdersPath(t *testing.T) {
	h, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
	oauth := &successfulOrdersOAuthStub{}
	if err := h.SetH5OAuth(oauth); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?state=opaque&code=synthetic", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/my/orders" || oauth.completes != 1 {
		t.Fatalf("orders OAuth success status=%d fixed_return=%t completion_count=%d", response.Code, response.Header().Get("Location") == "/my/orders", oauth.completes)
	}
	var sessionCookie, returnCookieCleared bool
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == SessionCookieName && cookie.Value == "pays_h5_session_token_00000001" {
			sessionCookie = true
		}
		if cookie.Name == h5OAuthReturnCookieName && cookie.MaxAge == -1 {
			returnCookieCleared = true
		}
	}
	if !sessionCookie || !returnCookieCleared {
		t.Fatalf("orders OAuth callback did not issue the trusted session and clear the temporary return cookie")
	}
}
