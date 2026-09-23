package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigrationKeepsIdentityAndProviderDataOutsideOwnedTables(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	var schema string
	for _, name := range []string{"0036_ai_assistant_review.sql", "0037_outbound_private_messages.sql", "0100_ai_assistant_machine_actor.sql"} {
		payload, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		schema += "\n" + strings.ToLower(string(payload))
	}
	for _, forbidden := range []string{"external_userid", "openid", "unionid", "phone", "mobile", "sender_userid", "provider_payload"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("AI Assistant migration contains forbidden identity/provider field %q", forbidden)
		}
	}
	for _, required := range []string{"customer_id bigint not null", "staff_id bigint not null", "foreign key (current_content_version_id, id)", "foreign key (outbound_intent_id) references outbound_private_message_intents(id)", "created_actor_ref text", "actor_ref text"} {
		if !strings.Contains(schema, required) {
			t.Fatalf("AI Assistant migration missing invariant %q", required)
		}
	}
}
