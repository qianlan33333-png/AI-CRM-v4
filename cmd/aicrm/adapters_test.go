package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type fakeAccessAuthentication struct {
	principal accessdomain.Principal
	issued    accessapp.IssuedSession
	err       error
	session   string
	csrf      [3]string
	wecomID   string
}

func (fake *fakeAccessAuthentication) Authenticate(_ context.Context, token string) (accessdomain.Principal, error) {
	fake.session = token
	return fake.principal, fake.err
}

func (fake *fakeAccessAuthentication) AuthorizeCSRF(_ context.Context, session, cookie, request string) (accessdomain.Principal, error) {
	fake.csrf = [3]string{session, cookie, request}
	return fake.principal, fake.err
}

func (fake *fakeAccessAuthentication) LoginWithWeComUserID(_ context.Context, command accessapp.WeComLoginCommand) (accessapp.IssuedSession, error) {
	fake.wecomID = command.WeComUserID
	return fake.issued, fake.err
}

type directUnitOfWork struct{}

func (directUnitOfWork) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type paymentRecoveryRouteApplication struct {
	paymenthttp.Application
	commands []paymentport.ProfitSharingReceiverRecoveryCommand
}

func (stub *paymentRecoveryRouteApplication) RecoverProfitSharingReceiver(_ context.Context, command paymentport.ProfitSharingReceiverRecoveryCommand) (paymentport.ReceiverReadiness, error) {
	stub.commands = append(stub.commands, command)
	return paymentport.ReceiverReadiness{Reference: command.ReceiverReference, State: "accepted", UpdatedAt: time.Date(2026, time.September, 14, 0, 0, 0, 0, time.UTC)}, nil
}

type paymentRecoveryRouteSecurity struct {
	principal   accessdomain.Principal
	err         error
	requireCSRF bool
	csrfCalls   int
}

func (stub *paymentRecoveryRouteSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, stub.err
}

func (stub *paymentRecoveryRouteSecurity) AuthorizeCSRF(_ context.Context, request *http.Request) (accessdomain.Principal, error) {
	stub.csrfCalls++
	if stub.requireCSRF && request.Header.Get("X-CSRF-Token") == "" {
		return accessdomain.Principal{}, accessdomain.ErrAuthentication
	}
	return stub.principal, stub.err
}

