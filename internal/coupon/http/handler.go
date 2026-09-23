// Package http adapts frozen coupon admin pages to narrow Coupon-owned ports.
// Claim reads contain only local Customer IDs and masked claim display values;
// it exposes no channel identity, redemption, order or payment operation.
package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Handler struct {
	rules    couponport.RuleApplication
	options  productport.ProductOptionReader
	targets  productport.ProductTargetBatchReader
	claims   couponport.CouponClaimAdminReader
	public   couponport.PublicCouponApplication
	security RequestSecurity
}

func NewHandler(rules couponport.RuleApplication, options productport.ProductOptionReader, security RequestSecurity) (*Handler, error) {
	if rules == nil || options == nil || security == nil {
		return nil, errors.New("coupon HTTP dependencies are required")
	}
	targets, ok := options.(productport.ProductTargetBatchReader)
	if !ok || targets == nil {
		return nil, errors.New("coupon product target reader is required")
	}
	return &Handler{rules: rules, options: options, targets: targets, security: security}, nil
}

// NewHandlerWithClaims enables the couponData read journey. Kept separate
// from NewHandler so composition can adopt the additive page without changing
// the established rule-management HTTP contract atomically with this domain.
func NewHandlerWithClaims(rules couponport.RuleApplication, options productport.ProductOptionReader, claims couponport.CouponClaimAdminReader, security RequestSecurity) (*Handler, error) {
	h, err := NewHandler(rules, options, security)
	if err != nil || claims == nil {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("coupon claim reader is required")
	}
	h.claims = claims
	return h, nil
}

// NewHandlerWithClaimsAndPublic preserves the frozen admin GET share
// interaction. The host authenticates and CSRF-checks that user gesture before
// the Coupon application atomically creates its first stable public slug.
func NewHandlerWithClaimsAndPublic(rules couponport.RuleApplication, options productport.ProductOptionReader, claims couponport.CouponClaimAdminReader, public couponport.PublicCouponApplication, security RequestSecurity) (*Handler, error) {
	h, err := NewHandlerWithClaims(rules, options, claims, security)
	if err != nil || public == nil {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("coupon public application is required")
	}
	h.public = public
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/coupons"), "/")
	if tail == "product-options" {
		h.productOptions(w, r)
		return
	}
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			if h.read(w, r) {
				h.list(w, r)
			}
		case http.MethodPost:
			if p, ok := h.mutate(w, r); ok {
				h.upsert(w, r, p, 0)
			}
		default:
			method(w, "GET, POST")
		}
		return
	}
	parts := strings.Split(tail, "/")
	if len(parts) > 2 || len(parts) == 0 {
		writeError(w, 404, "not_found")
		return
	}
	id, ok := parseID(parts[0])
	if !ok {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if h.read(w, r) {
				h.detail(w, r, couponport.ID(id))
			}
		case http.MethodPut:
			if p, ok := h.mutate(w, r); ok {
				h.upsert(w, r, p, couponport.ID(id))
			}
		case http.MethodDelete:
			if p, ok := h.mutate(w, r); ok {
				// The admin's destructive control means an auditable archive. A
				// physical draft delete cannot preserve the rule identity required
				// by claim, redemption, and order history.
				h.command(w, r, p, couponport.ID(id), "archive")
			}
		default:
			method(w, "GET, PUT, DELETE")
		}
		return
	}
	switch parts[1] {
	case "share":
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		p, ok := h.mutate(w, r)
		if ok {
			h.share(w, r, p, couponport.ID(id))
		}
	case "claims":
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if h.read(w, r) {
			h.claimList(w, r, couponport.ID(id))
		}
	case "publish", "stop", "archive", "copy":
		if r.Method != http.MethodPost {
			method(w, "POST")
			return
		}
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		h.command(w, r, p, couponport.ID(id), parts[1])
	default:
		writeError(w, 404, "not_found")
	}
}

func (h *Handler) share(w http.ResponseWriter, r *http.Request, principal accessdomain.Principal, couponID couponport.ID) {
	if h.public == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	share, err := h.public.EnsurePublicShare(r.Context(), couponID, principal.InternalID)
	if err != nil {
		resultError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "coupon_id": share.CouponID, "public_slug": share.PublicSlug, "url": share.URL, "available": true, "public_route_ready": true, "real_external_call_executed": false})
}

