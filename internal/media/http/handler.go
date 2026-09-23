// Package http exposes Media-owned admin compatibility routes. It never
// delegates a write to a provider: every response is a local material fact.
package http

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type Handler struct {
	service             mediaapp.HTTPFacade
	security            RequestSecurity
	materialSources     outboundport.MaterialSourceReader
	materialStatus      outboundport.MaterialStatusReader
	materialPreparer    outboundport.MaterialPreparer
	materialRefresher   outboundport.MaterialRefresher
	materialScopeDigest string
}

func NewHandler(service mediaapp.HTTPFacade, security RequestSecurity) (*Handler, error) {
	if service == nil || security == nil {
		return nil, errors.New("media HTTP dependencies are required")
	}
	return &Handler{service: service, security: security}, nil
}

// BindMaterialPreparation adds only stable Outbound ports to the Media admin
// surface. Media never imports an Outbound store, provider, worker, or EER
// implementation, and it never receives the raw corporation credential.
func (h *Handler) BindMaterialPreparation(sources outboundport.MaterialSourceReader, status outboundport.MaterialStatusReader, preparer outboundport.MaterialPreparer, refresher outboundport.MaterialRefresher, scopeDigest string) error {
	if h == nil || sources == nil || status == nil || preparer == nil || refresher == nil || strings.TrimSpace(scopeDigest) == "" {
		return errors.New("media preparation dependencies are required")
	}
	h.materialSources = sources
	h.materialStatus = status
	h.materialPreparer = preparer
	h.materialRefresher = refresher
	h.materialScopeDigest = scopeDigest
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil || h.security == nil {
		writeError(w, 503, "unavailable")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/"), "/")
	switch {
	case path == "media-preparations" || strings.HasPrefix(path, "media-preparations/"):
		h.materialPreparations(w, r, strings.Trim(strings.TrimPrefix(path, "media-preparations"), "/"))
	case path == "image-library" || strings.HasPrefix(path, "image-library/"):
		h.images(w, r, strings.Trim(strings.TrimPrefix(path, "image-library"), "/"))
	case path == "attachment-library" || strings.HasPrefix(path, "attachment-library/"):
		h.attachments(w, r, strings.Trim(strings.TrimPrefix(path, "attachment-library"), "/"))
	case path == "miniprogram-library" || strings.HasPrefix(path, "miniprogram-library/"):
		h.miniprograms(w, r, strings.Trim(strings.TrimPrefix(path, "miniprogram-library"), "/"))
	case path == "group-invite-library" || strings.HasPrefix(path, "group-invite-library/"):
		h.groups(w, r, strings.Trim(strings.TrimPrefix(path, "group-invite-library"), "/"))
	default:
		writeError(w, 404, "not_found")
	}
}

func (h *Handler) materialPreparations(w http.ResponseWriter, r *http.Request, tail string) {
	if h.materialSources == nil || h.materialStatus == nil || h.materialPreparer == nil || h.materialRefresher == nil || h.materialScopeDigest == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if tail == "" && r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 500 {
				invalidQuery(w)
				return
			}
			limit = parsed
		}
		cursor, err := scalarQuery(r, "cursor")
		if err != nil {
			invalidQuery(w)
			return
		}
		page, err := h.materialSources.ListEnabledSourceSnapshots(r.Context(), outboundport.MaterialSnapshotPageRequest{CorpScopeDigest: h.materialScopeDigest, Cursor: cursor, Limit: limit})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		items := make([]map[string]any, 0, len(page.Items))
		for _, source := range page.Items {
			status, statusErr := h.materialStatus.GetMaterialStatus(r.Context(), source, h.materialScopeDigest)
			if statusErr != nil {
				writeError(w, http.StatusServiceUnavailable, "unavailable")
				return
			}
			items = append(items, materialProjection(source, status))
		}
		// A scan failure is still a source-owned, actionable catalog entry.  It
		// cannot safely be represented as a material snapshot because no bytes,
		// digest, or metadata are available to upload.  Keep it separately so the
		// administrator can locate and replace the original source.
		failures := make([]map[string]any, 0, len(page.Failures))
		for _, failure := range page.Failures {
			failures = append(failures, map[string]any{
				"source_ref":        failure.SourceRef,
				"failure_code":      failure.FailureCode,
				"state":             "final_failed",
				"credential_state":  "missing",
				"credential_usable": false,
			})
		}
		today, exists, err := h.materialRefresher.GetTodayRefreshRound(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		var todayProjection any
		if exists {
			todayProjection = refreshRoundProjection(today)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items, "failures": failures, "today_refresh_round": todayProjection, "next_refresh_at": nextRefreshAt(), "next_cursor": page.NextCursor, "done": page.Done, "local_fact_only": false})
		return
	}
	if tail == "refresh-rounds" && r.Method == http.MethodPost {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var input struct {
			Force bool `json:"force"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || !input.Force {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		localDate := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
		round, err := h.materialRefresher.RefreshAll(r.Context(), outboundport.MaterialRefreshCommand{LocalDate: localDate, Force: input.Force, ActorAdminID: actor.InternalID, OperationKey: mutationKey(r)})
		if err != nil {
			writeMaterialPreparationError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "refresh_round": refreshRoundProjection(round)})
		return
	}
	if strings.HasPrefix(tail, "refresh-rounds/") && r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		id, err := id(strings.TrimPrefix(tail, "refresh-rounds/"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		round, err := h.materialRefresher.GetRefreshRound(r.Context(), id)
		if err != nil {
			writeMaterialPreparationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "refresh_round": refreshRoundProjection(round)})
		return
	}
	if strings.HasSuffix(tail, "/prepare") && r.Method == http.MethodPost {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		sourceRef := strings.TrimSuffix(tail, "/prepare")
		if !mediaport.ValidSourceRef(sourceRef) {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var input struct {
			Force bool `json:"force"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || !input.Force {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		source, err := h.materialSources.GetSourceSnapshot(r.Context(), sourceRef)
		if err != nil {
			if errors.Is(err, mediaport.ErrSourceNotFound) {
				writeError(w, http.StatusNotFound, "not_found")
			} else {
				writeError(w, http.StatusServiceUnavailable, "unavailable")
			}
			return
		}
		result, err := h.materialPreparer.Prepare(r.Context(), outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: h.materialScopeDigest, ForceRefresh: input.Force, ActorAdminID: actor.InternalID, OperationKey: mutationKey(r)})
		if err != nil {
			writeMaterialPreparationError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "material": materialProjection(source, result)})
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPost)
}

