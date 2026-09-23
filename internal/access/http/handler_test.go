package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type testRenderer struct{}

func (testRenderer) Render(_ context.Context, response http.ResponseWriter, status int, _ string, _ map[string]any) error {
	response.WriteHeader(status)
	return nil
}

type capturingRenderer struct {
	values map[string]any
}

func (renderer *capturingRenderer) Render(_ context.Context, response http.ResponseWriter, status int, _ string, values map[string]any) error {
	renderer.values = values
	response.WriteHeader(status)
	return nil
}

type testAuth struct {
	csrfErr        error
	authenticateAs domain.Principal
	loginCalls     *int
}

func (auth testAuth) Login(context.Context, app.LoginCommand) (app.IssuedSession, error) {
	if auth.loginCalls != nil {
		(*auth.loginCalls)++
	}
	return app.IssuedSession{SessionToken: "session-secret", CSRFToken: "csrf-secret",
		ExpiresAt: time.Now().Add(time.Hour), User: app.UserSummary{ID: 1}}, nil
}

func (auth testAuth) Authenticate(context.Context, string) (domain.Principal, error) {
	if auth.authenticateAs.InternalID > 0 {
		return auth.authenticateAs, nil
	}
	return domain.Principal{}, domain.ErrAuthentication
}
func (auth testAuth) AuthorizeCSRF(context.Context, string, string, string) (domain.Principal, error) {
	if auth.csrfErr != nil {
		return domain.Principal{}, auth.csrfErr
	}
	return domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin}}, nil
}
func (auth testAuth) Logout(context.Context, string, string, string) error { return auth.csrfErr }

type testManagement struct{}

func (testManagement) ListUsers(context.Context, domain.Principal) ([]app.UserSummary, error) {
	return []app.UserSummary{{ID: 2, Username: "employee", DisplayName: "Employee", Active: true,
		SessionVersion: 3, Roles: []domain.Role{domain.RoleViewer}}}, nil
}
func (testManagement) SetLoginAccess(_ context.Context, _ domain.Principal, key string, changes []app.LoginAccessChange) ([]app.UserSummary, error) {
	if key == "donor-admin-access-drift" {
		return nil, domain.ErrConflict
	}
	if key != "donor-admin-access-key" {
		return nil, domain.ErrInvalidInput
	}
	if len(changes) != 1 || changes[0].AdminUserID != 2 {
		return nil, domain.ErrInvalidInput
	}
	return []app.UserSummary{{ID: 2, Username: "employee", DisplayName: "Employee", Active: changes[0].LoginEnabled,
		SessionVersion: 4, Roles: []domain.Role{domain.RoleViewer}}}, nil
}

func (testManagement) AddUser(context.Context, domain.Principal, app.AddUserInput) (domain.User, error) {
	return domain.User{ID: 2, Username: "employee", Active: true}, nil
}
func (testManagement) DisableUser(context.Context, domain.Principal, int64) error { return nil }
func (testManagement) BindWeComUserID(context.Context, domain.Principal, int64, string) error {
	return nil
}
func (testManagement) ChangeRoles(context.Context, domain.Principal, int64, []domain.Role) error {
	return nil
}
func (testManagement) ResetPassword(context.Context, domain.Principal, int64, string) error {
	return nil
}

func TestLoginCookieContractAndSafeNext(t *testing.T) {
	renderer := &capturingRenderer{}
	handler, err := NewHandler(Config{Renderer: renderer, Auth: testAuth{}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	loginCookie, loginToken := getLoginCSRF(t, handler, renderer)
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=operator&password=secret&next=https%3A%2F%2Fevil.example&login_csrf_token="+loginToken))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(loginCookie)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	cookies := cookiesByName(response.Result().Cookies())
	if cookie := cookies[SessionCookieName]; cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", cookie)
	}
	if cookie := cookies[CSRFCookieName]; cookie == nil || !cookie.Secure || cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("csrf cookie = %#v", cookie)
	}
	if cookie := cookies[CompatCSRFCookieName]; cookie == nil || !cookie.Secure || cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Value != cookies[CSRFCookieName].Value {
		t.Fatalf("compat csrf cookie = %#v", cookie)
	}
	if cookie := cookies[LoginCSRFCookieName]; cookie == nil || cookie.MaxAge != -1 || cookie.Path != "/login" {
		t.Fatalf("cleared login csrf cookie = %#v", cookie)
	}
}

