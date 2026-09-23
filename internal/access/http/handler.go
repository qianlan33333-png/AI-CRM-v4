// Package http exposes access-owned endpoints without importing webshell.
package http

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

const (
	SessionCookieName    = "aicrm_admin_session"
	CSRFCookieName       = "aicrm_admin_csrf"
	CompatCSRFCookieName = "aicrm_csrf"
	LoginCSRFCookieName  = "aicrm_login_csrf"
	loginCSRFTTL         = 10 * time.Minute
)

type Renderer interface {
	Render(context.Context, nethttp.ResponseWriter, int, string, map[string]any) error
}

type Authentication interface {
	Login(context.Context, app.LoginCommand) (app.IssuedSession, error)
	Authenticate(context.Context, string) (domain.Principal, error)
	AuthorizeCSRF(context.Context, string, string, string) (domain.Principal, error)
	Logout(context.Context, string, string, string) error
}

type Management interface {
	ListUsers(context.Context, domain.Principal) ([]app.UserSummary, error)
	ListGovernance(context.Context, domain.Principal) (app.GovernanceListing, error)
	ListEnterpriseEmployees(context.Context, domain.Principal, string, string, int) (app.EnterpriseEmployeeListing, error)
	SetLoginAccess(context.Context, domain.Principal, string, []app.LoginAccessChange) ([]app.UserSummary, error)
	ProvisionEnterpriseEmployee(context.Context, domain.Principal, app.ProvisionEnterpriseEmployeeInput) (domain.User, error)
	SetGovernanceLoginEnabled(context.Context, domain.Principal, app.SetLoginEnabledInput) error
	SetGovernanceRole(context.Context, domain.Principal, app.SetRoleInput) error
	BindGovernanceWeComUserID(context.Context, domain.Principal, app.BindEnterpriseEmployeeInput) error
	ResetGovernancePassword(context.Context, domain.Principal, app.ResetPasswordInput) error
	TransferGovernanceSuperAdmin(context.Context, domain.Principal, app.TransferSuperAdminInput) error
}

type Config struct {
	Renderer     Renderer
	Auth         Authentication
	Management   Management
	CookieSecure bool
	CookiePath   string
}

type Handler struct {
	renderer     Renderer
	auth         Authentication
	management   Management
	cookieSecure bool
	cookiePath   string
}

func NewHandler(config Config) (*Handler, error) {
	if config.Renderer == nil || config.Auth == nil || config.Management == nil {
		return nil, errors.New("access HTTP dependencies are required")
	}
	if !config.CookieSecure {
		return nil, errors.New("access session cookies must be Secure")
	}
	if config.CookiePath == "" {
		config.CookiePath = "/"
	}
	if config.CookiePath[0] != '/' {
		return nil, errors.New("access cookie path must be absolute")
	}
	return &Handler{renderer: config.Renderer, auth: config.Auth, management: config.Management,
		cookieSecure: config.CookieSecure, cookiePath: config.CookiePath}, nil
}

func (handler *Handler) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /login", handler.loginPage)
	mux.HandleFunc("POST /login", handler.login)
	mux.HandleFunc("POST /logout", handler.logout)
	mux.HandleFunc("GET /api/admin/access/users", handler.listUsers)
	mux.HandleFunc("GET /api/admin/access/enterprise-employees", handler.listEnterpriseEmployees)
	mux.HandleFunc("POST /api/admin/access/users/refresh-names", handler.refreshStaffNames)
	mux.HandleFunc("POST /api/admin/access/super-admin-transfer", handler.transferSuperAdmin)
	mux.HandleFunc("PUT /api/admin/access/users/{id}/login-access", handler.setLoginEnabled)
	mux.HandleFunc("PUT /api/admin/access/users/{id}/role", handler.setRole)
	mux.HandleFunc("PUT /api/admin/access/users/{id}/wecom-userid", handler.bindWeCom)
	mux.HandleFunc("PUT /api/admin/access/users/{id}/password", handler.resetPassword)
	// The frozen PR09 AdminOps bundle calls this compatibility path. It stays
	// access-owned so auth, CSRF, transactions, session fencing, and audits are
	// identical to the canonical access API.
	mux.HandleFunc("GET /api/admin/admin-access", handler.adminAccess)
	mux.HandleFunc("PUT /api/admin/admin-access", handler.adminAccess)
	mux.HandleFunc("POST /api/admin/access/users", handler.addUser)
	mux.HandleFunc("POST /api/admin/access/users/{id}/disable", handler.disableUser)
	mux.HandleFunc("POST /api/admin/access/users/{id}/wecom-userid", handler.bindWeCom)
	mux.HandleFunc("POST /api/admin/access/users/{id}/roles", handler.changeRoles)
	mux.HandleFunc("POST /api/admin/access/users/{id}/password", handler.resetPassword)
	return mux
}

