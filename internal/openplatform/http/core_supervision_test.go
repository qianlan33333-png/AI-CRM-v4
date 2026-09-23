package http

import (
	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the public mount, not just the inner mux: this was the missing route.
func TestCoreAudiencePublicMount(t *testing.T) {
	for _, descriptor := range openplatformport.OperationCatalog() {
		if !strings.HasPrefix(descriptor.RESTPath, "/open/v1/audience/") {
			continue
		}
		t.Run(string(descriptor.OperationID), func(t *testing.T) {
			stub := &handlerOperationStub{}
			h := newV1Handler(t, accessdomain.MachinePrincipal{ClientID: "supervisor-a"}, stub)
			path := descriptor.RESTPath
			path = replaceTestPath(path)
			body := ""
			if descriptor.RESTMethod == "POST" {
				body = `{"push_id":"p-1","customer_id":7,"package_id":2}`
			}
			req := machineRequest(descriptor.RESTMethod, "https://crm.example.com"+path, body)
			req.Header.Set("Idempotency-Key", "push-report-00000001")
			w := httptest.NewRecorder()
			Mount(http.NotFoundHandler(), h.Routes()).ServeHTTP(w, req)
			if w.Code != 200 || len(stub.invocations) != 1 || stub.invocations[0].Operation != descriptor.OperationID {
				t.Fatalf("route=%s code=%d body=%s", path, w.Code, w.Body.String())
			}
			var envelope map[string]json.RawMessage
			if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope["data"] == nil || envelope["request_id"] == nil {
				t.Fatal("missing v1 envelope")
			}
		})
	}
}
func replaceTestPath(path string) string {
	return strings.NewReplacer("{package_id}", "2", "{customer_id}", "7").Replace(path)
}
func TestCoreAudienceValidationBeforeInvocation(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/open/v1/audience/push-records", `{`},
		{"GET", "/open/v1/audience/packages/no/members", ""},
		{"GET", "/open/v1/audience/packages/2/members?limit=101", ""},
		{"GET", "/open/v1/audience/packages/2/members?limit=1&limit=2", ""},
		{"GET", "/open/v1/audience/packages/2/members?customer_id=7", ""},
	} {
		stub := &handlerOperationStub{}
		h := newV1Handler(t, accessdomain.MachinePrincipal{}, stub)
		w := httptest.NewRecorder()
		Mount(http.NotFoundHandler(), h.Routes()).ServeHTTP(w, machineRequest(tc.method, "https://crm.example.com"+tc.path, tc.body))
		if w.Code != 400 || len(stub.invocations) != 0 {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
