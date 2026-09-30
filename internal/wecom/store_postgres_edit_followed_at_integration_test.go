package wecom

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLEditCallbackKeepsCompletedDirectoryFollowTime(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	editAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	observedAt := editAt.Add(-20 * time.Minute)
	followedAt := editAt.Add(-30 * 24 * time.Hour)
	var runID int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs
		(run_key,trigger_type,status,corp_scope,started_at,completed_at)
		VALUES('edit-follow-time-fixture','manual','succeeded','wecom-corp:test',$1,$2) RETURNING id`,
		observedAt.Add(-time.Minute), observedAt.Add(time.Minute)).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	makeCustomer := func(name string, withDirectory bool) int64 {
		t.Helper()
		var customerID int64
		if err := pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if !withDirectory {
			return customerID
		}
		var identityID int64
		if err := pool.Native().QueryRow(ctx, `INSERT INTO customer_identities
			(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'wecom_external_userid','wecom-corp:test',$2,'verified','test-fixture',1,$3) RETURNING id`,
			customerID, name, observedAt).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(name))
		if _, err := pool.Native().Exec(ctx, `INSERT INTO wecom_external_contact_profiles
			(customer_id,corp_scope,external_identity_id,activation_status,profile_digest,last_seen_run_id,fetched_at,updated_at)
			VALUES($1,'wecom-corp:test',$2,'active',$3,$4,$5,$5)`,
			customerID, identityID, digest[:], runID, observedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Native().Exec(ctx, `INSERT INTO wecom_customer_owner_observations
			(customer_id,corp_scope,employee_id,relationship_status,last_seen_run_id,observed_at,updated_at,followed_at)
			VALUES($1,'wecom-corp:test','huangyoucan','active',$2,$3,$3,$4)`,
			customerID, runID, observedAt, followedAt); err != nil {
			t.Fatal(err)
		}
		return customerID
	}
	olderFriend := makeCustomer("edit-older-friend", true)
	unknownFriend := makeCustomer("edit-unknown-friend", false)
	deletedFriend := makeCustomer("edit-deleted-friend", true)
	store := NewPostgreSQLFollowRelationshipStore()
	apply := func(customerID int64, callback, change string, active bool, at time.Time) {
		t.Helper()
		if err := unit.Within(ctx, func(tx context.Context) error {
			_, applyErr := store.ApplyCallbackEvent(tx, CallbackFollowRelationship{
				CallbackID: callback, CorpID: "test", EmployeeID: "huangyoucan",
				CustomerID: customerdomain.CustomerID(customerID), ChangeType: change,
				Active: active, OccurredAt: at,
			})
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply(olderFriend, "edit-older-callback", ChangeEditExternalContact, true, editAt)
	apply(unknownFriend, "edit-unknown-callback", ChangeEditExternalContact, true, editAt)
	apply(deletedFriend, "edit-delete-callback", ChangeDelExternalContact, false, editAt.Add(-time.Minute))
	apply(deletedFriend, "edit-after-delete-callback", ChangeEditExternalContact, true, editAt)

	for _, row := range []struct {
		id   int64
		want *time.Time
	}{
		{olderFriend, &followedAt},
		{unknownFriend, nil},
		{deletedFriend, nil},
	} {
		var got *time.Time
		if err := pool.Native().QueryRow(ctx, `SELECT followed_at FROM wecom_follow_relationships
			WHERE corp_id='test' AND employee_id='huangyoucan' AND customer_id=$1`, row.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if row.want == nil && got != nil || row.want != nil && (got == nil || !got.Equal(*row.want)) {
			t.Fatalf("customer %d followed_at=%v, want %v", row.id, got, row.want)
		}
	}
	var got []struct {
		customerID customerdomain.CustomerID
		followedAt *time.Time
	}
	if err := unit.Within(ctx, func(tx context.Context) error {
		contacts, readErr := store.AudienceContactsForCustomers(tx, time.Now().UTC().Add(time.Minute),
			[]customerdomain.CustomerID{customerdomain.CustomerID(olderFriend)})
		if readErr != nil {
			return readErr
		}
		for _, contact := range contacts {
			if contact.OwnerUserID == "huangyoucan" && contact.Status == "active" {
				got = append(got, struct {
					customerID customerdomain.CustomerID
					followedAt *time.Time
				}{contact.CustomerID, contact.FollowedAt})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].customerID != customerdomain.CustomerID(olderFriend) ||
		got[0].followedAt == nil || !got[0].followedAt.Equal(followedAt) {
		t.Fatalf("audience did not retain completed directory follow time: %+v", got)
	}

	// A subsequent successful directory read can heal an older edit callback
	// whose add time was unknown, without changing the customer's root.
	refreshedAt := editAt.Add(10 * time.Second)
	var refreshRunID int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs
		(run_key,trigger_type,status,corp_scope,started_at,completed_at)
		VALUES('edit-follow-time-recheck','manual','succeeded','wecom-corp:test',$1,$2) RETURNING id`,
		editAt.Add(5*time.Second), refreshedAt).Scan(&refreshRunID); err != nil {
		t.Fatal(err)
	}
	var identityID int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customer_identities
		(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:test','edit-unknown-friend','verified','test-fixture',1,$2) RETURNING id`,
		unknownFriend, refreshedAt).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("edit-unknown-friend"))
	if _, err := pool.Native().Exec(ctx, `INSERT INTO wecom_external_contact_profiles
		(customer_id,corp_scope,external_identity_id,activation_status,profile_digest,last_seen_run_id,fetched_at,updated_at)
		VALUES($1,'wecom-corp:test',$2,'active',$3,$4,$5,$5)`,
		unknownFriend, identityID, digest[:], refreshRunID, refreshedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Native().Exec(ctx, `INSERT INTO wecom_customer_owner_observations
		(customer_id,corp_scope,employee_id,relationship_status,last_seen_run_id,observed_at,updated_at,followed_at)
		VALUES($1,'wecom-corp:test','huangyoucan','active',$2,$3,$3,$4)`,
		unknownFriend, refreshRunID, refreshedAt, followedAt); err != nil {
		t.Fatal(err)
	}
	apply(unknownFriend, "edit-healed-callback", ChangeEditExternalContact, true, editAt.Add(20*time.Second))
	apply(deletedFriend, "edit-still-deleted-callback", ChangeEditExternalContact, true, editAt.Add(30*time.Second))
	for _, row := range []struct {
		id   int64
		want *time.Time
	}{
		{unknownFriend, &followedAt},
		{deletedFriend, nil},
	} {
		var followed *time.Time
		if err := pool.Native().QueryRow(ctx, `SELECT followed_at FROM wecom_follow_relationships
			WHERE corp_id='test' AND employee_id='huangyoucan' AND customer_id=$1`, row.id).Scan(&followed); err != nil {
			t.Fatal(err)
		}
		if row.want == nil && followed != nil || row.want != nil && (followed == nil || !followed.Equal(*row.want)) {
			t.Fatalf("customer %d followed_at after recheck=%v, want %v", row.id, followed, row.want)
		}
	}
}
