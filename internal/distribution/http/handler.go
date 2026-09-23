// Package http hosts the external Distributor center. It deliberately has a
// separate browser-session boundary from CRM employee Access and from the
// short-lived Payment checkout session.
package http

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

const (
	DistributionSessionCookieName = "aicrm_distribution_session"
	// DistributionCSRFCookieName is intentionally readable by the isolated
	// distributor web app. Its matching header, plus a verified same-origin
	// request, proves a browser mutation came from that app rather than a
	// cross-site form.
	DistributionCSRFCookieName = "aicrm_distribution_csrf"
	DistributionCSRFHeader     = "X-Distribution-CSRF"
	maxBody                    = 32 << 10
)

type RegistrationApplication interface {
	CurrentAgreement(context.Context) (distributionport.Agreement, error)
	Profile(context.Context, distributionport.TrustedSessionActor) (distributionport.DistributorProfile, error)
	Register(context.Context, distributionport.RegisterCommand) (distributionport.DistributorProfile, error)
	PrepareReceiver(context.Context, distributionport.TrustedSessionActor) (distributionport.ReceiverPreparationResult, error)
}

type PromotionApplication interface {
	ListPromotionProducts(context.Context, distributionport.TrustedSessionActor, string, int32) (distributionport.PromotionPage, error)
	ApplicationTarget(context.Context, int64, distributiondomain.ProductType) (distributionport.ApplicationTarget, error)
	IssuePromotionLink(context.Context, distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error)
	ResolvePromotionTarget(context.Context, string) (string, error)
}

type EarningsApplication interface {
	Earnings(context.Context, distributionport.TrustedSessionActor) (distributionport.Earnings, error)
	ListCommissions(context.Context, distributionport.TrustedSessionActor, distributiondomain.CommissionStatus, string, int32) (distributionport.CommissionPage, error)
}

type SessionResolver interface {
	Resolve(context.Context, string) (distributionport.TrustedSessionActor, error)
}

// PaymentSessionBridge is composition-owned. It is the only component that
// receives the one-time Payment cookie; its implementation resolves that
// trusted session server-side and asks Distribution to mint its own session.
type PaymentSessionBridge interface {
	BridgePaymentSession(context.Context, string) (string, time.Time, error)
}

type Config struct {
	Registration RegistrationApplication
	Promotion    PromotionApplication
	Earnings     EarningsApplication
	Sessions     SessionResolver
	Bridge       PaymentSessionBridge
	CookieSecure bool
	// AllowedOrigins contains canonical configured public origins. H5 can be a
	// different configured origin from the ordinary public host.
	AllowedOrigins []string
}

type Handler struct {
	registration   RegistrationApplication
	promotion      PromotionApplication
	earnings       EarningsApplication
	sessions       SessionResolver
	bridge         PaymentSessionBridge
	cookieSecure   bool
	allowedOrigins map[string]struct{}
}

