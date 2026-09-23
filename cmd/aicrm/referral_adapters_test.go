package main

import (
	"context"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

type referralVerifierResolver struct {
	result customerport.CanonicalCustomer
}

func (stub referralVerifierResolver) ResolveCanonicalCustomer(context.Context, customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return stub.result, nil
}

type referralVerifierIdentity struct{ trusted bool }

func (stub referralVerifierIdentity) HasActiveVerifiedIdentity(context.Context, customerdomain.CustomerID) (bool, error) {
	return stub.trusted, nil
}

func TestReferralCaptainVerifierRequiresCanonicalRootAndVerifiedEvidence(t *testing.T) {
	canonical := customerdomain.CustomerID(7)
	accepted := referralCanonicalCustomerVerifier{
		resolver:   referralVerifierResolver{result: customerport.CanonicalCustomer{RequestedCustomerID: canonical, CustomerID: canonical}},
		identities: referralVerifierIdentity{trusted: true},
	}
	if trusted, err := accepted.VerifyCanonicalCustomer(context.Background(), 7); err != nil || !trusted {
		t.Fatalf("trusted=%v err=%v", trusted, err)
	}
	inactive := accepted
	inactive.identities = referralVerifierIdentity{trusted: false}
	if trusted, err := inactive.VerifyCanonicalCustomer(context.Background(), 7); err != nil || trusted {
		t.Fatalf("inactive trusted=%v err=%v", trusted, err)
	}
	merged := accepted
	merged.resolver = referralVerifierResolver{result: customerport.CanonicalCustomer{RequestedCustomerID: canonical, CustomerID: 9, Merged: true}}
	if trusted, err := merged.VerifyCanonicalCustomer(context.Background(), 7); err != nil || trusted {
		t.Fatalf("merged trusted=%v err=%v", trusted, err)
	}
}
