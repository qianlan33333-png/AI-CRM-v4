package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// ServicePeriodPublicHandler owns the old /s/{code} public surface. It is a
// separate route from ordinary /p and /pay: codes are exact, never numeric
// aliases, and a standard Product with the same text cannot be selected here.
type publicServicePeriodMediaReader interface {
	mediaport.ImageVariantReader
	LocalImageExists(context.Context, int64) (bool, error)
}

type ServicePeriodPublicHandler struct {
	products           productport.ServicePeriodPublicReader
	presentation       productport.ServicePeriodPublicPresentationReader
	media              publicServicePeriodMediaReader
	leadQR             channelport.PublicLeadQRCodeReader
	uow                platformport.UnitOfWork
	sessions           paymentport.SessionReader
	entitlements       orderport.EntitlementService
	now                func() time.Time
	presentationAssets PublicPresentationAssets
}

func NewServicePeriodPublicHandler(products productport.ServicePeriodPublicReader) (*ServicePeriodPublicHandler, error) {
	if products == nil {
		return nil, errors.New("service period public reader is required")
	}
	h := &ServicePeriodPublicHandler{products: products, now: time.Now}
	if presentation, ok := products.(productport.ServicePeriodPublicPresentationReader); ok {
		h.presentation = presentation
	}
	return h, nil
}

// SetTrustedPublicState adds existing session and entitlement readers to the
// public Host. It only reads canonical Customer facts from Payment's opaque
// session; the route never resolves identity or mutates access.
func (h *ServicePeriodPublicHandler) SetTrustedPublicState(uow platformport.UnitOfWork, sessions paymentport.SessionReader, entitlements orderport.EntitlementService) error {
	if h == nil || uow == nil || sessions == nil || entitlements == nil {
		return errors.New("service period public state dependencies are required")
	}
	h.uow, h.sessions, h.entitlements = uow, sessions, entitlements
	return nil
}

func (h *ServicePeriodPublicHandler) SetPublicLeadQRCodeReader(reader channelport.PublicLeadQRCodeReader) error {
	if h == nil || reader == nil {
		return errors.New("public lead QR reader is required")
	}
	h.leadQR = reader
	return nil
}

func (h *ServicePeriodPublicHandler) SetPublicMediaReader(media publicServicePeriodMediaReader) error {
	if h == nil || media == nil {
		return errors.New("public media reader is required")
	}
	h.media = media
	return nil
}

// SetPublicPresentationAssets injects the same manifest-verified public
// presentation closure used by ordinary Product routes. A deferred closure is
// resolved only when this public route is requested; the service-period frozen
// body stays immutable and cannot silently fall back when a bound release is
// incomplete.
func (h *ServicePeriodPublicHandler) SetPublicPresentationAssets(assets PublicPresentationAssets) error {
	if h == nil || !assets.bound() {
		return errors.New("service-period public presentation assets are required")
	}
	h.presentationAssets = assets
	return nil
}