func (handler *Handler) adminAccess(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "no-store")
	switch request.Method {
	case nethttp.MethodGet:
		var session string
		if cookie, err := request.Cookie(SessionCookieName); err == nil {
			session = cookie.Value
		}
		actor, err := handler.auth.Authenticate(request.Context(), session)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		users, err := handler.management.ListUsers(request.Context(), actor)
		if err != nil {
			handler.writeAdminAccessError(response, request, err)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, nethttp.StatusOK, adminAccessRead(users))
	case nethttp.MethodPut:
		actor, payload, ok := handler.authorizedPayload(response, request)
		if !ok {
			return
		}
		changes, err := parseAdminAccessChanges(payload)
		if err != nil {
			writeJSON(response, nethttp.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_request"})
			return
		}
		key, err := adminAccessIdempotencyKey(request)
		if err != nil {
			writeJSON(response, nethttp.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_request"})
			return
		}
		users, err := handler.management.SetLoginAccess(request.Context(), actor, key, changes)
		if err != nil {
			handler.writeAdminAccessError(response, request, err)
			return
		}
		result := adminAccessRead(users)
		result["idempotency_key"] = key
		writeJSON(response, nethttp.StatusOK, result)
	default:
		response.Header().Set("Allow", nethttp.MethodGet+", "+nethttp.MethodPut)
		writeJSON(response, nethttp.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "invalid_request"})
	}
}

func adminAccessRead(users []app.UserSummary) map[string]any {
	members := make([]map[string]any, 0, len(users))
	for _, user := range users {
		members = append(members, map[string]any{
			"admin_user_id": user.ID, "display_name": user.DisplayName,
			"role": adminAccessRole(user.Roles), "staff_id": nil,
			"staff_wecom_userid": user.WeComUserID, "staff_name": user.DisplayName,
			"is_active": user.Active, "login_enabled": user.LoginEnabled,
		})
	}
	return map[string]any{"ok": true, "members": members, "local_only": true, "external": false}
}

func adminAccessRole(roles []domain.Role) string {
	for _, expected := range []domain.Role{domain.RoleSuperAdmin, domain.RoleAdmin, domain.RoleViewer} {
		for _, role := range roles {
			if role == expected {
				return string(role)
			}
		}
	}
	return ""
}

func parseAdminAccessChanges(payload map[string]any) ([]app.LoginAccessChange, error) {
	if len(payload) != 1 {
		return nil, domain.ErrInvalidInput
	}
	raw, ok := payload["members"].([]any)
	if !ok || len(raw) == 0 || len(raw) > 200 {
		return nil, domain.ErrInvalidInput
	}
	changes := make([]app.LoginAccessChange, 0, len(raw))
	for _, value := range raw {
		member, ok := value.(map[string]any)
		if !ok || len(member) != 2 {
			return nil, domain.ErrInvalidInput
		}
		id, validID := member["admin_user_id"].(float64)
		enabled, validEnabled := member["login_enabled"].(bool)
		if !validID || id < 1 || id != float64(int64(id)) || !validEnabled {
			return nil, domain.ErrInvalidInput
		}
		changes = append(changes, app.LoginAccessChange{AdminUserID: int64(id), LoginEnabled: enabled})
	}
	return changes, nil
}

func adminAccessIdempotencyKey(request *nethttp.Request) (string, error) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", domain.ErrInvalidInput
	}
	key := strings.TrimSpace(values[0])
	if key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return "", domain.ErrInvalidInput
	}
	return key, nil
}

func (handler *Handler) writeAdminAccessError(response nethttp.ResponseWriter, request *nethttp.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrAuthentication), errors.Is(err, domain.ErrCSRFRequired), errors.Is(err, domain.ErrPermissionDenied):
		handler.writeError(response, request, err)
	case errors.Is(err, domain.ErrConflict):
		writeJSON(response, nethttp.StatusConflict, map[string]any{"ok": false, "error": "idempotency_conflict"})
	case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, domain.ErrNotFound):
		writeJSON(response, nethttp.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_member"})
	default:
		writeJSON(response, nethttp.StatusServiceUnavailable, map[string]any{"ok": false, "error": "admin_access_unavailable"})
	}
}

