package sidebar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strconv"
	"strings"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var ErrInvalidContext = errors.New("invalid sidebar context")

// Bootstrap state sentinel errors. The composition-root viewer bootstrapper
// returns them so the HTTP adapter can answer with the sidebar bootstrap
// state machine instead of a bare error.
var (
	ErrViewerSessionRequired = errors.New("sidebar viewer session required")
	ErrCustomerNotBound      = errors.New("sidebar customer not bound")
)

type Principal struct {
	CorpID     string
	EmployeeID string
}

// ViewerBootstrapper authenticates the WeCom sidebar viewer session (HttpOnly
// cookie), resolves the external contact to an existing local customer, and
// mints a scoped context token. It is implemented at the composition root over
// the WeCom session, identity, and context-token services; it must never
// provision a customer or trust caller-supplied employee/corp fields.
type ViewerBootstrapper interface {
	BootstrapViewer(ctx context.Context, request *http.Request, externalUserID string) (Principal, customerdomain.CustomerID, string, error)
}

type ContextVerifier interface {
	VerifySidebarContext(context.Context, string) (Principal, customerdomain.CustomerID, error)
}

type Config struct {
	Contexts     ContextVerifier
	Viewer       ViewerBootstrapper
	Profiles     customerport.SidebarProfileService
	Surveys      customerport.CustomerSurveyReader
	Timeline     customerport.CustomerTimelineReader
	Products     productport.ProductOptionReader
	ProductByID  productport.ProductTargetReader
	Orders       orderport.Query
	Entitlements orderport.EntitlementService
	// Coupons is the Coupon-owned definition directory. It intentionally is
	// not CustomerCouponReader: a customer's issued claims are not the set of
	// public rules that the standard sidebar is allowed to display.
	Coupons      couponport.SidebarClaimableCatalog
	Materials    mediaport.ImageLibraryReader
	MaterialSend mediaport.SidebarImageSendReader
	// ImageVariants serves bounded enabled-image previews to the scoped sidebar
	// viewer. It is optional so existing contract tests keep constructing
	// Config without it; a nil reader answers 503 capability_not_ready.
	ImageVariants mediaport.EnabledImageVariantReader
	Radar         radarport.Manager
	Sends         outboundport.SidebarSendAccepter
	PublicOrigin  string
	// CursorSigningKey binds sidebar pagination to the authenticated Customer
	// projection and its fixed watermark. It is process-local, like the
	// Customer admin profile cursor key, so a restart deliberately requires a
	// fresh first page rather than accepting an old cursor under a new process.
	CursorSigningKey []byte
	Now              func() time.Time
}

type Handler struct{ config Config }

func NewHandler(config Config) (*Handler, error) {
	if config.Contexts == nil || config.Profiles == nil || config.Surveys == nil || config.Timeline == nil || config.Products == nil || config.ProductByID == nil || config.Orders == nil || config.Entitlements == nil || config.Coupons == nil || config.Materials == nil || config.MaterialSend == nil || config.Radar == nil || config.Sends == nil || len(config.CursorSigningKey) < 32 {
		return nil, errors.New("sidebar dependencies are required")
	}
	origin, err := urlpkg.Parse(strings.TrimRight(config.PublicOrigin, "/"))
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.Path != "" {
		return nil, errors.New("sidebar public origin must be an https origin")
	}
	config.PublicOrigin = origin.String()
	config.CursorSigningKey = append([]byte(nil), config.CursorSigningKey...)
	return &Handler{config: config}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	if h.config.Viewer != nil {
		mux.HandleFunc("POST /api/sidebar/v2/bootstrap", h.bootstrap)
	}
	mux.HandleFunc("GET /api/sidebar/v2/workbench", h.workbench)
	mux.HandleFunc("GET /api/sidebar/v2/profile", h.profile)
	mux.HandleFunc("PUT /api/sidebar/v2/profile", h.updateProfile)
	mux.HandleFunc("POST /api/sidebar/v2/phone-binding", h.bindPhone)
	mux.HandleFunc("GET /api/sidebar/v2/questionnaires", h.questionnaires)
	mux.HandleFunc("GET /api/sidebar/v2/timeline", h.timeline)
	mux.HandleFunc("GET /api/sidebar/v2/products", h.products)
	mux.HandleFunc("GET /api/sidebar/v2/orders", h.orders)
	mux.HandleFunc("GET /api/sidebar/v2/periodic-orders", h.periodicOrders)
	mux.HandleFunc("PUT /api/sidebar/v2/periodic-orders/{entitlement_id}/remark", h.updateRemark)
	mux.HandleFunc("GET /api/sidebar/v2/coupons", h.coupons)
	mux.HandleFunc("GET /api/sidebar/v2/materials", h.materials)
	mux.HandleFunc("GET /api/sidebar/v2/materials/{image_id}/variants/{variant_key}", h.materialVariant)
	mux.HandleFunc("GET /api/sidebar/v2/radar-links", h.radarLinks)
	mux.HandleFunc("POST /api/sidebar/v2/send-intents", h.createSendIntent)
	mux.HandleFunc("POST /api/sidebar/v2/send-intents/{intent_id}/outcome", h.completeSendIntent)
	return mux
}

