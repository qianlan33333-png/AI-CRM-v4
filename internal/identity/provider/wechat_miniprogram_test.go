package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type miniProgramDoer func(*http.Request) (*http.Response, error)

func (do miniProgramDoer) Do(request *http.Request) (*http.Response, error) { return do(request) }

func TestWeChatMiniProgramCodeProducesScopedVerifiedOpenID(t *testing.T) {
	calls := 0
	provider, err := NewWeChatMiniProgram(WeChatMiniProgramConfig{AppID: "wx-app", AppSecret: "app-secret", APIBaseURL: "https://api.weixin.qq.com"}, miniProgramDoer(func(request *http.Request) (*http.Response, error) {
		calls++
		query := request.URL.Query()
		if request.Method != http.MethodGet || request.URL.Path != "/sns/jscode2session" || query.Get("appid") != "wx-app" || query.Get("secret") != "app-secret" || query.Get("js_code") != "one-time-code" || query.Get("grant_type") != "authorization_code" {
			t.Fatalf("request=%s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"openid":"openid-1","session_key":"not-retained"}`)), Header: make(http.Header)}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	fact, err := provider.VerifyCode(context.Background(), "one-time-code")
	reference := fact.Reference()
	if err != nil || calls != 1 || !fact.Valid() || reference.Kind != identitydomain.KindMPOpenID || reference.Scope != "wechat-app:wx-app" || reference.NormalizedValue != "openid-1" || reference.Assurance != identitydomain.AssuranceVerified {
		t.Fatalf("reference=%+v calls=%d err=%v", reference, calls, err)
	}
}

func TestWeChatMiniProgramRejectsProviderErrorWithoutVerifiedFact(t *testing.T) {
	provider, err := NewWeChatMiniProgram(WeChatMiniProgramConfig{AppID: "wx-app", AppSecret: "app-secret", APIBaseURL: "https://api.weixin.qq.com"}, miniProgramDoer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errcode":40029,"errmsg":"invalid code"}`)), Header: make(http.Header)}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if fact, verifyErr := provider.VerifyCode(context.Background(), "bad-code"); verifyErr == nil || fact.Valid() {
		t.Fatalf("fact=%+v err=%v", fact.Reference(), verifyErr)
	}
}
