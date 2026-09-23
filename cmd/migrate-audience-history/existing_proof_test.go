package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"testing"
	"time"
)

type proofResolver struct{ calls int }

func (r *proofResolver) Resolve(_ context.Context, ref identitydomain.Reference) (identityport.ResolveResult, error) {
	r.calls++
	if ref.Kind != identitydomain.KindWeComExternalUserID {
		panic("UnionID must not resolve")
	}
	return identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 10, IdentityID: 20}, nil
}
func TestDerivedEnvelopeFreezesSourceAndProof(t *testing.T) {
	s := fixture(t)
	b, _ := json.Marshal(s)
	p := proof.Snapshot{Version: 2, ResolutionMode: proof.ExistingWecomOnly, Scopes: proof.Scopes{CorpID: "fixture"}, CapturedAt: s.CapturedAt, Rows: []proof.Row{}}
	d, _ := p.Digest()
	s.ExistingProof = &existingProofEnvelope{ProofSourceSHA256: sum([]byte("frozen raw")), SourceSHA256: sum(b), ProofSHA256: hex.EncodeToString(d[:]), DerivedAt: s.CapturedAt.Add(time.Minute), Proof: p}
	if e := validate(s); e != nil {
		t.Fatal(e)
	}
	s.CapturedAt = s.CapturedAt.Add(time.Second)
	if validate(s) == nil {
		t.Fatal("source time rewritten")
	}
	s.CapturedAt = p.CapturedAt
	s.ExistingProof.Proof.Scopes.CorpID = "changed"
	if validate(s) == nil {
		t.Fatal("proof drift ignored")
	}
}
func TestProofResolutionDoesNotInferUnionNamespace(t *testing.T) {
	refs := map[string]identitydomain.Reference{"opaque": {Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:fixture", Value: "external", Assurance: identitydomain.AssuranceVerified, Source: proof.Provenance}}
	for _, tc := range []struct {
		raw, want string
		calls     int
	}{{`{"unionid":"opaque","identity_type":"unionid","identity_value":"opaque"}`, "resolved", 1}, {`{"unionid":"opaque","identity_type":"unionid","identity_value":"other"}`, "conflict", 0}, {`{"unionid":"missing"}`, "unresolved", 0}, {`{"unionid":"opaque","identity_type":"external_userid","identity_value":"other"}`, "conflict", 0}} {
		var m map[string]json.RawMessage
		json.Unmarshal([]byte(tc.raw), &m)
		r := &proofResolver{}
		_, reason, e := resolveProofMember(context.Background(), m, refs, r, map[identitydomain.Reference]identityport.ResolveResult{})
		if e != nil || reason != tc.want || r.calls != tc.calls {
			t.Fatalf("%s %v calls=%d", reason, e, r.calls)
		}
	}
}
