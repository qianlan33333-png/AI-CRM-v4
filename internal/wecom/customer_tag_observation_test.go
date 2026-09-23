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

func TestCustomerTagObservationRefreshPostgreSQLPersistsOnlyProviderReadback(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rawCustomerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&rawCustomerID); err != nil {
		t.Fatal(err)
	}
	customerID := customerdomain.CustomerID(rawCustomerID)
	now := time.Date(2026, 9, 6, 9, 30, 0, 0, time.UTC)
	reader := &externalContactReaderStub{contact: wecomport.ExternalContact{
		ExternalUserID: "external-1",
		FollowInfo:     []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "provider-tag-1", Name: "Observed tag", Type: 1}}}},
	}}
	service := CustomerTagObservationService{Enabled: true, CorpID: "corp-1", Provider: reader, Store: PostgreSQLCustomerSyncStore{}, UOW: unit, Now: func() time.Time { return now }}
	if err = service.RefreshCustomerTagObservation(ctx, "eer_1", customerID, "staff-1", "external-1"); err != nil {
		t.Fatal(err)
	}
	var trigger, status, observedID, observedName string
	var runCount, effectCount int
	if err = pool.Native().QueryRow(ctx, `SELECT trigger_type,status FROM wecom_customer_sync_runs`).Scan(&trigger, &status); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		observations, readErr := (PostgreSQLCustomerSyncStore{}).CustomerTagObservations(tx, customerID)
		if readErr != nil {
			return readErr
		}
		if len(observations) != 1 {
			return errors.New("expected one observation")
		}
		observedID, observedName = observations[0].ProviderTagID, observations[0].ObservedName
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_sync_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectCount); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 || trigger != "tag_refresh" || status != "succeeded" || observedID != "provider-tag-1" || observedName != "Observed tag" || runCount != 1 || effectCount != 0 {
		t.Fatalf("calls=%d trigger=%q status=%q observation=%q/%q runs=%d effects=%d", reader.calls, trigger, status, observedID, observedName, runCount, effectCount)
	}

	reader.err = errors.New("directory read unavailable")
	if err = service.RefreshCustomerTagObservation(ctx, "eer_1", customerID, "staff-1", "external-1"); !errors.Is(err, reader.err) {
		t.Fatalf("read error=%v", err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_sync_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("failed read must not persist a refresh run, runs=%d", runCount)
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

func TestCustomerTagRefreshWinsOverInProgressFullSyncAndIsHiddenFromSyncListPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := PostgreSQLCustomerSyncStore{}
	var rawCustomerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&rawCustomerID); err != nil {
		t.Fatal(err)
	}
	customerID := customerdomain.CustomerID(rawCustomerID)
	var full CustomerSyncRun
	if err = unit.Within(ctx, func(tx context.Context) error {
		var createErr error
		full, _, createErr = store.Create(tx, CreateCustomerSyncRun{RunKey: "full-sync-before-tag-refresh", Trigger: "manual", CorpScope: "wecom-corp:corp-1"})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.Transition(tx, full.ID, full.Version, SyncQueued, SyncListingStaff)
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		var getErr error
		full, getErr = store.Get(tx, full.ID)
		return getErr
	}); err != nil || full.StartedAt == nil {
		t.Fatalf("full run=%+v err=%v", full, err)
	}
	oldPage := []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "old-tag", Name: "Old", Type: 1}}}}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, full.ID, "wecom-corp:corp-1", customerID, oldPage, full.StartedAt.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	refreshAt := full.StartedAt.Add(time.Minute)
	reader := &externalContactReaderStub{contact: wecomport.ExternalContact{ExternalUserID: "external-1", FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "fresh-tag", Name: "Fresh", Type: 1}}}}}}
	service := CustomerTagObservationService{Enabled: true, CorpID: "corp-1", Provider: reader, Store: store, UOW: unit, Now: func() time.Time { return refreshAt }}
	if err = service.RefreshCustomerTagObservation(ctx, "eer_2", customerID, "staff-1", "external-1"); err != nil {
		t.Fatal(err)
	}
	// This is a delayed commit of a page read before the later single-contact
	// refresh. Its old Provider-read timestamp must not resurrect old-tag.
	if err = unit.Within(ctx, func(tx context.Context) error {
		if applyErr := store.UpsertProfileObservations(tx, full.ID, "wecom-corp:corp-1", customerID, oldPage, full.StartedAt.Add(2*time.Second)); applyErr != nil {
			return applyErr
		}
		return store.ReconcileProfileObservations(tx, full.ID, refreshAt.Add(2*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	var activeID string
	var staleCount int
	if err = pool.Native().QueryRow(ctx, `SELECT provider_tag_id FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='active'`, customerID).Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='stale'`, customerID).Scan(&staleCount); err != nil {
		t.Fatal(err)
	}
	var listed []CustomerSyncRun
	if err = unit.Within(ctx, func(tx context.Context) error {
		var listErr error
		listed, listErr = store.List(tx, 10)
		return listErr
	}); err != nil {
		t.Fatal(err)
	}
	if activeID != "fresh-tag" || staleCount != 1 || len(listed) != 1 || listed[0].ID != full.ID {
		t.Fatalf("active=%q stale=%d listed=%+v", activeID, staleCount, listed)
	}
}

func TestCustomerTagRefreshUsesReadTimeForEmptyAndDelayedObservationsPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rawCustomerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&rawCustomerID); err != nil {
		t.Fatal(err)
	}
	store := PostgreSQLCustomerSyncStore{}
	customerID := customerdomain.CustomerID(rawCustomerID)
	earlierRead := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	laterRead := earlierRead.Add(time.Minute)
	fresh := []wecomport.ExternalContactTag{{ProviderTagID: "fresh-tag", Name: "Fresh", Type: 1}}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", customerID, "staff-1", fresh, laterRead, "tag-refresh-later-read")
	}); err != nil {
		t.Fatal(err)
	}
	// This models an earlier request whose Provider response was delayed until
	// after the later read was committed. Its persisted response must not win.
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", customerID, "staff-1", []wecomport.ExternalContactTag{{ProviderTagID: "old-tag", Name: "Old", Type: 1}}, earlierRead, "tag-refresh-earlier-read")
	}); err != nil {
		t.Fatal(err)
	}
	var active []string
	if err = pool.Native().QueryRow(ctx, `SELECT coalesce(array_agg(provider_tag_id ORDER BY provider_tag_id),ARRAY[]::text[]) FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='active'`, customerID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0] != "fresh-tag" {
		t.Fatalf("delayed old response replaced fresh observation: %v", active)
	}
	// A current empty Provider response is a complete observation, so it must
	// clear the prior active set rather than leave requested tags as observed.
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", customerID, "staff-1", nil, laterRead.Add(time.Minute), "tag-refresh-empty-current")
	}); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT coalesce(array_agg(provider_tag_id ORDER BY provider_tag_id),ARRAY[]::text[]) FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='active'`, customerID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("empty complete observation left active tags: %v", active)
	}
}

func TestCustomerTagObservationVersionSerializesInterleavedCompleteReadsPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := PostgreSQLCustomerSyncStore{}
	at := time.Date(2026, 9, 6, 11, 0, 0, 0, time.UTC)

	// An older full page holds the shared row as a newer refresh arrives.
	// The refresh blocks and then owns the final complete set.
	fullOlder := newObservationCustomer(t, ctx, pool.Native())
	fullRun := seedObservationRun(t, ctx, pool.Native(), "full-interleaved-old", "manual", "wecom-corp:corp-1", "staff-1", at)
	oldTx, err := pool.Native().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the raw lock-owning transaction fail-safe: t.Fatal below must not
	// leave the concurrent UnitOfWork blocked while the pool is being closed.
	defer func() { _ = oldTx.Rollback(context.Background()) }()
	oldCtx := platformpostgres.BindTransaction(ctx, oldTx)
	if advanced, advanceErr := advanceCustomerTagObservation(oldCtx, oldTx, fullOlder, "wecom-corp:corp-1", "staff-1", fullRun, at.Add(time.Second)); advanceErr != nil || !advanced {
		t.Fatalf("full advance=%t err=%v", advanced, advanceErr)
	}
	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- unit.Within(ctx, func(tx context.Context) error {
			return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", fullOlder, "staff-1", []wecomport.ExternalContactTag{{ProviderTagID: "new-refresh", Name: "New", Type: 1}}, at.Add(2*time.Second), "refresh-after-full-read")
		})
	}()
	assertBlocked(t, refreshDone)
	if err = replaceCustomerTagObservation(oldCtx, oldTx, fullOlder, "wecom-corp:corp-1", "staff-1", []wecomport.ExternalContactTag{{ProviderTagID: "old-full", Name: "Old", Type: 1}}, fullRun, at.Add(time.Second)); err != nil {
		_ = oldTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = oldTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-refreshDone; err != nil {
		t.Fatal(err)
	}
	assertActiveTags(t, ctx, pool.Native(), fullOlder, "new-refresh")

	// Reverse the interleave: an older refresh commits after a newer full page
	// starts waiting on that same row. The newer full set must win.
	refreshOlder := newObservationCustomer(t, ctx, pool.Native())
	oldRefreshRun := seedObservationRun(t, ctx, pool.Native(), "refresh-interleaved-old", "tag_refresh", "wecom-corp:corp-1", "staff-1", at)
	fullNewRun := seedObservationRun(t, ctx, pool.Native(), "full-after-refresh", "manual", "wecom-corp:corp-1", "staff-1", at)
	refreshTx, err := pool.Native().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Roll back on every failure path before the test pool is closed.
	defer func() { _ = refreshTx.Rollback(context.Background()) }()
	refreshCtx := platformpostgres.BindTransaction(ctx, refreshTx)
	if advanced, advanceErr := advanceCustomerTagObservation(refreshCtx, refreshTx, refreshOlder, "wecom-corp:corp-1", "staff-1", oldRefreshRun, at.Add(time.Second)); advanceErr != nil || !advanced {
		t.Fatalf("refresh advance=%t err=%v", advanced, advanceErr)
	}
	fullDone := make(chan error, 1)
	go func() {
		fullDone <- unit.Within(ctx, func(tx context.Context) error {
			return store.UpsertProfileObservations(tx, fullNewRun, "wecom-corp:corp-1", refreshOlder, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "new-full", Name: "New", Type: 1}}}}, at.Add(2*time.Second))
		})
	}()
	assertBlocked(t, fullDone)
	if err = replaceCustomerTagObservation(refreshCtx, refreshTx, refreshOlder, "wecom-corp:corp-1", "staff-1", []wecomport.ExternalContactTag{{ProviderTagID: "old-refresh", Name: "Old", Type: 1}}, oldRefreshRun, at.Add(time.Second)); err != nil {
		_ = refreshTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = refreshTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-fullDone; err != nil {
		t.Fatal(err)
	}
	assertActiveTags(t, ctx, pool.Native(), refreshOlder, "new-full")
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
		return store.UpsertProfileObservations(tx, fullRun, "wecom-corp:corp-1", customerID, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: nil}}, at.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	assertActiveTags(t, ctx, pool.Native(), customerID)
	newer := at.Add(2 * time.Minute)
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", customerID, "staff-1", []wecomport.ExternalContactTag{{ProviderTagID: "newtag", Name: "New", Type: 1}}, newer, "refresh-newtag")
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

