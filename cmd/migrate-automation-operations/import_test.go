package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type resolverStub struct {
	results map[string]identityport.ResolveResult
	err     error
}

func (stub resolverStub) Resolve(_ context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	if stub.err != nil {
		return identityport.ResolveResult{}, stub.err
	}
	result, ok := stub.results[string(reference.Kind)+"|"+reference.Scope+"|"+reference.Value]
	if !ok {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return result, nil
}

func TestResolveOneIDNeverUsesSourceCustomerID(t *testing.T) {
	resolver := resolverStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:wx-open|union-1": {Status: identityport.ResolveFound, CustomerID: customerdomain.CustomerID(91)},
	}}
	id, disposition, err := resolveOneID(context.Background(), resolver, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "union-1", Assurance: "verified", Source: "provider-history:v2"}})
	if err != nil || id != 91 || disposition != "mapped" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
	id, disposition, err = resolveOneID(context.Background(), resolver, nil)
	if err != nil || id != 0 || disposition != "unresolved" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
}

func TestResolveOneIDConflictsAcrossCanonicalRoots(t *testing.T) {
	resolver := resolverStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:wx-open|union-1":  {Status: identityport.ResolveFound, CustomerID: 91},
		"wecom_external_userid|wecom-corp:corp-1|ext-1": {Status: identityport.ResolveFound, CustomerID: 92},
	}}
	rows := []identityRow{
		{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "union-1", Assurance: "verified", Source: "provider-history:v2"},
		{Kind: "wecom_external_userid", Scope: "wecom-corp:corp-1", Value: "ext-1", Assurance: "verified", Source: "provider-history:v2"},
	}
	if id, disposition, err := resolveOneID(context.Background(), resolver, rows); err != nil || id != 0 || disposition != "conflict" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
}

func TestResolveOneIDRejectsUnverifiedInvalidAndPropagatesStoreFailure(t *testing.T) {
	if _, disposition, err := resolveOneID(context.Background(), resolverStub{}, []identityRow{{Kind: "unionid", Scope: "", Value: "x", Assurance: "verified", Source: "provider-history:v2"}}); err != nil || disposition != "invalid" {
		t.Fatalf("disposition=%s err=%v", disposition, err)
	}
	if _, disposition, err := resolveOneID(context.Background(), resolverStub{}, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "x", Assurance: "declared", Source: "provider-history:v2"}}); err != nil || disposition != "unresolved" {
		t.Fatalf("disposition=%s err=%v", disposition, err)
	}
	want := errors.New("store unavailable")
	if _, _, err := resolveOneID(context.Background(), resolverStub{err: want}, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "x", Assurance: "verified", Source: "provider-history:v2"}}); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestExpectedQuarantineReasonReplaysEveryImporterDisposition(t *testing.T) {
	int64Pointer := func(value int64) *int64 { return &value }
	packageMapped := map[string]sourceReceipt{
		receiptKey("audience_packages", "10"): {targetTable: "segment_audience_packages", targetPK: int64Pointer(99), disposition: "imported"},
	}
	tests := []struct {
		name     string
		source   frozenSourceRow
		receipt  sourceReceipt
		receipts map[string]sourceReceipt
		want     string
	}{
		{
			name:    "invalid agent",
			source:  frozenSourceRow{table: "automation_agents", pk: "0", raw: json.RawMessage(`{"id":0}`)},
			receipt: sourceReceipt{disposition: "invalid"},
			want:    "invalid_agent",
		},
		{
			name:    "agent code conflict",
			source:  frozenSourceRow{table: "automation_agents", pk: "1", raw: json.RawMessage(`{"id":1,"agent_code":"agent","published_version":1,"fixed_content_package_json":{}}`)},
			receipt: sourceReceipt{disposition: "conflict"},
			want:    "agent_code_digest_conflict",
		},
		{
			name:    "invalid configuration definition",
			source:  frozenSourceRow{table: "audience_configuration_versions", pk: "10:1", raw: json.RawMessage(`{"package_id":10,"version":1,"definition":"not-an-object"}`)},
			receipt: sourceReceipt{disposition: "invalid"},
			want:    "invalid_definition",
		},
		{
			name:    "unresolved binding reference",
			source:  frozenSourceRow{table: "audience_bindings", pk: "10", raw: json.RawMessage(`{"package_id":10,"automation_agent_id":20}`)},
			receipt: sourceReceipt{disposition: "unresolved"},
			want:    "binding_reference_unresolved",
		},
		{
			name:     "sender package unresolved",
			source:   frozenSourceRow{table: "audience_senders", pk: "10:1", raw: json.RawMessage(`{"package_id":10,"sender_userid":"staff","sort_order":1,"is_enabled":true}`)},
			receipt:  sourceReceipt{disposition: "unresolved"},
			receipts: map[string]sourceReceipt{},
			want:     "sender_package_unresolved",
		},
		{
			name:     "sender identity unresolved",
			source:   frozenSourceRow{table: "audience_senders", pk: "10:1", raw: json.RawMessage(`{"package_id":10,"sender_userid":"staff","sort_order":1,"is_enabled":true}`)},
			receipt:  sourceReceipt{disposition: "unresolved"},
			receipts: packageMapped,
			want:     "sender_identity_unresolved",
		},
		{
			name:     "sender set incomplete",
			source:   frozenSourceRow{table: "audience_senders", pk: "10:1", raw: json.RawMessage(`{"package_id":10,"sender_userid":"staff","sort_order":1,"is_enabled":true}`)},
			receipt:  sourceReceipt{disposition: "quarantine"},
			receipts: packageMapped,
			want:     "sender_set_incomplete",
		},
		{
			name:    "member oneid conflict",
			source:  frozenSourceRow{table: "audience_members", pk: "10:1", raw: json.RawMessage(`{"segment_id":10,"customer_id":1}`)},
			receipt: sourceReceipt{disposition: "conflict"},
			want:    "oneid_conflict",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := expectedQuarantineReason(test.source, test.receipt, test.receipts)
			if err != nil || got != test.want {
				t.Fatalf("reason=%q err=%v want=%q", got, err, test.want)
			}
		})
	}
}

