// Package http implements the frozen v2 automation-agent compatibility API.
package http

import (
	"context"
	"crypto/rand"
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
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type Handler struct {
	service   automationport.AgentService
	published automationport.PublishedAgentReader
	security  RequestSecurity
}

func NewHandler(service automationport.AgentService, security RequestSecurity) (*Handler, error) {
	if service == nil || security == nil {
		return nil, errors.New("automation HTTP dependencies are required")
	}
	published, _ := service.(automationport.PublishedAgentReader)
	return &Handler{service: service, published: published, security: security}, nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/automation-agents"), "/")
	if r.URL.Path != "/api/admin/automation-agents" && !strings.HasPrefix(r.URL.Path, "/api/admin/automation-agents/") {
		errorJSON(w, 404, "automation_agent_not_found")
		return
	}
	if tail == "" {
		if r.Method == http.MethodGet {
			h.list(w, r)
			return
		}
		if r.Method == http.MethodPost {
			h.create(w, r)
			return
		}
		method(w, "GET, POST")
		return
	}
	parts := strings.Split(tail, "/")
	if len(parts) > 2 {
		errorJSON(w, 404, "automation_agent_not_found")
		return
	}
	id, ok := parseID(parts[0])
	if !ok {
		errorJSON(w, 404, "automation_agent_not_found")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.detail(w, r, id)
		case http.MethodPatch:
			h.update(w, r, id)
		case http.MethodDelete:
			h.archive(w, r, id)
		default:
			method(w, "GET, PATCH, DELETE")
		}
		return
	}
	switch parts[1] {
	case "copy":
		if r.Method == http.MethodPost {
			h.copy(w, r, id)
		} else {
			method(w, "POST")
		}
	case "pause":
		if r.Method == http.MethodPost {
			h.status(w, r, id, automationport.AgentStatusPaused)
		} else {
			method(w, "POST")
		}
	case "publish":
		if r.Method == http.MethodPost {
			h.publish(w, r, id)
		} else {
			method(w, "POST")
		}
	case "fixed-content":
		if r.Method == http.MethodPut {
			h.fixed(w, r, id)
		} else {
			method(w, "PUT")
		}
	case "precheck":
		if r.Method == http.MethodGet {
			h.precheck(w, r, id)
		} else {
			method(w, "GET")
		}
	case "activate":
		if r.Method == http.MethodPost {
			h.status(w, r, id, automationport.AgentStatusActive)
		} else {
			method(w, "POST")
		}
	default:
		errorJSON(w, 404, "automation_agent_not_found")
	}
}
func role(p accessdomain.Principal, write bool) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, r := range p.Roles {
		if write && (r == accessdomain.RoleAdmin || r == accessdomain.RoleSuperAdmin) {
			return true
		}
		if !write && (r == accessdomain.RoleViewer || r == accessdomain.RoleAdmin || r == accessdomain.RoleSuperAdmin) {
			return true
		}
	}
	return false
}
func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		errorJSON(w, 401, "unauthorized")
		return false
	}
	if !role(p, false) {
		errorJSON(w, 403, "forbidden")
		return false
	}
	return true
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
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
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if !h.read(w, r) {
		return
	}
	kind := automationport.AutomationType(r.URL.Query().Get("automation_type"))
	page, e := h.service.List(r.Context(), kind)
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": summaries(page.Items), "total": page.Total})
}
func (h *Handler) detail(w http.ResponseWriter, r *http.Request, id automationport.AgentID) {
	if !h.read(w, r) {
		return
	}
	a, e := h.service.Get(r.Context(), id)
	if e != nil {
		resultError(w, e)
		return
	}
	detail := detailDTO(a)
	if h.published != nil {
		published, found, readErr := h.published.PublishedAgent(r.Context(), id)
		if readErr != nil {
			resultError(w, readErr)
			return
		}
		if found {
			combined := sha256.Sum256(append(append([]byte{}, published.ContentDigest[:]...), published.MaterialsDigest[:]...))
			detail["published_version"] = published.PublishedVersion
			detail["published_digest"] = hex.EncodeToString(combined[:])
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": detail})
}

type input struct {
	AgentName      *string                             `json:"agent_name"`
	AgentCode      string                              `json:"agent_code"`
	AutomationType *automationport.AutomationType      `json:"automation_type"`
	Status         *automationport.AgentStatus         `json:"status"`
	RolePrompt     *string                             `json:"role_prompt"`
	TaskPrompt     *string                             `json:"task_prompt"`
	Fixed          *automationport.FixedContentPackage `json:"fixed_content_package"`
	Legacy         *json.RawMessage                    `json:"legacy_configuration"`
	Content        *automationport.FixedContentPackage `json:"content_package"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	var b input
	if decode(r, &b) != nil || b.AgentName == nil || b.AutomationType == nil {
		errorJSON(w, 400, "invalid_agent_payload")
		return
	}
	idempotencyKey, keyOK := requestKey(w, r)
	if !keyOK {
		return
	}
	a, e := h.service.Create(r.Context(), automationport.CreateCommand{Actor: p.InternalID, IdempotencyKey: idempotencyKey, Agent: automationport.Agent{AgentName: *b.AgentName, AgentCode: b.AgentCode, AutomationType: *b.AutomationType, Status: automationport.AgentStatusPaused, DraftRolePrompt: value(b.RolePrompt), DraftTaskPrompt: value(b.TaskPrompt), FixedContentPackage: fixed(b.Fixed), LegacyConfiguration: raw(b.Legacy)}})
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": detailDTO(a)})
}
func (h *Handler) update(w http.ResponseWriter, r *http.Request, id automationport.AgentID) {
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	var b input
	if decode(r, &b) != nil {
		errorJSON(w, 400, "invalid_agent_payload")
		return
	}
	idempotencyKey, keyOK := requestKey(w, r)
	if !keyOK {
		return
	}
	a, e := h.service.Update(r.Context(), automationport.UpdateCommand{ID: id, AgentName: b.AgentName, AutomationType: b.AutomationType, Status: b.Status, RolePrompt: b.RolePrompt, TaskPrompt: b.TaskPrompt, FixedContentPackage: b.Fixed, LegacyConfiguration: b.Legacy, Actor: p.InternalID, IdempotencyKey: idempotencyKey})
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": detailDTO(a)})
}
func (h *Handler) copy(w http.ResponseWriter, r *http.Request, id automationport.AgentID) {
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	idempotencyKey, keyOK := requestKey(w, r)
	if !keyOK {
		return
	}
	a, e := h.service.Copy(r.Context(), automationport.MutationCommand{ID: id, Actor: p.InternalID, IdempotencyKey: idempotencyKey})
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": detailDTO(a)})
}
func (h *Handler) status(w http.ResponseWriter, r *http.Request, id automationport.AgentID, status automationport.AgentStatus) {
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	idempotencyKey, keyOK := requestKey(w, r)
	if !keyOK {
		return
	}
	a, e := h.service.SetStatus(r.Context(), automationport.MutationCommand{ID: id, Actor: p.InternalID, IdempotencyKey: idempotencyKey}, status)
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": detailDTO(a)})
}
func (h *Handler) archive(w http.ResponseWriter, r *http.Request, id automationport.AgentID) {
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	idempotencyKey, keyOK := requestKey(w, r)
	if !keyOK {
		return
	}
	a, e := h.service.SetStatus(r.Context(), automationport.MutationCommand{ID: id, Actor: p.InternalID, IdempotencyKey: idempotencyKey}, automationport.AgentStatusArchived)
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": map[string]any{"id": a.ID, "status": "archived"}})
}
func (h *Handler) publish(w http.ResponseWriter, r *http.Request, id automationport.AgentID) {
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	idempotencyKey, keyOK := requestKey(w, r)
	if !keyOK {
		return
	}
	a, e := h.service.Publish(r.Context(), automationport.MutationCommand{ID: id, Actor: p.InternalID, IdempotencyKey: idempotencyKey})
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": detailDTO(a)})
}
func (h *Handler) fixed(w http.ResponseWriter, r *http.Request, id automationport.AgentID) {
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	var b input
	if decode(r, &b) != nil || b.Content == nil {
		errorJSON(w, 400, "invalid_agent_payload")
		return
	}
	idempotencyKey, keyOK := requestKey(w, r)
	if !keyOK {
		return
	}
	a, e := h.service.SaveFixedContent(r.Context(), automationport.FixedContentCommand{ID: id, ContentPackage: *b.Content, Actor: p.InternalID, IdempotencyKey: idempotencyKey})
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": detailDTO(a)})
}
func (h *Handler) precheck(w http.ResponseWriter, r *http.Request, id automationport.AgentID) {
	if !h.read(w, r) {
		return
	}
	a, e := h.service.Get(r.Context(), id)
	if e != nil {
		resultError(w, e)
		return
	}
	configured := strings.TrimSpace(a.DraftRolePrompt) != "" && strings.TrimSpace(a.DraftTaskPrompt) != ""
	materials := materialsConfigured(a)
	published := a.DraftVersion == a.PublishedVersion
	reasons := []string{}
	if !configured {
		reasons = append(reasons, "prompt_unconfigured")
	}
	if !materials {
		reasons = append(reasons, "material_unconfigured")
	}
	if !published {
		reasons = append(reasons, "unpublished_changes")
	}
	if a.Status == automationport.AgentStatusActive {
		reasons = append(reasons, "already_active")
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent_id": id, "configuration_ready": configured, "materials_configured": materials, "execution_enabled": a.ExecutionEnabled, "can_activate": configured && materials && published && a.Status == automationport.AgentStatusPaused, "reasons": reasons, "real_external_call_executed": false})
}
func summaries(items []automationport.Agent) []any {
	out := make([]any, 0, len(items))
	for _, a := range items {
		out = append(out, summary(a))
	}
	return out
}
func summary(a automationport.Agent) map[string]any {
	return map[string]any{"id": a.ID, "automation_type": a.AutomationType, "agent_code": a.AgentCode, "agent_name": a.AgentName, "bound_package_key": "", "bound_package_id": nil, "bound_package_name": "", "fixed_material_summary": materialSummary(a.FixedContentPackage), "status": a.Status, "execution_enabled": a.ExecutionEnabled, "materials_configured": materialsConfigured(a), "updated_at": a.UpdatedAt.UTC().Format(time.RFC3339)}
}

func materialSummary(content automationport.FixedContentPackage) map[string]int {
	return map[string]int{"image_count": len(content.ImageLibraryIDs), "miniprogram_count": len(content.MiniprogramLibraryIDs), "attachment_count": len(content.AttachmentLibraryIDs), "group_invite_count": len(content.GroupInviteLibraryIDs)}
}

func materialsConfigured(a automationport.Agent) bool {
	return a.AutomationType != automationport.AutomationTypeFixedScript || a.FixedContentPackage.ContentText != "" || len(a.FixedContentPackage.ImageLibraryIDs) != 0 || len(a.FixedContentPackage.AttachmentLibraryIDs) != 0
}
func detailDTO(a automationport.Agent) map[string]any {
	x := summary(a)
	x["automation_type_label"] = map[bool]string{true: "固定话术", false: "Agent 机器人"}[a.AutomationType == automationport.AutomationTypeFixedScript]
	x["draft_role_prompt"] = a.DraftRolePrompt
	x["draft_task_prompt"] = a.DraftTaskPrompt
	x["published_role_prompt"] = a.PublishedRolePrompt
	x["published_task_prompt"] = a.PublishedTaskPrompt
	x["draft_version"] = a.DraftVersion
	x["published_version"] = a.PublishedVersion
	x["has_unpublished_changes"] = a.DraftVersion != a.PublishedVersion
	x["fixed_content_package"] = a.FixedContentPackage
	x["fixed_content_package_preview"] = map[string]any{"content_text": a.FixedContentPackage.ContentText, "material_summary": x["fixed_material_summary"], "materials": []any{}}
	var legacy any = map[string]any{}
	_ = json.Unmarshal(a.LegacyConfiguration, &legacy)
	x["legacy_configuration"] = legacy
	return x
}
func fixed(v *automationport.FixedContentPackage) automationport.FixedContentPackage {
	if v == nil {
		return automationport.FixedContentPackage{}
	}
	return *v
}
func raw(v *json.RawMessage) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	return *v
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func parseID(s string) (automationport.AgentID, bool) {
	v, e := strconv.ParseInt(s, 10, 64)
	return automationport.AgentID(v), e == nil && v > 0 && strconv.FormatInt(v, 10) == s
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 128<<10))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func requestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	value, err := key(r)
	if err != nil {
		errorJSON(w, http.StatusServiceUnavailable, "idempotency_unavailable")
		return "", false
	}
	return value, true
}

func key(r *http.Request) (string, error) {
	v := r.Header.Get("Idempotency-Key")
	if len(v) >= 16 && len(v) <= 128 && strings.TrimSpace(v) == v {
		return v, nil
	}
	return compatibilityIdempotencyKey(rand.Read)
}

func compatibilityIdempotencyKey(read func([]byte) (int, error)) (string, error) {
	var b [20]byte
	if _, err := read(b[:]); err != nil {
		return "", err
	}
	return "server_compat_" + hex.EncodeToString(b[:]), nil
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	errorJSON(w, 405, "method_not_allowed")
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errorJSON(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}
func resultError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, automationapp.ErrAgentNotFound):
		errorJSON(w, 404, "automation_agent_not_found")
	case errors.Is(e, automationapp.ErrAgentConflict):
		errorJSON(w, 409, "automation_agent_conflict")
	case errors.Is(e, automationapp.ErrAgentExecutionDisabled):
		errorJSON(w, 410, "automation_execution_disabled")
	case errors.Is(e, automationapp.ErrInvalidAgent):
		errorJSON(w, 400, "invalid_agent_payload")
	default:
		errorJSON(w, 503, "automation_agent_unavailable")
	}
}
