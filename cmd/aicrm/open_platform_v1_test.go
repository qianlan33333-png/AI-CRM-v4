package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func v1ExecutorForTest(t *testing.T, identity *openPlatformIdentityStub, profiles *openPlatformProfileStub) *openPlatformExecutor {
	t.Helper()
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, profiles, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestV1CatalogOnlyPublishesComposedGrantedOperations(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{
		string(openplatformport.CapabilityPlatformCapabilitiesRead),
		string(openplatformport.CapabilityCustomerResolve),
		string(openplatformport.CapabilityCustomerRead),
		string(openplatformport.CapabilityCustomerActivityRead),
	}}
	items, err := executor.Available(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]openplatformport.OperationID, 0, len(items))
	for _, item := range items {
		got = append(got, item.OperationID)
	}
	want := map[openplatformport.OperationID]bool{
		openplatformport.OperationCapabilitiesList: true,
		openplatformport.OperationCustomerResolve:  true,
		openplatformport.OperationCustomerContext:  true,
	}
	if len(got) != len(want) {
		t.Fatalf("available = %#v", got)
	}
	for _, operation := range got {
		if !want[operation] {
			t.Fatalf("unexpected operation %q", operation)
		}
	}
}

func TestV1ResolveUsesScopedOneIDAndNeverProvisions(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42, IdentityID: 7}}
	executor := v1ExecutorForTest(t, identity, &openPlatformProfileStub{})
	input := json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42"}]}`)
	result, err := executor.Invoke(context.Background(), openplatformport.Invocation{
		Operation: openplatformport.OperationCustomerResolve,
		Principal: accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}},
		Input:     input,
	})
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	if customerID, ok := data["customer_id"].(customerdomain.CustomerID); !ok || customerID != 42 || data["identity_id"] != int64(7) || data["status"] != "found" {
		t.Fatalf("result = %#v", data)
	}
	if len(identity.calls) != 1 || identity.seen.Scope != "wechat-open-platform:shared" || identity.seen.Assurance != "declared" || identity.seen.Source != "open_platform.v1" {
		t.Fatalf("identity reference = %+v", identity.seen)
	}
}

func TestV1ResolveRejectsCallerAssuranceAndConflictingRoots(t *testing.T) {
	identity := &openPlatformIdentityStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:shared|union-42": {Status: identityport.ResolveFound, CustomerID: 42},
		"unionid|wechat-open-platform:shared|union-43": {Status: identityport.ResolveFound, CustomerID: 43},
	}}
	executor := v1ExecutorForTest(t, identity, &openPlatformProfileStub{})
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}}
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerResolve, Principal: principal, Input: json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42","assurance":"verified"}]}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation || len(identity.calls) != 0 {
		t.Fatalf("declared assurance err=%v calls=%d", err, len(identity.calls))
	}
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerResolve, Principal: principal, Input: json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42"},{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-43"}]}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorIdentityConflict || len(identity.calls) != 2 {
		t.Fatalf("conflict err=%v calls=%d", err, len(identity.calls))
	}
}

func TestV1CustomerContextChecksOwnerScopeBeforeProfileRead(t *testing.T) {
	profiles := &openPlatformProfileStub{}
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, profiles)
	principal := accessdomain.MachinePrincipal{
		Scopes:       []string{"read"},
		Capabilities: []string{string(openplatformport.CapabilityCustomerRead)},
		OwnerScope:   accessdomain.OwnerScope{"customer_id": {"43"}},
	}
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerContext, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorNotFound || profiles.calls != 0 {
		t.Fatalf("scope err=%v profile_calls=%d", err, profiles.calls)
	}
}

func TestV1CustomerContextRequiresReadTokenAndCurrentGrant(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{
		Operation: openplatformport.OperationCustomerContext,
		Principal: accessdomain.MachinePrincipal{Scopes: []string{"write"}, Capabilities: []string{string(openplatformport.CapabilityCustomerRead)}},
		Input:     json.RawMessage(`{"customer_id":42}`),
	})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorPermission {
		t.Fatalf("error = %v", err)
	}
}

