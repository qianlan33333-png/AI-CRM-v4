package wecom

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type externalContactReaderStub struct {
	contact wecomport.ExternalContact
	err     error
	calls   int
}

func (stub *externalContactReaderStub) ReadExternalContact(_ context.Context, externalUserID string) (wecomport.ExternalContact, error) {
	stub.calls++
	if stub.err != nil {
		return wecomport.ExternalContact{}, stub.err
	}
	return stub.contact, nil
}

func TestDirectoryMissingFollowedAtClearsPriorFriendTime(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	customerID := newObservationCustomer(t, ctx, pool.Native())
	oldAt := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	oldRun := seedObservationRun(t, ctx, pool.Native(), "followed-at-known", "manual", "wecom-corp:followed-at", "staff-1", oldAt)
	store := PostgreSQLCustomerSyncStore{}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, oldRun, "wecom-corp:followed-at", customerID, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", FollowedAt: &oldAt}}, oldAt)
	}); err != nil {
		t.Fatal(err)
	}

	newRunAt := oldAt.Add(24 * time.Hour)
	newRun := seedObservationRun(t, ctx, pool.Native(), "followed-at-unknown", "manual", "wecom-corp:followed-at", "staff-1", newRunAt)
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, newRun, "wecom-corp:followed-at", customerID, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1"}}, newRunAt)
	}); err != nil {
		t.Fatal(err)
	}
	var followedAt *time.Time
	if err = pool.Native().QueryRow(ctx, `SELECT followed_at FROM wecom_customer_owner_observations WHERE customer_id=$1 AND corp_scope='wecom-corp:followed-at' AND employee_id='staff-1'`, customerID).Scan(&followedAt); err != nil {
		t.Fatal(err)
	}
	if followedAt != nil {
		t.Fatalf("unknown Provider createtime must not inherit a prior relationship time: %v", followedAt)
	}
}

