package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	hxcapp "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

type Authenticator interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type Handler struct {
	Service hxcapp.Service
	Store   *hxcstore.PostgreSQL
	Auth    Authenticator
	Key     []byte
	Now     func() time.Time
}

func (h Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/hxc-dashboard/shares", h.shares)
	mux.HandleFunc("POST /api/admin/hxc-dashboard/shares", h.shares)
	mux.HandleFunc("DELETE /api/admin/hxc-dashboard/shares", h.shares)
	mux.HandleFunc("POST /api/public/hxc-dashboard/query", h.publicQuery)
	mux.HandleFunc("GET /api/admin/hxc-dashboard/views", h.views)
	mux.HandleFunc("POST /api/admin/hxc-dashboard/views", h.views)
	mux.HandleFunc("DELETE /api/admin/hxc-dashboard/views", h.views)
	mux.HandleFunc("GET /api/admin/hxc-dashboard/summary", h.summary)
	mux.HandleFunc("POST /api/admin/hxc-dashboard/query", h.query)
	mux.HandleFunc("POST /api/admin/hxc-dashboard/refreshes", h.refresh)
	mux.HandleFunc("GET /api/admin/hxc-dashboard/refreshes/{run_id}", h.getRefresh)
	return mux
}
func (h Handler) authenticate(r *http.Request) (accessdomain.Principal, error) {
	p, err := h.Auth.Authenticate(r.Context(), r)
	if err == nil && p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff {
		return p, accessdomain.ErrPermissionDenied
	}
	return p, err
}
func (h Handler) summary(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authenticate(r); err != nil {
		writeError(w, err)
		return
	}
	s, err := h.Store.Summary(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	age := now.Sub(s.PublishedAt)
	freshness := "fresh"
	if age > 8*time.Hour {
		freshness = "stale"
	}
	writeJSON(w, 200, map[string]any{"projection_id": s.ID, "rule_version": s.RuleVersion, "projection_as_of": s.AsOf, "source_watermark": s.Watermark, "published_at": s.PublishedAt, "freshness": freshness, "age_seconds": int64(age.Seconds()), "source_digest": hex.EncodeToString(s.SourceDigest[:]), "projection_digest": hex.EncodeToString(s.ProjectionDigest[:]), "counts": map[string]int64{"total": s.Counts.Total, "active_used": s.Counts.ActiveUsed, "active_unused": s.Counts.ActiveUnused, "registered_no_active_membership": s.Counts.RegisteredNoActiveMembership, "matched": s.Counts.Matched, "unmatched": s.Counts.Unmatched, "conflict": s.Counts.Conflict, "matched_by_unionid": s.Counts.MatchedByUnionID, "matched_by_phone": s.Counts.MatchedByPhone, "matched_by_both": s.Counts.MatchedByBoth, "pending_observation": s.Counts.PendingObservation, "invalid_identity": s.Counts.InvalidIdentity}})
}

type queryRequest struct {
	ProjectionID   int64        `json:"projection_id,omitempty"`
	Filters        queryFilters `json:"filters"`
	ExactHXCUserID string       `json:"exact_hxc_user_id,omitempty"`
	Sort           string       `json:"sort,omitempty"`
	GroupBy        string       `json:"group_by,omitempty"`
	Cursor         string       `json:"cursor,omitempty"`
	Limit          int          `json:"limit,omitempty"`
}

type queryFilters struct {
	Stage            []string `json:"stage,omitempty"`
	SubscriptionTier []string `json:"subscription_tier,omitempty"`
	LastCapability   []string `json:"last_capability,omitempty"`
	BusinessStage    []string `json:"business_stage,omitempty"`
	UserSegment      []string `json:"user_segment,omitempty"`
	IdentityState    []string `json:"identity_state,omitempty"`
	MatchedBy        []string `json:"matched_by,omitempty"`
	IdentityReason   []string `json:"identity_reason_code,omitempty"`
}
type cursor struct {
	ProjectionID int64  `json:"p"`
	Offset       int    `json:"o"`
	QueryHash    string `json:"q"`
}

func (h Handler) query(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authenticate(r); err != nil {
		writeError(w, err)
		return
	}
	var request queryRequest
	if err := decode(r, &request); err != nil {
		writeError(w, err)
		return
	}
	if request.Limit == 0 {
		request.Limit = 50
	}
	if !validQuery(request) {
		writeError(w, errors.New("invalid_query"))
		return
	}
	summary, err := h.Store.Summary(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	projectionID, offset := summary.ID, 0
	if request.ProjectionID > 0 {
		projectionID = request.ProjectionID
	}
	if request.Cursor != "" {
		value, err := h.verifyCursor(request.Cursor)
		if err != nil || value.ProjectionID != projectionID || value.QueryHash != queryFingerprint(request, "internal") {
			writeError(w, errors.New("invalid_cursor"))
			return
		}
		offset = value.Offset
	}
	q := hxcstore.Query{ProjectionID: projectionID, Stages: request.Filters.Stage, SubscriptionTiers: cleanValues(request.Filters.SubscriptionTier), LastCapabilities: cleanValues(request.Filters.LastCapability), BusinessStages: cleanValues(request.Filters.BusinessStage), UserSegments: cleanValues(request.Filters.UserSegment), IdentityStates: request.Filters.IdentityState, MatchedBy: request.Filters.MatchedBy, IdentityReasonCodes: request.Filters.IdentityReason, Sort: request.Sort, GroupBy: request.GroupBy, Limit: request.Limit, Offset: offset}
	if request.ExactHXCUserID != "" {
		digest, _, err := domain.Subject(h.Key, request.ExactHXCUserID)
		if err != nil {
			writeError(w, errors.New("invalid_search"))
			return
		}
		q.SubjectDigest = digest[:]
	}
	result, err := h.Store.QueryWorkspace(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	items, groups, more, metrics, tiers := result.Items, result.Groups, result.More, result.Metrics, result.Tiers
	next := ""
	if more {
		next = h.signCursor(cursor{ProjectionID: projectionID, Offset: offset + len(items), QueryHash: queryFingerprint(request, "internal")})
	}
	writeJSON(w, 200, map[string]any{"projection_id": projectionID, "items": items, "groups": groups, "next_cursor": next, "total": metrics["total"], "metrics": metrics, "tiers": tiers})
}
func (h Handler) refresh(w http.ResponseWriter, r *http.Request) {
	principal, err := h.Auth.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !dashboardRefreshWriteRole(principal) {
		writeError(w, accessdomain.ErrPermissionDenied)
		return
	}
	if r.Body != nil && r.ContentLength > 0 {
		writeError(w, errors.New("body_not_allowed"))
		return
	}
	raw := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if _, err = idempotency.Parse(raw); err != nil {
		writeError(w, err)
		return
	}
	digest := sha256.Sum256([]byte(raw))
	run, replay, err := h.Service.Create(r.Context(), "manual:"+hex.EncodeToString(digest[:]), "manual", principal.InternalID)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusAccepted
	if replay {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"run": run, "replayed": replay})
}

func dashboardRefreshWriteRole(principal accessdomain.Principal) bool {
	if principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func (h Handler) getRefresh(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authenticate(r); err != nil {
		writeError(w, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("run_id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, errors.New("invalid_id"))
		return
	}
	run, err := h.Service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, run)
}
func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid_json")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid_json")
	}
	return nil
}
func validValues(values, allowed []string) bool {
	set := map[string]bool{}
	for _, v := range allowed {
		set[v] = true
	}
	for _, v := range values {
		if !set[v] {
			return false
		}
	}
	return len(values) <= 20
}
func cleanValues(values []string) []string {
	if len(values) > 20 {
		return []string{"__invalid__"}
	}
	for _, v := range values {
		if strings.TrimSpace(v) != v || v == "" || len(v) > 100 {
			return []string{"__invalid__"}
		}
	}
	return values
}
func validFreeform(values []string) bool {
	if len(values) > 20 {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) != value || value == "" || len(value) > 100 {
			return false
		}
	}
	return true
}
func validSort(v string) bool {
	return map[string]bool{"": true, "last_used_at_desc": true, "source_updated_at_desc": true, "subscription_expires_at_asc": true, "subscription_expires_at_desc": true, "messages_7d_desc": true}[v]
}
func validGroup(v string) bool {
	return map[string]bool{"": true, "stage": true, "subscription_tier": true, "last_capability": true, "business_stage": true, "user_segment": true, "identity_state": true, "matched_by": true, "identity_reason_code": true}[v]
}
func (h Handler) signCursor(c cursor) string {
	raw, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, h.Key)
	mac.Write([]byte("hxc-cursor-v1\x00"))
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (h Handler) verifyCursor(value string) (cursor, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursor{}, errors.New("invalid_cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor{}, err
	}
	mac := hmac.New(sha256.New, h.Key)
	mac.Write([]byte("hxc-cursor-v1\x00"))
	mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor{}, errors.New("invalid_cursor")
	}
	var c cursor
	if json.Unmarshal(raw, &c) != nil || c.ProjectionID < 1 || c.Offset < 0 || c.Offset > 10000000 {
		return cursor{}, errors.New("invalid_cursor")
	}
	return c, nil
}
func writeError(w http.ResponseWriter, err error) {
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, accessdomain.ErrAuthentication):
		status, code = 401, "authentication_required"
	case errors.Is(err, accessdomain.ErrCSRFRequired):
		status, code = 403, "csrf_required"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, hxcstore.ErrNotFound):
		status, code = 503, "hxc_dashboard_unavailable"
	case errors.Is(err, hxcapp.ErrConflict):
		status, code = 409, "refresh_already_active"
	case errors.Is(err, hxcapp.ErrNotReady):
		status, code = 503, "hxc_sync_disabled"
	case errors.Is(err, idempotency.ErrInvalidKey) || strings.HasPrefix(err.Error(), "invalid_") || err.Error() == "body_not_allowed":
		status, code = 400, "invalid_request"
	}
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func validQuery(request queryRequest) bool {
	if request.Limit == 0 {
		request.Limit = 50
	}
	return !(request.Limit < 1 || request.Limit > 100 || !validValues(request.Filters.Stage, []string{"active_used", "active_unused", "registered_no_active_membership"}) || !validValues(request.Filters.IdentityState, []string{"matched", "unmatched", "conflict"}) || !validValues(request.Filters.MatchedBy, []string{"none", "unionid", "phone", "both"}) || !validValues(request.Filters.IdentityReason, []string{"matched_unionid", "matched_phone", "matched_both", "no_match", "missing_identity", "invalid_unionid", "invalid_phone", "duplicate_hxc_unionid", "duplicate_hxc_phone", "duplicate_hxc_customer", "identity_multiple_roots", "unionid_phone_cross_root", "concurrent_identity_conflict"}) || !validFreeform(request.Filters.SubscriptionTier) || !validFreeform(request.Filters.LastCapability) || !validFreeform(request.Filters.BusinessStage) || !validFreeform(request.Filters.UserSegment) || len(request.ExactHXCUserID) > 255 || !validSort(request.Sort) || !validGroup(request.GroupBy))
}

func queryFingerprint(q queryRequest, scope string) string {
	q.Cursor = ""
	q.ProjectionID = 0
	data, _ := json.Marshal(q)
	sum := sha256.Sum256(append([]byte(scope+"\x00"), data...))
	return hex.EncodeToString(sum[:])
}