func (handler *Handler) listUsers(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "no-store")
	var session string
	if cookie, err := request.Cookie(SessionCookieName); err == nil {
		session = cookie.Value
	}
	actor, err := handler.auth.Authenticate(request.Context(), session)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	listing, err := handler.management.ListGovernance(request.Context(), actor)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, nethttp.StatusOK, map[string]any{"ok": true, "actor": listing.Actor, "capabilities": listing.Capabilities, "users": listing.Users})
}

func (handler *Handler) loginPage(response nethttp.ResponseWriter, request *nethttp.Request) {
	next := SafeNextPath(request.URL.Query().Get("next"), "/admin")
	if cookie, err := request.Cookie(SessionCookieName); err == nil {
		if _, authErr := handler.auth.Authenticate(request.Context(), cookie.Value); authErr == nil {
			nethttp.Redirect(response, request, next, nethttp.StatusSeeOther)
			return
		}
	}
	token, err := handler.issueLoginCSRF(response)
	if err != nil {
		nethttp.Error(response, "prepare login", nethttp.StatusInternalServerError)
		return
	}
	if err := handler.renderer.Render(request.Context(), response, nethttp.StatusOK, "login", map[string]any{
		"next_path": next, "login_csrf_token": token,
	}); err != nil {
		nethttp.Error(response, "render login", nethttp.StatusInternalServerError)
	}
}

func (handler *Handler) login(response nethttp.ResponseWriter, request *nethttp.Request) {
	payload, err := parsePayload(response, request)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	if !validLoginCSRF(request, payload) {
		handler.writeError(response, request, domain.ErrCSRFRequired)
		return
	}
	issued, err := handler.auth.Login(request.Context(), app.LoginCommand{
		Username: text(payload["username"]), Password: text(payload["password"]), Remote: remoteAddress(request),
	})
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.clearLoginCSRF(response)
	handler.setCookies(response, issued)
	next := SafeNextPath(text(payload["next"]), "/admin")
	if wantsJSON(request) {
		writeJSON(response, nethttp.StatusOK, map[string]any{"ok": true, "next": next, "csrf_token": issued.CSRFToken})
		return
	}
	nethttp.Redirect(response, request, next, nethttp.StatusSeeOther)
}

func (handler *Handler) logout(response nethttp.ResponseWriter, request *nethttp.Request) {
	session, csrfCookie, csrfRequest := requestCredentials(request)
	if err := handler.auth.Logout(request.Context(), session, csrfCookie, csrfRequest); err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.clearCookies(response)
	if wantsJSON(request) {
		writeJSON(response, nethttp.StatusOK, map[string]any{"ok": true})
		return
	}
	nethttp.Redirect(response, request, "/login", nethttp.StatusSeeOther)
}

func (handler *Handler) listEnterpriseEmployees(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "no-store")
	actor, ok := handler.authenticatedActor(response, request)
	if !ok {
		return
	}
	limit := enterpriseLimit(request.URL.Query().Get("limit"))
	if limit == 0 {
		handler.writeError(response, request, domain.ErrInvalidInput)
		return
	}
	listing, err := handler.management.ListEnterpriseEmployees(request.Context(), actor, request.URL.Query().Get("cursor"), request.URL.Query().Get("query"), limit)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, nethttp.StatusOK, map[string]any{"ok": true, "items": listing.Items, "next_cursor": listing.NextCursor, "has_more": listing.HasMore})
}

func (handler *Handler) addUser(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, payload, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	if err == nil {
		role, parseErr := parseGovernanceRole(payload["role"])
		if parseErr != nil {
			err = parseErr
		} else {
			_, err = handler.management.ProvisionEnterpriseEmployee(request.Context(), actor, app.ProvisionEnterpriseEmployeeInput{WeComUserID: text(payload["wecom_userid"]), Role: role, IdempotencyKey: key})
		}
	}
	handler.writeMutationResult(response, request, err)
}

// disableUser retains the old frozen POST path but applies the same CSRF,
// idempotency and row-level governance policy as PUT login-access.
func (handler *Handler) disableUser(response nethttp.ResponseWriter, request *nethttp.Request) {
	handler.setLoginEnabledWithValue(response, request, false)
}

