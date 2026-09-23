package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	cutoverproof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

func applySnapshot(s snapshot, digest string, evidence []byte, targetURL string, adminID int64, mode string) error {
	actor, err := segmentport.AdminMutationActor(adminID)
	if err != nil {
		return errors.New("actual importing admin ID required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: targetURL, MaxConnections: 2})
	if err != nil {
		return errors.New("target database unavailable")
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	repository, err := segmentstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	resolver := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	var out segmentport.HistoricalImportResult
	err = uow.Within(ctx, func(txctx context.Context) error {
		if mode != "apply" {
			tx, e := platformpostgres.RequireTransaction(txctx)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(txctx, `SET TRANSACTION READ ONLY`); e != nil {
				return e
			}
		}
		if mode == "reconcile" {
			d, _ := hex.DecodeString(digest)
			var hash segmentport.Digest
			copy(hash[:], d)
			var e error
			out, e = repository.VerifyHistorical(txctx, s.SourceSystem, hash)
			if e != nil {
				return e
			}
			expectedRows := 0
			for _, table := range s.Tables {
				expectedRows += table.Count
			}
			if out.SourceRows != expectedRows || out.Groups != s.Tables[tableNames[0]].Count || out.Packages != s.Tables[tableNames[1]].Count || out.SourceMembers != s.Tables[tableNames[3]].Count {
				return errors.New("conservation mismatch")
			}
			return nil
		}
		input, e := prepareImport(txctx, s, digest, evidence, actor, resolver)
		if e != nil {
			return e
		}
		if mode == "apply" {
			out, e = repository.ImportHistorical(txctx, input)
			return e
		}
		out.Groups = len(input.Groups)
		out.Packages = len(input.Packages)
		out.SourceRows = len(input.Rows)
		for _, p := range input.Packages {
			unique := map[int64]bool{}
			for _, m := range p.Members {
				out.SourceMembers++
				if !m.Active {
					out.Exited++
				} else if m.Reason == "resolved" {
					out.ResolvedActive++
					unique[m.CustomerID] = true
				} else {
					out.Quarantined++
				}
			}
			out.PublishedMembers += len(unique)
		}
		return nil
	})
	if err != nil {
		return errors.New("audience migration transaction failed; no partial writes committed")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": mode, "counts": out, "provider_calls": 0, "jobs_enqueued": 0, "identities_created": 0, "sha256": digest})
}

func prepareImport(ctx context.Context, s snapshot, digest string, evidence []byte, actor segmentport.MutationActor, resolver identityport.Resolver) (segmentport.HistoricalImport, error) {
	in := segmentport.HistoricalImport{Source: s.SourceSystem, CapturedAt: s.CapturedAt, EncryptedEvidence: evidence, Actor: actor}
	d, e := hex.DecodeString(digest)
	if e != nil || len(d) != 32 {
		return in, errors.New("invalid digest")
	}
	copy(in.Digest[:], d)
	if s.ExistingProof != nil {
		p := s.ExistingProof
		d, _ := hex.DecodeString(p.SourceSHA256)
		copy(in.ResolutionSourceDigest[:], d)
		d, _ = hex.DecodeString(p.ParentSHA256)
		copy(in.ResolutionParentDigest[:], d)
		in.ResolutionDerivedAt = p.DerivedAt
	}
	kinds := map[string]string{tableNames[0]: "group", tableNames[1]: "package", tableNames[2]: "version", tableNames[3]: "member"}
	for _, tableName := range tableNames {
		for _, row := range s.Tables[tableName].Rows {
			d, e := hex.DecodeString(row.Digest)
			if e != nil || len(d) != 32 {
				return in, errors.New("invalid source digest")
			}
			fact := segmentport.HistoricalRow{Kind: kinds[tableName], SourceID: row.ID}
			copy(fact.Digest[:], d)
			in.Rows = append(in.Rows, fact)
		}
	}
	for _, row := range s.Tables[tableNames[0]].Rows {
		m := decodeFact(row)
		in.Groups = append(in.Groups, segmentport.HistoricalGroup{SourceID: row.ID, Name: factString(m, "name")})
	}
	packageIndex := map[int64]int{}
	for _, row := range s.Tables[tableNames[1]].Rows {
		m := decodeFact(row)
		status := factString(m, "status")
		if status != "active" && status != "archived" && status != "paused" {
			return in, errors.New("unknown package status")
		}
		group := int64(0)
		if raw := m["group_id"]; len(raw) > 0 && string(raw) != "null" {
			group, e = integer(m, "group_id", true)
			if e != nil {
				return in, e
			}
		}
		packageIndex[row.ID] = len(in.Packages)
		in.Packages = append(in.Packages, segmentport.HistoricalPackage{SourceID: row.ID, GroupSourceID: group, Name: factString(m, "name"), Archived: status == "archived"})
	}
	var proofRefs map[string]identitydomain.Reference
	if s.ExistingProof != nil {
		var e error
		proofRefs, _, e = cutoverproof.ExistingWecomReferences(s.ExistingProof.Proof, identityadapter.ProviderHistory{})
		if e != nil {
			return in, e
		}
	}
	// Cache exact scoped references only, avoiding 30,000 duplicate Port queries.
	cache := map[identitydomain.Reference]identityport.ResolveResult{}
	for _, row := range s.Tables[tableNames[3]].Rows {
		m := decodeFact(row)
		pid, e := integer(m, "package_id", false)
		if e != nil {
			return in, e
		}
		idx, ok := packageIndex[pid]
		if !ok {
			return in, errors.New("orphan member")
		}
		status := factString(m, "status")
		if status != "active" && status != "exited" {
			return in, errors.New("unknown member status")
		}
		member := segmentport.HistoricalMember{SourceID: row.ID, Active: status == "active", Reason: "exited"}
		// Exited facts are preserved in evidence/ledger and never put into snapshots.
		if member.Active {
			entered, e := time.Parse(time.RFC3339Nano, factString(m, "first_entered_at"))
			if e != nil {
				member.Reason = "invalid"
			} else {
				member.EnteredAt = entered
				if s.ExistingProof != nil {
					member.CustomerID, member.Reason, e = resolveProofMember(ctx, m, proofRefs, resolver, cache)
				} else {
					member.CustomerID, member.Reason, e = resolveMember(ctx, m, s.ScopeDeclaration, resolver, cache)
				}
				if e != nil {
					return in, e
				}
			}
		}
		in.Packages[idx].Members = append(in.Packages[idx].Members, member)
	}
	return in, nil
}
func decodeFact(row row) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(row.Fact, &m)
	return m
}
func factString(m map[string]json.RawMessage, key string) string {
	var v string
	_ = json.Unmarshal(m[key], &v)
	return v
}
func resolveMember(ctx context.Context, m map[string]json.RawMessage, scopes map[string]string, resolver identityport.Resolver, cache map[identitydomain.Reference]identityport.ResolveResult) (int64, string, error) {
	refs := []identitydomain.Reference{}
	union := factString(m, "unionid")
	kind := factString(m, "identity_type")
	value := factString(m, "identity_value")
	if union != "" {
		refs = append(refs, identitydomain.Reference{Kind: identitydomain.KindUnionID, Value: union})
	}
	if value != "" {
		switch kind {
		case "unionid":
			refs = append(refs, identitydomain.Reference{Kind: identitydomain.KindUnionID, Value: value})
		case "external_userid", "wecom_external_userid":
			refs = append(refs, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Value: value})
		default:
			if len(refs) == 0 {
				return 0, "invalid", nil
			}
		}
	}
	if len(refs) == 0 {
		return 0, "invalid", nil
	}
	var root int64
	missing := false
	for _, ref := range refs {
		ref.Scope = scopes[string(ref.Kind)]
		ref.Assurance = identitydomain.AssuranceDeclared
		ref.Source = "audience_history"
		if ref.Scope == "" {
			missing = true
			continue
		}
		if _, e := identitydomain.Normalize(ref); e != nil {
			return 0, "invalid", nil
		}
		result, ok := cache[ref]
		if !ok {
			var e error
			result, e = resolver.Resolve(ctx, ref)
			if e != nil {
				return 0, "", errors.New("identity resolution unavailable")
			}
			cache[ref] = result
		}
		if result.Status == identityport.ResolveConflict {
			return 0, "conflict", nil
		}
		if result.Status == identityport.ResolveFound {
			if result.CustomerID < 1 {
				return 0, "invalid", nil
			}
			if root > 0 && root != int64(result.CustomerID) {
				return 0, "conflict", nil
			}
			root = int64(result.CustomerID)
		}
	}
	// Missing scope is never inferred from a different identity field.
	if missing {
		return 0, "missing_scope", nil
	}
	if root > 0 {
		return root, "resolved", nil
	}
	return 0, "unresolved", nil
}

func resolveProofMember(ctx context.Context, m map[string]json.RawMessage, proof map[string]identitydomain.Reference, resolver identityport.Resolver, cache map[identitydomain.Reference]identityport.ResolveResult) (int64, string, error) {
	union := factString(m, "unionid")
	value := factString(m, "identity_value")
	kind := factString(m, "identity_type")
	if kind == "unionid" && value != "" {
		if union != "" && union != value {
			return 0, "conflict", nil
		}
		union = value
	}
	if union == "" {
		return 0, "unresolved", nil
	}
	ref, ok := proof[union]
	if !ok {
		return 0, "unresolved", nil
	}
	if (kind == "external_userid" || kind == "wecom_external_userid") && value != "" && value != ref.Value {
		return 0, "conflict", nil
	}
	if ref.Kind != identitydomain.KindWeComExternalUserID {
		return 0, "invalid", nil
	}
	result, ok := cache[ref]
	if !ok {
		var e error
		result, e = resolver.Resolve(ctx, ref)
		if e != nil {
			return 0, "", errors.New("existing identity resolution unavailable")
		}
		cache[ref] = result
	}
	if result.Status == identityport.ResolveConflict {
		return 0, "conflict", nil
	}
	if result.Status != identityport.ResolveFound || result.CustomerID < 1 || result.IdentityID < 1 {
		return 0, "unresolved", nil
	}
	return int64(result.CustomerID), "resolved", nil
}
