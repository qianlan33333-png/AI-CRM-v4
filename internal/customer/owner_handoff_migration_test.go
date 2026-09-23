package customer

import (
	"os"
	"strings"
	"testing"
)

func TestOwnerHandoffMigrationKeepsLocalAndWeComFactsSeparate(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0092_customer_owner_handoff.sql")
	if err != nil {
		t.Fatal(err)
	}
	value := string(raw)
	for _, required := range []string{
		"CREATE TABLE customer_local_owners",
		"CREATE TABLE customer_owner_handoff_previews",
		"CREATE TABLE customer_owner_handoff_lines",
		"CREATE TABLE customer_owner_handoff_effects",
		"CREATE TABLE customer_owner_handoff_effect_lines",
		"owner_handoff_local_only",
		"owner_handoff_wecom_then_crm",
		"external_identity_digest",
		"customer_owner_handoff_history_imports",
		"transfer_result_cursor_ciphertext",
		"transfer_status INTEGER",
		"customer_tag_command",
		"commerce_product_push",
	} {
		if !strings.Contains(value, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	if strings.Contains(value, "wecom_customer_owner_observations") || strings.Contains(value, "wecom_customer_profile_primary_owners") {
		t.Fatal("owner handoff must not write WeCom observations")
	}
	if strings.Contains(value, "UNIQUE (effect_id)") {
		t.Fatal("one bounded provider effect must be able to map its frozen lines")
	}
}
