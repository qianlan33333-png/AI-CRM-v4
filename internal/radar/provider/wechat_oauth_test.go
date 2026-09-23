package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

func TestExchangeRequiresUnionIDAndReturnsOnlyScopedUnionIDFact(t *testing.T) {
	tests := []struct {
		name, body string
		ok         bool
	}{
		{name: "silent scope rejected", body: `{"unionid":"union-user","scope":"snsapi_base"}`},
		{name: "snapshot rejected", body: `{"unionid":"union-user","scope":"snsapi_userinfo","is_snapshotuser":1}`},
		{name: "missing unionid", body: `{"openid":"oa-user","scope":"snsapi_userinfo"}`},
		{name: "scoped unionid", body: `{"openid":"oa-user","unionid":"union-user","scope":"snsapi_userinfo"}`, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			provider, err := NewWeChatOAuth(true, "app", "secret", "platform", "https://crm.example/api/public/radar/oauth/callback")
			if err != nil {
				t.Fatal(err)
			}
			provider.apiBase = server.URL
			provider.client = server.Client()
			fact, err := provider.Exchange(context.Background(), "provider-code")
			if !test.ok {
				if err == nil {
					t.Fatal("missing unionid must fail closed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reference := fact.Reference()
			if reference.Kind != identitydomain.KindUnionID || reference.Scope != "wechat-open-platform:platform" || reference.NormalizedValue != "union-user" || reference.Assurance != identitydomain.AssuranceVerified {
				t.Fatalf("unexpected reference: %+v", reference)
			}
		})
	}
}

func TestConfigurationRejectsNonRadarCallback(t *testing.T) {
	if _, err := NewWeChatOAuth(true, "app", "secret", "platform", "https://crm.example/api/h5/surveys/oauth/callback"); err == nil {
		t.Fatal("survey callback must not be accepted for Radar")
	}
}

func TestAuthorizationURLRequestsUserInfo(t *testing.T) {
	p, err := NewWeChatOAuth(true, "app", "secret", "platform", "https://crm.example/api/public/radar/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(p.AuthorizationURL("opaque-state"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("scope") != "snsapi_userinfo" || u.Query().Get("state") != "opaque-state" {
		t.Fatal("userinfo authorization scope missing")
	}
}