// sidebarSafety declares the sidebar's standing safety envelope: every
// projection is local CRM data, no provider execution happened while serving
// the read.  Writes carry their own receipts through the send-intent flow.
func sidebarSafety() map[string]any {
	return map[string]any{"local_only": true, "provider_execution_eligible": false, "real_external_call_executed": false}
}

// bootstrap resolves the viewer session and external contact into a scoped
// context token plus the first workbench projection in one round trip, so the
// workbench never falls back to the retired two-step context-token flow.
func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExternalUserID string `json:"external_userid"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.ExternalUserID) == "" || len(body.ExternalUserID) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, customerID, token, err := h.config.Viewer.BootstrapViewer(r.Context(), r, body.ExternalUserID)
	if err != nil {
		switch {
		case errors.Is(err, ErrViewerSessionRequired):
			h.writeJSON(w, http.StatusOK, map[string]any{"state": "viewer_session_required", "safety": sidebarSafety()})
		case errors.Is(err, ErrCustomerNotBound):
			h.writeJSON(w, http.StatusOK, map[string]any{"state": "customer_not_bound", "safety": sidebarSafety()})
		default:
			h.sectionError(w)
		}
		return
	}
	workbench, werr := h.workbenchProjection(r.Context(), customerID)
	if werr != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"state":         "ready",
		"context_token": token,
		"customer_id":   int64(customerID),
		"workbench":     workbench,
		"safety":        sidebarSafety(),
	})
}

// workbenchProjection assembles the first paint from existing section
// services. Counts use each Owner's actual total through bounded reads:
// questionnaires fetch one item plus their authoritative total, orders and
// periodic orders use their store totals, and materials count the shared
// library. The profile only carries fields
// the directory projection actually stores; there is no owner assignment in
// this backend, so no owner_staff_id is emitted.
func (h *Handler) workbenchProjection(ctx context.Context, customerID customerdomain.CustomerID) (map[string]any, error) {
	profile, err := h.config.Profiles.ReadSidebarProfile(ctx, customerID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(profile.DisplayName)
	if name == "" {
		name = fmt.Sprintf("客户 %d", int64(customerID))
	}
	surveys, err := h.config.Surveys.CustomerSurveys(ctx, customerID, customerport.PageQuery{Limit: 1, Watermark: h.now()})
	if err != nil {
		return nil, err
	}
	orders, err := h.config.Orders.List(ctx, orderport.ListQuery{CustomerID: int64(customerID), Limit: 1})
	if err != nil {
		return nil, err
	}
	entitlements, err := h.config.Entitlements.ListCustomerEntitlements(ctx, int64(customerID), 1)
	if err != nil {
		return nil, err
	}
	materials, err := h.config.Materials.ListImages(ctx, mediaport.ImageListQuery{Limit: 1, EnabledOnly: true})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"profile":              sidebarWorkbenchProfile(profile, name, customerID),
		"questionnaire_count":  surveys.Total,
		"order_count":          orders.Total,
		"periodic_order_count": entitlements.Total,
		"material_count":       materials.Total,
		"safety":               sidebarSafety(),
	}, nil
}

// sidebarWorkbenchProfile is a deliberately safe Customer-owned projection.
// It includes the full profile contract that the standard Sidebar renders,
// while preserving the Port's boundary: no raw phone or external identifier is
// present. A declared phone therefore remains distinguishable from a provider
// verified phone through PhoneAssurance.
func sidebarWorkbenchProfile(profile customerport.SidebarProfile, name string, customerID customerdomain.CustomerID) map[string]any {
	return map[string]any{
		// The workbench is already scoped by BootstrapViewer's resolved canonical
		// CustomerID. Keep its presentation label derived from that same trusted
		// key instead of any provider identifier or a separate frontend format.
		"customer_id":             int64(customerID),
		"oneid":                   customerdomain.CanonicalOneIDLabel(customerID),
		"customer_number":         profile.CustomerNumber,
		"name":                    name,
		"display_name":            name,
		"avatar_url":              profile.AvatarURL,
		"phone_masked":            profile.PhoneMasked,
		"phone_assurance":         profile.PhoneAssurance,
		"status":                  profile.Status,
		"activation_status":       profile.ActivationState,
		"gender":                  profile.Gender,
		"contact_type":            profile.ContactType,
		"corp_name":               profile.CorpName,
		"source":                  profile.Source,
		"profile_source":          profile.ProfileSource,
		"profile_version":         profile.ProfileVersion,
		"industry":                profile.Industry,
		"industry_description":    profile.IndustryDescription,
		"needs_blockers_followup": profile.NeedsBlockersFollowup,
		"version":                 profile.Version,
		"last_synced_at":          profile.LastSyncedAt,
		"updated_at":              profile.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *Handler) workbench(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	profile, err := h.config.Profiles.ReadSidebarProfile(r.Context(), customerID)
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"customer_id": customerID,
		"status":      profile.Status,
		"tabs":        []string{"profile", "questionnaires", "products", "orders", "coupons", "materials"},
	})
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	profile, err := h.config.Profiles.ReadSidebarProfile(r.Context(), customerID)
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer": profile, "capability": "ready"})
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var body struct {
		DisplayName            string  `json:"display_name"`
		Gender                 int16   `json:"gender"`
		CorpName               string  `json:"corp_name"`
		Source                 *string `json:"source"`
		Industry               *string `json:"industry"`
		IndustryDescription    *string `json:"industry_description"`
		NeedsBlockersFollowup  *string `json:"needs_blockers_followup"`
		ExpectedVersion        int64   `json:"expected_version"`
		ExpectedProfileVersion *int64  `json:"expected_profile_version"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	command := customerport.SidebarProfileUpdate{CustomerID: customerID, EmployeeID: principal.EmployeeID, DisplayName: body.DisplayName, Gender: body.Gender, CorpName: body.CorpName, ExpectedVersion: body.ExpectedVersion, IdempotencyKey: idempotencyKey(r)}
	if body.Source != nil {
		command.ProfileSource, command.SourceSet = *body.Source, true
	}
	if body.Industry != nil {
		command.Industry, command.IndustrySet = *body.Industry, true
	}
	if body.IndustryDescription != nil {
		command.IndustryDescription, command.IndustryDescriptionSet = *body.IndustryDescription, true
	}
	if body.NeedsBlockersFollowup != nil {
		command.NeedsBlockersFollowup, command.NeedsBlockersFollowupSet = *body.NeedsBlockersFollowup, true
	}
	if body.ExpectedProfileVersion != nil {
		command.ExpectedProfileVersion = *body.ExpectedProfileVersion
	}
	result, err := h.config.Profiles.UpdateSidebarProfile(r.Context(), command)
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer": result})
}

