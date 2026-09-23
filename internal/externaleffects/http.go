package externaleffects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// RequestSecurity is supplied by the composition root. Read APIs require an
// authenticated administrator; all controls additionally require CSRF.
type RequestSecurity interface {
	Authenticate(ctx context.Context, request *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(ctx context.Context, request *http.Request) (accessdomain.Principal, error)
}

type HTTPHandler struct {
	repository *Repository
	security   RequestSecurity
}

func NewHTTPHandler(repository *Repository, security RequestSecurity) (*HTTPHandler, error) {
	if repository == nil || security == nil {
		return nil, ErrInvalid
	}
	return &HTTPHandler{repository: repository, security: security}, nil
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repository == nil || h.security == nil {
		writeEffectError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/external-effects")
	if path == "" || path == "/" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if !h.read(w, r) {
			return
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				writeEffectError(w, http.StatusBadRequest, "invalid_request")
				return
			}
		}
		items, err := h.repository.List(r.Context(), limit)
		if err != nil {
			writeEffectError(w, http.StatusInternalServerError, "unavailable")
			return
		}
		writeEffectJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	if path == "/diagnostics" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if !h.read(w, r) {
			return
		}
		value, err := h.repository.Diagnostics(r.Context())
		if err != nil {
			writeEffectError(w, http.StatusInternalServerError, "unavailable")
			return
		}
		writeEffectJSON(w, http.StatusOK, value)
		return
	}
	if path == "/jobs" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if !h.read(w, r) {
			return
		}
		h.jobs(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeEffectError(w, http.StatusNotFound, "not_found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if _, err := parseEffectID(id); err != nil {
			writeEffectError(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if !h.read(w, r) {
			return
		}
		value, err := h.repository.Get(r.Context(), id)
		h.writeResult(w, value, err)
		return
	}
	if len(parts) != 2 || (parts[1] != "cancel" && parts[1] != "retry" && parts[1] != "reconcile") || func() bool { _, err := parseEffectID(id); return err != nil }() {
		writeEffectError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	actor, ok := h.mutate(w, r)
	if !ok {
		return
	}
	key := digestHeader(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeEffectError(w, http.StatusBadRequest, "idempotency_key_required")
		return
	}
	command := ControlCommand{EffectID: id, ReceiptKey: key, ActorAdminUserID: actor.InternalID}
	var value Projection
	var err error
	switch parts[1] {
	case "cancel":
		value, _, err = h.repository.Cancel(r.Context(), command)
	case "retry":
		value, _, err = h.repository.Retry(r.Context(), command)
	case "reconcile":
		var body struct {
			EvidenceDigest    string `json:"evidence_digest"`
			Outcome           string `json:"outcome"`
			MediaID           string `json:"media_id"`
			ProviderCreatedAt string `json:"provider_created_at"`
		}
		if decodeEffectJSON(r, &body) != nil {
			writeEffectError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		command.EvidenceDigest = Digest(body.EvidenceDigest)
		command.ReconciliationOutcome = body.Outcome
		if body.Outcome == "confirmed_effect" {
			created, parseErr := time.Parse(time.RFC3339, body.ProviderCreatedAt)
			if parseErr != nil || strings.TrimSpace(body.MediaID) == "" {
				writeEffectError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			payload, _ := json.Marshal(struct {
				MediaID           string    `json:"media_id"`
				ProviderCreatedAt time.Time `json:"provider_created_at"`
			}{body.MediaID, created.UTC()})
			command.ReconciliationArtifact = ResultArtifact{Kind: "outbound.material.upload.v1", Payload: payload}
			command.ReconciliationArtifact.Digest = Hash("external-effect.artifact.v1", command.ReconciliationArtifact.Kind, string(payload))
		}
		value, _, err = h.repository.Reconcile(r.Context(), command)
	default:
		writeEffectError(w, http.StatusNotFound, "not_found")
		return
	}
	h.writeResult(w, value, err)
}

func (h *HTTPHandler) jobs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repository.pool.Query(r.Context(), `SELECT job.river_job_id,effect.state,effect.attempt_count,effect.created_at,effect.updated_at FROM external_effect_jobs job JOIN external_effects effect ON effect.id=job.effect_id WHERE job.generation=effect.generation ORDER BY effect.updated_at DESC LIMIT 50`)
	if err != nil {
		writeEffectError(w, 500, "unavailable")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var state string
		var count int32
		var created, updated any
		if err = rows.Scan(&id, &state, &count, &created, &updated); err != nil {
			writeEffectError(w, 500, "unavailable")
			return
		}
		items = append(items, map[string]any{"id": "eerj_" + strconv.FormatInt(id, 10), "status": state, "classification": effectClassification(state), "attempt_count": count, "created_at": created, "status_updated_at": updated})
	}
	writeEffectJSON(w, 200, map[string]any{"ok": true, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false, "provider_execution_eligible": false, "items": items})
}
func effectClassification(state string) string {
	if state == string(StateUnknown) {
		return "manual_review"
	}
	return "local"
}

func (h *HTTPHandler) read(w http.ResponseWriter, r *http.Request) bool {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeEffectError(w, http.StatusForbidden, "forbidden")
		return false
	}
	if !mayReadEffects(principal) {
		writeEffectError(w, http.StatusForbidden, "permission_denied")
		return false
	}
	return true
}
func (h *HTTPHandler) mutate(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	principal, err := h.security.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		writeEffectError(w, http.StatusForbidden, "csrf_required")
		return accessdomain.Principal{}, false
	}
	if !mayControlEffects(principal) {
		writeEffectError(w, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func mayReadEffects(principal accessdomain.Principal) bool {
	if principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func mayControlEffects(principal accessdomain.Principal) bool {
	if !mayReadEffects(principal) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func (h *HTTPHandler) writeResult(w http.ResponseWriter, v Projection, err error) {
	switch {
	case err == nil:
		writeEffectJSON(w, http.StatusOK, v)
	case errors.Is(err, ErrNotFound):
		writeEffectError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrPayloadMismatch), errors.Is(err, ErrTransition), errors.Is(err, ErrReconcileRequired):
		writeEffectError(w, http.StatusConflict, "state_conflict")
	case errors.Is(err, ErrInvalid):
		writeEffectError(w, http.StatusBadRequest, "invalid_request")
	default:
		writeEffectError(w, http.StatusInternalServerError, "unavailable")
	}
}
func digestHeader(raw string) Digest {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 200 {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}
func decodeEffectJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func writeEffectJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeEffectError(w http.ResponseWriter, status int, code string) {
	writeEffectJSON(w, status, map[string]any{"ok": false, "error": code, "code": code})
}
