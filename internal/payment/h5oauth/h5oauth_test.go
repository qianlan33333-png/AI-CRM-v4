package h5oauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
)

type uowStub struct{}

func (uowStub) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type stateStoreStub struct {
	states map[[32]byte]State
	all    map[[32]byte]State
}

func (s *stateStoreStub) Create(_ context.Context, digest [32]byte, state State, _ time.Time) error {
	if s.states == nil {
		s.states = map[[32]byte]State{}
	}
	s.states[digest] = state
	if s.all == nil {
		s.all = map[[32]byte]State{}
	}
	s.all[digest] = state
	return nil
}
func (s *stateStoreStub) ReturnPath(_ context.Context, digest [32]byte) (string, error) {
	state, ok := s.all[digest]
	if !ok {
		return "", ErrInvalid
	}
	return state.ReturnPath, nil
}
func (s *stateStoreStub) Consume(_ context.Context, digest [32]byte, _ time.Time) (State, error) {
	state, ok := s.states[digest]
	if !ok {
		return State{}, ErrInvalid
	}
	delete(s.states, digest)
	return state, nil
}

type issuerStub struct{ command paymentsession.IssueCommand }

func (i *issuerStub) IssueTrusted(_ context.Context, command paymentsession.IssueCommand) (paymentsession.Issued, error) {
	i.command = command
	return paymentsession.Issued{Token: "pays_h5_session_token_00000001", ExpiresAt: time.Now().Add(time.Minute), Channel: paymentdomain.ChannelH5Official}, nil
}

type providerStub struct{ fact identitydomain.VerifiedFact }

func (providerStub) Enabled() bool { return true }
func (providerStub) AuthorizationURL(state string) string {
	return "https://open.weixin.qq.com/connect/oauth2/authorize?state=" + url.QueryEscape(state)
}
func (stub providerStub) Exchange(context.Context, string) (paymentport.H5OAuthFacts, error) {
	union, _ := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:test", Value: "union-test", Source: "test.provider"})
	return paymentport.H5OAuthFacts{OpenID: stub.fact, UnionID: union, DisplayName: "微信昵称", AvatarURL: "https://thirdwx.qlogo.cn/avatar"}, nil
}

