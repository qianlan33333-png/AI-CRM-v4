package externaleffects

import (
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// PushCenterHandler is a compatibility read/control projection built solely
// from external_effects, attempts, receipts and River links. It never joins
// customer, identity, recipient or provider tables.
type PushCenterHandler struct {
	repository *Repository
	security   RequestSecurity
}

func (h *PushCenterHandler) read(w http.ResponseWriter, r *http.Request) bool {
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
func (h *PushCenterHandler) mutate(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	return (&HTTPHandler{security: h.security}).mutate(w, r)
}
func (h *PushCenterHandler) writeResult(w http.ResponseWriter, p Projection, err error) {
	(&HTTPHandler{}).writeResult(w, p, err)
}

func NewPushCenterHandler(repository *Repository, security RequestSecurity) (*PushCenterHandler, error) {
	if repository == nil || security == nil {
		return nil, ErrInvalid
	}
	return &PushCenterHandler{repository: repository, security: security}, nil
}
func (h *PushCenterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repository == nil || h.security == nil {
		writeEffectError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/push-center"), "/")
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		switch path {
		case "sections":
			h.sections(w, r)
			return
		case "stats":
			h.stats(w, r)
			return
		case "jobs":
			h.jobs(w, r)
			return
		}
		parts := strings.Split(path, "/")
		if len(parts) == 3 && parts[0] == "jobs" && (parts[2] == "cancel" || parts[2] == "retry") {
			w.Header().Set("Allow", http.MethodPost)
			writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if len(parts) == 2 && parts[0] == "jobs" {
			h.job(w, r, parts[1], false)
			return
		}
		if len(parts) == 3 && parts[0] == "jobs" && parts[2] == "reconciliation" {
			h.job(w, r, parts[1], true)
			return
		}
		writeEffectError(w, http.StatusNotFound, "not_found")
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == "jobs" {
		w.Header().Set("Allow", http.MethodGet)
		writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if len(parts) != 3 || parts[0] != "jobs" || (parts[2] != "cancel" && parts[2] != "retry") {
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
	jobID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || jobID < 1 {
		writeEffectError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key := digestHeader(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeEffectError(w, http.StatusBadRequest, "idempotency_key_required")
		return
	}
	command := ControlCommand{EffectID: effectIDString(jobID), ReceiptKey: key, ActorAdminUserID: actor.InternalID}
	var p Projection
	if parts[2] == "cancel" {
		p, _, err = h.repository.Cancel(r.Context(), command)
	} else {
		p, _, err = h.repository.Retry(r.Context(), command)
	}
	if err != nil {
		h.writeResult(w, p, err)
		return
	}
	status := string(p.State)
	if parts[2] == "retry" {
		status = "pending"
	}
	writeEffectJSON(w, http.StatusOK, pushControlEnvelope(jobID, status, map[string]string{"cancel": "cancel", "retry": "manual_retry"}[parts[2]], p.UpdatedAt, actor.InternalID))
}

func pushControlEnvelope(jobID int64, status, operation string, completedAt any, actorAdminUserID int64) map[string]any {
	return map[string]any{
		"ok": true, "fallback_used": false, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false, "provider_execution_eligible": false,
		"control_receipt": map[string]any{
			"task_id": jobID, "task_status": status, "operation": operation, "completed_at": completedAt, "actor_admin_user_id": actorAdminUserID,
			"local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false,
		},
	}
}
func effectIDString(id int64) string { return "eer_" + strconv.FormatInt(id, 10) }
func (h *PushCenterHandler) sections(w http.ResponseWriter, r *http.Request) {
	stats, err := h.repository.pushStats(r.Context())
	if err != nil {
		writeEffectError(w, 500, "unavailable")
		return
	}
	writeEffectJSON(w, 200, map[string]any{"ok": true, "route_owner": "ai_crm_next", "filters": map[string]any{}, "sections": []map[string]any{{"key": "pending", "label": "待处理", "count": stats.Accepted + stats.Queued}, {"key": "running", "label": "执行中", "count": stats.Attempted}, {"key": "failed", "label": "待人工对账", "count": stats.Unknown + stats.Retryable + stats.FinalFailed}, {"key": "reconciled", "label": "已对账", "count": stats.Reconciled}}})
}
func (h *PushCenterHandler) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.repository.pushStats(r.Context())
	if err != nil {
		writeEffectError(w, 500, "unavailable")
		return
	}
	writeEffectJSON(w, 200, map[string]any{"ok": true, "route_owner": "ai_crm_next", "real_external_call_executed": false, "filters": map[string]any{}, "counts": map[string]any{"total": stats.Total, "pending": stats.Accepted + stats.Queued, "running": stats.Attempted, "sent": stats.Sent, "failed": stats.Unknown + stats.Retryable + stats.FinalFailed, "reconciled": stats.Reconciled}})
}
func pushStatus(state string) string {
	switch State(state) {
	case StateAccepted, StateQueued:
		return "pending"
	case StateAttempted:
		return "running"
	case StateExecuted:
		return "sent"
	case StateReconciled:
		return "reconciled"
	case StateFinalFailed:
		return "failed"
	default:
		return state
	}
}
func (h *PushCenterHandler) jobs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repository.pool.Query(r.Context(), `SELECT effect.id,effect.state,effect.attempt_count,effect.created_at,effect.updated_at FROM external_effect_jobs job JOIN external_effects effect ON effect.id=job.effect_id WHERE job.generation=effect.generation ORDER BY effect.updated_at DESC LIMIT 50`)
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
		classification := "local"
		if state == string(StateUnknown) {
			classification = "manual_review"
		}
		items = append(items, map[string]any{"job_id": id, "status": pushStatus(state), "attempt_count": count, "failure_class": classification, "created_at": created, "status_updated_at": updated, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false, "provider_execution_eligible": false})
	}
	writeEffectJSON(w, 200, map[string]any{"ok": true, "fallback_used": false, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false, "provider_execution_eligible": false, "items": items})
}
func (h *PushCenterHandler) job(w http.ResponseWriter, r *http.Request, raw string, reconciliation bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		writeEffectError(w, 400, "invalid_request")
		return
	}
	var state string
	var count int32
	var created, updated any
	err = h.repository.pool.QueryRow(r.Context(), `SELECT state,attempt_count,created_at,updated_at FROM external_effects WHERE id=$1`, id).Scan(&state, &count, &created, &updated)
	if err != nil {
		writeEffectError(w, 404, "not_found")
		return
	}
	body := map[string]any{"ok": true, "fallback_used": false, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false, "provider_execution_eligible": false, "job": map[string]any{"job_id": id, "status": pushStatus(state), "attempt_count": count, "failure_class": effectClassification(state), "created_at": created, "status_updated_at": updated, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false, "provider_execution_eligible": false}}
	if reconciliation {
		effectID := id
		attemptRows, queryErr := h.repository.pool.Query(r.Context(), `SELECT id,number,state,completed_at,started_at FROM external_effect_attempts WHERE effect_id=$1 ORDER BY number`, effectID)
		if queryErr != nil {
			writeEffectError(w, 500, "unavailable")
			return
		}
		defer attemptRows.Close()
		attempts := []any{}
		for attemptRows.Next() {
			var attemptID int64
			var number int32
			var attemptState string
			var completed any
			var started any
			if queryErr = attemptRows.Scan(&attemptID, &number, &attemptState, &completed, &started); queryErr != nil {
				writeEffectError(w, 500, "unavailable")
				return
			}
			attempts = append(attempts, map[string]any{"attempt_id": attemptID, "attempt": number, "state": attemptState, "failure_class": effectClassification(attemptState), "completed_at": completed, "dispatch_started_at": started, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false})
		}
		receiptRows, queryErr := h.repository.pool.Query(r.Context(), `SELECT operation,state,completed_at,actor_admin_user_id FROM external_effect_operation_receipts WHERE effect_id=$1 ORDER BY id`, effectID)
		if queryErr != nil {
			writeEffectError(w, 500, "unavailable")
			return
		}
		defer receiptRows.Close()
		receipts := []any{}
		for receiptRows.Next() {
			var operation, status string
			var completed any
			var actor *int64
			if queryErr = receiptRows.Scan(&operation, &status, &completed, &actor); queryErr != nil {
				writeEffectError(w, 500, "unavailable")
				return
			}
			receipts = append(receipts, map[string]any{"task_id": effectID, "operation": operation, "task_status": pushStatus(status), "completed_at": completed, "actor_admin_user_id": actor, "local_fact_only": true, "real_external_call_executed": false, "delivery_proven": false})
		}
		body["attempts"] = attempts
		body["control_receipts"] = receipts
	}
	writeEffectJSON(w, 200, body)
}
