package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

func TestAuthorizationURLUsesInteractiveScopeAndExactCallback(t *testing.T) {
	provider, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://id-dev.youcangogogo.com/api/h5/surveys/oauth/callback", "snsapi_userinfo")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(provider.AuthorizationURL("state-token"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "open.weixin.qq.com" || parsed.Query().Get("scope") != "snsapi_userinfo" || parsed.Query().Get("redirect_uri") != "https://id-dev.youcangogogo.com/api/h5/surveys/oauth/callback" || parsed.Query().Get("state") != "state-token" {
		t.Fatalf("authorization URL=%s", parsed.Redacted())
	}
}

func TestExchangeRequiresUserinfoUnionID(t *testing.T) {
	for _, tc := range []struct {
		name, token, info string
		ok                bool
	}{
		{"token_and_userinfo", `{"access_token":"token","openid":"oa-open","unionid":"union-one","scope":"snsapi_userinfo"}`, `{"openid":"oa-open","unionid":"union-one"}`, true},
		{"userinfo_only", `{"access_token":"token","openid":"oa-open","scope":"snsapi_userinfo"}`, `{"openid":"oa-open","unionid":"union-one"}`, true},
		{"missing_union", `{"access_token":"token","openid":"oa-open","scope":"snsapi_userinfo"}`, `{"openid":"oa-open"}`, false},
		{"mismatched_openid", `{"access_token":"token","openid":"oa-open","scope":"snsapi_userinfo"}`, `{"openid":"other","unionid":"union-one"}`, false},
		{"mismatched_union", `{"access_token":"token","openid":"oa-open","unionid":"other","scope":"snsapi_userinfo"}`, `{"openid":"oa-open","unionid":"union-one"}`, false},
		{"provider_error", `{"access_token":"token","openid":"oa-open","scope":"snsapi_userinfo"}`, `{"errcode":40003}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/sns/oauth2/access_token" {
					_, _ = w.Write([]byte(tc.token))
					return
				}
				if r.URL.Path != "/sns/userinfo" || r.URL.Query().Get("access_token") != "token" || r.URL.Query().Get("openid") != "oa-open" {
					t.Error("invalid userinfo request")
				}
				_, _ = w.Write([]byte(tc.info))
			}))
			defer server.Close()
			p, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://example.test/api/h5/surveys/oauth/callback", "snsapi_userinfo")
			if err != nil {
				t.Fatal(err)
			}
			p.apiBase, p.client = server.URL, server.Client()
			fact, err := p.Exchange(context.Background(), "provider-code")
			if (err == nil) != tc.ok {
				t.Fatalf("success=%v expected=%v", err == nil, tc.ok)
			}
			if tc.ok && (fact.Reference().Kind != identitydomain.KindUnionID || fact.Reference().Scope != "wechat-open-platform:platform" || calls != 2) {
				t.Fatal("missing verified UnionID")
			}
		})
	}
}

func TestExchangeRejectsWrongScopeProviderErrorsAndSnapshotIdentity(t *testing.T) {
	for _, payload := range []string{
		`{"openid":"oa-open","scope":"snsapi_base"}`,
		`{"errcode":40029,"errmsg":"invalid code"}`,
		`{"openid":"virtual-open","unionid":"virtual-union","scope":"snsapi_userinfo","is_snapshotuser":1}`,
		`{"openid":"snapshot-user","scope":"snsapi_userinfo"}`,
		`{"openid":" oa-open ","scope":"snsapi_userinfo"}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload)) }))
		provider, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://example.test/api/h5/surveys/oauth/callback", "snsapi_userinfo")
		if err != nil {
			t.Fatal(err)
		}
		provider.apiBase, provider.client = server.URL, server.Client()
		if _, err = provider.Exchange(context.Background(), "provider-code"); err == nil {
			server.Close()
			t.Fatalf("accepted payload shape %q", payload)
		}
		server.Close()
	}
}

func TestEnabledOAuthRequiresOpenPlatformScopeAndInteractiveScope(t *testing.T) {
	callback := "https://example.test/api/h5/surveys/oauth/callback"
	if _, err := NewWeChatOAuth(true, "wx-app", "secret", "", callback, "snsapi_userinfo"); err == nil {
		t.Fatal("missing open platform scope accepted")
	}
	if _, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", callback, "snsapi_base"); err == nil {
		t.Fatal("non-interactive OAuth scope accepted")
	}
	if _, err := NewWeChatOAuth(true, "wx-app", "secret\n", "platform", callback, "snsapi_userinfo"); err == nil {
		t.Fatal("unsafe OAuth secret accepted")
	}
	if _, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://example.test/other", "snsapi_userinfo"); err == nil {
		t.Fatal("unexpected OAuth callback accepted")
	}
}
