package channel

import (
	"os"
	"strings"
	"testing"
)

func TestChannelCenterMigrationContract(t *testing.T) {
	contents, err := os.ReadFile("../../migrations/0029_channel_center.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, required := range []string{
		"Owner: internal/channel", "Forward-only", "CREATE TABLE channels", "CREATE TABLE channel_config_versions",
		"CREATE TABLE channel_assignees", "CREATE TABLE channel_operation_receipts", "channels_code_unique_ci",
		"channels_current_config_fk", "channel_config_versions_immutable", "channel_assignees_immutable",
		"channel_operation_receipts_guard", "channels are archive-only", "audit_events", "outbox_events",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{" external_userid ", " unionid ", " openid ", " phone ", " raw_state ", " welcome_code "} {
		if strings.Contains(strings.ToLower(source), forbidden) {
			t.Fatalf("migration contains forbidden identity/provider field %q", forbidden)
		}
	}
}

func TestChannelRuntimeAndHistoryMigrationContracts(t *testing.T) {
	files := []string{
		"../../migrations/0031_channel_history_import.sql",
		"../../migrations/0032_channel_acquisition_assets.sql",
		"../../migrations/0033_wecom_welcome_grants.sql",
		"../../migrations/0034_channel_entrant_actions.sql",
		"../../migrations/0035_channel_acquisition_links.sql",
	}
	combined := ""
	for _, path := range files {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		combined += "\n" + string(contents)
	}
	for _, required := range []string{
		"channel_history_source_maps", "channel_history_contacts", "channel_acquisition_assets",
		"channel_asset_reconciliation_receipts", "wecom_welcome_grants", "channel_entrant_assignments",
		"channel_entrant_actions", "channel_acquisition_link_receipts", "channel_acquisition_link_reconciliations",
		"channel_acquisition_asset", "channel_welcome_message", "channel_entry_tag", "channel_acquisition_link_mutation",
	} {
		if !strings.Contains(combined, required) {
			t.Fatalf("channel migrations missing %q", required)
		}
	}
	for _, forbidden := range []string{" external_userid text", " raw_state text", " welcome_code text", " phone text"} {
		if strings.Contains(strings.ToLower(combined), forbidden) {
			t.Fatalf("channel migrations persist forbidden boundary field %q", forbidden)
		}
	}
}

func TestLegacyAssetRetirementMigrationContract(t *testing.T) {
	contents, err := os.ReadFile("../../migrations/0065_channel_legacy_asset_retirement.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, required := range []string{
		"Owner: internal/channel", "Forward-only", "DROP CONSTRAINT channel_legacy_acquisition_assets_check",
		"ADD CONSTRAINT channel_legacy_acquisition_assets_check", "provider_asset_ref <> ''",
		"result_url <> ''", "verified_at IS NOT NULL",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("legacy retirement migration missing %q", required)
		}
	}
	if strings.Contains(source, "retired_at IS NULL") {
		t.Fatal("verified legacy assets must remain valid audit facts after retirement")
	}
	if strings.Contains(source, "legacy_retired") {
		t.Fatal("retirement must not overwrite provider verification status")
	}
}

func TestChannelWelcomeIntentMigrationContract(t *testing.T) {
	contents, err := os.ReadFile("../../migrations/0066_channel_welcome_intents.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, required := range []string{
		"Owner: internal/channel", "channel_welcome_intents", "send_deadline_at=first_received_at+interval '20 seconds'",
		"welcome_not_configured", "welcome_material_unavailable", "channel_welcome_intent_guard",
		"result_reason", "deadline_missing", "deadline_expired", "grant_expired",
		"external_effect_jobs_queue_check", "outbound_welcome",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("welcome migration missing %q", required)
		}
	}
	for _, forbidden := range []string{" external_userid ", " raw_state ", " welcome_code text", " phone text"} {
		if strings.Contains(strings.ToLower(source), forbidden) {
			t.Fatalf("welcome migration persists forbidden field %q", forbidden)
		}
	}
}

func TestChannelWelcomeMessageSnapshotMigrationContract(t *testing.T) {
	contents, err := os.ReadFile("../../migrations/0150_channel_welcome_message_snapshots.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, required := range []string{
		"Owner: internal/channel", "Forward-only", "channel_welcome_message_snapshots",
		"template_digest", "rendered_message_digest", "ciphertext", "envelope_fingerprint",
		"channel_welcome_message_snapshot_guard", "effect_envelope_fingerprint",
		"customer_name_unavailable", "welcome_template_invalid", "welcome_message_too_long", "frozen_message_unavailable",
		"num_nonnulls(effect_target_digest,effect_payload_digest,effect_policy_digest,effect_envelope_fingerprint) IN (0,4)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("welcome snapshot migration missing %q", required)
		}
	}
	for _, forbidden := range []string{" external_userid ", " welcome_code ", " display_name text", " phone text"} {
		if strings.Contains(strings.ToLower(source), forbidden) {
			t.Fatalf("welcome snapshot migration persists forbidden field %q", forbidden)
		}
	}
}
