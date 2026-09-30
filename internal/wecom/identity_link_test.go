package wecom

import (
	"context"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

type provisionOnlyIdentity struct {
	calls   int
	command identityport.ProvisionCommand
	result  identityport.ProvisionResult
}

func (identity *provisionOnlyIdentity) ProvisionVerifiedIdentity(_ context.Context, command identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	identity.calls++
	identity.command = command
	return identity.result, nil
}

func TestResolveOrBindWeComContactKeepsExtOnlyProvisioningWithoutResolver(t *testing.T) {
	fact, err := provider.VerifiedExternalContact("corp-1", "external-1", "wecom.directory_sync")
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &provisionOnlyIdentity{result: identityport.ProvisionResult{CustomerID: customerdomain.CustomerID(14), IdentityID: 27, Created: true}}
	outcome, err := resolveOrBindWeComContact(context.Background(), nil, provisioner, nil, fact, nil, "sync-key", identitydomain.LinkEvidence{})
	if err != nil || outcome.Conflict || outcome.Provision != provisioner.result {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if provisioner.calls != 1 || provisioner.command.Fact.Reference().NormalizedValue != "external-1" || provisioner.command.IdempotencyKey != "sync-key" {
		t.Fatalf("provisioner calls=%d command=%+v", provisioner.calls, provisioner.command)
	}
}