func TestFrozenRefreshConfigurationPreservesDonorModesAndLegacySnapshots(t *testing.T) {
	definition := `{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`
	tests := []struct {
		name     string
		refresh  string
		wantMode string
		wantCron string
		wantErr  bool
	}{
		{name: "manual", refresh: `"refresh_mode":"manual"`, wantMode: "manual"},
		{name: "every three minutes", refresh: `"refresh_mode":"incremental_3m"`, wantMode: "every_3m"},
		{name: "daily", refresh: `"refresh_mode":"daily_0200"`, wantMode: "daily_0200"},
		{name: "combined", refresh: `"refresh_mode":"incremental_3m_plus_daily_0200"`, wantMode: "every_3m_plus_daily_0200"},
		{name: "prior scheduled custom cron", refresh: `"refresh_mode":"scheduled","refresh_cron":"0 2 * * *"`, wantMode: "legacy_custom", wantCron: "0 2 * * *"},
		{name: "mode absent prior snapshot", refresh: `"refresh_cron":"0 2 * * *"`, wantMode: "legacy_custom", wantCron: "0 2 * * *"},
		{name: "mode and cron absent prior snapshot", refresh: `"refresh_cron":null`, wantMode: "legacy_custom"},
		{name: "unknown is not manual", refresh: `"refresh_mode":"hourly"`, wantErr: true},
		{name: "automatic mode cannot carry arbitrary cron", refresh: `"refresh_mode":"manual","refresh_cron":"0 2 * * *"`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var row configRow
			raw := fmt.Sprintf(`{"package_id":10,"version":1,"definition":%s,%s,"created_at":"2026-09-04T00:00:00Z"}`, definition, test.refresh)
			if err := json.Unmarshal([]byte(raw), &row); err != nil {
				t.Fatal(err)
			}
			got, err := frozenRefreshConfiguration(row)
			if test.wantErr {
				if err == nil {
					t.Fatalf("refresh=%+v accepted", row)
				}
				return
			}
			if err != nil || got.Mode != test.wantMode || got.Cron != test.wantCron {
				t.Fatalf("got=%+v err=%v want=%s/%q", got, err, test.wantMode, test.wantCron)
			}
		})
	}
}