func (handler *Handler) setLoginEnabled(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, payload, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	target, targetErr := targetID(request)
	enabled, valid := payload["login_enabled"].(bool)
	if err == nil && targetErr != nil {
		err = targetErr
	}
	if err == nil && !valid {
		err = domain.ErrInvalidInput
	}
	if err == nil {
		err = handler.management.SetGovernanceLoginEnabled(request.Context(), actor, app.SetLoginEnabledInput{TargetID: target, LoginEnabled: enabled, IdempotencyKey: key})
	}
	handler.writeMutationResult(response, request, err)
}

func (handler *Handler) setLoginEnabledWithValue(response nethttp.ResponseWriter, request *nethttp.Request, enabled bool) {
	actor, _, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	target, targetErr := targetID(request)
	if err == nil && targetErr != nil {
		err = targetErr
	}
	if err == nil {
		err = handler.management.SetGovernanceLoginEnabled(request.Context(), actor, app.SetLoginEnabledInput{TargetID: target, LoginEnabled: enabled, IdempotencyKey: key})
	}
	handler.writeMutationResult(response, request, err)
}

func (handler *Handler) bindWeCom(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, payload, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	target, targetErr := targetID(request)
	if err == nil && targetErr != nil {
		err = targetErr
	}
	if err == nil {
		err = handler.management.BindGovernanceWeComUserID(request.Context(), actor, app.BindEnterpriseEmployeeInput{TargetID: target, WeComUserID: text(payload["wecom_userid"]), IdempotencyKey: key})
	}
	handler.writeMutationResult(response, request, err)
}

func (handler *Handler) changeRoles(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, payload, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	target, targetErr := targetID(request)
	roles, roleErr := parseRoles(payload["roles"])
	role, singleErr := domain.SingleRole(roles)
	if err == nil && targetErr != nil {
		err = targetErr
	}
	if err == nil && roleErr != nil {
		err = roleErr
	}
	if err == nil && singleErr != nil {
		err = singleErr
	}
	if err == nil {
		err = handler.management.SetGovernanceRole(request.Context(), actor, app.SetRoleInput{TargetID: target, Role: role, IdempotencyKey: key})
	}
	handler.writeMutationResult(response, request, err)
}

func (handler *Handler) setRole(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, payload, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	target, targetErr := targetID(request)
	role, roleErr := parseGovernanceRole(payload["role"])
	if err == nil && targetErr != nil {
		err = targetErr
	}
	if err == nil && roleErr != nil {
		err = roleErr
	}
	if err == nil {
		err = handler.management.SetGovernanceRole(request.Context(), actor, app.SetRoleInput{TargetID: target, Role: role, IdempotencyKey: key})
	}
	handler.writeMutationResult(response, request, err)
}

func (handler *Handler) resetPassword(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, payload, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	target, targetErr := targetID(request)
	if err == nil && targetErr != nil {
		err = targetErr
	}
	if err == nil {
		err = handler.management.ResetGovernancePassword(request.Context(), actor, app.ResetPasswordInput{TargetID: target, Password: text(payload["password"]), IdempotencyKey: key})
	}
	handler.writeMutationResult(response, request, err)
}

func (handler *Handler) transferSuperAdmin(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, payload, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	key, err := adminAccessIdempotencyKey(request)
	target, targetErr := positivePayloadID(payload["target_admin_user_id"])
	if err == nil && targetErr != nil {
		err = targetErr
	}
	if err == nil {
		err = handler.management.TransferGovernanceSuperAdmin(request.Context(), actor, app.TransferSuperAdminInput{TargetID: target, IdempotencyKey: key})
	}
	handler.writeMutationResult(response, request, err)
}

func (handler *Handler) authenticatedActor(response nethttp.ResponseWriter, request *nethttp.Request) (domain.Principal, bool) {
	var session string
	if cookie, err := request.Cookie(SessionCookieName); err == nil {
		session = cookie.Value
	}
	actor, err := handler.auth.Authenticate(request.Context(), session)
	if err != nil {
		handler.writeError(response, request, err)
		return domain.Principal{}, false
	}
	return actor, true
}

