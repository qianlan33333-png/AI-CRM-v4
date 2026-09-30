package main

import (
	"context"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecom "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

// TestPostgreSQLOwnerHandoffAllRangeUsesPublishedDirectoryAndIgnoresLocalPrecedence proves
// the all-range Provider transfer against published directory facts. Historical
// CRM-local owners cannot change its target set.
func TestPostgreSQLOwnerHandoffAllRangeUsesPublishedDirectoryAndIgnoresLocalPrecedence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 4, MinConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	access := accessstore.NewPostgreSQL()
	cipher, err := customer.NewOwnerHandoffCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	owners := customer.NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	resolver := customerOwnerHandoffCandidates{
		staff:              access,
		relationshipLister: wecom.NewPostgreSQLFollowRelationshipStore(),
		identities:         identityquery.NewPostgreSQL(),
	}
	service, err := customerapp.NewOwnerHandoffService(uow, owners, access, resolver, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	service.SetWeComProviderEnabled(true)
	var governanceOwner, source, target int64
	var localSource, onlyPrimary, locallyReassigned, mixed customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		database, txErr := platformpostgres.RequireTransaction(tx)
		if txErr != nil {
			return txErr
		}
		if txErr = database.QueryRow(tx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('all-range-source','$argon2id$fixture','Source','all-range-source',true,false) RETURNING id`).Scan(&source); txErr != nil {
			return txErr
		}
		if txErr = database.QueryRow(tx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('all-range-target','$argon2id$fixture','Target','all-range-target',true,false) RETURNING id`).Scan(&target); txErr != nil {
			return txErr
		}
		if txErr = database.QueryRow(tx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at) VALUES('all-range-governance-owner','$argon2id$fixture','Governance Owner','all-range-governance-owner',true,true,clock_timestamp()) RETURNING id`).Scan(&governanceOwner); txErr != nil {
			return txErr
		}
		if _, txErr = database.Exec(tx, `INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES($1,'viewer'),($2,'viewer'),($3,'super_admin')`, source, target, governanceOwner); txErr != nil {
			return txErr
		}
		if _, txErr = database.Exec(tx, `INSERT INTO access_super_admin_control(singleton,admin_user_id,version,updated_at) VALUES(TRUE,$1,1,clock_timestamp())`, governanceOwner); txErr != nil {
			return txErr
		}
		for _, destination := range []*customerdomain.CustomerID{&localSource, &onlyPrimary, &locallyReassigned, &mixed} {
			if txErr = database.QueryRow(tx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(destination); txErr != nil {
				return txErr
			}
		}
		if _, txErr = database.Exec(tx, `INSERT INTO customer_local_owners(customer_id,staff_id,source) VALUES($1,$2,'owner_handoff_local_only'),($3,$4,'owner_handoff_local_only'),($5,$2,'owner_handoff_local_only')`, localSource, source, locallyReassigned, target, mixed); txErr != nil {
			return txErr
		}
		var runID int64
		if txErr = database.QueryRow(tx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at) VALUES('owner-handoff-all-range-primary','manual','succeeded','wecom-corp:all-range',clock_timestamp()) RETURNING id`).Scan(&runID); txErr != nil {
			return txErr
		}
		for index, customerID := range []customerdomain.CustomerID{onlyPrimary, locallyReassigned, mixed} {
			var identityID int64
			if txErr = database.QueryRow(tx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:all-range',$2,'verified','all_range_fixture',1,clock_timestamp()) RETURNING id`, customerID, "all-range-external-"+string(rune('a'+index))).Scan(&identityID); txErr != nil {
				return txErr
			}
			if _, txErr = database.Exec(tx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id) VALUES($1,'wecom-corp:all-range',$2,decode(repeat('01',32),'hex'),$3,clock_timestamp(),'all-range-source',$3)`, customerID, identityID, runID); txErr != nil {
				return txErr
			}
			if _, txErr = database.Exec(tx, `INSERT INTO wecom_customer_owner_observations(customer_id,corp_scope,employee_id,relationship_status,last_seen_run_id,observed_at,primary_owner_userid) VALUES($1,'wecom-corp:all-range','all-range-source','active',$2,clock_timestamp(),'all-range-source')`, customerID, runID); txErr != nil {
				return txErr
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewAllOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{
		ActorAdminUserID:   source,
		Mode:               customerport.OwnerHandoffWeComThenCRM,
		SourceStaffID:      source,
		TargetStaffID:      target,
		CorpScope:          "wecom-corp:all-range",
		ConfirmationPhrase: "CONFIRM ALL RANGE",
		IdempotencyKey:     "all-range-preview-001",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[customerdomain.CustomerID]string{}
	for _, row := range preview.Rows {
		got[row.CustomerID] = row.State
	}
	for _, customerID := range []customerdomain.CustomerID{onlyPrimary, locallyReassigned, mixed} {
		if got[customerID] != "ready" {
			t.Fatalf("customer %d all-range state=%q rows=%+v", customerID, got[customerID], preview.Rows)
		}
	}
	if _, found := got[localSource]; found || len(got) != 3 {
		t.Fatalf("local owner assigned to another staff was reselected: rows=%+v", preview.Rows)
	}

}

type ownerHandoffAllRangeStaff map[int64]accessdomain.User

func (s ownerHandoffAllRangeStaff) UserByID(_ context.Context, id int64, _ bool) (accessdomain.User, error) {
	user, found := s[id]
	if !found {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return user, nil
}

func (s ownerHandoffAllRangeStaff) UserByWeComUserID(_ context.Context, userID string, _ bool) (accessdomain.User, error) {
	for _, user := range s {
		if user.WeComUserID == userID {
			return user, nil
		}
	}
	return accessdomain.User{}, accessdomain.ErrNotFound
}

type ownerHandoffAllRangeOwners struct {
	localIDs []customerdomain.CustomerID
	owners   map[customerdomain.CustomerID]customerport.LocalOwner
}

func (s ownerHandoffAllRangeOwners) LocalOwner(_ context.Context, customerID customerdomain.CustomerID, _ bool) (customerport.LocalOwner, bool, error) {
	owner, found := s.owners[customerID]
	return owner, found, nil
}

func (s ownerHandoffAllRangeOwners) ListOwnerHandoffCustomerIDs(_ context.Context, _ int64, _ int) ([]customerdomain.CustomerID, error) {
	return append([]customerdomain.CustomerID(nil), s.localIDs...), nil
}

type ownerHandoffAllRangePrimaryLister struct {
	ids []customerdomain.CustomerID
}

func (s ownerHandoffAllRangePrimaryLister) ListOwnerHandoffPrimaryOwnerCustomerIDs(_ context.Context, _ string, _ string, _ int) ([]customerdomain.CustomerID, error) {
	return append([]customerdomain.CustomerID(nil), s.ids...), nil
}

func TestOwnerHandoffAllRangeRejectsRetiredLocalMode(t *testing.T) {
	adapter := customerOwnerHandoffCandidates{staff: ownerHandoffAllRangeStaff{41: {ID: 41, Active: true, WeComUserID: "source"}}}
	_, err := adapter.DiscoverOwnerHandoffCustomerIDs(context.Background(), customerport.OwnerHandoffLocalOnly, 41, "wecom-corp:fixture", 20000)
	if err != customerapp.ErrOwnerHandoffInvalid {
		t.Fatalf("local discovery err=%v", err)
	}
}