func materialProjection(source outboundport.MaterialSourceSnapshot, result outboundport.MaterialResult) map[string]any {
	return map[string]any{"source_ref": source.SourceRef, "source_type": source.SourceType, "content_digest": "sha256:" + hex.EncodeToString(source.ContentDigest[:]), "file_name": source.FileName, "media_type": source.MediaType, "size_bytes": source.SizeBytes, "snapshot_version": source.SnapshotVersion, "state": result.State, "credential_state": result.CredentialState, "credential_usable": result.CredentialUsable, "failure_code": result.FailureCode, "effect_id": result.EffectID, "media_id": result.MediaID, "source_digest": "sha256:" + hex.EncodeToString(result.SourceDigest[:]), "provider_created_at": optionalTime(result.ProviderCreatedAt), "last_succeeded_at": optionalTime(result.LastSucceededAt), "expires_at": optionalTime(result.ExpiresAt), "next_refresh_at": optionalTime(result.NextRefreshAt)}
}

func refreshRoundProjection(round outboundport.MaterialRefreshRound) map[string]any {
	return map[string]any{"id": round.ID, "local_date": round.LocalDate, "state": round.State, "cursor": round.Cursor, "total": round.Total, "queued": round.Queued, "succeeded": round.Succeeded, "failed": round.Failed, "unknown": round.Unknown, "started_at": optionalTime(round.StartedAt), "completed_at": round.CompletedAt}
}

func optionalTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

// writeMaterialPreparationError depends only on closed outbound port errors.
// It intentionally treats unknown errors as unavailable, never as not-found.
func writeMaterialPreparationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, outboundport.ErrInvalidMaterialRequest):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, outboundport.ErrMaterialOperationConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, outboundport.ErrMaterialPreparationNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func nextRefreshAt() time.Time {
	location := time.FixedZone("CST", 8*3600)
	now := time.Now().In(location)
	next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, location)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// download is deliberately stricter than metadata reads. A library viewer can
// use the existing safe catalogue but cannot export the underlying attachment.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) bool {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return false
	}
	if !writeRole(p) {
		writeError(w, 403, "permission_denied")
		return false
	}
	return true
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return false
	}
	if !readRole(p) {
		writeError(w, 403, "permission_denied")
		return false
	}
	return true
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !writeRole(p) {
		writeError(w, 403, "permission_denied")
		return accessdomain.Principal{}, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, 403, "csrf_required")
		return accessdomain.Principal{}, false
	}
	return p, true
}

// mutationKey preserves a supplied client key exactly. Frozen v2 Media
// controls omitted this header, so their compatibility route mints a fresh
// per-request key instead of accidentally coalescing independent clicks. The
// reserved prefix is recorded in Media audit facts by the store.
func mutationKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		return key
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "server_compat_fallback_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "server_compat_" + hex.EncodeToString(raw[:])
}
func readRole(p accessdomain.Principal) bool {
	return (p.Kind == accessdomain.KindAdmin || p.Kind == accessdomain.KindStaff) && p.InternalID > 0 && (hasRole(p, accessdomain.RoleViewer) || hasRole(p, accessdomain.RoleAdmin) || hasRole(p, accessdomain.RoleSuperAdmin))
}
func writeRole(p accessdomain.Principal) bool {
	return (p.Kind == accessdomain.KindAdmin || p.Kind == accessdomain.KindStaff) && p.InternalID > 0 && (hasRole(p, accessdomain.RoleAdmin) || hasRole(p, accessdomain.RoleSuperAdmin))
}
func hasRole(principal accessdomain.Principal, wanted accessdomain.Role) bool {
	for _, role := range principal.Roles {
		if role == wanted {
			return true
		}
	}
	return false
}
func method(w http.ResponseWriter, got, want string) bool {
	if got == want {
		return true
	}
	w.Header().Set("Allow", want)
	writeError(w, 405, "method_not_allowed")
	return false
}
func scalarQuery(r *http.Request, name string) (string, error) {
	q := r.URL.Query()
	values, exists := q[name]
	if !exists {
		return "", nil
	}
	if len(values) != 1 {
		return "", ErrInvalidQuery
	}
	return values[0], nil
}

