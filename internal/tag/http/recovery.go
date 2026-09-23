package http

import (
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"net/http"
	"strings"
)

func (h *Handler) recoverMutation(w http.ResponseWriter, r *http.Request, tail string) {
	if r.Method != http.MethodPost {
		method(w, http.MethodPost)
		return
	}
	principal, ok := h.mutate(w, r)
	if !ok {
		return
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(tail, "mutations/"), "/retry")
	id, ok := parseID(raw)
	if !ok {
		writeError(w, 404, "not_found")
		return
	}
	key, err := idempotencyKey(r, "")
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	result, err := h.catalog.RetryMutation(r.Context(), id, command(principal, key, opaqueRequestID()))
	if err != nil {
		if errors.Is(err, effectport.ErrReconciliationConflict) {
			writeJSON(w, 409, map[string]any{"ok": false, "error": "tag_retry_unsafe", "message": "当前结果不能安全重试，请先核对企微"})
			return
		}
		resultError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"ok": true, "effect_id": result.ID, "effect_state": result.State, "message": recoveryStateMessage(result.State), "real_external_call_executed": false})
}

// A repeated command returns the original effect's current state, which may
// already be terminal. HTTP acceptance is not another Provider execution.
func recoveryStateMessage(state effectport.State) string {
	switch state {
	case effectport.StateQueued, effectport.StateAccepted:
		return "原企微任务等待执行"
	case effectport.StateAttempted:
		return "原企微任务正在执行，结果待确认"
	case effectport.StateExecuted:
		return "原企微任务已完成"
	case effectport.StateUnknown:
		return "原企微任务结果待核对，请勿重复创建"
	case effectport.StateFinalFailed, effectport.StateRetryable:
		return "原企微任务尚未完成，请查看当前失败状态"
	case effectport.StateCancelled:
		return "原企微任务已取消"
	case effectport.StateReconciled:
		return "原企微任务已完成对账，请查看核对结果"
	default:
		return "原企微任务状态待确认"
	}
}