func enterpriseLimit(raw string) int {
	if raw == "" {
		return 50
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 50 {
		return 0
	}
	return value
}

func parseGovernanceRole(value any) (domain.Role, error) {
	role := domain.Role(text(value))
	if role != domain.RoleAdmin && role != domain.RoleViewer {
		return "", domain.ErrInvalidInput
	}
	return role, nil
}

func positivePayloadID(value any) (int64, error) {
	number, valid := value.(float64)
	if !valid || number < 1 || number != float64(int64(number)) {
		return 0, domain.ErrInvalidInput
	}
	return int64(number), nil
}

func (handler *Handler) authorizedPayload(response nethttp.ResponseWriter, request *nethttp.Request) (domain.Principal, map[string]any, bool) {
	response.Header().Set("Cache-Control", "no-store")
	payload, err := parsePayload(response, request)
	if err != nil {
		handler.writeError(response, request, err)
		return domain.Principal{}, nil, false
	}
	session, csrfCookie, csrfRequest := requestCredentials(request)
	if csrfRequest == "" {
		csrfRequest = text(payload["csrf_token"])
	}
	actor, err := handler.auth.AuthorizeCSRF(request.Context(), session, csrfCookie, csrfRequest)
	if err != nil {
		handler.writeError(response, request, err)
		return domain.Principal{}, nil, false
	}
	return actor, payload, true
}

func (handler *Handler) writeMutationResult(response nethttp.ResponseWriter, request *nethttp.Request, err error) {
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) writeError(response nethttp.ResponseWriter, request *nethttp.Request, err error) {
	if strings.HasPrefix(request.URL.Path, "/api/admin/access/") || request.URL.Path == "/api/admin/admin-access" {
		response.Header().Set("Cache-Control", "no-store")
	}
	status, code := nethttp.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		status, code = nethttp.StatusUnauthorized, "invalid_credentials"
	case errors.Is(err, domain.ErrAuthentication):
		status, code = nethttp.StatusUnauthorized, "authentication_required"
	case errors.Is(err, domain.ErrCSRFRequired):
		status, code = nethttp.StatusForbidden, "csrf_required"
	case errors.Is(err, domain.ErrPermissionDenied):
		status, code = nethttp.StatusForbidden, "permission_denied"
	case errors.Is(err, domain.ErrRateLimited):
		status, code = nethttp.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, domain.ErrInvalidInput):
		status, code = nethttp.StatusBadRequest, "invalid_request"
	case errors.Is(err, domain.ErrNotFound):
		status, code = nethttp.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		status, code = nethttp.StatusConflict, "conflict"
	case errors.Is(err, app.ErrEnterpriseDirectoryUnavailable):
		status, code = nethttp.StatusServiceUnavailable, "enterprise_directory_unavailable"
	}
	if request.URL.Path == "/login" && !wantsJSON(request) {
		token, tokenErr := handler.issueLoginCSRF(response)
		if tokenErr != nil {
			nethttp.Error(response, "prepare login", nethttp.StatusInternalServerError)
			return
		}
		_ = handler.renderer.Render(request.Context(), response, status, "login", map[string]any{
			"next_path": SafeNextPath(request.FormValue("next"), "/admin"), "error": code, "login_csrf_token": token,
		})
		return
	}
	writeJSON(response, status, map[string]any{"ok": false, "error": code})
}

func (handler *Handler) issueLoginCSRF(response nethttp.ResponseWriter) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := time.Now().Add(loginCSRFTTL)
	response.Header().Set("Cache-Control", "no-store")
	nethttp.SetCookie(response, &nethttp.Cookie{
		Name: LoginCSRFCookieName, Value: token, Path: "/login", Expires: expiresAt, MaxAge: int(loginCSRFTTL.Seconds()),
		Secure: handler.cookieSecure, HttpOnly: true, SameSite: nethttp.SameSiteStrictMode,
	})
	return token, nil
}

func validLoginCSRF(request *nethttp.Request, payload map[string]any) bool {
	cookie, err := request.Cookie(LoginCSRFCookieName)
	submitted := text(payload["login_csrf_token"])
	if err != nil || cookie.Value == "" || submitted == "" || len(cookie.Value) != len(submitted) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) == 1
}

func (handler *Handler) clearLoginCSRF(response nethttp.ResponseWriter) {
	nethttp.SetCookie(response, &nethttp.Cookie{
		Name: LoginCSRFCookieName, Value: "", Path: "/login", MaxAge: -1, Expires: time.Unix(1, 0),
		Secure: handler.cookieSecure, HttpOnly: true, SameSite: nethttp.SameSiteStrictMode,
	})
}

