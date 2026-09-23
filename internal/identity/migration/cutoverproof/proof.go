// Package cutoverproof is an offline migration adapter, not a runtime source.
package cutoverproof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

var ErrInvalid = errors.New("invalid protected identity proof")

const Provenance = "provider-history:cutover-wecom-directory-v1"

type Scopes struct {
	CorpID     string `json:"corp_id"`
	UnionScope string `json:"union_scope"`
}

func (s Scopes) Validate() error {
	if s.CorpID == "" || strings.TrimSpace(s.CorpID) != s.CorpID || !strings.HasPrefix(s.UnionScope, "wechat-open-platform:") {
		return ErrInvalid
	}
	if identitydomain.ValidateNamespace(identitydomain.KindUnionID, s.UnionScope) != nil || identitydomain.ValidateNamespace(identitydomain.KindWeComExternalUserID, "wecom-corp:"+s.CorpID) != nil {
		return ErrInvalid
	}
	return nil
}

// Row retains only protected raw identity proof, never phone, name or profile.
// The source adapter may set MapVerified only when the provider raw profile's
// external_contact userid and unionid exactly match this directory relation.
type Row struct {
	UnionID           string     `json:"unionid"`
	CRMStatus         string     `json:"crm_status"`
	PrimaryExternalID string     `json:"primary_external_id"`
	Evidence          []Evidence `json:"evidence"`
}
type Evidence struct {
	MapID         int64  `json:"map_id"`
	ProviderOK    bool   `json:"provider_ok"`
	CorpID        string `json:"corp_id"`
	ExternalID    string `json:"external_id"`
	UnionID       string `json:"unionid"`
	Status        string `json:"status"`
	RawUnionID    string `json:"raw_unionid"`
	RawExternalID string `json:"raw_external_id"`
}
type Snapshot struct {
	Version        int               `json:"version"`
	ResolutionMode string            `json:"resolution_mode,omitempty"`
	Scopes         Scopes            `json:"scopes"`
	CapturedAt     time.Time         `json:"captured_at"`
	Rows           []Row             `json:"rows"`
	ParentProofSHA string            `json:"parent_proof_sha256,omitempty"`
	CandidateSHA   string            `json:"candidate_sha256,omitempty"`
	Verification   map[string]string `json:"verification,omitempty"`
}

