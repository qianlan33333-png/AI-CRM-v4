package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// This test only creates and retires disposable tables in its own schema in a
// guarded local test database. It never uses the application schema or data.
func TestRetirementRequiresBackupCalibrationAndExportsBeforeDropPostgreSQL(t *testing.T) {
	raw, configuredErr := platformconfig.DatabaseURL()
	if configuredErr != nil {
		t.Skip("AICRM_DATABASE_URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") || !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "aicrm_test_") {
		t.Fatal("isolated local test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema := "retire_fixture_" + time.Now().Format("150405000000000")
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = pool.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	fixture, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	_, err = fixture.Exec(ctx, `
 CREATE TABLE wecom_directory_publications(baseline_initialized bool,complete bool,full_observed_at timestamptz);
 INSERT INTO wecom_directory_publications VALUES(false,true,now());
 CREATE TABLE wecom_customer_sync_runs(status text);
 CREATE TABLE customer_owner_handoff_batches(mode text,state text);
 CREATE TABLE segment_audience_packages(lifecycle text,current_configuration_version_id bigint,published_snapshot_id bigint);
 CREATE TABLE segment_audience_configuration_versions(id bigint,definition jsonb);
 CREATE TABLE segment_audience_snapshots(id bigint,source_watermarks jsonb);
 INSERT INTO segment_audience_packages VALUES('active',1,1);
 INSERT INTO segment_audience_configuration_versions VALUES(1,'{"template_key":"paid_order"}');
 INSERT INTO segment_audience_snapshots VALUES(1,'[]');
 CREATE TABLE wecom_follow_relationships(customer_id bigint,employee_id text);
 INSERT INTO wecom_follow_relationships VALUES(2,'synthetic-b'),(1,'synthetic-a');
 CREATE TABLE customer_local_owners(customer_id bigint,staff_id bigint);
 INSERT INTO customer_local_owners VALUES(1,9);
 `)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "database.dump")
	command := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--file", backup, "--schema", schema, raw)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthetic backup: %v %s", err, output)
	}
	t.Setenv("AICRM_DATABASE_URL", u.String())
	privateDir := func() string { p := filepath.Join(t.TempDir(), "exports"); return p }
	if err = run(ctx, privateDir(), backup, "", true); err == nil {
		t.Fatal("uninitialized baseline allowed retirement")
	}
	if _, err = fixture.Exec(ctx, "UPDATE wecom_directory_publications SET baseline_initialized=true"); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, privateDir(), backup, "", true); err == nil {
		t.Fatal("uncalibrated audience allowed retirement")
	}
	if _, err = fixture.Exec(ctx, `UPDATE segment_audience_snapshots SET source_watermarks='[{"source":"wecom.directory.published.v2"}]'; INSERT INTO customer_owner_handoff_batches VALUES('local_only','executing')`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, privateDir(), backup, "", true); err == nil {
		t.Fatal("in-flight local ownership allowed retirement")
	}
	if _, err = fixture.Exec(ctx, `DELETE FROM customer_owner_handoff_batches; INSERT INTO wecom_customer_sync_runs VALUES('ingesting')`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, privateDir(), backup, "", true); err == nil {
		t.Fatal("in-flight refresh allowed retirement")
	}
	if _, err = fixture.Exec(ctx, "DELETE FROM wecom_customer_sync_runs"); err != nil {
		t.Fatal(err)
	}
	dry := privateDir()
	if err = run(ctx, dry, backup, "", false); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err = fixture.QueryRow(ctx, "SELECT to_regclass('wecom_follow_relationships') IS NOT NULL").Scan(&exists); err != nil || !exists {
		t.Fatalf("dry run modified tables: %v", err)
	}
	// Execution requires the reviewed dry-run receipt. Differences are detected
	// after the actual locked export, before either DROP is attempted.
	assertTables := func() {
		t.Helper()
		var present bool
		if e := fixture.QueryRow(ctx, "SELECT to_regclass('wecom_follow_relationships') IS NOT NULL AND to_regclass('customer_local_owners') IS NOT NULL").Scan(&present); e != nil || !present {
			t.Fatalf("rejected execution deleted a table: %v", e)
		}
	}
	if e := run(ctx, privateDir(), backup, "", true); e == nil {
		t.Fatal("execution without reviewed export receipt allowed")
	}
	assertTables()
	dryRaw, e := os.ReadFile(filepath.Join(dry, "export-receipt.json"))
	if e != nil {
		t.Fatal(e)
	}
	var wrong map[string]any
	if e = json.Unmarshal(dryRaw, &wrong); e != nil {
		t.Fatal(e)
	}
	wrong["database_backup_sha256"] = strings.Repeat("0", 64)
	wrongRaw, e := json.Marshal(wrong)
	if e != nil {
		t.Fatal(e)
	}
	wrongPath := filepath.Join(t.TempDir(), "wrong-backup-receipt.json")
	if e = os.WriteFile(wrongPath, wrongRaw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = run(ctx, privateDir(), backup, wrongPath, true); e == nil {
		t.Fatal("receipt for another backup allowed retirement")
	}
	assertTables()
	if _, e := fixture.Exec(ctx, "INSERT INTO customer_local_owners VALUES(2,10)"); e != nil {
		t.Fatal(e)
	}
	if e := run(ctx, privateDir(), backup, filepath.Join(dry, "export-receipt.json"), true); e == nil {
		t.Fatal("changed row count allowed retirement")
	}
	assertTables()
	if _, e := fixture.Exec(ctx, "DELETE FROM customer_local_owners WHERE customer_id=2; UPDATE wecom_follow_relationships SET employee_id='synthetic-changed' WHERE customer_id=1"); e != nil {
		t.Fatal(e)
	}
	if e := run(ctx, privateDir(), backup, filepath.Join(dry, "export-receipt.json"), true); e == nil {
		t.Fatal("changed content with same row count allowed retirement")
	}
	assertTables()
	if _, e := fixture.Exec(ctx, "UPDATE wecom_follow_relationships SET employee_id='synthetic-a' WHERE customer_id=1"); e != nil {
		t.Fatal(e)
	}
	output := privateDir()
	if err = run(ctx, output, backup, filepath.Join(dry, "export-receipt.json"), true); err != nil {
		t.Fatal(err)
	}
	if err = fixture.QueryRow(ctx, "SELECT to_regclass('wecom_follow_relationships') IS NOT NULL OR to_regclass('customer_local_owners') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("retirement did not remove tables: %v", err)
	}
	var receipt struct {
		Committed bool          `json:"committed"`
		Exports   []tableExport `json:"exports"`
		Expected  string        `json:"expected_export_receipt_sha256"`
	}
	bytes, err := os.ReadFile(filepath.Join(output, "retirement-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(bytes, &receipt); err != nil {
		t.Fatal(err)
	}
	dryBytes, err := os.ReadFile(filepath.Join(dry, "export-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	dryHash := sha256.Sum256(dryBytes)
	if receipt.Expected != hex.EncodeToString(dryHash[:]) {
		t.Fatal("execution did not bind reviewed receipt")
	}
	if !receipt.Committed || len(receipt.Exports) != 2 {
		t.Fatal("missing committed export receipt")
	}
	for i, e := range receipt.Exports {
		content, err := os.ReadFile(filepath.Join(output, e.File))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != e.SHA256 || e.Rows != int64(2-i) {
			t.Fatal("export checksum/count mismatch")
		}
		info, _ := os.Stat(filepath.Join(output, e.File))
		if info.Mode().Perm() != 0600 {
			t.Fatal("export is not private")
		}
	}
}

func TestRetirementRejectsInvalidBackupBeforeConnecting(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "invalid.dump")
	if err := os.WriteFile(backup, []byte("PGDMPinvalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), filepath.Join(t.TempDir(), "exports"), backup, "", true); err == nil {
		t.Fatal("unreadable archive accepted")
	}
}
