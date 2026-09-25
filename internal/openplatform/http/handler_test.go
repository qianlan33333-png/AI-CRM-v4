package http

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

type handlerMachineStub struct {
	principal accessdomain.MachinePrincipal
	err       error
}

func (stub handlerMachineStub) IssueClientCredentialsToken(context.Context, accessport.ClientCredentialsInput) (accessport.IssuedAccessToken, error) {
	return accessport.IssuedAccessToken{AccessToken: "token", TokenType: "Bearer", ExpiresIn: 1800, Scope: "read"}, nil
}
func (stub handlerMachineStub) AuthenticateBearer(context.Context, string, string, netip.Addr) (accessdomain.MachinePrincipal, error) {
	return stub.principal, stub.err
}

type handlerRateLimiterStub struct {
	credentialErr error
	machineErr    error
	credentialHit int
	machineHit    int
}

func (stub *handlerRateLimiterStub) AllowClientCredentials(context.Context, string, netip.Addr) error {
	stub.credentialHit++
	return stub.credentialErr
}
func (stub *handlerRateLimiterStub) AllowMachineRequest(context.Context, accessdomain.MachinePrincipal, netip.Addr) error {
	stub.machineHit++
	return stub.machineErr
}

type handlerAdminStub struct{}

func (handlerAdminStub) Authenticate(context.Context, string) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, nil
}
func (handlerAdminStub) AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error) {
	return handlerAdminStub{}.Authenticate(context.Background(), "")
}

type handlerManagementStub struct{}

func (handlerManagementStub) Create(context.Context, accessdomain.Principal, accessport.CreateMachineClientInput) (accessport.IssuedMachineClient, error) {
	return accessport.IssuedMachineClient{}, nil
}
func (handlerManagementStub) CreateV1(context.Context, accessdomain.Principal, accessport.CreateMachineClientInput) (accessport.IssuedMachineClient, error) {
	return accessport.IssuedMachineClient{}, nil
}
func (handlerManagementStub) List(context.Context, accessdomain.Principal) ([]accessport.MachineClientSummary, error) {
	return []accessport.MachineClientSummary{}, nil
}
func (handlerManagementStub) Rotate(context.Context, accessdomain.Principal, string) (accessport.IssuedMachineClient, error) {
	return accessport.IssuedMachineClient{}, nil
}
func (handlerManagementStub) Update(context.Context, accessdomain.Principal, string, accessport.UpdateMachineClientInput) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}
func (handlerManagementStub) Get(context.Context, accessdomain.Principal, string) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}
func (handlerManagementStub) PatchV1(context.Context, accessdomain.Principal, string, accessport.PatchMachineClientInput) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}
func (handlerManagementStub) ListAudit(context.Context, accessdomain.Principal, string, int) ([]accessport.MachineAuditEntry, error) {
	return []accessport.MachineAuditEntry{}, nil
}
func (handlerManagementStub) Activate(context.Context, accessdomain.Principal, string, string, bool) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}
func (handlerManagementStub) SetEnabled(context.Context, accessdomain.Principal, string, bool) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}

type handlerOperationStub struct {
	available       []openplatformport.Descriptor
	invocations     []openplatformport.Invocation
	result          openplatformport.Result
	invokeErr       error
	availableErr    error
	waitForDeadline bool
	deadlineSeen    bool
}

func (stub *handlerOperationStub) Available(context.Context, accessdomain.MachinePrincipal) ([]openplatformport.Descriptor, error) {
	return append([]openplatformport.Descriptor(nil), stub.available...), stub.availableErr
}
func (stub *handlerOperationStub) Invoke(ctx context.Context, invocation openplatformport.Invocation) (openplatformport.Result, error) {
	stub.invocations = append(stub.invocations, invocation)
	if stub.waitForDeadline {
		<-ctx.Done()
		stub.deadlineSeen = true
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "deadline")
	}
	if stub.result.Data == nil && stub.invokeErr == nil {
		return openplatformport.Result{Data: map[string]any{"ok": true}}, nil
	}
	return stub.result, stub.invokeErr
}