func TestLoginPreservesExternalEffectsNextQuery(t *testing.T) {
	renderer := &capturingRenderer{}
	handler, err := NewHandler(Config{Renderer: renderer, Auth: testAuth{}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	loginCookie, loginToken := getLoginCSRF(t, handler, renderer)
	next := "/admin/external-effects?view=external-effects&job=42"
	values := url.Values{
		"username":         {"operator"},
		"password":         {"secret"},
		"next":             {next},
		"login_csrf_token": {loginToken},
	}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(loginCookie)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != next {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
}

func TestLoginPageIssuesStrictHttpOnlyCSRFAndRendersMatchingToken(t *testing.T) {
	renderer := &capturingRenderer{}
	handler, err := NewHandler(Config{Renderer: renderer, Auth: testAuth{}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	loginCookie, loginToken := getLoginCSRF(t, handler, renderer)
	if len(loginToken) < 32 || loginCookie.Value != loginToken {
		t.Fatalf("token length=%d cookie matches=%t", len(loginToken), loginCookie.Value == loginToken)
	}
	if !loginCookie.Secure || !loginCookie.HttpOnly || loginCookie.SameSite != http.SameSiteStrictMode || loginCookie.Path != "/login" {
		t.Fatalf("login csrf cookie = %#v", loginCookie)
	}
}

func TestLoginRejectsMissingOrMismatchedFormCSRFBeforeAuthentication(t *testing.T) {
	for _, submitted := range []string{"", "wrong-token"} {
		t.Run(submitted, func(t *testing.T) {
			calls := 0
			renderer := &capturingRenderer{}
			handler, err := NewHandler(Config{Renderer: renderer, Auth: testAuth{loginCalls: &calls}, Management: testManagement{}, CookieSecure: true})
			if err != nil {
				t.Fatal(err)
			}
			loginCookie, _ := getLoginCSRF(t, handler, renderer)
			request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=operator&password=secret&login_csrf_token="+submitted))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(loginCookie)
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || calls != 0 {
				t.Fatalf("status=%d login calls=%d", response.Code, calls)
			}
			if token := text(renderer.values["login_csrf_token"]); token == "" || token == loginCookie.Value {
				t.Fatalf("rerendered token was not rotated: %q", token)
			}
		})
	}
}

func getLoginCSRF(t *testing.T, handler *Handler, renderer *capturingRenderer) (*http.Cookie, string) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("login page status=%d", response.Code)
	}
	cookie := cookiesByName(response.Result().Cookies())[LoginCSRFCookieName]
	if cookie == nil {
		t.Fatal("login page did not issue CSRF cookie")
	}
	return cookie, text(renderer.values["login_csrf_token"])
}

func cookiesByName(cookies []*http.Cookie) map[string]*http.Cookie {
	result := make(map[string]*http.Cookie, len(cookies))
	for _, cookie := range cookies {
		result[cookie.Name] = cookie
	}
	return result
}

func TestManagementHandlerRejectsMissingCSRF(t *testing.T) {
	handler, err := NewHandler(Config{Renderer: testRenderer{}, Auth: testAuth{csrfErr: domain.ErrCSRFRequired}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/access/users/2/disable", strings.NewReader(""))
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "csrf_required") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestListUsersNeedsSessionButNotCSRFAndReturnsPublicFields(t *testing.T) {
	principal := domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin}}
	handler, err := NewHandler(Config{Renderer: testRenderer{}, Auth: testAuth{authenticateAs: principal}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/access/users", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"username":"employee"`) || !strings.Contains(body, `"admin_user_id":2`) || strings.Contains(body, "password_hash") || strings.Contains(body, "digest") {
		t.Fatalf("public list body=%q", body)
	}
}

func TestAdminAccessCompatibilityUsesFrozenDTOAndExistingAccessGuards(t *testing.T) {
	principal := domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin}}
	handler, err := NewHandler(Config{Renderer: testRenderer{}, Auth: testAuth{authenticateAs: principal}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/admin/admin-access", nil)
	get.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, get)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"local_only":true`) || !strings.Contains(response.Body.String(), `"admin_user_id":2`) || strings.Contains(response.Body.String(), "password") {
		t.Fatalf("get status=%d body=%q", response.Code, response.Body.String())
	}

	put := httptest.NewRequest(http.MethodPut, "/api/admin/admin-access", strings.NewReader(`{"members":[{"admin_user_id":2,"login_enabled":false}]}`))
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("Idempotency-Key", "donor-admin-access-key")
	put.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-secret"})
	put.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "csrf-secret"})
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, put)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"idempotency_key":"donor-admin-access-key"`) || !strings.Contains(response.Body.String(), `"login_enabled":false`) {
		t.Fatalf("put status=%d body=%q", response.Code, response.Body.String())
	}

	missing := httptest.NewRequest(http.MethodPut, "/api/admin/admin-access", strings.NewReader(`{"members":[{"admin_user_id":2,"login_enabled":false}]}`))
	missing.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, missing)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"error":"invalid_request"`) {
		t.Fatalf("missing key status=%d body=%q", response.Code, response.Body.String())
	}

	drift := httptest.NewRequest(http.MethodPut, "/api/admin/admin-access", strings.NewReader(`{"members":[{"admin_user_id":2,"login_enabled":true}]}`))
	drift.Header.Set("Content-Type", "application/json")
	drift.Header.Set("Idempotency-Key", "donor-admin-access-drift")
	drift.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-secret"})
	drift.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "csrf-secret"})
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, drift)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"error":"idempotency_conflict"`) {
		t.Fatalf("payload drift status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestSafeNextPath(t *testing.T) {
	for _, unsafe := range []string{"https://evil.example", "//evil.example", `\\evil`, "/api/admin/users", "/static/x"} {
		if got := SafeNextPath(unsafe, "/admin"); got != "/admin" {
			t.Errorf("SafeNextPath(%q) = %q", unsafe, got)
		}
	}
	if got := SafeNextPath("/admin/customers?q=1", "/admin"); got != "/admin/customers?q=1" {
		t.Fatalf("safe next = %q", got)
	}
}

func (testManagement) ListGovernance(_ context.Context, actor domain.Principal) (app.GovernanceListing, error) {
	return app.GovernanceListing{Actor: app.GovernanceActor{AdminUserID: actor.InternalID, Role: domain.RoleSuperAdmin}, Users: []app.GovernanceUser{{UserSummary: app.UserSummary{ID: 2, Username: "employee", DisplayName: "Employee", Active: true, Roles: []domain.Role{domain.RoleViewer}}, AdminUserID: 2, Role: domain.RoleViewer, LoginEnabled: true}}}, nil
}
func (testManagement) ListEnterpriseEmployees(context.Context, domain.Principal, string, string, int) (app.EnterpriseEmployeeListing, error) {
	return app.EnterpriseEmployeeListing{}, nil
}
func (testManagement) ProvisionEnterpriseEmployee(context.Context, domain.Principal, app.ProvisionEnterpriseEmployeeInput) (domain.User, error) {
	return domain.User{ID: 3}, nil
}
func (testManagement) SetGovernanceLoginEnabled(context.Context, domain.Principal, app.SetLoginEnabledInput) error {
	return nil
}
func (testManagement) SetGovernanceRole(context.Context, domain.Principal, app.SetRoleInput) error {
	return nil
}
func (testManagement) BindGovernanceWeComUserID(context.Context, domain.Principal, app.BindEnterpriseEmployeeInput) error {
	return nil
}
func (testManagement) ResetGovernancePassword(context.Context, domain.Principal, app.ResetPasswordInput) error {
	return nil
}
func (testManagement) TransferGovernanceSuperAdmin(context.Context, domain.Principal, app.TransferSuperAdminInput) error {
	return nil
}
