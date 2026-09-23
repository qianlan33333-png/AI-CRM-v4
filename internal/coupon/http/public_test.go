package http

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type publicTestUOW struct{}

func (publicTestUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type publicCouponStub struct {
	coupon couponport.PublicCoupon
	holder int64
	target string
}

func (s *publicCouponStub) GetPublicCoupon(_ context.Context, slug string) (couponport.PublicCoupon, error) {
	if slug != s.coupon.PublicSlug {
		return couponport.PublicCoupon{}, errors.New("missing")
	}
	return s.coupon, nil
}
func (s *publicCouponStub) PublicClaimState(_ context.Context, holder int64, couponID couponport.ID) (couponport.PublicCouponClaimState, error) {
	if holder != 9 || couponID != 7 {
		return couponport.PublicCouponClaimState{}, errors.New("wrong claim state")
	}
	return couponport.PublicCouponClaimState{ClaimCount: 1}, nil
}
func (s *publicCouponStub) ListAvailableClaims(_ context.Context, holder int64, target string, _ time.Time) ([]couponport.CustomerCoupon, error) {
	s.holder, s.target = holder, target
	return []couponport.CustomerCoupon{{ClaimID: 31, CouponID: 7, Name: "公开券", DiscountMinor: 100, Currency: "CNY", Status: "available"}}, nil
}
func (s *publicCouponStub) EnsurePublicShare(context.Context, couponport.ID, int64) (couponport.PublicCouponShare, error) {
	return couponport.PublicCouponShare{}, errors.New("unused")
}

type publicClaimStub struct{ command couponport.ClaimCommand }

func (s *publicClaimStub) Claim(_ context.Context, command couponport.ClaimCommand) (couponport.CustomerCoupon, error) {
	s.command = command
	return couponport.CustomerCoupon{ClaimID: 32, CouponID: int64(command.CouponID), Name: "公开券", DiscountMinor: 100, Currency: "CNY", Status: "available"}, nil
}

type publicSessionStub struct{}

func (publicSessionStub) LookupWithin(_ context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if token != "trusted-session-token-123456" {
		return paymentport.SessionActor{}, errors.New("invalid")
	}
	return paymentport.SessionActor{PayerCustomerID: 9, BeneficiaryCustomerID: 9}, nil
}

type publicProductStub struct{}

func (publicProductStub) ReadCheckoutProductWithin(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.CheckoutProduct, error) {
	if kind != productport.ProductOptionServicePeriod || id != 8 {
		return productport.CheckoutProduct{}, errors.New("wrong product")
	}
	return productport.CheckoutProduct{ID: id, ProductType: kind, Code: "term-31", Name: "31 天服务", PriceMinor: 9800, Currency: "CNY", Version: 2, ServicePeriodDurationDays: 31}, nil
}

func newPublicCouponHandler(t *testing.T, status string) (*PublicHandler, *publicCouponStub, *publicClaimStub) {
	t.Helper()
	stub := &publicCouponStub{coupon: couponport.PublicCoupon{Coupon: couponport.Coupon{ID: 7, Name: "公开券", DiscountAmountTotal: 100, Currency: "CNY", Status: status, TotalIssueLimit: 10, IssuedCount: 2, ClaimStartsAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ClaimEndsAt: time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC), Instructions: "仅限适用商品", TargetRefs: []string{"service_period:8"}}, PublicSlug: "cp-a1b2c3"}}
	claims := &publicClaimStub{}
	h, err := NewPublicHandler(stub, claims, publicSessionStub{}, publicProductStub{}, publicTestUOW{})
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC) }
	return h, stub, claims
}

func trustedCouponRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "trusted-session-token-123456"})
	return r
}