func newV1Handler(t *testing.T, principal accessdomain.MachinePrincipal, operations *handlerOperationStub) *Handler {
	t.Helper()
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: principal}, RateLimiter: &handlerRateLimiterStub{}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Operations: operations, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func machineRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Authorization", "Bearer test")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func TestV1RoutesMountOnlyOperationCatalogPaths(t *testing.T) {
	operations := &handlerOperationStub{}
	handler := newV1Handler(t, accessdomain.MachinePrincipal{}, operations)
	for _, item := range []struct{ method, path, body string }{
		{http.MethodGet, "/open/v1/capabilities", ""},
		{http.MethodPost, "/open/v1/customers:resolve", `{}`},
		{http.MethodGet, "/open/v1/customers/42", ""},
		{http.MethodGet, "/open/v1/customers/42/activities", ""},
		{http.MethodPost, "/open/v1/ai/review-plans", `{}`},
		{http.MethodGet, "/open/v1/operations/op-42", ""},
		{http.MethodGet, "/open/v1/orders", ""},
		{http.MethodGet, "/open/v1/orders/42", ""},
	} {
		request := machineRequest(item.method, "https://crm.example.com"+item.path, item.body)
		response := httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, request)
		if response.Code == http.StatusNotFound {
			t.Fatalf("catalog route %s %s was not mounted", item.method, item.path)
		}
	}
	request := machineRequest(http.MethodGet, "https://crm.example.com/api/external/orders", "")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("retired route code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRESTNormalizesPathIntoSameInvocationDTO(t *testing.T) {
	operations := &handlerOperationStub{result: openplatformport.Result{Data: map[string]any{"customer_id": 42}}}
	handler := newV1Handler(t, accessdomain.MachinePrincipal{ClientID: "client-a", Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerRead)}}, operations)
	request := machineRequest(http.MethodGet, "https://crm.example.com/open/v1/customers/42", "")
	request.Header.Set("X-Request-ID", "request-42")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(operations.invocations) != 1 {
		t.Fatalf("code=%d invocations=%+v body=%s", response.Code, operations.invocations, response.Body.String())
	}
	invocation := operations.invocations[0]
	if invocation.Operation != openplatformport.OperationCustomerContext || string(invocation.Input) != `{"customer_id":42}` || invocation.RequestID != "request-42" {
		t.Fatalf("invocation=%+v", invocation)
	}
	if !strings.Contains(response.Body.String(), `"data":{"customer_id":42}`) || !strings.Contains(response.Body.String(), `"request_id":"request-42"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestRESTRejectsPathQueryOrBodyConflictBeforeApplication(t *testing.T) {
	operations := &handlerOperationStub{}
	handler := newV1Handler(t, accessdomain.MachinePrincipal{}, operations)
	request := machineRequest(http.MethodGet, "https://crm.example.com/open/v1/customers/42?customer_id=43", "")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(operations.invocations) != 0 || !strings.Contains(response.Body.String(), `"code":"validation"`) {
		t.Fatalf("code=%d invocations=%+v body=%s", response.Code, operations.invocations, response.Body.String())
	}
	request = machineRequest(http.MethodGet, "https://crm.example.com/open/v1/customers/42", `{"customer_id":43}`)
	request.ContentLength = -1 // Exercise a chunked body rather than only Content-Length input.
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(operations.invocations) != 0 {
		t.Fatalf("chunked body code=%d invocations=%+v body=%s", response.Code, operations.invocations, response.Body.String())
	}
}

func TestRESTAndMCPInvokeSameOperationWithSameInput(t *testing.T) {
	descriptor, _ := openplatformport.DescriptorForOperation(openplatformport.OperationCustomerResolve)
	operations := &handlerOperationStub{available: []openplatformport.Descriptor{descriptor}}
	principal := accessdomain.MachinePrincipal{ClientID: "client-a", Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}}
	handler := newV1Handler(t, principal, operations)
	payload := `{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"u-42"}]}`
	rest := machineRequest(http.MethodPost, "https://crm.example.com/open/v1/customers:resolve", payload)
	restResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(restResponse, rest)
	mcp := machineRequest(http.MethodPost, "https://crm.example.com/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"resolve_customer","arguments":`+payload+`}}`)
	mcpResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpResponse, mcp)
	if restResponse.Code != http.StatusOK || mcpResponse.Code != http.StatusOK || len(operations.invocations) != 2 {
		t.Fatalf("rest=%d mcp=%d invocations=%+v", restResponse.Code, mcpResponse.Code, operations.invocations)
	}
	if operations.invocations[0].Operation != operations.invocations[1].Operation || string(operations.invocations[0].Input) != string(operations.invocations[1].Input) {
		t.Fatalf("REST=%+v MCP=%+v", operations.invocations[0], operations.invocations[1])
	}
	if !strings.Contains(mcpResponse.Body.String(), `"structuredContent"`) {
		t.Fatalf("mcp body=%s", mcpResponse.Body.String())
	}
}

