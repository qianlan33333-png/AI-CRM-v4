// Package http owns only machine protocol and DTO adaptation. Business
// operations are dispatched through the composition-owned openplatform Port.
package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

const maxBodyBytes int64 = 64 << 10

type AdminAuthentication interface {
	Authenticate(context.Context, string) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error)
}

type Config struct {
	MachineAuthentication accessport.MachineTokenIssuer
	RateLimiter           accessport.MachineRequestLimiter
	AdminAuthentication   AdminAuthentication
	Management            accessport.MachineManagement
	Operations            openplatformport.OperationService
	// Executor remains only as a source-compatible field while stale callers
	// are removed. V1 never dispatches through the retired route executor.
	Executor          openplatformport.Executor
	SessionCookieName string
	CSRFCookieName    string
	TrustedProxyCIDRs []string
	PublicOrigin      string
	RequestTimeout    time.Duration
}

type Handler struct {
	machine        accessport.MachineTokenIssuer
	rateLimiter    accessport.MachineRequestLimiter
	requestTimeout time.Duration
	admin          AdminAuthentication
	management     accessport.MachineManagement
	operations     openplatformport.OperationService
	executor       openplatformport.Executor
	sessionCookie  string
	csrfCookie     string
	trustedProxies []netip.Prefix
	publicOrigin   string
}

func NewHandler(config Config) (*Handler, error) {
	if config.MachineAuthentication == nil || config.RateLimiter == nil || config.AdminAuthentication == nil || config.Management == nil || config.Operations == nil || config.SessionCookieName == "" || config.CSRFCookieName == "" {
		return nil, errors.New("open platform HTTP dependencies are required")
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 10 * time.Second
	}
	proxies := make([]netip.Prefix, 0, len(config.TrustedProxyCIDRs))
	for _, raw := range config.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("invalid trusted proxy CIDR")
		}
		proxies = append(proxies, prefix.Masked())
	}
	return &Handler{machine: config.MachineAuthentication, rateLimiter: config.RateLimiter, requestTimeout: config.RequestTimeout, admin: config.AdminAuthentication, management: config.Management,
		operations: config.Operations, executor: config.Executor, sessionCookie: config.SessionCookieName, csrfCookie: config.CSRFCookieName, trustedProxies: proxies, publicOrigin: strings.TrimRight(strings.TrimSpace(config.PublicOrigin), "/")}, nil
}

func (handler *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", handler.token)
	mux.HandleFunc("POST /open/v1/audience/push-records", handler.coreSupervisedPush)
	mux.HandleFunc("GET /open/v1/audience/core-products", handler.coreOperationsRead)
	mux.HandleFunc("GET /open/v1/audience/packages/{package_id}/members/{customer_id}/history", handler.coreOperationsRead)
	mux.HandleFunc("GET /open/v1/audience/packages/{package_id}/members", handler.coreOperationsRead)
	mux.HandleFunc("GET /open/v1/audience/packages/{package_id}/members/{customer_id}/operations", handler.coreOperationsRead)
	mux.HandleFunc("GET /mcp", handler.mcpMetadata)
	mux.HandleFunc("POST /mcp", handler.mcp)
	mux.HandleFunc("GET /open/v1/capabilities", handler.v1Capabilities)
	mux.HandleFunc("POST /open/v1/customers:resolve", handler.v1ResolveCustomer)
	mux.HandleFunc("GET /open/v1/customers", handler.v1Customers)
	mux.HandleFunc("GET /open/v1/customers/{customer_id}", handler.v1CustomerContext)
	mux.HandleFunc("GET /open/v1/customers/{customer_id}/activities", handler.v1CustomerActivities)
	mux.HandleFunc("POST /open/v1/ai/review-plans", handler.v1AIReviewPlan)
	mux.HandleFunc("GET /open/v1/operations/{operation_id}", handler.v1OperationStatus)
	mux.HandleFunc("GET /open/v1/orders", handler.v1Orders)
	mux.HandleFunc("GET /open/v1/orders/{order_id}", handler.v1Order)
	mux.HandleFunc("GET /open/v1/customers/{customer_id}/identities", handler.v1Identities)
	mux.HandleFunc("GET /open/v1/questionnaire-submissions", handler.v1QuestionnaireSubmissions)
	mux.HandleFunc("GET /open/v1/customers/{customer_id}/detail", handler.v1CustomerDetail)
	mux.HandleFunc("GET /open/v1/radar/clicks", handler.v1RadarClicks)
	mux.HandleFunc("GET /open/v1/radar/links", handler.v1RadarLinks)
	mux.HandleFunc("GET /open/v1/chat-records", handler.v1ChatRecords)
	// These V3 management endpoints are the control plane used by PR #164.
	// The obsolete donor-shaped config endpoints are intentionally not mounted.
	mux.HandleFunc("GET /api/admin/open-platform/clients", handler.listClients)
	mux.HandleFunc("POST /api/admin/open-platform/clients", handler.createClient)
	mux.HandleFunc("GET /api/admin/open-platform/clients/{client_id}", handler.getClient)
	mux.HandleFunc("PATCH /api/admin/open-platform/clients/{client_id}", handler.patchClient)
	mux.HandleFunc("GET /api/admin/open-platform/clients/{client_id}/audit", handler.listClientAudit)
	mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/activate", handler.activateClient)
	mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/rotate", handler.rotateClient)
	mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/enable", handler.enableClient)
	mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/disable", handler.disableClient)
	mux.HandleFunc("GET /api/admin/open-platform/routes", handler.routes)
	return noStore(handler.withBoundedRequest(mux))
}

// withBoundedRequest applies the public V1 body and deadline limits before a
// protocol handler parses forms or JSON. The derived context is passed through
// every stable Port; neither Access nor an Owner Port receives an unbounded
// machine request.
func (handler *Handler) withBoundedRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(response, request.Body, maxBodyBytes)
		ctx, cancel := context.WithTimeout(request.Context(), handler.requestTimeout)
		defer cancel()
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