func (h *ServicePeriodPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.products == nil || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/h5/service-period-products/") {
		h.publicStateOrDetailMedia(w, r)
		return
	}
	code, payment, ok := servicePeriodPublicCode(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	product, err := h.products.ReadPublicServicePeriodByCode(r.Context(), code)
	available := err == nil
	if err != nil && h.presentation != nil {
		product, available, err = h.presentation.ReadServicePeriodPublicPresentationByCode(r.Context(), code)
	}
	if err != nil || product.ID < 1 || product.ProductType != productport.ProductOptionServicePeriod || product.Code != code || product.Name == "" || product.PriceMinor < 1 || product.Currency != "CNY" || product.Version < 1 || product.ServicePeriodDurationDays < 1 {
		http.NotFound(w, r)
		return
	}
	promotionContext, accepted := publicPromotionContext(r)
	if !accepted {
		clearLegacyPromotionCookies(w)
		http.NotFound(w, r)
		return
	}
	if promotionContext == "" {
		clearLegacyPromotionCookies(w)
	}
	public := publicProduct{ID: product.ID, Name: product.Name, PriceMinor: product.PriceMinor, Currency: product.Currency, PaymentPath: publicPaymentPath("/s/"+url.PathEscape(product.Code)+"/pay", promotionContext), PromotionContext: promotionContext, BuyButtonText: "立即报名", ProductKind: "service_period", CouponTargetRef: "service_period:" + strconv.FormatInt(int64(product.ID), 10), ServicePeriodDurationDays: product.ServicePeriodDurationDays, Images: publicDetailMedia(product.Code, product.DetailMedia), RegionOptionsJSON: "[]"}
	presentation := PublicPresentationAssets{}
	if h.presentationAssets.bound() {
		presentation, err = h.presentationAssets.resolved()
		if err != nil {
			http.Error(w, "public presentation unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", publicCommerceContentSecurityPolicy())
	if available {
		if err = publicProductPage.Execute(w, publicProductPageView{Product: public, Payment: true, Detail: !payment && len(public.Images) > 0, Presentation: publicPresentationTemplateFor(presentation)}); err != nil {
			return
		}
		return
	}
	state, entitlementErr := h.publicState(r.Context(), r, product, public)
	if entitlementErr != nil {
		http.Error(w, "service state unavailable", http.StatusServiceUnavailable)
		return
	}
	if !available {
		state.Available, state.Status, state.CTA, state.LeadQRURL = false, "unavailable", "暂未开放", ""
	}
	_ = renderServicePeriodPublicPageWithPresentation(w, state, presentation)
}

func (h *ServicePeriodPublicHandler) publicState(ctx context.Context, r *http.Request, product productport.CheckoutProduct, public publicProduct) (servicePeriodPublicState, error) {
	state := servicePeriodPublicState{Available: true, Product: public, Status: "none", CTA: "立即报名"}
	entitlement, found, err := h.trustedEntitlement(ctx, r, product.ID)
	if err != nil || !found {
		return state, err
	}
	state.Status, state.EndAt = entitlement.Status, entitlement.EndAt.UTC()
	if state.Status == "active" && !state.EndAt.After(h.now().UTC()) {
		state.Status = "expired"
	}
	switch state.Status {
	case "active":
		state.CTA = "立即续费"
		state.RemainingDays = remainingServicePeriodDays(h.now().UTC(), state.EndAt)
	case "expired", "refunded":
		state.Status, state.CTA = "expired", "重新开通"
	default:
		state.Status, state.CTA = "none", "立即报名"
	}
	if state.Status == "active" && !product.CompletionBlocksLeadQR && product.LeadChannelID > 0 && h.leadQR != nil {
		lead, leadErr := h.leadQR.ReadPublicLeadQRCode(ctx, product.LeadChannelID)
		if leadErr == nil && lead.URL != "" {
			state.LeadQRURL, state.LeadQRTitle, state.LeadQRSubtitle = lead.URL, product.LeadQRTitle, product.LeadQRSubtitle
		}
	}
	return state, nil
}

func (h *ServicePeriodPublicHandler) publicStateOrDetailMedia(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/h5/service-period-products/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		code, err := url.PathUnescape(parts[0])
		if err != nil || code == "" || code != strings.TrimSpace(code) {
			http.NotFound(w, r)
			return
		}
		product, readErr := h.products.ReadPublicServicePeriodByCode(r.Context(), code)
		if readErr != nil || product.Code != code || product.ProductType != productport.ProductOptionServicePeriod || product.ID < 1 || product.ServicePeriodDurationDays < 1 {
			http.NotFound(w, r)
			return
		}
		public := publicProduct{ID: product.ID, Name: product.Name, PriceMinor: product.PriceMinor, Currency: product.Currency, PaymentPath: "/s/" + url.PathEscape(product.Code) + "/pay", BuyButtonText: "立即报名", ProductKind: "service_period", CouponTargetRef: "service_period:" + strconv.FormatInt(int64(product.ID), 10), ServicePeriodDurationDays: product.ServicePeriodDurationDays, Images: publicDetailMedia(product.Code, product.DetailMedia)}
		state, stateErr := h.publicState(r.Context(), r, product, public)
		if stateErr != nil {
			http.Error(w, "service state unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "available": state.Available, "entitlement": map[string]any{"status": state.Status, "end_at": state.EndAt.UTC().Format(time.RFC3339), "remaining_days": state.RemainingDays}, "lead_qr": map[string]any{"qr_url": state.LeadQRURL, "title": state.LeadQRTitle, "subtitle": state.LeadQRSubtitle}, "cta_text": state.CTA, "checkout_url": state.Product.PaymentPath})
		return
	}
	h.detailMedia(w, r)
}

func (h *ServicePeriodPublicHandler) detailMedia(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/h5/service-period-products/"), "/")
	if len(parts) != 5 || parts[1] != "images" || parts[3] != "variants" || parts[4] != "original" {
		http.NotFound(w, r)
		return
	}
	code, err := url.PathUnescape(parts[0])
	id, idErr := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || idErr != nil || id < 1 || h.media == nil {
		http.NotFound(w, r)
		return
	}
	product, readErr := h.products.ReadPublicServicePeriodByCode(r.Context(), code)
	if readErr != nil {
		http.NotFound(w, r)
		return
	}
	allowed := false
	for _, item := range product.DetailMedia {
		if item.ImageID == id {
			allowed = true
			break
		}
	}
	if !allowed {
		http.NotFound(w, r)
		return
	}
	exists, existErr := h.media.LocalImageExists(r.Context(), id)
	if existErr != nil {
		http.Error(w, "media unavailable", http.StatusServiceUnavailable)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	variant, variantErr := h.media.GetImageVariant(r.Context(), id, "original")
	if variantErr != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", variant.MediaType)
	w.Header().Set("ETag", variant.ETag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(variant.Content)
}

func publicDetailMedia(code string, media []productport.PublicDetailMedia) []string {
	out := make([]string, 0, len(media))
	for _, item := range media {
		if item.ImageID > 0 {
			out = append(out, "/api/h5/service-period-products/"+url.PathEscape(code)+"/images/"+strconv.FormatInt(item.ImageID, 10)+"/variants/original")
		}
	}
	return out
}

func servicePeriodPublicCode(path string) (string, bool, bool) {
	if !strings.HasPrefix(path, "/s/") {
		return "", false, false
	}
	tail := strings.TrimPrefix(path, "/s/")
	payment := false
	if strings.HasSuffix(tail, "/pay") {
		payment, tail = true, strings.TrimSuffix(tail, "/pay")
	}
	if tail == "" || strings.Contains(tail, "/") {
		return "", false, false
	}
	code, err := url.PathUnescape(tail)
	if err != nil || code == "" || code != strings.TrimSpace(code) || len(code) > 200 || strings.ContainsRune(code, '\x00') {
		return "", false, false
	}
	return code, payment, true
}

func (h *ServicePeriodPublicHandler) trustedEntitlement(ctx context.Context, r *http.Request, productID productport.ID) (orderport.Entitlement, bool, error) {
	if h == nil || h.uow == nil || h.sessions == nil || h.entitlements == nil {
		return orderport.Entitlement{}, false, nil
	}
	cookie, err := r.Cookie(paymentport.TrustedSessionCookieName)
	if err != nil || cookie.Value == "" {
		return orderport.Entitlement{}, false, nil
	}
	var (
		actor        paymentport.SessionActor
		sessionValid bool
	)
	err = h.uow.Within(ctx, func(txctx context.Context) error {
		resolvedActor, lookupErr := h.sessions.LookupWithin(txctx, cookie.Value, h.now().UTC())
		if errors.Is(lookupErr, paymentport.ErrSessionRequired) {
			// An expired or invalid browser cookie has no public entitlement
			// authority. Treat it as an anonymous read, like no cookie, so it
			// cannot expose a prior customer state or turn into a false 503.
			return nil
		}
		if lookupErr != nil {
			return lookupErr
		}
		actor, sessionValid = resolvedActor, true
		return nil
	})
	if err != nil {
		return orderport.Entitlement{}, false, err
	}
	if !sessionValid || actor.PayerCustomerID < 1 {
		return orderport.Entitlement{}, false, nil
	}
	// The Payment session reader requires the short local transaction above,
	// while the stable Order entitlement application owns its own read UoW.
	// Keep the calls sequential: forwarding txctx would attempt a nested
	// PostgreSQL transaction and turn a legitimate public unavailable state
	// into a 503. This route is a read-only presentation projection.
	return h.entitlements.GetCustomerServicePeriodEntitlement(ctx, actor.PayerCustomerID, int64(productID))
}

func remainingServicePeriodDays(now, end time.Time) int32 {
	if !end.After(now) {
		return 0
	}
	days := int32(end.Sub(now).Hours() / 24)
	if end.After(now.Add(time.Duration(days) * 24 * time.Hour)) {
		days++
	}
	if days < 1 {
		return 1
	}
	return days
}
