package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	archivemigration "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/migration"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestMessageArchiveMigrationDryRunApplyReplayResolveAndReconcilePostgreSQL(t *testing.T) {
	pool, cleanup := archiveMigrationIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native := pool.Native()
	manifest := archiveMigrationManifest(t)
	if err := seedArchiveMigrationIdentity(ctx, native, "wm_known"); err != nil {
		t.Fatal(err)
	}
	resolver := newHistoricalResolver(manifest)

	dry, err := dryRun(ctx, native, manifest, resolver)
	if err != nil || dry.Inserted != 1 || dry.Unresolved != 3 || len(dry.Rows) != 4 {
		t.Fatalf("dry-run=%+v err=%v", dry, err)
	}
	assertArchiveMigrationCounts(t, ctx, native, 0, 0, 0)

	applied, err := apply(ctx, native, manifest, resolver)
	if err != nil || applied.Inserted != 1 || applied.Unresolved != 3 {
		t.Fatalf("apply=%+v err=%v", applied, err)
	}
	var knownCustomer, knownStaff int64
	if err = native.QueryRow(ctx, `SELECT COALESCE(max(customer_id_at_ingest),0),COALESCE(max(staff_user_id),0) FROM message_archive_participants participant JOIN message_archive_messages message ON message.id=participant.message_id WHERE message.msgid='m-known'`).Scan(&knownCustomer, &knownStaff); err != nil || knownCustomer < 1 || knownStaff < 1 {
		t.Fatalf("resolved archive participant customer=%d staff=%d err=%v", knownCustomer, knownStaff, err)
	}
	var historicalUnionID, historicalGroupName string
	if err = native.QueryRow(ctx, `SELECT projection.historical_unionid,projection.historical_group_name FROM message_archive_legacy_projections projection JOIN message_archive_messages message ON message.id=projection.message_id WHERE message.msgid='m-known'`).Scan(&historicalUnionID, &historicalGroupName); err != nil || historicalUnionID != "union-known" || historicalGroupName != "Legacy group" {
		t.Fatalf("historical projection union=%q group=%q err=%v", historicalUnionID, historicalGroupName, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, manifest); reconcileErr != nil || !matched {
		t.Fatalf("initial reconcile matched=%t err=%v", matched, reconcileErr)
	}
	// A fully matching snapshot replays without creating a second message. The
	// stronger existing-message comparison below must preserve this normal case.
	replayed, err := apply(ctx, native, manifest, resolver)
	if err != nil || replayed.Duplicates != 4 || replayed.Inserted != 0 || replayed.Unresolved != 0 || replayed.Quarantined != 0 {
		t.Fatalf("duplicate replay=%+v err=%v", replayed, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, manifest); reconcileErr != nil || !matched {
		t.Fatalf("duplicate reconcile matched=%t err=%v", matched, reconcileErr)
	}
	projectionConflict := archiveMigrationProjectionConflictManifest(t)
	conflictedProjection, err := apply(ctx, native, projectionConflict, resolver)
	if err != nil || conflictedProjection.Quarantined != 1 || conflictedProjection.Duplicates != 0 {
		t.Fatalf("projection conflict=%+v err=%v", conflictedProjection, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, projectionConflict); reconcileErr != nil || !matched {
		t.Fatalf("projection conflict reconcile matched=%t err=%v", matched, reconcileErr)
	}

	if err = seedArchiveMigrationIdentity(ctx, native, "wm_later"); err != nil {
		t.Fatal(err)
	}
	// The first bounded pass is deliberately occupied by unresolved rows that
	// remain not_found. The operator receives a cursor and can continue past
	// them to the later verified identity; there is no background worker.
	firstPass, err := reResolve(ctx, native, manifest, resolver, 2, 0)
	if err != nil || firstPass.Inserted != 0 || firstPass.Unresolved != 2 || firstPass.NextParticipantID < 1 {
		t.Fatalf("first re-resolve=%+v err=%v", firstPass, err)
	}
	reResolved, err := reResolve(ctx, native, manifest, resolver, 2, firstPass.NextParticipantID)
	if err != nil || reResolved.Inserted != 1 || reResolved.Unresolved != 0 || reResolved.NextParticipantID <= firstPass.NextParticipantID {
		t.Fatalf("continued re-resolve=%+v err=%v", reResolved, err)
	}
	var attempts int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM message_archive_resolution_attempts`).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatalf("resolution attempts=%d err=%v", attempts, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, manifest); reconcileErr != nil || !matched {
		t.Fatalf("reconcile after resolution matched=%t err=%v", matched, reconcileErr)
	}
	conflictManifest := archiveMigrationSequenceConflictManifest(t)
	conflicted, err := apply(ctx, native, conflictManifest, resolver)
	if err != nil || conflicted.Quarantined != 1 || conflicted.Duplicates != 0 || conflicted.Inserted != 0 {
		t.Fatalf("same msgid changed seq=%+v err=%v", conflicted, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, conflictManifest); reconcileErr != nil || !matched {
		t.Fatalf("sequence conflict reconcile matched=%t err=%v", matched, reconcileErr)
	}

	if _, err = native.Exec(ctx, `UPDATE message_archive_legacy_projections SET historical_group_name='drift' WHERE message_id=(SELECT id FROM message_archive_messages WHERE msgid='m-known')`); err != nil {
		t.Fatal(err)
	}
	if _, reconcileErr := reconcile(ctx, native, manifest); !errors.Is(reconcileErr, errReconcileDrift) {
		t.Fatalf("historical projection drift reconcile=%v", reconcileErr)
	}
	if _, err = native.Exec(ctx, `UPDATE message_archive_messages SET content_text='drift' WHERE msgid='m-known'`); err != nil {
		t.Fatal(err)
	}
	if _, reconcileErr := reconcile(ctx, native, manifest); !errors.Is(reconcileErr, errReconcileDrift) {
		t.Fatalf("content drift reconcile=%v", reconcileErr)
	}
}

func TestMessageArchiveExtractLegacyRowsPreservesHistoricalProjectionAndReplaysPostgreSQL(t *testing.T) {
	target, cleanupTarget := archiveMigrationIntegrationPool(t)
	defer cleanupTarget()
	source, cleanupSource, sourceURL := archiveLegacySourcePool(t)
	defer cleanupSource()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := source.Exec(ctx, `
		CREATE TABLE archived_messages (
			id BIGINT PRIMARY KEY,
			seq BIGINT NOT NULL,
			msgid TEXT NOT NULL,
			unionid TEXT,
			group_name TEXT,
			raw_payload TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	rawFallback := `{"seq":1,"encrypted_record":{"msgid":"legacy-raw-group","encrypt_chat_msg":"protected-text"},"decrypted_message":{"msgid":"legacy-raw-group","from":"staff-one","tolist":["wm_known"],"roomid":"room-raw","msgtype":"text","msgtime":1788336000,"text":{"content":"raw group"},"group_name":"Ignored decrypted name"},"group_name":"Raw payload group"}`
	rowPreferred := `{"seq":2,"encrypted_record":{"msgid":"legacy-row-group","encrypt_chat_msg":"protected-image"},"decrypted_message":{"msgid":"legacy-row-group","from":"staff-one","tolist":["wm_known"],"roomid":"room-row","msgtype":"image","msgtime":1788336060,"image":{"sdkfileid":"sdk-image","md5sum":"abc","filesize":42},"group_name":"Ignored decrypted name"},"group_name":"Raw group must not win"}`
	unknownAttachment := `{"seq":3,"encrypted_record":{"msgid":"legacy-file","encrypt_chat_msg":"protected-file"},"decrypted_message":{"msgid":"legacy-file","from":"staff-one","tolist":["wm_known"],"msgtype":"file","msgtime":1788336120,"file":{"sdkfileid":"sdk-file","filename":"history.pdf"}}}`
	if _, err := source.Exec(ctx, `INSERT INTO archived_messages(id,seq,msgid,unionid,group_name,raw_payload) VALUES
		(11,1,'legacy-raw-group','union-raw','',$1),
		(12,2,'legacy-row-group','union-row','Row column group',$2),
		(13,3,'legacy-file','union-file','',$3)`, rawFallback, rowPreferred, unknownAttachment); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	sourceURLPath, snapshotPath := filepath.Join(directory, "legacy-source.url"), filepath.Join(directory, "archive-snapshot.json")
	if err := os.WriteFile(sourceURLPath, []byte(sourceURL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	revision := "dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f"
	if err := run(ctx, []string{"-mode", "extract", "-snapshot", snapshotPath, "-source-database-url-file", sourceURLPath, "-source-revision", revision, "-wecom-corp-id", "wx-archive-integration"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(snapshotPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot info=%v err=%v", info, err)
	}
	manifest, err := archivemigration.Load(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SourceName != "ai-crm:archived_messages:"+revision || len(manifest.Records) != 3 || manifest.Records[0].HistoricalUnionID != "union-raw" || manifest.Records[0].HistoricalGroupName != "Raw payload group" || manifest.Records[1].HistoricalUnionID != "union-row" || manifest.Records[1].HistoricalGroupName != "Row column group" || manifest.Records[2].MsgID != "legacy-file" || manifest.Records[2].SourcePayloadDigest == "" {
		t.Fatalf("extracted manifest=%+v", manifest)
	}
	var extractedPayload map[string]any
	if err = json.Unmarshal(manifest.Records[1].Payload, &extractedPayload); err != nil || extractedPayload["msgtype"] != "image" {
		t.Fatalf("extracted decrypted payload=%v err=%v", extractedPayload, err)
	}
	var sourceRows int
	if err = source.QueryRow(ctx, `SELECT count(*) FROM archived_messages`).Scan(&sourceRows); err != nil || sourceRows != 3 {
		t.Fatalf("source rows=%d err=%v", sourceRows, err)
	}

	native := target.Native()
	if err = seedArchiveMigrationIdentity(ctx, native, "wm_known"); err != nil {
		t.Fatal(err)
	}
	resolver := newHistoricalResolver(manifest)
	dry, err := dryRun(ctx, native, manifest, resolver)
	if err != nil || dry.Inserted != 3 || dry.Unresolved != 0 {
		t.Fatalf("dry run=%+v err=%v", dry, err)
	}
	assertArchiveMigrationCounts(t, ctx, native, 0, 0, 0)
	applied, err := apply(ctx, native, manifest, resolver)
	if err != nil || applied.Inserted != 3 || applied.Duplicates != 0 {
		t.Fatalf("apply=%+v err=%v", applied, err)
	}
	replayed, err := apply(ctx, native, manifest, resolver)
	if err != nil || replayed.Duplicates != 3 || replayed.Inserted != 0 {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, manifest); reconcileErr != nil || !matched {
		t.Fatalf("verify/reconcile matched=%t err=%v", matched, reconcileErr)
	}
	for msgID, expected := range map[string][2]string{
		"legacy-raw-group": {"union-raw", "Raw payload group"},
		"legacy-row-group": {"union-row", "Row column group"},
	} {
		var gotUnion, gotGroup string
		if err = native.QueryRow(ctx, `SELECT legacy.historical_unionid,legacy.historical_group_name FROM message_archive_legacy_projections legacy JOIN message_archive_messages message ON message.id=legacy.message_id WHERE message.msgid=$1`, msgID).Scan(&gotUnion, &gotGroup); err != nil || gotUnion != expected[0] || gotGroup != expected[1] {
			t.Fatalf("projection msgid=%s union=%q group=%q err=%v", msgID, gotUnion, gotGroup, err)
		}
	}
	var messageType, providerPayload string
	if err = native.QueryRow(ctx, `SELECT msgtype,provider_payload::text FROM message_archive_messages WHERE msgid='legacy-file'`).Scan(&messageType, &providerPayload); err != nil || messageType != "file" || !strings.Contains(providerPayload, "sdk-file") {
		t.Fatalf("unknown attachment msgtype=%q provider_payload=%q err=%v", messageType, providerPayload, err)
	}
}

func archiveLegacySourcePool(t *testing.T) (*pgxpool.Pool, func(), string) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
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
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_legacy_archive_source_" + hex.EncodeToString(random)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal("create legacy archive source schema")
	}
	sourceURL := sourceURLForSchema(t, databaseURL, schema)
	sourceConfig, err := pgxpool.ParseConfig(sourceURL)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	source, err := pgxpool.NewWithConfig(ctx, sourceConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatal("open legacy archive source schema")
	}
	var currentSchema string
	if err = source.QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil || currentSchema != schema {
		source.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatalf("source search_path=%q err=%v", currentSchema, err)
	}
	return source, func() {
		source.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}, sourceURL
}

func sourceURLForSchema(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("options", "-c search_path="+schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func archiveMigrationManifest(t *testing.T) archivemigration.Manifest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": archivemigration.SchemaVersion,
		"source_name":    "archive-migration-integration",
		"corp_scope":     "wecom-corp:wx-archive-integration",
		"records": []map[string]any{
			{"source_row_key": "row-known", "seq": 1, "msgid": "m-known", "historical_unionid": "union-known", "historical_group_name": "Legacy group", "payload": map[string]any{"msgid": "m-known", "from": "staff-one", "tolist": []string{"wm_known"}, "roomid": "wr-legacy", "msgtype": "text", "msgtime": 1788336000, "text": map[string]string{"content": "known"}}},
			{"source_row_key": "row-never-one", "seq": 2, "msgid": "m-never-one", "payload": map[string]any{"msgid": "m-never-one", "from": "staff-one", "tolist": []string{"wm_never_one"}, "msgtype": "text", "msgtime": 1788336060, "text": map[string]string{"content": "never one"}}},
			{"source_row_key": "row-never-two", "seq": 3, "msgid": "m-never-two", "payload": map[string]any{"msgid": "m-never-two", "from": "staff-one", "tolist": []string{"wm_never_two"}, "msgtype": "text", "msgtime": 1788336120, "text": map[string]string{"content": "never two"}}},
			{"source_row_key": "row-later", "seq": 4, "msgid": "m-later", "payload": map[string]any{"msgid": "m-later", "from": "staff-one", "tolist": []string{"wm_later"}, "msgtype": "text", "msgtime": 1788336180, "text": map[string]string{"content": "later"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := archivemigration.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func archiveMigrationSequenceConflictManifest(t *testing.T) archivemigration.Manifest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": archivemigration.SchemaVersion,
		"source_name":    "archive-migration-sequence-conflict",
		"corp_scope":     "wecom-corp:wx-archive-integration",
		"records": []map[string]any{
			{"source_row_key": "row-sequence-conflict", "seq": 99, "msgid": "m-known", "payload": map[string]any{"msgid": "m-known", "from": "staff-one", "tolist": []string{"wm_known"}, "msgtype": "text", "msgtime": 1788336000, "text": map[string]string{"content": "known"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := archivemigration.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func archiveMigrationProjectionConflictManifest(t *testing.T) archivemigration.Manifest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": archivemigration.SchemaVersion,
		"source_name":    "archive-migration-projection-conflict",
		"corp_scope":     "wecom-corp:wx-archive-integration",
		"records": []map[string]any{
			{"source_row_key": "row-projection-conflict", "seq": 1, "msgid": "m-known", "historical_unionid": "union-known", "historical_group_name": "Changed legacy group", "payload": map[string]any{"msgid": "m-known", "from": "staff-one", "tolist": []string{"wm_known"}, "roomid": "wr-legacy", "msgtype": "text", "msgtime": 1788336000, "text": map[string]string{"content": "known"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := archivemigration.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func seedArchiveMigrationIdentity(ctx context.Context, native *pgxpool.Pool, externalUserID string) error {
	var customerID int64
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		return err
	}
	_, err := native.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:wx-archive-integration',$2,'verified','integration',1,'active',clock_timestamp())`, customerID, externalUserID)
	return err
}

func assertArchiveMigrationCounts(t *testing.T, ctx context.Context, native *pgxpool.Pool, messages, receipts, runs int) {
	t.Helper()
	var actualMessages, actualReceipts, actualRuns int
	if err := native.QueryRow(ctx, `SELECT count(*) FROM message_archive_messages`).Scan(&actualMessages); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `SELECT count(*) FROM message_archive_migration_receipts`).Scan(&actualReceipts); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `SELECT count(*) FROM message_archive_migration_runs`).Scan(&actualRuns); err != nil {
		t.Fatal(err)
	}
	if actualMessages != messages || actualReceipts != receipts || actualRuns != runs {
		t.Fatalf("counts messages=%d receipts=%d runs=%d", actualMessages, actualReceipts, actualRuns)
	}
}

func archiveMigrationIntegrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
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
	schema := "aicrm_message_archive_test_" + hex.EncodeToString(random)
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
	for _, path := range archiveMigrationPaths(t) {
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
			t.Fatalf("apply archive migration %s: %v", filepath.Base(path), execErr)
		}
	}
	applyArchiveAccessLoginFixture(t, ctx, native)
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `WITH account AS (
		INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at)
		VALUES('archive-staff','$argon2id$integration','Archive Staff','staff-one',TRUE,FALSE,NULL)
		RETURNING id
	) INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account`); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	var ungrantedStaff int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM admin_users WHERE username='archive-staff' AND login_enabled=FALSE AND access_granted_at IS NULL`).Scan(&ungrantedStaff); err != nil || ungrantedStaff != 1 {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatalf("archive fixture staff login grant count=%d err=%v", ungrantedStaff, err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

// The archive fixture owns only the migrations required for this historical
// importer. It still calls the current Access read port, so it declares the
// 0152 login-column contract while keeping this business staff record
// explicitly ungranted and unable to log in.
func applyArchiveAccessLoginFixture(t *testing.T, ctx context.Context, database *pgxpool.Pool) {
	t.Helper()
	if _, err := database.Exec(ctx, `ALTER TABLE admin_users
		ADD COLUMN login_enabled BOOLEAN NOT NULL DEFAULT FALSE,
		ADD COLUMN legacy_login_reactivation_pending BOOLEAN NOT NULL DEFAULT FALSE,
		ADD COLUMN access_granted_at TIMESTAMPTZ,
		ADD CONSTRAINT ck_admin_users_login_requires_access_grant CHECK (access_granted_at IS NOT NULL OR login_enabled = FALSE)`); err != nil {
		t.Fatalf("apply current Access login fixture contract: %v", err)
	}
}

func archiveMigrationPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate archive migration integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return []string{
		filepath.Join(root, "migrations", "0001_platform.sql"),
		filepath.Join(root, "migrations", "0002_identity.sql"),
		filepath.Join(root, "migrations", "0003_access.sql"),
		filepath.Join(root, "migrations", "0071_message_archive_core.sql"),
		filepath.Join(root, "migrations", "0072_message_archive_migration_receipts.sql"),
		filepath.Join(root, "migrations", "0098_message_archive_historical_projection.sql"),
	}
}