func TestCustomerTagObservationReconcileWaitsForRefreshAndKeepsLaterCompleteReadPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := PostgreSQLCustomerSyncStore{}
	at := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	customerID := newObservationCustomer(t, ctx, pool.Native())
	priorRun := seedObservationRun(t, ctx, pool.Native(), "reconcile-prior", "manual", "wecom-corp:corp-1", "staff-1", at.Add(-time.Minute))
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, priorRun, "wecom-corp:corp-1", customerID, []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "shared", Name: "Old", Type: 1}}}}, at.Add(-time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	fullRun := seedObservationRun(t, ctx, pool.Native(), "reconcile-current", "manual", "wecom-corp:corp-1", "staff-1", at)
	refreshRun := seedObservationRun(t, ctx, pool.Native(), "reconcile-refresh", "tag_refresh", "wecom-corp:corp-1", "staff-1", at.Add(time.Minute))
	refreshTx, err := pool.Native().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Roll back on every failure path before the test pool is closed.
	defer func() { _ = refreshTx.Rollback(context.Background()) }()
	refreshCtx := platformpostgres.BindTransaction(ctx, refreshTx)
	if advanced, advanceErr := advanceCustomerTagObservation(refreshCtx, refreshTx, customerID, "wecom-corp:corp-1", "staff-1", refreshRun, at.Add(time.Minute)); advanceErr != nil || !advanced {
		t.Fatalf("refresh advance=%t err=%v", advanced, advanceErr)
	}
	if err = replaceCustomerTagObservation(refreshCtx, refreshTx, customerID, "wecom-corp:corp-1", "staff-1", []wecomport.ExternalContactTag{{ProviderTagID: "shared", Name: "Fresh", Type: 1}}, refreshRun, at.Add(time.Minute)); err != nil {
		_ = refreshTx.Rollback(ctx)
		t.Fatal(err)
	}
	reconcileDone := make(chan error, 1)
	go func() {
		reconcileDone <- unit.Within(ctx, func(tx context.Context) error {
			return store.ReconcileProfileObservations(tx, fullRun, at.Add(2*time.Minute))
		})
	}()
	assertBlocked(t, reconcileDone)
	if err = refreshTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-reconcileDone; err != nil {
		t.Fatal(err)
	}
	assertActiveTags(t, ctx, pool.Native(), customerID, "shared")
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