func (handler *Handler) setCookies(response nethttp.ResponseWriter, issued app.IssuedSession) {
	maxAge := int(time.Until(issued.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	nethttp.SetCookie(response, &nethttp.Cookie{Name: SessionCookieName, Value: issued.SessionToken, Path: handler.cookiePath,
		Expires: issued.ExpiresAt, MaxAge: maxAge, Secure: handler.cookieSecure, HttpOnly: true, SameSite: nethttp.SameSiteLaxMode})
	nethttp.SetCookie(response, &nethttp.Cookie{Name: CSRFCookieName, Value: issued.CSRFToken, Path: handler.cookiePath,
		Expires: issued.ExpiresAt, MaxAge: maxAge, Secure: handler.cookieSecure, HttpOnly: false, SameSite: nethttp.SameSiteLaxMode})
	nethttp.SetCookie(response, &nethttp.Cookie{Name: CompatCSRFCookieName, Value: issued.CSRFToken, Path: handler.cookiePath,
		Expires: issued.ExpiresAt, MaxAge: maxAge, Secure: handler.cookieSecure, HttpOnly: false, SameSite: nethttp.SameSiteLaxMode})
}

func (handler *Handler) clearCookies(response nethttp.ResponseWriter) {
	for _, name := range []string{SessionCookieName, CSRFCookieName, CompatCSRFCookieName} {
		nethttp.SetCookie(response, &nethttp.Cookie{Name: name, Value: "", Path: handler.cookiePath,
			MaxAge: -1, Expires: time.Unix(1, 0), Secure: handler.cookieSecure,
			HttpOnly: name == SessionCookieName, SameSite: nethttp.SameSiteLaxMode})
	}
}

func requestCredentials(request *nethttp.Request) (string, string, string) {
	var session, csrfCookie string
	if cookie, err := request.Cookie(SessionCookieName); err == nil {
		session = cookie.Value
	}
	if cookie, err := request.Cookie(CSRFCookieName); err == nil {
		csrfCookie = cookie.Value
	}
	return session, csrfCookie, strings.TrimSpace(request.Header.Get("X-CSRF-Token"))
}

func parsePayload(response nethttp.ResponseWriter, request *nethttp.Request) (map[string]any, error) {
	request.Body = nethttp.MaxBytesReader(response, request.Body, 1<<20)
	if strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		var payload map[string]any
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return nil, domain.ErrInvalidInput
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, domain.ErrInvalidInput
		}
		return payload, nil
	}
	if err := request.ParseForm(); err != nil {
		return nil, domain.ErrInvalidInput
	}
	payload := make(map[string]any, len(request.PostForm))
	for key, values := range request.PostForm {
		if key == "roles" {
			payload[key] = values
		} else if len(values) > 0 {
			payload[key] = values[len(values)-1]
		}
	}
	return payload, nil
}

func parseRoles(value any) ([]domain.Role, error) {
	var raw []string
	switch roles := value.(type) {
	case []any:
		for _, role := range roles {
			raw = append(raw, text(role))
		}
	case []string:
		raw = roles
	case string:
		raw = strings.Split(roles, ",")
	default:
		return nil, domain.ErrInvalidInput
	}
	result := make([]domain.Role, 0, len(raw))
	for _, role := range raw {
		result = append(result, domain.Role(strings.TrimSpace(role)))
	}
	return domain.NormalizeRoles(result)
}

func targetID(request *nethttp.Request) (int64, error) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, domain.ErrInvalidInput
	}
	return id, nil
}

func publicUser(user domain.User) map[string]any {
	return map[string]any{"id": user.ID, "username": user.Username, "display_name": user.DisplayName,
		"wecom_userid": user.WeComUserID, "active": user.Active, "roles": user.Roles}
}

func text(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func wantsJSON(request *nethttp.Request) bool {
	return strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") || strings.Contains(request.Header.Get("Accept"), "application/json")
}

func writeJSON(response nethttp.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func remoteAddress(request *nethttp.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}

func SafeNextPath(value, fallback string) string {
	value = strings.TrimSpace(value)
	if fallback == "" || !strings.HasPrefix(fallback, "/") {
		fallback = "/admin"
	}
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.Contains(value, "://") || strings.Contains(value, "\\") || strings.ContainsAny(value, "\x00\r\n") ||
		strings.HasPrefix(value, "/static") || strings.HasPrefix(value, "/api/") || strings.HasPrefix(value, "/auth/wecom/callback") {
		return fallback
	}
	return value
}

func (handler *Handler) refreshStaffNames(response nethttp.ResponseWriter, request *nethttp.Request) {
	actor, _, ok := handler.authorizedPayload(response, request)
	if !ok {
		return
	}
	service, ok := handler.management.(interface {
		RefreshStaffNames(context.Context, domain.Principal) error
	})
	if !ok {
		handler.writeMutationResult(response, request, app.ErrEnterpriseDirectoryUnavailable)
		return
	}
	handler.writeMutationResult(response, request, service.RefreshStaffNames(request.Context(), actor))
}