// Mount installs the V1 machine protocol ahead of the main application.
// It claims only explicit catalog and management paths, never an /api prefix.
func Mount(next, machine http.Handler) http.Handler {
	if next == nil || machine == nil {
		return http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	for _, route := range []string{
		"POST /oauth/token", "GET /mcp", "POST /mcp",
		"GET /open/v1/capabilities", "POST /open/v1/customers:resolve",
		"GET /open/v1/customers",
		"GET /open/v1/customers/{customer_id}", "GET /open/v1/customers/{customer_id}/activities",
		"POST /open/v1/ai/review-plans", "GET /open/v1/operations/{operation_id}",
		"GET /open/v1/orders", "GET /open/v1/orders/{order_id}",
		"GET /open/v1/customers/{customer_id}/identities",
		"GET /open/v1/questionnaire-submissions",
		"GET /open/v1/customers/{customer_id}/detail",
		"GET /open/v1/radar/clicks", "GET /open/v1/radar/links",
		"GET /open/v1/chat-records",
		"GET /open/v1/audience/core-products",
		"GET /open/v1/audience/packages/{package_id}/members",
		"GET /open/v1/audience/packages/{package_id}/members/{customer_id}/operations",
		"GET /open/v1/audience/packages/{package_id}/members/{customer_id}/history",
		"POST /open/v1/audience/push-records",
		"GET /api/admin/open-platform/clients", "POST /api/admin/open-platform/clients",
		"GET /api/admin/open-platform/clients/{client_id}", "PATCH /api/admin/open-platform/clients/{client_id}", "GET /api/admin/open-platform/clients/{client_id}/audit",
		"POST /api/admin/open-platform/clients/{client_id}/activate", "POST /api/admin/open-platform/clients/{client_id}/rotate", "POST /api/admin/open-platform/clients/{client_id}/enable", "POST /api/admin/open-platform/clients/{client_id}/disable",
		"GET /api/admin/open-platform/routes",
	} {
		mux.Handle(route, machine)
	}
	// Inventory is historical evidence only. Explicit 404 handlers prevent an
	// old machine path from reaching an unrelated V3 owner through next.
	for _, retired := range Inventory {
		if retired.Path == "/mcp" {
			continue
		}
		mux.Handle(retired.Method+" "+retired.Path, http.NotFoundHandler())
	}
	mux.Handle("/", next)
	return mux
}

// MountWithLegacyProtocols is retained only to avoid a source break for old
// local test callers. V1 never dispatches retired paths or falls back from an
// invalid machine bearer to a legacy authentication protocol.
func MountWithLegacyProtocols(next, machine http.Handler, _ string) http.Handler {
	return Mount(next, machine)
}

func (handler *Handler) token(response http.ResponseWriter, request *http.Request) {
	source, ok := handler.secureSource(response, request)
	if !ok {
		return
	}
	if err := request.ParseForm(); err != nil {
		writeOAuthError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	if request.Form.Get("grant_type") != "client_credentials" {
		writeOAuthError(response, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	clientID, clientSecret, hasBasic := request.BasicAuth()
	formID, formSecret := request.Form.Get("client_id"), request.Form.Get("client_secret")
	if hasBasic && (formID != "" || formSecret != "") && (formID != clientID || formSecret != clientSecret) {
		writeOAuthError(response, http.StatusUnauthorized, "invalid_client")
		return
	}
	if !hasBasic {
		clientID, clientSecret = formID, formSecret
	}
	if err := handler.rateLimiter.AllowClientCredentials(request.Context(), clientID, source); err != nil {
		setMachineRetryAfter(response, err)
		writeOAuthError(response, statusForMachineError(err), oauthErrorFor(err))
		return
	}
	requestedScopes := strings.Fields(request.Form.Get("scope"))
	issued, err := handler.machine.IssueClientCredentialsToken(request.Context(), accessport.ClientCredentialsInput{
		ClientID: clientID, ClientSecret: clientSecret, Audience: request.Form.Get("audience"), RequestedScopes: requestedScopes, SourceIP: source,
	})
	if err != nil {
		writeOAuthError(response, statusForMachineError(err), oauthErrorFor(err))
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, issued)
}

func (handler *Handler) mcpMetadata(response http.ResponseWriter, request *http.Request) {
	principal, err := handler.authenticateMachine(request)
	requestID := requestID(request)
	if err != nil {
		writeV1Error(response, http.StatusUnauthorized, openplatformport.ErrorAuthentication, requestID)
		return
	}
	if err = handler.allowMachineRequest(request, principal); err != nil {
		setMachineRetryAfter(response, err)
		writeV1Error(response, statusForMachineError(err), operationErrorForMachineError(err), requestID)
		return
	}
	writeV1Data(response, http.StatusOK, map[string]any{"transport": "jsonrpc", "methods": []string{"initialize", "tools/list", "tools/call"}, "client_id": principal.ClientID}, requestID)
}

func (handler *Handler) mcp(response http.ResponseWriter, request *http.Request) {
	requestID := requestID(request)
	response.Header().Set("X-Request-ID", requestID)
	if !isJSONContent(request) {
		writeJSONRPCOperationError(response, nil, openplatformport.ErrorValidation)
		return
	}
	body, err := readBody(request)
	if err != nil || !openplatformport.ValidJSONObject(body) {
		writeJSONRPCOperationError(response, nil, openplatformport.ErrorValidation)
		return
	}
	var rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err = json.Unmarshal(body, &rpc); err != nil || rpc.JSONRPC != "2.0" || !validRPCID(rpc.ID) || strings.TrimSpace(rpc.Method) == "" {
		writeJSONRPCError(response, nil, -32600, "invalid request")
		return
	}
	principal, err := handler.authenticateMachine(request)
	if err != nil {
		writeJSONRPCOperationError(response, rpc.ID, openplatformport.ErrorAuthentication)
		return
	}
	if err = handler.allowMachineRequest(request, principal); err != nil {
		setMachineRetryAfter(response, err)
		writeJSONRPCOperationError(response, rpc.ID, operationErrorForMachineError(err))
		return
	}
	switch rpc.Method {
	case "initialize":
		writeJSONRPCResult(response, rpc.ID, map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]string{"name": "aicrm-v3", "version": openplatformport.SchemaVersion}, "capabilities": map[string]any{"tools": map[string]any{}}})
	case "tools/list":
		items, catalogErr := handler.operations.Available(request.Context(), principal)
		if catalogErr != nil {
			writeJSONRPCOperationError(response, rpc.ID, openplatformport.ErrorCodeOf(catalogErr))
			return
		}
		writeJSONRPCResult(response, rpc.ID, map[string]any{"tools": mcpTools(items)})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := decodeMCPParams(rpc.Params, &params); err != nil || strings.TrimSpace(params.Name) == "" {
			writeJSONRPCError(response, rpc.ID, -32602, "invalid params")
			return
		}
		descriptor, known := openplatformport.DescriptorForMCPTool(params.Name)
		if !known {
			writeJSONRPCError(response, rpc.ID, -32602, "unknown tool")
			return
		}
		if len(params.Arguments) == 0 {
			params.Arguments = json.RawMessage(`{}`)
		}
		if descriptor.OperationID == openplatformport.OperationAIReviewPlanCreate && strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
			writeJSONRPCOperationError(response, rpc.ID, openplatformport.ErrorValidation)
			return
		}
		result, invokeErr := handler.operations.Invoke(request.Context(), openplatformport.Invocation{
			Operation: descriptor.OperationID, Principal: principal, RequestID: requestID,
			IdempotencyKey: strings.TrimSpace(request.Header.Get("Idempotency-Key")), Input: params.Arguments,
		})
		if invokeErr != nil {
			writeJSONRPCOperationDetailedError(response, rpc.ID, openplatformport.ErrorCodeOf(invokeErr), openplatformport.ErrorDetailsOf(invokeErr))
			return
		}
		writeJSONRPCResult(response, rpc.ID, map[string]any{"content": []any{}, "structuredContent": result.Data})
	default:
		writeJSONRPCError(response, rpc.ID, -32601, "method not found")
	}
}

func (handler *Handler) v1Capabilities(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationCapabilitiesList, func(*http.Request) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
}

func (handler *Handler) v1ResolveCustomer(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationCustomerResolve, requestJSONInput)
}

func (handler *Handler) v1CustomerContext(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationCustomerContext, pathJSONInput("customer_id"))
}

func (handler *Handler) v1Customers(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationCustomerList, func(request *http.Request) (json.RawMessage, error) {
		return externalRecordsJSONInput(request, "customers", map[string]bool{"limit": true}, map[string]bool{"updated_from": true, "updated_to": true, "cursor": true})
	})
}

func (handler *Handler) v1CustomerActivities(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationCustomerActivities, activityJSONInput)
}

func (handler *Handler) v1AIReviewPlan(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationAIReviewPlanCreate, requestJSONInput)
}

func (handler *Handler) v1OperationStatus(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationGet, pathJSONInput("operation_id"))
}
func (handler *Handler) v1Orders(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationOrderList, ordersJSONInput)
}
func (handler *Handler) v1Order(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationOrderGet, pathJSONInput("order_id"))
}
func (handler *Handler) v1Identities(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationIdentityGet, identitiesJSONInput)
}
func (handler *Handler) v1QuestionnaireSubmissions(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationQuestionnaireSubmissions, questionnaireSubmissionsJSONInput)
}
func (handler *Handler) v1CustomerDetail(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationCustomerDetail, pathJSONInput("customer_id"))
}
func (handler *Handler) v1RadarClicks(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationRadarClicks, radarClicksJSONInput)
}
func (handler *Handler) v1RadarLinks(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationRadarLinks, radarLinksJSONInput)
}
func (handler *Handler) v1ChatRecords(response http.ResponseWriter, request *http.Request) {
	handler.invokeV1(response, request, openplatformport.OperationChatRecords, chatRecordsJSONInput)
}

