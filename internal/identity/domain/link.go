package domain

import "errors"

type EvidenceStrength string

const (
	EvidenceStrong EvidenceStrength = "strong"
	EvidenceMedium EvidenceStrength = "medium"
	EvidenceWeak   EvidenceStrength = "weak"
)

var ErrInvalidEvidence = errors.New("invalid identity link evidence")

// LinkEvidence is deliberately digest-only. Full external identifiers,
// callback payloads and tokens are not evidence metadata and must not be
// copied into audit records or ordinary logs.
type LinkEvidence struct {
	Type          string
	Strength      EvidenceStrength
	Source        string
	EventID       string
	Digest        string
	PolicyVersion string
}

func (evidence LinkEvidence) Valid() bool {
	if evidence.Type == "" || evidence.Source == "" || evidence.Digest == "" || evidence.PolicyVersion == "" {
		return false
	}
	switch evidence.Strength {
	case EvidenceStrong, EvidenceMedium, EvidenceWeak:
		return true
	default:
		return false
	}
}
