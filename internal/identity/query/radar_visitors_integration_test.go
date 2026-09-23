package query_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestAdminRadarVisitorIdentityProjectionMatchesCanonicalLineageAndFailsClosed(t *testing.T) {
	native, cleanup := radarVisitorIdentityPool(t)
	defer cleanup()
	ctx := context.Background()
	root := insertRadarVisitorCustomer(t, native, "active", nil)
	merged := insertRadarVisitorCustomer(t, native, "merged", &root)
	if _, err := native.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:radar-visitor','visitor-contact','verified','fixture',1,CURRENT_TIMESTAMP)`, root); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	reader := query.NewPostgreSQL()
	if err = uow.Within(ctx, func(tx context.Context) error {
		lineage, readErr := reader.CanonicalLineage(tx, customerdomain.CustomerID(merged))
		if readErr != nil {
			return readErr
		}
		if len(lineage) != 2 || lineage[0] != customerdomain.CustomerID(root) || lineage[1] != customerdomain.CustomerID(merged) {
			t.Fatalf("existing lineage=%v", lineage)
		}
		projection, readErr := reader.AdminRadarVisitorIdentities(tx, "wecom-corp:radar-visitor", []customerdomain.CustomerID{customerdomain.CustomerID(merged)})
		if readErr != nil {
			return readErr
		}
		value, exists := projection[customerdomain.CustomerID(merged)]
		if !exists || value.CanonicalCustomerID != customerdomain.CustomerID(root) || value.ExternalContactStatus != identityport.AdminRadarVisitorExternalContactAvailable || value.ExternalContactID != "visitor-contact" {
			t.Fatalf("admin visitor projection=%+v", projection)
		}
		matches, readErr := reader.SearchAdminRadarVisitorCustomers(tx, "wecom-corp:radar-visitor", "visitor-contact", []customerdomain.CustomerID{customerdomain.CustomerID(root)}, 101)
		if readErr != nil {
			return readErr
		}
		if len(matches) != 2 || matches[0] != customerdomain.CustomerID(root) || matches[1] != customerdomain.CustomerID(merged) {
			t.Fatalf("historic lineage search=%v", matches)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertCanonicalAgreementFailure(t, ctx, uow, reader, "missing", customerdomain.CustomerID(root+999), query.ErrNotFound)

	if _, err = native.Exec(ctx, `ALTER TABLE customers DROP CONSTRAINT ck_customers_merged_state`); err != nil {
		t.Fatal(err)
	}
	broken := insertRadarVisitorCustomer(t, native, "merged", nil)
	assertCanonicalAgreementFailure(t, ctx, uow, reader, "broken", customerdomain.CustomerID(broken), query.ErrInvalidQuery)

	cycleLeft := insertRadarVisitorCustomer(t, native, "active", nil)
	cycleRight := insertRadarVisitorCustomer(t, native, "active", nil)
	if _, err = native.Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=CURRENT_TIMESTAMP WHERE id=$1`, cycleLeft, cycleRight); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=CURRENT_TIMESTAMP WHERE id=$1`, cycleRight, cycleLeft); err != nil {
		t.Fatal(err)
	}
	assertCanonicalAgreementFailure(t, ctx, uow, reader, "cycle", customerdomain.CustomerID(cycleLeft), query.ErrInvalidQuery)

	withinBoundary := insertRadarVisitorMergeChain(t, native, 127)
	if err = uow.Within(ctx, func(tx context.Context) error {
		lineage, readErr := reader.CanonicalLineage(tx, customerdomain.CustomerID(withinBoundary[0]))
		if readErr != nil {
			return readErr
		}
		if len(lineage) != len(withinBoundary) || lineage[len(lineage)-1] != customerdomain.CustomerID(withinBoundary[len(withinBoundary)-1]) {
			t.Fatalf("127-pointer existing lineage root=%v", lineage)
		}
		projection, readErr := reader.AdminRadarVisitorIdentities(tx, "wecom-corp:radar-visitor", []customerdomain.CustomerID{customerdomain.CustomerID(withinBoundary[0])})
		if readErr != nil {
			return readErr
		}
		if projection[customerdomain.CustomerID(withinBoundary[0])].CanonicalCustomerID != customerdomain.CustomerID(withinBoundary[len(withinBoundary)-1]) {
			t.Fatalf("127-pointer batch projection=%+v", projection)
		}
		roots, readErr := reader.CanonicalCustomerRoots(tx, []customerdomain.CustomerID{
			customerdomain.CustomerID(merged), customerdomain.CustomerID(root), customerdomain.CustomerID(merged),
		})
		if readErr != nil || len(roots) != 2 || roots[customerdomain.CustomerID(merged)] != customerdomain.CustomerID(root) || roots[customerdomain.CustomerID(root)] != customerdomain.CustomerID(root) {
			t.Fatalf("canonical customer roots=%v err=%v", roots, readErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	batch := make([]customerdomain.CustomerID, 0, 500)
	for index := 0; index < 500; index++ {
		batch = append(batch, customerdomain.CustomerID(insertRadarVisitorCustomer(t, native, "active", nil)))
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		roots, readErr := reader.CanonicalCustomerRoots(tx, batch)
		if readErr != nil || len(roots) != len(batch) {
			t.Fatalf("500 canonical customer roots=%d err=%v", len(roots), readErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.CanonicalCustomerRoots(ctx, append(batch, customerdomain.CustomerID(root))); !errors.Is(err, query.ErrInvalidQuery) {
		t.Fatalf("501 canonical customer roots error=%v", err)
	}
	overBoundary := insertRadarVisitorMergeChain(t, native, 128)
	assertCanonicalAgreementFailure(t, ctx, uow, reader, "depth", customerdomain.CustomerID(overBoundary[0]), query.ErrInvalidQuery)
}

func insertRadarVisitorMergeChain(t *testing.T, native *pgxpool.Pool, pointers int) []int64 {
	t.Helper()
	chain := make([]int64, 0, pointers+1)
	for index := 0; index <= pointers; index++ {
		id := insertRadarVisitorCustomer(t, native, "active", nil)
		if len(chain) > 0 {
			if _, err := native.Exec(context.Background(), `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=CURRENT_TIMESTAMP WHERE id=$1`, chain[len(chain)-1], id); err != nil {
				t.Fatal(err)
			}
		}
		chain = append(chain, id)
	}
	return chain
}

func assertCanonicalAgreementFailure(t *testing.T, ctx context.Context, uow platformport.UnitOfWork, reader query.PostgreSQL, name string, id customerdomain.CustomerID, expected error) {
	t.Helper()
	err := uow.Within(ctx, func(tx context.Context) error {
		_, existingErr := reader.CanonicalLineage(tx, id)
		if !errors.Is(existingErr, expected) {
			t.Fatalf("%s existing CanonicalLineage error=%v want category %v", name, existingErr, expected)
		}
		_, batchErr := reader.AdminRadarVisitorIdentities(tx, "wecom-corp:radar-visitor", []customerdomain.CustomerID{id})
		if !errors.Is(batchErr, expected) {
			t.Fatalf("%s batch visitor lineage error=%v want category %v", name, batchErr, expected)
		}
		_, rootsErr := reader.CanonicalCustomerRoots(tx, []customerdomain.CustomerID{id})
		if !errors.Is(rootsErr, expected) {
			t.Fatalf("%s canonical root error=%v want category %v", name, rootsErr, expected)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func insertRadarVisitorCustomer(t *testing.T, native *pgxpool.Pool, status string, target *int64) int64 {
	t.Helper()
	var id int64
	if err := native.QueryRow(context.Background(), `INSERT INTO customers(status,merged_into_customer_id,merged_at) VALUES($1,$2::bigint,CASE WHEN $1='merged' AND $2::bigint IS NOT NULL THEN CURRENT_TIMESTAMP ELSE NULL END) RETURNING id`, status, target).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func radarVisitorIdentityPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL := environmentValue("AICRM_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Radar visitor Identity PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "radar_visitor_identity_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate identity migration")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0002_identity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(cleanupCtx)
	}
}
