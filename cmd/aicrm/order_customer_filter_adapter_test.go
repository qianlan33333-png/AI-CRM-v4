package main

import (
	"context"
	"testing"

	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type orderCustomerFilterTestUOW struct{}

func (orderCustomerFilterTestUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

func TestOrderCustomerFilterAdapterResolvesOnlyExistingIdentity(t *testing.T) {
	store := identityapp.NewMemoryStore()
	oneID := identityapp.OneIDService{Store: store}
	adapter := orderCustomerFilterAdapter{uow: orderCustomerFilterTestUOW{}, oneID: oneID, corpID: "fixture-corp"}

	missing, err := adapter.ResolveOrderCustomerFilter(context.Background(), orderport.CustomerFilter{ExternalUserID: "external-missing"})
	if err != nil || missing.Status != orderport.CustomerFilterNotFound || store.CustomerCount() != 0 {
		t.Fatalf("missing=%+v customers=%d err=%v", missing, store.CustomerCount(), err)
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:fixture-corp", Value: "external-known", Source: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	provisioned, err := oneID.ProvisionVerifiedIdentity(context.Background(), identityport.ProvisionCommand{Fact: fact})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := adapter.ResolveOrderCustomerFilter(context.Background(), orderport.CustomerFilter{ExternalUserID: "external-known"})
	if err != nil || resolved.Status != orderport.CustomerFilterFound || resolved.CustomerID != provisioned.CustomerID || store.CustomerCount() != 1 {
		t.Fatalf("resolved=%+v provisioned=%+v customers=%d err=%v", resolved, provisioned, store.CustomerCount(), err)
	}
	invalid, err := adapter.ResolveOrderCustomerFilter(context.Background(), orderport.CustomerFilter{ExternalUserID: " external-known"})
	if err != nil || invalid.Status != orderport.CustomerFilterInvalid || store.CustomerCount() != 1 {
		t.Fatalf("invalid=%+v customers=%d err=%v", invalid, store.CustomerCount(), err)
	}
}
