package externaleffects

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// A nil pool intentionally makes unauthorized store access observable as a
// panic. Both the canonical EER and historical PushCenter API must reject the
// same foreign principal or Viewer control before resolving any effect ID.
func TestSol61EffectsAliasesRejectForeignPrincipalsBeforeObjectLookup(t *testing.T) {
	for _, principal := range []accessdomain.Principal{
		{Kind: accessdomain.KindCustomer, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}},
		{Kind: accessdomain.KindService, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}},
		{Kind: accessdomain.KindAdmin, InternalID: 0, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}},
		{Kind: accessdomain.KindAdmin, InternalID: 7},
		{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{"invalid_role"}},
	} {
		name := string(principal.Kind) + "/" + strings.Join(sol61RoleStrings(principal.Roles), ",")
		t.Run(name, func(t *testing.T) {
			security := effectTestSecurity{readPrincipal: principal, writePrincipal: principal}
			for _, path := range []string{"/api/admin/external-effects/eer_1", "/api/admin/external-effects/eer_999999", "/api/admin/push-center/jobs/1", "/api/admin/push-center/jobs/999999"} {
				var handler http.Handler = &HTTPHandler{repository: &Repository{}, security: security}
				if strings.Contains(path, "push-center") {
					handler = &PushCenterHandler{repository: &Repository{}, security: security}
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusForbidden {
					t.Fatalf("foreign principal reached %s: status=%d", path, response.Code)
				}
			}
		})
	}
	viewer := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	for _, security := range []effectTestSecurity{
		{readPrincipal: viewer, writePrincipal: viewer},
		{readPrincipal: admin, writePrincipal: admin, writeErr: errors.New("CSRF rejected")},
	} {
		for _, path := range []string{"/api/admin/external-effects/eer_1/retry", "/api/admin/external-effects/eer_999999/cancel", "/api/admin/push-center/jobs/1/retry", "/api/admin/push-center/jobs/999999/cancel"} {
			var handler http.Handler = &HTTPHandler{repository: &Repository{}, security: security}
			if strings.Contains(path, "push-center") {
				handler = &PushCenterHandler{repository: &Repository{}, security: security}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
			if response.Code != http.StatusForbidden {
				t.Fatalf("unauthorized control reached %s: status=%d", path, response.Code)
			}
		}
	}
}

func sol61RoleStrings(values []accessdomain.Role) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}