func TestMCPToolListIsDynamicAndRetiredToolsAreAbsent(t *testing.T) {
	descriptor, _ := openplatformport.DescriptorForOperation(openplatformport.OperationCustomerContext)
	operations := &handlerOperationStub{available: []openplatformport.Descriptor{descriptor}}
	handler := newV1Handler(t, accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerRead)}}, operations)
	request := machineRequest(http.MethodPost, "https://crm.example.com/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"get_customer_context"`) || strings.Contains(response.Body.String(), "get_recent_messages") {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestMCPPreservesApplicationErrorCategory(t *testing.T) {
	descriptor, _ := openplatformport.DescriptorForOperation(openplatformport.OperationCustomerResolve)
	operations := &handlerOperationStub{available: []openplatformport.Descriptor{descriptor}, invokeErr: openplatformport.NewError(openplatformport.ErrorIdentityConflict, "conflict")}
	handler := newV1Handler(t, accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}}, operations)
	request := machineRequest(http.MethodPost, "https://crm.example.com/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"resolve_customer","arguments":{"references":[]}}}`)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"category":"identity_conflict"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestTrustedProxyTakesRightmostUntrustedForwardedSource(t *testing.T) {
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{}, RateLimiter: &handlerRateLimiterStub{}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Operations: &handlerOperationStub{}, SessionCookieName: "session", CSRFCookieName: "csrf", TrustedProxyCIDRs: []string{"192.0.2.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://crm.example.com/open/v1/capabilities", nil)
	request.RemoteAddr = "192.0.2.10:443"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
	source, err := handler.source(request)
	if err != nil || source != netip.MustParseAddr("203.0.113.9") {
		t.Fatalf("forwarded source = %v, %v", source, err)
	}
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.20")
	source, err = handler.source(request)
	if err != nil || source != netip.MustParseAddr("198.51.100.7") {
		t.Fatalf("trusted intermediary source = %v, %v", source, err)
	}
}

func TestMountClaimsOnlyV1MachinePaths(t *testing.T) {
	machine := markerHandler("machine")
	legacy := markerHandler("legacy")
	mounted := Mount(legacy, machine)
	for _, item := range []struct{ method, path, expected string }{
		{http.MethodPost, "/oauth/token", "machine"},
		{http.MethodGet, "/open/v1/capabilities", "machine"},
		{http.MethodPost, "/open/v1/ai/review-plans", "machine"},
		{http.MethodGet, "/api/admin/open-platform/clients", "machine"},
		{http.MethodPatch, "/api/admin/open-platform/clients/client-a", "machine"},
		{http.MethodGet, "/api/admin/open-platform/clients/client-a/audit", "machine"},
		{http.MethodPost, "/api/admin/open-platform/clients/client-a/activate", "machine"},
		{http.MethodGet, "/api/external/orders", "404 page not found"},
		{http.MethodPost, "/api/operation-cycles/reports", "404 page not found"},
	} {
		request := httptest.NewRequest(item.method, "https://crm.example.com"+item.path, nil)
		response := httptest.NewRecorder()
		mounted.ServeHTTP(response, request)
		if body := strings.TrimSpace(response.Body.String()); body != item.expected {
			t.Fatalf("%s %s mounted to %q, want %q", item.method, item.path, body, item.expected)
		}
	}
}

func TestNewHandlerRequiresV1OperationService(t *testing.T) {
	_, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err == nil || !strings.Contains(err.Error(), "dependencies") {
		t.Fatalf("err=%v", err)
	}
}

func markerHandler(value string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(value)) })
}

