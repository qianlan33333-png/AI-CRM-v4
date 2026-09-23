package main

import (
	"context"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"testing"
)

type existingResolver struct {
	results []identityport.ResolveResult
	calls   int
}

func (r *existingResolver) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	v := r.results[r.calls]
	r.calls++
	return v, nil
}
func TestExistingOnlyNeverProvisionsAndRejectsUnresolvedOrMixedRoots(t *testing.T) {
	factory := identityadapter.ProviderHistory{}
	f, err := factory.VerifiedHistoricalFact(identityport.HistoricalVerifiedInput{Kind: "wecom_external_userid", Scope: "wecom-corp:test", Value: "external-test", Source: "provider-history:verified-target-existing"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		results []identityport.ResolveResult
		wantErr bool
	}{
		{"same root", []identityport.ResolveResult{{Status: identityport.ResolveFound, CustomerID: 9, IdentityID: 3}, {Status: identityport.ResolveFound, CustomerID: 9, IdentityID: 4}}, false},
		{"missing", []identityport.ResolveResult{{Status: identityport.ResolveNotFound}}, true},
		{"different roots", []identityport.ResolveResult{{Status: identityport.ResolveFound, CustomerID: 9, IdentityID: 3}, {Status: identityport.ResolveFound, CustomerID: 10, IdentityID: 4}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &existingResolver{results: c.results}
			facts := []identitydomain.VerifiedFact{f}
			if len(c.results) == 2 {
				facts = append(facts, f)
			}
			got, e := (existingIdentitiesOnly{Resolver: r}).ProvisionHistoricalSubject(context.Background(), identityport.HistoricalSubjectCommand{Facts: facts})
			if (e != nil) != c.wantErr {
				t.Fatalf("error=%v", e)
			}
			if !c.wantErr && (got.CustomerID != 9 || len(got.IdentityIDs) != 2) {
				t.Fatal("existing roots not preserved")
			}
		})
	}
}
