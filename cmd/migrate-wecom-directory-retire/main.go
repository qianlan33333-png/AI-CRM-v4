// migrate-wecom-directory-retire is an explicit post-cutover operation. It is
// never imported by runtime. The release coordinator runs it only after the
// full directory, audience calibration and database backup are verified.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func main() {
	output := flag.String("output-dir", "", "private directory for old table exports and checksums")
	backup := flag.String("database-backup", "", "existing PostgreSQL custom-format full migration backup")
	expected := flag.String("expected-export-receipt", "", "reviewed dry-run export receipt required when executing")
	execute := flag.Bool("execute", false, "drop retired tables after exporting; default exports and rolls back")
	flag.Parse()
	if err := run(context.Background(), *output, *backup, *expected, *execute); err != nil {
		fmt.Fprintln(os.Stderr, "directory retirement refused:", err)
		os.Exit(1)
	}
}

type tableExport struct {
	Table  string `json:"table"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
}

func run(ctx context.Context, output, backup, expected string, execute bool) error {
	if !filepath.IsAbs(output) || !filepath.IsAbs(backup) {
		return errors.New("absolute output and backup paths are required")
	}
	// Require a recoverable migration backup before even taking the destructive
	// path. Its identity is included in the receipt, never database credentials.
	file, err := os.Open(backup)
	if err != nil {
		return errors.New("database backup is unavailable")
	}
	defer file.Close()
	var header [5]byte
	if _, err = io.ReadFull(file, header[:]); err != nil || string(header[:]) != "PGDMP" {
		return errors.New("database backup must be PostgreSQL custom format")
	}
	if _, err = file.Seek(0, 0); err != nil {
		return err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	backupHash := hex.EncodeToString(hash.Sum(nil))
	validate := exec.CommandContext(ctx, "pg_restore", "--list", backup)
	validate.Stdout = io.Discard
	validate.Stderr = io.Discard
	if err = validate.Run(); err != nil {
		return errors.New("database backup archive cannot be read by pg_restore")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		return err
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return errors.New("export directory must be private (0700)")
	}
	dbURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return errors.New("database URL is required")
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return errors.New("database connection unavailable")
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('wecom.directory.publication.v1',0))`); err != nil {
		return err
	}
	var ready bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wecom_directory_publications WHERE baseline_initialized AND complete AND full_observed_at IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM wecom_customer_sync_runs WHERE status IN ('queued','listing_staff','fetching_profiles','ingesting','reconciling','failed_retryable')) AND NOT EXISTS(SELECT 1 FROM customer_owner_handoff_batches WHERE mode='local_only' AND state IN ('accepted','executing')) AND NOT EXISTS(
 SELECT 1 FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id
 WHERE p.lifecycle<>'archived' AND c.definition->>'template_key' IN ('hxc_registration','wecom_contact_registration','questionnaire_submissions','questionnaire_choice_answers','paid_order','channel_entry','radar_first_click_elapsed','member_usage_status','member_excluding_group_paid') AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(s.source_watermarks) w WHERE w->>'source'='wecom.directory.published.v2'))`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("full directory, audience calibration or in-flight task checks are incomplete")
	}
	var reviewed []tableExport
	var reviewedHash string
	if execute {
		reviewed, reviewedHash, err = readExpectedExports(expected, backupHash)
		if err != nil {
			return err
		}
	}
	tables := []string{"wecom_follow_relationships", "customer_local_owners"}
	exports := []tableExport{}
	for _, table := range tables {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err = tx.Exec(ctx, `LOCK TABLE `+pgx.Identifier{table}.Sanitize()+` IN ACCESS EXCLUSIVE MODE`); err != nil {
			return err
		}
		filename := table + ".ndjson"
		destination, err := os.OpenFile(filepath.Join(output, filename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		digest := sha256.New()
		writer := io.MultiWriter(destination, digest)
		rows, err := tx.Query(ctx, `SELECT row_to_json(t)::text FROM `+pgx.Identifier{table}.Sanitize()+` t ORDER BY row_to_json(t)::text`)
		if err != nil {
			destination.Close()
			return err
		}
		count := int64(0)
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if _, err = fmt.Fprintln(writer, raw); err != nil {
				break
			}
			count++
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err == nil {
			err = destination.Sync()
		}
		destination.Close()
		if err != nil {
			return err
		}
		exports = append(exports, tableExport{table, count, hex.EncodeToString(digest.Sum(nil)), filename})
	}
	receipt := map[string]any{"created_at": time.Now().UTC(), "database_backup_sha256": backupHash, "exports": exports, "execute_requested": execute, "committed": false}
	if execute {
		receipt["expected_export_receipt_sha256"] = reviewedHash
	}
	if err = writeReceipt(output, "export-receipt.json", receipt); err != nil {
		return err
	}
	if !execute {
		fmt.Println("retired tables exported; dry run rolled back")
		return nil
	}
	if !sameExports(reviewed, exports) {
		return errors.New("locked exports differ from reviewed dry run; tables retained")
	}
	for _, e := range exports {
		if _, err = tx.Exec(ctx, `DROP TABLE `+pgx.Identifier{e.Table}.Sanitize()); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	receipt["committed"] = true
	if err = writeReceipt(output, "retirement-receipt.json", receipt); err != nil {
		return err
	}
	fmt.Println("retired relationship tables exported and removed")
	return nil
}
func writeReceipt(output, name string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, name), append(raw, '\n'), 0600)
}

// The release coordinator supplies the private dry-run receipt. Bind execution
// to that exact file and backup; compare its exports while the table locks are
// still held, before issuing any destructive statement.
func readExpectedExports(path, backupHash string) ([]tableExport, string, error) {
	if !filepath.IsAbs(path) {
		return nil, "", errors.New("absolute reviewed dry-run receipt path is required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024*1024 {
		return nil, "", errors.New("reviewed receipt must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", errors.New("reviewed receipt unavailable")
	}
	var receipt struct {
		Backup    string        `json:"database_backup_sha256"`
		Execute   bool          `json:"execute_requested"`
		Committed bool          `json:"committed"`
		Exports   []tableExport `json:"exports"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil || receipt.Execute || receipt.Committed || receipt.Backup != backupHash || receipt.Exports == nil {
		return nil, "", errors.New("reviewed receipt must identify a dry run with the same backup")
	}
	seen := map[string]bool{}
	for _, e := range receipt.Exports {
		digest, decodeErr := hex.DecodeString(e.SHA256)
		if (e.Table != "wecom_follow_relationships" && e.Table != "customer_local_owners") || seen[e.Table] || e.File != e.Table+".ndjson" || e.Rows < 0 || decodeErr != nil || len(digest) != sha256.Size {
			return nil, "", errors.New("reviewed receipt contains invalid export metadata")
		}
		seen[e.Table] = true
	}
	digest := sha256.Sum256(raw)
	return receipt.Exports, hex.EncodeToString(digest[:]), nil
}
func sameExports(expected, actual []tableExport) bool {
	if len(expected) != len(actual) {
		return false
	}
	byTable := map[string]tableExport{}
	for _, e := range expected {
		byTable[e.Table] = e
	}
	for _, e := range actual {
		old, ok := byTable[e.Table]
		if !ok || old.Rows != e.Rows || old.SHA256 != e.SHA256 || old.File != e.File {
			return false
		}
	}
	return true
}