func TestAIReviewPlanRequiresIdempotencyKeyOnRESTAndMCP(t *testing.T) {
	descriptor, _ := openplatformport.DescriptorForOperation(openplatformport.OperationAIReviewPlanCreate)
	operations := &handlerOperationStub{available: []openplatformport.Descriptor{descriptor}}
	principal := accessdomain.MachinePrincipal{Scopes: []string{"write"}, Capabilities: []string{string(openplatformport.CapabilityAIReviewPlanCreate)}}
	handler := newV1Handler(t, principal, operations)
	payload := `{"name":"review","source_kind":"open","source_digest":"digest","recipients":[]}`
	rest := machineRequest(http.MethodPost, "https://crm.example.com/open/v1/ai/review-plans", payload)
	restResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(restResponse, rest)
	mcp := machineRequest(http.MethodPost, "https://crm.example.com/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_ai_review_plan","arguments":`+payload+`}}`)
	mcpResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpResponse, mcp)
	if restResponse.Code != http.StatusBadRequest || mcpResponse.Code != http.StatusOK || !strings.Contains(mcpResponse.Body.String(), `"category":"validation"`) || len(operations.invocations) != 0 {
		t.Fatalf("REST=%d MCP=%d invocations=%d mcpbody=%s", restResponse.Code, mcpResponse.Code, len(operations.invocations), mcpResponse.Body.String())
	}
}

func TestV1RejectsOversizeAndDuplicateJSONBeforeOperation(t *testing.T) {
	operations := &handlerOperationStub{}
	handler := newV1Handler(t, accessdomain.MachinePrincipal{}, operations)
	duplicate := machineRequest(http.MethodPost, "https://crm.example.com/open/v1/customers:resolve", `{"references":[],"references":[]}`)
	duplicateResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusBadRequest || len(operations.invocations) != 0 {
		t.Fatalf("duplicate response=%d invocations=%d", duplicateResponse.Code, len(operations.invocations))
	}
	overse := machineRequest(http.MethodPost, "https://crm.example.com/open/v1/customers:resolve", `{"references":"`+strings.Repeat("x", int(maxBodyBytes))+`"}`)
	overseResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(overseResponse, overse)
	if overseResponse.Code != http.StatusBadRequest || len(operations.invocations) != 0 {
		t.Fatalf("oversize response=%d invocations=%d", overseResponse.Code, len(operations.invocations))
	}
	mcp := machineRequest(http.MethodPost, "https://crm.example.com/mcp", `{"jsonrpc":"2.0","jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"resolve_customer","arguments":{"references":[]}}}`)
	mcp.Header.Set("X-Request-ID", "mcp-request-42")
	mcpResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpResponse, mcp)
	if mcpResponse.Code != http.StatusOK || len(operations.invocations) != 0 || !strings.Contains(mcpResponse.Body.String(), `"category":"validation"`) || mcpResponse.Header().Get("X-Request-ID") != "mcp-request-42" {
		t.Fatalf("mcp duplicate code=%d invocations=%d request_id=%q body=%s", mcpResponse.Code, len(operations.invocations), mcpResponse.Header().Get("X-Request-ID"), mcpResponse.Body.String())
	}
}

func TestRESTActivityQueryUsesCanonicalDTOAndRejectsAliases(t *testing.T) {
	operations := &handlerOperationStub{}
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerActivityRead)}}
	handler := newV1Handler(t, principal, operations)
	request := machineRequest(http.MethodGet, "https://crm.example.com/open/v1/customers/42/activities?types=order&types=message&limit=20", "")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(operations.invocations) != 1 || string(operations.invocations[0].Input) != `{"customer_id":42,"limit":20,"types":["order","message"]}` {
		t.Fatalf("code=%d invocations=%+v body=%s", response.Code, operations.invocations, response.Body.String())
	}
	for _, target := range []string{
		"https://crm.example.com/open/v1/customers/42/activities?customer_id=43",
		"https://crm.example.com/open/v1/customers/42/activities?cursor=a&cursor=b",
		"https://crm.example.com/open/v1/customers/42/activities?limit=01",
		"https://crm.example.com/open/v1/customers/42/activities?types=message&types=message",
	} {
		request = machineRequest(http.MethodGet, target, "")
		response = httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || len(operations.invocations) != 1 {
			t.Fatalf("target=%s code=%d invocations=%+v", target, response.Code, operations.invocations)
		}
	}
	request = machineRequest(http.MethodGet, "https://crm.example.com/open/v1/customers/42/activities", `{"customer_id":43}`)
	request.ContentLength = -1
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(operations.invocations) != 1 {
		t.Fatalf("chunked activity body code=%d invocations=%+v", response.Code, operations.invocations)
	}
}

