package excel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

type bridgeHTTPSecurity struct{ principal accessdomain.Principal }

func (security bridgeHTTPSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, nil
}

func (security bridgeHTTPSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("csrf not expected for GET")
}

type bridgeHTTPAuthorizer struct {
	actions []accessport.AIAssistantAction
}

func (authorizer *bridgeHTTPAuthorizer) AuthorizeAIAssistant(_ context.Context, principal accessdomain.Principal, action accessport.AIAssistantAction) error {
	authorizer.actions = append(authorizer.actions, action)
	if principal.Validate() != nil || principal.Kind != accessdomain.KindAdmin {
		return accessdomain.ErrAuthentication
	}
	for _, role := range principal.Roles {
		if action == accessport.AIAssistantRead && (role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin) {
			return nil
		}
		if action == accessport.AIAssistantReview && (role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin) {
			return nil
		}
	}
	return accessdomain.ErrPermissionDenied
}

func TestBridgeReportCSVRequiresAdministratorButJSONReportRemainsReadable(t *testing.T) {
	for _, test := range []struct {
		name       string
		roles      []accessdomain.Role
		path       string
		wantStatus int
	}{
		{name: "viewer cannot export CSV", roles: []accessdomain.Role{accessdomain.RoleViewer}, path: "/api/admin/operation-batches/42/report.csv", wantStatus: http.StatusForbidden},
		{name: "admin can export CSV", roles: []accessdomain.Role{accessdomain.RoleAdmin}, path: "/api/admin/operation-batches/42/report.csv", wantStatus: http.StatusBadRequest},
		{name: "super admin can export CSV", roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}, path: "/api/admin/operation-batches/42/report.csv", wantStatus: http.StatusBadRequest},
		{name: "viewer can still read JSON report", roles: []accessdomain.Role{accessdomain.RoleViewer}, path: "/api/admin/operation-batches/42/report", wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &bridgeHTTPAuthorizer{}
			handler := &Bridge{
				Security:   bridgeHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: test.roles}},
				Authorizer: authorizer,
			}
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			wantAction := accessport.AIAssistantRead
			if test.path == "/api/admin/operation-batches/42/report.csv" {
				wantAction = accessport.AIAssistantReview
			}
			if len(authorizer.actions) != 1 || authorizer.actions[0] != wantAction {
				t.Fatalf("actions=%v want=%s", authorizer.actions, wantAction)
			}
		})
	}
}
