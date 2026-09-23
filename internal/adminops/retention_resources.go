package adminops

import (
	"bytes"
	_ "embed"
	"encoding/json"
)

// Generated solely from the checked canonical registry. No runtime filesystem
// read, resource scan, Owner query or executable policy is derived from it.
//
//go:embed retention_resources.generated.json
var retentionResourcesJSON []byte

type RetentionResource struct {
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	Owner               string `json:"owner"`
	Policy              string `json:"policy"`
	Reason              string `json:"reason"`
	Source              string `json:"source"`
	CleanupEntrypoint   string `json:"cleanup_entrypoint"`
	PolicyID            string `json:"policy_id"`
	CoverageStatus      string `json:"coverage_status"`
	GapCode             string `json:"gap_code"`
	AuthorizationExpiry string `json:"authorization_expiry"`
}

type RetentionCoverageSummary struct {
	RegisteredTables             int `json:"registered_tables"`
	RegisteredResources          int `json:"registered_resources"`
	RegisteredFilesystemPrefixes int `json:"registered_filesystem_prefixes"`
	GapResources                 int `json:"gap_resources"`
	ProtectedResources           int `json:"protected_resources"`
	OwnerManagedResources        int `json:"owner_managed_resources"`
	ScheduledResources           int `json:"scheduled_resources"`
	DisabledResources            int `json:"disabled_resources"`
	UnboundResources             int `json:"unbound_resources"`
	UnobservedResources          int `json:"unobserved_resources"`
	SecurityTTLResources         int `json:"security_ttl_resources"`
	MixedPayloadResources        int `json:"mixed_payload_resources"`
	UnclassifiedResources        int `json:"unclassified_resources"`
	CoordinationResources        int `json:"coordination_resources"`
}

type RetentionCoverage struct {
	RegistryVersion         int                      `json:"registry_version"`
	RegistrySHA256          string                   `json:"registry_sha256"`
	InventoryScope          string                   `json:"inventory_scope"`
	AutomaticCleanupEnabled bool                     `json:"automatic_cleanup_enabled"`
	AllowlistPolicyCount    int                      `json:"allowlist_policy_count"`
	Summary                 RetentionCoverageSummary `json:"summary"`
	Items                   []RetentionResource      `json:"items"`
}

// Resources separates inventory coverage from the health of the configured
// execution allowlist. "Scheduled" means bound and enabled, never a successful
// deletion claim. Native/host execution needs its separate observation evidence.
func (s *RetentionService) Resources() (RetentionCoverage, error) {
	var out RetentionCoverage
	decoder := json.NewDecoder(bytes.NewReader(retentionResourcesJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil || out.RegistryVersion != 1 || len(out.RegistrySHA256) != 64 || out.InventoryScope != "committed_registry" || len(out.Items) == 0 {
		return RetentionCoverage{}, ErrInspectionInvalid
	}
	out.AutomaticCleanupEnabled = s.enabled
	bound := map[string]bool{}
	for _, policy := range s.Policies() {
		if validRetentionPolicy(policy.ID) {
			bound[policy.ID] = true
		}
	}
	out.AllowlistPolicyCount = len(bound)
	for i := range out.Items {
		item := &out.Items[i]
		switch item.Kind {
		case "table":
			out.Summary.RegisteredTables++
		case "resource":
			out.Summary.RegisteredResources++
		case "filesystem_prefix":
			out.Summary.RegisteredFilesystemPrefixes++
		default:
			return RetentionCoverage{}, ErrInspectionInvalid
		}
		switch item.Policy {
		case "security_ttl":
			out.Summary.SecurityTTLResources++
		case "protected_mixed_payload":
			out.Summary.MixedPayloadResources++
		case "protected_unclassified":
			out.Summary.UnclassifiedResources++
		case "runtime_coordination":
			out.Summary.CoordinationResources++
		}
		switch item.CoverageStatus {
		case "policy_available":
			if !validRetentionPolicy(item.PolicyID) {
				return RetentionCoverage{}, ErrInspectionInvalid
			}
			switch {
			case !bound[item.PolicyID]:
				item.CoverageStatus, item.GapCode = "owner_not_bound", "owner_port_not_bound"
				out.Summary.UnboundResources++
				out.Summary.GapResources++
			case !s.enabled:
				item.CoverageStatus = "disabled"
				out.Summary.DisabledResources++
			default:
				item.CoverageStatus = "scheduled"
				out.Summary.ScheduledResources++
			}
		case "host_unobserved":
			if (item.PolicyID == "host_process_files" && s.host == nil) || (item.PolicyID == "release_artifacts" && s.release == nil) {
				item.CoverageStatus, item.GapCode = "owner_not_bound", "host_evidence_reader_not_bound"
				out.Summary.UnboundResources++
				out.Summary.GapResources++
			} else {
				out.Summary.UnobservedResources++
			}
		case "native_unobserved":
			out.Summary.UnobservedResources++
		case "gap":
			out.Summary.GapResources++
		case "protected":
			out.Summary.ProtectedResources++
		case "owner_managed":
			out.Summary.OwnerManagedResources++
		default:
			return RetentionCoverage{}, ErrInspectionInvalid
		}
	}
	return out, nil
}

func (s *RetentionService) coverageMetrics() (map[string]int64, error) {
	coverage, err := s.Resources()
	if err != nil {
		return nil, err
	}
	v := coverage.Summary
	return map[string]int64{
		"coverage_tables":              int64(v.RegisteredTables),
		"coverage_resources":           int64(v.RegisteredResources),
		"coverage_filesystem_prefixes": int64(v.RegisteredFilesystemPrefixes),
		"coverage_gaps":                int64(v.GapResources),
		"coverage_security_ttl":        int64(v.SecurityTTLResources),
		"coverage_mixed_payload":       int64(v.MixedPayloadResources),
		"coverage_unclassified":        int64(v.UnclassifiedResources),
		"coverage_coordination":        int64(v.CoordinationResources),
		"coverage_owner_managed":       int64(v.OwnerManagedResources),
		"coverage_disabled":            int64(v.DisabledResources),
		"coverage_unbound":             int64(v.UnboundResources),
		"coverage_unobserved":          int64(v.UnobservedResources),
	}, nil
}
