package main

import (
	"context"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type contactDescriptionIdentityStub struct {
	value string
	found bool
	kind  identitydomain.Kind
	scope string
}

func (stub *contactDescriptionIdentityStub) VerifiedExternalIdentityValue(_ context.Context, _ customerdomain.CustomerID, kind identitydomain.Kind, scope string) (string, bool, error) {
	stub.kind, stub.scope = kind, scope
	return stub.value, stub.found, nil
}

func TestContactDescriptionTargetAdapterUsesScopedIdentityWithoutCallbackRelationship(t *testing.T) {
	identities := &contactDescriptionIdentityStub{value: "external-1", found: true}
	adapter := contactDescriptionTargetAdapter{uow: directUnitOfWork{}, corpID: "corp-1", identities: identities}
	target, err := adapter.ResolveContactDescriptionTarget(context.Background(), customerdomain.CustomerID(7), "follow-staff")
	if err != nil || target.EmployeeUserID != "follow-staff" || target.ExternalUserID != "external-1" || identities.kind != identitydomain.KindWeComExternalUserID || identities.scope != "wecom-corp:corp-1" {
		t.Fatalf("target=%+v kind=%q scope=%q err=%v", target, identities.kind, identities.scope, err)
	}
}
