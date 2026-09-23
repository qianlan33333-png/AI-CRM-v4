package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
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

func TestFrozenSnapshotRejectsDuplicateAndUnverifiedRecords(t *testing.T) {
	s := validSnapshot()
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	s.Materials = append(s.Materials, s.Materials[0])
	if err := s.validate(); err == nil {
		t.Fatal("duplicate immutable source record was accepted")
	}
	s = validSnapshot()
	s.Materials[0].SourceRecordDigest = "sha256:not-a-digest"
	if err := s.validate(); err == nil {
		t.Fatal("unverified source record digest was accepted")
	}
	s = validSnapshot()
	s.Materials[0].Kind = "attachment"
	if err := s.validate(); err == nil {
		t.Fatal("source record with a mismatched material kind was accepted")
	}
}

func TestFrozenMappingCommandDryRunApplyReplayAndVerify(t *testing.T) {
	rawURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; command PostgreSQL journey runs in CI")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_media_mapping_cmd_" + fmt.Sprintf("%x", random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE") }()
	config, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	for _, migration := range []string{"0007_media.sql", "0080_media_legacy_material_mappings.sql"} {
		body, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", migration))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	sourceDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err = pool.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',1,$2)`, sourceDigest, []byte("x")); err != nil {
		t.Fatal(err)
	}
	var imageID int64
	if err = pool.QueryRow(ctx, `INSERT INTO media_images(blob_digest,file_name,name,mime_type,byte_size,width,height,created_by,updated_by) VALUES($1,'image.png','image','image/png',1,1,1,7,7) RETURNING id`, sourceDigest).Scan(&imageID); err != nil {
		t.Fatal(err)
	}
	s := validSnapshot()
	s.Materials[0].MaterialID = imageID
	s.Materials[0].SourceDigest = sourceDigest
	snapshotPath := filepath.Join(t.TempDir(), "mapping.json")
	body, err := json.Marshal(s)
	if err != nil || os.WriteFile(snapshotPath, body, 0o600) != nil {
		t.Fatal(err)
	}
	configuredURL := rawURL + "?search_path=" + schema
	if strings.Contains(rawURL, "?") {
		configuredURL = rawURL + "&search_path=" + schema
	}
	t.Setenv("AICRM_DATABASE_URL", configuredURL)
	args := []string{"--snapshot=" + snapshotPath, "--snapshot-sha256=" + digestHex(body), "--actor-admin-user-id=7"}
	if err = run(ctx, append([]string{"--mode=dry-run"}, args...)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM media_legacy_material_mappings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("dry run mappings=%d err=%v", count, err)
	}
	if err = run(ctx, append([]string{"--mode=apply", "--confirm-apply"}, args...)); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, append([]string{"--mode=apply", "--confirm-apply"}, args...)); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, append([]string{"--mode=verify"}, args...)); err != nil {
		t.Fatal(err)
	}
	missing := s
	missing.Materials = append([]material(nil), s.Materials...)
	missing.Materials[0].LegacyID = "missing"
	missing.Materials[0].SourceRecord = json.RawMessage(`{"kind":"image","legacy_id":"missing"}`)
	missing.Materials[0].SourceRecordDigest = digestHex(missing.Materials[0].SourceRecord)
	body, _ = json.Marshal(missing)
	if err = os.WriteFile(snapshotPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshotPath, "--snapshot-sha256=" + digestHex(body), "--actor-admin-user-id=7"}); err == nil || !strings.Contains(err.Error(), "verified mapping is missing for image:missing") {
		t.Fatalf("verify missing mapping err=%v", err)
	}
	// The mapping exists again, but its originally verified V3 Media fact has
	// changed. Verification must reject that drift rather than treating the
	// immutable mapping row as sufficient evidence.
	body, _ = json.Marshal(s)
	if err = os.WriteFile(snapshotPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	driftDigest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err = pool.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',1,$2)`, driftDigest, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE media_images SET blob_digest=$1 WHERE id=$2`, driftDigest, imageID); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshotPath, "--snapshot-sha256=" + digestHex(body), "--actor-admin-user-id=7"}); err == nil || !strings.Contains(err.Error(), "target Media source drift or unavailable for image:1") {
		t.Fatalf("verify target drift err=%v", err)
	}
}

func TestInspectNeedsOnlyFrozenSnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "materials.json")
	sourceRecord := []byte(`{"kind":"image","legacy_id":1}`)
	raw := []byte(`{"manifest":{"source_system":"ai-crm-v2","source_revision":"0123456789012345678901234567890123456789","snapshot_at":"2026-09-05T01:02:03Z"},"materials":[{"kind":"image","legacy_id":"1","source_record":{"kind":"image","legacy_id":1},"source_record_digest":"` + digestHex(sourceRecord) + `","material_id":7,"source_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=inspect", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
}

func validSnapshot() snapshot {
	var s snapshot
	s.Manifest.SourceSystem = expectedSourceSystem
	s.Manifest.SourceRevision = "0123456789012345678901234567890123456789"
	s.Manifest.SnapshotAt = time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	sourceRecord := json.RawMessage(`{"kind":"image","legacy_id":1}`)
	s.Materials = []material{{Kind: "image", LegacyID: "1", SourceRecord: sourceRecord, SourceRecordDigest: digestHex(sourceRecord), MaterialID: 7, SourceDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	return s
}
