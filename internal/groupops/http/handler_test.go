package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

// Embedding the contracts keeps these boundary tests focused: the requests
// below must be rejected before an application/runtime method is reachable.
type applicationStub struct{ groupopshttp.Application }
type runtimeStub struct {
	groupopshttp.RuntimeApplication
}

type executionRuntimeStub struct {
	runtimeStub
	page groupopsport.ExecutionPage
}

type planListProjectionStub struct {
	applicationStub
	page groupopsport.PlanPage
}

func (stub planListProjectionStub) List(context.Context, int32, int32) (groupopsport.PlanPage, error) {
	return stub.page, nil
}

func (stub executionRuntimeStub) ListExecutions(context.Context, int64, int32, int32) (groupopsport.ExecutionPage, error) {
	return stub.page, nil
}

type historyStub struct {
	groupopshttp.HistoryApplication
}

func (historyStub) ListHistoricalPlans(context.Context, int32, int32) (groupopsport.HistoricalPlanPage, error) {
	return groupopsport.HistoricalPlanPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalPlan{}, Limit: 50}, nil
}
func (historyStub) ListHistoricalDirectory(context.Context, int32, int32) (groupopsport.HistoricalDirectoryPage, error) {
	return groupopsport.HistoricalDirectoryPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalDirectory{}, Limit: 50}, nil
}
func (historyStub) ListHistoricalGroups(context.Context, int64, int32, int32) (groupopsport.HistoricalGroupPage, error) {
	return groupopsport.HistoricalGroupPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalGroup{}, Limit: 50, PlanID: 1}, nil
}
func (historyStub) ListHistoricalNodes(context.Context, int64, int32, int32) (groupopsport.HistoricalNodePage, error) {
	return groupopsport.HistoricalNodePage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalNode{}, Limit: 50, PlanID: 1}, nil
}

type unavailableHistoryStub struct {
	groupopshttp.HistoryApplication
}

func (unavailableHistoryStub) ListHistoricalPlans(context.Context, int32, int32) (groupopsport.HistoricalPlanPage, error) {
	return groupopsport.HistoricalPlanPage{}, groupopsapp.ErrUnavailable
}
func (unavailableHistoryStub) ListHistoricalDirectory(context.Context, int32, int32) (groupopsport.HistoricalDirectoryPage, error) {
	return groupopsport.HistoricalDirectoryPage{}, groupopsapp.ErrUnavailable
}
func (unavailableHistoryStub) ListHistoricalGroups(context.Context, int64, int32, int32) (groupopsport.HistoricalGroupPage, error) {
	return groupopsport.HistoricalGroupPage{}, groupopsapp.ErrUnavailable
}
func (unavailableHistoryStub) ListHistoricalNodes(context.Context, int64, int32, int32) (groupopsport.HistoricalNodePage, error) {
	return groupopsport.HistoricalNodePage{}, groupopsapp.ErrUnavailable
}

type contentDeliveryStub struct {
	mediaport.ContentDeliveryService
	previewCalls int
}

func (s *contentDeliveryStub) Preview(context.Context, mediaport.ContentPackageCommand) (mediaport.ContentPackage, error) {
	s.previewCalls++
	return mediaport.ContentPackage{ID: 1, Name: "内容包", ContentText: "正文", Version: 1, Refs: []mediaport.ContentRef{}}, nil
}

type protocolStub struct {
	called bool
	err    error
}

func (s *protocolStub) AuthenticateGroupOpsWebhook(context.Context, *http.Request, string, []byte) (string, error) {
	s.called = true
	return "group-ops-webhook-test-key", s.err
}

type webhookRuntimeStub struct {
	runtimeStub
	calls   int
	inbound groupopsport.WebhookInboundCommand
	err     error
}

func (s *webhookRuntimeStub) AcceptWebhook(_ context.Context, _ string, _ string, inbound groupopsport.WebhookInboundCommand) (groupopsport.RunSummary, error) {
	s.calls++
	s.inbound = inbound
	return groupopsport.RunSummary{}, s.err
}

type securityStub struct {
	principal accessdomain.Principal
	csrfErr   error
}

func (s securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}

