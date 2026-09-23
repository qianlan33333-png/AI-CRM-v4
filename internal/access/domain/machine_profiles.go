package domain

// SystemMachineProfile freezes the external-integration service profiles that
// existed in the dd8d60d auth platform. They are registry/import facts, not
// administrator-selectable UI templates: the compatibility page remains
// limited to external_api and mcp.
type SystemMachineProfile struct {
	Purpose      string
	Audiences    []string
	Scopes       []string
	Capabilities []string
}

var regularMachineProfiles = map[string]SystemMachineProfile{
	"external_agent": {
		Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"external_read", "external_write"},
	},
	"mcp": {
		Purpose: "mcp", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"mcp_execute", "mcp_read"},
	},
	"direct_api_key": {
		Purpose: "direct_api_key", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"external_read"},
	},
}

var systemMachineProfiles = map[string]SystemMachineProfile{
	"identity": {
		Purpose: "identity", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"identity_resolve"},
	},
	"group_broadcast": {
		Purpose: "group_broadcast", Audiences: []string{"external_integration"}, Scopes: []string{"write"}, Capabilities: []string{"group_broadcast_execute"},
	},
	"campaign_agent": {
		Purpose: "campaign_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{
			"campaign_draft_create", "campaign_preparation_commit", "campaign_preparation_create", "campaign_preparation_read", "campaign_status_read",
			"customer_read_limited", "customer_resolve_read", "material_create", "material_read", "operation_cycle_context_read", "operation_cycle_strategy_propose",
		},
	},
	"ops_reporter": {
		Purpose: "ops_reporter", Audiences: []string{"external_integration"}, Scopes: []string{"write"}, Capabilities: []string{"operation_cycle_report_write"},
	},
	"operation_runner": {
		Purpose: "operation_runner", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"operation_cycle_action_claim", "operation_cycle_action_event_write", "operation_cycle_runner_heartbeat"},
	},
}

// MachineProfileForPurpose returns a copy of the frozen external-integration
// profile. It deliberately excludes donor internal_worker profiles, whose
// workers are not machine HTTP callers and whose routes must never be opened
// through this platform.
func MachineProfileForPurpose(purpose string) (SystemMachineProfile, bool) {
	profile, ok := regularMachineProfiles[purpose]
	if !ok {
		profile, ok = systemMachineProfiles[purpose]
	}
	if !ok {
		return SystemMachineProfile{}, false
	}
	profile.Audiences = append([]string(nil), profile.Audiences...)
	profile.Scopes = append([]string(nil), profile.Scopes...)
	profile.Capabilities = append([]string(nil), profile.Capabilities...)
	return profile, true
}

// SystemMachineProfileForPurpose distinguishes non-page-managed profiles for
// legacy import and system registration callers.
func SystemMachineProfileForPurpose(purpose string) (SystemMachineProfile, bool) {
	if _, regular := regularMachineProfiles[purpose]; regular {
		return SystemMachineProfile{}, false
	}
	return MachineProfileForPurpose(purpose)
}

func IsMachinePurpose(purpose string) bool {
	_, ok := MachineProfileForPurpose(purpose)
	return ok
}

// IsLegacyAdminManagedMachinePurpose is the narrow old management-page set.
// Direct keys use their own fixed page action; only external_api and mcp are
// regular API-client templates.
func IsLegacyAdminManagedMachinePurpose(purpose string) bool {
	return purpose == "external_agent" || purpose == "mcp"
}
