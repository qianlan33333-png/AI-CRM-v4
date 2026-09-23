package segment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLReadinessRequiresCurrentSegmentSchema(t *testing.T) {
	ctx := context.Background()
	native, apply, cleanup := segmentReadinessDatabase(t, ctx)
	defer cleanup()

	module := NewModuleRegistration()
	if err := module.Readiness(ctx, native); err == nil {
		t.Fatal("readiness succeeded before refresh-mode schema was installed")
	}
	apply("0083_segment_audience_refresh_modes.sql")
	apply("0085_segment_audience_refresh_kind.sql")
	if err := module.Readiness(ctx, native); err == nil {
		t.Fatal("readiness succeeded before mutation actor schema was installed")
	}
	apply("0097_segment_audience_mutation_actor.sql")
	if err := module.Readiness(ctx, native); err == nil {
		t.Fatal("readiness succeeded before core operations schema")
	}
	apply("0183_segment_core_operations.sql")
	if err := module.Readiness(ctx, native); err != nil {
		t.Fatalf("readiness after refresh-mode schema: %v", err)
	}
}

func segmentReadinessDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, func(string), func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "segment_readiness_it_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		t.Fatal("locate segment readiness integration test")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	apply := func(name string) {
		t.Helper()
		sql, readErr := os.ReadFile(filepath.Join(base, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, execErr := native.Exec(applyCtx, string(sql)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	for _, name := range []string{
		"0039_segment_audience_configuration.sql",
		"0040_segment_audience_snapshots.sql",
		"0041_segment_audience_webhooks.sql",
		"0042_segment_audience_execution_bindings.sql",
		"0045_segment_audience_member_events.sql",
		"0048_segment_audience_schedule_state.sql",
		"0053_segment_audience_member_event_fact_kinds.sql",
	} {
		apply(name)
	}
	cleanup := func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}
	return native, apply, cleanup
}