func (handler *Handler) invokeV1(response http.ResponseWriter, request *http.Request, operation openplatformport.OperationID, normalize func(*http.Request) (json.RawMessage, error)) {
	id := requestID(request)
	principal, err := handler.authenticateMachine(request)
	if err != nil {
		writeV1Error(response, http.StatusUnauthorized, openplatformport.ErrorAuthentication, id)
		return
	}
	if err = handler.allowMachineRequest(request, principal); err != nil {
		setMachineRetryAfter(response, err)
		writeV1Error(response, statusForMachineError(err), operationErrorForMachineError(err), id)
		return
	}
	input, err := normalize(request)
	if err != nil {
		writeV1Error(response, http.StatusBadRequest, openplatformport.ErrorValidation, id)
		return
	}
	if operation == openplatformport.OperationAIReviewPlanCreate && strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
		writeV1Error(response, http.StatusBadRequest, openplatformport.ErrorValidation, id)
		return
	}
	result, err := handler.operations.Invoke(request.Context(), openplatformport.Invocation{
		Operation: operation, Principal: principal, RequestID: id,
		IdempotencyKey: strings.TrimSpace(request.Header.Get("Idempotency-Key")), Input: input,
	})
	if err != nil {
		if details := openplatformport.ErrorDetailsOf(err); details != nil {
			response.Header().Set("X-Request-ID", id)
			writeJSON(response, statusForOperationError(openplatformport.ErrorCodeOf(err)), map[string]any{"data": nil, "error": map[string]any{"code": string(openplatformport.ErrorCodeOf(err)), "details": details}, "request_id": id})
			return
		}
		writeV1Error(response, statusForOperationError(openplatformport.ErrorCodeOf(err)), openplatformport.ErrorCodeOf(err), id)
		return
	}
	status := http.StatusOK
	if operation == openplatformport.OperationAIReviewPlanCreate {
		status = http.StatusCreated
	}
	if operation == openplatformport.OperationCoreProducts {
		// Keep the published top-level list alias while exposing the V1 envelope.
		data, ok := result.Data.(map[string]any)
		if ok {
			response.Header().Set("X-Request-ID", id)
			writeJSON(response, status, map[string]any{"data": data, "items": data["items"], "error": nil, "request_id": id})
			return
		}
	}
	writeV1Data(response, status, result.Data, id)
}

func (handler *Handler) authenticateMachine(request *http.Request) (accessdomain.MachinePrincipal, error) {
	source, err := handler.source(request)
	if err != nil {
		return accessdomain.MachinePrincipal{}, err
	}
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		return accessdomain.MachinePrincipal{}, errors.New("machine bearer is required")
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if bearer == "" {
		return accessdomain.MachinePrincipal{}, errors.New("machine bearer is required")
	}
	return handler.machine.AuthenticateBearer(request.Context(), bearer, "external_integration", source)
}

func (handler *Handler) allowMachineRequest(request *http.Request, principal accessdomain.MachinePrincipal) error {
	source, err := handler.source(request)
	if err != nil {
		return err
	}
	return handler.rateLimiter.AllowMachineRequest(request.Context(), principal, source)
}

func requestJSONInput(request *http.Request) (json.RawMessage, error) {
	if !isJSONContent(request) {
		return nil, errors.New("JSON content type is required")
	}
	body, err := readBody(request)
	if err != nil || len(strings.TrimSpace(string(body))) == 0 {
		return nil, errors.New("JSON body is required")
	}
	if !openplatformport.ValidJSONObject(body) {
		return nil, errors.New("invalid JSON")
	}
	return json.RawMessage(body), nil
}

// pathJSONInput rejects query and body aliases so REST cannot smuggle a second
// customer_id/operation_id that diverges from the normalized MCP DTO.
func pathJSONInput(name string) func(*http.Request) (json.RawMessage, error) {
	return func(request *http.Request) (json.RawMessage, error) {
		if request.URL.RawQuery != "" {
			return nil, errors.New("path operation does not accept query or body")
		}
		// A chunked request has ContentLength -1. Read every GET body so it
		// cannot smuggle a conflicting identifier past the normalized path DTO.
		body, err := readBody(request)
		if err != nil || len(bytes.TrimSpace(body)) != 0 {
			return nil, errors.New("path operation does not accept query or body")
		}
		value := strings.TrimSpace(request.PathValue(name))
		if value == "" {
			return nil, errors.New("path value is required")
		}
		if name == "customer_id" || name == "order_id" {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed < 1 {
				return nil, errors.New("invalid customer_id")
			}
			return json.Marshal(map[string]int64{name: parsed})
		}
		return json.Marshal(map[string]string{name: value})
	}
}

// activityJSONInput canonicalizes the REST path and query into exactly the
// DTO accepted by the MCP activity tool. It permits repeated `types`, but all
// scalar query keys appear once and every GET body is rejected, including a
// chunked one, so the path customer cannot be shadowed by JSON.
func activityJSONInput(request *http.Request) (json.RawMessage, error) {
	body, err := readBody(request)
	if err != nil || len(bytes.TrimSpace(body)) != 0 {
		return nil, errors.New("activity operation does not accept a body")
	}
	customerID, err := strconv.ParseInt(strings.TrimSpace(request.PathValue("customer_id")), 10, 64)
	if err != nil || customerID < 1 {
		return nil, errors.New("invalid customer_id")
	}
	query := request.URL.Query()
	values := map[string]any{"customer_id": customerID}
	for name, entries := range query {
		switch name {
		case "types":
			if len(entries) == 0 {
				return nil, errors.New("invalid activity types")
			}
			seen := make(map[string]struct{}, len(entries))
			for _, value := range entries {
				if strings.TrimSpace(value) != value || value == "" {
					return nil, errors.New("invalid activity types")
				}
				if _, duplicate := seen[value]; duplicate {
					return nil, errors.New("duplicate activity type")
				}
				seen[value] = struct{}{}
			}
			values["types"] = append([]string(nil), entries...)
		case "cursor":
			if len(entries) != 1 || strings.TrimSpace(entries[0]) != entries[0] || entries[0] == "" || len(entries[0]) > 4096 {
				return nil, errors.New("invalid activity cursor")
			}
			values["cursor"] = entries[0]
		case "limit":
			if len(entries) != 1 || entries[0] == "" {
				return nil, errors.New("invalid activity limit")
			}
			limit, parseErr := strconv.ParseInt(entries[0], 10, 32)
			if parseErr != nil || strconv.FormatInt(limit, 10) != entries[0] || limit < 1 || limit > 100 {
				return nil, errors.New("invalid activity limit")
			}
			values["limit"] = int32(limit)
		default:
			return nil, errors.New("unknown activity query")
		}
	}
	return json.Marshal(values)
}

// ordersJSONInput makes REST query parameters the exact object consumed by the
// MCP tool. Unknown/repeated scalar values and GET bodies are rejected.
func ordersJSONInput(request *http.Request) (json.RawMessage, error) {
	body, err := readBody(request)
	if err != nil || len(bytes.TrimSpace(body)) != 0 {
		return nil, errors.New("orders operation does not accept a body")
	}
	values := map[string]any{}
	for name, entries := range request.URL.Query() {
		if len(entries) != 1 || entries[0] == "" || strings.TrimSpace(entries[0]) != entries[0] {
			return nil, errors.New("invalid orders query")
		}
		value := entries[0]
		switch name {
		case "provider", "product_code", "merchant_order_no", "provider_transaction_no", "source_system", "source_record_id", "cursor":
			values[name] = value
		case "customer_id", "created_from", "created_to", "paid_from", "paid_to", "limit":
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || strconv.FormatInt(n, 10) != value {
				return nil, errors.New("invalid orders number")
			}
			values[name] = n
		case "is_paid", "is_refunded":
			if value != "true" && value != "false" {
				return nil, errors.New("invalid order boolean")
			}
			values[name] = value == "true"
		default:
			return nil, errors.New("unknown orders query")
		}
	}
	return json.Marshal(values)
}

