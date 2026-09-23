package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type RuntimeHandler struct {
	service  *automationapp.RuntimeService
	security RequestSecurity
}

func NewRuntimeHandler(service *automationapp.RuntimeService, security RequestSecurity) (*RuntimeHandler, error) {
	if service == nil || security == nil {
		return nil, errors.New("automation runtime HTTP dependencies are required")
	}
	return &RuntimeHandler{service: service, security: security}, nil
}
func (h *RuntimeHandler) principal(w http.ResponseWriter, r *http.Request, write bool) (accessdomain.Principal, bool) {
	if write {
		return h.write(w, r)
	}
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		errorJSON(w, 401, "unauthorized")
		return p, false
	}
	if !role(p, false) {
		errorJSON(w, 403, "forbidden")
		return p, false
	}
	return p, true
}
func (h *RuntimeHandler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		errorJSON(w, 401, "unauthorized")
		return p, false
	}
	if !role(p, true) {
		errorJSON(w, 403, "forbidden")
		return p, false
	}
	if _, e = h.security.AuthorizeCSRF(r.Context(), r); e != nil {
		errorJSON(w, 403, "csrf_required")
		return p, false
	}
	return p, true
}
func (h *RuntimeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/admin/automations" || strings.HasPrefix(r.URL.Path, "/api/admin/automations/"):
		h.policies(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/admin/automation-runs"):
		h.runs(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/admin/ai-audience/packages/"):
		h.packageRun(w, r)
	default:
		errorJSON(w, 404, "automation_runtime_not_found")
	}
}

type policyInput struct {
	Code            string                     `json:"code"`
	Name            string                     `json:"name"`
	PackageID       int64                      `json:"package_id"`
	Trigger         automationport.TriggerKind `json:"trigger"`
	Action          automationport.ActionKind  `json:"action"`
	ActionConfig    json.RawMessage            `json:"action_config"`
	QuietHours      json.RawMessage            `json:"quiet_hours"`
	SingleRunLimit  int                        `json:"single_run_limit"`
	ExpectedVersion int64                      `json:"expected_version"`
}

func (h *RuntimeHandler) policies(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/automations"), "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			if _, ok := h.principal(w, r, false); !ok {
				return
			}
			items, e := h.service.ListPolicies(r.Context())
			if e != nil {
				runtimeError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"items": items})
		case http.MethodPost:
			p, ok := h.principal(w, r, true)
			if !ok {
				return
			}
			var in policyInput
			if decode(r, &in) != nil {
				errorJSON(w, 400, "invalid_automation_policy")
				return
			}
			key, ok := requestKey(w, r)
			if !ok {
				return
			}
			out, e := h.service.CreatePolicy(r.Context(), policyCommand(in, p.InternalID, key))
			if e != nil {
				runtimeError(w, e)
				return
			}
			writeJSON(w, 201, map[string]any{"data": out})
		default:
			method(w, "GET, POST")
		}
		return
	}
	parts := strings.Split(tail, "/")
	id, ok := strictInt(parts[0])
	if !ok {
		errorJSON(w, 404, "automation_policy_not_found")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if _, ok := h.principal(w, r, false); !ok {
				return
			}
			p, v, e := h.service.Policy(r.Context(), id)
			if e != nil {
				runtimeError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"data": map[string]any{"policy": p, "version": v}})
		case http.MethodPatch:
			principal, ok := h.principal(w, r, true)
			if !ok {
				return
			}
			var in policyInput
			if decode(r, &in) != nil {
				errorJSON(w, 400, "invalid_automation_policy")
				return
			}
			in.ExpectedVersion = max64(in.ExpectedVersion, 1)
			key, ok := requestKey(w, r)
			if !ok {
				return
			}
			c := policyCommand(in, principal.InternalID, key)
			c.PolicyID = id
			out, e := h.service.PutPolicyVersion(r.Context(), c)
			if e != nil {
				runtimeError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"data": out})
		default:
			method(w, "GET, PATCH")
		}
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		errorJSON(w, 404, "automation_policy_not_found")
		return
	}
	principal, ok := h.principal(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	if decode(r, &body) != nil {
		errorJSON(w, 400, "invalid_automation_policy")
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	var target automationdomain.PolicyLifecycle
	switch parts[1] {
	case "activate":
		target = automationdomain.PolicyActive
	case "pause":
		target = automationdomain.PolicyPaused
	case "archive":
		target = automationdomain.PolicyArchived
	default:
		errorJSON(w, 404, "automation_policy_not_found")
		return
	}
	out, e := h.service.TransitionPolicy(r.Context(), automationapp.PolicyLifecycleCommand{PolicyID: id, ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, Target: target, IdempotencyKey: key})
	if e != nil {
		runtimeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
func policyCommand(in policyInput, actor int64, key string) automationapp.PolicyCommand {
	// The frozen automation contract only has a requires-approval gate. It
	// never lets the policy editor nominate an arbitrary staff member as an
	// approver. The existing immutable policy record still requires a trusted
	// local actor, so bind it to the authenticated caller only.
	approval := actor
	return automationapp.PolicyCommand{Code: in.Code, Name: in.Name, ExpectedVersion: in.ExpectedVersion, PackageID: segmentport.PackageID(in.PackageID), TriggerKind: in.Trigger, ActionKind: in.Action, ActionConfig: in.ActionConfig, QuietHours: in.QuietHours, SingleRunLimit: in.SingleRunLimit, ApprovalStaffID: &approval, Actor: actor, IdempotencyKey: key}
}
func (h *RuntimeHandler) packageRun(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/ai-audience/packages/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 {
		errorJSON(w, 404, "automation_runtime_not_found")
		return
	}
	id, ok := strictInt(parts[0])
	if !ok {
		errorJSON(w, 404, "automation_runtime_not_found")
		return
	}
	switch parts[1] {
	case "broadcast-previews":
		if r.Method != http.MethodPost {
			method(w, "POST")
			return
		}
		p, ok := h.principal(w, r, false)
		if !ok {
			return
		}
		out, e := h.service.CreateBroadcastPreview(r.Context(), id, p.InternalID)
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"snapshot_id": out.SnapshotID, "agent_id": out.AgentID, "agent_published_version": out.AgentPublishedVersion, "target_count": out.TargetCount, "skipped_count": out.SkippedCount, "preview_digest": automationapp.PreviewDigestString(out), "expected_package_version": out.PackageVersion})
	case "runs":
		if r.Method != http.MethodPost {
			method(w, "POST")
			return
		}
		p, ok := h.principal(w, r, true)
		if !ok {
			return
		}
		var in struct {
			SnapshotID             int64  `json:"snapshot_id"`
			AgentID                int64  `json:"agent_id"`
			AgentPublishedVersion  int64  `json:"agent_published_version"`
			PreviewDigest          string `json:"preview_digest"`
			ExpectedPackageVersion int64  `json:"expected_package_version"`
		}
		if decode(r, &in) != nil {
			errorJSON(w, 400, "invalid_automation_run")
			return
		}
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		out, e := h.service.ConfirmRun(r.Context(), automationapp.RunConfirmCommand{PackageID: id, PackageVersion: in.ExpectedPackageVersion, SnapshotID: in.SnapshotID, AgentID: in.AgentID, AgentPublishedVersion: in.AgentPublishedVersion, PreviewDigest: in.PreviewDigest, Actor: p.InternalID, IdempotencyKey: key})
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 202, map[string]any{"run": runDTO(out)})
	default:
		errorJSON(w, 404, "automation_runtime_not_found")
	}
}
func (h *RuntimeHandler) runs(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/automation-runs"), "/")
	if tail == "" {
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if _, ok := h.principal(w, r, false); !ok {
			return
		}
		cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		limit := queryLimit(r, 50)
		items, next, e := h.service.ListRuns(r.Context(), cursor, limit)
		if e != nil {
			runtimeError(w, e)
			return
		}
		out := make([]any, len(items))
		for i := range items {
			out[i] = runDTO(items[i])
		}
		writeJSON(w, 200, map[string]any{"items": out, "next_cursor": next})
		return
	}
	parts := strings.Split(tail, "/")
	id, ok := strictInt(parts[0])
	if !ok {
		errorJSON(w, 404, "automation_run_not_found")
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		if _, ok := h.principal(w, r, false); !ok {
			return
		}
		out, e := h.service.Run(r.Context(), id)
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"run": runDTO(out)})
		return
	}
	if len(parts) == 2 && parts[1] == "recipients" && r.Method == http.MethodGet {
		if _, ok := h.principal(w, r, false); !ok {
			return
		}
		cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		items, next, e := h.service.RunRecipients(r.Context(), id, cursor, queryLimit(r, 50))
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
		return
	}
	if len(parts) == 2 && parts[1] == "generation-items" && r.Method == http.MethodGet {
		if _, ok := h.principal(w, r, false); !ok {
			return
		}
		cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		items, next, e := h.service.GenerationItems(r.Context(), id, cursor, queryLimit(r, 50))
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		p, ok := h.principal(w, r, true)
		if !ok {
			return
		}
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		out, e := h.service.CancelRun(r.Context(), id, p.InternalID, key)
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"run": runDTO(out)})
		return
	}
	if len(parts) == 4 && parts[1] == "effects" && parts[2] != "" && parts[3] == "reconciliation-candidate" && r.Method == http.MethodGet {
		if _, ok := h.principal(w, r, false); !ok {
			return
		}
		out, e := h.service.EffectReconciliationCandidate(r.Context(), id, parts[2])
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out})
		return
	}
	if len(parts) == 4 && parts[1] == "effects" && parts[2] != "" && parts[3] == "reconcile" && r.Method == http.MethodPost {
		principal, ok := h.principal(w, r, true)
		if !ok {
			return
		}
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		var body struct {
			Generation     int64  `json:"generation"`
			Fence          int64  `json:"fence"`
			LeaseExpiresAt string `json:"lease_expires_at"`
			EvidenceDigest string `json:"evidence_digest"`
			Resolution     string `json:"resolution"`
		}
		if decode(r, &body) != nil {
			errorJSON(w, 400, "invalid_automation_reconciliation")
			return
		}
		lease, e := time.Parse(time.RFC3339Nano, body.LeaseExpiresAt)
		if e != nil {
			errorJSON(w, 400, "invalid_automation_reconciliation")
			return
		}
		out, e := h.service.ReconcileRunEffect(r.Context(), automationapp.RunEffectReconcileCommand{RunID: id, Actor: principal.InternalID, Generation: body.Generation, Fence: body.Fence, EffectID: parts[2], IdempotencyKey: key, LeaseExpiresAt: lease, EvidenceDigest: body.EvidenceDigest, Resolution: body.Resolution})
		if e != nil {
			runtimeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out})
		return
	}
	errorJSON(w, 404, "automation_run_not_found")
}
func runDTO(r automationdomain.RuntimeRun) map[string]any {
	return map[string]any{"id": r.ID, "policy_id": r.PolicyID, "policy_version": r.PolicyVersion, "state": r.State, "ai_plan_id": r.AIPlanID, "ai_plan_state": r.AIPlanState, "target_count": r.TargetCount, "skipped_count": r.SkippedCount, "outcome_unknown_count": r.OutcomeUnknownCount, "generation": r.Generation, "package_id": r.PackageID, "snapshot_id": r.SnapshotID, "agent_id": r.AgentID, "agent_published_version": r.AgentPublishedVersion, "created_at": r.CreatedAt}
}
func strictInt(v string) (int64, bool) {
	n, e := strconv.ParseInt(v, 10, 64)
	return n, e == nil && n > 0 && strconv.FormatInt(n, 10) == v
}
func queryLimit(r *http.Request, fallback int) int {
	n, e := strconv.Atoi(r.URL.Query().Get("limit"))
	if e != nil || n < 1 || n > 100 {
		return fallback
	}
	return n
}
func max64(v, min int64) int64 {
	if v < min {
		return min
	}
	return v
}
func runtimeError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, automationapp.ErrRuntimeInvalid):
		errorJSON(w, 400, "invalid_automation_runtime_request")
	case errors.Is(e, automationapp.ErrRuntimeNotFound):
		errorJSON(w, 404, "automation_runtime_not_found")
	case errors.Is(e, automationapp.ErrRuntimeConflict):
		errorJSON(w, 409, "automation_runtime_conflict")
	case errors.Is(e, automationapp.ErrRuntimeNotReady):
		errorJSON(w, 422, "automation_runtime_not_ready")
	default:
		errorJSON(w, 503, "automation_runtime_unavailable")
	}
}
