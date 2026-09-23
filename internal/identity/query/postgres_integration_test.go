package query_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLOneIDQueries(t *testing.T) {
	databaseURL := environmentValue("AICRM_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping OneID query PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "oneid_query_test_" + hex.EncodeToString(random)
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET search_path TO `+pgx.Identifier{schema}.Sanitize())
		return err
	}
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	}()

	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, source, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "migrations", "0002_identity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	var canonicalID, mergedID, otherID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&canonicalID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `
		INSERT INTO customers(status, merged_into_customer_id, merged_at)
		VALUES ('merged', $1, CURRENT_TIMESTAMP)
		RETURNING id`, canonicalID).Scan(&mergedID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `
		INSERT INTO customer_identities(
			customer_id, kind, scope_key, normalized_value, assurance, source, normalizer_version, status, verified_at
		) VALUES
			($1, 'phone', 'phone:e164', '+13800138000', 'declared', 'integration', 1, 'active', NULL),
			($1, 'mp_openid', 'wechat-app:commerce', 'payer-openid', 'verified', 'integration', 1, 'active', CURRENT_TIMESTAMP),
			($2, 'mp_openid', 'wechat-app:commerce-other', 'beneficiary-openid', 'verified', 'integration', 1, 'active', CURRENT_TIMESTAMP),
			($1, 'ext', 'ext:test', 'retired-secret', 'declared', 'integration', 1, 'retired', NULL)`, canonicalID, otherID); err != nil {
		t.Fatal(err)
	}
	// Backfill real seven-digit aliases after existing identities exist. Existing
	// canonical roots and identity ownership must survive the migration.
	publicMigration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "migrations", "0178_customer_public_numbers.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(publicMigration)); err != nil {
		t.Fatal(err)
	}

	var evidenceID int64
	if err = native.QueryRow(ctx, `
		INSERT INTO identity_link_evidence(
			left_customer_id, right_customer_id, evidence_type, strength, source, evidence_digest, policy_version
		) VALUES ($1, $2, 'integration', 'strong', 'integration', 'digest-not-for-query', 'v1')
		RETURNING id`, canonicalID, otherID).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `
		INSERT INTO customer_merge_candidates(
			left_customer_id, right_customer_id, left_customer_version, right_customer_version,
			evidence_id, evidence_strength, reason
		) VALUES ($1, $2, 1, 1, $3, 'strong', 'cross_root_link_requires_confirmation')`, canonicalID, otherID, evidenceID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `
		INSERT INTO customer_identity_conflicts(left_customer_id, right_customer_id, evidence_id, reason)
		VALUES ($1, $2, $3, 'two_wecom_roots')`, canonicalID, otherID, evidenceID); err != nil {
		t.Fatal(err)
	}

	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := query.NewPostgreSQL()
	if _, err = store.Customer(ctx, customerdomain.CustomerID(mergedID)); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("query without transaction error=%v", err)
	}
	if _, err = store.CanonicalCustomers(ctx, []customerdomain.CustomerID{customerdomain.CustomerID(mergedID)}); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("bulk query without transaction error=%v", err)
	}

	if err = unit.Within(ctx, func(txContext context.Context) error {
		numbers, numberErr := store.CustomerPublicNumbers(txContext, []customerdomain.CustomerID{customerdomain.CustomerID(canonicalID), customerdomain.CustomerID(mergedID), customerdomain.CustomerID(otherID)})
		if numberErr != nil {
			return numberErr
		}
		seen := map[string]bool{}
		for id, number := range numbers {
			if len(number) != 7 || seen[number] {
				t.Fatalf("invalid or duplicate public number: %q", number)
			}
			seen[number] = true
			reverse, found, e := store.CustomerForPublicNumber(txContext, number)
			if e != nil || !found || reverse != id {
				t.Fatalf("public number reverse mismatch: %v", e)
			}
		}
		if len(seen) != 3 {
			t.Fatal("missing public numbers")
		}

		detail, queryErr := store.Customer(txContext, customerdomain.CustomerID(mergedID))
		if queryErr != nil {
			return queryErr
		}
		if detail.CustomerID != customerdomain.CustomerID(mergedID) || detail.Status != customerdomain.StatusMerged ||
			detail.CanonicalCustomerID != customerdomain.CustomerID(canonicalID) || detail.CanonicalStatus != customerdomain.StatusActive {
			t.Errorf("customer detail=%#v", detail)
		}
		if len(detail.Identities) != 2 || detail.Identities[0].Kind != identitydomain.KindPhone || detail.Identities[0].Scope != "phone:e164" || detail.Identities[1].Kind != identitydomain.KindMPOpenID {
			t.Errorf("safe active identities=%#v", detail.Identities)
		}
		resolved, queryErr := store.ResolveCommerce(txContext, identityport.CommerceReferenceSet{References: []identitydomain.Reference{{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:commerce", Value: "payer-openid", Assurance: identitydomain.AssuranceVerified, Source: "integration"}}})
		if queryErr != nil || resolved.Status != identityport.CommerceResolved || resolved.CustomerID != customerdomain.CustomerID(canonicalID) || len(resolved.Matches) != 1 {
			t.Errorf("commerce resolved=%#v err=%v", resolved, queryErr)
		}
		conflicted, queryErr := store.ResolveCommerce(txContext, identityport.CommerceReferenceSet{References: []identitydomain.Reference{
			{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:commerce", Value: "payer-openid", Assurance: identitydomain.AssuranceVerified, Source: "integration"},
			{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:commerce-other", Value: "beneficiary-openid", Assurance: identitydomain.AssuranceVerified, Source: "integration"},
		}})
		if queryErr != nil || conflicted.Status != identityport.CommerceConflict || conflicted.CustomerID != 0 {
			t.Errorf("commerce conflict=%#v err=%v", conflicted, queryErr)
		}
		invalid, queryErr := store.ResolveCommerce(txContext, identityport.CommerceReferenceSet{References: []identitydomain.Reference{{Kind: identitydomain.KindMPOpenID, Scope: "", Value: "payer-openid", Assurance: identitydomain.AssuranceVerified, Source: "integration"}}})
		if queryErr != nil || invalid.Status != identityport.CommerceInvalid || invalid.CustomerID != 0 || len(invalid.Matches) != 0 {
			t.Errorf("scope-less commerce identity=%#v err=%v", invalid, queryErr)
		}
		if len(detail.MergeLineage) != 0 {
			t.Errorf("unexpected merge lineage=%#v", detail.MergeLineage)
		}
		roots, queryErr := store.CanonicalCustomers(txContext, []customerdomain.CustomerID{
			customerdomain.CustomerID(mergedID),
			customerdomain.CustomerID(canonicalID),
			customerdomain.CustomerID(otherID),
			customerdomain.CustomerID(mergedID),
		})
		if queryErr != nil {
			return queryErr
		}
		want := []query.CanonicalCustomerRoot{
			{RequestedCustomerID: customerdomain.CustomerID(mergedID), CustomerID: customerdomain.CustomerID(canonicalID)},
			{RequestedCustomerID: customerdomain.CustomerID(canonicalID), CustomerID: customerdomain.CustomerID(canonicalID)},
			{RequestedCustomerID: customerdomain.CustomerID(otherID), CustomerID: customerdomain.CustomerID(otherID)},
			{RequestedCustomerID: customerdomain.CustomerID(mergedID), CustomerID: customerdomain.CustomerID(canonicalID)},
		}
		if len(roots) != len(want) {
			t.Fatalf("canonical roots=%#v", roots)
		}
		for index := range want {
			if roots[index] != want[index] {
				t.Fatalf("canonical root[%d]=%#v want %#v", index, roots[index], want[index])
			}
		}

		conflicts, queryErr := store.Conflicts(txContext, query.ListOptions{})
		if queryErr != nil {
			return queryErr
		}
		if conflicts.Limit != query.DefaultLimit || conflicts.Offset != 0 || len(conflicts.Items) != 1 ||
			conflicts.Items[0].Reason != "two_wecom_roots" || conflicts.Items[0].Status != "open" {
			t.Errorf("conflicts=%#v", conflicts)
		}

		candidates, queryErr := store.MergeCandidates(txContext, query.ListOptions{Limit: 1})
		if queryErr != nil {
			return queryErr
		}
		if candidates.Limit != 1 || len(candidates.Items) != 1 || candidates.Items[0].Reason != "cross_root_link_requires_confirmation" ||
			candidates.Items[0].EvidenceStrength != identitydomain.EvidenceStrong || candidates.Items[0].Status != "open" {
			t.Errorf("candidates=%#v", candidates)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err = unit.Within(ctx, func(txContext context.Context) error {
		_, queryErr := store.Customer(txContext, customerdomain.CustomerID(otherID+10000))
		return queryErr
	}); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("missing customer error=%v", err)
	}
	if err = unit.Within(ctx, func(txContext context.Context) error {
		_, queryErr := store.CanonicalCustomers(txContext, []customerdomain.CustomerID{customerdomain.CustomerID(otherID), customerdomain.CustomerID(otherID + 10000)})
		return queryErr
	}); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("missing bulk customer error=%v", err)
	}
	// The exact root FOR UPDATE lock used by merge cannot interleave with a
	// business eligibility UoW holding the new read Port's lineage locks.
	if err = unit.Within(ctx, func(txContext context.Context) error {
		roots, e := store.LockedCanonicalLineage(txContext, customerdomain.CustomerID(mergedID))
		if e != nil {
			return e
		}
		if len(roots) != 2 {
			return errors.New("unexpected locked lineage")
		}
		concurrent, e := native.Begin(ctx)
		if e != nil {
			return e
		}
		defer concurrent.Rollback(ctx)
		_, e = concurrent.Exec(ctx, `SELECT id FROM customers WHERE id=$1 FOR UPDATE NOWAIT`, canonicalID)
		if e == nil {
			return errors.New("merge root lock bypassed locked lineage")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Once eligibility commits, the merge root lock is available again.
	mergeTx, e := native.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = mergeTx.Exec(ctx, `SELECT id FROM customers WHERE id=$1 FOR UPDATE NOWAIT`, canonicalID); e != nil {
		t.Fatal(e)
	}
	_ = mergeTx.Rollback(ctx)
	if _, err = store.Conflicts(ctx, query.ListOptions{Status: "deleted", Limit: 1}); !errors.Is(err, query.ErrInvalidQuery) {
		t.Fatalf("invalid status error=%v", err)
	}
	// Concurrent creates allocate persisted numbers without changing existing roots.
	type allocation struct {
		number int64
		err    error
	}
	allocations := make(chan allocation, 16)
	for range 16 {
		go func() {
			var number int64
			err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING public_number`).Scan(&number)
			allocations <- allocation{number, err}
		}()
	}
	seen := map[int64]bool{}
	for range 16 {
		value := <-allocations
		if value.err != nil || value.number < 1000003 || value.number > 9999999 || seen[value.number] {
			t.Fatalf("invalid or duplicate allocation: %+v", value)
		}
		seen[value.number] = true
	}

}

func environmentValue(key string) string {
	prefix := key + "="
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}