func TestImportDryRunApplyReplayAndReconcilePostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "automationops_migration_" + hex.EncodeToString(random)
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyPlatformSQL(t, ctx, pool)
	applyRiverSchema(t, ctx, pool)
	var actorID, customerID, otherCustomerID int64
	seed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Rollback(ctx)
	if err = seed.QueryRow(ctx, `INSERT INTO admin_users(id,username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at,created_at,updated_at) OVERRIDING SYSTEM VALUE VALUES(42,'migration-admin','$argon2id$fixture','Migration Admin','staff-provider-1',true,true,clock_timestamp(),clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES($1,'super_admin')`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(ctx, `INSERT INTO access_super_admin_control(singleton,admin_user_id,version,updated_at) VALUES(TRUE,$1,1,clock_timestamp())`, actorID); err != nil {
		t.Fatal(err)
	}
	if err = seed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO customers(status,created_at,updated_at) VALUES('active',clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO customers(status,created_at,updated_at) VALUES('active',clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&otherCustomerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at,created_at,updated_at) VALUES($1,'unionid','wechat-open-platform:fixture','union-fixture','verified','fixture',1,'active',clock_timestamp(),clock_timestamp(),clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	snapshot := migrationFixture(t)
	keyFile := filepath.Join(t.TempDir(), "snapshot.key")
	snapshotFile := filepath.Join(t.TempDir(), "snapshot.enc")
	if err = generateKey(keyFile); err != nil {
		t.Fatal(err)
	}
	if err = writeEncryptedSnapshot(snapshotFile, keyFile, snapshot); err != nil {
		t.Fatal(err)
	}
	cliURL := databaseURLWithSearchPath(t, url, schema)
	// The command deliberately accepts only the canonical target role (or the
	// designated legacy source role), never an arbitrary environment variable.
	// Override the canonical value with this isolated schema so the real CLI
	// journey exercises that allowlist rather than bypassing it.
	t.Setenv("AICRM_DATABASE_URL", cliURL)
	commandArgs := []string{"--actor-id", strconv.FormatInt(actorID, 10), "--snapshot-file", snapshotFile, "--key-file", keyFile, "--timeout", "30s"}
	report := executeImportCommand(t, "dry-run", commandArgs...)
	if report.Tables["audience_members"].Mapped != 1 || report.Tables["audience_members"].Unresolved != 1 {
		t.Fatalf("dry-run report=%+v", report)
	}
	var batches int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM automation_operations_migration_batches`).Scan(&batches); err != nil || batches != 0 {
		t.Fatalf("dry-run batches=%d err=%v", batches, err)
	}
	assertImportedSegmentActors(t, ctx, pool, actorID, 0)
	report = executeImportCommand(t, "apply", append(commandArgs, "--confirm-import")...)
	assertImportedSegmentActors(t, ctx, pool, actorID, 1)
	if report.ProviderEffectsCreated != 0 || report.RiverJobsCreated != 0 {
		t.Fatalf("side effects=%+v", report)
	}
	// Re-applying the same frozen snapshot is the operator's recovery path.
	// It must only load the prior source receipts: no history row may be
	// rewritten and it must not create River work or a Provider effect.
	replay := executeImportCommand(t, "replay-check", commandArgs...)
	assertImportedSegmentActors(t, ctx, pool, actorID, 1)
	if replay.Tables["audience_members"].Mapped != 1 || replay.Tables["audience_members"].Unresolved != 1 {
		t.Fatalf("replay=%+v", replay)
	}
	if replay.ProviderEffectsCreated != 0 || replay.RiverJobsCreated != 0 {
		t.Fatalf("replay side effects=%+v", replay)
	}
	var lifecycle string
	var member int64
	if err = pool.QueryRow(ctx, `SELECT p.lifecycle,m.customer_id FROM segment_audience_packages p JOIN segment_audience_snapshot_members m ON m.snapshot_id=p.published_snapshot_id WHERE p.code='v2-audience-10'`).Scan(&lifecycle, &member); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "paused" || member != customerID {
		t.Fatalf("lifecycle=%s member=%d want=%d", lifecycle, member, customerID)
	}
	var historyRows, readOnlyRows, replayableRows, effectDigests, effects, riverJobs int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE read_only),count(*) FILTER (WHERE replayable),count(*) FILTER (WHERE source_effect_digest IS NOT NULL) FROM automation_operations_legacy_history`).Scan(&historyRows, &readOnlyRows, &replayableRows, &effectDigests); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&riverJobs); err != nil {
		t.Fatal(err)
	}
	if historyRows != 4 || readOnlyRows != 4 || replayableRows != 0 || effectDigests != 1 || effects != 0 || riverJobs != 0 {
		t.Fatalf("legacy history rows=%d readonly=%d replayable=%d effects=%d external=%d river=%d", historyRows, readOnlyRows, replayableRows, effectDigests, effects, riverJobs)
	}
	var quarantines int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM automation_operations_migration_quarantine`).Scan(&quarantines); err != nil || quarantines != 2 {
		t.Fatalf("quarantines=%d err=%v", quarantines, err)
	}

	// The reconciliation command must bind the encrypted snapshot to the exact
	// stored manifest. Matching donor commit and watermark alone are not enough.
	if _, err = Reconcile(ctx, pool, report.BatchKey, alteredFrozenSnapshot(t, snapshot)); err == nil {
		t.Fatal("expected mismatched frozen snapshot rejection")
	}
	assertBatchStatus(t, ctx, pool, report.BatchKey, "imported")

	// The database guards these facts append-only. Disable those test-only
	// guards to prove Reconcile detects a privileged/manual drift before it can
	// stamp the batch reconciled, then restore each fact for the final CLI path.
	disableTestAppendOnlyGuards(t, ctx, pool)
	defer restoreTestAppendOnlyGuards(t, ctx, pool)

	originalHistory := loadHistoryFixture(t, ctx, pool, report.BatchKey, "automation_enrollments")
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET source_state='tampered' WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET source_state=$2 WHERE id=$1`, originalHistory.ID, originalHistory.SourceState); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET occurred_at=occurred_at + interval '1 second' WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET occurred_at=$2 WHERE id=$1`, originalHistory.ID, originalHistory.OccurredAt); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `ALTER TABLE automation_operations_legacy_history DROP CONSTRAINT automation_operations_legacy_history_read_only_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET read_only=false WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET read_only=true WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE automation_operations_legacy_history ADD CONSTRAINT automation_operations_legacy_history_read_only_check CHECK(read_only)`); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `DELETE FROM automation_operations_legacy_history WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	restoreHistoryFixture(t, ctx, pool, originalHistory)

	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshot_members SET customer_id=$2 WHERE customer_id=$1`, customerID, otherCustomerID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshot_members SET customer_id=$2 WHERE customer_id=$1`, otherCustomerID, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshots SET reference_time=reference_time + interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshots SET reference_time=$1`, snapshot.Manifest.SnapshotAt); err != nil {
		t.Fatal(err)
	}

	// Current target configuration is not proven by a preserved source digest
	// alone. Each imported target association must be present and retain the
	// immutable facts the importer declared.
	if _, err = pool.Exec(ctx, `UPDATE automation_agents SET published_task_prompt='tampered' WHERE agent_code='migrated-fixed'`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_agents SET published_task_prompt='' WHERE agent_code='migrated-fixed'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_groups SET sort_order=99 WHERE name='Migrated'`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_groups SET sort_order=1 WHERE name='Migrated'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_configuration_versions SET definition='{"schema_version":1,"template_key":"tampered","parameters":{"within_days":"30"}}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_configuration_versions SET definition='{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_automation_binding_versions SET agent_published_version=2`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_automation_binding_versions SET agent_published_version=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_sender_set_members SET staff_id=99999`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_sender_set_members SET staff_id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	var senderSetID, senderStaffID, senderEligibilityVersion int64
	var senderPosition int16
	var senderEligibilityRefreshed time.Time
	if err = pool.QueryRow(ctx, `SELECT sender_set_id,sort_order,staff_id,eligibility_version,eligibility_refreshed_at FROM segment_audience_sender_set_members`).Scan(&senderSetID, &senderPosition, &senderStaffID, &senderEligibilityVersion, &senderEligibilityRefreshed); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM segment_audience_sender_set_members WHERE sender_set_id=$1 AND sort_order=$2`, senderSetID, senderPosition); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `INSERT INTO segment_audience_sender_set_members(sender_set_id,sort_order,staff_id,eligibility_version,eligibility_refreshed_at) VALUES($1,$2,$3,$4,$5)`, senderSetID, senderPosition, senderStaffID, senderEligibilityVersion, senderEligibilityRefreshed); err != nil {
		t.Fatal(err)
	}

	originalQuarantine := loadQuarantineFixture(t, ctx, pool, report.BatchKey, "audience_members")
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_migration_quarantine SET reason_code='tampered' WHERE id=$1`, originalQuarantine.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_migration_quarantine SET reason_code=$2 WHERE id=$1`, originalQuarantine.ID, originalQuarantine.ReasonCode); err != nil {
		t.Fatal(err)
	}
	originalAgentQuarantine := loadQuarantineFixture(t, ctx, pool, report.BatchKey, "automation_agents")
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_migration_quarantine SET reason_code='tampered' WHERE id=$1`, originalAgentQuarantine.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_migration_quarantine SET reason_code=$2 WHERE id=$1`, originalAgentQuarantine.ID, originalAgentQuarantine.ReasonCode); err != nil {
		t.Fatal(err)
	}

	var cliOutput bytes.Buffer
	if err = execute([]string{"reconcile", "--batch-key", report.BatchKey, "--snapshot-file", snapshotFile, "--key-file", keyFile, "--timeout", "30s"}, &cliOutput); err != nil {
		t.Fatalf("actual reconcile command: %v", err)
	}
	assertBatchStatus(t, ctx, pool, report.BatchKey, "reconciled")
	shadow, err := Shadow(ctx, pool, report.BatchKey, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !shadow.ReadyForProbe || !shadow.ReadyForReconcile || !shadow.SnapshotManifestMatches || len(shadow.Packages) != 1 || !shadow.Packages[0].StructureMatches || !shadow.Packages[0].MemberDigestMatches || shadow.Packages[0].IsolatedMemberRows != 1 || len(shadow.Targets) != 5 || !allReadyTargets(shadow.Targets) || len(shadow.Quarantines) != 2 || !hasReadyQuarantine(shadow.Quarantines, "automation_agents") || !hasReadyQuarantine(shadow.Quarantines, "audience_members") || len(shadow.History) != 4 {
		t.Fatalf("shadow=%+v", shadow)
	}
	for _, item := range shadow.History {
		if !item.Ready || !item.StateMatches || !item.OccurredAtMatches || !item.ReadOnly || item.Replayable {
			t.Fatalf("history shadow=%+v", item)
		}
	}

	// A frozen historical-only batch has no current audience to probe, but its
	// every-row receipts and immutable history still permit reconciliation.
	historyOnly := historyOnlyMigrationSnapshot(t, snapshot, snapshot.Manifest.SnapshotAt.Add(time.Hour), "automation-history-only")
	historyOnlyFile := filepath.Join(t.TempDir(), "history-only.enc")
	if err = writeEncryptedSnapshot(historyOnlyFile, keyFile, historyOnly); err != nil {
		t.Fatal(err)
	}
	historyArgs := []string{"--actor-id", strconv.FormatInt(actorID, 10), "--snapshot-file", historyOnlyFile, "--key-file", keyFile, "--timeout", "30s"}
	historyReport := executeImportCommand(t, "apply", append(historyArgs, "--confirm-import")...)
	if _, err = Reconcile(ctx, pool, historyReport.BatchKey, historyOnly); err != nil {
		t.Fatalf("reconcile history-only batch: %v", err)
	}
	historyShadow, err := Shadow(ctx, pool, historyReport.BatchKey, historyOnly)
	if err != nil {
		t.Fatal(err)
	}
	if !historyShadow.ReadyForReconcile || historyShadow.ReadyForProbe || len(historyShadow.Packages) != 0 || len(historyShadow.Targets) != 0 || len(historyShadow.History) != 4 {
		t.Fatalf("history-only shadow=%+v", historyShadow)
	}

	// An audience whose every source member was intentionally isolated has a
	// published zero-member target and is still a complete, reconcilable batch.
	allIsolated := allMembersIsolatedMigrationSnapshot(t, snapshot, snapshot.Manifest.SnapshotAt.Add(2*time.Hour), "automation-members-isolated")
	allIsolatedFile := filepath.Join(t.TempDir(), "all-members-isolated.enc")
	if err = writeEncryptedSnapshot(allIsolatedFile, keyFile, allIsolated); err != nil {
		t.Fatal(err)
	}
	isolatedArgs := []string{"--actor-id", strconv.FormatInt(actorID, 10), "--snapshot-file", allIsolatedFile, "--key-file", keyFile, "--timeout", "30s"}
	isolatedReport := executeImportCommand(t, "apply", append(isolatedArgs, "--confirm-import")...)
	if _, err = Reconcile(ctx, pool, isolatedReport.BatchKey, allIsolated); err != nil {
		t.Fatalf("reconcile all-isolated members batch: %v", err)
	}
	isolatedShadow, err := Shadow(ctx, pool, isolatedReport.BatchKey, allIsolated)
	if err != nil {
		t.Fatal(err)
	}
	if !isolatedShadow.ReadyForReconcile || isolatedShadow.ReadyForProbe || len(isolatedShadow.Packages) != 1 || isolatedShadow.Packages[0].MappedMemberRows != 0 || isolatedShadow.Packages[0].IsolatedMemberRows != 1 || !isolatedShadow.Packages[0].MemberDigestMatches || len(isolatedShadow.Quarantines) != 2 {
		t.Fatalf("all-isolated shadow=%+v", isolatedShadow)
	}

	// The donor stored four closed schedule modes. They remain independent
	// configuration facts even when their audience definitions are byte-equal;
	// a definition-only lookup would collapse all four into legacy_custom.
	refreshModes := refreshModesMigrationSnapshot(t, snapshot, snapshot.Manifest.SnapshotAt.Add(3*time.Hour), "automation-refresh-modes")
	refreshModesFile := filepath.Join(t.TempDir(), "refresh-modes.enc")
	if err = writeEncryptedSnapshot(refreshModesFile, keyFile, refreshModes); err != nil {
		t.Fatal(err)
	}
	refreshArgs := []string{"--actor-id", strconv.FormatInt(actorID, 10), "--snapshot-file", refreshModesFile, "--key-file", keyFile, "--timeout", "30s"}
	refreshReport := executeImportCommand(t, "apply", append(refreshArgs, "--confirm-import")...)
	rows, err := pool.Query(ctx, `SELECT c.version,c.refresh_mode,COALESCE(c.refresh_cron_utc,'') FROM segment_audience_configuration_versions c JOIN segment_audience_packages p ON p.id=c.package_id WHERE p.code='v2-audience-11' ORDER BY c.version`)
	if err != nil {
		t.Fatal(err)
	}
	type refreshFact struct {
		version int64
		mode    string
		cron    string
	}
	var actualRefreshes []refreshFact
	for rows.Next() {
		var item refreshFact
		if err = rows.Scan(&item.version, &item.mode, &item.cron); err != nil {
			t.Fatal(err)
		}
		actualRefreshes = append(actualRefreshes, item)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	wantRefreshes := []refreshFact{{1, "manual", ""}, {2, "every_3m", ""}, {3, "daily_0200", ""}, {4, "every_3m_plus_daily_0200", ""}}
	if !reflect.DeepEqual(actualRefreshes, wantRefreshes) {
		t.Fatalf("imported refresh configurations=%+v want=%+v", actualRefreshes, wantRefreshes)
	}
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_configuration_versions SET refresh_mode='manual' WHERE id=(SELECT current_configuration_version_id FROM segment_audience_packages WHERE code='v2-audience-11')`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, refreshReport.BatchKey, refreshModes)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_configuration_versions SET refresh_mode='every_3m_plus_daily_0200' WHERE id=(SELECT current_configuration_version_id FROM segment_audience_packages WHERE code='v2-audience-11')`); err != nil {
		t.Fatal(err)
	}
	if _, err = Reconcile(ctx, pool, refreshReport.BatchKey, refreshModes); err != nil {
		t.Fatalf("reconcile refresh modes: %v", err)
	}
}

// Verify the existing import command retains the real target admin attribution
// across all five actor-bearing tables, rolls it back on dry-run, and preserves
// it on replay. An admin is deliberately not represented as a machine caller.
func assertImportedSegmentActors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, actorID int64, wantRows int) {
	t.Helper()
	for _, table := range []struct {
		name    string
		mutable bool
	}{
		{"segment_audience_groups", true},
		{"segment_audience_packages", true},
		{"segment_audience_configuration_versions", false},
		{"segment_audience_automation_binding_versions", false},
		{"segment_audience_sender_sets", false},
	} {
		predicate := "created_by=$1 AND created_actor_kind='admin' AND created_actor_ref=$2"
		if table.mutable {
			predicate += " AND updated_by=$1 AND updated_actor_kind='admin' AND updated_actor_ref=$2"
		}
		var total, attributed int
		query := "SELECT count(*),count(*) FILTER (WHERE " + predicate + ") FROM " + table.name
		if err := pool.QueryRow(ctx, query, actorID, fmt.Sprintf("admin:%d", actorID)).Scan(&total, &attributed); err != nil {
			t.Fatalf("%s actor readback: %v", table.name, err)
		}
		if total != wantRows || attributed != wantRows {
			t.Fatalf("%s rows=%d admin-attributed=%d want=%d", table.name, total, attributed, wantRows)
		}
	}
}

func applyPlatformSQL(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func applyRiverSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
}

func executeImportCommand(t *testing.T, command string, args ...string) Report {
	t.Helper()
	var output bytes.Buffer
	if err := execute(append([]string{command}, args...), &output); err != nil {
		t.Fatalf("actual %s command: %v", command, err)
	}
	var report Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode %s report: %v", command, err)
	}
	return report
}

func assertReconcileRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey string, snapshot segmentmigration.Snapshot) {
	t.Helper()
	if _, err := Reconcile(ctx, pool, batchKey, snapshot); err == nil {
		t.Fatal("expected reconciliation rejection")
	}
	assertBatchStatus(t, ctx, pool, batchKey, "imported")
}

func assertBatchStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey, want string) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM automation_operations_migration_batches WHERE batch_key=$1`, batchKey).Scan(&status); err != nil || status != want {
		t.Fatalf("batch status=%q want=%q err=%v", status, want, err)
	}
}

