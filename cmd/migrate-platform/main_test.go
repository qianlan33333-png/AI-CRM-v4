package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestLoadMigrationsSortsAndChecksVersions(t *testing.T) {
	filesystem := fstest.MapFS{
		"0002_second.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
		"README.md":       &fstest.MapFile{Data: []byte("ignored")},
		"0001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1")},
	}
	items, err := loadMigrations(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].version != "0001" || items[1].version != "0002" {
		t.Fatalf("unexpected order: %+v", items)
	}

	filesystem["0001_duplicate.sql"] = &fstest.MapFile{Data: []byte("SELECT 3")}
	if _, err = loadMigrations(filesystem); err == nil {
		t.Fatal("expected duplicate migration version error")
	}
}

func TestApplyMigrationsFreshAndUpgradePostgreSQL(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_it_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	items, err := loadMigrations(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("fresh migration: %v", err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade replay: %v", err)
	}
	foundConfigMigration := false
	foundSegmentMigration := false
	foundSegmentSnapshotMigration := false
	foundSegmentWebhookMigration := false
	foundCommercePushMigration := false
	for _, item := range items {
		if item.name == "0015_config_adminops.sql" {
			foundConfigMigration = true
		}
		if item.name == "0039_segment_audience_configuration.sql" {
			foundSegmentMigration = true
		}
		if item.name == "0040_segment_audience_snapshots.sql" {
			foundSegmentSnapshotMigration = true
		}
		if item.name == "0041_segment_audience_webhooks.sql" {
			foundSegmentWebhookMigration = true
		}
		if item.name == "0095_product_external_push.sql" {
			foundCommercePushMigration = true
		}
	}
	if !foundConfigMigration {
		t.Fatal("expected 0015_config_adminops.sql in the platform migration set")
	}
	if !foundSegmentMigration || !foundSegmentSnapshotMigration || !foundSegmentWebhookMigration {
		t.Fatal("expected Segment configuration, snapshot and webhook migrations in the platform migration set")
	}
	if !foundCommercePushMigration {
		t.Fatal("expected 0095_product_external_push.sql in the platform migration set")
	}
	var applied int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM platform_schema_migrations`).Scan(&applied); err != nil || applied != len(items) {
		t.Fatalf("applied=%d expected=%d err=%v", applied, len(items), err)
	}
	for _, table := range []string{"media_blobs", "media_references", "media_attachment_upload_parts", "media_content_packages", "media_content_package_versions", "media_content_package_version_refs", "media_content_delivery_receipts", "media_content_delivery_bindings", "media_group_ops_preparation_receipts", "media_group_ops_preparation_items", "tag_groups", "tag_catalog_tags", "tag_provider_observations", "products", "product_operation_receipts", "product_external_push_configurations", "product_external_push_tests", "coupon_rules", "coupon_rule_targets", "coupon_operation_receipts", "coupon_audit_events", "coupon_outbox", "automation_agents", "automation_operation_receipts", "automation_audit_events", "automation_outbox", "automation_policies", "automation_policy_versions", "automation_enrollments", "automation_run_previews", "automation_runs", "automation_run_recipients", "automation_run_reconciliations", "automation_runtime_operation_receipts", "automation_runtime_audit_events", "automation_runtime_outbox", "automation_operations_migration_batches", "automation_operations_migration_source_map", "automation_operations_migration_quarantine", "automation_operations_legacy_history", "segment_audience_groups", "segment_audience_packages", "segment_audience_configuration_versions", "segment_audience_operation_receipts", "segment_audience_audit_events", "segment_audience_outbox", "segment_audience_refresh_runs", "segment_audience_snapshots", "segment_audience_snapshot_members", "segment_audience_refresh_batches", "segment_audience_webhook_receipts", "segment_audience_automation_binding_versions", "segment_audience_sender_sets", "segment_audience_sender_set_members", "segment_audience_member_events", "segment_audience_schedule_states", "outbound_message_intents", "outbound_message_audit_events", "outbound_message_outbox", "group_ops_plans", "group_ops_plan_members", "group_ops_plan_group_assets", "group_ops_plan_nodes", "group_ops_plan_webhook_descriptors", "group_ops_operation_receipts", "group_ops_audit_events", "group_ops_outbox", "group_ops_runs", "group_ops_executions", "group_ops_execution_intents", "group_ops_group_message_tasks", "group_ops_directory_groups", "group_ops_directory_refresh_receipts", "group_ops_protocol_replays", "group_ops_operation_member_directory", "operation_cycle_strategies", "operation_cycle_runs", "operation_cycle_report_receipts", "operation_cycle_runners", "operation_cycle_action_requests", "operation_cycle_action_request_events", "operation_cycle_strategy_proposals", "operation_cycle_strategy_versions", "operation_cycle_run_versions", "operation_cycle_run_ordinals", "operation_cycle_admin_receipts", "config_settings", "config_audits", "config_outbox", "adminops_release_projections", "adminops_diagnostic_snapshots", "admin_access_login_compat_receipts", "hxc_dashboard_versions", "hxc_dashboard_rows", "hxc_dashboard_refresh_runs", "identity_source_subjects", "identity_source_observations", "identity_source_conflicts", "identity_source_resolution_receipts", "identity_source_conflict_actions", "ai_assistant_plans", "ai_assistant_plan_recipients", "ai_assistant_content_versions", "ai_assistant_review_decisions", "ai_assistant_effect_bindings", "ai_assistant_operation_receipts", "ai_assistant_integration_nonces", "ai_assistant_audit_events", "ai_assistant_outbox", "outbound_private_message_intents", "outbound_commerce_push_intents", "outbound_commerce_push_audit_events", "outbound_commerce_push_outbox", "outbound_commerce_push_history_batches", "outbound_commerce_push_history_rows", "outbound_commerce_push_history_batch_rows"} {
		var present bool
		if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table).Scan(&present); err != nil || !present {
			t.Fatalf("owned table %s present=%v err=%v", table, present, err)
		}
	}
	for _, table := range []string{"radar_links", "radar_link_versions", "radar_operation_receipts", "radar_audit_events", "radar_outbox", "radar_oauth_states", "radar_view_sessions", "radar_events", "radar_migration_batches", "radar_migration_source_map", "radar_migration_quarantine", "radar_legacy_events"} {
		var present bool
		if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table).Scan(&present); err != nil || !present {
			t.Fatalf("Radar-owned table %s present=%v err=%v", table, present, err)
		}
	}
	for _, table := range []string{"customer_sidebar_profile_receipts", "order_service_entitlements", "order_entitlement_operation_receipts", "order_entitlement_audit_events", "order_entitlement_outbox", "coupon_customer_claims", "outbound_sidebar_send_intents", "outbound_sidebar_send_grants", "outbound_sidebar_send_audit_events", "outbound_sidebar_send_outbox", "sidebar_history_migration_batches", "sidebar_history_migration_source_map", "sidebar_history_migration_quarantine"} {
		var present bool
		if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table).Scan(&present); err != nil || !present {
			t.Fatalf("sidebar owned table %s present=%v err=%v", table, present, err)
		}
	}
	for _, table := range []string{"channel_semantic_repair_runs", "channel_semantic_repair_conflicts", "channel_semantic_repaired_configs", "channel_legacy_acquisition_assets", "channel_legacy_material_maps", "channel_legacy_tag_maps", "channel_history_contact_reconciliations"} {
		var present bool
		if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table).Scan(&present); err != nil || !present {
			t.Fatalf("Channel semantic repair table %s present=%v err=%v", table, present, err)
		}
	}
}

// TestExcelBatchLifecycleMigrationKeepsRollbackImportsReadable proves the
// installer can safely restore a pre-0124 binary after the forward migration
// has committed. That binary still inserts just batch_key, plan_id and digest;
// the compatibility projection must retain a complete read-only history
// without changing any approval or outbound state.
func TestExcelBatchLifecycleMigrationKeepsRollbackImportsReadable(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_excel_rollback_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		t.Fatal(err)
	}
	previous := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0124_" {
			continue
		}
		contents, readErr := fs.ReadFile(filesystem, entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		previous[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	if err = applyMigrations(ctx, pool, previous); err != nil {
		t.Fatalf("apply release before 0124: %v", err)
	}

	var planID, recipientID, contentID int64
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_at,updated_at)
		VALUES('rollback legacy Excel','excel_batch',decode(repeat('11',32),'hex'),'pending_review',1,1,1,0,0,0,0,1,clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&planID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,deferred_target,created_at,updated_at)
		VALUES($1,0,0,'{"unionid":"legacy-union","scope":"wechat-open-platform:legacy","sender_userid":"legacy-sender"}'::jsonb,clock_timestamp(),clock_timestamp()) RETURNING id`, planID).Scan(&recipientID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_at)
		VALUES($1,1,decode(repeat('22',32),'hex'),'[{"kind":"text","text":"legacy"},{"kind":"mini_program","excel_card":{"appid":"legacy-app","path":"pages/legacy","title":"legacy","cover_digest":""}}]'::jsonb,1,clock_timestamp()) RETURNING id`, recipientID).Scan(&contentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2 WHERE id=$1`, recipientID, contentID); err != nil {
		t.Fatal(err)
	}
	// A prior rollback may already have a legacy cover before 0124 runs. Its
	// first historical version must retain that cover instead of inferring an
	// empty one from a later current-content read.
	preUpgradeCover := "sha256:" + strings.Repeat("c", 64)
	var preUpgradePlanID, preUpgradeRecipientID, preUpgradeContentID int64
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_at,updated_at)
		VALUES('existing legacy Excel','excel_batch',decode(repeat('55',32),'hex'),'pending_review',1,1,1,0,0,0,0,1,clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&preUpgradePlanID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,deferred_target,created_at,updated_at)
		VALUES($1,0,0,'{"unionid":"existing-union","scope":"wechat-open-platform:legacy","sender_userid":"legacy-sender"}'::jsonb,clock_timestamp(),clock_timestamp()) RETURNING id`, preUpgradePlanID).Scan(&preUpgradeRecipientID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_at)
		VALUES($1,1,decode(repeat('66',32),'hex'),'[{"kind":"text","text":"legacy"},{"kind":"mini_program","excel_card":{"appid":"legacy-app","path":"pages/legacy","title":"legacy","cover_digest":"`+preUpgradeCover+`"}}]'::jsonb,1,clock_timestamp()) RETURNING id`, preUpgradeRecipientID).Scan(&preUpgradeContentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2 WHERE id=$1`, preUpgradeRecipientID, preUpgradeContentID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO ai_assistant_excel_imports(batch_key,plan_id,file_digest) VALUES($1,$2,$3)`, "pre-upgrade-legacy-import", preUpgradePlanID, "sha256:"+strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("apply 0124: %v", err)
	}
	var preUpgradeVersionCover string
	if err = pool.QueryRow(ctx, `SELECT cover_digest FROM ai_assistant_excel_batch_versions WHERE plan_id=$1 AND content_revision=1`, preUpgradePlanID).Scan(&preUpgradeVersionCover); err != nil || preUpgradeVersionCover != preUpgradeCover {
		t.Fatalf("pre-upgrade legacy cover=%q err=%v", preUpgradeVersionCover, err)
	}

	fileDigest := "sha256:" + strings.Repeat("a", 64)
	if _, err = pool.Exec(ctx, `INSERT INTO ai_assistant_excel_imports(batch_key,plan_id,file_digest) VALUES($1,$2,$3)`, "rollback-legacy-import", planID, fileDigest); err != nil {
		t.Fatalf("pre-0124 import insert after migration: %v", err)
	}
	var origin, versionDigest, coverDigest, state string
	var revision, planVersion, recipientRevision, versionActor int64
	var createdAt time.Time
	err = pool.QueryRow(ctx, `SELECT source_origin,content_revision,created_at FROM ai_assistant_excel_imports WHERE plan_id=$1`, planID).Scan(&origin, &revision, &createdAt)
	if err != nil || origin != "legacy_snapshot" || revision != 1 || createdAt.IsZero() {
		t.Fatalf("legacy import metadata origin=%q revision=%d created_at=%v err=%v", origin, revision, createdAt, err)
	}
	err = pool.QueryRow(ctx, `SELECT file_digest,cover_digest,created_by FROM ai_assistant_excel_batch_versions WHERE plan_id=$1 AND content_revision=1`, planID).Scan(&versionDigest, &coverDigest, &versionActor)
	if err != nil || versionDigest != fileDigest || coverDigest != "" || versionActor != 1 {
		t.Fatalf("legacy version digest=%q cover=%q actor=%d err=%v", versionDigest, coverDigest, versionActor, err)
	}
	if err = pool.QueryRow(ctx, `SELECT excel_batch_content_revision FROM ai_assistant_plan_recipients WHERE id=$1`, recipientID).Scan(&recipientRevision); err != nil || recipientRevision != 1 {
		t.Fatalf("legacy recipient revision=%d err=%v", recipientRevision, err)
	}

	coverA := "sha256:" + strings.Repeat("a", 64)
	coverB := "sha256:" + strings.Repeat("b", 64)
	var coverAContentID, coverBContentID int64
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_at)
		VALUES($1,2,decode(repeat('33',32),'hex'),'[{"kind":"text","text":"legacy"},{"kind":"mini_program","excel_card":{"appid":"legacy-app","path":"pages/legacy","title":"legacy","cover_digest":"`+coverA+`"}}]'::jsonb,1,clock_timestamp()) RETURNING id`, recipientID).Scan(&coverAContentID)
	if err != nil {
		t.Fatalf("pre-0124 cover A content insert: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, recipientID, coverAContentID); err != nil {
		t.Fatalf("pre-0124 cover A projection: %v", err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_at)
		VALUES($1,3,decode(repeat('44',32),'hex'),'[{"kind":"text","text":"legacy"},{"kind":"mini_program","excel_card":{"appid":"legacy-app","path":"pages/legacy","title":"legacy","cover_digest":"`+coverB+`"}}]'::jsonb,1,clock_timestamp()) RETURNING id`, recipientID).Scan(&coverBContentID)
	if err != nil {
		t.Fatalf("pre-0124 cover B content insert: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, recipientID, coverBContentID); err != nil {
		t.Fatalf("pre-0124 cover B projection: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, recipientID, coverAContentID); err != nil {
		t.Fatalf("pre-0124 cover A reversion projection: %v", err)
	}
	var latestCover string
	if err = pool.QueryRow(ctx, `SELECT cover_digest FROM ai_assistant_excel_batch_version_covers WHERE plan_id=$1 AND content_revision=1 ORDER BY created_at DESC,cover_digest DESC LIMIT 1`, planID).Scan(&latestCover); err != nil || latestCover != coverA {
		t.Fatalf("legacy cover reversion latest=%q err=%v", latestCover, err)
	}
	if err = pool.QueryRow(ctx, `SELECT state,version FROM ai_assistant_plans WHERE id=$1`, planID).Scan(&state, &planVersion); err != nil || state != "pending_review" || planVersion != 1 {
		t.Fatalf("legacy trigger changed plan state=%q version=%d err=%v", state, planVersion, err)
	}
}

func TestHXCIdentityV2MigrationsUpgradePublishedV1Projection(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_hxc_v2_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		t.Fatal(err)
	}
	previous := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0063_identity_hxc_source_observations.sql" {
			continue
		}
		contents, readErr := fs.ReadFile(filesystem, entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		previous[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	if err = applyMigrations(ctx, pool, previous); err != nil {
		t.Fatalf("apply release through 0062: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO customers DEFAULT VALUES;
		INSERT INTO hxc_dashboard_versions(rule_version,status,projection_as_of,source_digest,projection_digest,total_count,active_used_count,active_unused_count,registered_no_active_membership_count,matched_count,unmatched_count,conflict_count)
		VALUES('hxc-current-v1','published',now(),decode(repeat('01',32),'hex'),decode(repeat('02',32),'hex'),1,1,0,0,1,0,0);
		INSERT INTO hxc_dashboard_rows(projection_id,subject_digest,user_ref,stage,subscription_tier,monthly_chat_quota,current_period_used,consultation_limit,consultation_used,membership_attribution,sessions_7d,sessions_30d,sessions_total,user_messages_7d,user_messages_30d,user_messages_total,capability_usage,focus_topics,customer_id,identity_state,source_updated_at)
		SELECT id,decode(repeat('03',32),'hex'),'HXC-030303030303','active_used','pro',0,0,0,0,'user_id',0,0,0,0,0,0,'{}','[]',1,'matched',now() FROM hxc_dashboard_versions WHERE status='published';
		INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,status) VALUES('v1-upgrade',decode(repeat('04',32),'hex'),'initial','succeeded')`); err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade release through 0064: %v", err)
	}
	var unionMatches, pending, replayVerified int64
	var matchedBy, reason, mode string
	if err = pool.QueryRow(ctx, `SELECT matched_by_unionid_count,pending_observation_count FROM hxc_dashboard_versions WHERE status='published'`).Scan(&unionMatches, &pending); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT matched_by,identity_reason_code FROM hxc_dashboard_rows`).Scan(&matchedBy, &reason); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT identity_mode,identity_replay_verified_count FROM hxc_dashboard_refresh_runs WHERE run_key='v1-upgrade'`).Scan(&mode, &replayVerified); err != nil {
		t.Fatal(err)
	}
	if unionMatches != 1 || pending != 0 || matchedBy != "unionid" || reason != "matched_unionid" || mode != "inspect" || replayVerified != 0 {
		t.Fatalf("unexpected HXC v1 backfill: union=%d pending=%d matched_by=%s reason=%s mode=%s replay=%d", unionMatches, pending, matchedBy, reason, mode, replayVerified)
	}
}

func TestLoadMigrationsRejectsEmptySet(t *testing.T) {
	_, err := loadMigrations(fstest.MapFS{"README.md": &fstest.MapFile{Data: []byte("none")}})
	if err == nil {
		t.Fatal("expected missing migration error")
	}
	if _, ok := err.(*fs.PathError); ok {
		t.Fatalf("expected safe domain error, got %T", err)
	}
}

func TestOperationCycleHistoryMigrationUpgradesExistingProjection(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_upgrade_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		t.Fatal(err)
	}
	prior := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0023_operation_cycle_admin_history.sql" {
			continue
		}
		contents, readErr := fs.ReadFile(filesystem, entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		prior[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	if err = applyMigrations(ctx, pool, prior); err != nil {
		t.Fatalf("apply previous release: %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO operation_cycle_strategies(strategy_key,title,status,version,definition,snapshot,updated_at)
		VALUES ('upgrade.review','升级复盘','active',3,'{"external_effects":"none"}','{"schema_version":"operation_cycle_snapshot.v1"}',clock_timestamp());
		INSERT INTO operation_cycle_runs(run_key,strategy_key,snapshot_revision,snapshot,received_at)
		VALUES ('upgrade.review.001','upgrade.review',8,'{"schema_version":"operation_cycle_snapshot.v1","revision":8}',clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade current release: %v", err)
	}
	var strategyVersion, runRevision, runOrdinal int32
	if err = pool.QueryRow(ctx, `SELECT version FROM operation_cycle_strategy_versions WHERE strategy_key='upgrade.review'`).Scan(&strategyVersion); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT snapshot_revision FROM operation_cycle_run_versions WHERE run_key='upgrade.review.001'`).Scan(&runRevision); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT ordinal FROM operation_cycle_run_ordinals WHERE run_key='upgrade.review.001'`).Scan(&runOrdinal); err != nil {
		t.Fatal(err)
	}
	if strategyVersion != 3 || runRevision != 8 || runOrdinal < 1 {
		t.Fatalf("backfilled strategy/run version/ordinal=%d/%d/%d, want 3/8/positive", strategyVersion, runRevision, runOrdinal)
	}
	if _, err = pool.Exec(ctx, `UPDATE operation_cycle_run_ordinals SET run_key='changed' WHERE ordinal=$1`, runOrdinal); err == nil {
		t.Fatal("backfilled run ordinal accepted mutation")
	}
}

// TestAdminAccessCompatibilityMigrationUpgradesPreviousRelease proves that
// 0027 executes against a database whose migration ledger already contains the
// complete previous release (0001 through 0026). Re-applying a full filesystem
// after 0027 is present would only test checksum skipping, not this upgrade.
func TestAdminAccessCompatibilityMigrationUpgradesPreviousRelease(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_access_upgrade_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		t.Fatal(err)
	}
	previous := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0027_admin_access_login_compat.sql" {
			continue
		}
		contents, readErr := fs.ReadFile(filesystem, entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		previous[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	if err = applyMigrations(ctx, pool, previous); err != nil {
		t.Fatalf("apply previous release through 0026: %v", err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade previous release through 0027: %v", err)
	}
	var name string
	if err = pool.QueryRow(ctx, `SELECT name FROM platform_schema_migrations WHERE version='0027'`).Scan(&name); err != nil || name != "0027_admin_access_login_compat.sql" {
		t.Fatalf("0027 ledger name=%q err=%v", name, err)
	}
	var definition string
	if err = pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='admin_access_audit'::regclass AND conname='ck_admin_access_audit_action'`).Scan(&definition); err != nil || !strings.Contains(definition, "set_login_enabled") {
		t.Fatalf("upgraded action constraint=%q err=%v", definition, err)
	}
	var receiptTable bool
	if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.admin_access_login_compat_receipts') IS NOT NULL`).Scan(&receiptTable); err != nil || !receiptTable {
		t.Fatalf("upgraded receipt table=%v err=%v", receiptTable, err)
	}
}
