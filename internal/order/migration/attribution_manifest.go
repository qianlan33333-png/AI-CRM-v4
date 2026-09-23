package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

const AttributionSchemaVersion = "aicrm-order-history-attribution-v1"

var ErrInvalidAttributionManifest = errors.New("invalid order history attribution manifest")

type AttributionEvidenceState string

const (
	AttributionCandidate               AttributionEvidenceState = "candidate"
	AttributionSourceIdentityMissing   AttributionEvidenceState = "source_identity_missing"
	AttributionSourceIdentityNotFound  AttributionEvidenceState = "source_identity_not_found"
	AttributionSourceExternalAmbiguous AttributionEvidenceState = "source_external_identity_ambiguous"
)

type AttributionRow struct {
	SourceKey       string                   `json:"source_key"`
	MerchantOrderNo string                   `json:"merchant_order_no"`
	ExternalUserID  string                   `json:"external_userid"`
	EvidenceState   AttributionEvidenceState `json:"evidence_state"`
	EvidenceDigest  string                   `json:"evidence_digest"`
}

type AttributionManifest struct {
	SchemaVersion string            `json:"schema_version"`
	RunKey        string            `json:"run_key"`
	SnapshotAt    time.Time         `json:"snapshot_at"`
	SourceSystem  string            `json:"source_system"`
	IdentityKind  string            `json:"identity_kind"`
	Rows          []AttributionRow  `json:"rows"`
	Digest        [sha256.Size]byte `json:"-"`
}

type AttributionSummary struct {
	Rows                    int `json:"rows"`
	Candidates              int `json:"candidates"`
	SourceIdentityMissing   int `json:"source_identity_missing"`
	SourceIdentityNotFound  int `json:"source_identity_not_found"`
	SourceIdentityAmbiguous int `json:"source_external_identity_ambiguous"`
}

func LoadAttribution(path string) (AttributionManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return AttributionManifest{}, err
	}
	return ParseAttribution(raw)
}

func ParseAttribution(raw []byte) (AttributionManifest, error) {
	var manifest AttributionManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return AttributionManifest{}, ErrInvalidAttributionManifest
	}
	manifest.Digest = sha256.Sum256(raw)
	if err := manifest.Validate(); err != nil {
		return AttributionManifest{}, err
	}
	return manifest, nil
}

func (manifest AttributionManifest) Validate() error {
	if manifest.SchemaVersion != AttributionSchemaVersion || !valid(manifest.RunKey, 200) || manifest.SnapshotAt.IsZero() || manifest.SourceSystem != "aicrm-production" || manifest.IdentityKind != "wecom_external_userid" || len(manifest.Rows) == 0 || len(manifest.Rows) > 1_000_000 {
		return ErrInvalidAttributionManifest
	}
	sources := make(map[string]struct{}, len(manifest.Rows))
	orders := make(map[string]struct{}, len(manifest.Rows))
	for _, row := range manifest.Rows {
		if !valid(row.SourceKey, 200) || !valid(row.MerchantOrderNo, 200) || !sha256Evidence.MatchString(row.EvidenceDigest) || len(row.ExternalUserID) > 1024 || strings.TrimSpace(row.ExternalUserID) != row.ExternalUserID || strings.ContainsAny(row.ExternalUserID, "\r\n\t") {
			return ErrInvalidAttributionManifest
		}
		if _, exists := sources[row.SourceKey]; exists {
			return ErrInvalidAttributionManifest
		}
		if _, exists := orders[row.MerchantOrderNo]; exists {
			return ErrInvalidAttributionManifest
		}
		sources[row.SourceKey] = struct{}{}
		orders[row.MerchantOrderNo] = struct{}{}
		switch row.EvidenceState {
		case AttributionCandidate:
			if row.ExternalUserID == "" {
				return ErrInvalidAttributionManifest
			}
		case AttributionSourceIdentityMissing, AttributionSourceIdentityNotFound, AttributionSourceExternalAmbiguous:
			if row.ExternalUserID != "" {
				return ErrInvalidAttributionManifest
			}
		default:
			return ErrInvalidAttributionManifest
		}
	}
	return nil
}

func (manifest AttributionManifest) DigestHex() string { return hex.EncodeToString(manifest.Digest[:]) }

func (manifest AttributionManifest) Summary() AttributionSummary {
	result := AttributionSummary{Rows: len(manifest.Rows)}
	for _, row := range manifest.Rows {
		switch row.EvidenceState {
		case AttributionCandidate:
			result.Candidates++
		case AttributionSourceIdentityMissing:
			result.SourceIdentityMissing++
		case AttributionSourceIdentityNotFound:
			result.SourceIdentityNotFound++
		case AttributionSourceExternalAmbiguous:
			result.SourceIdentityAmbiguous++
		}
	}
	return result
}

func (row AttributionRow) Digest() ([sha256.Size]byte, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(row.EvidenceDigest, "sha256:"))
	if err != nil || len(raw) != sha256.Size {
		return [sha256.Size]byte{}, ErrInvalidAttributionManifest
	}
	var result [sha256.Size]byte
	copy(result[:], raw)
	return result, nil
}