// imagePage follows the frozen image-library parser.  Its normalizing rules
// intentionally differ from the other Media lists.
func imagePage(r *http.Request) (int, int, string, error) {
	limit := 100
	if raw, err := scalarQuery(r, "limit"); err != nil {
		return 0, 0, "", err
	} else if raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return 0, 0, "", ErrInvalidQuery
		}
		switch {
		case value == 0:
			limit = 100
		case value < 0:
			limit = 1
		case value > 500:
			limit = 500
		default:
			limit = int(value)
		}
	}
	offset := 0
	if raw, err := scalarQuery(r, "offset"); err != nil {
		return 0, 0, "", err
	} else if raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return 0, 0, "", ErrInvalidQuery
		}
		if value > 0 {
			offset = int(value)
		}
	}
	query, err := scalarQuery(r, "q")
	if err != nil {
		return 0, 0, "", err
	}
	return limit, offset, strings.TrimSpace(query), nil
}

func mediaPage(r *http.Request, defaultLimit, maxLimit int, defaultEnabled bool) (int, int, bool, string, error) {
	limit := defaultLimit
	if raw, err := scalarQuery(r, "limit"); err != nil {
		return 0, 0, false, "", err
	} else if raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 1 || value > int64(maxLimit) {
			return 0, 0, false, "", ErrInvalidQuery
		}
		limit = int(value)
	}
	offset := 0
	if raw, err := scalarQuery(r, "offset"); err != nil {
		return 0, 0, false, "", err
	} else if raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 0 {
			return 0, 0, false, "", ErrInvalidQuery
		}
		offset = int(value)
	}
	enabled := defaultEnabled
	if raw, err := scalarQuery(r, "enabled_only"); err != nil {
		return 0, 0, false, "", err
	} else if raw != "" {
		if raw != "true" && raw != "false" {
			return 0, 0, false, "", ErrInvalidQuery
		}
		enabled = raw == "true"
	}
	query, err := scalarQuery(r, "q")
	if err != nil {
		return 0, 0, false, "", err
	}
	return limit, offset, enabled, strings.TrimSpace(query), nil
}
func id(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "0") {
		return 0, errors.New("invalid id")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("invalid id")
		}
	}
	return strconv.ParseInt(value, 10, 64)
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, mediaapp.ErrHTTPNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, mediaapp.ErrHTTPConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, mediaapp.ErrHTTPReferences):
		writeError(w, 409, "has_references")
	case errors.Is(err, mediaapp.ErrHTTPReferenceReaderUnavailable):
		writeError(w, 503, "unavailable")
	case errors.Is(err, mediaapp.ErrHTTPInvalid), errors.Is(err, domain.ErrUnsafeImage), errors.Is(err, domain.ErrUnsafeAttachment):
		writeError(w, 400, "invalid_request")
	default:
		writeError(w, 503, "unavailable")
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	compat := map[string]string{"invalid_request": "MALFORMED_REQUEST", "not_found": "NOT_FOUND", "conflict": "CONFLICT", "idempotency_conflict": "CONFLICT", "has_references": "CONFLICT", "unauthorized": "UNAUTHORIZED", "permission_denied": "FORBIDDEN", "csrf_required": "FORBIDDEN", "unavailable": "DEPENDENCY_UNAVAILABLE", "method_not_allowed": "METHOD_NOT_ALLOWED"}[code]
	if compat == "" {
		compat = "DEPENDENCY_UNAVAILABLE"
	}
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	payload := map[string]any{"ok": false, "code": compat, "message": strings.ReplaceAll(strings.ToLower(compat), "_", " "), "request_id": "media_" + hex.EncodeToString(raw[:])}
	if code == "has_references" {
		payload["error"] = "material has local references"
		payload["references"] = map[string]any{"automation_agents": []int{}, "channels": []int{}, "radar_links": []int{}}
	}
	writeJSON(w, status, payload)
}

func writeReferenceConflict(w http.ResponseWriter, kind string, ids map[string][]int64) {
	references := make(map[string]any, len(ids))
	for classification, values := range ids {
		items := make([]map[string]int64, 0, len(values))
		for _, value := range values {
			items = append(items, map[string]int64{"id": value})
		}
		references[classification] = items
	}
	writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "code": "CONFLICT", "error": kind + "_has_references", "references": references, "source_status": "local_delete", "route_owner": "ai_crm_next", "fallback_used": false, "real_external_call_executed": false, "storage_adapter_mode": "postgresql", "adapter_mode": "postgresql"})
}