func alteredFrozenSnapshot(t *testing.T, source segmentmigration.Snapshot) segmentmigration.Snapshot {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var altered segmentmigration.Snapshot
	if err = json.Unmarshal(raw, &altered); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err = json.Unmarshal(altered.Tables["automation_actions"], &rows); err != nil || len(rows) == 0 {
		t.Fatalf("decode altered frozen snapshot: %v", err)
	}
	rows[0]["status"] = "tampered"
	updated, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	altered.Tables["automation_actions"] = updated
	digest := sha256.Sum256(updated)
	altered.Manifest.Digests["automation_actions"] = hex.EncodeToString(digest[:])
	if err = segmentmigration.ValidateSnapshot(altered); err != nil {
		t.Fatalf("altered snapshot must remain structurally valid: %v", err)
	}
	return altered
}

func disableTestAppendOnlyGuards(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, statement := range []string{
		`ALTER TABLE automation_operations_legacy_history DISABLE TRIGGER automation_operations_legacy_history_append_only`,
		`ALTER TABLE automation_operations_migration_quarantine DISABLE TRIGGER automation_operations_migration_quarantine_append_only`,
		`ALTER TABLE segment_audience_snapshot_members DISABLE TRIGGER segment_audience_snapshot_members_append_only`,
		`ALTER TABLE segment_audience_configuration_versions DISABLE TRIGGER segment_audience_configuration_append_only`,
		`ALTER TABLE segment_audience_automation_binding_versions DISABLE TRIGGER segment_audience_binding_append_only`,
		`ALTER TABLE segment_audience_sender_set_members DISABLE TRIGGER segment_audience_sender_members_append_only`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

func restoreTestAppendOnlyGuards(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, statement := range []string{
		`ALTER TABLE automation_operations_legacy_history ENABLE TRIGGER automation_operations_legacy_history_append_only`,
		`ALTER TABLE automation_operations_migration_quarantine ENABLE TRIGGER automation_operations_migration_quarantine_append_only`,
		`ALTER TABLE segment_audience_snapshot_members ENABLE TRIGGER segment_audience_snapshot_members_append_only`,
		`ALTER TABLE segment_audience_configuration_versions ENABLE TRIGGER segment_audience_configuration_append_only`,
		`ALTER TABLE segment_audience_automation_binding_versions ENABLE TRIGGER segment_audience_binding_append_only`,
		`ALTER TABLE segment_audience_sender_set_members ENABLE TRIGGER segment_audience_sender_members_append_only`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Error(err)
		}
	}
}

type historyFixture struct {
	ID                 int64
	BatchID            int64
	SourceSystem       string
	SourceTable        string
	SourcePK           string
	SourceState        string
	SourceEffectDigest []byte
	RecordDigest       []byte
	SafeSummary        string
	OccurredAt         time.Time
	ImportedAt         time.Time
	ReadOnly           bool
	Replayable         bool
}

func loadHistoryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey, sourceTable string) historyFixture {
	t.Helper()
	var value historyFixture
	err := pool.QueryRow(ctx, `SELECT h.id,h.batch_id,h.source_system,h.source_table,h.source_pk,h.source_state,h.source_effect_digest,h.record_digest,h.safe_summary::text,h.occurred_at,h.imported_at,h.read_only,h.replayable FROM automation_operations_legacy_history h JOIN automation_operations_migration_batches b ON b.id=h.batch_id WHERE b.batch_key=$1 AND h.source_table=$2`, batchKey, sourceTable).Scan(&value.ID, &value.BatchID, &value.SourceSystem, &value.SourceTable, &value.SourcePK, &value.SourceState, &value.SourceEffectDigest, &value.RecordDigest, &value.SafeSummary, &value.OccurredAt, &value.ImportedAt, &value.ReadOnly, &value.Replayable)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func restoreHistoryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, value historyFixture) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO automation_operations_legacy_history(id,batch_id,source_system,source_table,source_pk,source_state,source_effect_digest,record_digest,safe_summary,occurred_at,imported_at,read_only,replayable) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13)`, value.ID, value.BatchID, value.SourceSystem, value.SourceTable, value.SourcePK, value.SourceState, value.SourceEffectDigest, value.RecordDigest, value.SafeSummary, value.OccurredAt, value.ImportedAt, value.ReadOnly, value.Replayable); err != nil {
		t.Fatal(err)
	}
}

type quarantineFixture struct {
	ID           int64
	BatchID      int64
	SourceSystem string
	SourceTable  string
	SourcePK     string
	ReasonCode   string
	SafeSummary  string
	RecordDigest []byte
	CreatedAt    time.Time
}

func loadQuarantineFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey, sourceTable string) quarantineFixture {
	t.Helper()
	var value quarantineFixture
	err := pool.QueryRow(ctx, `SELECT q.id,q.batch_id,q.source_system,q.source_table,q.source_pk,q.reason_code,q.safe_summary::text,q.record_digest,q.created_at FROM automation_operations_migration_quarantine q JOIN automation_operations_migration_batches b ON b.id=q.batch_id WHERE b.batch_key=$1 AND q.source_table=$2`, batchKey, sourceTable).Scan(&value.ID, &value.BatchID, &value.SourceSystem, &value.SourceTable, &value.SourcePK, &value.ReasonCode, &value.SafeSummary, &value.RecordDigest, &value.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func hasReadyQuarantine(items []ShadowQuarantine, sourceTable string) bool {
	for _, item := range items {
		if item.SourceTable == sourceTable && item.Ready {
			return true
		}
	}
	return false
}

func allReadyTargets(items []ShadowTarget) bool {
	for _, item := range items {
		if !item.Ready || !item.Exists || !item.FactsMatch {
			return false
		}
	}
	return true
}

func derivedMigrationSnapshot(t *testing.T, source segmentmigration.Snapshot, at time.Time, watermarkSeed string) segmentmigration.Snapshot {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var derived segmentmigration.Snapshot
	if err = json.Unmarshal(raw, &derived); err != nil {
		t.Fatal(err)
	}
	derived.Manifest.SnapshotAt = at.UTC()
	watermark := sha256.Sum256([]byte(watermarkSeed))
	derived.Manifest.SourceWatermarkDigest = hex.EncodeToString(watermark[:])
	return derived
}

func sealMigrationSnapshot(t *testing.T, snapshot *segmentmigration.Snapshot) {
	t.Helper()
	for _, table := range segmentmigration.LogicalTables {
		var rows []json.RawMessage
		if err := json.Unmarshal(snapshot.Tables[table], &rows); err != nil {
			t.Fatal(err)
		}
		snapshot.Manifest.Counts[table] = len(rows)
		digest := sha256.Sum256(snapshot.Tables[table])
		snapshot.Manifest.Digests[table] = hex.EncodeToString(digest[:])
	}
	if err := segmentmigration.ValidateSnapshot(*snapshot); err != nil {
		t.Fatalf("derived migration snapshot: %v", err)
	}
}

func historyOnlyMigrationSnapshot(t *testing.T, source segmentmigration.Snapshot, at time.Time, watermarkSeed string) segmentmigration.Snapshot {
	t.Helper()
	snapshot := derivedMigrationSnapshot(t, source, at, watermarkSeed)
	for _, table := range []string{"audience_groups", "audience_packages", "audience_configuration_versions", "automation_agents", "audience_bindings", "audience_senders", "audience_members"} {
		snapshot.Tables[table] = json.RawMessage("[]")
	}
	sealMigrationSnapshot(t, &snapshot)
	return snapshot
}

func allMembersIsolatedMigrationSnapshot(t *testing.T, source segmentmigration.Snapshot, at time.Time, watermarkSeed string) segmentmigration.Snapshot {
	t.Helper()
	snapshot := derivedMigrationSnapshot(t, source, at, watermarkSeed)
	var members []json.RawMessage
	if err := json.Unmarshal(snapshot.Tables["audience_members"], &members); err != nil || len(members) != 2 {
		t.Fatalf("fixture members err=%v count=%d", err, len(members))
	}
	raw, err := json.Marshal([]json.RawMessage{members[1]})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Tables["audience_members"] = raw
	sealMigrationSnapshot(t, &snapshot)
	return snapshot
}

func refreshModesMigrationSnapshot(t *testing.T, source segmentmigration.Snapshot, at time.Time, watermarkSeed string) segmentmigration.Snapshot {
	t.Helper()
	snapshot := derivedMigrationSnapshot(t, source, at, watermarkSeed)
	definition := map[string]any{"schema_version": 1, "template_key": "active_contacts", "parameters": map[string]any{"within_days": "30"}}
	rows := map[string]any{
		"audience_groups":   []any{},
		"audience_packages": []any{map[string]any{"id": 11, "group_id": nil, "lifecycle": "active", "version": 4, "name": "Legacy refresh modes", "definition": definition, "refresh_mode": "incremental_3m_plus_daily_0200", "member_count": 0, "created_at": at, "updated_at": at}},
		"audience_configuration_versions": []any{
			map[string]any{"package_id": 11, "version": 1, "definition": definition, "refresh_mode": "manual", "created_at": at},
			map[string]any{"package_id": 11, "version": 2, "definition": definition, "refresh_mode": "incremental_3m", "created_at": at},
			map[string]any{"package_id": 11, "version": 3, "definition": definition, "refresh_mode": "daily_0200", "created_at": at},
			map[string]any{"package_id": 11, "version": 4, "definition": definition, "refresh_mode": "incremental_3m_plus_daily_0200", "created_at": at},
		},
		"automation_agents":          []any{},
		"audience_bindings":          []any{},
		"audience_senders":           []any{},
		"audience_members":           []any{},
		"automation_policies":        []any{},
		"automation_policy_versions": []any{},
		"automation_enrollments":     []any{},
		"automation_actions":         []any{},
	}
	for table, value := range rows {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Tables[table] = raw
	}
	sealMigrationSnapshot(t, &snapshot)
	return snapshot
}

func databaseURLWithSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := neturl.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	options := strings.TrimSpace(query.Get("options"))
	if options != "" {
		options += " "
	}
	query.Set("options", options+"-c search_path="+schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func migrationFixture(t *testing.T) segmentmigration.Snapshot {
	t.Helper()
	at := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	rows := map[string]any{
		"audience_groups":                 []any{map[string]any{"id": 1, "name": "Migrated", "sort_order": 1, "version": 1, "created_at": at, "updated_at": at}},
		"audience_packages":               []any{map[string]any{"id": 10, "group_id": 1, "lifecycle": "active", "version": 2, "name": "Legacy audience", "definition": map[string]any{"schema_version": 1, "template_key": "active_contacts", "parameters": map[string]any{"within_days": "30"}}, "refresh_mode": "manual", "member_count": 2, "created_at": at, "updated_at": at}},
		"audience_configuration_versions": []any{map[string]any{"package_id": 10, "version": 1, "definition": map[string]any{"schema_version": 1, "template_key": "active_contacts", "parameters": map[string]any{"within_days": "30"}}, "refresh_mode": "manual", "created_at": at}},
		"automation_agents":               []any{map[string]any{"id": 20, "agent_name": "Fixed", "agent_code": "migrated-fixed", "automation_type": "fixed_script", "status": "paused", "draft_role_prompt": "", "draft_task_prompt": "", "published_role_prompt": "", "published_task_prompt": "", "draft_version": 1, "published_version": 1, "fixed_content_package_json": map[string]any{"content_text": "hello", "image_library_ids": []int64{}, "miniprogram_library_ids": []int64{}, "attachment_library_ids": []int64{}, "group_invite_library_ids": []int64{}}, "created_at": at, "updated_at": at}, map[string]any{"id": 21, "agent_name": "Invalid historical agent", "agent_code": "", "automation_type": "fixed_script", "status": "paused", "draft_version": 0, "published_version": 0, "fixed_content_package_json": map[string]any{}, "created_at": at, "updated_at": at}},
		"audience_bindings":               []any{map[string]any{"package_id": 10, "automation_agent_id": 20, "version": 1, "created_at": at, "updated_at": at}},
		"audience_senders":                []any{map[string]any{"package_id": 10, "sender_userid": "staff-provider-1", "sort_order": 1, "is_enabled": true, "created_at": at, "updated_at": at}},
		"audience_members":                []any{map[string]any{"segment_id": 10, "customer_id": 999999, "computed_at": at, "identities": []any{map[string]any{"kind": "unionid", "scope": "wechat-open-platform:fixture", "value": "union-fixture", "assurance": "verified", "source": "fixture"}}}, map[string]any{"segment_id": 10, "customer_id": 999998, "computed_at": at, "identities": []any{}}},
		"automation_policies":             []any{map[string]any{"id": 30, "status": "active", "created_at": at}},
		"automation_policy_versions":      []any{map[string]any{"automation_id": 30, "version": 2, "status": "published", "published_at": at.Add(time.Minute)}},
		"automation_enrollments":          []any{map[string]any{"id": 40, "state": "sent", "external_effect_id": "legacy-effect-40", "enrolled_at": at.Add(2 * time.Minute)}},
		"automation_actions":              []any{map[string]any{"id": 50, "status": "failed", "completed_at": at.Add(3 * time.Minute)}},
	}
	snapshot := segmentmigration.Snapshot{Manifest: segmentmigration.Manifest{SourceSystem: "fixture", DonorCommit: segmentmigration.DonorCommit, SnapshotAt: at, SchemaDigest: strings.Repeat("1", 64), Counts: map[string]int{}, Digests: map[string]string{}}, Tables: map[string]json.RawMessage{}}
	for _, name := range segmentmigration.LogicalTables {
		raw, err := json.Marshal(rows[name])
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Tables[name] = raw
		var decoded []json.RawMessage
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		snapshot.Manifest.Counts[name] = len(decoded)
		digest := sha256.Sum256(raw)
		snapshot.Manifest.Digests[name] = hex.EncodeToString(digest[:])
	}
	watermark := sha256.Sum256([]byte("fixture"))
	snapshot.Manifest.SourceWatermarkDigest = hex.EncodeToString(watermark[:])
	return snapshot
}
