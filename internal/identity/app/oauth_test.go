package app

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"testing"
)

func TestOAuthSubjectPreservesRootsAndRejectsCrossRoot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	oa := verifiedFact(t, identitydomain.KindOAOpenID, "wechat-app:oa", "openid")
	union := verifiedFact(t, identitydomain.KindUnionID, "wechat-open-platform:platform", "union")
	prior, err := service.ProvisionCustomerFromVerifiedIdentity(ctx, union)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ProvisionVerifiedOAuthSubject(ctx, identityport.OAuthSubjectCommand{OpenID: oa, UnionID: union, EventID: "verified-oauth-event-0001"})
	if err != nil || result.Conflict || result.CustomerID != prior.CustomerID || store.CustomerCount() != 1 {
		t.Fatalf("existing union root not reused: %+v %v", result, err)
	}
	other := verifiedFact(t, identitydomain.KindUnionID, "wechat-open-platform:platform", "other-union")
	_, err = service.ProvisionCustomerFromVerifiedIdentity(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	result, err = service.ProvisionVerifiedOAuthSubject(ctx, identityport.OAuthSubjectCommand{OpenID: oa, UnionID: other, EventID: "verified-oauth-event-0002"})
	if err != nil || !result.Conflict || result.CustomerID != prior.CustomerID || store.CustomerCount() != 2 {
		t.Fatalf("cross roots not held: %+v %v", result, err)
	}
}