func (h *Handler) referenceConflict(w http.ResponseWriter, err error, kind string) bool {
	if !errors.Is(err, mediaapp.ErrHTTPReferences) {
		return false
	}
	if references, ok := h.service.ReferenceConflict(err); ok {
		writeReferenceConflict(w, kind, references)
		return true
	}
	resultError(w, mediaapp.ErrHTTPReferenceReaderUnavailable)
	return true
}

func invalidQuery(w http.ResponseWriter) {
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"code": "VALIDATION_FAILED", "message": "Validation failed.", "request_id": "media_" + hex.EncodeToString(raw[:])})
}

func (h *Handler) images(w http.ResponseWriter, r *http.Request, tail string) {
	if h.materialGroupRoute(w, r, "image", tail) {
		return
	}
	if tail == "" {
		if r.Method == http.MethodGet {
			if !h.read(w, r) {
				return
			}
			limit, offset, q, e := imagePage(r)
			if e != nil {
				invalidQuery(w)
				return
			}
			query, e := imageQuery(r, limit, offset, q)
			if e != nil {
				invalidQuery(w)
				return
			}
			items, total, e := h.service.ListImagesFiltered(r.Context(), query)
			if e != nil {
				resultError(w, e)
				return
			}
			next := any(nil)
			if offset+len(items) < total {
				next = offset + len(items)
			}
			writeJSON(w, 200, map[string]any{"ok": true, "items": items, "images": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "next_offset": next, "source_status": "next_media_library", "route_owner": "ai_crm_next", "fallback_used": false, "real_external_call_executed": false, "storage_adapter_mode": "postgresql", "adapter_mode": "postgresql", "local_fact_only": true})
			return
		}
		if r.Method == http.MethodPost {
			h.imageCreate(w, r, "local_repository_write")
			return
		}
		method(w, r.Method, http.MethodGet+", "+http.MethodPost)
		return
	}
	// The frozen Media client posts new image files to this historical alias.
	// Keep the alias server-owned; it is not an independently exposed page.
	if tail == "upload" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		h.imageCreate(w, r, "local_upload")
		return
	}
	if tail == "facets" {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return
		}
		categories, tags, e := h.service.ImageFacets(r.Context())
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "categories": categories, "tags": tags, "items": categories, "source_status": "next_media_library", "route_owner": "ai_crm_next", "fallback_used": false, "real_external_call_executed": false, "storage_adapter_mode": "postgresql", "adapter_mode": "postgresql"})
		return
	}
	parts := strings.Split(tail, "/")
	imageID, e := id(parts[0])
	if e != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 3 && parts[1] == "variants" {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return
		}
		key := parts[2]
		if !h.service.ValidImageVariant(key) {
			invalidQuery(w)
			return
		}
		variant, err := h.service.GetImageVariant(r.Context(), imageID, key)
		if errors.Is(err, mediaapp.ErrInvalidImageVariant) {
			invalidQuery(w)
			return
		}
		if errors.Is(err, mediaapp.ErrImageVariantNotFound) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			writeError(w, 503, "unavailable")
			return
		}
		w.Header().Set("ETag", variant.ETag)
		if r.Header.Get("If-None-Match") == variant.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", variant.MediaType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(variant.Content)
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, content, _, e := h.service.Image(r.Context(), imageID)
		if e != nil {
			resultError(w, e)
			return
		}
		if r.URL.Query().Get("include_data") == "true" {
			item["data_url"] = "data:" + item["mime_type"].(string) + ";base64," + base64.StdEncoding.EncodeToString(content)
		}
		if variant := r.URL.Query().Get("variant"); variant != "" {
			if !h.service.ValidImageVariant(variant) {
				invalidQuery(w)
				return
			}
			item["variant"] = variant
			item["variant_url"] = "/api/admin/image-library/" + strconv.FormatInt(imageID, 10) + "/variants/" + variant
		}
		writeJSON(w, 200, imageEnvelope(item))
		return
	}
	if r.Method == http.MethodPut {
		h.imageUpdate(w, r, imageID)
		return
	}
	if r.Method == http.MethodDelete {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		out, e := h.service.Delete(r.Context(), "image", imageID, actor.InternalID, mutationKey(r))
		if e != nil {
			if h.referenceConflict(w, e, "image") {
				return
			}
			resultError(w, e)
			return
		}
		out["ok"], out["local_only"], out["provider_call_executed"], out["real_external_call_executed"], out["references_cleared"] = true, true, false, false, map[string]any{"miniprograms_cleared": 0, "campaign_steps_cleared": 0}
		out["source_status"], out["route_owner"], out["fallback_used"], out["storage_adapter_mode"], out["adapter_mode"] = "local_delete", "ai_crm_next", false, "postgresql", "postgresql"
		writeJSON(w, 200, out)
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
}
func imageQuery(r *http.Request, limit, offset int, q string) (mediaapp.ImageQuery, error) {
	values := r.URL.Query()
	enabled := true
	if raw, exists := values["enabled_only"]; exists {
		if len(raw) != 1 || (raw[0] != "true" && raw[0] != "false") {
			return mediaapp.ImageQuery{}, ErrInvalidQuery
		}
		enabled = raw[0] == "true"
	}
	onlyUnlabeled := false
	if raw, exists := values["only_unlabeled"]; exists {
		if len(raw) != 1 || (raw[0] != "true" && raw[0] != "false") {
			return mediaapp.ImageQuery{}, ErrInvalidQuery
		}
		onlyUnlabeled = raw[0] == "true"
	}
	normalize := func(raw string) ([]string, error) {
		if raw == "" {
			return nil, nil
		}
		seen := map[string]bool{}
		out := []string{}
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if len([]rune(item)) > 64 {
				return nil, ErrInvalidQuery
			}
			if !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
		if len(out) > 50 {
			return nil, ErrInvalidQuery
		}
		return out, nil
	}
	tags, err := normalize(values.Get("tags"))
	if err != nil {
		return mediaapp.ImageQuery{}, err
	}
	groups := [][]string{}
	for _, raw := range values["tag_group"] {
		group, err := normalize(raw)
		if err != nil {
			return mediaapp.ImageQuery{}, err
		}
		if len(group) > 0 {
			groups = append(groups, group)
		}
	}
	return mediaapp.ImageQuery{Limit: limit, Offset: offset, EnabledOnly: enabled, Query: q, Category: strings.TrimSpace(values.Get("category")), Tags: tags, TagGroups: groups, OnlyUnlabeled: onlyUnlabeled, OnlyUngrouped: values.Get("only_ungrouped") == "true"}, nil
}

