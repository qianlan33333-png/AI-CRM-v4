package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	openport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

func TestCLIHumanDiscoveryDoesNotPromoteRolesOrIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, operation, decision string
		role                      accessdomain.Role
	}{
		{"viewer-read", "admin.listAdminUsers", "requires_resource_check", accessdomain.RoleViewer},
		{"viewer-write", "admin.provisionAccessEnterpriseEmployee", "denied", accessdomain.RoleViewer},
		{"admin-governance", "admin.createOpenPlatformClient", "denied", accessdomain.RoleAdmin},
		{"super-governance", "admin.createOpenPlatformClient", "requires_resource_check", accessdomain.RoleSuperAdmin},
		{"super-machine", "customer.context.get", "denied", accessdomain.RoleSuperAdmin},
		{"super-candidate", "candidate.wecom.moments", "denied", accessdomain.RoleSuperAdmin},
		{"super-legacy-integration", "admin.createLegacyAIAssistantReviewPlan", "denied", accessdomain.RoleSuperAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{tc.role}}}
			if tc.name == "super-governance" {
				auth.principal.Roles = append(auth.principal.Roles, accessdomain.RoleViewer)
			}
			fallbackCalls := 0
			h := mountCLIHuman(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { fallbackCalls++ }), auth)
			r := httptest.NewRequest("POST", "/api/admin/cli/can-i", strings.NewReader(`{"operation_id":"`+tc.operation+`"}`))
			r.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "SESSION"})
			r.AddCookie(&http.Cookie{Name: "aicrm_admin_csrf", Value: "CSRF"})
			r.Header.Set("X-CSRF-Token", "CSRF")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || result["decision"] != tc.decision || fallbackCalls != 0 || auth.csrf != [3]string{"SESSION", "CSRF", "CSRF"} {
				t.Fatalf("status=%d result=%v csrf=%v calls=%d", w.Code, result, auth.csrf, fallbackCalls)
			}
		})
	}
	for _, kind := range []accessdomain.Kind{accessdomain.KindCustomer, accessdomain.KindService} {
		auth := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: kind, InternalID: 7}}
		h := mountCLIHuman(http.NotFoundHandler(), auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/cli/capabilities", nil))
		if w.Code != 401 {
			t.Fatalf("non-human principal entered catalog: %s %d", kind, w.Code)
		}
	}
	// Duplicate authorization fields never reach either discovery or a business handler.
	auth := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}
	h := mountCLIHuman(http.NotFoundHandler(), auth)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/cli/can-i", strings.NewReader(`{"operation_id":"admin.listAdminUsers","operation_id":"customer.context.get"}`)))
	if w.Code != 400 {
		t.Fatalf("ambiguous authorization accepted: %d", w.Code)
	}
	for _, body := range []string{
		`{"operation_id":"admin.listAdminUsers","Operation_ID":"customer.context.get"}`,
		`{"operation_id":"admin.listAdminUsers","resource":{},"Resource":{"customer_id":8}}`,
	} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/cli/can-i", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("case ambiguity accepted: %d", w.Code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/cli/capabilities", nil))
	var result struct {
		Catalog struct {
			Operations []struct {
				Binding *struct {
					Auth string `json:"auth"`
				} `json:"binding"`
			} `json:"operations"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, op := range result.Catalog.Operations {
		if op.Binding != nil && op.Binding.Auth != "human" {
			t.Fatal("machine executable binding in human catalog")
		}
	}
}

func TestCLIMachineCanIChecksTrustedResourceAndNeverExecutes(t *testing.T) {
	e := &openPlatformExecutor{}
	p := accessdomain.MachinePrincipal{OwnerScope: accessdomain.OwnerScope{"customer_id": {"7"}}}
	for _, tc := range []struct{ input, decision string }{
		{`{"customer_id":7}`, "allowed"},
		{`{"customer_id":8}`, "denied"},
		{`{"customer_id":8,"owner_userid":"approved-owner"}`, "denied"},
		{`{"customer_id":7,"customer_id":8}`, "denied"},
		{`{"customer_id":7,"Customer_ID":8}`, "denied"},
		{`{"Customer_ID":7}`, "denied"},
		{``, "requires_resource_check"},
	} {
		d, err := e.CheckPermission(context.Background(), p, openport.OperationCustomerDetail, json.RawMessage(tc.input))
		if err != nil || d.Decision != tc.decision {
			t.Fatalf("decision=%v err=%v", d, err)
		}
	}
}
