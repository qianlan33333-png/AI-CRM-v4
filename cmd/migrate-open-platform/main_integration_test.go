package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestOpenPlatformHistoryCLIPostgreSQLJourney is a complete offline rehearsal:
// it reads only the donor metadata columns under repeatable-read, seals a
// 0600 snapshot, imports disabled/reissue-required clients, records excluded
// grants and legacy audit facts, then proves replay and same-revision drift.
func TestOpenPlatformHistoryCLIPostgreSQLJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sourceURL, targetURL, cleanup := openPlatformHistoryDatabases(t, ctx)
	defer cleanup()
	seedOpenPlatformHistorySource(t, ctx, sourceURL)
	migrateOpenPlatformHistoryTarget(t, ctx, targetURL)

	// The target happens to have customers.id=42; donor local IDs remain pending.
	targetSeed, seedErr := pgxpool.New(ctx, targetURL)
	if seedErr != nil {
		t.Fatal(seedErr)
	}
	if _, seedErr = targetSeed.Exec(ctx, `INSERT INTO customers(id,status,version,lineage_version) OVERRIDING SYSTEM VALUE VALUES(42,'active',1,1)`); seedErr != nil {
		targetSeed.Close()
		t.Fatal(seedErr)
	}
	targetSeed.Close()

	directory := t.TempDir()
	keyPath := filepath.Join(directory, "snapshot.key")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(directory, "auth-history.bin")
	const revision = "dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f"
	t.Setenv("AICRM_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("AICRM_DATABASE_URL", targetURL)
	if err := run(ctx, []string{"-mode", "extract", "-snapshot", snapshotPath, "-snapshot-key-file", keyPath, "-source-revision", revision}); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) == "" || containsBytes(sealed, []byte("source-secret-must-never-export")) {
		t.Fatal("protected snapshot exposed donor secret")
	}
	snapshot, digest, err := loadFile(snapshotPath, keyPath)
	if err != nil || len(snapshot.Clients) != 5 || len(snapshot.Audits) != 1 {
		t.Fatalf("snapshot clients=%d audits=%d err=%v", len(snapshot.Clients), len(snapshot.Audits), err)
	}
	if err = run(ctx, []string{"-mode", "apply", "-snapshot", snapshotPath, "-snapshot-key-file", keyPath, "-manifest-sha256", fmt.Sprintf("%x", digest), "-confirm-apply"}); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"-mode", "verify", "-snapshot", snapshotPath, "-snapshot-key-file", keyPath, "-manifest-sha256", fmt.Sprintf("%x", digest)}); err != nil {
		t.Fatal(err)
	}
	// Exact replay has no new credential/audit facts.
	if err = run(ctx, []string{"-mode", "apply", "-snapshot", snapshotPath, "-snapshot-key-file", keyPath, "-manifest-sha256", fmt.Sprintf("%x", digest), "-confirm-apply"}); err != nil {
		t.Fatal(err)
	}

	target, err := pgxpool.New(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var enabled, reissue bool
	var scopes, capabilities []string
	var corp string
	if err = target.QueryRow(ctx, `SELECT enabled,reissue_required,scopes,COALESCE((SELECT array_agg(capability ORDER BY capability) FROM access_machine_client_grants WHERE machine_client_id=c.id), '{}'),corp_id FROM access_machine_clients c WHERE client_id='historic.mcp'`).Scan(&enabled, &reissue, &scopes, &capabilities, &corp); err != nil {
		t.Fatal(err)
	}
	if enabled || !reissue || len(scopes) != 1 || scopes[0] != "read" || len(capabilities) != 1 || capabilities[0] != "mcp_read" || corp != "source-corp" {
		t.Fatalf("historical client enabled=%v reissue=%v scopes=%v capabilities=%v corp=%q", enabled, reissue, scopes, capabilities, corp)
	}
	// The donor group-broadcast service profile is an external-integration
	// caller, while the old direct key maps to the fixed V3 direct-key record.
	var importedDirect int
	if err = target.QueryRow(ctx, `SELECT count(*) FROM access_machine_clients WHERE client_id IN ('historic.group','direct_external_api_key') AND enabled=false AND reissue_required=true`).Scan(&importedDirect); err != nil || importedDirect != 2 {
		t.Fatalf("system/direct migration count=%d err=%v", importedDirect, err)
	}
	var scopedOutcome, scopedReason string
	if err = target.QueryRow(ctx, `SELECT outcome,reason_code FROM access_machine_import_receipts WHERE source_row_id='auth_api_clients/historic.scoped'`).Scan(&scopedOutcome, &scopedReason); err != nil || scopedOutcome != "excluded" || scopedReason != "owner_scope_mapping_pending" {
		t.Fatalf("numeric owner-scope collision outcome=%q reason=%q err=%v", scopedOutcome, scopedReason, err)
	}
	var excluded, imported, audits int
	if err = target.QueryRow(ctx, `SELECT count(*) FILTER (WHERE outcome='excluded'),count(*) FILTER (WHERE outcome='reissue_required') FROM access_machine_import_receipts`).Scan(&excluded, &imported); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM access_machine_historical_audit_facts`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if excluded != 2 || imported != 3 || audits != 1 {
		t.Fatalf("receipts excluded=%d reissue=%d audit=%d", excluded, imported, audits)
	}

	service, closeService, err := machineHistoryService(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeService()
	// A later factual snapshot from the same frozen revision may add a new row
	// while reusing every existing global source receipt.
	overlapped := snapshot
	overlapped.Clients = append(overlapped.Clients, historicalClientRow{SourceRowID: "auth_api_clients/historic.new", ClientID: "historic.new", PrincipalID: "api_client:historic.new", PrincipalType: "api_client", DisplayName: "Historic new", Purpose: "mcp", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"mcp_read"}, CorpID: "source-corp", OwnerScope: map[string][]string{}, SourceAuthVersion: 1, TokenTTLSeconds: 1800})
	overlapped.Manifest.SnapshotAt = overlapped.Manifest.SnapshotAt.Add(time.Second)
	if err = populateManifest(&overlapped, revision); err != nil {
		t.Fatal(err)
	}
	_, overlapDigest, err := canonicalSnapshot(overlapped)
	if err != nil {
		t.Fatal(err)
	}
	result, err := applySnapshot(ctx, service, overlapped, overlapDigest)
	if err != nil || result.Imported != 1 || result.Replayed != 5 || result.AuditReplayed != 1 {
		t.Fatalf("overlapping snapshot result=%+v err=%v", result, err)
	}
	var batches, batchRows int
	if err = target.QueryRow(ctx, `SELECT count(*) FROM access_machine_import_batches`).Scan(&batches); err != nil || batches != 2 {
		t.Fatalf("snapshot batch count=%d err=%v", batches, err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM access_machine_import_batch_receipts`).Scan(&batchRows); err != nil || batchRows != 11 {
		t.Fatalf("snapshot receipt count=%d err=%v", batchRows, err)
	}

	// A changed source row has the same source namespace and record identity;
	// its fresh batch may exist for review, but it cannot alter the global fact.
	drifted := snapshot
	drifted.Clients[0].Scopes = []string{"read", "write"}
	drifted.Manifest.SnapshotAt = drifted.Manifest.SnapshotAt.Add(2 * time.Second)
	if err = populateManifest(&drifted, revision); err != nil {
		t.Fatal(err)
	}
	_, driftDigest, err := canonicalSnapshot(drifted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applySnapshot(ctx, service, drifted, driftDigest); err == nil {
		t.Fatal("cross-batch source row drift was accepted")
	}
}