func (s securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	if s.csrfErr != nil {
		return accessdomain.Principal{}, s.csrfErr
	}
	return s.principal, nil
}

func adminSecurity(csrfErr error) securityStub {
	return securityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, csrfErr: csrfErr}
}

func newBoundaryHandler(t *testing.T, security securityStub, protocols groupopshttp.ProtocolAuthenticator) http.Handler {
	t.Helper()
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtimeStub{}, security, protocols)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newHistoryHandler(t *testing.T, security securityStub) http.Handler {
	t.Helper()
	handler, err := groupopshttp.NewHandlerWithRuntimeAndHistory(applicationStub{}, runtimeStub{}, historyStub{}, security, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newUnavailableHistoryHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := groupopshttp.NewHandlerWithRuntimeAndHistory(applicationStub{}, runtimeStub{}, unavailableHistoryStub{}, adminSecurity(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestGroupOpsPlanListReturnsResponsibleOwnerProjection(t *testing.T) {
	page := groupopsport.PlanPage{Items: []groupopsport.PlanListItem{{Plan: groupopsport.Plan{
		ID: 41, Name: "负责人投影", Status: groupopsport.PlanDraft, Revision: 3,
		Owner: groupopsport.PlanOwner{
			StaffID: 7, SenderUserID: "wecom-owner", DisplayName: "一号运营",
			NameSource: "wecom_profile", ProfileReadState: "ready",
		},
	}}}, Total: 1, Limit: groupopsapp.DefaultLimit, Safety: groupopsport.LocalSafety()}
	handler, err := groupopshttp.NewHandlerWithRuntime(planListProjectionStub{page: page}, runtimeStub{}, adminSecurity(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, groupopshttp.PlansPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			Owner           groupopsport.PlanOwner `json:"owner"`
			BoundGroupCount *int64                 `json:"bound_group_count"`
		} `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload.Items) != 1 || payload.Items[0].Owner.StaffID != 7 || payload.Items[0].Owner.SenderUserID != "wecom-owner" || payload.Items[0].Owner.DisplayName != "一号运营" || payload.Items[0].Owner.NameSource != "wecom_profile" || payload.Items[0].Owner.ProfileReadState != "ready" || payload.Items[0].BoundGroupCount == nil || *payload.Items[0].BoundGroupCount != 0 {
		t.Fatalf("payload=%s decoded=%+v err=%v", response.Body.String(), payload, err)
	}
}

func TestGroupOpsHistoryUsesExactReadOnlyDonorURLs(t *testing.T) {
	handler := newHistoryHandler(t, adminSecurity(nil))
	for _, target := range []string{
		groupopshttp.HistoryPath + "/plans?limit=20&offset=0",
		groupopshttp.HistoryPath + "/directory?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/groups?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/nodes?limit=20&offset=0",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"source":"v1_history"`) || !strings.Contains(response.Body.String(), `"read_only":true`) {
			t.Fatalf("target=%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
}

func TestGroupOpsHistoryRejectsInvalidSubresources(t *testing.T) {
	handler := newHistoryHandler(t, adminSecurity(nil))
	for _, target := range []string{
		groupopshttp.HistoryPath + "/plans/not-a-plan/groups",
		groupopshttp.HistoryPath + "/plans/1/unknown",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("target=%s status=%d cache=%q body=%s", target, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
}

func TestGroupOpsHistoryUnavailablePageBoundaryReturns503(t *testing.T) {
	handler := newUnavailableHistoryHandler(t)
	for _, target := range []string{
		groupopshttp.HistoryPath + "/plans?limit=20&offset=0",
		groupopshttp.HistoryPath + "/directory?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/groups?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/nodes?limit=20&offset=0",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"group_ops_unavailable"`) {
			t.Fatalf("target=%s status=%d cache=%q body=%s", target, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
}

func TestGroupOpsHistoryIsReadOnlyAndRequiresSession(t *testing.T) {
	handler := newHistoryHandler(t, adminSecurity(nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, groupopshttp.HistoryPath+"/plans", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("write status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler = newHistoryHandler(t, securityStub{})
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, groupopshttp.HistoryPath+"/plans", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("anonymous status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsWebhookWithoutProtocolAdapterFailsClosed(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, groupopshttp.BroadcastPath, strings.NewReader(`{"plan_id":1}`))
	request.URL.Path = "/api/automation/group-ops/webhooks/plan-hook"
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(nil), nil).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"protocol_auth_unavailable"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsExecutionPageAdaptsVerifiedDeliveryForFrozenDetailDTO(t *testing.T) {
	runtime := executionRuntimeStub{page: groupopsport.ExecutionPage{Items: []groupopsport.Execution{{
		ID: 71, PlanID: 9, State: groupopsport.ExecutionProviderAccepted,
		ProviderAccepted: true, DeliveryProven: true, ProviderReceiptPresent: true,
	}}}}
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtime, adminSecurity(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, groupopshttp.PlansPath+"/9/executions?limit=100&offset=0", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			State        string `json:"state"`
			RuntimeState string `json:"runtime_state"`
			Delivery     bool   `json:"delivery_proven"`
			Receipt      bool   `json:"provider_receipt_present"`
		} `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload.Items) != 1 || payload.Items[0].State != "delivery_proven" || payload.Items[0].RuntimeState != "provider_accepted" || !payload.Items[0].Delivery || !payload.Items[0].Receipt {
		t.Fatalf("payload=%s decoded=%+v err=%v", response.Body.String(), payload, err)
	}
}

func TestGroupOpsOperationMembersRejectsAudienceScope(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, groupopshttp.OperationMembersPath+"/sync", strings.NewReader(`{"scope":"audience","page_size":20}`))
	request.Header.Set("Idempotency-Key", "group-ops-operation-members-01")
	request.Header.Set("X-CSRF-Token", "csrf")
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(nil), nil).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsMutationRequiresCSRFBeforeApplication(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, groupopshttp.PlansPath, strings.NewReader(`{"name":"plan"}`))
	request.Header.Set("Idempotency-Key", "group-ops-create-plan-01")
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(errors.New("csrf required")), nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"permission_denied"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsWebhookRejectsBodiesBeyondHMACBound(t *testing.T) {
	protocols := &protocolStub{}
	body := `{"value":"` + strings.Repeat("a", 64<<10) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(body))
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(nil), protocols).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) || protocols.called {
		t.Fatalf("status=%d protocol_called=%v body=%s", response.Code, protocols.called, response.Body.String())
	}
}

func TestGroupOpsWebhookDecodesStrictDynamicMessagesBeforeRuntime(t *testing.T) {
	protocols := &protocolStub{}
	runtime := &webhookRuntimeStub{}
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtime, adminSecurity(nil), protocols)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(`{"webhook_reference":"plan-hook","target_chat_references":["bound-chat"],"messages":[{"type":"text","text":"今日话术"},{"type":"image","image_id":7},{"type":"file","attachment_id":8},{"type":"miniprogram","miniprogram_id":9}]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !protocols.called || runtime.calls != 1 || len(runtime.inbound.Messages) != 4 || runtime.inbound.Messages[2].AttachmentID != 8 || runtime.inbound.Messages[3].MiniProgramID != 9 {
		t.Fatalf("status=%d protocol=%v runtime=%+v body=%s", response.Code, protocols.called, runtime, response.Body.String())
	}
	protocols.called = false
	request = httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(`{"webhook_reference":"plan-hook","target_chat_references":["bound-chat"],"messages":[{"type":"image","image_id":7},{"type":"text","text":"too late"}]}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || protocols.called || runtime.calls != 1 || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("status=%d protocol=%v runtime=%+v body=%s", response.Code, protocols.called, runtime, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(`{"webhook_reference":"plan-hook","target_chat_references":["bound-chat"],"messages":[{"type":"miniprogram","miniprogram_id":"9"}]}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || protocols.called || runtime.calls != 1 || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("string miniprogram id status=%d protocol=%v runtime=%+v body=%s", response.Code, protocols.called, runtime, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(`{"webhook_reference":"plan-hook","target_chat_references":["bound-chat"],"messages":[{"type":"text","text":"has\u0000nul"}]}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || protocols.called || runtime.calls != 1 || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("NUL text status=%d protocol=%v runtime=%+v body=%s", response.Code, protocols.called, runtime, response.Body.String())
	}
}

func TestGroupOpsWebhookReportsMiniProgramCoverFailuresPrecisely(t *testing.T) {
	body := `{"webhook_reference":"plan-hook","target_chat_references":["bound-chat"],"messages":[{"type":"text","text":"今日话术"}]}`
	for _, test := range []struct {
		name string
		err  error
		code int
		want string
	}{
		{name: "unsupported automatic source", err: groupopsapp.ErrMiniProgramCoverUnsupported, code: http.StatusBadRequest, want: "miniprogram_cover_unsupported"},
		{name: "temporary cover failure", err: groupopsapp.ErrMiniProgramCoverResolverUnavailable, code: http.StatusServiceUnavailable, want: "miniprogram_cover_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			protocols := &protocolStub{}
			runtime := &webhookRuntimeStub{err: test.err}
			handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtime, adminSecurity(nil), protocols)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(body)))
			if response.Code != test.code || !strings.Contains(response.Body.String(), test.want) || runtime.calls != 1 {
				t.Fatalf("status=%d runtime=%+v body=%s", response.Code, runtime, response.Body.String())
			}
		})
	}
}

func TestGroupOpsWebhookReturnsReplayPayloadConflict(t *testing.T) {
	protocols := &protocolStub{err: groupopshttp.ErrProtocolReplayConflict}
	request := httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(`{"webhook_reference":"plan-hook","target_chat_references":["bound-chat"],"messages":[{"type":"text","text":"今日话术"}]}`))
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(nil), protocols).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"idempotency_conflict"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsWebhookRejectsSignedBodyForDifferentURLBeforeRuntime(t *testing.T) {
	protocols := &protocolStub{}
	runtime := &webhookRuntimeStub{}
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtime, adminSecurity(nil), protocols)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/second-hook", strings.NewReader(`{"webhook_reference":"first-hook","target_chat_references":["bound-chat"],"messages":[{"type":"text","text":"今日话术"}]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || protocols.called || runtime.calls != 0 || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("status=%d protocol=%v runtime=%+v body=%s", response.Code, protocols.called, runtime, response.Body.String())
	}
}