func (s Snapshot) Validate() error {
	validScope := s.Version == 1 && s.ResolutionMode == "" && s.Scopes.Validate() == nil
	if s.Version == 2 && s.ResolutionMode == ExistingWecomOnly && s.Scopes.UnionScope == "" && validateCorp(s.Scopes.CorpID) == nil {
		validScope = true
	}
	if s.Version == 3 && s.ResolutionMode == VerifiedWecomProvision && s.Scopes.UnionScope == "" && validateCorp(s.Scopes.CorpID) == nil && validSHA(s.ParentProofSHA) && validSHA(s.CandidateSHA) && len(s.Verification) == len(s.Rows) {
		validScope = true
	}
	if s.Version != 3 && (s.ParentProofSHA != "" || s.CandidateSHA != "" || s.Verification != nil) {
		return ErrInvalid
	}
	if !validScope || s.CapturedAt.IsZero() || s.Rows == nil {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range s.Rows {
		if r.UnionID == "" || seen[r.UnionID] {
			return ErrInvalid
		}
		seen[r.UnionID] = true
		if s.Version == 3 && s.Verification[r.UnionID] != "verified" && s.Verification[r.UnionID] != "provider_failed" && s.Verification[r.UnionID] != "provider_mismatch" && s.Verification[r.UnionID] != "already_found" && s.Verification[r.UnionID] != "target_conflict" {
			return ErrInvalid
		}
	}
	return nil
}
func (s Snapshot) Digest() ([32]byte, error) {
	if s.Validate() != nil {
		return [32]byte{}, ErrInvalid
	}
	b, e := json.Marshal(s)
	return sha256.Sum256(b), e
}
func Key(r Row, scopes Scopes) string {
	d := sha256.Sum256([]byte(scopes.UnionScope + "\x00" + r.UnionID))
	return "cutover-proof:" + hex.EncodeToString(d[:])
}
func RowDigest(r Row, scopes Scopes) [32]byte {
	b, _ := json.Marshal(struct {
		Row    Row
		Scopes Scopes
	}{r, scopes})
	return sha256.Sum256(b)
}

type PlanRow struct {
	SourceKey string                        `json:"source_key"`
	State     string                        `json:"state"`
	Facts     []identitydomain.VerifiedFact `json:"-"`
}
type Plan struct {
	Counts map[string]int `json:"counts"`
	Rows   []PlanRow      `json:"rows"`
}

func BuildPlan(ctx context.Context, s Snapshot, resolver identityport.Resolver, factory identityport.HistoricalFactFactory) (Plan, error) {
	out := Plan{Counts: map[string]int{}, Rows: []PlanRow{}}
	if s.Validate() != nil || s.Version != 1 || resolver == nil || factory == nil {
		return out, ErrInvalid
	}
	// Shared primary external identities across different source subjects are
	// not unique evidence; never attach both unionids to the same root.
	externalOwners := map[string]int{}
	for _, r := range s.Rows {
		if r.PrimaryExternalID != "" {
			externalOwners[r.PrimaryExternalID]++
		}
	}
	for _, r := range s.Rows {
		item := PlanRow{SourceKey: Key(r, s.Scopes)}
		if r.CRMStatus != "active" {
			item.State = "quarantine_crm_inactive"
		} else if r.PrimaryExternalID == "" || len(r.Evidence) == 0 {
			item.State = "quarantine_missing_provider_relation"
		} else if externalOwners[r.PrimaryExternalID] != 1 {
			item.State = "quarantine_ambiguous_source_subject"
		} else {
			verified := true
			for _, e := range r.Evidence {
				if !e.ProviderOK || e.MapID < 1 || e.CorpID != s.Scopes.CorpID || e.ExternalID != r.PrimaryExternalID || e.UnionID != r.UnionID || e.Status != "active" || e.RawUnionID != r.UnionID || e.RawExternalID != r.PrimaryExternalID {
					verified = false
				}
			}
			if !verified {
				item.State = "quarantine_unverified_provider_relation"
			} else {
				inputs := []identityport.HistoricalVerifiedInput{{Kind: string(identitydomain.KindWeComExternalUserID), Scope: "wecom-corp:" + s.Scopes.CorpID, Value: r.PrimaryExternalID, Source: Provenance}, {Kind: string(identitydomain.KindUnionID), Scope: s.Scopes.UnionScope, Value: r.UnionID, Source: Provenance}}
				roots := map[int64]bool{}
				foundIndex := -1
				foundCount := 0
				for i, input := range inputs {
					fact, e := factory.VerifiedHistoricalFact(input)
					if e != nil {
						return out, ErrInvalid
					}
					item.Facts = append(item.Facts, fact)
					ref := fact.Reference()
					match, e := resolver.Resolve(ctx, identitydomain.Reference{Kind: ref.Kind, Scope: ref.Scope, Value: ref.NormalizedValue, Assurance: ref.Assurance, Source: ref.Source})
					if e != nil {
						return out, errors.New("identity proof resolve failed")
					}
					switch match.Status {
					case identityport.ResolveFound:
						roots[int64(match.CustomerID)] = true
						foundIndex = i
						foundCount++
					case identityport.ResolveNotFound:
					default:
						item.State = "quarantine_target_conflict"
					}
				}
				if len(roots) > 1 {
					item.State = "quarantine_cross_root"
				}
				if item.State == "" {
					if foundCount == len(inputs) {
						item.State = "already_linked"
					} else if foundIndex >= 0 {
						item.State = "candidate_attach"
						item.Facts[0], item.Facts[foundIndex] = item.Facts[foundIndex], item.Facts[0]
					} else {
						item.State = "candidate_provision"
					}
				}
			}
		}
		if strings.HasPrefix(item.State, "quarantine_") {
			item.Facts = nil
		}
		out.Counts[item.State]++
		out.Rows = append(out.Rows, item)
	}
	return out, nil
}
