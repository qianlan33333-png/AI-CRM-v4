package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLCaptureFrozenFactsAndReadOnlyEnforcement(t *testing.T) {
	base, configErr := platformconfig.NamedDatabaseURL("AICRM_DATABASE_URL")
	if configErr != nil {
		t.Skip("isolated PostgreSQL database URL not configured")
	}
	ctx := context.Background()
	admin, e := pgx.Connect(ctx, base)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("audience_capture_test_%d", time.Now().UnixNano())
	ident := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+ident); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+ident+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	u, e := url.Parse(base)
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	conn, e := pgx.Connect(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(ctx)
	f := fixture(t)
	for _, tableName := range tableNames {
		rows := f.Tables[tableName].Rows
		cols := map[string]bool{}
		for _, r := range rows {
			var m map[string]json.RawMessage
			json.Unmarshal(r.Fact, &m)
			for k := range m {
				cols[k] = true
			}
		}
		ordered := []string{}
		for k := range cols {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		defs := []string{}
		for _, k := range ordered {
			defs = append(defs, pgx.Identifier{k}.Sanitize()+" jsonb")
		}
		tableID := pgx.Identifier{"public", tableName}.Sanitize()
		if _, e = conn.Exec(ctx, "CREATE TABLE "+tableID+" ("+strings.Join(defs, ",")+")"); e != nil {
			t.Fatal(e)
		}
		for _, r := range rows {
			if _, e = conn.Exec(ctx, "INSERT INTO "+tableID+" SELECT * FROM jsonb_to_record($1::jsonb) AS x("+strings.Join(defs, ",")+")", []byte(r.Fact)); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = conn.Exec(ctx, "CREATE TABLE read_only_sentinel(id bigint)"); e != nil {
		t.Fatal(e)
	}
	s, e := extract(ctx, u.String(), "test-source", f.ScopeDeclaration)
	if e != nil {
		t.Fatal(e)
	}
	if e = validate(s); e != nil {
		t.Fatal(e)
	}
	if s.Tables["ai_audience_member_current"].Count != 2 || s.Tables["ai_audience_package_version"].Count != 1 {
		t.Fatal("count loss")
	}
	// Exercise the command's default named audience source role rather than
	// only calling extract directly.
	t.Setenv("AICRM_AUDIENCE_SOURCE_DATABASE_URL", u.String())
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "snapshot.key")
	key := bytes.Repeat([]byte{9}, 32)
	if e = os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0600); e != nil {
		t.Fatal(e)
	}
	snapshotPath := filepath.Join(dir, "snapshot.enc")
	if e = run([]string{"extract", "--source-system", "test-source", "--snapshot", snapshotPath, "--snapshot-key-file", keyPath}); e != nil {
		t.Fatalf("default extract path: %v", e)
	}
	if _, e = readPrivate(snapshotPath, 512<<20); e != nil {
		t.Fatalf("default extract did not write protected snapshot: %v", e)
	}
	// A hostile source view cannot exploit capture to execute writes. The stored
	// package SQL above remains plain historical data throughout the happy path.
	_, e = conn.Exec(ctx, `ALTER TABLE ai_audience_member_current RENAME TO captured_members;
 CREATE FUNCTION capture_write_probe() RETURNS jsonb LANGUAGE plpgsql VOLATILE AS $$ BEGIN INSERT INTO read_only_sentinel VALUES(1); RETURN '4'::jsonb; END $$;
 CREATE VIEW ai_audience_member_current AS SELECT capture_write_probe() AS id, '2'::jsonb AS package_id;`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = extract(ctx, u.String(), "test-source", f.ScopeDeclaration); e == nil {
		t.Fatal("source view write was accepted")
	}
	var count int
	if e = conn.QueryRow(ctx, "SELECT count(*) FROM read_only_sentinel").Scan(&count); e != nil || count != 0 {
		t.Fatal("capture wrote source", count, e)
	}
}