func NewHandler(config Config) (*Handler, error) {
	if config.Registration == nil || config.Promotion == nil || config.Sessions == nil || config.Bridge == nil {
		return nil, distributionport.ErrUnavailable
	}
	allowed := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, raw := range config.AllowedOrigins {
		origin, ok := canonicalOrigin(raw)
		if !ok {
			return nil, distributionport.ErrUnavailable
		}
		allowed[origin] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, distributionport.ErrUnavailable
	}
	return &Handler{registration: config.Registration, promotion: config.Promotion, earnings: config.Earnings, sessions: config.Sessions, bridge: config.Bridge, cookieSecure: config.CookieSecure, allowedOrigins: allowed}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.registration == nil || h.promotion == nil || h.sessions == nil || h.bridge == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case strings.HasPrefix(path, "/d/"):
		h.promotionHandoff(w, r, strings.TrimPrefix(path, "/d/"))
	case path == "/api/v1/distribution/agreement":
		h.agreement(w, r)
	case path == "/api/v1/distribution/session/bridge":
		h.bridgeSession(w, r)
	case path == "/api/v1/distribution/me":
		h.me(w, r)
	case path == "/api/v1/distribution/registration":
		h.register(w, r)
	case path == "/api/v1/distribution/receiver-preparation":
		h.prepareReceiver(w, r)
	case path == "/api/v1/distribution/products":
		h.products(w, r)
	case path == "/api/v1/distribution/application-context":
		h.applicationContext(w, r)
	case strings.HasPrefix(path, "/api/v1/distribution/products/") && strings.HasSuffix(path, "/promotion-credentials"):
		h.issueCredential(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/distribution/products/"), "/promotion-credentials"))
	case path == "/api/v1/distribution/earnings":
		h.earningsSummary(w, r)
	case path == "/api/v1/distribution/commissions":
		h.commissions(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) applicationContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, http.MethodGet)
		return
	}
	if !onlyQuery(r, "product_id", "product_type") {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	rawID := r.URL.Query().Get("product_id")
	productID, err := strconv.ParseInt(rawID, 10, 64)
	productType := distributiondomain.ProductType(r.URL.Query().Get("product_type"))
	if err != nil || productID < 1 || rawID != strconv.FormatInt(productID, 10) || !productType.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	target, err := h.promotion.ApplicationTarget(r.Context(), productID, productType)
	if err != nil {
		resultError(w, err)
		return
	}
	if target.ProductID != productID || target.ProductType != productType || !target.PolicyEnabled || strings.TrimSpace(target.ProductName) == "" || strings.TrimSpace(target.PurchaseURL) == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product_id": target.ProductID, "product_type": target.ProductType, "policy_enabled": target.PolicyEnabled, "product_name": target.ProductName, "purchase_url": target.PurchaseURL})
}

func (h *Handler) agreement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		method(w, http.MethodGet)
		return
	}
	agreement, err := h.registration.CurrentAgreement(r.Context())
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": agreement.Version, "content": agreement.Content})
}