var ErrInvalidQuery = errors.New("invalid media query")

func (h *Handler) imageCreate(w http.ResponseWriter, r *http.Request, source string) {
	actor, ok := h.write(w, r)
	if !ok {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		h.imageCreateJSON(w, r, actor, source)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(11 << 20); err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	defer file.Close()
	content, err := domain.ReadBounded(file)
	if err != nil {
		resultError(w, err)
		return
	}
	inspection, err := domain.Inspect(header.Filename, header.Header.Get("Content-Type"), content)
	if err != nil {
		resultError(w, err)
		return
	}
	enabled := true
	if raw := r.FormValue("enabled"); raw != "" {
		enabled = raw == "true"
	}
	groupID, groupErr := optionalGroupID(r.FormValue("group_id"))
	if groupErr != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	out, err := h.service.CreateImage(r.Context(), actor.InternalID, mutationKey(r), mediaapp.ImageInput{FileName: header.Filename, MIME: inspection.MediaType, Name: nonempty(r.FormValue("name"), header.Filename), Description: r.FormValue("description"), Tags: r.FormValue("tags"), Category: r.FormValue("category"), GroupID: groupID, Content: content, Width: inspection.Width, Height: inspection.Height, Enabled: enabled})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, imageMutationEnvelope(out, source))
}

