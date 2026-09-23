package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

type v1ChatRecordsStub struct {
	queries      []archiveport.V1ChatRecordQuery
	pages        []archiveport.V1ChatRecordPage
	err          error
	staffByWeCom map[string]int64
	staffResolve error
	staffLookups int
}

func (*v1ChatRecordsStub) CustomerMessages(context.Context, archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	return archiveport.CustomerPage{}, nil
}

func (*v1ChatRecordsStub) CustomerStaff(context.Context, customerdomain.CustomerID) ([]archiveport.StaffOption, error) {
	return []archiveport.StaffOption{}, nil
}

func (stub *v1ChatRecordsStub) V1ChatRecords(_ context.Context, query archiveport.V1ChatRecordQuery) (archiveport.V1ChatRecordPage, error) {
	stub.queries = append(stub.queries, query)
	if stub.err != nil {
		return archiveport.V1ChatRecordPage{}, stub.err
	}
	if len(stub.pages) == 0 {
		return archiveport.V1ChatRecordPage{}, nil
	}
	page := stub.pages[0]
	stub.pages = stub.pages[1:]
	return page, nil
}

func (stub *v1ChatRecordsStub) V1ChatStaffID(_ context.Context, wecomUserID string) (int64, error) {
	stub.staffLookups++
	if stub.staffResolve != nil {
		return 0, stub.staffResolve
	}
	id, found := stub.staffByWeCom[wecomUserID]
	if !found {
		return 0, archiveport.ErrStaffNotFound
	}
	return id, nil
}

type customerMessageOnlyStub struct{}

func (customerMessageOnlyStub) CustomerMessages(context.Context, archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	return archiveport.CustomerPage{}, nil
}

func (customerMessageOnlyStub) CustomerStaff(context.Context, customerdomain.CustomerID) ([]archiveport.StaffOption, error) {
	return []archiveport.StaffOption{}, nil
}

