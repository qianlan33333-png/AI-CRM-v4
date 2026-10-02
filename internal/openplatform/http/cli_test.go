package http

import (
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	openport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLIV2DiscoveryAndPermissionNeverInvokeBusinessOrPromoteIdentity(t *testing.T) {
	ops := &handlerOperationStub{available: []openport.Descriptor{{OperationID: openport.OperationCustomerContext}}}
	h, e := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "client-a"}}, RateLimiter: &handlerRateLimiterStub{}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Operations: ops, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct{ body, decision string }{{`{"operation_id":"customer.context.get"}`, "requires_resource_check"}, {`{"operation_id":"admin.listUsers"}`, "denied"}, {`{"operation_id":"candidate.wecom.moments"}`, "denied"}} {
		r := machineRequest("POST", "https://crm.test/open/v2/cli/can-i", test.body)
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), test.decision) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if len(ops.invocations) != 0 {
		t.Fatal("permission query executed business operation")
	}
	r := machineRequest("GET", "https://crm.test/open/v2/cli/context", "")
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "client-a") {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{
		`{"operation_id":"customer.context.get","operation_id":"admin.listUsers"}`,
		`{"operation_id":"customer.context.get","Operation_ID":"admin.listUsers"}`,
		`{"operation_id":"customer.context.get","resource":{},"Resource":{"customer_id":8}}`,
	} {
		r := machineRequest("POST", "https://crm.test/open/v2/cli/can-i", body)
		w = httptest.NewRecorder()
		h.Routes().ServeHTTP(w, r)
		if w.Code != 400 || len(ops.invocations) != 0 {
			t.Fatal("ambiguous permission input accepted or invoked business")
		}
	}
}