func TestMountOpenPlatformUIUsesAuthenticatedV3Host(t *testing.T) {
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, err: accessdomain.ErrAuthentication}
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("fallback")) })
	host := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("open-platform-host")) })
	handler := mountOpenPlatformUI(fallback, host, authentication)

	request := httptest.NewRequest(http.MethodGet, "/admin/api-docs", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fadmin%2Fapi-docs" {
		t.Fatalf("unauthenticated status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/apidocs.html?tab=clients&client=fixture-client", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fadmin%2Fapidocs.html%3Ftab%3Dclients%26client%3Dfixture-client" {
		t.Fatalf("unauthenticated deep-link status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	authentication.err = nil
	for _, path := range []string{"/admin/apidocs.html?client=fixture", "/admin/apidocs.html?tab=clients&client=fixture-client", "/admin/api-docs?tab=docs&client=fixture-client"} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: "valid"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != "open-platform-host" || authentication.session != "valid" {
			t.Fatalf("v3 host path=%s status=%d body=%q session=%q", path, response.Code, response.Body.String(), authentication.session)
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "fallback" {
		t.Fatalf("unrelated route status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestAllowedOAuthRedirectsIncludesHiddenExternalEffectsPage(t *testing.T) {
	if _, ok := allowedOAuthRedirects()["/admin/external-effects"]; !ok {
		t.Fatal("external effects page is not an allowed OAuth redirect")
	}
}

func TestMountSurveyAPIsIncludesLegacyOperationsLogRead(t *testing.T) {
	mux := http.NewServeMux()
	mountSurveyAPIs(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"total":0}`))
	}))

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/questionnaires/11/external-push-logs?limit=50&offset=0", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" || response.Body.String() != `{"items":[],"total":0}` {
		t.Fatalf("legacy survey operations log route status=%d content_type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

type fakeUserReader struct{ user accessdomain.User }

func (reader fakeUserReader) UserByID(context.Context, int64, bool) (accessdomain.User, error) {
	return reader.user, nil
}

func TestRequestSecurityUsesOnlyAdminCookiesAndCSRFHeader(t *testing.T) {
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	security := requestAccessSecurity{authentication: authentication}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/oneid/resolve", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "session-cookie"})
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_csrf", Value: "csrf-cookie"})
	request.Header.Set("X-CSRF-Token", "csrf-header")
	request.Header.Set("Authorization", "Bearer forged")

	if _, err := security.Authenticate(request.Context(), request); err != nil || authentication.session != "session-cookie" {
		t.Fatalf("authenticate session=%q err=%v", authentication.session, err)
	}
	if _, err := security.AuthorizeCSRF(request.Context(), request); err != nil || authentication.csrf != [3]string{"session-cookie", "csrf-cookie", "csrf-header"} {
		t.Fatalf("csrf=%q err=%v", authentication.csrf, err)
	}
}

func TestAdminShellRedirectsWithoutSessionAndServesWithSession(t *testing.T) {
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	handler := requireAdminSession(authentication, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/config/login-access?tab=staff", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fadmin%2Fconfig%2Flogin-access%3Ftab%3Dstaff" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	authentication.err = nil
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: "csrf-current"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || authentication.session != "valid" {
		t.Fatalf("status=%d session=%q", response.Code, authentication.session)
	}
	var compat *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == accesshttp.CompatCSRFCookieName {
			compat = cookie
			break
		}
	}
	if compat == nil || compat.Value != "csrf-current" || !compat.Secure || compat.HttpOnly || compat.SameSite != http.SameSiteLaxMode {
		t.Fatalf("compat csrf cookie=%#v", compat)
	}
}

func TestWeComAdaptersIssueSharedSessionAndResolveBoundEmployee(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	authentication := &fakeAccessAuthentication{
		principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 11, Roles: []accessdomain.Role{accessdomain.RoleViewer}},
		issued:    accessapp.IssuedSession{SessionToken: "session", CSRFToken: "csrf", ExpiresAt: expires},
	}
	issuer := weComSessionIssuer{authentication: authentication}
	credentials, err := issuer.IssueWeComSession(context.Background(), wecom.OAuthSidebar, wecom.OAuthIdentity{CorpID: "ww-corp", EmployeeID: "employee-11"})
	if err != nil || credentials.SessionToken != "session" || authentication.wecomID != "employee-11" {
		t.Fatalf("credentials=%+v employee=%q err=%v", credentials, authentication.wecomID, err)
	}

	resolver := sidebarPrincipalResolver{authentication: authentication, users: fakeUserReader{user: accessdomain.User{
		ID: 11, Active: true, WeComUserID: "employee-11",
	}}, uow: directUnitOfWork{}, corpID: "ww-corp"}
	principal, err := resolver.SidebarPrincipal(context.Background(), "sidebar-session")
	if err != nil || principal.CorpID != "ww-corp" || principal.EmployeeID != "employee-11" || authentication.session != "sidebar-session" {
		t.Fatalf("principal=%+v session=%q err=%v", principal, authentication.session, err)
	}

	resolver.users = fakeUserReader{user: accessdomain.User{ID: 11, Active: true}}
	if _, err = resolver.SidebarPrincipal(context.Background(), "sidebar-session"); !errors.Is(err, accessdomain.ErrAuthentication) {
		t.Fatalf("unbound user error=%v", err)
	}
}

func TestApplicationRouterKeepsOwnershipAndProtectsAdminShell(t *testing.T) {
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Owner", name)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := routeApplication(marker("health"), marker("access"), marker("identity"), marker("wecom"), marker("shell"), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"/healthz": "health", "/readyz": "health", "/login": "access", "/api/admin/access/users": "access", "/api/admin/admin-access": "access",
		"/api/admin/oneid/conflicts": "identity", "/auth/wecom/start": "wecom", "/api/sidebar/jssdk-config": "wecom",
		"/api/sidebar/v2/profile": "identity", "/api/sidebar/v2/send-intents": "identity",
		"/sidebar/bind-mobile": "shell", "/static/admin_console/admin_console.css": "shell",
	}
	for path, owner := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != owner {
			t.Fatalf("path=%s status=%d owner=%q", path, response.Code, response.Header().Get("X-Owner"))
		}
		if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("path=%s missing security headers", path)
		}
		if path == "/sidebar/bind-mobile" && response.Header().Get("X-Frame-Options") != "" {
			t.Fatalf("sidebar must remain embeddable by the WeCom client")
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/orders", nil))
	if response.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated admin status=%d", response.Code)
	}
	authentication.err = nil
	request := httptest.NewRequest(http.MethodGet, "/admin/orders", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "shell" {
		t.Fatalf("authenticated admin status=%d owner=%q", response.Code, response.Header().Get("X-Owner"))
	}
}

func TestFullApplicationRouterExposesOrderImportAPI(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Owner", name)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	other := marker("other")
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(
		other, other, marker("identity"), other, other, other,
		other, other, other, other, other, other, other, other,
		other, other, other, other, other, other, other, other,
		other, other, &fakeAccessAuthentication{}, "https://crm.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/order-imports/inspect", nil))
	if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "identity" {
		t.Fatalf("status=%d owner=%q", response.Code, response.Header().Get("X-Owner"))
	}
}

func TestFullApplicationRouterCanonicalizesLegacyAdminAccessPage(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Owner", name)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	other := marker("other")
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(
		other, marker("access"), other, other, other, other,
		other, other, other, other, other, other, other, other,
		other, other, other, other, other, other, other, other,
		other, marker("shell"), authentication, "https://crm.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/admin-access?journey_as=super", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fadmin%2Fadmin-access%3Fjourney_as%3Dsuper" {
		t.Fatalf("unauthenticated legacy page status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	authentication.err = nil
	request := httptest.NewRequest(http.MethodGet, "/admin/admin-access?journey_as=super", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != webshell.LoginAccessPath+"?journey_as=super" {
		t.Fatalf("legacy canonicalization status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	request = httptest.NewRequest(http.MethodGet, webshell.LoginAccessPath, nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "shell" {
		t.Fatalf("canonical access page status=%d owner=%q", response.Code, response.Header().Get("X-Owner"))
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/admin-access", nil))
	if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "access" {
		t.Fatalf("compatibility API status=%d owner=%q", response.Code, response.Header().Get("X-Owner"))
	}
}

func TestFullApplicationRouterDelegatesExactOperationMemberScopeToAdminAPIs(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Owner", name)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	adminAPIs := http.NewServeMux()
	adminAPIs.Handle("/api/admin/common/operation-members", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("scope") == "owner_migration" {
			writer.Header().Set("X-Owner", "owner-handoff")
		} else {
			writer.Header().Set("X-Owner", "group-ops-through-admin-apis")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	other := marker("other")
	groupOps := marker("group-ops-subtree")
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(
		other, other, adminAPIs, other, other, other,
		other, other, other, other, other, other, other, other,
		other, groupOps, other, other, other, other, other, other,
		other, other, &fakeAccessAuthentication{}, "https://crm.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, owner string
	}{
		{"/api/admin/common/operation-members?scope=owner_migration&include_inactive=true", "owner-handoff"},
		{"/api/admin/common/operation-members?scope=group_ops", "group-ops-through-admin-apis"},
		{"/api/admin/common/operation-members/sync", "group-ops-subtree"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != test.owner {
			t.Fatalf("path=%s status=%d owner=%q", test.path, response.Code, response.Header().Get("X-Owner"))
		}
	}
}

func TestMountHXCUIReplacesPlaceholderAndProtectsAssets(t *testing.T) {
	dashboard := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Owner", "hxc-ui")
		writer.WriteHeader(http.StatusNoContent)
	})
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Owner", "next")
		writer.WriteHeader(http.StatusNoContent)
	})
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler := mountHXCUI(next, dashboard, authentication)
	for _, target := range []string{"/admin/hxc-dashboard", "/hxc-dashboard-assets/admin-HASH.js"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "hxc-ui" {
			t.Fatalf("target=%q status=%d owner=%q", target, response.Code, response.Header().Get("X-Owner"))
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/customers", nil))
	if response.Header().Get("X-Owner") != "next" {
		t.Fatalf("unrelated owner=%q", response.Header().Get("X-Owner"))
	}
}

func TestTransactionRouteKeepsShellButReportsBackendUnavailable(t *testing.T) {
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNotFound) })
	handler, err := routeApplication(marker, marker, marker, marker, webshell.MustHandler(), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/orders", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "交易后端尚未就绪") || !strings.Contains(body, "功能待接入") {
		t.Fatalf("status=%d body=%q", response.Code, body)
	}
	if strings.Contains(body, "orderTransactionId") || strings.Contains(body, "创建退款 intent") {
		t.Fatal("blocked transaction shell mounted donor business actions before backend readiness")
	}
}

func TestSecurityHeadersAllowBlobImagesOnlyOnMediaAndSidebarPages(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for path, allowsBlob := range map[string]bool{
		"/admin/materials":                            true,
		"/admin/image-library":                        true,
		"/admin/miniprogram-library":                  true,
		"/admin/attachment-library":                   true,
		"/admin/images.html":                          true,
		"/admin/mpLib.html":                           true,
		"/admin/attach.html":                          true,
		webshell.SidebarPagePath:                      true,
		"/admin/campaigns.html?view=external-effects": false,
		"/admin/orders":                               false,
		"/api/admin/image-library":                    false,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		policy := response.Header().Get("Content-Security-Policy")
		if allowsBlob && !strings.Contains(policy, "img-src 'self' data: blob:") {
			t.Fatalf("Media/sidebar page CSP lacks blob image source for %s: %q", path, policy)
		}
		if !allowsBlob && strings.Contains(policy, "blob:") {
			t.Fatalf("unrelated page CSP unexpectedly permits blob images for %s: %q", path, policy)
		}
	}
}

func TestSecurityHeadersAllowInvitationProviderImagesOnlyOnPublicInvitationPages(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/gi/40e73b50a900267cfc3a384609e884a4ea77b49139bae075", nil))
	policy := public.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "img-src 'self' data: https://wework.qpic.cn") {
		t.Fatalf("public invitation CSP does not allow provider QR image: %q", policy)
	}

	admin := httptest.NewRecorder()
	handler.ServeHTTP(admin, httptest.NewRequest(http.MethodGet, "/admin/channels", nil))
	adminPolicy := admin.Header().Get("Content-Security-Policy")
	if strings.Contains(adminPolicy, "https://wework.qpic.cn") {
		t.Fatalf("admin CSP unexpectedly allows provider QR image: %q", adminPolicy)
	}
}

func TestSecurityHeadersAllowDashboardRuntimeStyles(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/hxc-dashboard", nil))
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("policy=%q", policy)
	}
}

func TestSecurityHeadersKeepChannelDonorScriptSameOriginOnly(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/channels/17/edit", nil))
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self' https://res.wx.qq.com") {
		t.Fatalf("channel CSP does not allow same-origin donor script: %q", policy)
	}
	if strings.Contains(policy, "unsafe-eval") || strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("channel CSP must not relax script execution for the donor: %q", policy)
	}
}

func TestSecurityHeadersKeepCouponDonorRuntimeSameOriginOnly(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/couponForm.html", nil))
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self' https://res.wx.qq.com") {
		t.Fatalf("coupon CSP does not allow same-origin donor runtime: %q", policy)
	}
	if strings.Contains(policy, "unsafe-eval") || strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("coupon CSP must not relax script execution for the donor runtime: %q", policy)
	}
}

func TestSecurityHeadersAllowFrozenOperationCycleInlineStylesOnBothPagesOnly(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for path, allowed := range map[string]bool{
		"/admin/operation-cycles":                        true,
		"/admin/operation-cycles/cyclesDetail.html?id=1": true,
		"/admin/operation-cycles/cycles.html":            true,
		"/admin/operation-cycles-unsafe":                 false,
		"/api/admin/operation-cycles/strategies":         false,
		"/assets/operationCyclesHost.js":                 false,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		policy := response.Header().Get("Content-Security-Policy")
		hasInlineStyle := strings.Contains(policy, "style-src 'self' 'unsafe-inline'")
		if hasInlineStyle != allowed {
			t.Fatalf("path=%s inline-style=%t policy=%q", path, hasInlineStyle, policy)
		}
		if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
			t.Fatalf("operation-cycle CSP relaxed scripts for %s: %q", path, policy)
		}
	}
}

func TestOwnerHandoffUIMountRetainsContactHistoryReadOnlyEntry(t *testing.T) {
	var hostCalls, historyCalls int
	host := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hostCalls++
		writer.Header().Set("X-Owner-Handoff", "host")
	})
	history := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("contact_history") != "1" {
			t.Fatalf("history fallback query=%q", request.URL.RawQuery)
		}
		historyCalls++
		writer.Header().Set("X-Owner-Handoff", "history-read-only")
	})
	handler := mountOwnerHandoffUI(history, host)

	for path, want := range map[string]string{
		"/admin/ownerMig.html":                   "host",
		"/admin/owner-migration":                 "host",
		"/admin/ownerMig.html?contact_history=1": "history-read-only",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if got := response.Header().Get("X-Owner-Handoff"); got != want {
			t.Fatalf("path=%s owner=%q want=%q", path, got, want)
		}
	}
	if hostCalls != 2 || historyCalls != 1 {
		t.Fatalf("host=%d history=%d", hostCalls, historyCalls)
	}
}

func TestSecurityHeadersAllowFrozenOwnerHandoffInlineStylesOnlyOnOwnerPage(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for path, allowed := range map[string]bool{
		"/admin/owner-migration":                      true,
		"/admin/ownerMig.html":                        true,
		"/admin/owner-migration/unsafe":               false,
		"/api/admin/customers/owner-handoffs":         false,
		"/static/admin_console/owner_handoff_host.js": false,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		policy := response.Header().Get("Content-Security-Policy")
		hasInlineStyle := strings.Contains(policy, "style-src 'self' 'unsafe-inline'")
		if hasInlineStyle != allowed {
			t.Fatalf("path=%s inline-style=%t policy=%q", path, hasInlineStyle, policy)
		}
		if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
			t.Fatalf("owner handoff CSP relaxed scripts for %s: %q", path, policy)
		}
	}
}

func TestApplicationRouterOwnsEffectsAndPushCenterSeparately(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Owner", name)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := routeApplicationWithEffects(marker("health"), marker("access"), marker("identity"), marker("effects"), marker("push"), marker("ui"), marker("wecom"), marker("shell"), &fakeAccessAuthentication{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for path, owner := range map[string]string{"/api/admin/external-effects": "effects", "/api/admin/external-effects/eer_1/cancel": "effects", "/api/admin/push-center/jobs": "push", "/api/admin/push-center/jobs/1/retry": "push"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(response, request)
		if response.Header().Get("X-Owner") != owner {
			t.Fatalf("%s owner=%q", path, response.Header().Get("X-Owner"))
		}
	}
}

func TestApplicationRouterMountsWeChatShopCallbackAndReconciliation(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Owner", name)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := routeApplication(marker("health"), marker("access"), marker("identity"), marker("wecom"), marker("shell"), &fakeAccessAuthentication{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/public/wechat-shop/callbacks/refund",
		"/api/admin/wechat-shop/refunds/9/reconcile",
		"/api/admin/wechat-pay/refunds/9/reconcile",
		"/api/admin/wechat-pay/profit-sharing/receivers/psrecv_9/recover",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "identity" {
			t.Fatalf("%s status=%d owner=%q", path, response.Code, response.Header().Get("X-Owner"))
		}
	}
}

func TestApplicationRouterAndAdminAPIsMountRecoveryAndWeChatPayRefundPrefixesExactly(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Owner", name)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	adminAPIs := http.NewServeMux()
	mountPaymentAdminAPIs(adminAPIs, marker("orders"), marker("payment"))
	handler, err := routeApplication(marker("health"), marker("access"), adminAPIs, marker("wecom"), marker("shell"), &fakeAccessAuthentication{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/admin/refunds/recovery",
		"/api/admin/wechat-pay/profit-sharing/receivers/psrecv_9/recover",
		"/api/admin/wechat-pay/refunds/9/reconcile",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "payment" {
			t.Fatalf("path=%s status=%d owner=%q", path, response.Code, response.Header().Get("X-Owner"))
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/profit-sharing/recover", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected broad payment route status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/refunds/recovery/extra", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected broad refund recovery route status=%d", response.Code)
	}
}

func TestApplicationRouterAndAdminAPIsEnforceRecoveryAuthenticationBeforeAcceptance(t *testing.T) {
	post := func(handler http.Handler) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/profit-sharing/receivers/psrecv_9/recover", strings.NewReader(`{"evidence_reference":"route-regression"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "receiver-route-regression-0001")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	for _, test := range []struct {
		name       string
		security   paymentRecoveryRouteSecurity
		wantStatus int
		wantCalls  int
	}{
		{name: "anonymous", security: paymentRecoveryRouteSecurity{err: accessdomain.ErrAuthentication}, wantStatus: http.StatusForbidden},
		{name: "ordinary admin", security: paymentRecoveryRouteSecurity{principal: accessdomain.Principal{InternalID: 4, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, wantStatus: http.StatusForbidden},
		{name: "csrf rejected", security: paymentRecoveryRouteSecurity{principal: accessdomain.Principal{InternalID: 5, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, requireCSRF: true}, wantStatus: http.StatusForbidden},
		{name: "super admin", security: paymentRecoveryRouteSecurity{principal: accessdomain.Principal{InternalID: 6, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}, wantStatus: http.StatusAccepted, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := &paymentRecoveryRouteApplication{}
			paymentHandler, err := paymenthttp.NewHandler(application, nil, &test.security, true)
			if err != nil {
				t.Fatal(err)
			}
			adminAPIs := http.NewServeMux()
			mountPaymentAdminAPIs(adminAPIs, marker, paymentHandler)
			handler, err := routeApplication(marker, marker, adminAPIs, marker, marker, &fakeAccessAuthentication{}, "https://crm.example")
			if err != nil {
				t.Fatal(err)
			}
			response := post(handler)
			if response.Code != test.wantStatus || len(application.commands) != test.wantCalls || test.security.csrfCalls != 1 {
				t.Fatalf("status=%d calls=%d csrf_calls=%d body=%s", response.Code, len(application.commands), test.security.csrfCalls, response.Body.String())
			}
			if test.wantCalls == 1 && application.commands[0].ActorAdminUserID != 6 {
				t.Fatalf("accepted command=%+v", application.commands[0])
			}
		})
	}
}

func TestExternalEffectsUIRequiresAdminAndExposesOnlyItsFrozenSurface(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "campaign.js"), []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "campaign.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "labs.css"), []byte("#stage{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "asset-manifest.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(`{"entries":{"admin":"assets/campaign.js","tokens":"assets/campaign.css","labs":"assets/labs.css"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	renderer, err := webshell.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	ui := externaleffects.NewUIHandler(dist, func(writer http.ResponseWriter, request *http.Request, tokens, labs, admin string) error {
		return renderer.RenderExternalEffects(writer, webshell.AdminPageForRequest(request, "外部效果与 Push Center", "", "api.admin_cloud_orchestrator_workspace"), webshell.ExternalEffectsAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin})
	})
	handler, err := routeApplicationWithEffects(marker, marker, marker, marker, marker, ui, marker, marker, authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=external-effects", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fadmin%2Fexternal-effects%3Fview%3Dexternal-effects" {
		t.Fatalf("unauthenticated effects UI status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	authentication.err = nil
	request := httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=campaign&unexpected=1", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/campaigns.html?view=external-effects" {
		t.Fatalf("query was not normalized status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=external-effects&job=42", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/campaigns.html?job=42&view=external-effects" {
		t.Fatalf("frozen donor alias was not preserved status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/campaigns.html?view=external-effects&job=42", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Count(response.Body.String(), `class="admin-sidebar"`) != 1 || strings.Count(response.Body.String(), "<main") != 1 || !strings.Contains(response.Body.String(), `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main>`) || strings.Contains(response.Body.String(), `<aside class="side">`) || !strings.Contains(response.Body.String(), `src="/assets/campaign.js"`) || !strings.Contains(response.Header().Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline'") || strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("external effects shell mismatch status=%d body=%q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=external-effects&job=0", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/campaigns.html?view=external-effects" {
		t.Fatalf("invalid job was not normalized status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/campaigns.html?view=campaign", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("non-effects campaign alias status=%d", response.Code)
	}

	for path, want := range map[string]struct {
		body string
		mime string
	}{
		"/assets/campaign.js":         {body: "asset", mime: "text/javascript; charset=utf-8"},
		"/assets/campaign.css":        {body: "body{}", mime: "text/css; charset=utf-8"},
		"/assets/asset-manifest.json": {body: `{"version":1}`, mime: "application/json; charset=utf-8"},
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != want.body || response.Header().Get("Content-Type") != want.mime {
			t.Fatalf("readable path=%s status=%d body=%q mime=%q", path, response.Code, response.Body.String(), response.Header().Get("Content-Type"))
		}
		if strings.Contains(response.Header().Get("Content-Security-Policy"), "unsafe-inline") {
			t.Fatalf("asset CSP unexpectedly relaxed for %s: %q", path, response.Header().Get("Content-Security-Policy"))
		}
	}

	for _, path := range []string{"/admin/campaigns.html", "/admin/customers.html", "/customers"} {
		response = httptest.NewRecorder()
		ui.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("effects UI exposed %s with status=%d", path, response.Code)
		}
	}
}

func TestTagsPageUsesItsBoundV3UIInsteadOfTheGenericShell(t *testing.T) {
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Tag-UI", "bound")
		writer.WriteHeader(http.StatusNoContent)
	})
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := routeApplicationWithMediaTags(marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, webshell.MustHandler(), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}

	// The canonical tags page must reach the V3-owned tag UI binding. A
	// generic shell would render successfully while dropping the real tag
	// operations, so the marker proves the precise handler remains mounted.
	request := httptest.NewRequest(http.MethodGet, "/admin/wecom-tags", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Tag-UI") != "bound" {
		t.Fatalf("tags page did not reach the bound UI status=%d marker=%q", response.Code, response.Header().Get("X-Tag-UI"))
	}

	// The donor staging name stays private; the built document name is an
	// ordinary shell page (placeholder in this harness, built page in release).
	request = httptest.NewRequest(http.MethodGet, "/admin/tags.html", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("private donor staging /admin/tags.html became routable: status=%d", response.Code)
	}
}

func TestCouponRoutesAreExplicitAndClaimPageMounts(t *testing.T) {
	marker := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Coupon", "yes")
		w.WriteHeader(http.StatusNoContent)
	})
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := routeApplicationWithProductsCoupons(marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, webshell.MustHandler(), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/coupons", "/api/admin/coupons/7", "/admin/coupons", "/admin/coupons/7/edit", "/admin/coupons.html", "/admin/couponForm.html?id=7"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent || res.Header().Get("X-Coupon") != "yes" {
			t.Fatalf("coupon route %s status=%d owner=%q", path, res.Code, res.Header().Get("X-Coupon"))
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/couponData.html?id=7", nil)
	req.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent || res.Header().Get("X-Coupon") != "yes" {
		t.Fatalf("couponData=%d owner=%q", res.Code, res.Header().Get("X-Coupon"))
	}
}

func TestApplicationRouterRejectsCrossSiteUnsafeRequests(t *testing.T) {
	authentication := &fakeAccessAuthentication{}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler, err := routeApplication(marker, marker, marker, marker, marker, authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		origin    string
		fetchSite string
		want      int
	}{
		{name: "same origin", origin: "https://crm.example", fetchSite: "same-origin", want: http.StatusNoContent},
		{name: "same origin overrides inconsistent fetch metadata", origin: "https://crm.example", fetchSite: "cross-site", want: http.StatusNoContent},
		{name: "cross origin", origin: "https://evil.example", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "cross origin overrides misleading fetch metadata", origin: "https://evil.example", fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "opaque origin", origin: "null", fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "cross-site without origin", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "provider callback without browser headers", want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestApplicationRouterDefersLoginPostToIndependentCSRFProtection(t *testing.T) {
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler, err := routeApplication(marker, marker, marker, marker, marker, &fakeAccessAuthentication{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.Header.Set("Origin", "null")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAIAssistantMountRejectsCrossSiteUnsafeRequests(t *testing.T) {
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := mountAIAssistant(marker, marker, marker, &fakeAccessAuthentication{}, true, "https://crm.example")

	request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-assistant/plans/7/approve", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/integrations/ai-assistant/review-plans", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("machine request status=%d body=%s", response.Code, response.Body.String())
	}
}
