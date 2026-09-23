package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