func TestV1MachinePatchInputUsesExplicitPresenceForClears(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "https://crm.example.test/api/admin/open-platform/clients/client-a", strings.NewReader(`{"capabilities":["customer.read"],"owner_scope":null,"expires_at":null}`))
	request.Header.Set("Content-Type", "application/json")
	input, err := v1MachinePatchInput(request)
	if err != nil || input.Capabilities == nil || len(*input.Capabilities) != 1 || (*input.Capabilities)[0] != "customer.read" || !input.OwnerScopeSet || len(input.OwnerScope) != 0 || !input.ExpiresAtSet || input.ExpiresAt != nil {
		t.Fatalf("patch input=%+v err=%v", input, err)
	}
}

func TestV1MachinePatchInputRejectsDuplicateAndEmptyGrantAmbiguity(t *testing.T) {
	for _, body := range []string{
		`{"capabilities":["customer.read"],"capabilities":["customer.resolve"]}`,
		`{}`,
		`{"capabilities":null}`,
	} {
		request := httptest.NewRequest(http.MethodPatch, "https://crm.example.test/api/admin/open-platform/clients/client-a", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if _, err := v1MachinePatchInput(request); err == nil {
			t.Fatalf("patch body %s was accepted", body)
		}
	}
}

func TestV1MachineRateLimitRejectsBeforeOperationAcrossRESTAndMCP(t *testing.T) {
	operations := &handlerOperationStub{}
	limiter := &handlerRateLimiterStub{machineErr: accessdomain.MachineRateLimitError{RetryAfter: 1500 * time.Millisecond}}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "client-a", ClientRecord: 7}}, RateLimiter: limiter, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Operations: operations, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	rest := machineRequest(http.MethodGet, "https://crm.example.com/open/v1/capabilities", "")
	restResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(restResponse, rest)
	if restResponse.Code != http.StatusTooManyRequests || restResponse.Header().Get("Retry-After") != "2" || !strings.Contains(restResponse.Body.String(), `"code":"rate_limited"`) || len(operations.invocations) != 0 {
		t.Fatalf("REST status=%d invocations=%d body=%s", restResponse.Code, len(operations.invocations), restResponse.Body.String())
	}
	mcp := machineRequest(http.MethodPost, "https://crm.example.com/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	mcpResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpResponse, mcp)
	if mcpResponse.Code != http.StatusOK || !strings.Contains(mcpResponse.Body.String(), `"category":"rate_limited"`) || limiter.machineHit != 2 {
		t.Fatalf("MCP status=%d hits=%d body=%s", mcpResponse.Code, limiter.machineHit, mcpResponse.Body.String())
	}
}

func TestOAuthCredentialRateLimitAndBodyBound(t *testing.T) {
	limiter := &handlerRateLimiterStub{credentialErr: accessdomain.MachineRateLimitError{RetryAfter: 1500 * time.Millisecond}}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{}, RateLimiter: limiter, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Operations: &handlerOperationStub{}, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.com/oauth/token", strings.NewReader("grant_type=client_credentials&client_id=client-a&client_secret=secret&audience=external_integration"))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "2" || !strings.Contains(response.Body.String(), `"error":"rate_limited"`) || limiter.credentialHit != 1 {
		t.Fatalf("rate status=%d hits=%d body=%s", response.Code, limiter.credentialHit, response.Body.String())
	}

	overse := httptest.NewRequest(http.MethodPost, "https://crm.example.com/oauth/token", strings.NewReader("grant_type=client_credentials&client_id="+strings.Repeat("x", int(maxBodyBytes))))
	overse.TLS = &tls.ConnectionState{}
	overse.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	overseResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(overseResponse, overse)
	if overseResponse.Code != http.StatusBadRequest || limiter.credentialHit != 1 {
		t.Fatalf("oversize status=%d hits=%d body=%s", overseResponse.Code, limiter.credentialHit, overseResponse.Body.String())
	}
}

func TestV1RequestDeadlinePropagatesToOperation(t *testing.T) {
	operations := &handlerOperationStub{waitForDeadline: true}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "client-a", ClientRecord: 7}}, RateLimiter: &handlerRateLimiterStub{}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Operations: operations, SessionCookieName: "session", CSRFCookieName: "csrf", RequestTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	request := machineRequest(http.MethodGet, "https://crm.example.com/open/v1/capabilities", "")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !operations.deadlineSeen || len(operations.invocations) != 1 {
		t.Fatalf("status=%d deadline=%t invocations=%d body=%s", response.Code, operations.deadlineSeen, len(operations.invocations), response.Body.String())
	}
}
