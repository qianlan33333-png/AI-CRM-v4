package wecom

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

func TestDirectoryTagHistoryBaselineDiffReaddAndEmployeeUnionPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := PostgreSQLCustomerSyncStore{}
	var customer customerdomain.CustomerID
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customer); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 30, 3, 0, 0, 0, time.UTC)
	a := wecomport.ExternalContactTag{ProviderTagID: "a", Name: "A", Type: 1}
	b := wecomport.ExternalContactTag{ProviderTagID: "b", Name: "B", Type: 3}
	publish := func(key string, day int, follows []wecomport.ExternalContactFollowInfo) {
		t.Helper()
		clock := at.Add(time.Duration(day) * 24 * time.Hour)
		if err = uow.Within(ctx, func(tx context.Context) error {
			var id int64
			native, _ := platformpostgres.RequireTransaction(tx)
			if e := native.QueryRow(tx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,started_at) VALUES($1,'manual','reconciling','wecom-corp:corp-1',$2) RETURNING id`, key, clock).Scan(&id); e != nil {
				return e
			}
			run := CustomerSyncRun{ID: id, CorpScope: "wecom-corp:corp-1", Trigger: "manual", Status: SyncReconciling, Version: 1}
			if e := store.BeginPublication(tx, run); e != nil {
				return e
			}
			if e := store.UpsertProfileObservations(tx, id, run.CorpScope, customer, follows, clock); e != nil {
				return e
			}
			if e := store.Complete(tx, id, 1, 0); e != nil {
				return e
			}
			return store.EndPublication(tx, run, clock)
		}); err != nil {
			t.Fatal(err)
		}
	}
	publish("baseline-publish", 0, []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a, b}}, {EmployeeID: "two", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a}}})
	publish("remove-and-add-1", 1, []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{b}}, {EmployeeID: "two", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a}}})
	a.Name = "Renamed A"
	publish("rename-no-change", 2, []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a, b}}, {EmployeeID: "two", TagsProjected: true, Tags: []wecomport.ExternalContactTag{a}}})
	publish("missing-tags-keep", 3, []wecomport.ExternalContactFollowInfo{{EmployeeID: "one"}, {EmployeeID: "two"}})
	publish("clear-explicit-4", 4, []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: []wecomport.ExternalContactTag{}}, {EmployeeID: "two", TagsProjected: true, Tags: []wecomport.ExternalContactTag{}}})
	if err = uow.Within(ctx, func(tx context.Context) error {
		page, e := store.CustomerTagHistory(tx, wecomport.TagHistoryQuery{CustomerID: customer})
		if e != nil {
			return e
		}
		if len(page.Items) != 8 {
			t.Fatalf("history=%+v", page.Items)
		}
		baseline, added, removed, readded := 0, 0, 0, 0
		for _, event := range page.Items {
			switch event.EventType {
			case "baseline":
				baseline++
				if event.RegistrationDate != "2026-09-30" || event.ProviderOperatedAt != nil {
					t.Fatal(event)
				}
			case "added":
				added++
			case "removed":
				removed++
			case "readded":
				readded++
			}
		}
		if baseline != 3 || added != 0 || removed != 4 || readded != 1 {
			t.Fatalf("baseline/add/remove/readd %d/%d/%d/%d", baseline, added, removed, readded)
		}
		stats, e := store.CustomerTagHistoryStatistics(tx, wecomport.TagHistoryQuery{CustomerID: customer})
		if e != nil {
			return e
		}
		if len(stats) != 2 || stats[0].EventType != "baseline" || stats[0].EventCount != 2 || stats[1].EventType != "removed" || stats[1].EventCount != 2 {
			t.Fatalf("union stats=%+v", stats)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryTagHistoryRejectsStaleReadAndKeepsPeriodPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	ctx := context.Background()
	store := PostgreSQLCustomerSyncStore{}
	var customer customerdomain.CustomerID
	_ = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customer)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var id int64
	if err := uow.Within(ctx, func(tx context.Context) error {
		native, _ := platformpostgres.RequireTransaction(tx)
		if e := native.QueryRow(tx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at) VALUES('new-contact-history','new_contact','succeeded','wecom-corp:corp-1',$1) RETURNING id`, now).Scan(&id); e != nil {
			return e
		}
		run := CustomerSyncRun{ID: id, CorpScope: "wecom-corp:corp-1", Trigger: "new_contact"}
		if e := store.BeginPublication(tx, run); e != nil {
			return e
		}
		for _, clock := range []time.Time{now, now.Add(-time.Hour), now.Add(time.Hour)} {
			tags := []wecomport.ExternalContactTag{{ProviderTagID: "same", Name: "Label", Type: 1}}
			if clock.Before(now) {
				tags = []wecomport.ExternalContactTag{}
			}
			if e := store.UpsertProfileObservations(tx, id, run.CorpScope, customer, []wecomport.ExternalContactFollowInfo{{EmployeeID: "one", TagsProjected: true, Tags: tags}}, clock); e != nil {
				return e
			}
		}
		if e := store.EndPublication(tx, run, now.Add(time.Hour)); e != nil {
			return e
		}
		page, e := store.CustomerTagHistory(tx, wecomport.TagHistoryQuery{CustomerID: customer})
		if e != nil {
			return e
		}
		if len(page.Items) != 1 || page.Items[0].EventType != "added" {
			t.Fatal(page)
		}
		var start time.Time
		if e = native.QueryRow(tx, `SELECT period_started_at FROM wecom_customer_tag_observations WHERE customer_id=$1`, customer).Scan(&start); e != nil {
			return e
		}
		if !start.Equal(now) {
			t.Fatal(start)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