func TestPublicCouponUsesTrustedSessionForClaimAndAvailableCoupons(t *testing.T) {
	h, public, claims := newPublicCouponHandler(t, "published")
	page := httptest.NewRecorder()
	h.ServeHTTP(page, trustedCouponRequest(http.MethodGet, "/c/cp-a1b2c3"))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-public-slug="cp-a1b2c3"`) || !strings.Contains(page.Body.String(), `/s/term-31/pay`) || !strings.Contains(page.Body.String(), `周期商品 · ¥98.00`) {
		t.Fatalf("page status=%d body=%s", page.Code, page.Body.String())
	}
	available := httptest.NewRecorder()
	h.ServeHTTP(available, trustedCouponRequest(http.MethodGet, "/api/h5/coupons/available?target_ref=service_period%3A8"))
	if available.Code != http.StatusOK || public.holder != 9 || public.target != "service_period:8" || !strings.Contains(available.Body.String(), `"claim_id":31`) {
		t.Fatalf("available status=%d holder=%d target=%q body=%s", available.Code, public.holder, public.target, available.Body.String())
	}
	claim := trustedCouponRequest(http.MethodPost, "/api/h5/coupons/cp-a1b2c3/claim")
	claim.Header.Set("Idempotency-Key", "coupon-public-claim-key-0001")
	claim.Header.Set("Content-Type", "application/json")
	claim.Body = io.NopCloser(strings.NewReader("{}"))
	claim.ContentLength = 2
	response := httptest.NewRecorder()
	h.ServeHTTP(response, claim)
	if response.Code != http.StatusOK || claims.command.HolderCustomerID != 9 || claims.command.CouponID != 7 || claims.command.ActorScope != "coupon-public:cp-a1b2c3" || claims.command.IdempotencyKey != "coupon-public-claim-key-0001" || strings.Contains(response.Body.String(), "customer_id") {
		t.Fatalf("claim status=%d command=%+v body=%s", response.Code, claims.command, response.Body.String())
	}
}

func TestFrozenCouponPublicTemplateRetainsDonorDOMAndRejectsClaimFields(t *testing.T) {
	actual := fmt.Sprintf("%x", sha256.Sum256([]byte(frozenCouponPublicTemplate)))
	if actual != frozenCouponPublicTemplateSHA256 {
		t.Fatalf("donor hash=%s", actual)
	}
	h, _, _ := newPublicCouponHandler(t, "published")
	page := httptest.NewRecorder()
	request := trustedCouponRequest(http.MethodGet, "/c/cp-a1b2c3")
	request.Header.Set("User-Agent", "MicroMessenger")
	h.ServeHTTP(page, request)
	for _, want := range []string{`data-route-owner="ai_crm_next"`, `class="coupon"`, `id="claimButton"`, `id="productSection"`, `body: "{}"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("frozen donor DOM missing %q: %s", want, page.Body.String())
		}
	}
	for _, body := range []string{`{"customer_id":123}`, `{}x`} {
		bad := trustedCouponRequest(http.MethodPost, "/api/h5/coupons/cp-a1b2c3/claim")
		bad.Header.Set("Idempotency-Key", "coupon-public-claim-key-0002")
		bad.Body = io.NopCloser(strings.NewReader(body))
		bad.ContentLength = -1 // exercise the unknown-length streaming path.
		response := httptest.NewRecorder()
		h.ServeHTTP(response, bad)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_request") {
			t.Fatalf("fielded claim body=%q status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
}

func TestFrozenCouponPublicPageRendersLimitAndTerminalStates(t *testing.T) {
	h, _, _ := newPublicCouponHandler(t, "published")
	request := trustedCouponRequest(http.MethodGet, "/c/cp-a1b2c3")
	request.Header.Set("User-Agent", "MicroMessenger")
	limited := httptest.NewRecorder()
	h.ServeHTTP(limited, request)
	if limited.Code != http.StatusOK || !strings.Contains(limited.Body.String(), "已达到领取上限") || !strings.Contains(limited.Body.String(), `id="claimButton" type="button" disabled`) || strings.Contains(limited.Body.String(), "{%") {
		t.Fatalf("limited page=%d body=%s", limited.Code, limited.Body.String())
	}
	ended, _, _ := newPublicCouponHandler(t, "stopped")
	terminal := httptest.NewRecorder()
	terminalRequest := httptest.NewRequest(http.MethodGet, "/c/cp-a1b2c3", nil)
	terminalRequest.Header.Set("User-Agent", "MicroMessenger")
	ended.ServeHTTP(terminal, terminalRequest)
	if terminal.Code != http.StatusOK || !strings.Contains(terminal.Body.String(), "领取已结束") || !strings.Contains(terminal.Body.String(), `disabled`) {
		t.Fatalf("terminal page=%d body=%s", terminal.Code, terminal.Body.String())
	}
}

func TestPublicCouponOAuthAndMalformedRoutesFailClosed(t *testing.T) {
	h, _, _ := newPublicCouponHandler(t, "published")
	redirect := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/c/cp-a1b2c3", nil)
	req.Header.Set("User-Agent", "MicroMessenger")
	h.ServeHTTP(redirect, req)
	if redirect.Code != http.StatusFound || redirect.Header().Get("Location") != "/api/h5/wechat-pay/oauth/start?return_url=%2Fc%2Fcp-a1b2c3" {
		t.Fatalf("oauth redirect=%d %q", redirect.Code, redirect.Header().Get("Location"))
	}
	available := httptest.NewRecorder()
	h.ServeHTTP(available, httptest.NewRequest(http.MethodGet, "/api/h5/coupons/available?target_ref=service_period%3A8", nil))
	if available.Code != http.StatusUnauthorized || strings.Contains(available.Body.String(), "customer_id") || strings.Contains(available.Body.String(), `oauth_start`) {
		t.Fatalf("available no session=%d %s", available.Code, available.Body.String())
	}
	claim := httptest.NewRequest(http.MethodPost, "/api/h5/coupons/cp-a1b2c3/claim", nil)
	claim.Header.Set("Idempotency-Key", "coupon-public-claim-key-0001")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, claim)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `%2Fc%2Fcp-a1b2c3`) {
		t.Fatalf("claim no session=%d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/c/7", "/c/cp-a1b2c3/extra", "/api/h5/coupons/cp-a1b2c3/claim?x=1", "/api/h5/coupons/available?target_ref=8"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, trustedCouponRequest(http.MethodGet, path))
		if r.Code < 400 {
			t.Fatalf("malformed route %s accepted: %d", path, r.Code)
		}
	}
}

func TestPublicCouponStoppedStateDoesNotClaimOrStartOAuth(t *testing.T) {
	h, _, _ := newPublicCouponHandler(t, "stopped")
	page := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/c/cp-a1b2c3", nil)
	req.Header.Set("User-Agent", "MicroMessenger")
	h.ServeHTTP(page, req)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), `id="claim"`) || !strings.Contains(page.Body.String(), "不可领取") {
		t.Fatalf("stopped page=%d %s", page.Code, page.Body.String())
	}
	claim := trustedCouponRequest(http.MethodPost, "/api/h5/coupons/cp-a1b2c3/claim")
	claim.Header.Set("Idempotency-Key", "coupon-public-claim-key-0001")
	claim.Header.Set("Content-Type", "application/json")
	claim.Body = io.NopCloser(strings.NewReader("{}"))
	claim.ContentLength = 2
	response := httptest.NewRecorder()
	h.ServeHTTP(response, claim)
	if response.Code != http.StatusConflict {
		t.Fatalf("stopped claim=%d", response.Code)
	}
}