func TestProviderTagCustomerListerUsesOnlyActiveOfficialTagsFromCompletedRuns(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	matching := newObservationCustomer(t, ctx, pool.Native())
	personal := newObservationCustomer(t, ctx, pool.Native())
	stale := newObservationCustomer(t, ctx, pool.Native())
	pending := newObservationCustomer(t, ctx, pool.Native())
	completedRun := seedObservationRun(t, ctx, pool.Native(), "directory-filter-completed", "tag_refresh", "wecom-corp:filter", "staff-filter", now)
	var pendingRun int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids)
		VALUES('directory-filter-pending','manual','queued','wecom-corp:filter',jsonb_build_array('staff-filter'::text)) RETURNING id`).Scan(&pendingRun); err != nil {
		t.Fatal(err)
	}
	insert := func(customerID customerdomain.CustomerID, providerType int16, state string, runID int64) {
		t.Helper()
		var staleAt any
		if state == "stale" {
			staleAt = now
		}
		if _, err = pool.Native().Exec(ctx, `INSERT INTO wecom_customer_tag_observations(customer_id,corp_scope,employee_id,provider_tag_id,provider_tag_type,observed_name,observation_status,last_seen_run_id,observed_at,stale_at)
			VALUES($1,'wecom-corp:filter','staff-filter','official-tag',$2,'目录筛选',$3,$4,$5,$6)`, customerID, providerType, state, runID, now, staleAt); err != nil {
			t.Fatal(err)
		}
	}
	insert(matching, 1, "active", completedRun)
	insert(personal, 2, "active", completedRun)
	insert(stale, 1, "stale", completedRun)
	insert(pending, 1, "active", pendingRun)

	var ids []customerdomain.CustomerID
	err = unit.Within(ctx, func(tx context.Context) error {
		var readErr error
		ids, readErr = (PostgreSQLCustomerSyncStore{}).ListCustomerIDsForProviderTag(tx, "official-tag", 10)
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != matching {
		t.Fatalf("directory tag customers=%v want=[%d]", ids, matching)
	}
}

func TestContactDescriptionSourceCoverageCountsCurrentDistinctFollowPairs(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := PostgreSQLCustomerSyncStore{}
	at := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	runID := seedObservationRun(t, ctx, pool.Native(), "description-source-coverage", "manual", "wecom-corp:coverage", "staff-1", at)
	first := newObservationCustomer(t, ctx, pool.Native())
	second := newObservationCustomer(t, ctx, pool.Native())
	present := "manual"
	if err = unit.Within(ctx, func(tx context.Context) error {
		if err := store.UpsertProfileObservations(tx, runID, "wecom-corp:coverage", first, []wecomport.ExternalContactFollowInfo{
			{EmployeeID: "staff-1", Description: &present, DescriptionProjected: true},
			{EmployeeID: "staff-2"},
		}, at); err != nil {
			return err
		}
		// A repeated observation within one run must never turn a proven field
		// projection into an omission. The next run remains authoritative.
		if err := store.UpsertProfileObservations(tx, runID, "wecom-corp:coverage", first, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1"}}, at.Add(time.Second)); err != nil {
			return err
		}
		return store.UpsertProfileObservations(tx, runID, "wecom-corp:coverage", second, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-3", Description: &present, DescriptionProjected: true}}, at)
	}); err != nil {
		t.Fatal(err)
	}
	secondRunID := seedObservationRun(t, ctx, pool.Native(), "description-source-coverage-next", "manual", "wecom-corp:coverage", "staff-1", at.Add(time.Minute))
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, secondRunID, "wecom-corp:coverage", first, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1"}}, at.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	service := CustomerSyncService{DescriptionSourceCoverage: store, UOW: unit}
	coverage, err := service.ContactDescriptionSourceStats(ctx, runID)
	if err != nil || coverage.Observed != 3 || coverage.Projected != 2 || coverage.Omitted != 1 {
		t.Fatalf("first coverage=%+v err=%v", coverage, err)
	}
	secondCoverage, err := service.ContactDescriptionSourceStats(ctx, secondRunID)
	if err != nil || secondCoverage.Observed != 1 || secondCoverage.Projected != 0 || secondCoverage.Omitted != 1 {
		t.Fatalf("second coverage=%+v err=%v", secondCoverage, err)
	}
}

func TestCustomerTagObservationFullEmptySetAndReconcileKeepNewerReadPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := PostgreSQLCustomerSyncStore{}
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	customerID := newObservationCustomer(t, ctx, pool.Native())
	fullRun := seedObservationRun(t, ctx, pool.Native(), "full-empty", "manual", "wecom-corp:corp-1", "staff-1", at)
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, fullRun, "wecom-corp:corp-1", customerID, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "old", Name: "Old", Type: 1}}}}, at)
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, fullRun, "wecom-corp:corp-1", customerID, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", TagsProjected: true, Tags: []wecomport.ExternalContactTag{}}}, at.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	assertActiveTags(t, ctx, pool.Native(), customerID)
	newer := at.Add(2 * time.Minute)
	newerRun := seedObservationRun(t, ctx, pool.Native(), "new-contact-newtag", "new_contact", "wecom-corp:corp-1", "staff-1", newer)
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, newerRun, "wecom-corp:corp-1", customerID, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", TagsProjected: true, Tags: []wecomport.ExternalContactTag{{ProviderTagID: "newtag", Name: "New", Type: 1}}}}, newer)
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.ReconcileProfileObservations(tx, fullRun, newer.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	assertActiveTags(t, ctx, pool.Native(), customerID, "newtag")
}

func newObservationCustomer(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) customerdomain.CustomerID {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return customerdomain.CustomerID(id)
}
func seedObservationRun(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, key, trigger, scope, staff string, at time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,started_at,completed_at) VALUES($1,$2,'succeeded',$3,jsonb_build_array($4::text),$5,$5) RETURNING id`, key, trigger, scope, staff, at).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func assertBlocked(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("concurrent write did not block: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
}
func assertActiveTags(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, customerID customerdomain.CustomerID, want ...string) {
	t.Helper()
	var got []string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(provider_tag_id ORDER BY provider_tag_id),ARRAY[]::text[]) FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='active'`, customerID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("active tags=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("active tags=%v want=%v", got, want)
		}
	}
}

func TestRetiredTagReadbackCannotWriteDirectory(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, _ := platformpostgres.NewUnitOfWork(pool)
	reader := &externalContactReaderStub{}
	service := CustomerTagObservationService{Enabled: true, CorpID: "corp-1", Provider: reader, Store: PostgreSQLCustomerSyncStore{}, UOW: unit}
	if err := service.RefreshCustomerTagObservation(context.Background(), "effect", 1, "employee", "external"); !errors.Is(err, ErrCustomerTagObservationUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if reader.calls != 0 {
		t.Fatal("retired observer called Provider")
	}
	var n int
	if err := pool.Native().QueryRow(context.Background(), `SELECT count(*) FROM wecom_customer_sync_runs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("runs=%d err=%v", n, err)
	}
}