func (h *Handler) bridgeSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.ContentLength > 0 {
		method(w, http.MethodPost)
		return
	}
	if !h.requireSameOrigin(w, r) || !h.requireIdempotencyKey(w, r) {
		return
	}
	paymentCookie, err := r.Cookie(paymentport.TrustedSessionCookieName)
	if err != nil || paymentCookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "payment_session_required")
		return
	}
	token, expiresAt, err := h.bridge.BridgePaymentSession(r.Context(), paymentCookie.Value)
	if err != nil {
		resultError(w, err)
		return
	}
	h.setDistributionSessionCookie(w, token, expiresAt)
	h.setDistributionCSRFCookie(w, expiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"expires_at": expiresAt.UTC()})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		method(w, http.MethodGet)
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	profile, err := h.registration.Profile(r.Context(), actor)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profileResponse(profile))
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		method(w, http.MethodPost)
		return
	}
	if !h.authorizeMutation(w, r) {
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	var body struct {
		AgreementVersion string `json:"agreement_version"`
	}
	if !decode(w, r, &body) {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	profile, err := h.registration.Register(r.Context(), distributionport.RegisterCommand{Actor: actor, AgreementVersion: body.AgreementVersion, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, profileResponse(profile))
}

func (h *Handler) prepareReceiver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.ContentLength > 0 {
		method(w, http.MethodPost)
		return
	}
	if !h.authorizeMutation(w, r) {
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	result, err := h.registration.PrepareReceiver(r.Context(), actor)
	if err != nil {
		resultError(w, err)
		return
	}
	status := http.StatusAccepted
	if result.State == "merchant_settlement_disabled" {
		// This is a truthful, side-effect-free merchant capability result, not
		// an asynchronously accepted receiver request.
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"receiver": receiverResponse(result.Receiver), "setup": map[string]any{"state": result.State, "action_url": result.ActionURL, "retry_after_seconds": result.RetryAfterSec}})
}

func (h *Handler) products(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !onlyQuery(r, "limit", "cursor") {
		method(w, http.MethodGet)
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(r, 50)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.promotion.ListPromotionProducts(r.Context(), actor, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, promotionProductResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor, "empty_reason": page.EmptyReason})
}

func (h *Handler) issueCredential(w http.ResponseWriter, r *http.Request, rawID string) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.ContentLength > 0 {
		method(w, http.MethodPost)
		return
	}
	if !h.authorizeMutation(w, r) {
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	// Product type is never accepted from the browser. Resolve the stored
	// candidate through a server-owned list/check at issuance time.
	page, err := h.promotion.ListPromotionProducts(r.Context(), actor, "", 100)
	if err != nil {
		resultError(w, err)
		return
	}
	for _, item := range page.Items {
		if item.ProductID != id {
			continue
		}
		link, issueErr := h.promotion.IssuePromotionLink(r.Context(), distributionport.IssuePromotionCommand{Actor: actor, ProductID: item.ProductID, ProductType: item.ProductType, IdempotencyKey: r.Header.Get("Idempotency-Key")})
		if issueErr != nil {
			resultError(w, issueErr)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"url": link.URL, "expires_at": link.ExpiresAt.UTC()})
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func (h *Handler) promotionHandoff(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		method(w, http.MethodGet)
		return
	}
	target, err := h.promotion.ResolvePromotionTarget(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	destination, parseErr := url.Parse(target)
	if parseErr != nil || destination.IsAbs() || destination.Host != "" || !strings.HasPrefix(destination.Path, "/") {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	// This opaque server-issued credential is the explicit purchase context.
	// Product/OAuth carry it only through the current checkout; a normal
	// Product entry has no context and therefore cannot inherit an old link.
	query := destination.Query()
	query.Set("promotion_context", token)
	destination.RawQuery = query.Encode()
	http.Redirect(w, r, destination.String(), http.StatusSeeOther)
}

func (h *Handler) earningsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		method(w, http.MethodGet)
		return
	}
	if h.earnings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	result, err := h.earnings.Earnings(r.Context(), actor)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gross_paid_sales_minor": result.GrossPaidSalesMinor, "successful_refunds_minor": result.SuccessfulRefundsMinor, "initial_commission_minor": result.InitialCommissionMinor, "commission_adjustments_minor": result.CommissionAdjustmentsMinor, "unsettled_payable_minor": result.UnsettledPayableMinor, "paid_commission_minor": result.PaidCommissionMinor, "recovered_minor": result.RecoveredMinor, "currency": result.Currency})
}

func (h *Handler) commissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !onlyQuery(r, "status", "cursor", "limit") {
		method(w, http.MethodGet)
		return
	}
	if h.earnings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	status := distributiondomain.CommissionStatus(r.URL.Query().Get("status"))
	if raw := r.URL.Query().Get("status"); raw != "" && !status.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limit, ok := queryLimit(r, 50)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.earnings.ListCommissions(r.Context(), actor, status, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, x := range page.Items {
		item := map[string]any{"commission_id": x.CommissionID, "order_reference": x.OrderReference, "product_name": x.ProductName, "initial_minor": x.InitialMinor, "current_payable_minor": x.CurrentPayableMinor, "paid_minor": x.PaidMinor, "status": x.Status, "hold_reason": x.HoldReason, "cancel_reason": x.CancelReason, "exception_reason": x.ExceptionReason, "paid_confirmed_at": x.PaidConfirmedAt.UTC(), "due_at": x.DueAt.UTC(), "created_at": x.CreatedAt.UTC(), "currency": x.Currency}
		if !x.SettlementConfirmedAt.IsZero() {
			item["settlement_confirmed_at"] = x.SettlementConfirmedAt.UTC()
			// Deprecated compatibility field. It has always been a Distribution
			// system confirmation fact here, never Provider/bank arrival time.
			item["paid_at"] = x.SettlementConfirmedAt.UTC()
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (h *Handler) sessionActor(w http.ResponseWriter, r *http.Request) (distributionport.TrustedSessionActor, bool) {
	cookie, err := r.Cookie(DistributionSessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "distribution_session_required")
		return distributionport.TrustedSessionActor{}, false
	}
	actor, err := h.sessions.Resolve(r.Context(), cookie.Value)
	if err != nil || !actor.Valid() {
		writeError(w, http.StatusUnauthorized, "distribution_session_required")
		return distributionport.TrustedSessionActor{}, false
	}
	return actor, true
}

func (h *Handler) setDistributionSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{Name: DistributionSessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode, Expires: expiresAt.UTC(), MaxAge: int(time.Until(expiresAt).Seconds())})
}

func (h *Handler) setDistributionCSRFCookie(w http.ResponseWriter, expiresAt time.Time) {
	token, err := randomToken()
	if err != nil {
		return
	}
	// The browser app reads only this random proof. It is not an identity,
	// authorization token, or a substitute for the HttpOnly distributor session.
	http.SetCookie(w, &http.Cookie{Name: DistributionCSRFCookieName, Value: token, Path: "/", Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode, Expires: expiresAt.UTC(), MaxAge: int(time.Until(expiresAt).Seconds())})
}

func (h *Handler) authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	return h.requireSameOrigin(w, r) && h.requireIdempotencyKey(w, r) && h.validCSRF(w, r)
}

