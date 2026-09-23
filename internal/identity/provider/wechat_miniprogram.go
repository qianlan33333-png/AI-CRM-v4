package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

var ErrWeChatMiniProgramVerification = errors.New("wechat mini program identity verification failed")

type MiniProgramHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type WeChatMiniProgramConfig struct {
	AppID, AppSecret string
	APIBaseURL       string
}

func (WeChatMiniProgramConfig) String() string {
	return "WeChatMiniProgramConfig{credentials:[REDACTED]}"
}
func (WeChatMiniProgramConfig) GoString() string {
	return "WeChatMiniProgramConfig{credentials:[REDACTED]}"
}

type WeChatMiniProgram struct {
	appID, secret string
	base          *url.URL
	client        MiniProgramHTTPDoer
}

func NewWeChatMiniProgram(config WeChatMiniProgramConfig, client MiniProgramHTTPDoer) (*WeChatMiniProgram, error) {
	base, err := url.Parse(config.APIBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.Path != "" || !validMiniProgramValue(config.AppID, 128) || !validMiniProgramValue(config.AppSecret, 4096) || client == nil {
		return nil, ErrWeChatMiniProgramVerification
	}
	return &WeChatMiniProgram{appID: config.AppID, secret: config.AppSecret, base: base, client: client}, nil
}

func (*WeChatMiniProgram) String() string   { return "wechat-mini-program-verifier[redacted]" }
func (*WeChatMiniProgram) GoString() string { return "wechat-mini-program-verifier[redacted]" }

func (provider *WeChatMiniProgram) VerifyCode(ctx context.Context, code string) (identitydomain.VerifiedFact, error) {
	if provider == nil || provider.base == nil || provider.client == nil || !validMiniProgramValue(code, 256) {
		return identitydomain.VerifiedFact{}, ErrWeChatMiniProgramVerification
	}
	endpoint := *provider.base
	endpoint.Path = "/sns/jscode2session"
	query := endpoint.Query()
	query.Set("appid", provider.appID)
	query.Set("secret", provider.secret)
	query.Set("js_code", code)
	query.Set("grant_type", "authorization_code")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return identitydomain.VerifiedFact{}, ErrWeChatMiniProgramVerification
	}
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return identitydomain.VerifiedFact{}, ErrWeChatMiniProgramVerification
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return identitydomain.VerifiedFact{}, ErrWeChatMiniProgramVerification
	}
	var result struct {
		OpenID  string `json:"openid"`
		ErrCode int64  `json:"errcode"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	var extra any
	if decoder.Decode(&result) != nil || decoder.Decode(&extra) != io.EOF || result.ErrCode != 0 || !validMiniProgramValue(result.OpenID, 1024) {
		return identitydomain.VerifiedFact{}, ErrWeChatMiniProgramVerification
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:" + provider.appID, Value: result.OpenID, Source: "wechat_miniprogram"})
	if err != nil {
		return identitydomain.VerifiedFact{}, ErrWeChatMiniProgramVerification
	}
	return fact, nil
}

func validMiniProgramValue(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}
