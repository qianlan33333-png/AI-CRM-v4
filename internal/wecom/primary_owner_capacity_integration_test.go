package wecom

import (
	"context"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// Publication inserts the entire new directory inside its transaction. The
// planner therefore still sees the pre-publication (empty) table statistics.
// Exercise that case at production scale, including two employees per customer.
func TestPostgreSQLPrimaryOwnerPublicationWithUnanalyzedFullDirectory(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"wecom_external_contact_profiles", "wecom_customer_owner_observations"} {
		if _, err = pool.Native().Exec(ctx, "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}
	var elapsed time.Duration
	err = unit.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		var runID int64
		if e = tx.QueryRow(txctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope) VALUES('capacity-full-directory','manual','reconciling','wecom-corp:capacity') RETURNING id`).Scan(&runID); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `WITH roots AS (
 INSERT INTO customers(status) SELECT 'active' FROM generate_series(1,23581) RETURNING id
), identities AS (
 INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
 SELECT id,'wecom_external_userid','wecom-corp:capacity','capacity-'||id,'verified','test-fixture',1,CURRENT_TIMESTAMP FROM roots RETURNING id,customer_id
)
INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,activation_status,profile_digest,last_seen_run_id,fetched_at,updated_at)
 SELECT customer_id,'wecom-corp:capacity',id,'active',decode(repeat('00',32),'hex'),$1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP FROM identities`, runID); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `INSERT INTO wecom_customer_owner_observations(customer_id,corp_scope,employee_id,last_seen_run_id,observed_at,updated_at)
 SELECT p.customer_id,p.corp_scope,employee,$1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP FROM wecom_external_contact_profiles p CROSS JOIN unnest(ARRAY['zara','bob']) employee`, runID); e != nil {
			return e
		}
		started := time.Now()
		if e = NewPostgreSQLCustomerSyncStore().RefreshProfilePrimaryOwners(txctx, runID, started); e != nil {
			return e
		}
		elapsed = time.Since(started)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var profiles, owners, wrong int64
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM wecom_external_contact_profiles),(SELECT count(*) FROM wecom_customer_owner_observations),(SELECT count(*) FROM wecom_external_contact_profiles WHERE primary_owner_userid<>'bob')+(SELECT count(*) FROM wecom_customer_owner_observations WHERE primary_owner_userid<>'bob')`).Scan(&profiles, &owners, &wrong); err != nil {
		t.Fatal(err)
	}
	if profiles != 23581 || owners != 47162 || wrong != 0 {
		t.Fatalf("profiles=%d owners=%d incorrect assignments=%d", profiles, owners, wrong)
	}
	t.Logf("primary owner publication: 23581 customers / 47162 observations in %s", elapsed)
}