// imageCreateJSON preserves the canonical compatibility create contract. The
// frozen UI currently chooses the multipart alias, while non-UI callers may
// still use the documented data URL request.
func (h *Handler) imageCreateJSON(w http.ResponseWriter, r *http.Request, actor accessdomain.Principal, source string) {
	var body struct {
		DataURL     string   `json:"data_url"`
		FileName    string   `json:"file_name"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Category    string   `json:"category"`
		GroupID     *int64   `json:"group_id"`
		Enabled     *bool    `json:"enabled"`
	}
	if decodeLimit(r, &body, (domain.MaxImageBytes*4)/3+(1<<20)) != nil || strings.TrimSpace(body.FileName) == "" {
		writeError(w, 400, "invalid_request")
		return
	}
	mediaType, content, valid := canonicalImageDataURL(body.DataURL)
	if !valid {
		writeError(w, 400, "invalid_request")
		return
	}
	inspection, err := domain.Inspect(body.FileName, mediaType, content)
	if err != nil {
		resultError(w, err)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	out, err := h.service.CreateImage(r.Context(), actor.InternalID, mutationKey(r), mediaapp.ImageInput{FileName: body.FileName, MIME: inspection.MediaType, Name: nonempty(body.Name, body.FileName), Description: body.Description, Tags: strings.Join(body.Tags, ","), Category: body.Category, GroupID: body.GroupID, Content: content, Width: inspection.Width, Height: inspection.Height, Enabled: enabled})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, imageMutationEnvelope(out, source))
}

func canonicalImageDataURL(value string) (string, []byte, bool) {
	const prefixPNG = "data:image/png;base64,"
	const prefixJPEG = "data:image/jpeg;base64,"
	const prefixGIF = "data:image/gif;base64,"
	prefix := ""
	mediaType := ""
	switch {
	case strings.HasPrefix(value, prefixPNG):
		prefix, mediaType = prefixPNG, "image/png"
	case strings.HasPrefix(value, prefixJPEG):
		prefix, mediaType = prefixJPEG, "image/jpeg"
	case strings.HasPrefix(value, prefixGIF):
		prefix, mediaType = prefixGIF, "image/gif"
	default:
		return "", nil, false
	}
	encoded := strings.TrimPrefix(value, prefix)
	if encoded == "" || strings.ContainsAny(encoded, "\r\n") {
		return "", nil, false
	}
	content, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(content) != encoded {
		return "", nil, false
	}
	return mediaType, content, true
}
func (h *Handler) imageUpdate(w http.ResponseWriter, r *http.Request, imageID int64) {
	actor, ok := h.write(w, r)
	if !ok {
		return
	}
	var patch map[string]any
	if decode(r, &patch) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	if !allowedFields(patch, "name", "description", "tags", "category", "enabled", "group_id", "expected_version") {
		writeError(w, 400, "invalid_request")
		return
	}
	out, e := h.service.UpdateImage(r.Context(), imageID, actor.InternalID, mutationKey(r), patch)
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, imageMutationEnvelope(out, "local_repository_write"))
}
func nonempty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func imageEnvelope(item map[string]any) map[string]any {
	return map[string]any{"ok": true, "item": item, "image": item, "item_id": item["id"], "source_status": "next_media_library", "route_owner": "ai_crm_next", "fallback_used": false, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false, "storage_adapter_mode": "postgresql", "adapter_mode": "postgresql"}
}
func imageMutationEnvelope(item map[string]any, source string) map[string]any {
	if source == "local_upload" {
		copy := make(map[string]any, len(item))
		for key, value := range item {
			copy[key] = value
		}
		if tags, ok := item["tags"].([]string); ok {
			copy["tags"] = strings.Join(tags, ",")
		}
		item = copy
	}
	response := imageEnvelope(item)
	response["source_status"] = source
	return response
}

func (h *Handler) attachments(w http.ResponseWriter, r *http.Request, tail string) {
	if h.materialGroupRoute(w, r, "attachment", tail) {
		return
	}
	if strings.HasPrefix(tail, "uploads") {
		h.attachmentUpload(w, r, strings.Trim(strings.TrimPrefix(tail, "uploads"), "/"))
		return
	}
	if tail == "" || tail == "upload" {
		if r.Method == http.MethodGet && tail == "" {
			if !h.read(w, r) {
				return
			}
			limit, offset, enabled, q, e := mediaPage(r, 50, 500, false)
			if e != nil {
				invalidQuery(w)
				return
			}
			group, e := groupQuery(r)
			if e != nil {
				invalidQuery(w)
				return
			}
			var items []map[string]any
			var total int
			if group != nil {
				grouped, ok := h.service.(mediaapp.MaterialGrouping)
				if !ok {
					writeError(w, 503, "unavailable")
					return
				}
				items, total, e = grouped.ListAttachmentsInGroup(r.Context(), limit, offset, enabled, q, group)
			} else {
				items, total, e = h.service.ListAttachments(r.Context(), limit, offset, enabled, q)
			}

			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "items": items, "attachments": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
			return
		}
		if r.Method == http.MethodPost {
			h.attachmentCreate(w, r)
			return
		}
		method(w, r.Method, http.MethodGet+", "+http.MethodPost)
		return
	}
	parts := strings.Split(tail, "/")
	attachmentID, e := id(parts[0])
	if e != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && parts[1] == "download" {
		if !method(w, r.Method, http.MethodGet) || !h.download(w, r) {
			return
		}
		item, content, e := h.service.Attachment(r.Context(), attachmentID)
		if e != nil {
			resultError(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(item["file_name"].(string), `"`, "_")+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(content)
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, _, e := h.service.Attachment(r.Context(), attachmentID)
		if e != nil {
			resultError(w, e)
			return
		}
		response := map[string]any{}
		for key, value := range item {
			response[key] = value
		}
		response["item"] = item
		response["attachment"] = item
		response["ok"] = true
		response["local_only"] = true
		response["provider_call_executed"] = false
		response["real_external_call_executed"] = false
		writeJSON(w, 200, response)
		return
	}
	if r.Method == http.MethodPut {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var patch map[string]any
		if decode(r, &patch) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !allowedFields(patch, "name", "description", "tags", "enabled", "expected_version", "group_id") {
			writeError(w, 400, "invalid_request")
			return
		}
		out, e := h.service.UpdateAttachment(r.Context(), attachmentID, actor.InternalID, mutationKey(r), patch)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, attachmentEnvelope(out))
		return
	}
	if r.Method == http.MethodDelete {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		out, e := h.service.Delete(r.Context(), "attachment", attachmentID, actor.InternalID, mutationKey(r))
		if e != nil {
			if h.referenceConflict(w, e, "attachment") {
				return
			}
			resultError(w, e)
			return
		}
		out["ok"] = true
		out["item_id"] = attachmentID
		out["local_only"] = true
		out["provider_call_executed"] = false
		out["real_external_call_executed"] = false
		writeJSON(w, 200, out)
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
}
func (h *Handler) attachmentCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.write(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxAttachmentBytes+(1<<20))
	if r.ParseMultipartForm(11<<20) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	file, header, e := r.FormFile("attachment")
	if e != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	defer file.Close()
	content, e := domain.ReadAttachmentBounded(file)
	if e != nil {
		resultError(w, e)
		return
	}
	if _, e = domain.InspectAttachment(header.Filename, header.Header.Get("Content-Type"), content); e != nil {
		resultError(w, e)
		return
	}
	tags := splitCSV(r.FormValue("tags"))
	groupID, groupErr := optionalGroupID(r.FormValue("group_id"))
	if groupErr != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	out, e := h.service.CreateAttachment(r.Context(), actor.InternalID, mutationKey(r), mediaapp.AttachmentInput{FileName: header.Filename, Name: nonempty(r.FormValue("name"), header.Filename), Description: r.FormValue("description"), Tags: tags, GroupID: groupID, Content: content, Enabled: r.FormValue("enabled") != "false"})
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, attachmentEnvelope(out))
}