func (h *Handler) bindPhone(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	result, err := h.config.Profiles.BindSidebarPhone(r.Context(), customerport.SidebarPhoneBind{CustomerID: customerID, EmployeeID: principal.EmployeeID, Phone: body.Phone, IdempotencyKey: idempotencyKey(r)})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) questionnaires(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	query, limit, ok := h.sidebarPageQuery(w, r, "questionnaires", customerID)
	if !ok {
		return
	}
	page, err := h.config.Surveys.CustomerSurveys(r.Context(), customerID, query)
	if err != nil {
		h.sectionError(w)
		return
	}
	items, next, hasMore, err := h.pageSurveys(page.Items, limit, customerID, query)
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer_id": customerID, "items": items, "total": page.Total, "limit": limit, "has_more": hasMore, "next_cursor": next, "source_status": page.Status.State, "as_of": page.Status.AsOf})
}

func (h *Handler) timeline(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	query, limit, ok := h.sidebarPageQuery(w, r, "business-timeline", customerID)
	if !ok {
		return
	}
	page, err := h.config.Timeline.CustomerTimeline(r.Context(), customerID, query)
	if err != nil {
		h.sectionError(w)
		return
	}
	items, next, hasMore, err := h.pageTimeline(page.Items, limit, customerID, query)
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer_id": customerID, "items": items, "limit": limit, "has_more": hasMore, "next_cursor": next, "source_status": page.Status.State, "as_of": page.Status.AsOf})
}

