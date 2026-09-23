package app

import (
	"context"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

type commerceResolverFunc func(context.Context, identityport.CommerceReferenceSet) (identityport.CommerceResolution, error)

func (fn commerceResolverFunc) ResolveCommerce(ctx context.Context, set identityport.CommerceReferenceSet) (identityport.CommerceResolution, error) {
	return fn(ctx, set)
}

func commerceReference(value string, assurance identitydomain.Assurance) identitydomain.Reference {
	return identitydomain.Reference{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:commerce", Value: value, Assurance: assurance, Source: "payment.oauth"}
}

func TestAttributionPreservesDifferentPayerAndBeneficiary(t *testing.T) {
	resolver := commerceResolverFunc(func(_ context.Context, set identityport.CommerceReferenceSet) (identityport.CommerceResolution, error) {
		id := customerdomain.CustomerID(11)
		if set.References[0].Value == "beneficiary" {
			id = 22
		}
		return identityport.CommerceResolution{Status: identityport.CommerceResolved, CustomerID: id}, nil
	})
	receipt, err := NewAttributor(resolver).Attribute(context.Background(), AttributionCommand{
		RecordOrigin: domain.RecordOriginNative,
		Payer:        ActorEvidence{References: []identitydomain.Reference{commerceReference("payer", identitydomain.AssuranceVerified)}},
		Beneficiary:  ActorEvidence{References: []identitydomain.Reference{commerceReference("beneficiary", identitydomain.AssuranceVerified)}},
	})
	if err != nil || receipt.Status != AttributionResolved || receipt.PayerCustomerID == nil || *receipt.PayerCustomerID != 11 || receipt.BeneficiaryCustomerID == nil || *receipt.BeneficiaryCustomerID != 22 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestAttributionRejectsDeclaredPhoneForNativeCheckoutWithoutCallingIdentity(t *testing.T) {
	called := false
	resolver := commerceResolverFunc(func(context.Context, identityport.CommerceReferenceSet) (identityport.CommerceResolution, error) {
		called = true
		return identityport.CommerceResolution{}, nil
	})
	phone := identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:e164", Value: "+8613812345678", Assurance: identitydomain.AssuranceDeclared, Source: "checkout.body"}
	receipt, err := NewAttributor(resolver).Attribute(context.Background(), AttributionCommand{RecordOrigin: domain.RecordOriginNative, Payer: ActorEvidence{References: []identitydomain.Reference{phone}}, Beneficiary: ActorEvidence{References: []identitydomain.Reference{phone}}})
	if err != nil || receipt.Status != AttributionRejected || receipt.ReasonCode != "native_identity_not_verified" || called {
		t.Fatalf("receipt=%+v called=%v err=%v", receipt, called, err)
	}
}

func TestHistoricalAttributionKeepsUnresolvedOrderFloatingWithSafeReceipt(t *testing.T) {
	resolver := commerceResolverFunc(func(_ context.Context, set identityport.CommerceReferenceSet) (identityport.CommerceResolution, error) {
		if set.References[0].Value == "payer" {
			return identityport.CommerceResolution{Status: identityport.CommerceResolved, CustomerID: 31}, nil
		}
		return identityport.CommerceResolution{Status: identityport.CommerceNotFound}, nil
	})
	receipt, err := NewAttributor(resolver).Attribute(context.Background(), AttributionCommand{
		RecordOrigin: domain.RecordOriginHistory,
		Payer:        ActorEvidence{References: []identitydomain.Reference{commerceReference("payer", identitydomain.AssuranceDeclared)}},
		Beneficiary:  ActorEvidence{References: []identitydomain.Reference{commerceReference("missing", identitydomain.AssuranceDeclared)}},
	})
	if err != nil || receipt.Status != AttributionQuarantined || receipt.ReasonCode != "identity_not_found" || receipt.PayerCustomerID == nil || *receipt.PayerCustomerID != 31 || receipt.BeneficiaryCustomerID != nil {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestHistoricalAttributionQuarantinesMultiRootConflict(t *testing.T) {
	resolver := commerceResolverFunc(func(context.Context, identityport.CommerceReferenceSet) (identityport.CommerceResolution, error) {
		return identityport.CommerceResolution{Status: identityport.CommerceConflict}, nil
	})
	reference := commerceReference("conflict", identitydomain.AssuranceDeclared)
	receipt, err := NewAttributor(resolver).Attribute(context.Background(), AttributionCommand{RecordOrigin: domain.RecordOriginHistory, Payer: ActorEvidence{References: []identitydomain.Reference{reference}}, Beneficiary: ActorEvidence{References: []identitydomain.Reference{reference}}})
	if err != nil || receipt.Status != AttributionQuarantined || receipt.ReasonCode != "identity_multi_root_conflict" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}