// attachmentUpload implements the frozen PDF uploader's init -> ordered parts
// -> complete protocol. Each transition has its own receipt/audit/outbox fact;
// no Provider is contacted and the completed bytes stay private to Media.
func (h *Handler) attachmentUpload(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var body struct {
			FileName    string `json:"file_name"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Size        int64  `json:"size"`
			SHA256      string `json:"sha256"`
			GroupID     *int64 `json:"group_id"`
			Enabled     *bool  `json:"enabled"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		uploadID, err := h.service.InitiateAttachmentUpload(r.Context(), actor.InternalID, mutationKey(r), mediaapp.AttachmentUploadInput{FileName: body.FileName, Name: body.Name, Description: body.Description, Size: body.Size, Digest: body.SHA256, Enabled: enabled, GroupID: body.GroupID})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"upload_id": uploadID, "local_fact_only": true, "provider_call_executed": false})
		return
	}
	parts := strings.Split(tail, "/")
	uploadID, err := id(parts[0])
	if err != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 3 && parts[1] == "parts" {
		if !method(w, r.Method, http.MethodPut) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		part, err := id(parts[2])
		if err != nil || part > 1<<31-1 {
			writeError(w, 404, "not_found")
			return
		}
		var body struct {
			SHA256  string `json:"sha256"`
			Content string `json:"content"`
		}
		if decodeLimit(r, &body, 2<<20) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		content, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if err = h.service.PutAttachmentUploadPart(r.Context(), uploadID, int(part), actor.InternalID, mutationKey(r), body.SHA256, content); err != nil {
			resultError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 2 && parts[1] == "complete" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		attachmentID, err := h.service.CompleteAttachmentUpload(r.Context(), uploadID, actor.InternalID, mutationKey(r))
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"attachment_id": attachmentID, "local_fact_only": true, "provider_call_executed": false})
		return
	}
	writeError(w, 404, "not_found")
}
func splitCSV(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, ",")
}

