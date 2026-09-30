package wecom

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

type descriptionKnownIdentity struct {
	customer customerdomain.CustomerID
	identity int64
}

func (f descriptionKnownIdentity) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: f.customer, IdentityID: f.identity}, nil
}
func TestExplicitDescriptionBackfillKeepsDirectoryReadOnlyPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	store := PostgreSQLCustomerSyncStore{}
	var customer customerdomain.CustomerID
	var identity, id int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customer); err != nil {
		t.Fatal(err)
	}
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:corp-1','woSyntheticDescription','verified','test',1,clock_timestamp()) RETURNING id`, customer).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if err := pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids) VALUES('explicit-description-backfill','description_backfill','ingesting','wecom-corp:corp-1','["staff"]') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	writer := &descriptionGapWriter{}
	audit := &descriptionGapAudit{}
	s := CustomerSyncService{Store: store, UOW: uow, IdentityResolver: descriptionKnownIdentity{customer, identity}, DescriptionIntents: writer, Audit: audit}
	note := "source description"
	if err := s.ingestPage(ctx, CustomerSyncRun{ID: id, Trigger: "description_backfill", CorpScope: "wecom-corp:corp-1", Version: 1, StaffIDs: []string{"staff"}}, "staff", wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{{ExternalUserID: "woSyntheticDescription", FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff", Description: &note, DescriptionProjected: true, TagsProjected: true, Tags: []wecomport.ExternalContactTag{{ProviderTagID: "tag", Type: 1}}}}}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var run CustomerSyncRun
	if err := uow.Within(ctx, func(tx context.Context) error { var err error; run, err = store.Get(tx, id); return err }); err != nil {
		t.Fatal(err)
	}
	if err := s.completeDescriptionBackfill(ctx, run); err != nil {
		t.Fatal(err)
	}

	var current, coverage int
	if err := pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM wecom_external_contact_profiles)+(SELECT count(*) FROM wecom_customer_owner_observations)+(SELECT count(*) FROM wecom_customer_tag_observations)+(SELECT count(*) FROM wecom_customer_tag_history)+(SELECT count(*) FROM wecom_directory_publications),(SELECT count(*) FROM wecom_contact_description_source_observations WHERE source_run_id=$1 AND description_projected)`, id).Scan(&current, &coverage); err != nil {
		t.Fatal(err)
	}
	if current != 0 || coverage != 1 || writer.calls != 1 || len(audit.events) != 2 {
		t.Fatalf("directory writes=%d coverage=%d intents=%d audits=%d", current, coverage, writer.calls, len(audit.events))
	}
}
