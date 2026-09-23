package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID decision: not involved. The sealed V2 snapshot contains Config
// release metadata only and is never used to resolve, provision, or link a
// customer. Persistence decision: a serializable, Config-owned historical
// ledger transaction. It deliberately creates neither a live release nor an
// active pointer, outbox event, job, or Provider effect.
func TestRuntimeConfigReleaseHistoryCLIApplyReplayVerifyAndDriftPostgreSQL(t *testing.T) {
	pool, scopedURL, cleanup := runtimeConfigHistoryIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Setenv("AICRM_DATABASE_URL", scopedURL)

	source := runtimeConfigHistoryFixture(t, strings.Repeat("a", 40))
	temp := t.TempDir()
	keyPath := filepath.Join(temp, "snapshot.key")
	snapshotPath := filepath.Join(temp, "snapshot.sealed")
	writeRuntimeConfigHistoryKey(t, keyPath)
	digest, err := sealToFile(source, snapshotPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(digest[:])

	if err := run(ctx, []string{"--mode", "dry-run", "--snapshot", snapshotPath, "--snapshot-key-file", keyPath, "--manifest-sha256", want}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	assertRuntimeConfigHistoryNoLiveEffects(t, ctx, pool)

	if err := run(ctx, []string{"--mode", "apply", "--snapshot", snapshotPath, "--snapshot-key-file", keyPath, "--manifest-sha256", want, "--confirm-apply"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertRuntimeConfigHistoryLedger(t, ctx, pool, "applied")
	assertRuntimeConfigHistoryNoLiveEffects(t, ctx, pool)

	// Same protected snapshot is a receipt replay: it cannot add rows, alter
	// the active pointer, or mint an operational event.
	if err := run(ctx, []string{"--mode", "apply", "--snapshot", snapshotPath, "--snapshot-key-file", keyPath, "--manifest-sha256", want, "--confirm-apply"}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	assertRuntimeConfigHistoryLedger(t, ctx, pool, "applied")

	if err := run(ctx, []string{"--mode", "verify", "--snapshot", snapshotPath, "--snapshot-key-file", keyPath, "--manifest-sha256", want}); err != nil {
		t.Fatalf("initial verify: %v", err)
	}
	assertRuntimeConfigHistoryLedger(t, ctx, pool, "reconciled")

	// Reconciliation compares every protected source row with the actual target
	// fact, rather than accepting a batch count or preserved manifest digest.
	if _, err := pool.Exec(ctx, `UPDATE config_runtime_release_history_rows SET read_only=FALSE WHERE outcome='excluded'`); err == nil {
		t.Fatal("read-only history guard accepted a writable target row")
	}
	if _, err := pool.Exec(ctx, `UPDATE config_runtime_release_history_rows SET source_state='draft',source_created_at=source_created_at+INTERVAL '1 second' WHERE source_release_id=1`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"--mode", "verify", "--snapshot", snapshotPath, "--snapshot-key-file", keyPath, "--manifest-sha256", want}); err == nil {
		t.Fatal("verify accepted changed target state or time")
	}
	if _, err := pool.Exec(ctx, `UPDATE config_runtime_release_history_rows SET source_state='published',source_created_at=$1 WHERE source_release_id=1`, source.Releases[0].CreatedAt.UTC()); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"--mode", "verify", "--snapshot", snapshotPath, "--snapshot-key-file", keyPath, "--manifest-sha256", want}); err != nil {
		t.Fatalf("verify after restoration: %v", err)
	}

	// A changed protected snapshot claiming the same immutable source revision
	// must be rejected before it can change the historic ledger.
	drift := source
	drift.Releases = append([]release(nil), source.Releases...)
	drift.Releases[0].Changes = []byte(`{"AICRM_INTERNAL_EVENTS_WORKER_BATCH_SIZE":"3"}`)
	if err := populateManifest(&drift, source.Manifest.SourceRevision, source.Manifest.SnapshotAt); err != nil {
		t.Fatal(err)
	}
	driftPath := filepath.Join(temp, "drift.sealed")
	driftDigest, err := sealToFile(drift, driftPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"--mode", "apply", "--snapshot", driftPath, "--snapshot-key-file", keyPath, "--manifest-sha256", hex.EncodeToString(driftDigest[:]), "--confirm-apply"}); err == nil || !strings.Contains(err.Error(), "source revision digest drift") {
		t.Fatalf("source revision drift=%v", err)
	}
	assertRuntimeConfigHistoryLedger(t, ctx, pool, "reconciled")
	assertRuntimeConfigHistoryNoLiveEffects(t, ctx, pool)
}

func runtimeConfigHistoryFixture(t *testing.T, revision string) snapshot {
	t.Helper()
	at := time.Date(2026, time.September, 6, 8, 0, 0, 0, time.UTC)
	validated := at.Add(time.Minute)
	published := at.Add(2 * time.Minute)
	firstID := int64(1)
	s := snapshot{Releases: []release{
		// AICRM_INTERNAL_EVENTS_WORKER_BATCH_SIZE is an actual V2 managed
		// runtime key. The V3 Config catalog intentionally has no semantic
		// equivalent, so history preserves this row as excluded rather than
		// pretending it is an Automation recipient ceiling.
		{ID: 1, ReleaseKey: "release-v2-1", ProfileID: "wecom-core", Status: "published", Changes: []byte(`{"AICRM_INTERNAL_EVENTS_WORKER_BATCH_SIZE":"2"}`), Before: []byte(`{"AICRM_INTERNAL_EVENTS_WORKER_BATCH_SIZE":{"exists":true,"value":"1"}}`), ValidationErrors: []byte(`[]`), Checksum: "legacy-a", CreatedBy: "legacy-admin", CreatedAt: at, ValidatedAt: &validated, PublishedBy: "legacy-admin", PublishedAt: &published},
		// Sensitive values stay only in the AES-GCM protected snapshot. The
		// target keeps no raw before/after payload, merely its source digest.
		{ID: 2, ReleaseKey: "release-v2-2", ProfileID: "wecom-core", Status: "superseded", Changes: []byte(`{"AICRM_OAUTH_IDENTITY_APP_SECRET":"secretref:file"}`), Before: []byte(`{"AICRM_OAUTH_IDENTITY_APP_SECRET":{"exists":false,"value":""}}`), ValidationErrors: []byte(`[]`), Checksum: "legacy-b", BasedOnReleaseID: &firstID, CreatedBy: "legacy-admin", CreatedAt: at, ValidatedAt: &validated, PublishedBy: "legacy-admin", PublishedAt: &published},
		{ID: 3, ReleaseKey: "release-v2-3", ProfileID: "wecom-core", Status: "draft", Changes: []byte(`{"AICRM_RUNTIME_CONFIG_CUTOVER_KEYS":"AICRM_INTERNAL_EVENTS_WORKER_BATCH_SIZE"}`), Before: []byte(`{}`), ValidationErrors: []byte(`[]`), Checksum: "legacy-c", BasedOnReleaseID: &firstID, RollbackOfReleaseID: &firstID, CreatedBy: "legacy-admin", CreatedAt: at},
	}}
	if err := populateManifest(&s, revision, at); err != nil {
		t.Fatal(err)
	}
	return s
}

func writeRuntimeConfigHistoryKey(t *testing.T, path string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertRuntimeConfigHistoryLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wantStatus string) {
	t.Helper()
	var input, pending, excluded, rows int
	var status string
	if err := pool.QueryRow(ctx, `SELECT input_count,pending_count,excluded_count,status FROM config_runtime_release_history_batches`).Scan(&input, &pending, &excluded, &status); err != nil {
		t.Fatal(err)
	}
	if input != 3 || pending != 0 || excluded != 3 || status != wantStatus {
		t.Fatalf("history batch input/pending/excluded/status=%d/%d/%d/%q", input, pending, excluded, status)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_release_history_rows`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("history rows=%d", rows)
	}
	var releaseKey, profileID, checksum, createdBy, publishedBy, outcome, reason string
	var sourceState string
	var basedOn, rollbackOf *int64
	var validatedAt, publishedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT source_release_key,source_profile_id,source_checksum,source_state,source_created_by,source_published_by,source_validated_at,source_published_at,source_based_on_release_id,source_rollback_of_release_id,outcome,reason_code FROM config_runtime_release_history_rows WHERE source_release_id=3`).Scan(&releaseKey, &profileID, &checksum, &sourceState, &createdBy, &publishedBy, &validatedAt, &publishedAt, &basedOn, &rollbackOf, &outcome, &reason); err != nil {
		t.Fatal(err)
	}
	if releaseKey != "release-v2-3" || profileID != "wecom-core" || checksum != "legacy-c" || sourceState != "draft" || createdBy != "legacy-admin" || publishedBy != "" || validatedAt != nil || publishedAt != nil || basedOn == nil || *basedOn != 1 || rollbackOf == nil || *rollbackOf != 1 || outcome != "excluded" || reason != "no_v3_runtime_equivalence" {
		t.Fatalf("read-only history context was not preserved key/profile/checksum/state/creator/times/links/outcome=%q/%q/%q/%q/%q/%q/%v/%v/%v/%v/%q/%q", releaseKey, profileID, checksum, sourceState, createdBy, publishedBy, validatedAt, publishedAt, basedOn, rollbackOf, outcome, reason)
	}
}

func assertRuntimeConfigHistoryNoLiveEffects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var releases, outbox int
	var active *int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_releases`).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT release_id FROM config_runtime_active_release WHERE singleton=TRUE`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM config_outbox`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if releases != 0 || active != nil || outbox != 0 {
		t.Fatalf("history import created live config releases=%d active=%v outbox=%d", releases, active, outbox)
	}
}

func runtimeConfigHistoryIntegrationPool(t *testing.T) (*pgxpool.Pool, string, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping runtime Config history PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("open PostgreSQL integration database")
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal("ping PostgreSQL integration database")
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_runtime_config_history_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal("create PostgreSQL integration schema")
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal("open isolated PostgreSQL integration schema")
	}
	for _, path := range runtimeConfigHistoryPaths(t) {
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatalf("apply runtime config migration %s: %v", filepath.Base(path), execErr)
		}
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal("parse scoped test url")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return native, parsed.String(), func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func runtimeConfigHistoryPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate runtime Config history integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return []string{
		filepath.Join(root, "migrations", "0013_automation_agents.sql"),
		filepath.Join(root, "migrations", "0015_config_adminops.sql"),
		filepath.Join(root, "migrations", "0043_automation_runtime.sql"),
		filepath.Join(root, "migrations", "0094_runtime_config_releases.sql"),
	}
}
