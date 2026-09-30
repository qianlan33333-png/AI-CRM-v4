package wecom

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestDirectoryPublicationStagingConcurrencyRollbackAndFieldPresencePostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	store := PostgreSQLCustomerSyncStore{}
	service := CustomerSyncService{Store: store, Projection: descriptionGapProjection{}, Timeline: descriptionGapTimeline{}}
	var id customerdomain.CustomerID
	var identityID int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:corp-1','synthetic-external','verified','test',1,clock_timestamp()) RETURNING id`, id).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	provision := identityport.ProvisionResult{CustomerID: id, IdentityID: identityID}
	at := time.Date(2026, 9, 30, 16, 59, 0, 0, time.UTC)
	newRun := func(key, trigger string, clock time.Time) CustomerSyncRun {
		t.Helper()
		var run CustomerSyncRun
		if err := uow.Within(ctx, func(tx context.Context) error {
			native, _ := platformpostgres.RequireTransaction(tx)
			return native.QueryRow(tx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,started_at,staff_ids) VALUES($1,$2,'reconciling','wecom-corp:corp-1',$3,'["one","two"]') RETURNING id`, key, trigger, clock).Scan(&run.ID)
		}); err != nil {
			t.Fatal(err)
		}
		run.CorpScope = "wecom-corp:corp-1"
		run.Trigger = trigger
		run.Version = 1
		return run
	}
	stage := func(run CustomerSyncRun, clock time.Time, name string, follows []wecomport.ExternalContactFollowInfo) {
		t.Helper()
		if err := uow.Within(ctx, func(tx context.Context) error {
			return store.StageContact(tx, run.ID, provision, wecomport.ExternalContact{ExternalUserID: "synthetic-external", Name: name, FollowInfo: follows}, clock)
		}); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(run CustomerSyncRun, clock time.Time, rollback bool) error {
		return uow.Within(ctx, func(tx context.Context) error {
			if err := service.publishStaged(tx, run, clock); err != nil {
				return err
			}
			if run.Trigger == "new_contact" {
				if err := store.ReconcileCustomerFollows(tx, run.ID, int64(id), at.Add(3*time.Minute)); err != nil {
					return err
				}
			}
			if err := store.Complete(tx, run.ID, run.Version, 0); err != nil {
				return err
			}
			if err := store.EndPublication(tx, run, clock); err != nil {
				return err
			}
			if rollback {
				return errors.New("synthetic commit failure")
			}
			return nil
		})
	}
	baseline := newRun("baseline", "manual", at)
	a := wecomport.ExternalContactTag{ProviderTagID: "A", Name: "A", Type: 1}
	b := wecomport.ExternalContactTag{ProviderTagID: "B", Name: "B", Type: 1}
	stage(baseline, at, "baseline", []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a}}})
	// A duplicate customer observed via another staff page must not lose that employee.
	stage(baseline, at.Add(time.Second), "baseline", []wecomport.ExternalContactFollowInfo{{EmployeeID: "two", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a}}})
	if err := publish(baseline, at.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	full := newRun("slow-full", "manual", at.Add(2*time.Minute))
	stage(full, at.Add(2*time.Minute), "old-response", []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{}}})
	if err := uow.Within(ctx, func(tx context.Context) error {
		p, err := store.DirectoryCustomerProfile(tx, id)
		if err != nil {
			return err
		}
		if p.DisplayName != "baseline" || len(p.FollowUsers) != 2 {
			t.Fatalf("partial page became visible: %+v", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	incremental := newRun("fresh-callback", "new_contact", at.Add(3*time.Minute))
	stage(incremental, at.Add(3*time.Minute), "new-response", []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a, b}}})
	if err := publish(incremental, at.Add(4*time.Minute), true); err == nil {
		t.Fatal("rollback injection succeeded")
	}
	if err := uow.Within(ctx, func(tx context.Context) error {
		p, err := store.DirectoryCustomerProfile(tx, id)
		if err != nil {
			return err
		}
		h, err := store.CustomerTagHistory(tx, wecomport.TagHistoryQuery{CustomerID: id})
		if err != nil {
			return err
		}
		if p.DisplayName != "baseline" || p.Publication.Revision != 1 || len(h.Items) != 2 {
			t.Fatalf("partial transaction leaked: profile=%+v history=%+v", p, h)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Pin a reader to the baseline while a real concurrent publisher waits.
	entered := make(chan struct{})
	done := make(chan error, 1)
	if err := uow.Within(ctx, func(tx context.Context) error {
		p, err := store.DirectoryCustomerProfile(tx, id)
		if err != nil {
			return err
		}
		go func() { close(entered); done <- publish(incremental, at.Add(4*time.Minute), false) }()
		<-entered
		select {
		case err := <-done:
			t.Fatalf("publication crossed pinned reader: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		next, err := store.DirectoryPublication(tx, "wecom-corp:corp-1")
		if err != nil {
			return err
		}
		if next.Revision != p.Publication.Revision {
			t.Fatal("reader mixed revisions")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := publish(full, at.Add(5*time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if err := uow.Within(ctx, func(tx context.Context) error {
		p, err := store.DirectoryCustomerProfile(tx, id)
		if err != nil {
			return err
		}
		h, err := store.CustomerTagHistory(tx, wecomport.TagHistoryQuery{CustomerID: id, Limit: 2})
		if err != nil {
			return err
		}
		if p.DisplayName != "new-response" || len(p.FollowUsers[0].Tags) != 2 || len(h.Items) != 2 || h.NextBeforeID == 0 {
			t.Fatalf("older full response regressed data: %+v history=%+v", p, h)
		}
		tail, err := store.CustomerTagHistory(tx, wecomport.TagHistoryQuery{CustomerID: id, BeforeID: h.NextBeforeID, Limit: 2})
		if err != nil {
			return err
		}
		if len(tail.Items) != 1 {
			t.Fatal(tail)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A full run may mix a page fetched before the complete callback with a
	// newer page. The old page cannot introduce an unseen employee, nor revive
	// the employee excluded by the newer complete detail.
	mixed := newRun("mixed-page-clocks", "manual", at.Add(6*time.Minute))
	stage(mixed, at.Add(7*time.Minute), "new-response", []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a, b}}})
	stage(mixed, at.Add(2*time.Minute), "old-response", []wecomport.ExternalContactFollowInfo{{EmployeeID: "never-observed", TagsProjected: true, Tags: []wecomport.ExternalContactTag{b}}, {EmployeeID: "two", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a}}})
	if err := publish(mixed, at.Add(8*time.Minute), false); err != nil {
		t.Fatal(err)
	}
	var activeEmployees, ghostEvents int
	if err := pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_owner_observations WHERE customer_id=$1 AND relationship_status='active'`, id).Scan(&activeEmployees); err != nil {
		t.Fatal(err)
	}
	if err := pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_tag_history WHERE customer_id=$1 AND employee_id='never-observed'`, id).Scan(&ghostEvents); err != nil {
		t.Fatal(err)
	}
	if activeEmployees != 1 || ghostEvents != 0 {
		t.Fatalf("older mixed page introduced a follower or history: active=%d ghost_events=%d", activeEmployees, ghostEvents)
	}

}

func TestDirectoryPublicationScopeChangeAndManualDedupPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	store := PostgreSQLCustomerSyncStore{}
	if _, err := pool.Native().Exec(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,completed_at) VALUES('previous','manual','succeeded','wecom-corp:corp-1','["a","b"]',clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if err := uow.Within(ctx, func(tx context.Context) error {
		first, _, err := store.Create(tx, CreateCustomerSyncRun{RunKey: "first-manual", Trigger: "manual", CorpScope: "wecom-corp:corp-1"})
		if err != nil {
			return err
		}
		second, replay, err := store.Create(tx, CreateCustomerSyncRun{RunKey: "second-manual", Trigger: "manual", CorpScope: "wecom-corp:corp-1"})
		if err != nil {
			return err
		}
		if !replay || first.ID != second.ID {
			t.Fatal("manual refresh did not reuse active run")
		}
		native, _ := platformpostgres.RequireTransaction(tx)
		if _, err = native.Exec(tx, `UPDATE wecom_customer_sync_runs SET staff_ids='["a"]' WHERE id=$1`, first.ID); err != nil {
			return err
		}
		complete, err := store.PublicationScopeComplete(tx, first)
		if err != nil {
			return err
		}
		if complete {
			t.Fatal("permission shrink treated as complete directory")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