func (h *Handler) products(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authorize(w, r); !ok {
		return
	}
	limit, ok := boundedLimit(w, r, int(productport.ProductOptionDefaultLimit), int(productport.ProductOptionMaximumLimit))
	if !ok {
		return
	}
	page, err := h.config.Products.ListProductOptions(r.Context(), productport.ProductOptionQuery{Q: r.URL.Query().Get("q"), ProductType: productport.ProductOptionType(r.URL.Query().Get("product_type")), Limit: int32(limit)})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *Handler) orders(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	offset, ok := boundedOffset(w, r, 1000000)
	if !ok {
		return
	}
	page, err := h.config.Orders.List(r.Context(), orderport.ListQuery{CustomerID: int64(customerID), Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *Handler) periodicOrders(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Entitlements.ListCustomerEntitlements(r.Context(), int64(customerID), int32(limit))
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *Handler) updateRemark(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("entitlement_id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		Remark          string `json:"remark"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	result, err := h.config.Entitlements.UpdateEntitlementRemark(r.Context(), orderport.RemarkCommand{EntitlementID: id, CustomerID: int64(customerID), EmployeeID: principal.EmployeeID, Remark: body.Remark, ExpectedVersion: body.ExpectedVersion, IdempotencyKey: idempotencyKey(r)})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) coupons(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	offset, ok := boundedOffset(w, r, int(couponport.SidebarClaimableMaximumOffset))
	if !ok {
		return
	}
	page, err := h.config.Coupons.ListSidebarClaimable(r.Context(), int64(customerID), couponport.SidebarClaimableQuery{Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		h.sectionError(w)
		return
	}
	// The Host may compose only a pre-existing, validated slug with its
	// configured origin. Missing slugs deliberately remain unlinked: rendering
	// the directory must never create a share or reserve/claim a coupon.
	type item struct {
		CouponID           couponport.ID                       `json:"coupon_id"`
		Name               string                              `json:"name"`
		DiscountMinor      int64                               `json:"discount_minor"`
		Currency           string                              `json:"currency"`
		Targets            []couponport.SidebarClaimableTarget `json:"targets"`
		ClaimEndsAt        time.Time                           `json:"claim_ends_at"`
		URL                string                              `json:"url,omitempty"`
		AvailabilityStatus string                              `json:"availability_status"`
		UserLimitReached   bool                                `json:"user_limit_reached"`
	}
	items := make([]item, 0, len(page.Items))
	for _, row := range page.Items {
		url := ""
		if row.PublicSlug != "" {
			url = h.config.PublicOrigin + "/c/" + urlpkg.PathEscape(row.PublicSlug)
		}
		items = append(items, item{CouponID: row.CouponID, Name: row.Name, DiscountMinor: row.DiscountMinor, Currency: row.Currency, Targets: row.Targets, ClaimEndsAt: row.ClaimEndsAt, URL: url, AvailabilityStatus: row.AvailabilityStatus, UserLimitReached: row.UserLimitReached})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}

func (h *Handler) materials(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authorize(w, r); !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 100)
	if !ok {
		return
	}
	offset, ok := boundedOffset(w, r, 1000000)
	if !ok {
		return
	}
	page, err := h.config.Materials.ListImages(r.Context(), mediaport.ImageListQuery{Limit: int64(limit), Offset: int64(offset), EnabledOnly: true, Search: r.URL.Query().Get("q"), Category: r.URL.Query().Get("category"), Tags: r.URL.Query().Get("tags")})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": page.Items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}

// materialVariant serves a bounded, read-only image preview to the scoped
// sidebar viewer. The admin image-library variant route stays admin-gated;
// this route re-uses the same Media-owned variant reader under the sidebar
// context token so the workbench can render thumbnails without admin cookies.
func (h *Handler) materialVariant(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authorize(w, r); !ok {
		return
	}
	if h.config.ImageVariants == nil {
		writeError(w, http.StatusServiceUnavailable, "capability_not_ready")
		return
	}
	imageID, err := strconv.ParseInt(r.PathValue("image_id"), 10, 64)
	if err != nil || imageID < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key := r.PathValue("variant_key")
	if key != "thumb_160" && key != "thumb_320" && key != "mobile_1080" {
		writeError(w, http.StatusNotFound, "resource_not_available")
		return
	}
	variant, err := h.config.ImageVariants.GetEnabledImageVariant(r.Context(), imageID, key)
	if err != nil {
		writeError(w, http.StatusNotFound, "resource_not_available")
		return
	}
	w.Header().Set("Content-Type", variant.MediaType)
	w.Header().Set("ETag", variant.ETag)
	w.Header().Set("Cache-Control", "private, max-age=300")
	if r.Header.Get("If-None-Match") == variant.ETag && variant.ETag != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(variant.Content)
}

func (h *Handler) radarLinks(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authorize(w, r); !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Radar.List(r.Context(), radarport.ListQuery{Status: radarport.StatusEnabled, Limit: int32(limit)})
	if err != nil {
		h.sectionError(w)
		return
	}
	type item struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	}
	items := make([]item, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, item{ID: int64(row.Link.ID), Name: row.Link.Name, Title: row.Link.Title, URL: h.config.PublicOrigin + "/r/" + urlpkg.PathEscape(string(row.Link.PublicCode)), ContentType: string(row.Link.Content.Type)})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": page.Total})
}

func (h *Handler) createSendIntent(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var body struct {
		ResourceKind string                        `json:"resource_kind"`
		ResourceID   string                        `json:"resource_id"`
		ProductType  productport.ProductOptionType `json:"product_type,omitempty"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	payload, err := h.sendPayload(r.Context(), int64(customerID), body.ResourceKind, body.ResourceID, body.ProductType)
	if err != nil {
		if errors.Is(err, mediaport.ErrSidebarMaterialPreparing) {
			w.Header().Set("Retry-After", "1")
			h.writeJSON(w, http.StatusAccepted, map[string]any{"state": "material_preparing"})
			return
		}
		if errors.Is(err, mediaport.ErrSidebarMaterialOutcomeUnknown) {
			writeError(w, http.StatusConflict, "material_upload_outcome_unknown")
			return
		}
		if errors.Is(err, mediaport.ErrSidebarMaterialPreparationFailed) {
			writeError(w, http.StatusServiceUnavailable, "material_upload_failed")
			return
		}
		if errors.Is(err, mediaport.ErrSidebarMaterialNotReady) {
			writeError(w, http.StatusServiceUnavailable, "capability_not_ready")
			return
		}
		writeError(w, http.StatusNotFound, "resource_not_available")
		return
	}
	digest := sha256.Sum256(payload)
	result, err := h.config.Sends.AcceptSidebarSend(r.Context(), outboundport.SidebarSendCommand{CustomerID: int64(customerID), EmployeeID: principal.EmployeeID, ResourceKind: body.ResourceKind, ResourceID: body.ResourceID, ContentDigest: digest, Payload: payload, IdempotencyKey: idempotencyKey(r)})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusAccepted, result)
}

func (h *Handler) completeSendIntent(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("intent_id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		Grant    string `json:"grant"`
		Outcome  string `json:"outcome"`
		Evidence string `json:"evidence"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	evidence := sha256.Sum256([]byte(strings.TrimSpace(body.Evidence)))
	result, err := h.config.Sends.CompleteSidebarSend(r.Context(), outboundport.SidebarSendOutcomeCommand{IntentID: id, CustomerID: int64(customerID), EmployeeID: principal.EmployeeID, Grant: body.Grant, Outcome: body.Outcome, EvidenceDigest: evidence})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) sendPayload(ctx context.Context, customerID int64, kind, rawID string, productType productport.ProductOptionType) ([]byte, error) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return nil, errors.New("invalid resource")
	}
	switch kind {
	case "product":
		shares, ok := h.config.ProductByID.(productport.SidebarProductShareReader)
		if !ok {
			return nil, errors.New("product sharing unavailable")
		}
		product, err := shares.ReadSidebarShareProduct(ctx, productType, productport.ID(id))
		if err != nil || product.ID != productport.ID(id) || product.Code == "" || product.Name == "" || product.ProductType != productType {
			return nil, errors.New("product unavailable for sharing")
		}
		prefix := "/p/"
		if product.ProductType == productport.ProductOptionServicePeriod {
			prefix = "/s/"
		}
		link := h.config.PublicOrigin + prefix + urlpkg.PathEscape(product.Code)
		cover, fallbackCover := h.productCardCover(product.CoverURL)
		description := ""
		if fallbackCover {
			description = "商品封面暂缺"
		}
		return json.Marshal(map[string]any{"msgtype": "news", "news": map[string]string{
			"link": link, "title": product.Name, "desc": description,
			"imgUrl": cover,
		}})
	case "coupon":
		coupon, err := h.config.Coupons.ReadSidebarClaimable(ctx, customerID, couponport.ID(id))
		if err != nil || coupon.CouponID != couponport.ID(id) || coupon.Name == "" || coupon.PublicSlug == "" {
			return nil, errors.New("coupon unavailable for sharing")
		}
		// This is a link only. Coupon's public claim command remains the only
		// authority that can allocate stock or create a customer claim. News
		// cards also carry the already-published neutral fallback cover used by
		// product cards; it identifies no coupon or product and avoids omitting
		// the SDK's imgUrl field.
		cover, _ := h.productCardCover("")
		return json.Marshal(map[string]any{"msgtype": "news", "news": map[string]string{
			"link": h.config.PublicOrigin + "/c/" + urlpkg.PathEscape(coupon.PublicSlug), "title": coupon.Name, "desc": "点击领取优惠券", "imgUrl": cover,
		}})
	case "material":
		material, err := h.config.MaterialSend.ReadSidebarImageForSend(ctx, id, h.now().Add(6*time.Minute))
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"msgtype": "image", "image": map[string]string{"mediaid": material.MediaID}})
	case "radar_link":
		detail, err := h.config.Radar.Get(ctx, radarport.RadarID(id))
		if err != nil || detail.Link.Status != radarport.StatusEnabled {
			return nil, errors.New("radar unavailable")
		}
		link := h.config.PublicOrigin + "/r/" + urlpkg.PathEscape(string(detail.Link.PublicCode))
		return json.Marshal(map[string]any{"msgtype": "link", "link": map[string]string{"title": detail.Link.Title, "desc": detail.Link.Description, "url": link}})
	default:
		return nil, errors.New("unsupported resource")
	}
}

// productCardCover returns an actually loadable public HTTPS asset. Relative
// URLs are accepted only for the two established public product routes; in
// particular, an /api/admin image preview is not public and falls back to the
// neutral cover. The SDK news-card requirement therefore never becomes a
// silent generic invalid-resource error.
func (h *Handler) productCardCover(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value != "" && len(value) <= 2048 {
		if parsed, err := urlpkg.Parse(value); err == nil && parsed.User == nil && parsed.Fragment == "" {
			if parsed.Scheme == "https" && parsed.Host != "" {
				return parsed.String(), false
			}
			if parsed.Scheme == "" && parsed.Host == "" && parsed.RawQuery == "" && isPublicProductCardPath(parsed.Path) {
				return h.config.PublicOrigin + parsed.String(), false
			}
		}
	}
	return h.config.PublicOrigin + "/static/sidebar_workbench/product-card-cover.png", true
}

func isPublicProductCardPath(path string) bool {
	if strings.HasPrefix(path, "/static/") {
		return true
	}
	if standardProductPublicMediaPath(path) {
		return true
	}
	const prefix = "/api/h5/service-period-products/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 5 || parts[1] != "images" || parts[3] != "variants" || parts[4] != "original" {
		return false
	}
	if parts[0] == "" || parts[2] == "" {
		return false
	}
	_, err := strconv.ParseInt(parts[2], 10, 64)
	return err == nil
}

