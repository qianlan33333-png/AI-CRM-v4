package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func segmentDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
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
		t.Fatal(err)
	}
	schema := "segment_it_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	for _, name := range []string{
		"0039_segment_audience_configuration.sql",
		"0040_segment_audience_snapshots.sql",
		"0041_segment_audience_webhooks.sql",
		"0042_segment_audience_execution_bindings.sql",
		"0045_segment_audience_member_events.sql",
		"0048_segment_audience_schedule_state.sql",
		"0053_segment_audience_member_event_fact_kinds.sql",
		"0083_segment_audience_refresh_modes.sql",
		"0085_segment_audience_refresh_kind.sql",
		"0097_segment_audience_mutation_actor.sql",
		"0183_segment_core_operations.sql",
	} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = native.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply segment migration %s: %v", name, err)
		}
	}
	return native, func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}
}

func assertSegmentCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want [6]int) {
	t.Helper()
	var got [6]int
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM segment_audience_groups),
		(SELECT count(*) FROM segment_audience_packages),
		(SELECT count(*) FROM segment_audience_configuration_versions),
		(SELECT count(*) FROM segment_audience_operation_receipts),
		(SELECT count(*) FROM segment_audience_audit_events),
		(SELECT count(*) FROM segment_audience_outbox)`).Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5])
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("segment counts=%v want=%v", got, want)
	}
}

func reservationFor(key string, payload json.RawMessage, now time.Time) Reservation {
	return Reservation{
		Operation: "create", ActorScope: "admin:7", ActorKind: "admin", ActorRef: "admin:7",
		KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now,
	}
}

func testFact(id int64, key string, now time.Time) MutationFact {
	return MutationFact{
		ResourceKind: "package", ResourceID: id, Operation: "create",
		EventType: "audience.package.created.v1", ActorID: 7,
		Payload: json.RawMessage(`{"package_id":1}`), IdempotencyKey: key, OccurredAt: now,
	}
}
