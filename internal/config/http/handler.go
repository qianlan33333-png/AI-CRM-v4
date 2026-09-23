// Package http exposes only the frozen PR09 local-configuration compatibility
// endpoints.  It has no Provider client and never applies runtime secrets.
package http

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	configcatalog "github.com/qianlan33333-png/AI-CRM-v3/internal/config"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Handler struct {
	models       configport.AIModelSettings
	settings     settingsService
	wizard       wizardService
	config       configport.Service
	projections  projectionReader
	runtime      configport.RuntimeReleaseApplication
	security     RequestSecurity
	actionMu     sync.Mutex
	actionGrants map[string]actionGrant
	now          func() time.Time
}

// actionGrant is deliberately memory-only.  It holds hashes instead of raw
// session material and makes an otherwise self-contained HMAC proof one-time.
type actionGrant struct {
	principal [32]byte
	session   [32]byte
	action    string
	path      string
	expiresAt int64
}

type settingsService interface {
	List(context.Context, configapp.SettingsListInput) (configapp.SettingsProjection, error)
	Save(context.Context, configapp.SaveSettingsInput) error
}
type wizardService interface {
	Get(context.Context) (configapp.SetupWizardSnapshot, error)
	Save(context.Context, configapp.SetupWizardSaveInput) (configapp.SetupWizardSaveResult, error)
}
type projectionReader = configport.SafeProjectionReader

