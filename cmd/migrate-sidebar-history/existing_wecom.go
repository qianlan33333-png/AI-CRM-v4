package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	oaproof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/oaproof"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"strings"
)

type externalProofContext struct{}

func isOpaqueSource(m manifest) bool { return m.SchemaVersion == 4 }

func validSubjectMode(m manifest) bool {
	if isOpaqueSource(m) {
		return m.ResolutionMode == "opaque_source_only" && m.UnionIDScope == "" && m.ProofSHA256 == "" && m.SourceSHA256 == "" && m.CorpID == "" && m.OAProofSHA256 == ""
	}
	if m.OAProofSHA256 != "" {
		if b, e := hex.DecodeString(m.OAProofSHA256); e != nil || len(b) != 32 || m.SchemaVersion != 3 {
			return false
		}
	}
	if m.SchemaVersion == legacySchemaVersion || m.SchemaVersion == currentSchemaVersion {
		return strings.HasPrefix(m.UnionIDScope, "wechat-open-platform:") && m.ResolutionMode == "" && m.ProofSHA256 == "" && m.SourceSHA256 == "" && m.CorpID == ""
	}
	_, a := hex.DecodeString(m.ProofSHA256)
	_, b := hex.DecodeString(m.SourceSHA256)
	return m.SchemaVersion == 3 && m.ResolutionMode == proof.ExistingWecomOnly && m.UnionIDScope == "" && m.CorpID != "" && identitydomain.ValidateNamespace(identitydomain.KindWeComExternalUserID, "wecom-corp:"+m.CorpID) == nil && len(m.ProofSHA256) == 64 && len(m.SourceSHA256) == 64 && a == nil && b == nil
}
func externalReferences(cfg options) (map[string]identitydomain.Reference, error) {
	if !cfg.matchedCorp || cfg.corp == "" {
		return nil, errors.New("independently matched Corp confirmation required")
	}
	s, d, e := proof.Load(cfg.proofPath, cfg.proofKey)
	if e != nil {
		return nil, e
	}
	if s.Version != 2 || s.Scopes.CorpID != cfg.corp || cfg.proofDigest != hex.EncodeToString(d[:]) {
		return nil, errors.New("external proof binding mismatch")
	}
	refs, _, e := proof.ExistingWecomReferences(s, identityadapter.ProviderHistory{})
	if e != nil {
		return nil, e
	}
	if cfg.oaDigest != "" || cfg.oaProof != "" || cfg.oaKey != "" {
		oa, d, e := oaproof.Load(cfg.oaProof, cfg.oaKey)
		if e != nil {
			return nil, e
		}
		if cfg.oaDigest != hex.EncodeToString(d[:]) {
			return nil, errors.New("OA proof binding mismatch")
		}
		extra, e := oa.References()
		if e != nil {
			return nil, e
		}
		for k, v := range extra {
			if _, exists := refs[k]; exists {
				return nil, errors.New("ambiguous OA and WeCom evidence")
			}
			refs[k] = v
		}
	}
	return refs, nil
}
func bindExternalProof(cfg options, m manifest) error {
	if m.SchemaVersion == 3 || cfg.output == "" {
		return errors.New("derive requires original snapshot and exclusive output")
	}
	if _, e := externalReferences(cfg); e != nil {
		return e
	}
	m.SourceSHA256 = hex.EncodeToString(m.rawDigest[:])
	m.SchemaVersion = 3
	m.UnionIDScope = ""
	m.ResolutionMode = proof.ExistingWecomOnly
	m.ProofSHA256 = cfg.proofDigest
	m.OAProofSHA256 = cfg.oaDigest
	m.CorpID = cfg.corp
	binding := m.SourceSHA256 + ":" + m.ProofSHA256
	if m.OAProofSHA256 != "" {
		binding += ":" + m.OAProofSHA256
	}
	d := sha256.Sum256([]byte(binding))
	m.RunKey = "sidebar-external:" + hex.EncodeToString(d[:])
	if e := validate(m); e != nil {
		return e
	}
	if e := save(cfg.output, m); e != nil {
		return e
	}
	out, e := load(cfg.output)
	if e != nil {
		return e
	}
	return printSummary("bind-external-proof", out, false)
}
func withExternalProof(ctx context.Context, cfg options, m manifest) (context.Context, error) {
	if m.ProofSHA256 != cfg.proofDigest || m.CorpID != cfg.corp || m.OAProofSHA256 != cfg.oaDigest {
		return ctx, errors.New("manifest external proof mismatch")
	}
	refs, e := externalReferences(cfg)
	if e != nil {
		return ctx, e
	}
	return context.WithValue(ctx, externalProofContext{}, refs), nil
}
func resolveSidebarSubject(ctx context.Context, owner identityport.Resolver, scope, value string) (identityport.ResolveResult, error) {
	if refs, ok := ctx.Value(externalProofContext{}).(map[string]identitydomain.Reference); ok {
		ref, found := refs[value]
		if !found {
			return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
		}
		return owner.Resolve(ctx, ref)
	}
	if !strings.HasPrefix(scope, "wechat-open-platform:") {
		return identityport.ResolveResult{}, errors.New("missing protected subject evidence")
	}
	return owner.Resolve(ctx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: scope, Value: value, Assurance: identitydomain.AssuranceVerified, Source: "sidebar_history"})
}