func TestV1CustomerActivitiesMergeOwnerPagesAndBindCursor(t *testing.T) {
	at := func(hour int) time.Time { return time.Date(2026, 9, 6, hour, 0, 0, 0, time.UTC) }
	archive := &openPlatformArchiveStub{activityFn: func(query archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
		all := []archiveport.MessageItem{{ID: 11, ChatType: "private", MessageType: "text", Direction: "customer_to_staff", RenderType: "supported", OccurredAt: at(12)}, {ID: 10, ChatType: "private", MessageType: "image", Direction: "staff_to_customer", RenderType: "unsupported", OccurredAt: at(10)}}
		return archiveport.CustomerPage{Items: afterMessagePosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	orders := &openPlatformOrderStub{activityFn: func(query orderport.CustomerActivityQuery) (orderport.CustomerActivityPage, error) {
		all := []orderport.CustomerActivity{{OrderID: 21, Relationship: "payer", Provider: orderdomain.ProviderWeChatPay, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 88, Currency: "CNY"}, OccurredAt: at(12)}, {OrderID: 20, Relationship: "beneficiary", Provider: orderdomain.ProviderWeChatPay, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 99, Currency: "CNY"}, OccurredAt: at(9)}}
		return orderport.CustomerActivityPage{Items: afterOrderActivityPosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	survey := &openPlatformSurveyStub{historyFn: func(query surveyport.CustomerHistoryQuery) (surveyport.CustomerHistoryWindow, error) {
		all := []surveyport.Submission{{ID: 41, QuestionnaireID: 7, QuestionnaireTitle: "Assessment", SubmittedAt: at(10)}}
		return surveyport.CustomerHistoryWindow{Items: afterSurveyPosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	radar := &openPlatformRadarLinksStub{activityFn: func(query radarport.CustomerActivityQuery) (radarport.CustomerActivityPage, error) {
		all := []radarport.CustomerActivity{{EventID: 31, RadarID: 6, Stage: radarport.EventContentOpened, OccurredAt: at(11)}}
		return radarport.CustomerActivityPage{Items: afterRadarPosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	identity := &openPlatformIdentityStub{}
	executor, err := newOpenPlatformExecutor(identity, orders, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.activityNow = func() time.Time { return at(13) }
	if err = executor.BindV1CustomerActivities(survey, radar, bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{ClientID: "reader-a", Audience: "external_integration", Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerActivityRead)}}
	first, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(`{"customer_id":42,"limit":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	firstData := first.Data.(map[string]any)
	if got := v1ActivityIDs(t, firstData); !reflect.DeepEqual(got, []string{"message:11", "order:21"}) {
		t.Fatalf("first page = %v", got)
	}
	cursor, _ := firstData["next_cursor"].(string)
	if cursor == "" || archive.activityQuery.Limit != 3 || orders.activityQuery.Limit != 3 || radar.activityQuery.Limit != 3 || survey.historyQuery.Limit != 3 {
		t.Fatalf("lookahead/cursor archive=%+v order=%+v radar=%+v survey=%+v cursor=%q", archive.activityQuery, orders.activityQuery, radar.activityQuery, survey.historyQuery, cursor)
	}
	second, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(fmt.Sprintf(`{"customer_id":42,"limit":2,"cursor":%q}`, cursor))})
	if err != nil {
		t.Fatal(err)
	}
	secondData := second.Data.(map[string]any)
	if got := v1ActivityIDs(t, secondData); !reflect.DeepEqual(got, []string{"radar:31", "message:10"}) {
		t.Fatalf("second page = %v", got)
	}
	thirdCursor, _ := secondData["next_cursor"].(string)
	third, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(fmt.Sprintf(`{"customer_id":42,"limit":2,"cursor":%q}`, thirdCursor))})
	if err != nil {
		t.Fatal(err)
	}
	thirdData := third.Data.(map[string]any)
	if got := v1ActivityIDs(t, thirdData); !reflect.DeepEqual(got, []string{"survey:41", "order:20"}) || thirdData["next_cursor"] != nil {
		t.Fatalf("third page = %#v ids=%v", thirdData, got)
	}
	// The cursor contains the effective grant digest, so a newly narrowed or
	// expanded client grant cannot resume a former feed position.
	principal.Capabilities = append(principal.Capabilities, "other-capability")
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(fmt.Sprintf(`{"customer_id":42,"cursor":%q}`, cursor))})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation {
		t.Fatalf("grant-swapped cursor err=%v", err)
	}
}

func TestV1CustomerActivitiesFailClosedAndUseOneHundredItemLookahead(t *testing.T) {
	at := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	archive := &openPlatformArchiveStub{err: archiveport.ErrNotReady}
	orders := &openPlatformOrderStub{}
	survey := &openPlatformSurveyStub{}
	radar := &openPlatformRadarLinksStub{}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, orders, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.activityNow = func() time.Time { return at }
	if err = executor.BindV1CustomerActivities(survey, radar, bytes.Repeat([]byte{3}, 32)); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerActivityRead)}}
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable || orders.activityCalls != 0 || radar.calls != 0 || survey.calls != 0 {
		t.Fatalf("owner failure err=%v subsequent calls order=%d radar=%d survey=%d", err, orders.activityCalls, radar.calls, survey.calls)
	}

	items := make([]orderport.CustomerActivity, 101)
	for index := range items {
		items[index] = orderport.CustomerActivity{OrderID: int64(101 - index), Relationship: "payer", Provider: orderdomain.ProviderWeChatPay, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 1, Currency: "CNY"}, OccurredAt: at.Add(-time.Duration(index) * time.Minute)}
	}
	archive = &openPlatformArchiveStub{}
	orders = &openPlatformOrderStub{activityPage: orderport.CustomerActivityPage{Items: items}}
	executor, err = newOpenPlatformExecutor(&openPlatformIdentityStub{}, orders, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.activityNow = func() time.Time { return at }
	if err = executor.BindV1CustomerActivities(&openPlatformSurveyStub{}, &openPlatformRadarLinksStub{}, bytes.Repeat([]byte{4}, 32)); err != nil {
		t.Fatal(err)
	}
	result, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(`{"customer_id":42,"types":["order"],"limit":100}`)})
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	if len(v1ActivityIDs(t, data)) != 100 || data["next_cursor"] == nil || orders.activityQuery.Limit != 101 {
		t.Fatalf("100-item lookahead data=%#v order query=%+v", data, orders.activityQuery)
	}
}

func v1ActivityIDs(t *testing.T, data map[string]any) []string {
	t.Helper()
	items, ok := data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("items type = %T", data["items"])
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item["activity_id"].(string))
	}
	return result
}

func afterMessagePosition(values []archiveport.MessageItem, at time.Time, id int64) []archiveport.MessageItem {
	result := []archiveport.MessageItem{}
	for _, value := range values {
		if at.IsZero() || value.OccurredAt.Before(at) || (value.OccurredAt.Equal(at) && value.ID < id) {
			result = append(result, value)
		}
	}
	return result
}
func afterOrderActivityPosition(values []orderport.CustomerActivity, at time.Time, id int64) []orderport.CustomerActivity {
	result := []orderport.CustomerActivity{}
	for _, value := range values {
		if at.IsZero() || value.OccurredAt.Before(at) || (value.OccurredAt.Equal(at) && value.OrderID < id) {
			result = append(result, value)
		}
	}
	return result
}
func afterSurveyPosition(values []surveyport.Submission, at time.Time, id surveyport.ID) []surveyport.Submission {
	result := []surveyport.Submission{}
	for _, value := range values {
		if at.IsZero() || value.SubmittedAt.Before(at) || (value.SubmittedAt.Equal(at) && value.ID < id) {
			result = append(result, value)
		}
	}
	return result
}
func afterRadarPosition(values []radarport.CustomerActivity, at time.Time, id int64) []radarport.CustomerActivity {
	result := []radarport.CustomerActivity{}
	for _, value := range values {
		if at.IsZero() || value.OccurredAt.Before(at) || (value.OccurredAt.Equal(at) && value.EventID < id) {
			result = append(result, value)
		}
	}
	return result
}

func TestV1OperationsAuditSuccessAndDeniedWithoutRequestPayload(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	audit := &openPlatformMachineAuditStub{}
	if err := executor.BindV1OperationAudit(audit, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{ClientID: "machine-a", ClientRecord: 19, Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityPlatformCapabilitiesRead)}}
	if _, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCapabilitiesList, Principal: principal, RequestID: "request-contains-user-value"}); err != nil {
		t.Fatal(err)
	}
	if audit.calls != 1 || audit.audit.MachineClientID != 19 || audit.audit.Action != "open_platform_operation" || audit.audit.Outcome != "succeeded" || !strings.Contains(string(audit.audit.Details), `"operation":"platform.capabilities.list"`) || !strings.Contains(string(audit.audit.Details), `"request_id_digest"`) || strings.Contains(string(audit.audit.Details), "request-") {
		t.Fatalf("success audit=%+v", audit.audit)
	}
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerContext, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorPermission || audit.calls != 2 || audit.audit.Outcome != "permission" {
		t.Fatalf("denied err=%v audit=%+v", err, audit.audit)
	}
}

func TestV1CustomerActivitiesDefaultLimitIsFiftyForRESTAndMCPDTO(t *testing.T) {
	at := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	archive := &openPlatformArchiveStub{}
	orders := &openPlatformOrderStub{}
	survey := &openPlatformSurveyStub{}
	radar := &openPlatformRadarLinksStub{}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, orders, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.activityNow = func() time.Time { return at }
	if err = executor.BindV1CustomerActivities(survey, radar, bytes.Repeat([]byte{5}, 32)); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerActivityRead)}}
	for index, raw := range []json.RawMessage{json.RawMessage(`{"customer_id":42}`), json.RawMessage(`{"customer_id":42,"types":["order"]}`)} {
		if _, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: raw}); err != nil {
			t.Fatal(err)
		}
		if index == 0 { // the full shared DTO requests every Owner Port.
			if archive.activityQuery.Limit != 51 || orders.activityQuery.Limit != 51 || radar.activityQuery.Limit != 51 || survey.historyQuery.Limit != 51 {
				t.Fatalf("default query limits archive=%d order=%d radar=%d survey=%d", archive.activityQuery.Limit, orders.activityQuery.Limit, radar.activityQuery.Limit, survey.historyQuery.Limit)
			}
		}
	}
}

func TestV1OperationAuditMustBeComposedForMachinePrincipal(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCapabilitiesList, Principal: accessdomain.MachinePrincipal{ClientRecord: 1, Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityPlatformCapabilitiesRead)}}})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable {
		t.Fatalf("missing auditor err=%v", err)
	}
}

func TestV1CustomerScopeOwnerFailureIsUnavailableBeforeContextOrActivities(t *testing.T) {
	owners := &openPlatformOwnerStub{err: errors.New("owner store unavailable")}
	profiles := &openPlatformProfileStub{}
	archive := &openPlatformArchiveStub{}
	orders := &openPlatformOrderStub{}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, orders, profiles, archive, &openPlatformTimelineStub{}, owners, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerRead), string(openplatformport.CapabilityCustomerActivityRead)}, OwnerScope: accessdomain.OwnerScope{"owner_userid": {"owner-a"}}}
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerContext, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable || profiles.calls != 0 {
		t.Fatalf("context error=%v profile_calls=%d", err, profiles.calls)
	}
	if err = executor.BindV1CustomerActivities(&openPlatformSurveyStub{}, &openPlatformRadarLinksStub{}, bytes.Repeat([]byte{6}, 32)); err != nil {
		t.Fatal(err)
	}
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable || archive.calls != 0 || orders.activityCalls != 0 {
		t.Fatalf("activities error=%v archive_calls=%d order_calls=%d", err, archive.calls, orders.activityCalls)
	}
}

type v1AIMachineStub struct {
	command aiassistantport.MachineCreatePlanCommand
	create  aiassistantport.MachineCreatePlanResult
	status  aiassistantport.MachineOperationStatus
	err     error
}

func (stub *v1AIMachineStub) CreateMachinePlanWithin(_ context.Context, command aiassistantport.MachineCreatePlanCommand) (aiassistantport.MachineCreatePlanResult, error) {
	stub.command = command
	return stub.create, stub.err
}
func (stub *v1AIMachineStub) GetMachinePlan(context.Context, aiassistantport.MachineActor, aiassistantport.PlanID) (aiassistantport.MachinePlan, error) {
	return aiassistantport.MachinePlan{}, stub.err
}
func (stub *v1AIMachineStub) GetMachineOperationStatus(_ context.Context, actor aiassistantport.MachineActor, id aiassistantport.PlanID) (aiassistantport.MachineOperationStatus, error) {
	if actor.Reference != "machine:client-a" || id != 12 {
		return aiassistantport.MachineOperationStatus{}, errors.New("wrong machine operation lookup")
	}
	return stub.status, stub.err
}

func TestV1AICreateAndStatusUseMachineActorAndAtomicAudit(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	audit := &openPlatformMachineAuditStub{}
	ai := &v1AIMachineStub{create: aiassistantport.MachineCreatePlanResult{Plan: aiassistantport.MachinePlan{ID: 12, ReviewState: aiassistantport.ReviewPending}}, status: aiassistantport.MachineOperationStatus{PlanID: 12, ReviewState: aiassistantport.ReviewApproved, OperationState: aiassistantport.MachineOperationApproved}}
	if err := executor.BindV1OperationAudit(audit, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
	if err := executor.BindV1AI(ai, ai, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
	writePrincipal := accessdomain.MachinePrincipal{ClientID: "client-a", ClientRecord: 7, Scopes: []string{"write"}, Capabilities: []string{string(openplatformport.CapabilityAIReviewPlanCreate)}}
	sourceDigest := effectport.Hash("v1-ai-test")
	input, _ := json.Marshal(map[string]any{"name": "review", "source_kind": "open_platform", "source_digest": sourceDigest, "recipients": []any{map[string]any{"customer_id": 42, "staff_id": 8, "content": []any{map[string]any{"kind": "text", "text": "hello"}}}}})
	created, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationAIReviewPlanCreate, Principal: writePrincipal, RequestID: "request-ai-12", IdempotencyKey: "idempotency-key-12", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if ai.command.Actor.Reference != "machine:client-a" || ai.command.Actor.StaffID != 0 || audit.calls != 1 || audit.audit.Outcome != "succeeded" || !strings.Contains(string(audit.audit.Details), "request_id_digest") {
		t.Fatalf("command=%+v audit=%+v", ai.command, audit.audit)
	}
	if data := created.Data.(map[string]any); data["operation_id"] != "ai_review_plan:12" || data["review_state"] != aiassistantport.ReviewPending {
		t.Fatalf("create=%#v", data)
	}
	readPrincipal := writePrincipal
	readPrincipal.Scopes, readPrincipal.Capabilities = []string{"read"}, []string{string(openplatformport.CapabilityOperationRead)}
	status, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationGet, Principal: readPrincipal, Input: json.RawMessage(`{"operation_id":"ai_review_plan:12"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if data := status.Data.(map[string]any); data["review_state"] != aiassistantport.ReviewApproved || data["operation_state"] != aiassistantport.MachineOperationApproved {
		t.Fatalf("status=%#v", data)
	}
}
