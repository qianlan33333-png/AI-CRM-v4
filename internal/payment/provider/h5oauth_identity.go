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
	"unicode/utf8"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

var ErrH5OAuthIdentity = errors.New("wechat H5 OAuth identity verification failed")

type H5OAuthIdentity struct {
	enabled                                 bool
	appID, secret, appScope, openPlatformID string
	callbackURL, apiBase                    string
	client                                  *http.Client
}

func NewH5OAuthIdentity(enabled bool, appID, secret, appScope, callbackURL, openPlatformID string) (*H5OAuthIdentity, error) {
	if enabled {
		parsed, err := url.ParseRequestURI(callbackURL)
		if !safeH5OAuthValue(openPlatformID, 256) || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "/api/h5/wechat-pay/oauth/callback" || parsed.RawQuery != "" || parsed.Fragment != "" || !safeH5OAuthValue(appID, 128) || !safeH5OAuthValue(secret, 4096) || appScope != "wechat-app:"+appID {
			return nil, ErrH5OAuthIdentity
		}
	}
	return &H5OAuthIdentity{enabled: enabled, openPlatformID: openPlatformID, appID: appID, secret: secret, appScope: appScope, callbackURL: callbackURL, apiBase: "https://api.weixin.qq.com", client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (p *H5OAuthIdentity) Enabled() bool { return p != nil && p.enabled }

func (p *H5OAuthIdentity) AuthorizationURL(state string) string {
	values := url.Values{"appid": {p.appID}, "redirect_uri": {p.callbackURL}, "response_type": {"code"}, "scope": {"snsapi_userinfo"}, "state": {state}}
	return "https://open.weixin.qq.com/connect/oauth2/authorize?" + values.Encode() + "#wechat_redirect"
}

func (p *H5OAuthIdentity) Exchange(ctx context.Context, code string) (paymentport.H5OAuthFacts, error) {
	if !p.Enabled() || !safeH5OAuthValue(code, 512) {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	values := url.Values{"appid": {p.appID}, "secret": {p.secret}, "code": {code}, "grant_type": {"authorization_code"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+"/sns/oauth2/access_token?"+values.Encode(), nil)
	if err != nil {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	response, err := p.client.Do(request)
	if err != nil {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var payload struct {
		OpenID      string `json:"openid"`
		AccessToken string `json:"access_token"`
		UnionID     string `json:"unionid"`
		ErrorCode   int    `json:"errcode"`
		Scope       string `json:"scope"`
	}
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(body, &payload) != nil || payload.ErrorCode != 0 || !safeH5OAuthValue(payload.OpenID, 512) || !h5ScopeContains(payload.Scope, "snsapi_userinfo") || !safeH5OAuthValue(payload.AccessToken, 4096) {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	values = url.Values{"access_token": {payload.AccessToken}, "openid": {payload.OpenID}, "lang": {"zh_CN"}}
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+"/sns/userinfo?"+values.Encode(), nil)
	if err != nil {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	response, err = p.client.Do(request)
	if err != nil {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	defer response.Body.Close()
	body, err = io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var info struct {
		OpenID    string `json:"openid"`
		UnionID   string `json:"unionid"`
		Nickname  string `json:"nickname"`
		AvatarURL string `json:"headimgurl"`
		ErrorCode int    `json:"errcode"`
		Snapshot  int    `json:"is_snapshotuser"`
	}
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(body, &info) != nil || info.ErrorCode != 0 || info.Snapshot != 0 || info.OpenID != payload.OpenID || !safeH5OAuthValue(info.UnionID, 512) || payload.UnionID != "" && payload.UnionID != info.UnionID {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: p.appScope, Value: info.OpenID, Source: "wechat.payment.h5_oauth.userinfo"})
	if err != nil {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	union, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:" + p.openPlatformID, Value: info.UnionID, Source: "wechat.payment.h5_oauth.userinfo"})
	if err != nil {
		return paymentport.H5OAuthFacts{}, ErrH5OAuthIdentity
	}
	return paymentport.H5OAuthFacts{OpenID: fact, UnionID: union, DisplayName: safeH5ProfileText(info.Nickname), AvatarURL: safeH5AvatarURL(info.AvatarURL)}, nil
}

func safeH5ProfileText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func safeH5AvatarURL(value string) string {
	if value == "" || len(value) > 2048 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}
func h5ScopeContains(raw, want string) bool {
	for _, scope := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		if scope == want {
			return true
		}
	}
	return false
}

func safeH5OAuthValue(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}
