package paymenthttp

import (
	"encoding/base64"
	paymenth5oauth "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