func TestH5OAuthStateIsBoundExpiresAndCannotReplay(t *testing.T) {
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-oa", Value: "oa-openid", Source: "test.provider"})
	if err != nil {
		t.Fatal(err)
	}
	store, issuer := &stateStoreStub{}, &issuerStub{}
	service, err := NewService(uowStub{}, store, providerStub{fact: fact}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC) }
	if _, err = service.Start(context.Background(), "https://evil.example/pay/course-7"); err == nil {
		t.Fatal("accepted open redirect")
	}
	authorization, err := service.Start(context.Background(), "/pay/course-7")
	if err != nil || !strings.HasPrefix(authorization, "https://open.weixin.qq.com/") {
		t.Fatalf("authorization=%q err=%v", authorization, err)
	}
	parsed, _ := url.Parse(authorization)
	state := parsed.Query().Get("state")
	issued, redirect, err := service.Complete(context.Background(), state, "trusted-code")
	if err != nil || redirect != "/pay/course-7" || issued.Channel != paymentdomain.ChannelH5Official || issuer.command.Fact.Reference().Kind != identitydomain.KindOAOpenID || issuer.command.Fact.Reference().Scope != "wechat-app:wx-oa" || issuer.command.DisplayName != "微信昵称" || issuer.command.AvatarURL != "https://thirdwx.qlogo.cn/avatar" {
		t.Fatalf("issued=%+v redirect=%q command=%+v err=%v", issued, redirect, issuer.command, err)
	}
	if recovered, recoverErr := service.RecoverReturnPath(context.Background(), state); recoverErr != nil || recovered != "/pay/course-7" {
		t.Fatalf("consumed state recovery path=%q err=%v", recovered, recoverErr)
	}
	if _, recoverErr := service.RecoverReturnPath(context.Background(), "unknown-state"); recoverErr == nil {
		t.Fatal("unknown state returned a path")
	}
	if _, _, err = service.Complete(context.Background(), state, "trusted-code"); err == nil {
		t.Fatal("OAuth state replay succeeded")
	}
	legacyAuthorization, err := service.Start(context.Background(), "/pay/7")
	if err != nil {
		t.Fatalf("legacy numeric payment path err=%v", err)
	}
	legacyURL, _ := url.Parse(legacyAuthorization)
	legacyState := legacyURL.Query().Get("state")
	_, legacyRedirect, err := service.Complete(context.Background(), legacyState, "trusted-code")
	if err != nil || legacyRedirect != "/pay/7" {
		t.Fatalf("legacy redirect=%q err=%v", legacyRedirect, err)
	}
	promotion := "dpc_" + strings.Repeat("A", 43)
	referralToken := "rfi_" + strings.Repeat("A", 43)
	for _, path := range []string{"/distribution", "/distribution?product_id=7&product_type=standard_product", "/distribution?product_id=8&product_type=service_period", "/referral", "/referral?campaign=7", "/referral?campaign=7&invite=" + referralToken, "/p/subscription_trial_month", "/p/course-7", "/p/7", "/s/term-31", "/s/term-31/pay", "/c/coupon-2026", "/p/course-7?promotion_context=" + promotion, "/pay/course-7?promotion_context=" + promotion, "/s/term-31?promotion_context=" + promotion, "/s/term-31/pay?promotion_context=" + promotion} {
		authorization, startErr := service.Start(context.Background(), path)
		if startErr != nil {
			t.Fatalf("path=%q err=%v", path, startErr)
		}
		parsed, _ := url.Parse(authorization)
		_, redirect, completeErr := service.Complete(context.Background(), parsed.Query().Get("state"), "trusted-code")
		if completeErr != nil || redirect != path {
			t.Fatalf("path=%q redirect=%q err=%v", path, redirect, completeErr)
		}
	}
	for _, path := range []string{"/distribution/", "/distribution?next=/pay/course", "/distribution?product_id=7", "/distribution?product_type=standard_product", "/distribution?product_id=0&product_type=standard_product", "/distribution?product_id=9223372036854775808&product_type=standard_product", "/distribution?product_id=7&product_type=unknown", "/distribution?product_type=standard_product&product_id=7", "/distribution?product_id=7&product_type=standard_product&next=/pay/course", "/distribution?product_id=7&product_id=8&product_type=standard_product", "/distribution?product_id=7&product_type=standard%5fproduct", "/distribution?product_id=7&product_type=standard_product#fragment", "/distribution%2fadmin", "/referral/", "/referral?invite=" + referralToken, "/referral?campaign=0", "/referral?campaign=0&invite=" + referralToken, "/referral?campaign=7&invite=rfi_short", "/referral?invite=" + referralToken + "&campaign=7", "/referral?campaign=7&invite=" + referralToken + "&next=/admin", "/referral?campaign=7&invite=" + referralToken + "#fragment", "/referral%2fadmin", "/p/", "/p/a/b", "/p/a?x=1", "/p/a#fragment", "/p/a%2fb", "/p/a%5cb", "/p/a%3fb", "/p/a%23b", "//evil.example/p/course-7", "https://evil.example/p/course-7", "https://evil.example/pay/course-7", "//evil.example/pay/course-7", "/pay/", "/pay/course/7", "/pay/course?x=1", "/pay/%", "/pay/course%2F7", "/pay/course%5C7", "/s/", "/s/course/other", "/s/course/pay/other", "/s/course%2Fother", "/s/course%23fragment", "/c/short", "/c/CAPITAL-2026", "/c/coupon%2F2026", "/p/course-7?promotion_context=dpc_short", "/p/course-7?promotion_context=" + promotion + "&next=/pay/course", "/p/course-7?next=/pay/course&promotion_context=" + promotion, "/p/course-7?promotion_context=" + promotion + "&promotion_context=" + promotion, "/p/course%2d7?promotion_context=" + promotion, "/p/course-7?promotion_context=dpc_" + strings.Repeat("A", 42), "/p/course-7?promotion_context=" + promotion + "#fragment", "/c/coupon-2026?promotion_context=" + promotion} {
		if _, err := service.Start(context.Background(), path); !errors.Is(err, ErrInvalid) {
			t.Fatalf("path=%q err=%v", path, err)
		}
	}
}
