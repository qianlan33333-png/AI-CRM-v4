package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type scheduleRuntimeSource struct{}

func (scheduleRuntimeSource) Evaluate(_ context.Context, _ segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	return segmentport.Evaluation{ReferenceAt: reference.UTC()}, nil
}

type scheduleRuntimeCanonical struct{}

func (scheduleRuntimeCanonical) CanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return ids, nil
}

type scheduleRuntimeEnqueuer struct {
	mu    sync.Mutex
	calls int
}

func (e *scheduleRuntimeEnqueuer) EnqueueRefreshWithin(context.Context, int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return 9001, nil
}

type scheduleRuntimeMemberEvents struct{}

func (scheduleRuntimeMemberEvents) EnqueueMemberEventsWithin(context.Context, segmentport.SnapshotID) (int64, error) {
	return 9002, nil
}

func TestPostgreSQLCombinedScheduleConcurrentScannersDeduplicateRefresh(t *testing.T) {
	ctx := context.Background()
	native, cleanup := scheduleRuntimeDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 18, 1, 0, 0, time.UTC) // Shanghai 02:01.
	created := now.Add(-2 * time.Minute)
	var configuration segmentdomain.ConfigurationVersion
	err = uow.Within(ctx, func(tx context.Context) error {
		group, e := segmentdomain.NewGroup("schedule runtime", 1, 7, created)
		if e != nil {
			return e
		}
		group, e = repository.CreateGroup(tx, group)
		if e != nil {
			return e
		}
		pkg, e := segmentdomain.NewPackage("schedule-runtime", "schedule runtime", &group.ID, 7, created)
		if e != nil {
			return e
		}
		pkg, e = repository.CreatePackage(tx, pkg)
		if e != nil {
			return e
		}
		configuration, e = segmentdomain.NewConfigurationVersion(pkg.ID, 1, []byte(`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"}}`), "", "every_3m_plus_daily_0200", 7, created)
		if e != nil {
			return e
		}
		configuration, e = repository.CreateConfigurationVersion(tx, configuration)
		if e != nil {
			return e
		}
		pkg, e = repository.SetCurrentConfiguration(tx, pkg.ID, configuration.ID, pkg.Version, 7, created)
		if e != nil {
			return e
		}
		locked, e := repository.LockPackage(tx, pkg.ID)
		if e != nil {
			return e
		}
		if e = locked.Transition(segmentdomain.Active, pkg.Version, 7, created); e != nil {
			return e
		}
		_, e = repository.UpdatePackage(tx, locked, pkg.Version)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(segmentcompiler.Compiler{}, scheduleRuntimeSource{}, scheduleRuntimeCanonical{})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer := &scheduleRuntimeEnqueuer{}
	refresh, err := NewSnapshotService(uow, repository, evaluator, enqueuer, scheduleRuntimeMemberEvents{})
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduledRefreshService(uow, repository, refresh)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.now = func() time.Time { return now }

	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errs <- scheduler.ScanScheduled(ctx)
		}()
	}
	close(start)
	workers.Wait()
	close(errs)
	for scanErr := range errs {
		if scanErr != nil {
			t.Fatal(scanErr)
		}
	}

	var states, runs int64
	if err = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_schedule_states WHERE configuration_version_id=$1`, configuration.ID).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_refresh_runs WHERE configuration_version_id=$1`, configuration.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if states != 2 || runs != 1 {
		t.Fatalf("schedule states=%d refresh runs=%d, want two independent cursors and one accepted occurrence", states, runs)
	}
	enqueuer.mu.Lock()
	calls := enqueuer.calls
	enqueuer.mu.Unlock()
	if calls != 1 {
		t.Fatalf("refresh enqueue calls=%d, want one shared occurrence", calls)
	}
	var refreshKind string
	if err = native.QueryRow(ctx, `SELECT refresh_kind FROM segment_audience_refresh_runs WHERE package_id=$1`, configuration.PackageID).Scan(&refreshKind); err != nil || refreshKind != "daily" {
		t.Fatalf("shared run kind=%q err=%v", refreshKind, err)
	}
	var incremental, daily time.Time
	rows, err := native.Query(ctx, `SELECT schedule_kind,last_dispatched_at FROM segment_audience_schedule_states WHERE configuration_version_id=$1 ORDER BY schedule_kind`, configuration.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var occurrence time.Time
		if err = rows.Scan(&kind, &occurrence); err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "incremental":
			incremental = occurrence
		case "daily":
			daily = occurrence
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantOccurrence := time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)
	if !incremental.Equal(wantOccurrence) || !daily.Equal(wantOccurrence) {
		t.Fatalf("occurrences incremental=%s daily=%s want=%s", incremental, daily, wantOccurrence)
	}
}

func scheduleRuntimeDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	return scheduleRuntimeDatabaseWithMutationActor(t, ctx, true)
}

func scheduleRuntimeDatabaseWithMutationActor(t *testing.T, ctx context.Context, includeMutationActor bool) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping scheduled refresh PostgreSQL integration test")
	}
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	var random [6]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "segment_schedule_runtime_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range segmentRuntimeMigrationNames(includeMutationActor) {
		applySegmentRuntimeMigration(t, native, name)
	}
	return native, func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}
}

func segmentRuntimeMigrationNames(includeMutationActor bool) []string {
	names := []string{
		"0039_segment_audience_configuration.sql", "0040_segment_audience_snapshots.sql", "0041_segment_audience_webhooks.sql",
		"0042_segment_audience_execution_bindings.sql", "0045_segment_audience_member_events.sql", "0048_segment_audience_schedule_state.sql",
		"0053_segment_audience_member_event_fact_kinds.sql", "0083_segment_audience_refresh_modes.sql", "0085_segment_audience_refresh_kind.sql",
	}
	if includeMutationActor {
		names = append(names, "0097_segment_audience_mutation_actor.sql")
	}
	return names
}

func applySegmentRuntimeMigration(t *testing.T, native *pgxpool.Pool, name string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate schedule runtime integration test")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(context.Background(), string(migration)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}
