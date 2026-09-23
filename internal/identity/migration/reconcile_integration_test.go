package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLReceiptVerifierRejectsSameCountRootAndEvidenceDrift(t *testing.T) {
	pool, cleanup := identityReceiptReconciliationPool(t)
	defer cleanup()
	ctx := context.Background()
	runKey := "identity-reconcile-001"
	subjectOneDigest := sha256.Sum256([]byte("subject-one"))
	subjectTwoDigest := sha256.Sum256([]byte("subject-two"))
	quarantineDigest := sha256.Sum256([]byte("quarantine-one"))
	evidence := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := pool.Exec(ctx, `INSERT INTO identity_history_import_receipts(run_key,source_key,source_digest,outcome,customer_id,identity_count) VALUES($1,'subject-one',$2,'canonical',101,2),($1,'subject-two',$3,'canonical',202,1)`, runKey, subjectOneDigest[:], subjectTwoDigest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity_history_import_receipts(run_key,source_key,source_digest,outcome,reason_code,safe_evidence) VALUES($1,'quarantine-one',$2,'quarantined','scope_missing',jsonb_build_object('source_digest',$3::text))`, runKey, quarantineDigest[:], evidence); err != nil {
		t.Fatal(err)
	}
	verifier := PostgreSQLReceiptVerifier{Pool: pool}
	subjects := []SubjectReceiptExpectation{
		{SourceKey: "subject-one", SourceDigest: subjectOneDigest, CustomerID: 101, IdentityCount: 2},
		{SourceKey: "subject-two", SourceDigest: subjectTwoDigest, CustomerID: 202, IdentityCount: 1},
	}
	quarantines := []QuarantineReceiptExpectation{{SourceKey: "quarantine-one", SourceDigest: quarantineDigest, ReasonCode: "scope_missing", EvidenceDigest: evidence}}
	matched, err := verifier.Verify(ctx, runKey, subjects, quarantines)
	if err != nil || matched.Canonical != 2 || matched.Quarantined != 1 {
		t.Fatalf("matched=%+v err=%v", matched, err)
	}
	wrongRoot := append([]SubjectReceiptExpectation(nil), subjects...)
	wrongRoot[1].CustomerID = 303
	if _, err = verifier.Verify(ctx, runKey, wrongRoot, quarantines); !errors.Is(err, ErrReceiptReconciliationMismatch) {
		t.Fatalf("same-count root drift err=%v", err)
	}
	wrongEvidence := append([]QuarantineReceiptExpectation(nil), quarantines...)
	wrongEvidence[0].EvidenceDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err = verifier.Verify(ctx, runKey, subjects, wrongEvidence); !errors.Is(err, ErrReceiptReconciliationMismatch) {
		t.Fatalf("same-count evidence drift err=%v", err)
	}
	if _, err = verifier.Verify(ctx, runKey, subjects[:1], quarantines); !errors.Is(err, ErrReceiptReconciliationMismatch) {
		t.Fatalf("missing canonical receipt err=%v", err)
	}
}

func identityReceiptReconciliationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping identity receipt PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_identity_reconcile_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate identity reconciliation test")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0026_identity_history_receipts.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
