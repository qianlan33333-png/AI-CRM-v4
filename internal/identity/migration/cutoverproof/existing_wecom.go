package cutoverproof

import (
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"strings"
)

const ExistingWecomOnly = "existing_wecom_only"

func validateCorp(corp string) error {
	if corp == "" || strings.TrimSpace(corp) != corp || identitydomain.ValidateNamespace(identitydomain.KindWeComExternalUserID, "wecom-corp:"+corp) != nil {
		return ErrInvalid
	}
	return nil
}

// ExistingWecomReferences uses the source UnionID only as an opaque lookup key.
// It constructs no UnionID fact and performs no provisioning or identity linking.
// Callers must bind the protected snapshot digest and confirm the matched Corp,
// then call Identity.Resolve and accept only an already existing customer.
func ExistingWecomReferences(s Snapshot, factory identityport.HistoricalFactFactory) (map[string]identitydomain.Reference, map[string]int, error) {
	refs := map[string]identitydomain.Reference{}
	counts := map[string]int{}
	if s.Validate() != nil || s.Version != 2 || factory == nil {
		return refs, counts, ErrInvalid
	}
	owners := map[string]int{}
	for _, r := range s.Rows {
		if r.PrimaryExternalID != "" {
			owners[r.PrimaryExternalID]++
		}
	}
	for _, r := range s.Rows {
		reason := "ready"
		switch {
		case r.CRMStatus != "active":
			reason = "quarantine_crm_inactive"
		case r.PrimaryExternalID == "" || len(r.Evidence) == 0:
			reason = "quarantine_missing_provider_relation"
		case owners[r.PrimaryExternalID] != 1:
			reason = "quarantine_ambiguous_source_subject"
		default:
			for _, e := range r.Evidence {
				if !e.ProviderOK || e.MapID < 1 || e.CorpID != s.Scopes.CorpID || e.ExternalID != r.PrimaryExternalID || e.UnionID != r.UnionID || e.Status != "active" || e.RawUnionID != r.UnionID || e.RawExternalID != r.PrimaryExternalID {
					reason = "quarantine_unverified_provider_relation"
				}
			}
		}
		if reason == "ready" {
			f, e := factory.VerifiedHistoricalFact(identityport.HistoricalVerifiedInput{Kind: string(identitydomain.KindWeComExternalUserID), Scope: "wecom-corp:" + s.Scopes.CorpID, Value: r.PrimaryExternalID, Source: Provenance})
			if e != nil {
				return nil, nil, ErrInvalid
			}
			ref := f.Reference()
			refs[r.UnionID] = identitydomain.Reference{Kind: ref.Kind, Scope: ref.Scope, Value: ref.NormalizedValue, Assurance: ref.Assurance, Source: ref.Source}
		}
		counts[reason]++
	}
	return refs, counts, nil
}
