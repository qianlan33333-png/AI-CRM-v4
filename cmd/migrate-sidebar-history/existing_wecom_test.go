package main

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"strings"
	"testing"
)

type recordingResolver struct {
	calls int
	kind  identitydomain.Kind
}

func (r *recordingResolver) Resolve(_ context.Context, ref identitydomain.Reference) (identityport.ResolveResult, error) {
	r.calls++
	r.kind = ref.Kind
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}
func TestExternalProofNeverFallsBackToUnion(t *testing.T) {
	owner := &recordingResolver{}
	ctx := context.WithValue(context.Background(), externalProofContext{}, map[string]identitydomain.Reference{"opaque-source-ref": {Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:test", Value: "synthetic"}})
	_, err := resolveSidebarSubject(ctx, owner, "wechat-open-platform:wrong", "opaque-source-ref")
	if err != nil || owner.kind != identitydomain.KindWeComExternalUserID {
		t.Fatal("did not use proof")
	}
	_, err = resolveSidebarSubject(ctx, owner, "wechat-open-platform:wrong", "missing")
	if err != nil || owner.calls != 1 {
		t.Fatal("unproven source fell back")
	}
	_, err = resolveSidebarSubject(context.Background(), owner, "", "opaque-source-ref")
	if err == nil {
		t.Fatal("unbound source accepted")
	}
}
func TestExternalManifestStrictBinding(t *testing.T) {
	m := manifest{SchemaVersion: 3, ResolutionMode: "existing_wecom_only", CorpID: "test", ProofSHA256: strings.Repeat("a", 64), SourceSHA256: strings.Repeat("b", 64)}
	if !validSubjectMode(m) {
		t.Fatal("invalid fixture")
	}
	m.UnionIDScope = "wechat-open-platform:test"
	if validSubjectMode(m) {
		t.Fatal("union mixed with opaque proof")
	}
	m.UnionIDScope = ""
	m.ProofSHA256 = ""
	if validSubjectMode(m) {
		t.Fatal("unbound proof")
	}
}
