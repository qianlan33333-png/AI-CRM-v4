package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func TestCustomerTagHistoryCLIExtractDryRunApplyReplayVerifyAndDrift(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, source, cleanup := tagHistoryPools(t, ctx, url)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", urlWithSchema(t, url, pool))
	seedTagHistory(t, ctx, pool)
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	rows := []sourceJob{
		// actor_id is intentionally an unrelated caller. Attribution must use
		// follow_user_userid, including this historical inactive staff member.
		{ID: 71, EffectType: "wecom.contact.tag.mark", Operation: "tag_mark", TargetID: "external-one", ActorID: "admin-caller", Payload: json.RawMessage(`{"external_userid":"external-one","follow_user_userid":"staff-historical","tag_ids":["provider-tag-one"]}`), Status: "succeeded", CreatedAt: at, CompletedAt: ptrTime(at.Add(time.Second))},
		{ID: 72, EffectType: "wecom.contact.tag.unmark", Operation: "tag_unmark", TargetID: "external-missing", ActorID: "admin-caller", Payload: json.RawMessage(`{"external_userid":"external-missing","follow_user_userid":"staff-historical","tag_ids":["provider-tag-one"]}`), Status: "succeeded", CreatedAt: at.Add(2 * time.Second)},
		{ID: 73, EffectType: "wecom.contact.tag.mark", Operation: "tag_mark", TargetID: "external-one", ActorID: "admin-caller", Payload: json.RawMessage(`{"external_userid":"other-external","follow_user_userid":"staff-historical","tag_ids":["provider-tag-one"]}`), Status: "queued", CreatedAt: at.Add(3 * time.Second)},
		{ID: 74, EffectType: "wecom.contact.tag.mark", Operation: "tag_mark", TargetID: "external-one", ActorID: "", Payload: json.RawMessage(`{"external_userid":"external-one","follow_user_userid":"staff-missing","tag_ids":["provider-tag-one"]}`), Status: "succeeded", CreatedAt: at.Add(4 * time.Second)},
		{ID: 75, EffectType: "wecom.contact.tag.unmark", Operation: "tag_unmark", TargetID: "external-one", ActorID: "", Payload: json.RawMessage(`{"external_userid":"external-one","follow_user_userid":"staff-historical","tag_ids":["provider-tag-one"]}`), Status: "succeeded", CreatedAt: at.Add(5 * time.Second)},
		{ID: 76, EffectType: "wecom.contact.tag.mark", Operation: "tag_unmark", TargetID: "external-one", ActorID: "", Payload: json.RawMessage(`{"external_userid":"external-one","follow_user_userid":"staff-historical","tag_ids":["provider-tag-one"]}`), Status: "succeeded", CreatedAt: at.Add(6 * time.Second)},
	}
	seedV2ExternalEffectJobs(t, ctx, source, rows)
	// This row has a tag-looking effect type but fails the frozen source
	// operation filter. It must never enter the snapshot.
	seedV2ExternalEffectJobs(t, ctx, source, []sourceJob{{ID: 77, EffectType: "wecom.contact.tag.mark", Operation: "other", TargetID: "external-one", Payload: json.RawMessage(`{}`), Status: "succeeded", CreatedAt: at}})
	sourceURLFile := filepath.Join(t.TempDir(), "v2-readonly-url")
	if err = os.WriteFile(sourceURLFile, []byte(urlWithSchema(t, url, source)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "tag-history.json")
	if err = run(ctx, []string{"--mode=extract", "--source-database-url-file=" + sourceURLFile, "--snapshot=" + snapshot, "--wecom-corp-id=corp-test"}); err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(snapshot); statErr != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot protection info=%v err=%v", info, statErr)
	}
	_, digest, err := load(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := load(snapshot)
	if err != nil || len(loaded.Jobs) != 6 || loaded.Jobs[0].ID != 71 || loaded.Jobs[0].CompletedAt == nil || !loaded.Jobs[0].CompletedAt.Equal(at.Add(time.Second)) {
		t.Fatalf("source PostgreSQL extraction mismatch: jobs=%+v err=%v", loaded.Jobs, err)
	}
	if err = run(ctx, []string{"--mode=inspect", "--snapshot=" + snapshot}); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=dry-run", "--snapshot=" + snapshot}); err != nil {
		t.Fatal(err)
	}
	apply := []string{"--mode=apply", "--snapshot=" + snapshot, "--manifest-sha256=" + digest, "--confirm-apply"}
	if err = run(ctx, apply); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, apply); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshot}); err != nil {
		t.Fatal(err)
	}
	var receipts, effects, commands, jobs int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM customer_tag_history_receipts),(SELECT count(*) FROM external_effects),(SELECT count(*) FROM customer_tag_commands),(SELECT count(*) FROM river_job)`).Scan(&receipts, &effects, &commands, &jobs); err != nil {
		t.Fatal(err)
	}
	if receipts != 6 || effects != 0 || commands != 0 || jobs != 0 {
		t.Fatalf("receipts=%d effects=%d commands=%d river=%d", receipts, effects, commands, jobs)
	}
	var imported, pending, conflict, excluded int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE resolution='imported'),count(*) FILTER (WHERE resolution='pending'),count(*) FILTER (WHERE resolution='conflict'),count(*) FILTER (WHERE resolution='excluded') FROM customer_tag_history_receipts`).Scan(&imported, &pending, &conflict, &excluded); err != nil || imported != 2 || pending != 2 || conflict != 1 || excluded != 1 {
		t.Fatalf("resolutions imported=%d pending=%d conflict=%d excluded=%d err=%v", imported, pending, conflict, excluded, err)
	}
	// A second process cannot interleave a re-run while the snapshot lease is
	// held. It observes a safe retryable refusal and cannot create effects.
	lease, err := acquire(ctx, pool, digest)
	if err != nil {
		t.Fatal(err)
	}
	err = run(ctx, apply)
	lease.Release()
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent apply err=%v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM customer_tag_history_receipts`).Scan(&receipts); err != nil || receipts != 6 {
		t.Fatalf("concurrent apply changed receipts=%d err=%v", receipts, err)
	}

	// A newer protected capture may overlap the first one. Its exact source
	// fact replays globally, and verification must read that global receipt.
	m, _, err := load(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	m.CapturedAt = m.CapturedAt.Add(time.Minute)
	overlap := filepath.Join(t.TempDir(), "overlap.json")
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(overlap, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	_, overlapDigest, err := load(overlap)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=apply", "--snapshot=" + overlap, "--manifest-sha256=" + overlapDigest, "--confirm-apply"}); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + overlap}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM customer_tag_history_receipts`).Scan(&receipts); err != nil || receipts != 6 {
		t.Fatalf("overlap receipts=%d err=%v", receipts, err)
	}
	badSchema := newSourceSchema(t, ctx, url)
	defer dropSourceSchema(ctx, url, badSchema)
	badURL := filepath.Join(t.TempDir(), "bad-v2-readonly-url")
	if err = os.WriteFile(badURL, []byte(url+"&search_path="+badSchema+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=extract", "--source-database-url-file=" + badURL, "--snapshot=" + filepath.Join(t.TempDir(), "drift.json"), "--wecom-corp-id=corp-test"}); err == nil {
		t.Fatal("source external_effect_job field drift was accepted")
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func tagHistoryPools(t *testing.T, ctx context.Context, url string) (*pgxpool.Pool, *pgxpool.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	schema := "aicrm_tag_history_" + hex.EncodeToString(raw)
	id := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+id); err != nil {
		t.Fatal(err)
	}
	_, _ = rand.Read(raw)
	sourceSchema := "aicrm_v2_effect_source_" + hex.EncodeToString(raw)
	sourceID := pgx.Identifier{sourceSchema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+sourceID); err != nil {
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	sourceCfg := cfg.Copy()
	sourceCfg.ConnConfig.RuntimeParams["search_path"] = sourceSchema
	source, err := pgxpool.NewWithConfig(ctx, sourceCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, v2ExternalEffectJobSchema); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0019_tag_catalog_sync_projection.sql", "0093_customer_tag_commands.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err := native.Exec(ctx, `ALTER TABLE admin_users
		ADD COLUMN login_enabled BOOLEAN NOT NULL DEFAULT FALSE,
		ADD COLUMN legacy_login_reactivation_pending BOOLEAN NOT NULL DEFAULT FALSE,
		ADD COLUMN access_granted_at TIMESTAMPTZ,
		ADD CONSTRAINT ck_admin_users_login_requires_access_grant CHECK (access_granted_at IS NOT NULL OR login_enabled = FALSE)`); err != nil {
		t.Fatalf("apply current Access login fixture contract: %v", err)
	}
	return native, source, func() {
		source.Close()
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+id+" CASCADE")
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+sourceID+" CASCADE")
		admin.Close()
	}
}
func seedTagHistory(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO admin_users(id,username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at,session_version) OVERRIDING SYSTEM VALUE VALUES(7,'staff-one','$argon2id$test','Staff','staff-one',true,false,NULL,1),(8,'staff-historical','$argon2id$test','Historical staff','staff-historical',false,false,NULL,1); INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES(7,'viewer'),(8,'viewer'); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(11,'active'); INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES(11,'wecom_external_userid','wecom-corp:corp-test','external-one','verified','test',1,clock_timestamp()); INSERT INTO tag_groups(id,group_name,sort_order) OVERRIDING SYSTEM VALUE VALUES(13,'history',1); INSERT INTO tag_catalog_tags(id,group_id,tag_name,sort_order) OVERRIDING SYSTEM VALUE VALUES(17,13,'tag',1); INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('provider-tag-one',17)`)
	if err != nil {
		t.Fatal(err)
	}
	var ungrantedStaff int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM admin_users WHERE id IN (7,8) AND login_enabled=FALSE AND access_granted_at IS NULL`).Scan(&ungrantedStaff); err != nil || ungrantedStaff != 2 {
		t.Fatalf("tag history fixture staff login grant count=%d err=%v", ungrantedStaff, err)
	}
}
func urlWithSchema(t *testing.T, url string, pool *pgxpool.Pool) string {
	t.Helper()
	var schema string
	if err := pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return url + "&search_path=" + schema
}

