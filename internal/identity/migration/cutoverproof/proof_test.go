package cutoverproof

import (
	"context"
	"encoding/base64"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type resolver map[identitydomain.Kind]int64

func (r resolver) Resolve(_ context.Context, ref identitydomain.Reference) (identityport.ResolveResult, error) {
	id := r[ref.Kind]
	if id == 0 {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: customerdomain.CustomerID(id)}, nil
}
func fixture() Snapshot {
	return Snapshot{Version: 1, Scopes: Scopes{CorpID: "test-corp", UnionScope: "wechat-open-platform:test-platform"}, CapturedAt: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), Rows: []Row{{UnionID: "synthetic-union", CRMStatus: "active", PrimaryExternalID: "synthetic-external", Evidence: []Evidence{{MapID: 1, ProviderOK: true, CorpID: "test-corp", ExternalID: "synthetic-external", UnionID: "synthetic-union", Status: "active", RawUnionID: "synthetic-union", RawExternalID: "synthetic-external"}}}}}
}
func TestProofPlanRequiresDirectProviderRelationship(t *testing.T) {
	cases := []struct {
		name, state string
		change      func(*Snapshot)
		resolve     resolver
		first       identitydomain.Kind
	}{
		{"new", "candidate_provision", nil, resolver{}, identitydomain.KindWeComExternalUserID},
		{"existing-external", "candidate_attach", nil, resolver{identitydomain.KindWeComExternalUserID: 7}, identitydomain.KindWeComExternalUserID},
		{"existing-union", "candidate_attach", nil, resolver{identitydomain.KindUnionID: 7}, identitydomain.KindUnionID},
		{"same-root", "already_linked", nil, resolver{identitydomain.KindUnionID: 7, identitydomain.KindWeComExternalUserID: 7}, identitydomain.KindWeComExternalUserID},
		{"cross-root", "quarantine_cross_root", nil, resolver{identitydomain.KindUnionID: 7, identitydomain.KindWeComExternalUserID: 8}, ""},
		{"no-map", "quarantine_missing_provider_relation", func(s *Snapshot) { s.Rows[0].Evidence = nil }, resolver{}, ""},
		{"provider-error", "quarantine_unverified_provider_relation", func(s *Snapshot) { s.Rows[0].Evidence[0].ProviderOK = false }, resolver{}, ""},
		{"wrong-raw-union", "quarantine_unverified_provider_relation", func(s *Snapshot) { s.Rows[0].Evidence[0].RawUnionID = "different" }, resolver{}, ""},
		{"wrong-corp", "quarantine_unverified_provider_relation", func(s *Snapshot) { s.Rows[0].Evidence[0].CorpID = "different" }, resolver{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			if tc.change != nil {
				tc.change(&s)
			}
			p, e := BuildPlan(context.Background(), s, tc.resolve, identityadapter.ProviderHistory{})
			if e != nil {
				t.Fatal(e)
			}
			r := p.Rows[0]
			if r.State != tc.state {
				t.Fatalf("state=%s", r.State)
			}
			if strings.Contains(r.SourceKey, s.Rows[0].UnionID) {
				t.Fatal("raw identity leaked")
			}
			if tc.first != "" && r.Facts[0].Reference().Kind != tc.first {
				t.Fatal("existing root not first anchor")
			}
			if tc.first == "" && len(r.Facts) > 0 {
				t.Fatal("quarantine retained verified facts")
			}
		})
	}
}
func TestProofAmbiguousExternalAndMissingScope(t *testing.T) {
	s := fixture()
	r := s.Rows[0]
	r.UnionID = "other-union"
	s.Rows = append(s.Rows, r)
	p, e := BuildPlan(context.Background(), s, resolver{}, identityadapter.ProviderHistory{})
	if e != nil || p.Counts["quarantine_ambiguous_source_subject"] != 2 {
		t.Fatalf("plan %+v %v", p, e)
	}
	s.Scopes.UnionScope = ""
	if _, e = BuildPlan(context.Background(), s, resolver{}, identityadapter.ProviderHistory{}); e == nil {
		t.Fatal("missing scope accepted")
	}
}
func TestProtectedProofRoundtripTamperAndExclusiveCreate(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	path := filepath.Join(dir, "proof.enc")
	if e := os.WriteFile(key, []byte(base64.RawStdEncoding.EncodeToString(make([]byte, 32))), 0600); e != nil {
		t.Fatal(e)
	}
	s := fixture()
	d, e := Seal(s, path, key)
	if e != nil {
		t.Fatal(e)
	}
	loaded, got, e := Load(path, key)
	if e != nil || got != d || len(loaded.Rows) != 1 {
		t.Fatalf("roundtrip %v", e)
	}
	if _, e = Seal(s, path, key); e == nil {
		t.Fatal("overwrote snapshot")
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), s.Rows[0].UnionID) {
		t.Fatal("plaintext persisted")
	}
	b[len(b)-1] ^= 1
	_ = os.WriteFile(path, b, 0600)
	if _, _, e = Load(path, key); e == nil {
		t.Fatal("tampered snapshot accepted")
	}
}
