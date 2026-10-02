package http

import (
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLIChallengeReusesLoginCSRFAndSessionSelfReadRequiresAuthentication(t *testing.T) {
	h, e := NewHandler(Config{Renderer: testRenderer{}, Auth: testAuth{}, Management: testManagement{}, CookieSecure: true})
	if e != nil {
		t.Fatal(e)
	}
	challenge := httptest.NewRecorder()
	h.Routes().ServeHTTP(challenge, httptest.NewRequest("GET", "https://crm.test/api/admin/access/cli/login-challenge", nil))
	if challenge.Code != 200 || !strings.Contains(challenge.Body.String(), "login_csrf_token") || challenge.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(challenge.Body.String())
	}
	cookies := challenge.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != LoginCSRFCookieName || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("cookies=%+v", cookies)
	}
	denied := httptest.NewRecorder()
	h.Routes().ServeHTTP(denied, httptest.NewRequest("GET", "/api/admin/access/cli/session", nil))
	if denied.Code != 401 {
		t.Fatalf("unauthenticated=%d", denied.Code)
	}
	h.auth = testAuth{authenticateAs: domain.Principal{Kind: domain.KindAdmin, InternalID: 7, Roles: []domain.Role{domain.RoleViewer}, SessionVersion: 3}}
	allowed := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/admin/access/cli/session", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	h.Routes().ServeHTTP(allowed, r)
	if allowed.Code != 200 || !strings.Contains(allowed.Body.String(), `"actor_id":7`) || strings.Contains(allowed.Body.String(), "session-secret") {
		t.Fatal(allowed.Body.String())
	}
}
