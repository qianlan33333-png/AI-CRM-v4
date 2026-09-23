package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLCaptureCompleteTablesAndMissingCoverage(t *testing.T) {
	dbURL, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("PostgreSQL URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, e := pgx.Connect(ctx, dbURL)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	var suffix [8]byte
	if _, e = rand.Read(suffix[:]); e != nil {
		t.Fatal(e)
	}
	name := "commerce_capture_" + hex.EncodeToString(suffix[:])
	identifier := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+identifier); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		admin.Exec(cleanup, "DROP DATABASE "+identifier+" WITH (FORCE)")
	}()
	parsed, e := url.Parse(dbURL)
	if e != nil {
		t.Fatal(e)
	}
	parsed.Path = "/" + name
	source, e := pgx.Connect(ctx, parsed.String())
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close(ctx)
	for _, table := range sourceTables {
		if _, e = source.Exec(ctx, "CREATE TABLE "+pgx.Identifier{table}.Sanitize()+" (id bigint, source_fact jsonb)"); e != nil {
			t.Fatal(e)
		}
		if table != "alipay_pay_orders" {
			if _, e = source.Exec(ctx, "INSERT INTO "+pgx.Identifier{table}.Sanitize()+" VALUES (1,'{\"protected\":\"must-not-log\"}')"); e != nil {
				t.Fatal(e)
			}
		}
	}
	t.Setenv("AICRM_COMMERCE_SOURCE_URL", parsed.String())
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	key := make([]byte, 32)
	rand.Read(key)
	if e = os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0600); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(dir, "capture")
	if e = run([]string{"--directory", output, "--key-file", keyPath}); e != nil {
		t.Fatal(e)
	}
	sealed, e := os.ReadFile(filepath.Join(output, "source.enc"))
	if e != nil {
		t.Fatal(e)
	}
	plain, e := open(key, sealed)
	if e != nil {
		t.Fatal(e)
	}
	var s snapshot
	if json.Unmarshal(plain, &s) != nil || validate(s) != nil {
		t.Fatal("invalid captured snapshot")
	}
	if s.Counts["alipay_pay_orders"] != 0 || s.Counts["wechat_pay_refunds"] != 1 || len(s.Tables) != 7 {
		t.Fatal("coverage lost")
	}
	if e = run([]string{"--mode", "inspect", "--directory", output, "--key-file", keyPath}); e != nil {
		t.Fatal(e)
	}
	if _, e = source.Exec(ctx, "DROP TABLE alipay_pay_orders"); e != nil {
		t.Fatal(e)
	}
	if e = run([]string{"--directory", filepath.Join(dir, "missing"), "--key-file", keyPath}); e == nil {
		t.Fatal("missing table accepted as zero")
	}
	if _, e = os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(e) {
		t.Fatal("incomplete capture published artifacts")
	}
}