func identitiesJSONInput(request *http.Request) (json.RawMessage, error) {
	body, err := readBody(request)
	if err != nil || len(bytes.TrimSpace(body)) != 0 {
		return nil, errors.New("identity operation does not accept a body")
	}
	id, err := strconv.ParseInt(request.PathValue("customer_id"), 10, 64)
	if err != nil || id < 1 {
		return nil, errors.New("invalid customer_id")
	}
	values := map[string]any{"customer_id": id}
	for name, entries := range request.URL.Query() {
		if name != "unionid_scope" || len(entries) == 0 {
			return nil, errors.New("invalid identity query")
		}
		for _, v := range entries {
			if strings.TrimSpace(v) != v || v == "" {
				return nil, errors.New("invalid unionid scope")
			}
		}
		values["unionid_scopes"] = entries
	}
	return json.Marshal(values)
}

func questionnaireSubmissionsJSONInput(request *http.Request) (json.RawMessage, error) {
	body, err := readBody(request)
	if err != nil || len(bytes.TrimSpace(body)) != 0 {
		return nil, errors.New("questionnaire submissions operation does not accept a body")
	}
	values := map[string]any{}
	for name, entries := range request.URL.Query() {
		if len(entries) != 1 || entries[0] == "" || strings.TrimSpace(entries[0]) != entries[0] {
			return nil, errors.New("invalid questionnaire submissions query")
		}
		value := entries[0]
		switch name {
		case "source_system", "source_record_id", "cursor":
			values[name] = value
		case "customer_id", "questionnaire_id", "submitted_from", "submitted_to", "limit":
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || strconv.FormatInt(n, 10) != value {
				return nil, errors.New("invalid questionnaire submissions number")
			}
			values[name] = n
		default:
			return nil, errors.New("unknown questionnaire submissions query")
		}
	}
	return json.Marshal(values)
}

func radarClicksJSONInput(request *http.Request) (json.RawMessage, error) {
	return radarJSONInput(request, map[string]bool{"customer_id": true, "radar_id": true, "session_id": true, "clicked_from": true, "clicked_to": true, "limit": true}, map[string]bool{"radar_code": true, "cursor": true})
}

func radarLinksJSONInput(request *http.Request) (json.RawMessage, error) {
	return radarJSONInput(request, map[string]bool{"radar_id": true, "limit": true}, map[string]bool{"radar_code": true, "cursor": true})
}

func chatRecordsJSONInput(request *http.Request) (json.RawMessage, error) {
	return externalRecordsJSONInput(request, "chat records", map[string]bool{"customer_id": true, "staff_user_id": true, "occurred_from": true, "occurred_to": true, "limit": true}, map[string]bool{"chat_type": true, "staff_wecom_userid": true, "source_system": true, "source_record_id": true, "message_id": true, "cursor": true})
}

func radarJSONInput(request *http.Request, numbers, stringsOnly map[string]bool) (json.RawMessage, error) {
	return externalRecordsJSONInput(request, "radar", numbers, stringsOnly)
}

func externalRecordsJSONInput(request *http.Request, operation string, numbers, stringsOnly map[string]bool) (json.RawMessage, error) {
	body, err := readBody(request)
	if err != nil || len(bytes.TrimSpace(body)) != 0 {
		return nil, errors.New(operation + " operation does not accept a body")
	}
	values := map[string]any{}
	for name, entries := range request.URL.Query() {
		if len(entries) != 1 || entries[0] == "" || strings.TrimSpace(entries[0]) != entries[0] {
			return nil, errors.New("invalid " + operation + " query")
		}
		if numbers[name] {
			n, parseErr := strconv.ParseInt(entries[0], 10, 64)
			if parseErr != nil || strconv.FormatInt(n, 10) != entries[0] {
				return nil, errors.New("invalid " + operation + " number")
			}
			values[name] = n
			continue
		}
		if stringsOnly[name] {
			values[name] = entries[0]
			continue
		}
		return nil, errors.New("unknown " + operation + " query")
	}
	return json.Marshal(values)
}

func decodeMCPParams(raw json.RawMessage, target any) error {
	if !openplatformport.ValidJSONObject(raw) {
		return errors.New("invalid params")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing params")
	}
	return nil
}

func isJSONContent(request *http.Request) bool {
	contentType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])
	return contentType == "application/json"
}

func requestID(request *http.Request) string {
	if value := strings.TrimSpace(request.Header.Get("X-Request-ID")); value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\x00") {
		return value
	}
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err == nil {
		return hex.EncodeToString(bytes)
	}
	return "open-v1-request"
}

func statusForOperationError(code openplatformport.ErrorCode) int {
	switch code {
	case openplatformport.ErrorAuthentication:
		return http.StatusUnauthorized
	case openplatformport.ErrorPermission:
		return http.StatusForbidden
	case openplatformport.ErrorValidation:
		return http.StatusBadRequest
	case openplatformport.ErrorNotFound:
		return http.StatusNotFound
	case openplatformport.ErrorIdentityPending, openplatformport.ErrorIdentityConflict, openplatformport.ErrorConflict:
		return http.StatusConflict
	case openplatformport.ErrorRateLimited:
		return http.StatusTooManyRequests
	case openplatformport.ErrorOutcomeUnknown:
		return http.StatusConflict
	default:
		return http.StatusServiceUnavailable
	}
}

func writeV1Data(response http.ResponseWriter, status int, data any, requestID string) {
	response.Header().Set("X-Request-ID", requestID)
	writeJSON(response, status, map[string]any{"data": data, "error": nil, "request_id": requestID})
}

func writeV1Error(response http.ResponseWriter, status int, code openplatformport.ErrorCode, requestID string) {
	response.Header().Set("X-Request-ID", requestID)
	writeJSON(response, status, map[string]any{"data": nil, "error": map[string]string{"code": string(code)}, "request_id": requestID})
}

func writeJSONRPCOperationError(response http.ResponseWriter, id json.RawMessage, category openplatformport.ErrorCode) {
	writeJSONRPCOperationDetailedError(response, id, category, nil)
}

func writeJSONRPCOperationDetailedError(response http.ResponseWriter, id json.RawMessage, category openplatformport.ErrorCode, details any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	data := map[string]any{"category": string(category)}
	if details != nil {
		data["details"] = details
	}
	writeJSON(response, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": string(category), "data": data}})
}
func (handler *Handler) listClients(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": clients})
}

func (handler *Handler) getClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	client, err := handler.management.Get(request.Context(), actor, request.PathValue("client_id"))
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"client": client})
}

func (handler *Handler) patchClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	input, err := v1MachinePatchInput(request)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	client, err := handler.management.PatchV1(request.Context(), actor, request.PathValue("client_id"), input)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"client": client})
}

func (handler *Handler) listClientAudit(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
			return
		}
		limit = parsed
	}
	entries, err := handler.management.ListAudit(request.Context(), actor, request.PathValue("client_id"), limit)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": entries})
}

func (handler *Handler) createClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	var input accessport.CreateMachineClientInput
	if err := decodeJSON(request, &input); err != nil || input.Purpose != "external_agent" {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	issued, err := handler.management.CreateV1(request.Context(), actor, input)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, issued)
}

