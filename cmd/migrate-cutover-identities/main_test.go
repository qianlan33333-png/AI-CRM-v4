package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestProofCaptureDryRunApplyReusesExistingRoot(t *testing.T) {
	dsn := platformconfig.CutoverIdentityEnvironment("AICRM_DATABASE_URL")
	if dsn == "" {
		t.Skip("local integration database not configured")
	}
	ctx := context.Background()
	admin, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	var suffix [8]byte
	_, _ = rand.Read(suffix[:])
	schema := "identity_proof_" + hex.EncodeToString(suffix[:])
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0026_identity_history_receipts.sql"} {
		b, e := os.ReadFile(filepath.Join(root, "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(ctx, string(b)); e != nil {
			t.Fatal(e)
		}
	}
	_, e = db.Exec(ctx, `CREATE TABLE service_period_entitlements(unionid text,tenant_id text,status text);
 CREATE TABLE commerce_coupon_claims(unionid text,tenant_id text);
 CREATE TABLE crm_user_identity(unionid text,identity_status text,primary_external_userid text);
 CREATE TABLE wecom_external_contact_identity_map(id bigint,corp_id text,external_userid text,unionid text,status text,raw_profile jsonb);
 INSERT INTO service_period_entitlements VALUES('synthetic-u1','aicrm','active');
 INSERT INTO commerce_coupon_claims VALUES('synthetic-u2','aicrm');
 INSERT INTO crm_user_identity VALUES('synthetic-u1','active','synthetic-e1'),('synthetic-u2','active','');
 INSERT INTO wecom_external_contact_identity_map VALUES(1,'test-corp','synthetic-e1','synthetic-u1','active','{"errcode":0,"external_contact":{"unionid":"synthetic-u1","external_userid":"synthetic-e1"}}');`)
	if e != nil {
		t.Fatal(e)
	}
	scopes := proof.Scopes{CorpID: "test-corp", UnionScope: "wechat-open-platform:test-platform"}
	s, e := proof.Capture(ctx, db, scopes)
	if e != nil || len(s.Rows) != 2 || !s.Rows[0].Evidence[0].ProviderOK {
		t.Fatalf("capture failed %v", e)
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	bound := platformpostgres.BindTransaction(ctx, tx)
	owner := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	fact, e := (identityadapter.ProviderHistory{}).VerifiedHistoricalFact(identityport.HistoricalVerifiedInput{Kind: "wecom_external_userid", Scope: "wecom-corp:test-corp", Value: "synthetic-e1", Source: proof.Provenance})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.ProvisionHistoricalSubject(bound, identityport.HistoricalSubjectCommand{SubjectKey: "preexisting", Facts: []identitydomain.VerifiedFact{fact}, SourceDigest: [32]byte{1}}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	path := filepath.Join(dir, "proof.enc")
	if e = os.WriteFile(key, []byte(base64.RawStdEncoding.EncodeToString(make([]byte, 32))), 0600); e != nil {
		t.Fatal(e)
	}
	digest, e := proof.Seal(s, path, key)
	if e != nil {
		t.Fatal(e)
	}
	testURL, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	query := testURL.Query()
	query.Set("search_path", schema)
	testURL.RawQuery = query.Encode()
	t.Setenv("AICRM_DATABASE_URL", testURL.String())
	args := []string{"--snapshot=" + path, "--snapshot-key-file=" + key, "--manifest-sha256=" + hex.EncodeToString(digest[:]), "--corp-id=test-corp", "--union-scope=wechat-open-platform:test-platform", "--confirm-matched-provider-scopes"}
	if e = run(ctx, append(args, "--mode=dry-run")); e != nil {
		t.Fatal(e)
	}
	var customers, identities, receipts int
	count := func() {
		t.Helper()
		if e = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM customer_identities),(SELECT count(*) FROM identity_history_import_receipts)`).Scan(&customers, &identities, &receipts); e != nil {
			t.Fatal(e)
		}
	}
	count()
	if customers != 1 || identities != 1 || receipts != 0 {
		t.Fatal("dry-run wrote database")
	}
	for i := 0; i < 2; i++ {
		if e = run(ctx, append(args, "--mode=apply", "--confirm-apply")); e != nil {
			t.Fatal(e)
		}
		count()
		if customers != 1 || identities != 2 || receipts != 2 {
			t.Fatalf("apply/replay counts %d/%d/%d", customers, identities, receipts)
		}
	}
}