func TestGroupOpsContentPackagePreviewUsesMediaPortAdapter(t *testing.T) {
	delivery := &contentDeliveryStub{}
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtimeStub{}, adminSecurity(nil), nil, delivery)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, groupopshttp.ContentPackagesPath+"/preview", strings.NewReader(`{"name":"内容包","content_text":"正文","refs":[]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || delivery.previewCalls != 1 || !strings.Contains(response.Body.String(), `"content_text":"正文"`) {
		t.Fatalf("status=%d preview_calls=%d body=%s", response.Code, delivery.previewCalls, response.Body.String())
	}
}

type directoryQueryRuntimeStub struct {
	runtimeStub
	owner  int64
	query  string
	limit  int32
	offset int32
	calls  int
}

func (stub *directoryQueryRuntimeStub) ListGroups(_ context.Context, owner int64, query string, limit, offset int32) (groupopsport.GroupDirectoryPage, error) {
	stub.calls++
	stub.owner, stub.query, stub.limit, stub.offset = owner, query, limit, offset
	return groupopsport.GroupDirectoryPage{Items: []groupopsport.GroupDirectoryItem{{ChatReference: "query-group", OwnerStaffID: owner, DisplayName: "查询群"}}, Total: 1, Limit: limit, Offset: offset}, nil
}

func TestGroupOpsDirectoryQueryPassesScopedPaginationToRuntime(t *testing.T) {
	runtime := &directoryQueryRuntimeStub{}
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtime, adminSecurity(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, groupopshttp.GroupPickerPath+"?owner_userid=7&q=%E6%9F%A5%E8%AF%A2&limit=2&offset=1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if runtime.calls != 1 || runtime.owner != 7 || runtime.query != "查询" || runtime.limit != 2 || runtime.offset != 1 {
		t.Fatalf("runtime query=%+v", runtime)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, groupopshttp.DirectoryPath+"?q="+strings.Repeat("x", 161), nil))
	if response.Code != http.StatusBadRequest || runtime.calls != 1 {
		t.Fatalf("oversized q status=%d calls=%d body=%s", response.Code, runtime.calls, response.Body.String())
	}
}
