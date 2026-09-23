package cutoverproof

import (
	"context"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"testing"
)

func TestExistingWecomProofNeverConstructsUnionFact(t *testing.T) {
	s := fixture()
	s.Version = 2
	s.ResolutionMode = ExistingWecomOnly
	s.Scopes.UnionScope = ""
	refs, counts, e := ExistingWecomReferences(s, identityadapter.ProviderHistory{})
	if e != nil || counts["ready"] != 1 || len(refs) != 1 {
		t.Fatalf("proof %v %v", counts, e)
	}
	for _, r := range refs {
		if r.Kind != identitydomain.KindWeComExternalUserID || r.Scope != "wecom-corp:test-corp" {
			t.Fatal("wrong reference")
		}
	}
	if _, e := BuildPlan(context.Background(), s, resolver{}, identityadapter.ProviderHistory{}); e == nil {
		t.Fatal("provisioning plan accepted resolve-only proof")
	}
	for _, mutate := range []func(*Snapshot){
		func(s *Snapshot) { s.Rows[0].Evidence[0].ProviderOK = false },
		func(s *Snapshot) { s.Rows[0].Evidence[0].RawUnionID = "other" },
		func(s *Snapshot) { s.Rows[0].Evidence[0].CorpID = "other" },
		func(s *Snapshot) { s.Rows[0].Evidence = nil },
		func(s *Snapshot) { r := s.Rows[0]; r.UnionID = "other"; s.Rows = append(s.Rows, r) },
	} {
		x := fixture()
		x.Version = 2
		x.ResolutionMode = ExistingWecomOnly
		x.Scopes.UnionScope = ""
		mutate(&x)
		got, _, err := ExistingWecomReferences(x, identityadapter.ProviderHistory{})
		if err != nil || len(got) != 0 {
			t.Fatal("unsafe proof accepted")
		}
	}
	s.Scopes.UnionScope = "wechat-open-platform:unconfirmed"
	if s.Validate() == nil {
		t.Fatal("union scope accepted in resolve-only proof")
	}
}