func TestV1ChatRecordsScopeBeforePagingAndRetainsMessageFilter(t *testing.T) {
	end := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	firstAt, secondAt := end.Add(-time.Minute), end.Add(-2*time.Minute)
	stub := &v1ChatRecordsStub{pages: []archiveport.V1ChatRecordPage{
		{Items: []archiveport.V1ChatRecord{{MessageID: "provider-message-1", SourceSystem: "message_archive", SourceRecordID: "31", ChatType: "private", MessageType: "text", Content: "safe", RenderType: "supported", Direction: "staff_to_customer", OccurredAt: firstAt, MediaArchiveStatus: "not_applicable", MediaAvailability: "not_applicable", Staff: []archiveport.StaffOption{{ID: 8, DisplayName: "客服"}}}}, HasMore: true},
		{Items: []archiveport.V1ChatRecord{{MessageID: "provider-message-2", SourceSystem: "message_archive", SourceRecordID: "30", ChatType: "private", MessageType: "text", Content: "safe", RenderType: "supported", Direction: "staff_to_customer", OccurredAt: secondAt, MediaArchiveStatus: "not_applicable", MediaAvailability: "not_applicable", Staff: []archiveport.StaffOption{{ID: 8, DisplayName: "客服"}}}}},
	}}
	executor := &openPlatformExecutor{archive: stub, activityNow: func() time.Time { return end }, v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	principal := accessdomain.MachinePrincipal{ClientID: "chat-client", ClientRecord: 5, Audience: "external_integration", AuthVersion: 2}
	first, err := executor.v1ChatRecords(context.Background(), principal, []byte(`{"customer_id":7,"staff_user_id":8,"message_id":"provider-message-1","limit":20}`))
	if err != nil {
		t.Fatal(err)
	}
	data := first.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) != 1 || items[0]["message_id"] != "provider-message-1" || items[0]["customer_id"] != "7" || items[0]["source_record_id"] != "31" || items[0]["media_archive_status"] != "not_applicable" || items[0]["media_availability"] != "not_applicable" {
		t.Fatalf("first=%#v", first.Data)
	}
	raw, marshalErr := json.Marshal(first.Data)
	if marshalErr != nil || strings.Contains(string(raw), "external_userid") || strings.Contains(string(raw), "unionid") || strings.Contains(string(raw), "sdkfile") {
		t.Fatalf("sensitive chat projection=%s err=%v", raw, marshalErr)
	}
	next, _ := data["next_cursor"].(string)
	if next == "" || len(stub.queries) != 1 || stub.queries[0].CustomerID != 7 || stub.queries[0].ChatType != "private" || stub.queries[0].StaffUserID != 8 || stub.queries[0].MessageID != "provider-message-1" || stub.queries[0].Limit != 20 || !stub.queries[0].EndAt.Equal(end) {
		t.Fatalf("first query=%+v", stub.queries)
	}
	second, err := executor.v1ChatRecords(context.Background(), principal, []byte(`{"customer_id":7,"staff_user_id":8,"message_id":"provider-message-1","limit":20,"cursor":`+mustJSON(t, next)+`}`))
	if err != nil || len(second.Data.(map[string]any)["items"].([]map[string]any)) != 1 || len(stub.queries) != 2 || !stub.queries[1].EndAt.Equal(end) || !stub.queries[1].BeforeOccurredAt.Equal(firstAt) || stub.queries[1].BeforeMessageID != 31 {
		t.Fatalf("second=%#v err=%v queries=%+v", second.Data, err, stub.queries)
	}
	if _, err = executor.v1ChatRecords(context.Background(), principal, []byte(`{"customer_id":7,"staff_user_id":8,"message_id":"provider-message-1","occurred_to":1789300801,"cursor":`+mustJSON(t, next)+`}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation {
		t.Fatalf("changed snapshot error=%v", err)
	}
}

func TestV1ChatRecordsRejectsOutOfScopeAndUncomposedArchive(t *testing.T) {
	stub := &v1ChatRecordsStub{}
	executor := &openPlatformExecutor{archive: stub, v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	principal := accessdomain.MachinePrincipal{OwnerScope: accessdomain.OwnerScope{"customer_id": {"8"}}}
	if _, err := executor.v1ChatRecords(context.Background(), principal, []byte(`{"customer_id":7,"chat_type":"group"}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorNotFound || len(stub.queries) != 0 {
		t.Fatalf("scope error=%v queries=%+v", err, stub.queries)
	}
	executor.archive = customerMessageOnlyStub{}
	if _, err := executor.v1ChatRecords(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7,"chat_type":"group"}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable {
		t.Fatalf("uncomposed archive error=%v", err)
	}
}

func TestV1ChatRecordsRequiresPrivateStaffAndResolvesWeComStaffSelector(t *testing.T) {
	end := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	stub := &v1ChatRecordsStub{
		staffByWeCom: map[string]int64{"follow-user-8": 8},
		pages: []archiveport.V1ChatRecordPage{
			{Items: []archiveport.V1ChatRecord{{
				MessageID: "private-8", SourceSystem: "message_archive", SourceRecordID: "8", ChatType: "private", OccurredAt: end.Add(-time.Minute),
				Staff: []archiveport.StaffOption{{ID: 8, DisplayName: "客服"}},
			}}, HasMore: true},
		},
	}
	executor := &openPlatformExecutor{archive: stub, activityNow: func() time.Time { return end }, v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	if _, err := executor.v1ChatRecords(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation || len(stub.queries) != 0 || stub.staffLookups != 0 {
		t.Fatalf("missing private staff error=%v queries=%+v lookups=%d", err, stub.queries, stub.staffLookups)
	}
	result, err := executor.v1ChatRecords(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7,"staff_wecom_userid":"follow-user-8"}`))
	if err != nil || len(stub.queries) != 1 || stub.queries[0].ChatType != "private" || stub.queries[0].StaffUserID != 8 || stub.staffLookups != 1 || result.Data.(map[string]any)["customer_id"] != "7" {
		t.Fatalf("resolved result=%+v err=%v query=%+v lookups=%d", result, err, stub.queries, stub.staffLookups)
	}
	if _, err = executor.v1ChatRecords(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7,"staff_user_id":9,"staff_wecom_userid":"follow-user-8"}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation {
		t.Fatalf("mismatched selectors error=%v", err)
	}
	next, _ := result.Data.(map[string]any)["next_cursor"].(string)
	if next == "" {
		t.Fatalf("missing private cursor result=%+v", result)
	}
	stub.staffByWeCom["follow-user-8"] = 9
	if _, err = executor.v1ChatRecords(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7,"staff_wecom_userid":"follow-user-8","cursor":`+mustJSON(t, next)+`}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation || len(stub.queries) != 1 {
		t.Fatalf("changed staff mapping cursor error=%v queries=%+v", err, stub.queries)
	}
	stub.pages = []archiveport.V1ChatRecordPage{{}}
	if _, err = executor.v1ChatRecords(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7,"chat_type":"group"}`)); err != nil || len(stub.queries) != 2 || stub.queries[1].ChatType != "group" || stub.queries[1].StaffUserID != 0 {
		t.Fatalf("group selector result=%v query=%+v", err, stub.queries)
	}
}