// This is the frozen v2 migration 0039 external_effect_job table contract.
// The source test uses PostgreSQL rows from it; it does not manufacture a
// line-based input format for the extractor.
const v2ExternalEffectJobSchema = `CREATE TABLE external_effect_job (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL DEFAULT 'aicrm', effect_type TEXT NOT NULL, adapter_name TEXT NOT NULL,
    operation TEXT NOT NULL, target_type TEXT NOT NULL, target_id TEXT NOT NULL,
    business_type TEXT NOT NULL DEFAULT '', business_id TEXT NOT NULL DEFAULT '', source_module TEXT NOT NULL DEFAULT '',
    source_route TEXT NOT NULL DEFAULT '', source_event_id TEXT NOT NULL DEFAULT '', source_command_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', correlation_id TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL, actor_id TEXT NOT NULL DEFAULT '', actor_type TEXT NOT NULL DEFAULT 'system',
    risk_level TEXT NOT NULL DEFAULT 'medium', requires_approval BOOLEAN NOT NULL DEFAULT FALSE, execution_mode TEXT NOT NULL DEFAULT 'shadow',
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb, payload_summary_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','approved','queued','dispatching','succeeded','failed_retryable','failed_terminal','blocked','cancelled','expired')),
    priority INTEGER NOT NULL DEFAULT 100, scheduled_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    attempt_count INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 5, next_retry_at TIMESTAMPTZ,
    locked_at TIMESTAMPTZ, locked_by TEXT NOT NULL DEFAULT '', last_attempt_id TEXT NOT NULL DEFAULT '',
    last_error_code TEXT NOT NULL DEFAULT '', last_error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    approved_at TIMESTAMPTZ, executed_at TIMESTAMPTZ, cancelled_at TIMESTAMPTZ,
    UNIQUE(tenant_id, idempotency_key)
)`

func seedV2ExternalEffectJobs(t *testing.T, ctx context.Context, source *pgxpool.Pool, rows []sourceJob) {
	t.Helper()
	for _, row := range rows {
		_, err := source.Exec(ctx, `INSERT INTO external_effect_job(id,effect_type,adapter_name,operation,target_type,target_id,idempotency_key,actor_id,payload_json,status,created_at,executed_at)
			VALUES($1,$2,'wecom_tag',$3,'external_user',$4,$5,$6,$7,$8,$9,$10)`, row.ID, row.EffectType, row.Operation, row.TargetID, fmt.Sprintf("source-%d", row.ID), row.ActorID, row.Payload, row.Status, row.CreatedAt, row.CompletedAt)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func newSourceSchema(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	schema := "aicrm_v2_effect_drift_" + hex.EncodeToString(raw)
	id := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+id); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, strings.Replace(v2ExternalEffectJobSchema, "external_effect_job", id+".external_effect_job", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "ALTER TABLE "+id+".external_effect_job DROP COLUMN executed_at"); err != nil {
		t.Fatal(err)
	}
	return schema
}

func dropSourceSchema(ctx context.Context, url, schema string) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return
	}
	defer admin.Close()
	_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
}
