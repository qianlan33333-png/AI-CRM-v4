package paymenthttp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	paymenth5oauth "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
)

func TestPeriodDetailOAuthStartFailuresReturnToRetryableDetail(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		handler, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
		if !disabled {
			oauth := &h5OAuthStub{enabled: true}
			if err := handler.SetH5OAuth(h5OAuthStartErrorStub{oauth: oauth, err: paymenth5oauth.ErrUnavailable}); err != nil {
				t.Fatal(err)
			}
		}
		path := "/s/term-31?promotion_context=dpc_" + strings.Repeat("a", 43)
		request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape(path), nil)
		request.Header.Set("User-Agent", "MicroMessenger")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != path {
			t.Fatalf("disabled=%t status=%d location=%q", disabled, response.Code, response.Header().Get("Location"))
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("retry redirect must not cache")
		}
	}
}

func TestPeriodOAuthRetryDoesNotBroadenReturnOrPaymentContract(t *testing.T) {
	for _, path := range []string{"/pay/course-7", "/s/term-31/pay", "https://evil.test/s/term-31", "/s/term-31?unexpected=1", "/s/a%2Fb"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start", nil)
		request.Header.Set("User-Agent", "MicroMessenger")
		if retryPeriodDetailOAuth(response, request, path) {
			t.Fatalf("broadened retry return %q", path)
		}
	}
}
