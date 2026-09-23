package cutoverproof

import (
	ia "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	"strings"
	"testing"
)

func TestProvisionRequiresSeparateLiveVerification(t *testing.T) {
	s := fixture()
	s.Version = 2
	s.ResolutionMode = ExistingWecomOnly
	s.Scopes.UnionScope = ""
	if _, e := ProvisionReferences(s, ia.ProviderHistory{}); e == nil {
		t.Fatal("resolve-only evidence authorized provision")
	}
	s.Version = 3
	s.ResolutionMode = VerifiedWecomProvision
	s.ParentProofSHA = strings.Repeat("a", 64)
	s.CandidateSHA = strings.Repeat("b", 64)
	s.Verification = map[string]string{s.Rows[0].UnionID: "provider_failed"}
	refs, e := ProvisionReferences(s, ia.ProviderHistory{})
	if e != nil || len(refs) != 0 {
		t.Fatal("failed provider result authorized provision")
	}
	s.Verification[s.Rows[0].UnionID] = "verified"
	refs, e = ProvisionReferences(s, ia.ProviderHistory{})
	if e != nil || len(refs) != 1 {
		t.Fatal("live verified fact rejected")
	}
	s.Rows[0].Evidence[0].ProviderOK = false
	refs, e = ProvisionReferences(s, ia.ProviderHistory{})
	if e != nil || len(refs) != 0 {
		t.Fatal("invalid original provider evidence accepted")
	}
	s.Scopes.UnionScope = "wechat-open-platform:unconfirmed"
	if s.Validate() == nil {
		t.Fatal("Union scope accepted")
	}
}
