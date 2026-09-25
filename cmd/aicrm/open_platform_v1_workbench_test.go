package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type workbenchUnionStub struct {
	verified bool
	scope    string
}

func (s *workbenchUnionStub) HasVerifiedScopedUnion(_ context.Context, _ customerdomain.CustomerID, scope, _ string) (bool, error) {
	s.scope = scope
	return s.verified, nil
}

type workbenchContactStub struct{ rows []wecomport.MachineContactRow }

func (s workbenchContactStub) MachineContactRows(context.Context, string) ([]wecomport.MachineContactRow, []wecomport.MachineUnresolvedRow, error) {
	return s.rows, nil, nil
}

type workbenchStatusStub struct{}

func (workbenchStatusStub) MachineContactStatuses(_ context.Context, ids []int64) (map[int64]customerport.MachineContactStatus, error) {
	out := map[int64]customerport.MachineContactStatus{}
	for _, id := range ids {
		out[id] = customerport.MachineContactStatus{State: "active"}
	}
	return out, nil
}

type workbenchStaffStub struct{ accessport.Repository }

func (workbenchStaffStub) UsersByWeComUserIDs(context.Context, []string) ([]accessdomain.User, error) {
	return []accessdomain.User{{ID: 9, WeComUserID: "staff-1", Active: true}}, nil
}

func TestV1WorkbenchPackageFreezesOnlyResolvedIDsAndRejectsPartial(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	executor := v1ExecutorForTest(t, identity, &openPlatformProfileStub{})
	executor.scopes.SurveyUnionScopes = []string{"wechat-open-platform:shared"}
	executor.contacts = workbenchContactStub{rows: []wecomport.MachineContactRow{{CustomerID: 42, OwnerUserID: "staff-1", IdentityStatus: "resolved", BindingStatus: "bound"}}}
	executor.contactStatuses = workbenchStatusStub{}
	executor.contactStaff = workbenchStaffStub{}
	executor.owners = &openPlatformOwnerStub{items: []wecomport.AudiencePrimaryOwner{{CustomerID: 42, CorpScope: "wecom-corp:corp-main", OwnerUserID: "staff-1", Status: "known"}}}
	verified := &workbenchUnionStub{verified: false}
	executor.workbenchUnions = verified
	ai := &v1AIMachineStub{create: aiassistantport.MachineCreatePlanResult{Plan: aiassistantport.MachinePlan{ID: 12, ReviewState: aiassistantport.ReviewPending}}}
	if err := executor.BindV1AI(ai, ai, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
	audit := &openPlatformMachineAuditStub{}
	if err := executor.BindV1OperationAudit(audit, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{ClientID: "client-a", ClientRecord: 7, CorpID: "corp-main", Scopes: []string{"write"}, Capabilities: []string{string(openplatformport.CapabilityAIReviewPlanCreate), string(openplatformport.CapabilityWorkbenchPackageCreate)}, OwnerScope: accessdomain.OwnerScope{"corp_id": {"corp-main"}, "owner_userid": {"staff-1"}, "customer_id": {"42"}}}
	pkg := aiassistantport.MachinePackageMetadata{AudiencePackageID: "aud-1", AudienceVersion: "v1", CopyPackageID: "copy-1", CopyVersion: "v1", StrategyVersion: "v1", ProductFactVersion: "v1", SourceFingerprint: string(effectport.Hash("synthetic-source")), ApprovalRevision: "rev-1", ClientReference: "synthetic-1", MemberCount: 1}
	input, _ := json.Marshal(map[string]any{"name": "synthetic review", "package": pkg, "members": []any{map[string]any{"union_id": "union-synthetic-42", "owner_userid": "staff-1"}}, "content": []any{map[string]any{"kind": "text", "text": "test only"}}})
	invocation := openplatformport.Invocation{Operation: openplatformport.OperationAIReviewPlanCreate, Principal: principal, IdempotencyKey: strings.Repeat("a", 16), Input: input}
	_, err := executor.Invoke(context.Background(), invocation)
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorConflict || ai.command.SourceKind != "" || audit.calls != 1 {
		t.Fatalf("rejected err=%v command=%+v audit=%d", err, ai.command, audit.calls)
	}
	decisions, ok := openplatformport.ErrorDetailsOf(err).(map[string]any)
	if !ok || decisions["created"] != false {
		t.Fatalf("missing safe decisions: %v", err)
	}
	verified.verified = true
	created, err := executor.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if ai.command.SourceKind != "scrm_workbench" || len(ai.command.Recipients) != 1 || ai.command.Recipients[0].CustomerID != 42 || ai.command.Recipients[0].StaffID != 9 || ai.command.Package == nil || verified.scope != "wechat-open-platform:shared" {
		t.Fatalf("unsafe command: %+v", ai.command)
	}
	encoded, _ := json.Marshal(created.Data)
	if strings.Contains(string(encoded), "union-synthetic-42") || strings.Contains(string(encoded), "test only") || !strings.Contains(string(encoded), "automatic_send_allowed") {
		t.Fatalf("unsafe response: %s", encoded)
	}
}