func openPlatformHistoryDatabases(t *testing.T, ctx context.Context) (string, string, func()) {
	t.Helper()
	raw, configErr := platformconfig.DatabaseURL()
	if configErr != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Open Platform historical PostgreSQL journey")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	adminURL := *parsed
	adminURL.Path, adminURL.RawPath = "/postgres", ""
	admin, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	prefix := hex.EncodeToString(random[:])
	sourceName, targetName := "aicrm_open_source_"+prefix, "aicrm_open_target_"+prefix
	for _, name := range []string{sourceName, targetName} {
		if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			admin.Close(ctx)
			t.Fatal(err)
		}
	}
	makeURL := func(name string) string {
		value := *parsed
		value.Path, value.RawPath = "/"+name, ""
		return value.String()
	}
	return makeURL(sourceName), makeURL(targetName), func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, name := range []string{sourceName, targetName} {
			_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		}
		admin.Close(cleanup)
	}
}

func seedOpenPlatformHistorySource(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE TABLE auth_api_clients (client_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL,principal_type TEXT NOT NULL,purpose TEXT NOT NULL,display_name TEXT NOT NULL,secret_hash TEXT NOT NULL,audiences_json JSONB NOT NULL,scopes_json JSONB NOT NULL,capabilities_json JSONB NOT NULL,allowed_cidrs_json JSONB NOT NULL,corp_id TEXT NOT NULL,owner_scope_json JSONB NOT NULL,auth_version BIGINT NOT NULL,token_ttl_seconds INTEGER NOT NULL,enabled BOOLEAN NOT NULL); CREATE TABLE admin_operation_logs (id BIGSERIAL PRIMARY KEY,operator TEXT NOT NULL,action_type TEXT NOT NULL,target_type TEXT NOT NULL,target_id TEXT NOT NULL,before_json JSONB NOT NULL,after_json JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL); INSERT INTO auth_api_clients(client_id,principal_id,principal_type,purpose,display_name,secret_hash,audiences_json,scopes_json,capabilities_json,allowed_cidrs_json,corp_id,owner_scope_json,auth_version,token_ttl_seconds,enabled) VALUES ('historic.mcp','api_client:historic.mcp','api_client','mcp','Historic MCP','source-secret-must-never-export','["external_integration"]','["read"]','["mcp_read"]','["203.0.113.0/24"]','source-corp','{}',9,1800,true),('historic.scoped','api_client:historic.scoped','api_client','mcp','Historic scoped MCP','scoped-secret','["external_integration"]','["read"]','["mcp_read"]','[]','source-corp','{"customer_id":["42"]}',2,1800,false),('historic.group','service:group_broadcast','service','group_broadcast','Historic group broadcast','group-secret','["external_integration"]','["write"]','["group_broadcast_execute"]','[]','source-corp','{}',1,1800,false),('aicrm-direct-external-api-key','api_client:aicrm-direct-external-api-key','api_client','external_agent','CRM 开放 API Key','direct-secret','["external_integration"]','["read"]','["external_read"]','[]','source-corp','{}',4,1800,true),('historic.unsupported','api_client:historic.unsupported','api_client','internal_worker','Unsupported caller','another-secret','["external_integration"]','["read"]','["external_read"]','[]','source-corp','{}',1,1800,false); INSERT INTO admin_operation_logs(operator,action_type,target_type,target_id,before_json,after_json,created_at) VALUES('crm_console','api_client_disabled','api_client','historic.mcp','{"enabled":true}','{"enabled":false}',TIMESTAMPTZ '2026-09-05T01:02:03Z')`)
	if err != nil {
		t.Fatal(err)
	}
}

func migrateOpenPlatformHistoryTarget(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migrations")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{"0002_identity.sql", "0003_access.sql", "0096_open_platform.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func containsBytes(haystack, needle []byte) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if string(haystack[index:index+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
