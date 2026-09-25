package port

import (
	"reflect"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

func TestOperationCatalogFreezesTheTwentyV1Operations(t *testing.T) {
	catalog := OperationCatalog()
	if len(catalog) != 20 {
		t.Fatalf("catalog count = %d, want 20", len(catalog))
	}
	got := make([]OperationID, 0, len(catalog))
	for _, item := range catalog {
		if item.OperationID == "" || item.RESTMethod == "" || item.RESTPath == "" || item.MCPTool == "" || item.Capability == "" || item.RequiredScope == "" || item.SchemaVersion != SchemaVersion {
			t.Fatalf("invalid descriptor: %+v", item)
		}
		got = append(got, item.OperationID)
	}
	want := []OperationID{OperationCapabilitiesList, OperationCustomerResolve, OperationCustomerContext, OperationCustomerList, OperationCustomerActivities, OperationAIReviewPlanCreate, OperationGet, OperationOrderList, OperationOrderGet, OperationIdentityGet, OperationQuestionnaireSubmissions, OperationCustomerDetail, OperationRadarClicks, OperationRadarLinks, OperationChatRecords, OperationCoreProducts, OperationCoreMembers, OperationCoreMemberOperations, OperationCoreMemberHistory, OperationCorePushRecord}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operation IDs = %#v, want %#v", got, want)
	}
	chat, found := DescriptorForOperation(OperationChatRecords)
	if !found {
		t.Fatal("chat records descriptor absent")
	}
	wantChat := Descriptor{OperationID: OperationChatRecords, RESTMethod: "GET", RESTPath: "/open/v1/chat-records", MCPTool: "list_chat_records", Capability: CapabilityChatRead, RequiredScope: "read", SchemaVersion: SchemaVersion}
	if !reflect.DeepEqual(chat, wantChat) {
		t.Fatalf("chat records descriptor = %#v, want %#v", chat, wantChat)
	}
}

func TestDescriptorRequiresTokenScopeAndCurrentCapability(t *testing.T) {
	write, found := DescriptorForOperation(OperationAIReviewPlanCreate)
	if !found {
		t.Fatal("write operation absent")
	}
	readToken := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(CapabilityAIReviewPlanCreate)}}
	if write.Allows(readToken) {
		t.Fatal("write capability must not escape a narrowed read token")
	}
	writeToken := accessdomain.MachinePrincipal{Scopes: []string{"write"}, Capabilities: []string{string(CapabilityAIReviewPlanCreate)}}
	if !write.Allows(writeToken) {
		t.Fatal("matching grant and write token should allow operation")
	}
	read, found := DescriptorForOperation(OperationCustomerActivities)
	if !found || !reflect.DeepEqual(read.ActivityTypes, []string{"message", "survey", "radar", "order"}) {
		t.Fatalf("activity descriptor = %+v", read)
	}
}

func TestCustomerActivityCursorContractBindsTheAuthorizedStream(t *testing.T) {
	contract := CustomerActivityCursorContract()
	if !contract.BindsCustomer || !contract.BindsTypes || !contract.BindsGrant || !contract.PerTypeCursor || !contract.NoAdvanceOnFailure {
		t.Fatalf("cursor contract = %+v", contract)
	}
}

func TestDescriptorForMCPToolOnlyReturnsCatalogTools(t *testing.T) {
	if item, found := DescriptorForMCPTool("get_operation_status"); !found || item.OperationID != OperationGet {
		t.Fatalf("operation status tool = %+v, found=%v", item, found)
	}
	if _, found := DescriptorForMCPTool("get_recent_messages"); found {
		t.Fatal("retired donor MCP tool must not be published")
	}
}

func TestValidJSONObjectRejectsDuplicateMembersAtEveryLevel(t *testing.T) {
	for _, input := range [][]byte{
		[]byte(`{"references":[],"references":[]}`),
		[]byte(`{"reference":{"kind":"unionid","kind":"phone"}}`),
		[]byte(`[]`),
		[]byte(`{"ok":true} trailing`),
	} {
		if ValidJSONObject(input) {
			t.Fatalf("accepted invalid JSON input %s", input)
		}
	}
	if !ValidJSONObject([]byte(`{"references":[{"kind":"unionid","scope":"s","value":"v"}]}`)) {
		t.Fatal("rejected unique JSON object")
	}
}