func (h *Handler) requireSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if h.sameOrigin(r) {
		return true
	}
	writeError(w, http.StatusForbidden, "cross_site_request")
	return false
}

func (h *Handler) requireIdempotencyKey(w http.ResponseWriter, r *http.Request) bool {
	_, ok := idempotencyKey(w, r)
	return ok
}

func (h *Handler) sameOrigin(r *http.Request) bool {
	if h == nil || len(h.allowedOrigins) == 0 {
		return false
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" && site != "same-site" && site != "none" {
		return false
	}
	origin, ok := canonicalOrigin(r.Header.Get("Origin"))
	if !ok {
		return false
	}
	_, ok = h.allowedOrigins[origin]
	return ok
}

func (h *Handler) validCSRF(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie(DistributionCSRFCookieName)
	header := r.Header.Get(DistributionCSRFHeader)
	if err != nil || cookie.Value == "" || header == "" || len(cookie.Value) != len(header) || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
		writeError(w, http.StatusForbidden, "csrf_required")
		return false
	}
	return true
}

func canonicalOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", false
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), true
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func profileResponse(profile distributionport.DistributorProfile) map[string]any {
	response := map[string]any{"current_agreement_version": profile.CurrentAgreementVersion, "registration_required": profile.RegistrationRequired, "receiver": receiverResponse(profile.Receiver), "settlement": map[string]any{"enabled": profile.Settlement.Enabled, "reason": profile.Settlement.Reason}}
	if profile.Distributor.ID > 0 {
		response["distributor"] = map[string]any{"id": profile.Distributor.ID, "public_no": profile.Distributor.PublicNo, "agreement_version": profile.Distributor.AgreementVersion, "enabled": profile.Distributor.Enabled, "registered_at": profile.Distributor.RegisteredAt.UTC(), "version": profile.Distributor.Version}
	}
	return response
}

func receiverResponse(value distributionport.ReceiverReadiness) map[string]any {
	return map[string]any{"ready": value.Ready, "reason": value.Reason, "reference": value.Reference, "app_id": value.AppID, "checked_at": value.CheckedAt.UTC()}
}

func promotionProductResponse(value distributionport.PromotionProduct) map[string]any {
	return map[string]any{"product_id": value.ProductID, "product_type": value.ProductType, "cover_url": value.CoverURL, "name": value.Name, "purchase_url": value.PurchaseURL, "price_minor": value.PriceMinor, "currency": value.Currency, "commission_rate_basis_points": value.CommissionRateBasisPoints, "estimated_commission_minor": value.EstimatedCommissionMinor, "wait_days": value.WaitDays, "promotion_ready": value.PromotionReady, "promotion_block_reason": value.PromotionBlockReason}
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil || r.ContentLength > maxBody {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func onlyQuery(r *http.Request, allowed ...string) bool {
	values := r.URL.Query()
	for key, entries := range values {
		found := false
		for _, candidate := range allowed {
			if key == candidate {
				found = true
			}
		}
		if !found || len(entries) != 1 {
			return false
		}
	}
	return true
}

func queryLimit(r *http.Request, fallback int32) (int32, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	return int32(value), err == nil && value >= 1 && value <= 100
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	value := r.Header.Get("Idempotency-Key")
	if value == "" || value != strings.TrimSpace(value) || len(value) < 16 || len(value) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return "", false
	}
	return value, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, distributionport.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "distribution_session_required")
	case errors.Is(err, distributionport.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, distributionport.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, distributionport.ErrQualification):
		writeError(w, http.StatusConflict, "qualification_unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}
