// Package http exposes the frozen operation-cycle API contract. Browser
// commands use the v3 session/CSRF boundary; runner endpoints use one disabled-
// by-default service token and never accept a browser session as fallback.
package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
)

const bodyLimit = 256 << 10

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Handler struct {
	service      *operationapp.Service
	security     RequestSecurity
	serviceToken string
}

func NewHandler(service *operationapp.Service, security RequestSecurity, serviceToken string) (*Handler, error) {
	serviceToken = strings.TrimSpace(serviceToken)
	if service == nil || security == nil || (serviceToken != "" && len(serviceToken) < 32) {
		return nil, errors.New("operation-cycle HTTP dependencies are invalid")
	}
	return &Handler{service: service, security: security, serviceToken: serviceToken}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "admin" && parts[2] == "operation-cycles" {
		h.serveAdmin(w, r, parts[3:])
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "operation-cycles" {
		h.serveRunner(w, r, parts[2:])
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func (h *Handler) serveAdmin(w http.ResponseWriter, r *http.Request, p []string) {
	principal, ok := h.admin(w, r, r.Method != http.MethodGet && r.Method != http.MethodHead)
	if !ok {
		return
	}
	limit, offset, pageOK := page(r)
	switch {
	case r.Method == http.MethodPost && len(p) == 1 && p[0] == "strategies" && noQuery(r):
		var body strategyCreateBody
		if decode(w, r, &body) != nil {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.CreateStrategy(r.Context(), operationapp.CreateStrategyCommand{StrategyKey: body.StrategyKey, Title: body.Title, Definition: body.Definition.command(), IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ActorID: strconv.FormatInt(principal.InternalID, 10)})
		writeResult(w, http.StatusCreated, value, err)
	case r.Method == http.MethodPut && len(p) == 2 && p[0] == "strategies" && noQuery(r):
		var body strategyUpdateBody
		if decode(w, r, &body) != nil {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.UpdateStrategy(r.Context(), operationapp.UpdateStrategyCommand{StrategyKey: p[1], ExpectedVersion: body.ExpectedVersion, Title: body.Title, Definition: body.Definition.command(), IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ActorID: strconv.FormatInt(principal.InternalID, 10)})
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodPost && len(p) == 3 && p[0] == "strategies" && p[2] == "status" && noQuery(r):
		var body struct {
			ExpectedVersion int32  `json:"expected_version"`
			Status          string `json:"status"`
		}
		if decode(w, r, &body) != nil {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.TransitionStrategy(r.Context(), operationapp.TransitionStrategyCommand{StrategyKey: p[1], ExpectedVersion: body.ExpectedVersion, Status: body.Status, IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ActorID: strconv.FormatInt(principal.InternalID, 10)})
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 1 && p[0] == "strategies" && pageOK:
		value, err := h.service.ListStrategies(r.Context(), limit, offset)
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 2 && p[0] == "strategies" && noQuery(r):
		value, err := h.service.GetStrategy(r.Context(), p[1])
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 3 && p[0] == "strategies" && p[2] == "versions" && pageOK:
		value, err := h.service.ListStrategyVersions(r.Context(), p[1], limit, offset)
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 1 && p[0] == "runs" && pageOK:
		// Compatibility alias used by the v3 host adapter to resolve a run by key.
		writeError(w, http.StatusBadRequest, "malformed_request")
	case r.Method == http.MethodGet && len(p) == 2 && p[0] == "runs" && noQuery(r):
		value, err := h.service.GetRun(r.Context(), p[1])
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 2 && p[0] == "run-ordinals" && noQuery(r):
		ordinal, err := strconv.ParseInt(p[1], 10, 32)
		if err != nil || ordinal < 1 || ordinal > 999999999 {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.GetRunByOrdinal(r.Context(), int32(ordinal))
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 3 && p[0] == "runs" && p[2] == "versions" && pageOK:
		value, err := h.service.ListRunVersions(r.Context(), p[1], limit, offset)
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 3 && p[0] == "strategies" && p[2] == "runs" && pageOK:
		value, err := h.service.ListRuns(r.Context(), p[1], limit, offset)
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 3 && p[0] == "strategies" && p[2] == "current-action" && noQuery(r):
		value, err := h.service.CurrentAction(r.Context(), p[1])
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 3 && p[0] == "strategies" && p[2] == "strategy-change-proposals" && pageOK:
		value, err := h.service.ListProposals(r.Context(), p[1], limit, offset)
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 3 && p[0] == "action-requests" && p[2] == "result" && noQuery(r):
		value, err := h.service.GetActionResult(r.Context(), p[1])
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodPost && len(p) == 5 && p[0] == "strategies" && p[2] == "actions" && p[4] == "start" && noQuery(r):
		var body struct {
			RunKey          string `json:"run_key"`
			ParentRequestID string `json:"parent_request_id"`
		}
		if decode(w, r, &body) != nil {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.Start(r.Context(), operationapp.StartCommand{StrategyKey: p[1], ActionKey: p[3], RunKey: body.RunKey, ParentRequest: body.ParentRequestID, IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ActorID: strconv.FormatInt(principal.InternalID, 10)})
		writeResult(w, http.StatusAccepted, value, err)
	case r.Method == http.MethodPost && len(p) == 3 && p[0] == "strategy-change-proposals" && p[2] == "decision" && noQuery(r):
		var body struct {
			Decision string `json:"decision"`
		}
		if decode(w, r, &body) != nil {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.DecideProposal(r.Context(), p[1], body.Decision, strconv.FormatInt(principal.InternalID, 10))
		writeResult(w, http.StatusOK, value, err)
	default:
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !pageOK {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		writeError(w, http.StatusNotFound, "not_found")
	}
}

type strategyStageBody struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Color string `json:"color"`
	State string `json:"state"`
}

type strategyDefinitionBody struct {
	Schedule       string                 `json:"schedule"`
	IndicatorColor string                 `json:"indicator_color"`
	PrimaryAction  string                 `json:"primary_action"`
	Stages         []strategyStageBody    `json:"stages"`
	Execution      *strategyExecutionBody `json:"execution,omitempty"`
}

type strategyExecutionBody struct {
	Title                 string         `json:"title"`
	Objective             string         `json:"objective"`
	CodexPrompt           string         `json:"codex_prompt"`
	RequiredLocalBindings []string       `json:"required_local_bindings"`
	ResultSchema          map[string]any `json:"result_schema"`
}

func (body strategyDefinitionBody) command() operationapp.StrategyDefinition {
	stages := make([]operationapp.StrategyStage, 0, len(body.Stages))
	for _, stage := range body.Stages {
		stages = append(stages, operationapp.StrategyStage{Key: stage.Key, Label: stage.Label, Color: stage.Color, State: stage.State})
	}
	var execution *operationapp.StrategyExecution
	if body.Execution != nil {
		execution = &operationapp.StrategyExecution{Title: body.Execution.Title, Objective: body.Execution.Objective, CodexPrompt: body.Execution.CodexPrompt, RequiredLocalBindings: append([]string(nil), body.Execution.RequiredLocalBindings...), ResultSchema: body.Execution.ResultSchema}
	}
	return operationapp.StrategyDefinition{Schedule: body.Schedule, IndicatorColor: body.IndicatorColor, PrimaryAction: body.PrimaryAction, Stages: stages, Execution: execution}
}

type strategyCreateBody struct {
	StrategyKey string                 `json:"strategy_key"`
	Title       string                 `json:"title"`
	Definition  strategyDefinitionBody `json:"definition"`
}

type strategyUpdateBody struct {
	ExpectedVersion int32                  `json:"expected_version"`
	Title           string                 `json:"title"`
	Definition      strategyDefinitionBody `json:"definition"`
}

func (h *Handler) serveRunner(w http.ResponseWriter, r *http.Request, p []string) {
	if !h.runner(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodPost && len(p) == 2 && p[0] == "action-requests" && p[1] == "claim" && noQuery(r):
		var body struct {
			SchemaVersion string `json:"schema_version"`
			RunnerID      string `json:"runner_id"`
			WaitSeconds   int32  `json:"wait_seconds"`
		}
		if decode(w, r, &body) != nil || body.SchemaVersion != "operation_cycle_action_claim.v1" || body.WaitSeconds != 0 {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.Claim(r.Context(), body.RunnerID, "operation-cycle-service")
		writeResult(w, http.StatusAccepted, value, err)
	case r.Method == http.MethodPost && len(p) == 3 && p[0] == "action-requests" && p[2] == "lease-renewals" && noQuery(r):
		var body struct {
			SchemaVersion string `json:"schema_version"`
			LeaseToken    string `json:"lease_token"`
		}
		if decode(w, r, &body) != nil || body.SchemaVersion != "operation_cycle_action_lease_renewal.v1" {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.RenewActionLease(r.Context(), operationapp.ActionLeaseRenewalCommand{RequestID: p[1], LeaseToken: body.LeaseToken})
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodPost && len(p) == 3 && p[0] == "action-requests" && p[2] == "events" && noQuery(r):
		var body struct {
			SchemaVersion string         `json:"schema_version"`
			EventType     string         `json:"event_type"`
			LeaseToken    string         `json:"lease_token"`
			ThreadID      string         `json:"thread_id"`
			TurnID        string         `json:"turn_id"`
			Result        map[string]any `json:"result"`
			FailureCode   string         `json:"failure_code"`
		}
		if decode(w, r, &body) != nil || body.SchemaVersion != "operation_cycle_action_event.v1" {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.RecordActionEvent(r.Context(), operationapp.ActionEventCommand{RequestID: p[1], EventID: strings.TrimSpace(r.Header.Get("Idempotency-Key")), EventType: body.EventType, LeaseToken: body.LeaseToken, ThreadID: body.ThreadID, TurnID: body.TurnID, Result: body.Result, FailureCode: body.FailureCode})
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 1 && p[0] == "context-index":
		limit, offset, ok := page(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.ContextIndex(r.Context(), limit, offset)
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodPost && len(p) == 1 && p[0] == "reports" && noQuery(r):
		var body map[string]any
		if decode(w, r, &body) != nil {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.Report(r.Context(), operationapp.ReportCommand{Snapshot: body, IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ReporterID: "operation-cycle-service", ClientID: "v3-runner"})
		writeResult(w, http.StatusAccepted, value, err)
	case r.Method == http.MethodPost && len(p) == 2 && p[0] == "runner" && p[1] == "heartbeat" && noQuery(r):
		var body struct {
			SchemaVersion       string   `json:"schema_version"`
			RunnerID            string   `json:"runner_id"`
			ConnectorVersion    string   `json:"connector_version"`
			CodexVersion        string   `json:"codex_version"`
			AppServerProtocol   string   `json:"app_server_protocol"`
			CompatibilityStatus string   `json:"compatibility_status"`
			BindingKeys         []string `json:"binding_keys"`
		}
		if decode(w, r, &body) != nil || body.SchemaVersion != "operation_cycle_runner_heartbeat.v1" {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.Heartbeat(r.Context(), operationapp.RunnerHeartbeatCommand{RunnerID: body.RunnerID, ConnectorVersion: body.ConnectorVersion, CodexVersion: body.CodexVersion, AppServerProtocol: body.AppServerProtocol, CompatibilityStatus: body.CompatibilityStatus, BindingKeys: body.BindingKeys, PrincipalID: "operation-cycle-service"})
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodGet && len(p) == 3 && p[0] == "strategies" && p[2] == "context":
		limit, offset, ok := pageWithMode(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		mode := r.URL.Query().Get("mode")
		if mode == "" {
			mode = "execution"
		}
		value, err := h.service.StrategyContext(r.Context(), p[1], mode, limit, offset, nil)
		writeResult(w, http.StatusOK, value, err)
	case r.Method == http.MethodPost && len(p) == 1 && p[0] == "strategy-change-proposals" && noQuery(r):
		var body map[string]any
		if decode(w, r, &body) != nil {
			writeError(w, http.StatusBadRequest, "malformed_request")
			return
		}
		value, err := h.service.CreateProposal(r.Context(), operationapp.ProposalCommand{Payload: body, IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ActorID: "operation-cycle-service"})
		writeResult(w, http.StatusAccepted, value, err)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) admin(w http.ResponseWriter, r *http.Request, write bool) (accessdomain.Principal, bool) {
	var p accessdomain.Principal
	var err error
	if write {
		p, err = h.security.AuthorizeCSRF(r.Context(), r)
	} else {
		p, err = h.security.Authenticate(r.Context(), r)
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return p, false
	}
	allowed := false
	for _, role := range p.Roles {
		if role == accessdomain.RoleSuperAdmin || role == accessdomain.RoleAdmin || (!write && role == accessdomain.RoleViewer) {
			allowed = true
		}
	}
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) || !allowed {
		writeError(w, http.StatusForbidden, "unauthorized")
		return p, false
	}
	return p, true
}

func (h *Handler) runner(w http.ResponseWriter, r *http.Request) bool {
	if h.serviceToken == "" {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
		return false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if len(raw) != len(h.serviceToken) || subtle.ConstantTimeCompare([]byte(raw), []byte(h.serviceToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return false
	}
	return true
}

func splitPath(value string) []string                   { return strings.Split(strings.Trim(value, "/"), "/") }
func noQuery(r *http.Request) bool                      { return len(r.URL.Query()) == 0 }
func page(r *http.Request) (int32, int32, bool)         { return parsePage(r, false) }
func pageWithMode(r *http.Request) (int32, int32, bool) { return parsePage(r, true) }
func parsePage(r *http.Request, mode bool) (int32, int32, bool) {
	for k, values := range r.URL.Query() {
		if len(values) != 1 || (k != "limit" && k != "offset" && !(mode && k == "mode")) {
			return 0, 0, false
		}
	}
	limit, offset := int64(operationapp.DefaultLimit), int64(0)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
	}
	if err == nil {
		if raw := r.URL.Query().Get("offset"); raw != "" {
			offset, err = strconv.ParseInt(raw, 10, 32)
		}
	}
	if err != nil || limit < 1 || limit > int64(operationapp.MaximumLimit) || offset < 0 || offset > int64(operationapp.MaximumOffset) {
		return 0, 0, false
	}
	return int32(limit), int32(offset), true
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, bodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}
func writeResult(w http.ResponseWriter, status int, value map[string]any, err error) {
	if err != nil {
		writeAppError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeAppError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "dependency_unavailable"
	switch {
	case errors.Is(err, operationapp.ErrInvalid):
		status, code = http.StatusBadRequest, "malformed_request"
	case errors.Is(err, operationapp.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, operationapp.ErrConflict), errors.Is(err, operationapp.ErrLeaseInvalid), errors.Is(err, operationapp.ErrActionUnavailable):
		status, code = http.StatusConflict, "conflict"
	}
	writeError(w, status, code)
}
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code})
}