func standardProductPublicMediaPath(path string) bool {
	const prefix = "/api/h5/product-images/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "variants" || parts[3] != "original" {
		return false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == parts[1]
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (Principal, customerdomain.CustomerID, bool) {
	// The current sidebar workbench sends the scoped token as
	// X-Sidebar-Context-Token; the retired workbench used Authorization:
	// Bearer.  Both name the same context token.
	token := strings.TrimSpace(r.Header.Get("X-Sidebar-Context-Token"))
	if token == "" {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(header, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		}
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "authentication_required")
		return Principal{}, 0, false
	}
	principal, customerID, err := h.config.Contexts.VerifySidebarContext(r.Context(), token)
	if err != nil || principal.CorpID == "" || principal.EmployeeID == "" || customerID < 1 {
		writeError(w, http.StatusUnauthorized, "invalid_context")
		return Principal{}, 0, false
	}
	return principal, customerID, true
}

func (h *Handler) commandError(w http.ResponseWriter, err error) {
	code := "invalid_request"
	status := http.StatusBadRequest
	if strings.Contains(err.Error(), "conflict") {
		code, status = "conflict", http.StatusConflict
	}
	writeError(w, status, code)
}
func (h *Handler) sectionError(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "section_unavailable")
}
func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
func (h *Handler) now() time.Time {
	if h.config.Now != nil {
		return h.config.Now().UTC()
	}
	return time.Now().UTC()
}