func (h *Handler) miniprograms(w http.ResponseWriter, r *http.Request, tail string) {
	if h.materialGroupRoute(w, r, "miniprogram", tail) {
		return
	}
	if tail == "" {
		if r.Method == http.MethodGet {
			if !h.read(w, r) {
				return
			}
			limit, offset, enabled, q, e := mediaPage(r, 100, 100, false)
			if e != nil {
				invalidQuery(w)
				return
			}
			group, e := groupQuery(r)
			if e != nil {
				invalidQuery(w)
				return
			}
			var items []map[string]any
			var total int
			if group != nil {
				grouped, ok := h.service.(mediaapp.MaterialGrouping)
				if !ok {
					writeError(w, 503, "unavailable")
					return
				}
				items, total, e = grouped.ListMiniProgramsInGroup(r.Context(), limit, offset, enabled, q, group)
			} else {
				items, total, e = h.service.ListMiniPrograms(r.Context(), limit, offset, enabled, q)
			}

			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "items": items, "miniprograms": items, "mini_programs": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
			return
		}
		if r.Method == http.MethodPost {
			actor, ok := h.write(w, r)
			if !ok {
				return
			}
			var body map[string]any
			if decode(r, &body) != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			if !allowedFields(body, "name", "appid", "app_id", "pagepath", "page_path", "title", "thumb_image_id", "enabled", "group_id", "expected_version") {
				writeError(w, 400, "invalid_request")
				return
			}
			out, e := h.service.CreateMiniProgram(r.Context(), actor.InternalID, mutationKey(r), body)
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, miniMutation(out))
			return
		}
		method(w, r.Method, http.MethodGet+", "+http.MethodPost)
		return
	}
	parts := strings.Split(tail, "/")
	resourceID, e := id(parts[0])
	if e != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && parts[1] == "test-resolve" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		resolution, err := h.service.ResolveMiniProgramThumbnail(r.Context(), resourceID, actor.InternalID, mutationKey(r))
		if err != nil {
			resultError(w, err)
			return
		}
		item, err := h.service.MiniProgram(r.Context(), resourceID)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "item": item, "miniprogram": item, "resolution": resolution, "changed": false, "thumb_media_id": "", "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		out, e := h.service.MiniProgram(r.Context(), resourceID)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, miniDetail(out))
		return
	}
	if r.Method == http.MethodPut {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var body map[string]any
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !allowedFields(body, "name", "appid", "app_id", "pagepath", "page_path", "title", "thumb_image_id", "enabled", "group_id", "expected_version") {
			writeError(w, 400, "invalid_request")
			return
		}
		out, e := h.service.UpdateMiniProgram(r.Context(), resourceID, actor.InternalID, mutationKey(r), body)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, miniMutation(out))
		return
	}
	if r.Method == http.MethodDelete {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		out, e := h.service.Delete(r.Context(), "miniprogram", resourceID, actor.InternalID, mutationKey(r))
		if e != nil {
			resultError(w, e)
			return
		}
		out["ok"] = true
		out["item_id"] = resourceID
		out["local_only"] = true
		out["provider_call_executed"] = false
		out["real_external_call_executed"] = false
		writeJSON(w, 200, out)
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
}
func (h *Handler) groups(w http.ResponseWriter, r *http.Request, tail string) {
	if tail != "" {
		itemID, err := id(tail)
		if err != nil {
			writeError(w, 404, "not_found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if !h.read(w, r) {
				return
			}
			item, err := h.service.GroupInvite(r.Context(), itemID)
			if err != nil {
				resultError(w, err)
				return
			}
			writeJSON(w, 200, groupMutation(item))
		case http.MethodPut:
			actor, ok := h.write(w, r)
			if !ok {
				return
			}
			var body map[string]any
			if decode(r, &body) != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			if !allowedFields(body, "name", "title", "description", "join_url", "cover_image_id", "enabled") {
				writeError(w, 400, "invalid_request")
				return
			}
			item, err := h.service.UpdateGroupInvite(r.Context(), itemID, actor.InternalID, mutationKey(r), body)
			if err != nil {
				resultError(w, err)
				return
			}
			writeJSON(w, 200, groupMutation(item))
		case http.MethodDelete:
			actor, ok := h.write(w, r)
			if !ok {
				return
			}
			item, err := h.service.ArchiveGroupInvite(r.Context(), itemID, actor.InternalID, mutationKey(r))
			if err != nil {
				resultError(w, err)
				return
			}
			response := groupMutation(item)
			response["archived"] = true
			writeJSON(w, 200, response)
		default:
			method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		}
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		limit, offset, enabled, q, e := mediaPage(r, 100, 100, false)
		if e != nil {
			invalidQuery(w)
			return
		}
		items, total, e := h.service.ListGroupInvites(r.Context(), limit, offset, enabled, q)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "items": items, "group_invites": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
		return
	}
	if r.Method == http.MethodPost {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var body map[string]any
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !allowedFields(body, "name", "title", "description", "join_url", "cover_image_id", "enabled") {
			writeError(w, 400, "invalid_request")
			return
		}
		out, e := h.service.CreateGroupInvite(r.Context(), actor.InternalID, mutationKey(r), body)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, groupMutation(out))
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPost)
}
func decode(r *http.Request, target any) error {
	return decodeLimit(r, target, 1<<20)
}
func decodeLimit(r *http.Request, target any, limit int64) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid body")
	}
	return nil
}

func allowedFields(value map[string]any, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		set[field] = struct{}{}
	}
	for field := range value {
		if _, ok := set[field]; !ok {
			return false
		}
	}
	return true
}

func groupMutation(item map[string]any) map[string]any {
	return map[string]any{"ok": true, "item": item, "group_invite": item, "item_id": item["id"], "local_only": true, "provider_call_executed": false, "real_external_call_executed": false}
}
func miniMutation(item map[string]any) map[string]any {
	changed, _ := item["_changed"].(bool)
	thumbResolve := item["_thumb_resolve"]
	public := make(map[string]any, len(item))
	for key, value := range item {
		if strings.HasPrefix(key, "_") {
			continue
		}
		public[key] = value
	}
	return map[string]any{"ok": true, "item": public, "miniprogram": public, "item_id": public["id"], "changed": changed, "thumb_resolve": thumbResolve, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false}
}
func miniDetail(item map[string]any) map[string]any {
	return map[string]any{"ok": true, "item": item, "miniprogram": item, "item_id": item["id"], "changed": false, "thumb_resolve": nil, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false}
}
func attachmentEnvelope(item map[string]any) map[string]any {
	response := map[string]any{}
	for key, value := range item {
		response[key] = value
	}
	response["ok"] = true
	response["item"] = item
	response["attachment"] = item
	response["local_only"] = true
	response["provider_call_executed"] = false
	response["real_external_call_executed"] = false
	return response
}
func mapKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}
