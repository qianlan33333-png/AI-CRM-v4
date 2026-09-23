package provider

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestH5OAuthIdentityRequiresUserinfoUnionID(t *testing.T) {
	for _, tc := range []struct {
		name, token, info string
		ok                bool
	}{
		{"trusted", `{"access_token":"token","openid":"oa-openid","scope":"snsapi_userinfo"}`, `{"openid":"oa-openid","unionid":"union-subject","nickname":"微信昵称","headimgurl":"https://thirdwx.qlogo.cn/avatar"}`, true},
		{"silent", `{"access_token":"token","openid":"oa-openid","scope":"snsapi_base"}`, `{"openid":"oa-openid","unionid":"union-subject"}`, false},
		{"missing_union", `{"access_token":"token","openid":"oa-openid","scope":"snsapi_userinfo"}`, `{"openid":"oa-openid"}`, false},
		{"wrong_openid", `{"access_token":"token","openid":"oa-openid","scope":"snsapi_userinfo"}`, `{"openid":"other","unionid":"union-subject"}`, false},
		{"wrong_union", `{"access_token":"token","openid":"oa-openid","unionid":"other","scope":"snsapi_userinfo"}`, `{"openid":"oa-openid","unionid":"union-subject"}`, false},
		{"error", `{"access_token":"token","openid":"oa-openid","scope":"snsapi_userinfo"}`, `{"errcode":40003}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/sns/oauth2/access_token" {
					if r.URL.Query().Get("code") != "trusted-code" {
						t.Error("missing code")
					}
					_, _ = w.Write([]byte(tc.token))
					return
				}
				if r.URL.Path != "/sns/userinfo" || r.URL.Query().Get("access_token") != "token" || r.URL.Query().Get("openid") != "oa-openid" {
					t.Error("invalid userinfo request")
				}
				_, _ = w.Write([]byte(tc.info))
			}))
			defer server.Close()
			p, err := NewH5OAuthIdentity(true, "wx-oa", "secret", "wechat-app:wx-oa", "https://crm.example.test/api/h5/wechat-pay/oauth/callback", "platform-1")
			if err != nil {
				t.Fatal(err)
			}
			p.apiBase, p.client = server.URL, server.Client()
			facts, err := p.Exchange(context.Background(), "trusted-code")
			if (err == nil) != tc.ok {
				t.Fatalf("success=%v expected=%v", err == nil, tc.ok)
			}
			if tc.ok && (facts.OpenID.Reference().Kind != identitydomain.KindOAOpenID || facts.UnionID.Reference().Scope != "wechat-open-platform:platform-1" || facts.DisplayName != "微信昵称" || facts.AvatarURL != "https://thirdwx.qlogo.cn/avatar" || calls != 2) {
				t.Fatal("missing trusted subject pair")
			}
			if !strings.Contains(p.AuthorizationURL("state"), "scope=snsapi_userinfo") {
				t.Fatal("silent scope")
			}
		})
	}
}