func (h *Handler) claimList(w http.ResponseWriter, r *http.Request, couponID couponport.ID) {
	if h.claims == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	q := r.URL.Query()
	if !only(q, "limit", "offset") {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limit, ok := intQuery(q.Get("limit"), 100, 1, 100)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	offset, ok := intQuery(q.Get("offset"), 0, 0, couponapp.MaximumOffset)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.claims.ListCouponClaims(r.Context(), couponID, int32(limit), int32(offset))
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{"claim_id": item.ClaimID, "customer_id": item.CustomerID, "coupon_id": item.CouponID, "status": item.Status, "claim_no_masked": item.ClaimNoMasked, "claimed_at": item.ClaimedAt.Format(time.RFC3339), "valid_from": nullableTime(item.ValidFrom), "valid_until": nullableTime(item.ValidUntil), "redeemed_at": nullableTime(item.RedeemedAt)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "coupon_id": couponID, "claims": items, "items": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return false
	}
	if !canRead(p) {
		writeError(w, 403, "forbidden")
		return false
	}
	return true
}
func (h *Handler) mutate(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !canWrite(p) {
		writeError(w, 403, "forbidden")
		return accessdomain.Principal{}, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, 403, "csrf_required")
		return accessdomain.Principal{}, false
	}
	return p, true
}
func canRead(p accessdomain.Principal) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range p.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func canWrite(p accessdomain.Principal) bool {
	if !canRead(p) {
		return false
	}
	for _, role := range p.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !only(q, "limit", "offset", "q", "status") {
		writeError(w, 400, "invalid_request")
		return
	}
	limit, ok := intQuery(q.Get("limit"), couponapp.DefaultLimit, 1, couponapp.MaximumLimit)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	offset, ok := intQuery(q.Get("offset"), 0, 0, couponapp.MaximumOffset)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	page, err := h.rules.List(r.Context(), int32(limit), int32(offset), q.Get("q"), q.Get("status"))
	if err != nil {
		resultError(w, err)
		return
	}
	targets, err := h.targetPresentations(r.Context(), page.Items)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	items, ok := couponList(page.Items, targets)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "coupons": items, "items": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}
func (h *Handler) detail(w http.ResponseWriter, r *http.Request, id couponport.ID) {
	c, err := h.rules.Get(r.Context(), id)
	if err != nil {
		resultError(w, err)
		return
	}
	targets, err := h.targetPresentations(r.Context(), []couponport.Coupon{c})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	v, ok := couponProjection(c, targets)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "data": map[string]any{"coupon": v}})
}

type upsertRequest struct {
	Name                 string   `json:"name"`
	DiscountAmountTotal  int64    `json:"discount_amount_total"`
	TotalIssueLimit      int64    `json:"total_issue_limit"`
	PerUserIssueLimit    *int64   `json:"per_user_issue_limit"`
	ClaimStartsAt        string   `json:"claim_starts_at"`
	ClaimEndsAt          string   `json:"claim_ends_at"`
	ValidityMode         string   `json:"validity_mode"`
	UseStartsAt          *string  `json:"use_starts_at"`
	UseEndsAt            *string  `json:"use_ends_at"`
	RelativeValidityDays *int32   `json:"relative_validity_days"`
	Instructions         *string  `json:"instructions"`
	TargetRefs           []string `json:"target_refs"`
}