func NewHandler(settings settingsService, wizard wizardService, configService configport.Service, projections projectionReader, security RequestSecurity, runtime ...configport.RuntimeReleaseApplication) (*Handler, error) {
	if settings == nil || wizard == nil || configService == nil || projections == nil || security == nil || len(runtime) > 1 {
		return nil, errors.New("config HTTP dependencies are required")
	}
	var runtimeReleases configport.RuntimeReleaseApplication
	if len(runtime) == 1 {
		runtimeReleases = runtime[0]
	}
	return &Handler{settings: settings, wizard: wizard, config: configService, projections: projections, runtime: runtimeReleases, security: security, actionGrants: map[string]actionGrant{}, now: time.Now}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/setup-wizard" {
		h.setupWizard(w, r)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/config/"), "/")
	switch path {
	case "app-settings":
		h.appSettings(w, r)
	case "setup-wizard":
		h.setupWizard(w, r)
	case "ai-model":
		h.aiModel(w, r)
	case "categories":
		h.categories(w, r)
	case "push-capabilities":
		h.pushCapabilities(w, r)
	case "releases":
		h.releases(w, r)
	case "runtime-releases":
		h.runtimeReleases(w, r)
	case "runtime-releases/legacy-binary-recovery":
		h.runtimeLegacyBinaryRecovery(w, r)
	case "runtime-catalog":
		h.runtimeCatalog(w, r)
	case "diagnostics":
		h.diagnostics(w, r)
	case "openapi.yaml":
		h.openapi(w, r)
	default:
		if strings.HasPrefix(path, "runtime-releases/") {
			h.runtimeRelease(w, r, strings.TrimPrefix(path, "runtime-releases/"))
			return
		}
		if strings.HasPrefix(path, "categories/") {
			h.category(w, r, strings.TrimPrefix(path, "categories/"))
			return
		}
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func readRole(p accessdomain.Principal) bool {
	return p.InternalID > 0 && (p.Kind == accessdomain.KindAdmin || p.Kind == accessdomain.KindStaff) && has(p, accessdomain.RoleViewer, accessdomain.RoleAdmin, accessdomain.RoleSuperAdmin)
}
func writeRole(p accessdomain.Principal) bool {
	return readRole(p) && has(p, accessdomain.RoleAdmin, accessdomain.RoleSuperAdmin)
}
func has(p accessdomain.Principal, want ...accessdomain.Role) bool {
	for _, r := range p.Roles {
		for _, x := range want {
			if r == x {
				return true
			}
		}
	}
	return false
}
func (h *Handler) read(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		writeError(w, 401, "unauthorized")
		return p, false
	}
	if !readRole(p) {
		writeError(w, 403, "forbidden")
		return p, false
	}
	return p, true
}
func (h *Handler) mutate(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, ok := h.read(w, r)
	if !ok {
		return p, false
	}
	if !writeRole(p) {
		writeError(w, 403, "forbidden")
		return p, false
	}
	if _, e := h.security.AuthorizeCSRF(r.Context(), r); e != nil {
		writeError(w, 403, "csrf_required")
		return p, false
	}
	return p, true
}

const adminSessionCookie = "aicrm_admin_session"

func principalProof(p accessdomain.Principal) [32]byte {
	roles := append([]accessdomain.Role(nil), p.Roles...)
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	parts := []string{strconv.FormatInt(p.InternalID, 10), string(p.Kind)}
	for _, role := range roles {
		parts = append(parts, string(role))
	}
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}

func sessionProof(r *http.Request) ([32]byte, bool) {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil || cookie.Value == "" {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(cookie.Value)), true
}

func (h *Handler) actionToken(r *http.Request, p accessdomain.Principal, action, path string) string {
	session, ok := sessionProof(r)
	if !ok || h.now == nil {
		return ""
	}
	now := h.now().UTC()
	if now.IsZero() {
		return ""
	}
	nonce := make([]byte, 32)
	expiresAt := now.Add(5 * time.Minute).Unix()
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	principal := principalProof(p)
	// This must stay 43 URL-safe characters: it is the frozen donor's action
	// token shape. All authorization context remains server-side in the grant.
	token := base64.RawURLEncoding.EncodeToString(nonce)
	h.actionMu.Lock()
	for candidate, grant := range h.actionGrants {
		if grant.expiresAt <= now.Unix() {
			delete(h.actionGrants, candidate)
		}
	}
	h.actionGrants[token] = actionGrant{principal: principal, session: session, action: action, path: path, expiresAt: expiresAt}
	h.actionMu.Unlock()
	return token
}
func (h *Handler) validActionToken(r *http.Request, p accessdomain.Principal, action, path, value string) bool {
	if h.now == nil || value == "" {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(value) != 43 || len(raw) != 32 {
		return false
	}
	session, ok := sessionProof(r)
	if !ok {
		return false
	}
	principal := principalProof(p)
	h.actionMu.Lock()
	defer h.actionMu.Unlock()
	grant, found := h.actionGrants[value]
	if !found || grant.expiresAt <= h.now().UTC().Unix() || grant.action != action || grant.path != path || subtle.ConstantTimeCompare(grant.principal[:], principal[:]) != 1 || subtle.ConstantTimeCompare(grant.session[:], session[:]) != 1 {
		return false
	}
	delete(h.actionGrants, value)
	return true
}
func actionFrom(r *http.Request, body string) string {
	if body != "" {
		return body
	}
	return r.Header.Get("X-Admin-Action-Token")
}

func (h *Handler) appSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, ok := h.read(w, r)
		if !ok {
			return
		}
		projection, e := h.settings.List(r.Context(), configapp.SettingsListInput{Search: r.URL.Query().Get("search"), Scope: r.URL.Query().Get("scope")})
		if e != nil {
			writeError(w, 503, "settings_unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "config": projection, "source_status": "next_read_model", "fallback_used": false, "admin_action_token": h.actionToken(r, p, "app-settings", r.URL.Path)})
	case http.MethodPut:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		enabled, enabledErr := h.categoryEnabled(r.Context(), "app-settings")
		if enabledErr != nil {
			writeError(w, 503, "settings_unavailable")
			return
		}
		if !enabled {
			writeError(w, 409, "config_category_disabled")
			return
		}
		var body struct {
			Settings map[string]json.RawMessage `json:"settings"`
			Confirm  bool                       `json:"confirm"`
			Action   string                     `json:"admin_action_token"`
			Operator json.RawMessage            `json:"operator"`
		}
		if e := decode(r, &body); e != nil {
			writeError(w, 400, "payload_must_be_object")
			return
		}
		if !body.Confirm {
			writeError(w, 400, "confirmation_required")
			return
		}
		if !h.validActionToken(r, p, "app-settings", r.URL.Path, actionFrom(r, body.Action)) {
			writeError(w, 400, "invalid_action_token")
			return
		}
		values := make(map[string][]string, len(body.Settings))
		for key, raw := range body.Settings {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				values[key] = []string{text}
				continue
			}
			var number int64
			if json.Unmarshal(raw, &number) == nil {
				values[key] = []string{strconv.FormatInt(number, 10)}
				continue
			}
			writeError(w, 400, "invalid_setting_value")
			return
		}
		key := idempotency(r)
		if key == "" {
			writeError(w, 400, "invalid_idempotency_key")
			return
		}
		if e := h.settings.Save(r.Context(), configapp.SaveSettingsInput{Values: values, Actor: strconv.FormatInt(p.InternalID, 10), RequestID: key}); e != nil {
			writeSettingsError(w, e)
			return
		}
		projection, e := h.settings.List(r.Context(), configapp.SettingsListInput{})
		if e != nil {
			writeError(w, 503, "settings_unavailable")
			return
		}
		changed := make([]map[string]any, 0, len(values))
		for key := range values {
			changed = append(changed, map[string]any{"key": key, "value": values[key][0]})
		}
		writeJSON(w, 200, map[string]any{"ok": true, "changed": changed, "changed_count": len(changed), "config": projection, "source_status": "next_command", "fallback_used": false, "real_external_call_executed": false})
	default:
		method(w, "GET, PUT")
	}
}

func (h *Handler) setupWizard(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, ok := h.read(w, r)
		if !ok {
			return
		}
		snapshot, e := h.wizard.Get(r.Context())
		if e != nil {
			writeError(w, 503, "setup_wizard_unavailable")
			return
		}
		writeJSON(w, 200, wizardRead(snapshot, h.actionToken(r, p, "setup-wizard", r.URL.Path)))
	case http.MethodPost:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			Corp          string `json:"wecom.corp_id"`
			Agent         int64  `json:"wecom.agent_id"`
			Secret        string `json:"wecom.secret"`
			CallbackToken string `json:"wecom.callback_token"`
			CallbackAES   string `json:"wecom.callback_aes_key"`
			AI            string `json:"ai.api_key"`
			Expected      string `json:"expected_digest"`
			Action        string `json:"admin_action_token"`
		}
		if e := decode(r, &body); e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !h.validActionToken(r, p, "setup-wizard", r.URL.Path, actionFrom(r, body.Action)) {
			writeError(w, 400, "invalid_action_token")
			return
		}
		key := idempotency(r)
		if key == "" {
			writeError(w, 400, "invalid_idempotency_key")
			return
		}
		result, e := h.wizard.Save(r.Context(), configapp.SetupWizardSaveInput{WeComCorpID: body.Corp, WeComAgentID: body.Agent, WeComSecret: body.Secret, WeComCallbackToken: body.CallbackToken, WeComCallbackAESKey: body.CallbackAES, AIAPIKey: body.AI, ExpectedDigest: body.Expected, Actor: strconv.FormatInt(p.InternalID, 10), IdempotencyKey: key})
		if e != nil {
			writeWizardError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "config": result.Snapshot, "receipt": result.Receipt, "external": false, "local_only": true, "runtime_applied": false})
	default:
		method(w, "GET, POST")
	}
}

