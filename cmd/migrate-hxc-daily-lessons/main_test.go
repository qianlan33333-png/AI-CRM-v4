package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

func TestSnapshotRejectsNonUUIDAndDigestDrift(t *testing.T) {
	directory, raw := writeSnapshot(t, validRecord(t))
	if _, _, err := loadSnapshot(directory, 1); err != nil {
		t.Fatal(err)
	}
	var value manifest
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("decode snapshot")
	}
	value.Records[0].ID = "opc100-q1"
	writeManifest(t, directory, value)
	if _, _, err := loadSnapshot(directory, 1); !errorsIs(err, errInvalidSnapshot) {
		t.Fatalf("non UUID snapshot err=%v", err)
	}
	value.Records[0] = validRecord(t)
	value.Records[0].Title = "drifted"
	writeManifest(t, directory, value)
	if _, _, err := loadSnapshot(directory, 1); !errorsIs(err, errInvalidSnapshot) {
		t.Fatalf("digest drift err=%v", err)
	}
}

func TestDryRunApplyReplayVerifyAndDrift(t *testing.T) {
	rawURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; PostgreSQL journey runs in CI")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var random [8]byte
	_, _ = rand.Read(random[:])
	schema := "aicrm_hxc_lesson_" + fmt.Sprintf("%x", random[:])
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
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, name := range []string{"0007_media.sql", "0080_media_legacy_material_mappings.sql", "0180_material_groups.sql", "0182_media_group_management.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}
	directory, raw := writeSnapshot(t, validRecord(t))
	configuredURL := rawURL + "?search_path=" + schema
	if strings.Contains(rawURL, "?") {
		configuredURL = rawURL + "&search_path=" + schema
	}
	t.Setenv("AICRM_DATABASE_URL", configuredURL)
	base := []string{"--snapshot-dir=" + directory, "--expected-count=1", "--manifest-sha256=" + hexDigest(raw), "--actor-admin-user-id=7"}
	if err = run(ctx, append([]string{"--mode=dry-run"}, base...)); err != nil {
		t.Fatal(err)
	}
	assertCount(t, pool, "media_miniprograms", 0)
	if err = run(ctx, append([]string{"--mode=verify"}, base...)); err == nil {
		t.Fatal("verify accepted a missing import")
	}
	if err = run(ctx, append([]string{"--mode=apply", "--confirm-apply"}, base...)); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, append([]string{"--mode=apply", "--confirm-apply"}, base...)); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, append([]string{"--mode=verify"}, base...)); err != nil {
		t.Fatal(err)
	}
	assertCount(t, pool, "media_images", 1)
	assertCount(t, pool, "media_miniprograms", 1)
	assertCount(t, pool, "media_legacy_material_mappings", 2)
	assertCount(t, pool, "media_references", 1)
	assertCount(t, pool, "media_operation_receipts", 1)
	assertCount(t, pool, "media_audit_events", 2)
	assertCount(t, pool, "media_outbox", 2)
	var title, appID, pagePath, category string
	var enabled bool
	if err = pool.QueryRow(ctx, `SELECT title,app_id,page_path,category,enabled FROM media_miniprograms`).Scan(&title, &appID, &pagePath, &category, &enabled); err != nil || title != "测试日课" || appID != lessonAppID || pagePath != "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=learn" || category != "日课" || !enabled {
		t.Fatalf("projection title=%q app=%q path=%q category=%q enabled=%v err=%v", title, appID, pagePath, category, enabled, err)
	}

	var snapshot manifest
	if json.Unmarshal(raw, &snapshot) != nil {
		t.Fatal("decode snapshot")
	}
	snapshot.Records[0].Title = "改变后的日课"
	snapshot.Records[0].SourceRecordDigest = sourceRecordDigest(snapshot.Records[0])
	driftRaw := writeManifest(t, directory, snapshot)
	driftArgs := []string{"--mode=apply", "--confirm-apply", "--snapshot-dir=" + directory, "--expected-count=1", "--manifest-sha256=" + hexDigest(driftRaw), "--actor-admin-user-id=7"}
	if err = run(ctx, driftArgs); err == nil {
		t.Fatal("source drift was accepted")
	}
	assertCount(t, pool, "media_miniprograms", 1)
}

func validRecord(t *testing.T) record {
	t.Helper()
	pngBytes := testPNG(t)
	item := record{ID: "11111111-2222-3333-4444-555555555555", Title: "测试日课", PublishDate: "2026-09-17", AppID: lessonAppID, PagePath: "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=learn", CoverFile: "covers/11111111-2222-3333-4444-555555555555.png", CoverSHA256: hexDigest(pngBytes), CoverSize: int64(len(pngBytes)), Width: 2, Height: 2}
	item.SourceRecordDigest = sourceRecordDigest(item)
	return item
}

func writeSnapshot(t *testing.T, item record) (string, []byte) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "covers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, filepath.FromSlash(item.CoverFile)), testPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	value := manifest{SchemaVersion: 1, SourceSystem: "hxc-daily-lessons", SourceBase: defaultSourceBase, SourceVersion: "test", CapturedAt: time.Date(2026, 9, 20, 1, 2, 3, 0, time.UTC), SourceTotal: 1, Records: []record{item}}
	return directory, writeManifest(t, directory, value)
}

func writeManifest(t *testing.T, directory string, value manifest) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err = os.WriteFile(filepath.Join(directory, manifestFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	imageValue := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageValue.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageValue); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertCount(t *testing.T, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&got); err != nil || got != want {
		t.Fatalf("%s count=%d want=%d err=%v", table, got, want, err)
	}
}

func errorsIs(err, target error) bool {
	return err != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}
