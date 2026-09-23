package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// PublicHandler is the thin Host adapter for the frozen public coupon journey.
// It receives a holder only from Payment's opaque, trusted H5 session and
// passes that canonical Customer ID into Coupon. It never resolves identity,
// accepts a browser customer ID, or calculates a checkout price.
type PublicHandler struct {
	coupons  couponport.PublicCouponApplication
	claims   couponport.ClaimApplication
	session  paymentport.SessionReader
	products productport.CheckoutProductReader
	uow      platformport.UnitOfWork
	now      func() time.Time
}

func NewPublicHandler(coupons couponport.PublicCouponApplication, claims couponport.ClaimApplication, session paymentport.SessionReader, products productport.CheckoutProductReader, uow platformport.UnitOfWork) (*PublicHandler, error) {
	if coupons == nil || claims == nil || session == nil || products == nil || uow == nil {
		return nil, errors.New("public coupon dependencies are required")
	}
	return &PublicHandler{coupons: coupons, claims: claims, session: session, products: products, uow: uow, now: time.Now}, nil
}

func (h *PublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.coupons == nil || h.claims == nil || h.session == nil || h.products == nil || h.uow == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/c/"):
		h.page(w, r)
	case r.URL.Path == "/api/h5/coupons/available":
		h.available(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/h5/coupons/"):
		h.api(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *PublicHandler) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	slug, ok := publicCouponSlug(r.URL.EscapedPath(), "/c/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	coupon, err := h.coupons.GetPublicCoupon(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if coupon.Status == "published" && !h.hasSession(r.Context(), r) && isMicroMessenger(r.UserAgent()) {
		http.Redirect(w, r, oauthStartPath("/c/"+slug), http.StatusFound)
		return
	}
	products, err := h.checkoutProducts(r.Context(), coupon.TargetRefs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	isWechat := isMicroMessenger(r.UserAgent())
	claimCount := int64(0)
	if actor, sessionOK := h.trustedActor(r.Context(), r); sessionOK {
		claimState, stateErr := h.coupons.PublicClaimState(r.Context(), actor.PayerCustomerID, coupon.ID)
		if stateErr != nil {
			http.Error(w, "coupon status unavailable", http.StatusServiceUnavailable)
			return
		}
		claimCount = claimState.ClaimCount
	}
	displayState := publicCouponDisplayState(coupon, h.now().UTC())
	userLimitReached := claimCount >= int64(coupon.PerUserIssueLimit)
	state := frozenCouponPageState{
		Coupon:   couponPublicView{Name: coupon.Name, DiscountAmountTotal: coupon.DiscountAmountTotal, Instructions: coupon.Instructions, PublicSlug: coupon.PublicSlug},
		Products: products, IsWechat: isWechat, Claimed: claimCount > 0, ShowProducts: claimCount > 0,
		UserLimitReached: userLimitReached, DisplayState: displayState,
		Claimable:    coupon.Status == "published" && isWechat && displayState == "active" && !userLimitReached,
		ValidityText: publicCouponValidityText(coupon),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self' https: data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	_ = publicCouponPage.Execute(w, state)
}

func (h *PublicHandler) api(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/h5/coupons/")
	parts := strings.Split(tail, "/")
	if len(parts) == 1 && r.Method == http.MethodGet && r.URL.RawQuery == "" {
		h.state(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "claim" && r.Method == http.MethodPost && r.URL.RawQuery == "" {
		h.claim(w, r, parts[0])
		return
	}
	method(w, "GET, POST")
}

func (h *PublicHandler) state(w http.ResponseWriter, r *http.Request, rawSlug string) {
	slug, ok := publicCouponSlug("/"+rawSlug, "/")
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	coupon, err := h.coupons.GetPublicCoupon(r.Context(), slug)
	if err != nil {
		publicResultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, publicCouponJSON(coupon))
}

func (h *PublicHandler) available(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	query := r.URL.Query()
	if !only(query, "target_ref") || !validPublicTargetRef(query.Get("target_ref")) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	actor, ok := h.trustedActor(r.Context(), r)
	if !ok {
		writeOAuthRequired(w, "")
		return
	}
	items, err := h.coupons.ListAvailableClaims(r.Context(), actor.PayerCustomerID, query.Get("target_ref"), h.now().UTC())
	if err != nil {
		publicResultError(w, err)
		return
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if item.ClaimID < 1 || item.CouponID < 1 || item.Name == "" || item.DiscountMinor < 1 || item.Currency != "CNY" {
			writeError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		out = append(out, map[string]any{"claim_id": item.ClaimID, "coupon_id": item.CouponID, "name": item.Name, "discount_amount_minor": item.DiscountMinor, "currency": item.Currency, "status": item.Status, "valid_from": nullableTime(item.ValidFrom), "valid_until": nullableTime(item.ValidUntil)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": out})
}

func (h *PublicHandler) claim(w http.ResponseWriter, r *http.Request, rawSlug string) {
	slug, ok := publicCouponSlug("/"+rawSlug, "/")
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	// The frozen donor posts an empty JSON object. It remains a body-free
	// command: reject every field and never accept a browser customer, amount,
	// product or recipient. The trusted session is the only holder source.
	if r.ContentLength > 2 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Body != nil {
		// The accepted frozen body is exactly `{}`. Read one extra byte for a
		// chunked request so a trailing field cannot hide behind an unknown
		// Content-Length.
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 3))
		if readErr != nil || (len(raw) != 0 && (len(raw) != 2 || string(raw) != "{}")) {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	actor, ok := h.trustedActor(r.Context(), r)
	if !ok {
		writeOAuthRequired(w, "/c/"+slug)
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	coupon, err := h.coupons.GetPublicCoupon(r.Context(), slug)
	if err != nil {
		publicResultError(w, err)
		return
	}
	if coupon.Status != "published" {
		writeError(w, http.StatusConflict, "conflict")
		return
	}
	claimed, err := h.claims.Claim(r.Context(), couponport.ClaimCommand{CouponID: coupon.ID, HolderCustomerID: actor.PayerCustomerID, ActorScope: "coupon-public:" + slug, IdempotencyKey: key, ClaimedAt: h.now().UTC()})
	if err != nil {
		publicResultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "claim": map[string]any{"claim_id": claimed.ClaimID, "coupon_id": claimed.CouponID, "name": claimed.Name, "discount_amount_minor": claimed.DiscountMinor, "currency": claimed.Currency, "status": claimed.Status, "valid_from": nullableTime(claimed.ValidFrom), "valid_until": nullableTime(claimed.ValidUntil)}})
}

func (h *PublicHandler) hasSession(ctx context.Context, r *http.Request) bool {
	_, ok := h.trustedActor(ctx, r)
	return ok
}

func (h *PublicHandler) trustedActor(ctx context.Context, r *http.Request) (paymentport.SessionActor, bool) {
	cookie, err := r.Cookie(paymentport.TrustedSessionCookieName)
	if err != nil || cookie.Value == "" {
		return paymentport.SessionActor{}, false
	}
	var actor paymentport.SessionActor
	err = h.uow.Within(ctx, func(txctx context.Context) error {
		var lookupErr error
		actor, lookupErr = h.session.LookupWithin(txctx, cookie.Value, h.now().UTC())
		return lookupErr
	})
	if err != nil || actor.PayerCustomerID < 1 {
		return paymentport.SessionActor{}, false
	}
	return actor, true
}

type publicCouponProduct struct {
	Name, URL    string
	PriceMinor   int64
	DurationDays int32
	Available    bool
	KindLabel    string
}

func (h *PublicHandler) checkoutProducts(ctx context.Context, refs []string) ([]publicCouponProduct, error) {
	products := make([]publicCouponProduct, 0, len(refs))
	err := h.uow.Within(ctx, func(txctx context.Context) error {
		for _, ref := range refs {
			kind, id, ok := parsePublicTargetRef(ref)
			if !ok {
				return errors.New("invalid coupon target")
			}
			product, err := h.products.ReadCheckoutProductWithin(txctx, kind, productport.ID(id))
			if err != nil || product.ID != productport.ID(id) || product.ProductType != kind || product.Code == "" || product.Name == "" || product.PriceMinor < 1 || product.Currency != "CNY" || product.Version < 1 || (kind == productport.ProductOptionServicePeriod && product.ServicePeriodDurationDays < 1) {
				return errors.New("unavailable coupon target")
			}
			path := "/pay/" + url.PathEscape(product.Code)
			if kind == productport.ProductOptionServicePeriod {
				path = "/s/" + url.PathEscape(product.Code) + "/pay"
			}
			kindLabel := "普通商品"
			if kind == productport.ProductOptionServicePeriod {
				kindLabel = "周期商品"
			}
			products = append(products, publicCouponProduct{Name: product.Name, URL: path, PriceMinor: product.PriceMinor, DurationDays: product.ServicePeriodDurationDays, Available: true, KindLabel: kindLabel})
		}
		return nil
	})
	return products, err
}

func publicCouponDisplayState(coupon couponport.PublicCoupon, now time.Time) string {
	return couponport.AvailabilityStatusAt(coupon.Coupon, now)
}

func publicCouponValidityText(coupon couponport.PublicCoupon) string {
	return "领取时间：" + coupon.ClaimStartsAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04") + " 至 " + coupon.ClaimEndsAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04")
}

func publicCouponJSON(coupon couponport.PublicCoupon) map[string]any {
	remaining := coupon.TotalIssueLimit - coupon.IssuedCount
	if remaining < 0 {
		remaining = 0
	}
	return map[string]any{"ok": true, "coupon": map[string]any{"coupon_id": coupon.ID, "public_slug": coupon.PublicSlug, "name": coupon.Name, "discount_amount_minor": coupon.DiscountAmountTotal, "currency": coupon.Currency, "status": coupon.Status, "total_issue_limit": coupon.TotalIssueLimit, "issued_count": coupon.IssuedCount, "remaining_issue_count": remaining, "claim_starts_at": coupon.ClaimStartsAt.Format(time.RFC3339), "claim_ends_at": coupon.ClaimEndsAt.Format(time.RFC3339), "instructions": coupon.Instructions, "target_refs": coupon.TargetRefs}}
}

func writeOAuthRequired(w http.ResponseWriter, returnPath string) {
	result := map[string]any{"ok": false, "code": "oauth_required", "error": "oauth_required"}
	if slug, ok := publicCouponSlug(returnPath, "/c/"); ok {
		result["oauth_start"] = oauthStartPath("/c/" + slug)
	}
	writeJSON(w, http.StatusUnauthorized, result)
}

func oauthStartPath(returnPath string) string {
	return "/api/h5/wechat-pay/oauth/start?return_url=" + url.QueryEscape(returnPath)
}

func publicResultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, couponapp.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, couponapp.ErrInvalidCoupon), errors.Is(err, couponapp.ErrInvalidTarget):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, couponapp.ErrConflict), errors.Is(err, couponapp.ErrNoEligibleCoupon):
		writeError(w, http.StatusConflict, "conflict")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func isMicroMessenger(agent string) bool {
	return strings.Contains(strings.ToLower(agent), "micromessenger")
}

func publicCouponSlug(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	slug, err := url.PathUnescape(raw)
	return slug, err == nil && validCouponPublicSlug(slug)
}

func validCouponPublicSlug(value string) bool {
	if len(value) < 6 || len(value) > 120 || value != strings.TrimSpace(value) || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validPublicTargetRef(value string) bool { _, _, ok := parsePublicTargetRef(value); return ok }
func parsePublicTargetRef(value string) (productport.ProductOptionType, int64, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || parts[1] == "" {
		return "", 0, false
	}
	kind := productport.ProductOptionStandard
	if parts[0] == "service_period" {
		kind = productport.ProductOptionServicePeriod
	} else if parts[0] != "standard_product" {
		return "", 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return kind, id, err == nil && id > 0 && strconv.FormatInt(id, 10) == parts[1]
}