func wizardRead(s configapp.SetupWizardSnapshot, token string) map[string]any {
	return map[string]any{"ok": true, "expected_digest": s.ExpectedDigest, "editable": s.Editable, "editable_configured": s.Configured, "masked": s.Masked, "admin_action_token": token, "external": false, "local_only": true, "runtime_applied": false}
}
func (h *Handler) categories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	enabled, err := h.categoryEnabled(r.Context(), "runtime-diagnostics")
	if err != nil {
		writeError(w, 503, "settings_unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": []map[string]any{{"key": "runtime-diagnostics", "name": "运行诊断", "enabled": enabled, "local_only": true}}, "source_status": "next_read_model"})
}
func (h *Handler) category(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 2 && parts[1] == "enabled" {
		h.categoryEnabledWrite(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "check" {
		h.categoryCheck(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "settings" {
		h.categorySettingsSave(w, r, parts[0])
		return
	}
	if len(parts) != 1 || r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	p, ok := h.read(w, r)
	if !ok {
		return
	}
	if !knownCategory(parts[0]) {
		writeError(w, 404, "not_found")
		return
	}
	enabled, err := h.categoryEnabled(r.Context(), parts[0])
	if err != nil {
		writeError(w, 503, "settings_unavailable")
		return
	}
	basePath := r.URL.Path
	writeJSON(w, 200, map[string]any{
		"ok": true, "key": parts[0], "enabled": enabled, "local_only": true, "runtime_applied": false,
		"admin_action_tokens": map[string]string{
			"enabled":  h.actionToken(r, p, "category:"+parts[0]+":enabled", basePath+"/enabled"),
			"check":    h.actionToken(r, p, "category:"+parts[0]+":check", basePath+"/check"),
			"settings": h.actionToken(r, p, "category:"+parts[0]+":settings", basePath+"/settings"),
		},
	})
}
func (h *Handler) pushCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	diagnostics, err := h.projections.ListDiagnosticSnapshots(r.Context())
	if err != nil || len(diagnostics) == 0 {
		writeError(w, 503, "unavailable")
		return
	}
	runtime := make([]map[string]any, 0, len(diagnostics))
	for _, item := range diagnostics {
		runtime = append(runtime, map[string]any{"id": item.ID, "key": item.Key, "status": item.Status, "observed_at": item.ObservedAt.UTC()})
	}
	// This existing donor read graph feeds the frozen Push-capabilities detail.
	// It reports persisted safe diagnostic projections, never raw details.
	writeJSON(w, 200, map[string]any{
		"ok":                          true,
		"capabilities":                map[string]any{"local_projection": map[string]any{"enabled": true, "state": "available", "local_only": true}, "runtime_diagnostics": map[string]any{"enabled": true, "state": "available", "local_only": true, "records": runtime}, "provider_write": map[string]any{"enabled": false, "state": "disabled", "local_only": true}},
		"source_status":               "local_runtime_policy",
		"local_only":                  true,
		"real_external_call_executed": false,
	})
}
func referencePresence(runtime configport.RuntimeReleaseApplication, ctx context.Context) (map[string]bool, error) {
	presence := map[string]bool{}
	statuses, err := runtime.ProtectedReferenceStatuses(ctx)
	if err != nil {
		return nil, err
	}
	for _, status := range statuses {
		presence[status.Reference] = status.Configured
	}
	return presence, nil
}

func (h *Handler) runtimeCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if h.runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime_release_unavailable")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	page, err := h.runtime.ListRuntimeReleases(r.Context(), 50)
	if err != nil {
		writeRuntimeReleaseError(w, err)
		return
	}
	applications, err := h.runtime.ListRuntimeApplications(r.Context(), 100)
	if err != nil {
		writeRuntimeReleaseError(w, err)
		return
	}
	presence, err := referencePresence(h.runtime, r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "runtime_reference_status_unavailable")
		return
	}
	// Application facts are presented separately from publication. The client
	// must compare the active revision with each required role; this endpoint
	// deliberately has no aggregate runtime_applied boolean.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "categories": configcatalog.RuntimeCatalog(presence),
		"runtime_releases": page, "effective": page.Effective,
		"applications": applications,
	})
}

