package main

import (
	"context"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

type openPlatformMachineMutationAuthorizerStub struct {
	calls       int
	principal   accessdomain.MachinePrincipal
	requirement accessport.MachineMutationRequirement
	err         error
}

func (stub *openPlatformMachineMutationAuthorizerStub) AuthorizeMachineMutationWithin(_ context.Context, principal accessdomain.MachinePrincipal, requirement accessport.MachineMutationRequirement) error {
	stub.calls++
	stub.principal = principal
	stub.requirement = requirement
	return stub.err
}

func bindOpenPlatformTestMachineMutationFence(t *testing.T, executor *openPlatformExecutor, authorizer *openPlatformMachineMutationAuthorizerStub) {
	t.Helper()
	if err := executor.BindV1MachineMutationFence(authorizer, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
}

func TestV1MachineWriteOperationsRequireTheAccessMutationFence(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	ai := &v1AIMachineStub{}
	if err := executor.BindV1AI(ai, ai, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
	audit := &openPlatformMachineAuditStub{}
	if err := executor.BindV1OperationAudit(audit, directUnitOfWork{}); err != nil {
		t.Fatal(err)
	}
	executor.coreAudience = &coreAudienceStub{}
	principal := accessdomain.MachinePrincipal{ClientID: "machine-a", Audience: "external_integration", Scopes: []string{"write"}, Capabilities: []string{string(openplatformport.CapabilityAIReviewPlanCreate), string(openplatformport.CapabilityCorePushWrite)}}
	contains := func(want openplatformport.OperationID) bool {
		t.Helper()
		items, err := executor.Available(context.Background(), principal)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.OperationID == want {
				return true
			}
		}
		return false
	}
	if contains(openplatformport.OperationAIReviewPlanCreate) || contains(openplatformport.OperationCorePushRecord) {
		t.Fatal("machine write operation published without the Access transaction fence")
	}
	bindOpenPlatformTestMachineMutationFence(t, executor, &openPlatformMachineMutationAuthorizerStub{})
	if !contains(openplatformport.OperationAIReviewPlanCreate) || !contains(openplatformport.OperationCorePushRecord) {
		t.Fatal("machine write operations remained hidden after all write dependencies were composed")
	}
}
