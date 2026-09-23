package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	cutoverproof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	"os"
	"time"
)

// The derivation preserves every original source row and capture time. Its
// identity evidence has an independent digest/time and cannot rewrite source
// scope declarations or upgrade any OneID fact.
type existingProofEnvelope struct {
	ParentSHA256      string                `json:"parent_sha256,omitempty"`
	ProofSourceSHA256 string                `json:"proof_source_sha256"`
	SourceSHA256      string                `json:"source_sha256"`
	ProofSHA256       string                `json:"proof_sha256"`
	DerivedAt         time.Time             `json:"derived_at"`
	Proof             cutoverproof.Snapshot `json:"proof"`
}

func validateExistingProof(s snapshot) error {
	p := s.ExistingProof
	if p == nil {
		return nil
	}
	if d, e := hex.DecodeString(p.ProofSourceSHA256); e != nil || len(d) != 32 {
		return errors.New("proof source digest required")
	}
	if p.ParentSHA256 != "" {
		d, e := hex.DecodeString(p.ParentSHA256)
		if e != nil || len(d) != 32 {
			return errors.New("invalid predecessor digest")
		}
	}
	if p.Proof.Version != 2 || p.Proof.ResolutionMode != cutoverproof.ExistingWecomOnly || p.Proof.Scopes.UnionScope != "" {
		return errors.New("existing-only proof required")
	}
	if p.DerivedAt.IsZero() || p.DerivedAt.Before(s.CapturedAt) || p.DerivedAt.Before(p.Proof.CapturedAt) {
		return errors.New("invalid proof derivation time")
	}
	original := s
	original.ExistingProof = nil
	b, e := json.Marshal(original)
	if e != nil || sum(b) != p.SourceSHA256 {
		return errors.New("derived source digest mismatch")
	}
	d, e := p.Proof.Digest()
	if e != nil || hex.EncodeToString(d[:]) != p.ProofSHA256 {
		return errors.New("derived identity proof digest mismatch")
	}
	// The existing-only proof API additionally enforces version/mode and absence
	// of any asserted Open Platform namespace before exposing references.
	return nil
}
func deriveExisting(s snapshot, digest, path, keyPath, out, proofSource, predecessor string, key []byte) error {
	if s.ExistingProof != nil || out == "" {
		return errors.New("new derivation output required")
	}
	proof, d, e := cutoverproof.Load(path, keyPath)
	if e != nil {
		return e
	}
	s.ExistingProof = &existingProofEnvelope{ProofSourceSHA256: proofSource, SourceSHA256: digest, ProofSHA256: hex.EncodeToString(d[:]), DerivedAt: time.Now().UTC(), Proof: proof}
	if predecessor != "" {
		sealed, e := readPrivate(predecessor, 512<<20)
		if e != nil {
			return e
		}
		plain, e := open(sealed, key)
		if e != nil {
			return e
		}
		var prior snapshot
		if json.Unmarshal(plain, &prior) != nil || validate(prior) != nil || prior.ExistingProof == nil || prior.ExistingProof.SourceSHA256 != digest {
			return errors.New("predecessor source proof mismatch")
		}
		canonical, _ := json.Marshal(prior)
		s.ExistingProof.ParentSHA256 = sum(canonical)
	}
	if e = validate(s); e != nil {
		return e
	}
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	sealed, e := seal(raw, key)
	if e != nil {
		return e
	}
	if e = writeExclusive(out, sealed); e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "derive-existing-wecom", "sha256": sum(raw), "source_sha256": digest, "proof_sha256": hex.EncodeToString(d[:]), "source_rows_preserved": true, "identities_created": 0, "provider_calls": 0})
}