func (h *Handler) upsert(w http.ResponseWriter, r *http.Request, p accessdomain.Principal, id couponport.ID) {
	var req upsertRequest
	if decode(r, &req) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	start, err := time.Parse(time.RFC3339, req.ClaimStartsAt)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	end, err := time.Parse(time.RFC3339, req.ClaimEndsAt)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	var useStart, useEnd *time.Time
	if req.UseStartsAt != nil {
		v, e := time.Parse(time.RFC3339, *req.UseStartsAt)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		useStart = &v
	}
	if req.UseEndsAt != nil {
		v, e := time.Parse(time.RFC3339, *req.UseEndsAt)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		useEnd = &v
	}
	perUser := int64(1)
	if req.PerUserIssueLimit != nil {
		perUser = *req.PerUserIssueLimit
	}
	instructions := ""
	if req.Instructions != nil {
		instructions = *req.Instructions
	}
	cmd := couponport.UpsertCommand{Coupon: couponport.Coupon{ID: id, Name: req.Name, DiscountAmountTotal: req.DiscountAmountTotal, TotalIssueLimit: req.TotalIssueLimit, PerUserIssueLimit: perUser, ClaimStartsAt: start, ClaimEndsAt: end, ValidityMode: couponport.ValidityMode(req.ValidityMode), UseStartsAt: useStart, UseEndsAt: useEnd, RelativeValidityDays: req.RelativeValidityDays, Instructions: instructions, TargetRefs: req.TargetRefs}, Actor: p.InternalID, IdempotencyKey: key}
	var c couponport.Coupon
	if id == 0 {
		c, err = h.rules.Create(r.Context(), cmd)
	} else {
		c, err = h.rules.UpdateDraft(r.Context(), cmd)
	}
	if err != nil {
		resultError(w, err)
		return
	}
	v := legacyCoupon(c)
	if id == 0 {
		writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "coupon_id": c.ID, "fallback_used": false, "create_replay_safe": true, "real_external_call_executed": false})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "fallback_used": false, "real_external_call_executed": false})
}
func (h *Handler) command(w http.ResponseWriter, r *http.Request, p accessdomain.Principal, id couponport.ID, operation string) {
	expectedVersion := int64(0)
	if operation == "archive" {
		var body struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if decode(r, &body) != nil || body.ExpectedVersion < 1 {
			writeError(w, 400, "invalid_request")
			return
		}
		expectedVersion = body.ExpectedVersion
	} else if decodeOptionalJSON(r, &struct{}{}) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	var c couponport.Coupon
	switch operation {
	case "publish":
		c, err = h.rules.Publish(r.Context(), id, p.InternalID, key)
	case "stop":
		c, err = h.rules.Stop(r.Context(), id, p.InternalID, key)
	case "archive":
		c, err = h.rules.Archive(r.Context(), id, expectedVersion, p.InternalID, key)
	case "copy":
		c, err = h.rules.Copy(r.Context(), id, p.InternalID, key)
	case "delete":
		c, err = h.rules.Delete(r.Context(), id, p.InternalID, key)
	}
	if err != nil {
		resultError(w, err)
		return
	}
	v := legacyCoupon(c)
	if operation == "delete" {
		writeJSON(w, 200, map[string]any{"ok": true, "coupon": v})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "fallback_used": false, "real_external_call_executed": false})
}
func (h *Handler) productOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	q := r.URL.Query()
	if !only(q, "q", "product_type", "limit", "offset") {
		writeError(w, 400, "invalid_request")
		return
	}
	kind := q.Get("product_type")
	if kind == "" {
		kind = "all"
	}
	if kind != "all" && kind != "standard_product" && kind != "service_period" {
		writeError(w, 400, "invalid_request")
		return
	}
	limit, ok := intQuery(q.Get("limit"), 50, 1, 100)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	offset, ok := intQuery(q.Get("offset"), 0, 0, couponapp.MaximumOffset)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	productType := productport.ProductOptionAll
	if kind == "standard_product" {
		productType = productport.ProductOptionStandard
	}
	if kind == "service_period" {
		productType = productport.ProductOptionServicePeriod
	}
	page, err := h.options.ListProductOptions(r.Context(), productport.ProductOptionQuery{Q: q.Get("q"), ProductType: productType, Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		writeError(w, 503, "unavailable")
		return
	}
	out := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		if item.ID < 1 || item.Name == "" || item.Currency != "CNY" || item.PriceMinor < 0 {
			writeError(w, 503, "unavailable")
			return
		}
		prefix := "standard_product"
		if item.ProductType == productport.ProductOptionServicePeriod {
			prefix = "service_period"
		}
		if item.ProductType != productport.ProductOptionStandard && item.ProductType != productport.ProductOptionServicePeriod {
			writeError(w, 503, "unavailable")
			return
		}
		out = append(out, map[string]any{"id": item.ID, "target_ref": prefix + ":" + strconv.FormatInt(int64(item.ID), 10), "name": item.Name, "title": item.Name, "product_type": prefix, "price_minor": item.PriceMinor, "price_cents": item.PriceMinor, "currency": "CNY"})
	}
	if page.Total < 0 || page.Limit != int32(limit) || page.Offset != int32(offset) {
		writeError(w, 503, "unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": out, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}

func legacyCoupon(c couponport.Coupon) map[string]any {
	return map[string]any{"id": c.ID, "resource_id": c.ID, "name": c.Name, "discount_amount_total": c.DiscountAmountTotal, "currency": "CNY", "status": c.Status, "availability_status": c.AvailabilityStatus, "total_issue_limit": c.TotalIssueLimit, "per_user_issue_limit": c.PerUserIssueLimit, "issued_count": c.IssuedCount, "rules_frozen": c.IssuedCount > 0, "claim_starts_at": c.ClaimStartsAt.Format(time.RFC3339), "claim_ends_at": c.ClaimEndsAt.Format(time.RFC3339), "validity_mode": c.ValidityMode, "use_starts_at": nullableTime(c.UseStartsAt), "use_ends_at": nullableTime(c.UseEndsAt), "relative_validity_days": c.RelativeValidityDays, "instructions": c.Instructions, "target_refs": c.TargetRefs, "created_by": c.CreatedBy, "updated_by": c.UpdatedBy, "version": c.Version, "created_at": c.CreatedAt.Format(time.RFC3339), "updated_at": c.UpdatedAt.Format(time.RFC3339)}
}

type targetPresentation struct {
	TargetRef string
	Name      string
	State     string
}

func (h *Handler) targetPresentations(ctx context.Context, coupons []couponport.Coupon) (map[string]targetPresentation, error) {
	if h == nil || h.targets == nil {
		return nil, errors.New("coupon product target reader is required")
	}
	references := make([]productport.ProductTargetReference, 0)
	keys := make([]string, 0)
	seen := make(map[string]struct{})
	for _, coupon := range coupons {
		for _, targetRef := range coupon.TargetRefs {
			if _, duplicate := seen[targetRef]; duplicate {
				continue
			}
			reference, ok := couponTargetReference(targetRef)
			if !ok {
				return nil, errors.New("invalid stored coupon target")
			}
			seen[targetRef] = struct{}{}
			references = append(references, reference)
			keys = append(keys, targetRef)
		}
	}
	if len(references) > productport.ProductTargetBatchMaximum {
		return nil, errors.New("coupon product target page exceeds Product read bound")
	}
	resolved := make(map[string]targetPresentation, len(references))
	lookups, err := h.targets.ReadProductTargets(ctx, references)
	if err != nil || len(lookups) != len(references) {
		return nil, errors.New("coupon product target projection unavailable")
	}
	for index, lookup := range lookups {
		expected := references[index]
		if lookup.Reference != expected {
			return nil, errors.New("coupon product target projection mismatched")
		}
		key := keys[index]
		if lookup.Found {
			if strings.TrimSpace(lookup.Name) == "" {
				return nil, errors.New("coupon product target projection invalid")
			}
			resolved[key] = targetPresentation{TargetRef: key, Name: lookup.Name, State: "available"}
			continue
		}
		resolved[key] = targetPresentation{TargetRef: key, State: "not_found"}
	}
	return resolved, nil
}

func couponTargetReference(value string) (productport.ProductTargetReference, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || (parts[0] != "standard_product" && parts[0] != "service_period") {
		return productport.ProductTargetReference{}, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != parts[1] {
		return productport.ProductTargetReference{}, false
	}
	kind := productport.ProductOptionStandard
	if parts[0] == "service_period" {
		kind = productport.ProductOptionServicePeriod
	}
	return productport.ProductTargetReference{ProductType: kind, ID: productport.ID(id)}, true
}

func couponProjection(c couponport.Coupon, targets map[string]targetPresentation) (map[string]any, bool) {
	value := legacyCoupon(c)
	items := make([]any, 0, len(c.TargetRefs))
	for _, ref := range c.TargetRefs {
		target, ok := targets[ref]
		if !ok || target.TargetRef != ref || (target.State != "available" && target.State != "not_found") {
			return nil, false
		}
		items = append(items, map[string]any{"target_ref": target.TargetRef, "name": target.Name, "state": target.State})
	}
	value["target_products"] = items
	return value, true
}

func couponList(items []couponport.Coupon, targets map[string]targetPresentation) ([]any, bool) {
	out := make([]any, 0, len(items))
	for _, c := range items {
		value, ok := couponProjection(c, targets)
		if !ok {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}
func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.Format(time.RFC3339)
}
func parseID(raw string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}
func intQuery(raw string, fallback, minimum, maximum int32) (int32, bool) {
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	return int32(n), err == nil && n >= int64(minimum) && n <= int64(maximum) && strconv.FormatInt(n, 10) == raw
}
func only(q map[string][]string, keys ...string) bool {
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return false
		}
	}
	return true
}
func decode(r *http.Request, target any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func decodeOptionalJSON(r *http.Request, target any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", errors.New("duplicate idempotency key")
	}
	if len(values) == 1 {
		if strings.TrimSpace(values[0]) != values[0] || len(values[0]) < 16 || len(values[0]) > 128 {
			return "", errors.New("invalid idempotency key")
		}
		return values[0], nil
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "server_compat_" + hex.EncodeToString(raw[:]), nil
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, couponapp.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, couponapp.ErrConflict), errors.Is(err, couponapp.ErrRulesFrozen):
		writeError(w, 409, "conflict")
	case errors.Is(err, couponapp.ErrInvalidCoupon), errors.Is(err, couponapp.ErrInvalidTarget):
		writeError(w, 400, "invalid_request")
	default:
		writeError(w, 503, "unavailable")
	}
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "method_not_allowed")
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code, "code": code, "message": code})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
