package main

import (
	"context"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type productPaidTagBindingStub struct{}

func (productPaidTagBindingStub) ProviderTagID(_ context.Context, id int64) (string, bool, error) {
	if id == 130 {
		return "provider-tag-130", true, nil
	}
	return "", false, nil
}

type productPaidFirstFollowStub struct {
	staff string
	err   error
	calls int
}

func (s *productPaidFirstFollowStub) ReadFirstExternalContactFollow(_ context.Context, external string) (string, error) {
	s.calls++
	if external != "external-1" {
		return "", errors.New("wrong external contact")
	}
	return s.staff, s.err
}

func TestProductPaidTagTargetFreezesIdentityWithoutLocalOwnerOrFollow(t *testing.T) {
	identities := &contactDescriptionIdentityStub{value: "external-1", found: true}
	gate := customerTagCommandGate{uow: directUnitOfWork{}, corpID: "corp-1", identities: identities, tags: productPaidTagBindingStub{}}
	target, err := gate.FreezeTagCommandTarget(context.Background(), "product_paid_purchase", customerport.TagCommandTarget{CustomerID: 7, AddTagIDs: []int64{130}})
	if err != nil || target.StaffID != 0 || target.TargetDigest != string(effectport.Hash("customer.tag.command.product-target.v1", "external-1")) || identities.kind != identitydomain.KindWeComExternalUserID || identities.scope != "wecom-corp:corp-1" {
		t.Fatalf("target=%+v kind=%q scope=%q err=%v", target, identities.kind, identities.scope, err)
	}
	identities.found = false
	if _, err = gate.FreezeTagCommandTarget(context.Background(), "product_paid_purchase", customerport.TagCommandTarget{CustomerID: 7, AddTagIDs: []int64{130}}); err == nil {
		t.Fatal("missing verified external identity must end without a tag effect")
	}
}

func TestProductPaidContactReadsFirstFollowAfterIdentityTransaction(t *testing.T) {
	identities := &contactDescriptionIdentityStub{value: "external-1", found: true}
	follows := &productPaidFirstFollowStub{staff: "first-staff"}
	adapter := productPaidTagContactAdapter{uow: directUnitOfWork{}, corpID: "corp-1", identities: identities, contacts: follows}
	contact, err := adapter.FirstProductPaidContact(context.Background(), customerdomain.CustomerID(7))
	if err != nil || contact.ExternalUserID != "external-1" || contact.EmployeeUserID != "first-staff" || follows.calls != 1 {
		t.Fatalf("contact=%+v calls=%d err=%v", contact, follows.calls, err)
	}
	identities.found = false
	_, err = adapter.FirstProductPaidContact(context.Background(), customerdomain.CustomerID(7))
	if !errors.Is(err, wecomport.ErrFirstExternalContactFollowUnavailable) || follows.calls != 1 {
		t.Fatalf("missing identity err=%v calls=%d", err, follows.calls)
	}
}
