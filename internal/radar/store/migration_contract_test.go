package store_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRadarMigrationsOwnCompleteSchemaWithoutRawIdentityColumns(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	files := []string{"migrations/0050_radar_core.sql", "migrations/0051_radar_sessions_events.sql", "migrations/0052_radar_legacy_import.sql"}
	var combined strings.Builder
	for _, name := range files {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		combined.Write(contents)
	}
	sql := strings.ToLower(combined.String())
	for _, table := range []string{"radar_links", "radar_link_versions", "radar_operation_receipts", "radar_audit_events", "radar_outbox", "radar_oauth_states", "radar_view_sessions", "radar_events", "radar_migration_batches", "radar_migration_source_map", "radar_migration_quarantine", "radar_legacy_events"} {
		if !strings.Contains(sql, "create table "+table) {
			t.Fatalf("missing owned table %s", table)
		}
	}
	for _, forbidden := range []string{" unionid ", " openid ", " external_userid ", " phone ", " oauth_code ", " access_token ", " user_agent ", " ip_address "} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration contains forbidden raw identity column token %q", forbidden)
		}
	}
}
