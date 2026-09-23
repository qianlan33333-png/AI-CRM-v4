package store

import (
	"context"
	"crypto/sha256"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestHistoricalResolutionPreservesSourceAndExistingRoots(t *testing.T) {
	ctx := context.Background()
	db, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	_, f, _, _ := runtime.Caller(0)
	for _, name := range []string{"0130_segment_historical_import.sql", "0135_segment_history_resolution_proof.sql"} {
		b, e := os.ReadFile(filepath.Join(filepath.Dir(f), "../../../migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(ctx, string(b)); e != nil {
			t.Fatal(e)
		}
	}
	pool, e := platformpostgres.Wrap(db, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := NewPostgreSQL(db, uow)
	if e != nil {
		t.Fatal(e)
	}
	actor, _ := segmentport.AdminMutationActor(2)
	hash := func(s string) segmentport.Digest { return segmentport.Digest(sha256.Sum256([]byte(s))) }
	// Source snapshots use PostgreSQL microsecond precision so a derived import
	// retains the exact captured instant read back from the previous batch.
	in := segmentport.HistoricalImport{Source: "test-resolution", Digest: hash("first"), CapturedAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond), EncryptedEvidence: make([]byte, 40), Actor: actor, Groups: []segmentport.HistoricalGroup{{SourceID: 1, Name: "group"}}, Packages: []segmentport.HistoricalPackage{{SourceID: 2, GroupSourceID: 1, Name: "package", Members: []segmentport.HistoricalMember{{SourceID: 3, Active: true, Reason: "unresolved", EnteredAt: time.Now().Add(-time.Hour)}}}}, Rows: []segmentport.HistoricalRow{{Kind: "group", SourceID: 1, Digest: hash("group")}, {Kind: "package", SourceID: 2, Digest: hash("package")}, {Kind: "member", SourceID: 3, Digest: hash("member")}}}
	apply := func(v segmentport.HistoricalImport) (segmentport.HistoricalImportResult, error) {
		var out segmentport.HistoricalImportResult
		err := uow.Within(ctx, func(tx context.Context) error { var e error; out, e = repo.ImportHistorical(tx, v); return e })
		return out, err
	}
	if _, e = apply(in); e != nil {
		t.Fatal(e)
	}
	next := in
	next.Digest = hash("second")
	next.ResolutionSourceDigest = hash("original-source")
	next.ResolutionParentDigest = in.Digest
	next.ResolutionDerivedAt = time.Now().Add(time.Second).UTC()
	next.Packages[0].Members[0].Reason = "resolved"
	next.Packages[0].Members[0].CustomerID = 12
	bad := next
	bad.Rows = append([]segmentport.HistoricalRow(nil), next.Rows...)
	bad.Rows[2].Digest = hash("drift")
	if _, e = apply(bad); e == nil {
		t.Fatal("source drift accepted")
	}
	bad = next
	bad.ResolutionParentDigest = hash("wrong-parent")
	if _, e = apply(bad); e == nil {
		t.Fatal("wrong predecessor accepted")
	}
	out, e := apply(next)
	if e != nil || out.ResolvedActive != 1 {
		t.Fatal(out, e)
	}
	out, e = apply(next)
	if e != nil || !out.Replayed {
		t.Fatal(out, e)
	}
	var packages, batches int
	db.QueryRow(ctx, `SELECT count(*) FROM segment_audience_packages`).Scan(&packages)
	db.QueryRow(ctx, `SELECT count(*) FROM segment_audience_history_batches`).Scan(&batches)
	if packages != 1 || batches != 2 {
		t.Fatalf("packages=%d batches=%d", packages, batches)
	}
	third := next
	third.Digest = hash("third")
	third.ResolutionParentDigest = next.Digest
	third.ResolutionDerivedAt = time.Now().Add(2 * time.Second).UTC()
	third.Packages[0].Members[0].CustomerID = 13
	if _, e = apply(third); e == nil {
		t.Fatal("previously resolved root silently changed")
	}
}
