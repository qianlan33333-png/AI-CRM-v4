package externaleffects

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type effectTestSecurity struct {
	readPrincipal  accessdomain.Principal
	writePrincipal accessdomain.Principal
	readErr        error
	writeErr       error
}

func (s effectTestSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.readPrincipal, s.readErr
}
func (s effectTestSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.writePrincipal, s.writeErr
}

func TestEffectsAndPushCenterShareViewerReadAndAdminControlPolicy(t *testing.T) {
	viewer := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	admin := accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/external-effects/eer_1/retry", nil)
	for _, handler := range []struct {
		name  string
		read  func(http.ResponseWriter, *http.Request) bool
		write func(http.ResponseWriter, *http.Request) (accessdomain.Principal, bool)
	}{
		{name: "effects", read: (&HTTPHandler{security: effectTestSecurity{readPrincipal: viewer}}).read, write: (&HTTPHandler{security: effectTestSecurity{writePrincipal: viewer}}).mutate},
		{name: "push", read: (&PushCenterHandler{security: effectTestSecurity{readPrincipal: viewer}}).read, write: (&PushCenterHandler{security: effectTestSecurity{writePrincipal: viewer}}).mutate},
	} {
		t.Run(handler.name, func(t *testing.T) {
			if !handler.read(httptest.NewRecorder(), request) {
				t.Fatal("viewer read was rejected")
			}
			response := httptest.NewRecorder()
			if _, ok := handler.write(response, request); ok || response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "permission_denied") {
				t.Fatalf("viewer write status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
	response := httptest.NewRecorder()
	principal, ok := (&HTTPHandler{security: effectTestSecurity{writePrincipal: admin}}).mutate(response, request)
	if !ok || principal.InternalID != admin.InternalID || response.Code != http.StatusOK {
		t.Fatalf("admin write principal=%+v ok=%t status=%d", principal, ok, response.Code)
	}
}

func TestEffectsRoutesNeverReturnAnEmptySuccessForMalformedPaths(t *testing.T) {
	viewer := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	security := effectTestSecurity{readPrincipal: viewer, writePrincipal: viewer}
	effects := &HTTPHandler{repository: &Repository{}, security: security}
	push := &PushCenterHandler{repository: &Repository{}, security: security}
	for _, test := range []struct {
		name, method, path string
		handler            http.Handler
		want               int
		allow              string
	}{
		{"effects unknown", http.MethodGet, "/api/admin/external-effects/bad", effects, http.StatusNotFound, ""},
		{"effects bad control", http.MethodPost, "/api/admin/external-effects/eer_1/bad", effects, http.StatusNotFound, ""},
		{"effects get control", http.MethodGet, "/api/admin/external-effects/eer_1/cancel", effects, http.StatusMethodNotAllowed, http.MethodPost},
		{"effects post detail", http.MethodPost, "/api/admin/external-effects/eer_1", effects, http.StatusMethodNotAllowed, http.MethodGet},
		{"effects post root", http.MethodPost, "/api/admin/external-effects", effects, http.StatusMethodNotAllowed, http.MethodGet},
		{"effects post diagnostics", http.MethodPost, "/api/admin/external-effects/diagnostics", effects, http.StatusMethodNotAllowed, http.MethodGet},
		{"effects post jobs", http.MethodPost, "/api/admin/external-effects/jobs", effects, http.StatusMethodNotAllowed, http.MethodGet},
		{"push unknown", http.MethodGet, "/api/admin/push-center/bad", push, http.StatusNotFound, ""},
		{"push bad control", http.MethodPost, "/api/admin/push-center/jobs/1/bad", push, http.StatusNotFound, ""},
		{"push get control", http.MethodGet, "/api/admin/push-center/jobs/1/cancel", push, http.StatusMethodNotAllowed, http.MethodPost},
		{"push post detail", http.MethodPost, "/api/admin/push-center/jobs/1", push, http.StatusMethodNotAllowed, http.MethodGet},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.want || response.Header().Get("Allow") != test.allow {
				t.Fatalf("status=%d allow=%q body=%q", response.Code, response.Header().Get("Allow"), response.Body.String())
			}
		})
	}
}