type sidebarPageCursor struct {
	Version    int    `json:"v"`
	Section    string `json:"s"`
	CustomerID int64  `json:"c"`
	Watermark  string `json:"w"`
	AfterAt    string `json:"a"`
	AfterID    int64  `json:"i"`
}

// sidebarPageQuery makes the Customer Owner's keyset fields available to the
// standard sidebar without replacing them with offsets. The cursor is signed,
// scoped to the already-verified customer, and carries a fixed watermark so a
// later page cannot shift when new owner records arrive.
func (h *Handler) sidebarPageQuery(w http.ResponseWriter, r *http.Request, section string, customerID customerdomain.CustomerID) (customerport.PageQuery, int, bool) {
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "limit" && key != "cursor") || len(entries) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return customerport.PageQuery{}, 0, false
		}
	}
	limit := 20
	if rawLimit := values.Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > 50 || strconv.Itoa(parsed) != rawLimit {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return customerport.PageQuery{}, 0, false
		}
		limit = parsed
	}
	query := customerport.PageQuery{Limit: limit + 1, Watermark: h.now()}
	cursor := values.Get("cursor")
	if cursor == "" {
		return query, limit, true
	}
	payload, err := decodeSidebarPageCursor(cursor, h.config.CursorSigningKey)
	if err != nil || payload.Section != section || payload.CustomerID != int64(customerID) || payload.AfterID < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return customerport.PageQuery{}, 0, false
	}
	query.Watermark, err = time.Parse(time.RFC3339Nano, payload.Watermark)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return customerport.PageQuery{}, 0, false
	}
	query.AfterAt, err = time.Parse(time.RFC3339Nano, payload.AfterAt)
	if err != nil || query.AfterAt.After(query.Watermark) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return customerport.PageQuery{}, 0, false
	}
	query.AfterID = payload.AfterID
	return query, limit, true
}