func (h *Handler) runtimeReleases(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime_release_unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, ok := h.read(w, r)
		if !ok {
			return
		}
		limit := runtimeQueryLimit(r, 50)
		page, err := h.runtime.ListRuntimeReleases(r.Context(), limit)
		if err != nil {
			writeRuntimeReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "runtime_releases": page, "saved": false,
			"published_revision": page.ActiveRevision, "effective": page.Effective,
			"admin_action_token":            h.actionToken(r, p, "runtime-release:create", r.URL.Path),
			"legacy_binary_recovery_action": h.actionToken(r, p, "runtime-release:legacy-binary-recovery", "/api/admin/config/runtime-releases/legacy-binary-recovery"),
		})
	case http.MethodPost:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			ExpectedBaseRevision int64                       `json:"expected_base_revision"`
			Settings             []configport.RuntimeSetting `json:"settings"`
			Action               string                      `json:"admin_action_token"`
		}
		if err := decode(r, &body); err != nil || body.ExpectedBaseRevision < 0 || len(body.Settings) == 0 || !h.validActionToken(r, p, "runtime-release:create", r.URL.Path, actionFrom(r, body.Action)) {
			writeError(w, http.StatusBadRequest, "invalid_runtime_release_request")
			return
		}
		key := idempotency(r)
		if key == "" {
			writeError(w, http.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		out, err := h.runtime.CreateRuntimeReleaseDraft(r.Context(), configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: body.ExpectedBaseRevision, Settings: body.Settings, Actor: strconv.FormatInt(p.InternalID, 10), IdempotencyKey: key})
		if err != nil {
			writeRuntimeReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "runtime_release": out, "saved": true, "published": false, "effective": false})
	default:
		method(w, "GET, POST")
	}
}

