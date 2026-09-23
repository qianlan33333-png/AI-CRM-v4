package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

// WeChatOAuth is intentionally stricter than the questionnaire adapter: a
// successful Radar exchange MUST contain UnionID. OpenID is never returned as
// a fallback or persisted by Radar.
type WeChatOAuth struct {
	enabled                                 bool
	appID, secret, openPlatformID, callback string
	client                                  *http.Client
	apiBase                                 string
}

func NewWeChatOAuth(enabled bool, appID, secret, openPlatformID, callback string) (*WeChatOAuth, error) {
	if enabled {
		parsed, e := url.ParseRequestURI(callback)
		if e != nil || !valid(appID, 128) || !valid(secret, 4096) || !valid(openPlatformID, 256) || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "/api/public/radar/oauth/callback" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("radar OAuth configuration incomplete")
		}
	}
	return &WeChatOAuth{enabled: enabled, appID: appID, secret: secret, openPlatformID: openPlatformID, callback: callback, client: &http.Client{Timeout: 10 * time.Second}, apiBase: "https://api.weixin.qq.com"}, nil
}
func (p *WeChatOAuth) Enabled() bool { return p != nil && p.enabled }
func (p *WeChatOAuth) AuthorizationURL(state string) string {
	q := url.Values{"appid": {p.appID}, "redirect_uri": {p.callback}, "response_type": {"code"}, "scope": {"snsapi_userinfo"}, "state": {state}}
	return "https://open.weixin.qq.com/connect/oauth2/authorize?" + q.Encode() + "#wechat_redirect"
}
func (p *WeChatOAuth) Exchange(ctx context.Context, code string) (identitydomain.VerifiedFact, error) {
	if !p.Enabled() || !valid(code, 512) {
		return identitydomain.VerifiedFact{}, errors.New("radar OAuth unavailable")
	}
	q := url.Values{"appid": {p.appID}, "secret": {p.secret}, "code": {code}, "grant_type": {"authorization_code"}}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+"/sns/oauth2/access_token?"+q.Encode(), nil)
	if e != nil {
		return identitydomain.VerifiedFact{}, errors.New("radar OAuth unavailable")
	}
	res, e := p.client.Do(req)
	if e != nil {
		return identitydomain.VerifiedFact{}, errors.New("radar OAuth unavailable")
	}
	defer res.Body.Close()
	body, e := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if e != nil || res.StatusCode != http.StatusOK {
		return identitydomain.VerifiedFact{}, errors.New("radar OAuth unavailable")
	}
	var payload struct {
		UnionID    string `json:"unionid"`
		Scope      string `json:"scope"`
		ErrorCode  int    `json:"errcode"`
		IsSnapshot int    `json:"is_snapshotuser"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.ErrorCode != 0 || payload.IsSnapshot != 0 || !valid(payload.UnionID, 512) || !scopeContains(payload.Scope, "snsapi_userinfo") {
		return identitydomain.VerifiedFact{}, errors.New("radar OAuth unionid unavailable")
	}
	return identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:" + p.openPlatformID, Value: payload.UnionID, Source: "wechat.radar.oauth"})
}
func valid(v string, max int) bool {
	return v != "" && len(v) <= max && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\r\n\x00")
}
func scopeContains(raw, want string) bool {
	for _, v := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		if v == want {
			return true
		}
	}
	return false
}