func (handler *Handler) activateClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	var input struct {
		ClientSecret    string `json:"client_secret"`
		CopiedConfirmed bool   `json:"copied_confirmed"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	client, err := handler.management.Activate(request.Context(), actor, request.PathValue("client_id"), input.ClientSecret, input.CopiedConfirmed)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"client": client})
}

func (handler *Handler) rotateClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	issued, err := handler.management.Rotate(request.Context(), actor, request.PathValue("client_id"))
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, issued)
}

func (handler *Handler) enableClient(response http.ResponseWriter, request *http.Request) {
	handler.setEnabled(response, request, true)
}
func (handler *Handler) disableClient(response http.ResponseWriter, request *http.Request) {
	handler.setEnabled(response, request, false)
}
func (handler *Handler) setEnabled(response http.ResponseWriter, request *http.Request, enabled bool) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	client, err := handler.management.SetEnabled(request.Context(), actor, request.PathValue("client_id"), enabled)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, client)
}

// The following handlers keep the frozen v2 admin endpoint and payload
// contract. They only adapt it to the stable Access management Port; no old
// runtime component is imported.
func (handler *Handler) legacyListClients(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	query := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	if status != "" && status != "enabled" && status != "disabled" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_status_filter"})
		return
	}
	rows := make([]map[string]any, 0, len(clients))
	configured, enabled := 0, 0
	for _, client := range clients {
		if client.ClientID == accessDirectKeyID || legacyClientType(client) == "" {
			continue
		}
		configured++
		if client.Enabled {
			enabled++
		}
		item := handler.legacyClientItem(client)
		if status != "" && item["status"] != status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(client.ClientID+" "+client.DisplayName+" "+item["type_label"].(string)+" "+item["permission_label"].(string)), query) {
			continue
		}
		rows = append(rows, item)
	}
	sort.Slice(rows, func(left, right int) bool {
		return rows[left]["client_id"].(string) < rows[right]["client_id"].(string)
	})
	payload := map[string]any{"rows": rows, "summary": map[string]any{
		"configured_count": configured, "enabled_count": enabled, "disabled_count": configured - enabled,
		"system_managed_count": 0, "status_label": legacyConfiguredLabel(configured),
	}, "templates": legacyClientTemplates(handler.baseURL(request))}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_clients": payload, "source_status": "auth_platform_read_model", "fallback_used": false})
}

func (handler *Handler) legacyGetClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	clientID := request.PathValue("client_id")
	for _, client := range clients {
		if client.ClientID == clientID && legacyClientType(client) != "" && client.ClientID != accessDirectKeyID {
			writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_read_model", "fallback_used": false})
			return
		}
	}
	writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "api_client_not_found"})
}

func (handler *Handler) legacyCreateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"display_name": {}, "client_id": {}, "client_type": {}, "token_ttl_minutes": {}, "allowed_cidrs": {}, "confirm": {}, "admin_action_token": {}})
	if !ok {
		return
	}
	if !legacyConfirmed(response, payload) {
		return
	}
	clientType, valid := legacyText(payload, "client_type")
	input, err := legacyCreateInput(clientType, payload)
	if !valid || err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_api_client_type"})
		return
	}
	issued, err := handler.management.Create(request.Context(), actor, input)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "client": handler.legacyClientItem(issued.Client), "client_secret": issued.Secret, "source_status": "auth_platform_command", "fallback_used": false, "real_external_call_executed": false})
}

func (handler *Handler) legacyUpdateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"display_name": {}, "token_ttl_minutes": {}, "allowed_cidrs": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	input, err := legacyUpdateInput(payload)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_token_ttl"})
		return
	}
	client, err := handler.management.Update(request.Context(), actor, request.PathValue("client_id"), input)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyActivateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"client_secret": {}, "copied_confirmed": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	secret, secretOK := legacyText(payload, "client_secret")
	copied, copiedOK := legacyBool(payload, "copied_confirmed")
	if !secretOK || !copiedOK {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "secret_copy_confirmation_required"})
		return
	}
	client, err := handler.management.Activate(request.Context(), actor, request.PathValue("client_id"), secret, copied)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyRotateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	issued, err := handler.management.Rotate(request.Context(), actor, request.PathValue("client_id"))
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(issued.Client), "client_secret": issued.Secret, "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyDisableClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"enabled": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	enabled, valid := legacyBool(payload, "enabled")
	if !valid || enabled {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "activation_requires_secret_self_check"})
		return
	}
	client, err := handler.management.SetEnabled(request.Context(), actor, request.PathValue("client_id"), false)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyDirectKeyStatus(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	status := handler.legacyDirectStatus(request, directClient(clients))
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_key_status": status, "source_status": "auth_platform_read_model", "fallback_used": false})
}

func (handler *Handler) legacyGenerateDirectKey(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	if directClient(clients) != nil {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "direct_api_key_already_configured"})
		return
	}
	issued, err := handler.management.Create(request.Context(), actor, accessport.CreateMachineClientInput{ClientID: accessDirectKeyID, DisplayName: "CRM 开放 API Key", Purpose: "direct_api_key", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"external_read"}, TokenTTLSeconds: 1800})
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "api_key": issued.Secret, "api_key_status": handler.legacyDirectStatus(request, &issued.Client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyRotateDirectKey(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	issued, err := handler.management.Rotate(request.Context(), actor, accessDirectKeyID)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_key": issued.Secret, "api_key_status": handler.legacyDirectStatus(request, &issued.Client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyDisableDirectKey(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"enabled": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	enabled, valid := legacyBool(payload, "enabled")
	if !valid || enabled {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "direct_api_key_reactivation_requires_rotation"})
		return
	}
	client, err := handler.management.SetEnabled(request.Context(), actor, accessDirectKeyID, false)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_key_status": handler.legacyDirectStatus(request, &client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) routes(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.adminPrincipal(response, request, false); !ok {
		return
	}
	items := openplatformport.OperationCatalog()
	writeJSON(response, http.StatusOK, map[string]any{"items": items, "count": len(items), "schema_version": openplatformport.SchemaVersion})
}

func (handler *Handler) machinePrincipal(response http.ResponseWriter, request *http.Request, audience, scope, capability string) (accessdomain.MachinePrincipal, bool) {
	source, ok := handler.secureSource(response, request)
	if !ok {
		return accessdomain.MachinePrincipal{}, false
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	if bearer == "" || !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "authentication_required"})
		return accessdomain.MachinePrincipal{}, false
	}
	principal, err := handler.machine.AuthenticateBearer(request.Context(), bearer, audience, source)
	if err != nil {
		writeJSON(response, statusForMachineError(err), map[string]string{"error": "invalid_token"})
		return accessdomain.MachinePrincipal{}, false
	}
	if err = handler.allowMachineRequest(request, principal); err != nil {
		writeJSON(response, statusForMachineError(err), map[string]string{"error": string(operationErrorForMachineError(err))})
		return accessdomain.MachinePrincipal{}, false
	}
	if !principal.HasScope(scope) || !principal.HasCapability(capability) {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return accessdomain.MachinePrincipal{}, false
	}
	return principal, true
}

func (handler *Handler) adminPrincipal(response http.ResponseWriter, request *http.Request, write bool) (accessdomain.Principal, bool) {
	session, csrf := "", ""
	if cookie, err := request.Cookie(handler.sessionCookie); err == nil {
		session = cookie.Value
	}
	if cookie, err := request.Cookie(handler.csrfCookie); err == nil {
		csrf = cookie.Value
	}
	var principal accessdomain.Principal
	var err error
	if write {
		principal, err = handler.admin.AuthorizeCSRF(request.Context(), session, csrf, strings.TrimSpace(request.Header.Get("X-CSRF-Token")))
	} else {
		principal, err = handler.admin.Authenticate(request.Context(), session)
	}
	if err != nil {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "authentication_required"})
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func (handler *Handler) secureSource(response http.ResponseWriter, request *http.Request) (netip.Addr, bool) {
	source, err := handler.source(request)
	if err != nil {
		code := "invalid_source_ip"
		if errors.Is(err, errHTTPSRequired) {
			code = "https_required"
		}
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": code})
		return netip.Addr{}, false
	}
	return source, true
}

var errHTTPSRequired = errors.New("https required")

func (handler *Handler) source(request *http.Request) (netip.Addr, error) {
	remote, err := remoteAddr(request.RemoteAddr)
	if err != nil {
		return netip.Addr{}, err
	}
	if request.TLS != nil {
		return remote, nil
	}
	if !handler.isTrustedProxy(remote) || request.Header.Get("X-Forwarded-Proto") != "https" {
		return netip.Addr{}, errHTTPSRequired
	}
	return handler.forwardedSource(request.Header.Get("X-Forwarded-For"))
}

// forwardedSource walks right to left. Each configured proxy hop is removed
// before selecting the first untrusted address, because a client can prepend
// arbitrary X-Forwarded-For values before the trusted proxy appends its peer.
func (handler *Handler) forwardedSource(value string) (netip.Addr, error) {
	hops := strings.Split(value, ",")
	if len(hops) == 0 || strings.TrimSpace(value) == "" {
		return netip.Addr{}, errors.New("missing forwarded source")
	}
	for index := len(hops) - 1; index >= 0; index-- {
		hop := strings.TrimSpace(hops[index])
		candidate, err := netip.ParseAddr(hop)
		if err != nil {
			return netip.Addr{}, err
		}
		if handler.isTrustedProxy(candidate) {
			continue
		}
		return candidate, nil
	}
	return netip.Addr{}, errors.New("forwarded source is only trusted proxies")
}

func (handler *Handler) isTrustedProxy(address netip.Addr) bool {
	for _, prefix := range handler.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(response, request)
	})
}

const accessDirectKeyID = "direct_external_api_key"

func (handler *Handler) legacyWritePayload(response http.ResponseWriter, request *http.Request, allowed map[string]struct{}) (accessdomain.Principal, map[string]json.RawMessage, bool) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return accessdomain.Principal{}, nil, false
	}
	body, err := readBody(request)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_request"})
		return accessdomain.Principal{}, nil, false
	}
	payload := map[string]json.RawMessage{}
	if json.Unmarshal(body, &payload) != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "payload_must_be_object"})
		return accessdomain.Principal{}, nil, false
	}
	for key := range payload {
		if _, known := allowed[key]; !known {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown_fields:" + key})
			return accessdomain.Principal{}, nil, false
		}
	}
	if raw, exists := payload["admin_action_token"]; exists {
		var token string
		if json.Unmarshal(raw, &token) != nil || strings.TrimSpace(token) == "" {
			writeJSON(response, http.StatusUnauthorized, map[string]any{"ok": false, "error": "invalid_admin_action_token"})
			return accessdomain.Principal{}, nil, false
		}
	}
	return actor, payload, true
}

func legacyConfirmed(response http.ResponseWriter, payload map[string]json.RawMessage) bool {
	confirmed, valid := legacyBool(payload, "confirm")
	if !valid || !confirmed {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "operation_confirmation_required"})
		return false
	}
	return true
}

func legacyText(payload map[string]json.RawMessage, key string) (string, bool) {
	raw, found := payload[key]
	if !found {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func legacyBool(payload map[string]json.RawMessage, key string) (bool, bool) {
	raw, found := payload[key]
	if !found {
		return false, false
	}
	var value bool
	return value, json.Unmarshal(raw, &value) == nil
}

func legacyTTL(payload map[string]json.RawMessage) (int, error) {
	raw, found := payload["token_ttl_minutes"]
	if !found {
		return 0, errors.New("missing ttl")
	}
	var minutes int
	if json.Unmarshal(raw, &minutes) != nil || (minutes != 15 && minutes != 30 && minutes != 60) {
		return 0, errors.New("invalid ttl")
	}
	return minutes * 60, nil
}

func legacyCIDRs(payload map[string]json.RawMessage) ([]string, error) {
	raw, found := payload["allowed_cidrs"]
	if !found || string(raw) == "null" {
		return []string{}, nil
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return values, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid cidrs")
	}
	return strings.FieldsFunc(value, func(character rune) bool { return character == ',' || character == '\n' }), nil
}

func legacyCreateInput(clientType string, payload map[string]json.RawMessage) (accessport.CreateMachineClientInput, error) {
	clientID, clientIDOK := legacyText(payload, "client_id")
	displayName, displayNameOK := legacyText(payload, "display_name")
	ttl, err := legacyTTL(payload)
	if !clientIDOK || !displayNameOK || err != nil {
		return accessport.CreateMachineClientInput{}, errors.New("invalid api client")
	}
	cidrs, err := legacyCIDRs(payload)
	if err != nil {
		return accessport.CreateMachineClientInput{}, err
	}
	input := accessport.CreateMachineClientInput{ClientID: clientID, DisplayName: displayName, Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, AllowedCIDRs: cidrs, TokenTTLSeconds: ttl}
	switch clientType {
	case "external_api":
		input.Purpose, input.Capabilities = "external_agent", []string{"external_read", "external_write"}
	case "mcp":
		input.Purpose, input.Capabilities = "mcp", []string{"mcp_read", "mcp_execute"}
	default:
		return accessport.CreateMachineClientInput{}, errors.New("invalid type")
	}
	return input, nil
}

func legacyUpdateInput(payload map[string]json.RawMessage) (accessport.UpdateMachineClientInput, error) {
	displayName, valid := legacyText(payload, "display_name")
	ttl, err := legacyTTL(payload)
	if !valid || err != nil {
		return accessport.UpdateMachineClientInput{}, errors.New("invalid update")
	}
	cidrs, err := legacyCIDRs(payload)
	if err != nil {
		return accessport.UpdateMachineClientInput{}, err
	}
	return accessport.UpdateMachineClientInput{DisplayName: displayName, TokenTTLSeconds: ttl, AllowedCIDRs: cidrs}, nil
}

func legacyClientType(client accessport.MachineClientSummary) string {
	switch client.Purpose {
	case "external_agent":
		return "external_api"
	case "mcp":
		return "mcp"
	default:
		return ""
	}
}

func (handler *Handler) legacyClientItem(client accessport.MachineClientSummary) map[string]any {
	clientType := legacyClientType(client)
	label, resource := "External API", "/api/external"
	if clientType == "mcp" {
		label, resource = "MCP", "/mcp"
	}
	return map[string]any{"client_id": client.ClientID, "display_name": client.DisplayName, "client_type": clientType, "type_label": label,
		"purpose": client.Purpose, "audience": "external_integration", "scopes": client.Scopes, "capabilities": client.Capabilities,
		"permission_label": label, "allowed_cidrs": client.AllowedCIDRs, "token_ttl_minutes": client.TokenTTLSeconds / 60,
		"enabled": client.Enabled, "status": map[bool]string{true: "enabled", false: "disabled"}[client.Enabled], "status_label": map[bool]string{true: "已启用", false: "已停用"}[client.Enabled],
		"auth_version": client.AuthVersion, "credential_hint": client.CredentialHint, "credential_hint_available": client.CredentialHint != "", "last_rotated_at": "", "created_at": client.CreatedAt, "updated_at": "",
		"system_managed": false, "mutable": true, "base_url": handler.publicOrigin, "token_url": handler.publicOrigin + "/oauth/token", "resource_url": handler.publicOrigin + resource, "grant_type": "client_credentials"}
}

func legacyClientTemplates(baseURL string) []map[string]any {
	return []map[string]any{
		{"key": "external_api", "label": "External API", "purpose": "external_agent", "audience": "external_integration", "scopes": []string{"read", "write"}, "capabilities": []string{"external_read", "external_write"}, "base_url": baseURL, "token_url": baseURL + "/oauth/token", "resource_url": baseURL + "/api/external", "grant_type": "client_credentials"},
		{"key": "mcp", "label": "MCP", "purpose": "mcp", "audience": "external_integration", "scopes": []string{"read", "write"}, "capabilities": []string{"mcp_read", "mcp_execute"}, "base_url": baseURL, "token_url": baseURL + "/oauth/token", "resource_url": baseURL + "/mcp", "grant_type": "client_credentials"},
	}
}

func legacyConfiguredLabel(count int) string {
	if count == 0 {
		return "未配置"
	}
	return "已配置 " + strconv.Itoa(count) + " 个"
}

func directClient(clients []accessport.MachineClientSummary) *accessport.MachineClientSummary {
	for index := range clients {
		if clients[index].ClientID == accessDirectKeyID {
			return &clients[index]
		}
	}
	return nil
}

func (handler *Handler) legacyDirectStatus(request *http.Request, client *accessport.MachineClientSummary) map[string]any {
	configured := client != nil
	enabled := configured && client.Enabled
	status, label := "unconfigured", "未配置"
	if configured && enabled {
		status, label = "enabled", "已启用"
	} else if configured {
		status, label = "disabled", "已停用"
	}
	hint, authVersion := "aics_••••••••••••••••••", int64(0)
	if client != nil {
		hint, authVersion = client.CredentialHint, client.AuthVersion
	}
	baseURL := handler.baseURL(request)
	return map[string]any{"configured": configured, "enabled": enabled, "status": status, "status_label": label, "auth_version": authVersion, "credential_hint": hint, "credential_hint_available": client != nil && client.CredentialHint != "", "last_rotated_at": "", "created_at": "", "base_url": baseURL, "resource_url": baseURL + "/api/external", "authorization_header": "Authorization: Bearer <CRM_API_KEY>", "permission_label": "CRM 开放 API 只读"}
}

func (handler *Handler) baseURL(request *http.Request) string {
	if handler.publicOrigin != "" {
		return handler.publicOrigin
	}
	scheme := "https"
	if request.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + request.Host
}

func writeLegacyAdminError(response http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "api_client_operation_failed"
	switch {
	case errors.Is(err, accessdomain.ErrNotFound):
		status, code = http.StatusNotFound, "api_client_not_found"
	case errors.Is(err, accessdomain.ErrMachineClientActive):
		status, code = http.StatusConflict, "active_client_update_requires_disable"
	case errors.Is(err, accessdomain.ErrMachineActivation):
		status, code = http.StatusBadRequest, "client_secret_self_check_failed"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = http.StatusForbidden, "manage_api_clients_required"
	}
	writeJSON(response, status, map[string]any{"ok": false, "error": code})
}

func remoteAddr(value string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	return netip.ParseAddr(host)
}

func readBody(request *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, request.Body, maxBodyBytes))
}

// v1MachinePatchInput deliberately parses presence before handing a stable
// value object to Access. This keeps omitted fields distinct from JSON null:
// only explicit null clears owner_scope or expires_at; an empty grant list is
// still rejected by Access rather than becoming an accidental broad grant.
func v1MachinePatchInput(request *http.Request) (accessport.PatchMachineClientInput, error) {
	if !isJSONContent(request) {
		return accessport.PatchMachineClientInput{}, errors.New("patch content type")
	}
	body, err := readBody(request)
	if err != nil || !openplatformport.ValidJSONObject(body) {
		return accessport.PatchMachineClientInput{}, errors.New("invalid patch JSON")
	}
	payload := map[string]json.RawMessage{}
	if err = json.Unmarshal(body, &payload); err != nil || len(payload) == 0 {
		return accessport.PatchMachineClientInput{}, errors.New("invalid patch JSON")
	}
	allowed := map[string]struct{}{
		"display_name": {}, "audiences": {}, "scopes": {}, "capabilities": {}, "allowed_cidrs": {}, "token_ttl_seconds": {}, "owner_scope": {}, "expires_at": {},
	}
	for key := range payload {
		if _, ok := allowed[key]; !ok {
			return accessport.PatchMachineClientInput{}, errors.New("unknown patch field")
		}
	}
	var input accessport.PatchMachineClientInput
	if raw, ok := payload["display_name"]; ok {
		var value string
		if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
			return input, errors.New("invalid display_name")
		}
		input.DisplayName = &value
	}
	if raw, ok := payload["audiences"]; ok {
		var value []string
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return input, errors.New("invalid audiences")
		}
		input.Audiences = &value
	}
	if raw, ok := payload["scopes"]; ok {
		var value []string
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return input, errors.New("invalid scopes")
		}
		input.Scopes = &value
	}
	if raw, ok := payload["capabilities"]; ok {
		var value []string
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return input, errors.New("invalid capabilities")
		}
		input.Capabilities = &value
	}
	if raw, ok := payload["allowed_cidrs"]; ok {
		var value []string
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return input, errors.New("invalid allowed_cidrs")
		}
		input.AllowedCIDRs = &value
	}
	if raw, ok := payload["token_ttl_seconds"]; ok {
		var value int
		if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
			return input, errors.New("invalid token_ttl_seconds")
		}
		input.TokenTTLSeconds = &value
	}
	if raw, ok := payload["owner_scope"]; ok {
		input.OwnerScopeSet = true
		if string(raw) != "null" {
			value, normalizeErr := accessdomain.NormalizeOwnerScope(raw)
			if normalizeErr != nil {
				return input, errors.New("invalid owner_scope")
			}
			input.OwnerScope = value
		}
	}
	if raw, ok := payload["expires_at"]; ok {
		input.ExpiresAtSet = true
		if string(raw) != "null" {
			var value time.Time
			if json.Unmarshal(raw, &value) != nil {
				return input, errors.New("invalid expires_at")
			}
			input.ExpiresAt = &value
		}
	}
	return input, nil
}

func decodeJSON(request *http.Request, target any) error {
	body, err := readBody(request)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func writeOAuthError(response http.ResponseWriter, status int, code string) {
	response.Header().Set("WWW-Authenticate", `Basic realm="aicrm-token"`)
	writeJSON(response, status, map[string]string{"error": code})
}

func writeJSONRPCResult(response http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(response, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func writeJSONRPCError(response http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeJSON(response, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}})
}

func validRPCID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	var stringID string
	if json.Unmarshal(id, &stringID) == nil {
		return true
	}
	var numberID json.Number
	decoder := json.NewDecoder(strings.NewReader(string(id)))
	decoder.UseNumber()
	return decoder.Decode(&numberID) == nil && numberID.String() != ""
}

func mcpTools(descriptors []openplatformport.Descriptor) []map[string]any {
	tools := make([]map[string]any, 0, len(descriptors))
	for _, descriptor := range descriptors {
		tools = append(tools, map[string]any{
			"name":        descriptor.MCPTool,
			"description": string(descriptor.OperationID),
			"inputSchema": mcpInputSchema(descriptor.OperationID),
		})
	}
	return tools
}

func mcpInputSchema(operation openplatformport.OperationID) map[string]any {
	stringValue := map[string]any{"type": "string"}
	switch operation {
	case openplatformport.OperationCapabilitiesList, openplatformport.OperationCoreProducts:
		return map[string]any{"type": "object", "additionalProperties": false}
	case openplatformport.OperationCoreMembers, openplatformport.OperationCoreMemberHistory, openplatformport.OperationCoreMemberOperations:
		required := []string{"package_id"}
		props := map[string]any{"package_id": map[string]any{"type": "integer", "minimum": 1}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "cursor": stringValue}
		if operation != openplatformport.OperationCoreMembers {
			required = append(required, "customer_id")
			props["customer_id"] = map[string]any{"type": "integer", "minimum": 1}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
	case openplatformport.OperationCorePushRecord:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"push_id", "customer_id", "package_id", "materials", "occurred_at", "status", "status_version"}, "properties": map[string]any{
			"push_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "customer_id": map[string]any{"type": "integer", "minimum": 1}, "package_id": map[string]any{"type": "integer", "minimum": 1}, "status_version": map[string]any{"type": "integer", "minimum": 1}, "occurred_at": map[string]any{"type": "string", "format": "date-time"}, "status": map[string]any{"type": "string", "enum": []string{"reported", "success", "failed", "unknown"}}, "materials": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "id"}, "properties": map[string]any{"kind": stringValue, "id": map[string]any{"type": "integer", "minimum": 1}}}},
		}}
	case openplatformport.OperationCustomerResolve:
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"references": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "scope", "value"}, "properties": map[string]any{"kind": stringValue, "scope": stringValue, "value": stringValue}}},
		}, "required": []string{"references"}}
	case openplatformport.OperationCustomerContext:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"customer_id"}, "properties": map[string]any{"customer_id": map[string]any{"type": "integer", "minimum": 1}}}
	case openplatformport.OperationCustomerActivities:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"customer_id"}, "properties": map[string]any{
			"customer_id": map[string]any{"type": "integer", "minimum": 1}, "types": map[string]any{"type": "array", "items": stringValue}, "cursor": stringValue, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		}}
	case openplatformport.OperationAIReviewPlanCreate:
		contentBlock := map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"kind"},
			"properties": map[string]any{
				"kind": map[string]any{"type": "string", "enum": []string{"text", "image", "mini_program", "link", "attachment"}},
				"text": stringValue, "material_kind": stringValue,
				"material_id": map[string]any{"type": "integer", "minimum": 1}, "material_digest": stringValue,
			},
		}
		recipient := map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"customer_id", "staff_id", "content"},
			"properties": map[string]any{
				"customer_id": map[string]any{"type": "integer", "minimum": 1}, "staff_id": map[string]any{"type": "integer", "minimum": 1},
				"content": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": contentBlock},
			},
		}
		return map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"name", "source_kind", "source_digest", "recipients"},
			"properties": map[string]any{
				"name": stringValue, "source_kind": stringValue, "source_digest": stringValue,
				"recipients": map[string]any{"type": "array", "minItems": 1, "maxItems": 5000, "items": recipient},
			},
		}

	case openplatformport.OperationGet:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operation_id"}, "properties": map[string]any{"operation_id": stringValue}}
	case openplatformport.OperationOrderList:
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"provider": stringValue, "product_code": stringValue, "merchant_order_no": stringValue, "provider_transaction_no": stringValue, "source_system": stringValue, "source_record_id": stringValue, "customer_id": map[string]any{"type": "integer", "minimum": 1}, "created_from": map[string]any{"type": "integer", "minimum": 0}, "created_to": map[string]any{"type": "integer", "minimum": 0}, "paid_from": map[string]any{"type": "integer", "minimum": 0}, "paid_to": map[string]any{"type": "integer", "minimum": 0}, "is_paid": map[string]any{"type": "boolean"}, "is_refunded": map[string]any{"type": "boolean"}, "cursor": stringValue, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}}
	case openplatformport.OperationOrderGet:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"order_id"}, "properties": map[string]any{"order_id": map[string]any{"type": "integer", "minimum": 1}}}
	case openplatformport.OperationIdentityGet:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"customer_id"}, "properties": map[string]any{"customer_id": map[string]any{"type": "integer", "minimum": 1}, "unionid_scopes": map[string]any{"type": "array", "items": stringValue}}}
	case openplatformport.OperationQuestionnaireSubmissions:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"customer_id"}, "properties": map[string]any{"customer_id": map[string]any{"type": "integer", "minimum": 1}, "questionnaire_id": map[string]any{"type": "integer", "minimum": 1}, "source_system": stringValue, "source_record_id": stringValue, "submitted_from": map[string]any{"type": "integer", "minimum": 0}, "submitted_to": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "cursor": stringValue}}
	case openplatformport.OperationCustomerDetail:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"customer_id"}, "properties": map[string]any{"customer_id": map[string]any{"type": "integer", "minimum": 1}}}
	case openplatformport.OperationRadarClicks:
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"customer_id": map[string]any{"type": "integer", "minimum": 1}, "radar_id": map[string]any{"type": "integer", "minimum": 1}, "radar_code": stringValue, "session_id": map[string]any{"type": "integer", "minimum": 1}, "clicked_from": map[string]any{"type": "integer", "minimum": 0}, "clicked_to": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "cursor": stringValue}}
	case openplatformport.OperationRadarLinks:
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"radar_id": map[string]any{"type": "integer", "minimum": 1}, "radar_code": stringValue, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "cursor": stringValue}}
	case openplatformport.OperationChatRecords:
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"customer_id"}, "properties": map[string]any{"customer_id": map[string]any{"type": "integer", "minimum": 1}, "chat_type": map[string]any{"type": "string", "enum": []string{"private", "group"}}, "staff_user_id": map[string]any{"type": "integer", "minimum": 1}, "staff_wecom_userid": stringValue, "occurred_from": map[string]any{"type": "integer", "minimum": 0}, "occurred_to": map[string]any{"type": "integer", "minimum": 0}, "source_system": stringValue, "source_record_id": stringValue, "message_id": stringValue, "limit": map[string]any{"type": "integer", "enum": []int{20}, "default": 20}, "cursor": stringValue}}
	default:
		return map[string]any{"type": "object", "additionalProperties": false}
	}
}

func routePlaceholders(path string) []string {
	result := make([]string, 0, 2)
	for remaining := path; ; {
		start := strings.Index(remaining, "{")
		if start < 0 {
			return result
		}
		end := strings.Index(remaining[start:], "}")
		if end < 2 {
			return result
		}
		result = append(result, remaining[start+1:start+end])
		remaining = remaining[start+end+1:]
	}
}

func audienceFor(Route) string { return "external_integration" }

func statusForMachineError(err error) int {
	switch {
	case errors.Is(err, accessdomain.ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, accessdomain.ErrMachineAudience), errors.Is(err, accessdomain.ErrMachineScope), errors.Is(err, accessdomain.ErrMachineSourceIP):
		return http.StatusForbidden
	case errors.Is(err, accessdomain.ErrMachineClientDisabled), errors.Is(err, accessdomain.ErrMachineClientExpired), errors.Is(err, accessdomain.ErrMachineReissueRequired), errors.Is(err, accessdomain.ErrMachineCredential):
		return http.StatusUnauthorized
	default:
		return http.StatusBadRequest
	}
}

func setMachineRetryAfter(response http.ResponseWriter, err error) {
	var limited accessdomain.MachineRateLimitError
	if !errors.As(err, &limited) {
		return
	}
	seconds := int64((limited.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	response.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
}

func oauthErrorFor(err error) string {
	if errors.Is(err, accessdomain.ErrRateLimited) {
		return "rate_limited"
	}
	if errors.Is(err, accessdomain.ErrMachineAudience) || errors.Is(err, accessdomain.ErrMachineScope) || errors.Is(err, accessdomain.ErrMachineSourceIP) {
		return "invalid_scope"
	}
	if errors.Is(err, accessdomain.ErrMachineCredential) || errors.Is(err, accessdomain.ErrMachineClientDisabled) || errors.Is(err, accessdomain.ErrMachineClientExpired) || errors.Is(err, accessdomain.ErrMachineReissueRequired) {
		return "invalid_client"
	}
	return "invalid_request"
}

func operationErrorForMachineError(err error) openplatformport.ErrorCode {
	if errors.Is(err, accessdomain.ErrRateLimited) {
		return openplatformport.ErrorRateLimited
	}
	return openplatformport.ErrorDependencyUnavailable
}

func writeAdminError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, accessdomain.ErrAuthentication) {
		status = http.StatusUnauthorized
	}
	if errors.Is(err, accessdomain.ErrPermissionDenied) {
		status = http.StatusForbidden
	}
	if errors.Is(err, accessdomain.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(response, status, map[string]string{"error": "open_platform_request_failed"})
}
