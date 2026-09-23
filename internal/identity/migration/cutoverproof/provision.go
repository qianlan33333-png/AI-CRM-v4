package cutoverproof

import (
	"encoding/hex"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

const VerifiedWecomProvision = "verified_wecom_provision"

func validSHA(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }

// ProvisionReferences accepts only the separately frozen live-provider plan.
// Resolve-only evidence can never authorize this explicit provisioning mode.
func ProvisionReferences(s Snapshot, f identityport.HistoricalFactFactory) (map[string]identitydomain.Reference, error) {
	if s.Validate() != nil || s.Version != 3 {
		return nil, ErrInvalid
	}
	copy := s
	copy.Version = 2
	copy.ResolutionMode = ExistingWecomOnly
	copy.ParentProofSHA = ""
	copy.CandidateSHA = ""
	copy.Verification = nil
	refs, _, e := ExistingWecomReferences(copy, f)
	if e != nil {
		return nil, e
	}
	for key := range refs {
		if s.Verification[key] != "verified" {
			delete(refs, key)
		}
	}
	return refs, nil
}