func (h *Handler) pageSurveys(items []customerport.SurveyItem, limit int, customerID customerdomain.CustomerID, query customerport.PageQuery) ([]customerport.SurveyItem, string, bool, error) {
	if len(items) <= limit {
		return items, "", false, nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	next, err := encodeSidebarPageCursor(sidebarPageCursor{Version: 1, Section: "questionnaires", CustomerID: int64(customerID), Watermark: query.Watermark.UTC().Format(time.RFC3339Nano), AfterAt: last.SubmittedAt.UTC().Format(time.RFC3339Nano), AfterID: last.ID}, h.config.CursorSigningKey)
	return items, next, true, err
}

func (h *Handler) pageTimeline(items []customerport.TimelineItem, limit int, customerID customerdomain.CustomerID, query customerport.PageQuery) ([]customerport.TimelineItem, string, bool, error) {
	if len(items) <= limit {
		return items, "", false, nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	next, err := encodeSidebarPageCursor(sidebarPageCursor{Version: 1, Section: "business-timeline", CustomerID: int64(customerID), Watermark: query.Watermark.UTC().Format(time.RFC3339Nano), AfterAt: last.OccurredAt.UTC().Format(time.RFC3339Nano), AfterID: last.ID}, h.config.CursorSigningKey)
	return items, next, true, err
}

func encodeSidebarPageCursor(payload sidebarPageCursor, key []byte) (string, error) {
	if len(key) < 32 {
		return "", ErrInvalidContext
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeSidebarPageCursor(value string, key []byte) (sidebarPageCursor, error) {
	if len(key) < 32 || len(value) > 2048 {
		return sidebarPageCursor{}, ErrInvalidContext
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return sidebarPageCursor{}, ErrInvalidContext
	}
	raw, err := decodeCanonicalSidebarBase64(parts[0])
	if err != nil {
		return sidebarPageCursor{}, err
	}
	signature, err := decodeCanonicalSidebarBase64(parts[1])
	if err != nil {
		return sidebarPageCursor{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return sidebarPageCursor{}, ErrInvalidContext
	}
	var payload sidebarPageCursor
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || payload.Version != 1 || payload.Section == "" || payload.CustomerID < 1 || payload.Watermark == "" || payload.AfterAt == "" {
		return sidebarPageCursor{}, ErrInvalidContext
	}
	return payload, nil
}

func decodeCanonicalSidebarBase64(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, ErrInvalidContext
	}
	return decoded, nil
}

func boundedLimit(w http.ResponseWriter, r *http.Request, fallback, maximum int) (int, bool) {
	allowed := map[string]bool{"limit": true, "q": true, "category": true, "product_type": true, "offset": true, "tags": true}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return 0, false
		}
	}
	if r.URL.Query().Get("limit") == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value < 1 || value > maximum {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return 0, false
	}
	return value, true
}

// boundedOffset parses the optional offset query parameter shared by the
// sidebar list endpoints. The parameter must already be whitelisted by
// boundedLimit for the handler.
func boundedOffset(w http.ResponseWriter, r *http.Request, maximum int) (int, bool) {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > maximum {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return 0, false
	}
	return value, true
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "json_required")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func idempotencyKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key"))
}
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}})
}

func ContentDigest(payload json.RawMessage) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
