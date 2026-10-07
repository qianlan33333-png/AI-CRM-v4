package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLOverviewNewCanonicalCustomerProvenance(t *testing.T) {
	pool, cleanup := identityPool(t)
	defer cleanup()
	ctx := context.Background()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate identity overview migration")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0026_identity_history_receipts.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	known := insertOverviewCustomer(t, ctx, pool, "wechat_miniprogram", start.Add(time.Hour))
	if _, err = pool.Native().Exec(ctx, `UPDATE customers SET status='closed' WHERE id=$1`, known); err != nil {
		t.Fatal(err)
	}
	historical := insertOverviewCustomer(t, ctx, pool, "wechat_miniprogram", start.Add(2*time.Hour))
	if _, err = pool.Native().Exec(ctx, `INSERT INTO identity_history_import_receipts(run_key,source_key,source_digest,outcome,customer_id,identity_count)
		VALUES('overview-history','subject-1',$1,'canonical',$2,1)`, overviewIdentityDigest("history"), historical); err != nil {
		t.Fatal(err)
	}
	_ = insertOverviewCustomer(t, ctx, pool, "directory_sync", start.Add(3*time.Hour))
	_ = insertOverviewCustomerWithoutIdentity(t, ctx, pool, start.Add(4*time.Hour))
	_ = insertOverviewCustomer(t, ctx, pool, "wechat_miniprogram", end)

	store := NewPostgresStore()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var result identityport.NewCanonicalCustomerOverview
	err = unit.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ReadNewCanonicalCustomerOverview(tx, identityport.OverviewWindow{Start: start, End: end})
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.KnownNewCanonicalCustomers != 1 || result.HistoricalExcluded != 1 || result.UnknownSource != 2 {
		t.Fatalf("provenance result=%+v", result)
	}
}

func insertOverviewCustomer(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, source string, createdAt time.Time) int64 {
	t.Helper()
	id := insertOverviewCustomerWithoutIdentity(t, ctx, pool, createdAt)
	if _, err := pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at,created_at,updated_at)
		VALUES($1,'mp_openid','wechat-app:overview-fixture',$2,'verified',$3,1,$4,$4,$4)`, id, "overview-"+source+"-"+createdAt.Format("150405"), source, createdAt); err != nil {
		t.Fatalf("insert identity source %s: %v", source, err)
	}
	return id
}

func insertOverviewCustomerWithoutIdentity(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, createdAt time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customers(created_at,updated_at) VALUES($1,$1) RETURNING id`, createdAt).Scan(&id); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	return id
}

func overviewIdentityDigest(value string) []byte {
	digest := make([]byte, 32)
	copy(digest, []byte(value))
	return digest
}

func TestPostgreSQLOverviewCreationSourceSurvivesMergeReverseAndChainedMerge(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	if _, err := h.pool.Native().Exec(ctx, string(mustOverviewHistoryMigration(t))); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	prior := start.Add(-24 * time.Hour)
	fact := func(kind identitydomain.Kind, scope, value, source string) identitydomain.VerifiedFact {
		t.Helper()
		f, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: kind, Scope: scope, Value: value, Source: source})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	runtimeFact := fact(identitydomain.KindOAOpenID, "wechat-app:overview-lineage", "runtime-synthetic", "wechat_miniprogram")
	importFact := fact(identitydomain.KindFirstPartyMemberID, "first-party:overview-lineage", "import-synthetic", "directory_sync")
	runtimeRoot, survivor := h.provision(t, runtimeFact), h.provision(t, importFact)
	if _, err := h.pool.Native().Exec(ctx, `UPDATE customers SET created_at=CASE WHEN id=$1 THEN $3::timestamptz ELSE $4::timestamptz END WHERE id IN($1,$2)`, runtimeRoot.CustomerID, survivor.CustomerID, start.Add(time.Hour), prior.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	check := func(stage string, priorUnknown int64) {
		t.Helper()
		for _, expected := range []struct {
			start          time.Time
			known, unknown int64
		}{{start, 1, 0}, {prior, 0, priorUnknown}} {
			var result identityport.NewCanonicalCustomerOverview
			h.within(t, func(tx context.Context) error {
				var e error
				result, e = h.service.Store.(*PostgresStore).ReadNewCanonicalCustomerOverview(tx, identityport.OverviewWindow{Start: expected.start, End: expected.start.Add(24 * time.Hour)})
				return e
			})
			if result.KnownNewCanonicalCustomers != expected.known || result.UnknownSource != expected.unknown || result.HistoricalExcluded != 0 {
				t.Errorf("%s window=%s provenance=%+v want known=%d unknown=%d", stage, expected.start, result, expected.known, expected.unknown)
			}
		}
	}
	merge := func(from identityport.ProvisionResult, target identitydomain.VerifiedFact, to identityport.ProvisionResult) identityapp.LinkResult {
		t.Helper()
		candidate := h.link(t, identityapp.LinkCommand{SourceCustomerID: from.CustomerID, Target: target, Evidence: testEvidence(identitydomain.EvidenceStrong)})
		if candidate.Candidate == nil {
			t.Fatal("expected explicit merge candidate")
		}
		return h.confirm(t, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: to.CustomerID, Operator: "synthetic-reviewer"})
	}
	check("before_merge", 1)
	first := merge(runtimeRoot, importFact, survivor)
	check("merged", 1)
	h.within(t, func(tx context.Context) error { _, e := h.service.RevertConfirmedMerge(tx, first.Merge.ID); return e })
	check("reversed", 1)
	thirdFact := fact(identitydomain.KindExtension, "ext:overview-lineage", "third-synthetic", "directory_sync")
	third := h.provision(t, thirdFact)
	if _, err := h.pool.Native().Exec(ctx, `UPDATE customers SET created_at=$2 WHERE id=$1`, third.CustomerID, prior.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	merge(runtimeRoot, importFact, survivor)
	merge(survivor, thirdFact, third)
	check("remerged_and_chained", 2)
}

func mustOverviewHistoryMigration(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate identity overview migration")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0026_identity_history_receipts.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