// runtimeLegacyBinaryRecovery is a deliberately narrow compatibility action.
// It publishes no arbitrary input: the application derives the previous
// catalog's sole setting from the active snapshot and rejects divergent newer
// settings before an operator can roll the binary back.
func (h *Handler) runtimeLegacyBinaryRecovery(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime_release_unavailable")
		return
	}
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	p, ok := h.mutate(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedBaseRevision int64  `json:"expected_base_revision"`
		Action               string `json:"admin_action_token"`
	}
	if err := decode(r, &body); err != nil || body.ExpectedBaseRevision < 0 || !h.validActionToken(r, p, "runtime-release:legacy-binary-recovery", r.URL.Path, actionFrom(r, body.Action)) {
		writeError(w, http.StatusBadRequest, "invalid_runtime_release_request")
		return
	}
	key := idempotency(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	out, err := h.runtime.PrepareLegacyRuntimeRecovery(r.Context(), configport.RuntimeReleaseLegacyRecoveryCommand{ExpectedBaseRevision: body.ExpectedBaseRevision, Actor: strconv.FormatInt(p.InternalID, 10), IdempotencyKey: key})
	if err != nil {
		writeRuntimeReleaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runtime_release": out, "legacy_binary_recovery_published": true, "published_revision": out.ID})
}

func (h *Handler) runtimeRelease(w http.ResponseWriter, r *http.Request, rest string) {
	if h.runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime_release_unavailable")
		return
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		p, ok := h.read(w, r)
		if !ok {
			return
		}
		out, err := h.runtime.RuntimeRelease(r.Context(), id)
		if err != nil {
			writeRuntimeReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runtime_release": out, "actions": map[string]string{
			"validate": h.actionToken(r, p, "runtime-release:validate", r.URL.Path+"/validate"),
			"publish":  h.actionToken(r, p, "runtime-release:publish", r.URL.Path+"/publish"),
			"rollback": h.actionToken(r, p, "runtime-release:rollback", r.URL.Path+"/rollback"),
		}})
		return
	}
	if len(parts) == 2 && parts[1] == "usage" {
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if _, ok := h.read(w, r); !ok {
			return
		}
		items, err := h.runtime.ListRuntimeUsage(r.Context(), id, runtimeQueryLimit(r, 50))
		if err != nil {
			writeRuntimeReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": id, "usage": items})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	p, ok := h.mutate(w, r)
	if !ok {
		return
	}
	key := idempotency(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	path := r.URL.Path
	switch parts[1] {
	case "validate":
		var body struct {
			Action string `json:"admin_action_token"`
		}
		if err := decode(r, &body); err != nil || !h.validActionToken(r, p, "runtime-release:validate", path, actionFrom(r, body.Action)) {
			writeError(w, http.StatusBadRequest, "invalid_runtime_release_request")
			return
		}
		out, err := h.runtime.ValidateRuntimeRelease(r.Context(), configport.RuntimeReleaseMutationCommand{ReleaseID: id, Actor: strconv.FormatInt(p.InternalID, 10), IdempotencyKey: key})
		if err != nil {
			writeRuntimeReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runtime_release": out, "validated": out.State == configport.RuntimeReleaseValidated})
	case "publish":
		var body struct {
			ExpectedBaseRevision int64  `json:"expected_base_revision"`
			ExpectedChecksum     string `json:"expected_checksum"`
			Action               string `json:"admin_action_token"`
		}
		if err := decode(r, &body); err != nil || body.ExpectedBaseRevision < 0 || !h.validActionToken(r, p, "runtime-release:publish", path, actionFrom(r, body.Action)) {
			writeError(w, http.StatusBadRequest, "invalid_runtime_release_request")
			return
		}
		out, err := h.runtime.PublishRuntimeRelease(r.Context(), configport.RuntimeReleasePublishCommand{ReleaseID: id, ExpectedBaseRevision: body.ExpectedBaseRevision, ExpectedChecksum: body.ExpectedChecksum, Actor: strconv.FormatInt(p.InternalID, 10), IdempotencyKey: key})
		if err != nil {
			writeRuntimeReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runtime_release": out, "published": true, "published_revision": out.ID})
	case "rollback":
		var body struct {
			ExpectedBaseRevision int64  `json:"expected_base_revision"`
			Action               string `json:"admin_action_token"`
		}
		if err := decode(r, &body); err != nil || body.ExpectedBaseRevision < 0 || !h.validActionToken(r, p, "runtime-release:rollback", path, actionFrom(r, body.Action)) {
			writeError(w, http.StatusBadRequest, "invalid_runtime_release_request")
			return
		}
		out, err := h.runtime.RollbackRuntimeRelease(r.Context(), configport.RuntimeReleaseRollbackCommand{ReleaseID: id, ExpectedBaseRevision: body.ExpectedBaseRevision, Actor: strconv.FormatInt(p.InternalID, 10), IdempotencyKey: key})
		if err != nil {
			writeRuntimeReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runtime_release": out, "rolled_back": true, "published_revision": out.ID})
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func writeRuntimeReleaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, configport.ErrRuntimeReleaseNotFound):
		writeError(w, http.StatusNotFound, "runtime_release_not_found")
	case errors.Is(err, configport.ErrRuntimeReleaseConflict):
		writeError(w, http.StatusConflict, "runtime_release_conflict")
	case errors.Is(err, configport.ErrRuntimeReleaseInvalid):
		writeError(w, http.StatusBadRequest, "invalid_runtime_release_request")
	default:
		writeError(w, http.StatusServiceUnavailable, "runtime_release_unavailable")
	}
}

func (h *Handler) releases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	items, err := h.projections.ListReleaseProjections(r.Context())
	if err != nil || len(items) == 0 {
		writeError(w, 503, "unavailable")
		return
	}
	releases := make([]map[string]any, 0, len(items))
	for _, item := range items {
		releases = append(releases, map[string]any{"id": item.ID, "state": item.Status, "checksum": item.ReleaseSHA})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "releases": releases, "source_status": "next_read_model", "local_only": true, "real_external_call_executed": false})
}
func (h *Handler) diagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	items, err := h.projections.ListDiagnosticSnapshots(r.Context())
	if err != nil || len(items) == 0 {
		writeError(w, 503, "unavailable")
		return
	}
	diagnostics := make([]map[string]any, 0, len(items))
	for _, item := range items {
		diagnostics = append(diagnostics, map[string]any{"id": item.ID, "key": item.Key, "status": item.Status, "observed_at": item.ObservedAt.UTC()})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "diagnostics": diagnostics, "source_status": "next_read_model", "local_only": true, "real_external_call_executed": false})
}

var categorySettings = map[string]configport.Key{
	"app-settings":        configport.AdminAppSettingsEnabled,
	"push-capabilities":   configport.AdminPushCapabilitiesEnabled,
	"releases":            configport.AdminReleasesEnabled,
	"runtime-diagnostics": configport.AdminDiagnosticsEnabled,
}

func knownCategory(key string) bool {
	_, ok := categorySettings[key]
	return ok
}

func (h *Handler) categoryEnabled(ctx context.Context, category string) (bool, error) {
	key, ok := categorySettings[category]
	if !ok {
		return false, configport.ErrUnknownSetting
	}
	setting, err := h.config.Get(ctx, key)
	if errors.Is(err, configport.ErrSettingNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var enabled bool
	if err := json.Unmarshal(setting.Value, &enabled); err != nil {
		return false, err
	}
	return enabled, nil
}

func (h *Handler) categoryEnabledWrite(w http.ResponseWriter, r *http.Request, category string) {
	if r.Method != http.MethodPut {
		method(w, "PUT")
		return
	}
	p, ok := h.mutate(w, r)
	if !ok {
		return
	}
	key, known := categorySettings[category]
	if !known {
		writeError(w, 404, "not_found")
		return
	}
	var body struct {
		Enabled bool   `json:"enabled"`
		Action  string `json:"admin_action_token"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	if !h.validActionToken(r, p, "category:"+category+":enabled", r.URL.Path, actionFrom(r, body.Action)) {
		writeError(w, 400, "invalid_action_token")
		return
	}
	requestID := idempotency(r)
	if requestID == "" {
		writeError(w, 400, "invalid_idempotency_key")
		return
	}
	// A category operation has one setting value; Manager.Set's audit receipt,
	// setting update and outbox are consequently one transaction. Hashing keeps
	// the per-setting receipt under the database's bounded request-id contract.
	digest := sha256.Sum256([]byte("category-enabled:" + category + ":" + requestID))
	commandID := "category:" + category + ":" + base64.RawURLEncoding.EncodeToString(digest[:18])
	value, _ := json.Marshal(body.Enabled)
	if _, err := h.config.Set(r.Context(), configport.SetCommand{Key: key, Value: value, Actor: strconv.FormatInt(p.InternalID, 10), RequestID: commandID}); err != nil {
		writeSettingsError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "key": category, "enabled": body.Enabled, "local_only": true, "runtime_applied": false, "real_external_call_executed": false})
}

// categorySettingsSave is a deliberately narrow compatibility endpoint for
// frozen category detail pages.  Those v2 pages show a Save button even for
// the three v3-owned readonly categories.  There are no editable values to
// reinterpret, so Save durably records the currently selected category state
// through the normal Config owner instead of silently turning it into Check.
func (h *Handler) categorySettingsSave(w http.ResponseWriter, r *http.Request, category string) {
	if r.Method != http.MethodPut {
		method(w, "PUT")
		return
	}
	p, ok := h.mutate(w, r)
	if !ok {
		return
	}
	key, known := categorySettings[category]
	if !known || category == "app-settings" {
		writeError(w, 404, "not_found")
		return
	}
	var body struct {
		Settings map[string]bool `json:"settings"`
		Action   string          `json:"admin_action_token"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	if body.Settings == nil || len(body.Settings) > 1 {
		writeError(w, 400, "readonly_category")
		return
	}
	if _, found := body.Settings["enabled"]; !found && len(body.Settings) != 0 {
		writeError(w, 400, "readonly_category")
		return
	}
	if !h.validActionToken(r, p, "category:"+category+":settings", r.URL.Path, actionFrom(r, body.Action)) {
		writeError(w, 400, "invalid_action_token")
		return
	}
	requestID := idempotency(r)
	if requestID == "" {
		writeError(w, 400, "invalid_idempotency_key")
		return
	}
	enabled, err := h.categoryEnabled(r.Context(), category)
	if err != nil {
		writeError(w, 503, "settings_unavailable")
		return
	}
	if requested, found := body.Settings["enabled"]; found {
		enabled = requested
	}
	digest := sha256.Sum256([]byte("category-settings:" + category + ":" + requestID))
	commandID := "category-settings:" + category + ":" + base64.RawURLEncoding.EncodeToString(digest[:18])
	value, _ := json.Marshal(enabled)
	if _, err = h.config.Set(r.Context(), configport.SetCommand{Key: key, Value: value, Actor: strconv.FormatInt(p.InternalID, 10), RequestID: commandID}); err != nil {
		writeSettingsError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "key": category, "enabled": enabled, "saved": true, "local_only": true, "runtime_applied": false, "real_external_call_executed": false})
}

func (h *Handler) categoryCheck(w http.ResponseWriter, r *http.Request, category string) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	p, ok := h.mutate(w, r)
	if !ok {
		return
	}
	if !knownCategory(category) {
		writeError(w, 404, "not_found")
		return
	}
	var body struct {
		Action string `json:"admin_action_token"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	if !h.validActionToken(r, p, "category:"+category+":check", r.URL.Path, actionFrom(r, body.Action)) {
		writeError(w, 400, "invalid_action_token")
		return
	}
	enabled, err := h.categoryEnabled(r.Context(), category)
	if err != nil {
		writeError(w, 503, "settings_unavailable")
		return
	}
	if !enabled {
		writeJSON(w, 200, map[string]any{"ok": true, "message": "检查发现：该本地能力已停用", "local_only": true, "real_external_call_executed": false})
		return
	}
	var message string
	switch category {
	case "app-settings":
		if _, err = h.settings.List(r.Context(), configapp.SettingsListInput{}); err == nil {
			message = "检查通过，应用设置读取正常"
		}
	case "push-capabilities":
		var items []configport.DiagnosticProjection
		items, err = h.projections.ListDiagnosticSnapshots(r.Context())
		if err == nil && len(items) == 0 {
			err = errors.New("diagnostic projection missing")
		}
		message = "检查通过，Push 能力安全投影可读取；Provider 写入保持禁用"
	case "releases":
		var items []configport.ReleaseProjection
		items, err = h.projections.ListReleaseProjections(r.Context())
		if err == nil && len(items) == 0 {
			err = errors.New("release projection missing")
		}
		message = "检查通过，发布记录安全投影可读取"
	case "runtime-diagnostics":
		var items []configport.DiagnosticProjection
		items, err = h.projections.ListDiagnosticSnapshots(r.Context())
		if err == nil && len(items) == 0 {
			err = errors.New("diagnostic projection missing")
		}
		message = "检查通过，运行诊断安全投影可读取"
	}
	if err != nil {
		writeError(w, 503, "unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": message, "local_only": true, "real_external_call_executed": false})
}

func runtimeQueryLimit(r *http.Request, fallback int) int {
	if r == nil || fallback < 1 {
		return fallback
	}
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 100 {
		return fallback
	}
	return value
}

func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func idempotency(r *http.Request) string {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return ""
	}
	key := values[0]
	if key == "" || strings.TrimSpace(key) != key || len(key) > 200 {
		return ""
	}
	return key
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "method_not_allowed")
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}
func writeSettingsError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, configport.ErrIdempotencyConflict):
		writeError(w, 409, "settings_idempotency_conflict")
	case errors.Is(e, configport.ErrSecretSetting):
		writeError(w, 400, "secret_input_forbidden")
	case errors.Is(e, configport.ErrInvalidSetting), errors.Is(e, configapp.ErrInvalidAppSettingsRequest):
		writeError(w, 400, "invalid_setting")
	default:
		writeError(w, 503, "settings_unavailable")
	}
}
func writeWizardError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, configapp.ErrSetupWizardConflict), errors.Is(e, configport.ErrIdempotencyConflict):
		writeError(w, 409, "setup_wizard_conflict")
	case errors.Is(e, configport.ErrSecretSetting):
		writeError(w, 400, "secret_input_forbidden")
	case errors.Is(e, configport.ErrInvalidSetting), errors.Is(e, configapp.ErrInvalidSetupWizardRequest):
		writeError(w, 400, "invalid_setting")
	default:
		writeError(w, 503, "setup_wizard_unavailable")
	}
}
