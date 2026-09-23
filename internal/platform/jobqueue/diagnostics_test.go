package jobqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type diagnosticIdleArgs struct{}

func (diagnosticIdleArgs) Kind() string { return "diagnostic.idle.fixture" }

type diagnosticIdleWorker struct {
	river.WorkerDefaults[diagnosticIdleArgs]
}

func (*diagnosticIdleWorker) Work(context.Context, *river.Job[diagnosticIdleArgs]) error { return nil }

func diagnosticWorkers() *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &diagnosticIdleWorker{})
	return workers
}

func TestRuntimeQueueReportCadenceMatchesDiagnosticContract(t *testing.T) {
	// Construction does not connect. Read the effective producer config created
	// by the real pinned River client, rather than copying its default into a
	// mock or waiting ten minutes. This test-only reflection deliberately fails
	// on upstream layout/cadence changes: review the contract on River upgrades.
	pool, err := pgxpool.New(context.Background(), "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	constructors := map[string]func() (*Runtime, error){
		"default": func() (*Runtime, error) { return NewRuntime(pool, diagnosticWorkers()) },
		"periodic": func() (*Runtime, error) {
			return NewRuntimeWithPeriodic(pool, diagnosticWorkers(), nil, OutboundQueue, OpsInspectionQueue)
		},
		"diagnostics": func() (*Runtime, error) {
			return NewRuntimeWithDiagnostics(pool, diagnosticWorkers(), nil, "fixture", nil, OutboundQueue, OpsRetentionQueue)
		},
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			runtime, err := construct()
			if err != nil {
				t.Fatal(err)
			}
			producers := reflect.ValueOf(runtime.client).Elem().FieldByName("producersByQueueName")
			if !producers.IsValid() || producers.Kind() != reflect.Map || producers.Len() == 0 {
				t.Fatal("River cadence contract changed: cannot inspect actual producers; review native queue reporting before upgrading")
			}
			for _, queue := range producers.MapKeys() {
				producer := producers.MapIndex(queue)
				if producer.Kind() != reflect.Pointer || producer.IsNil() || producer.Elem().Kind() != reflect.Struct {
					t.Fatal("River cadence contract changed: producer is not the expected pointer")
				}
				config := producer.Elem().FieldByName("config")
				if !config.IsValid() || config.Kind() != reflect.Pointer || config.IsNil() || config.Elem().Kind() != reflect.Struct {
					t.Fatal("River cadence contract changed: producer config is unavailable")
				}
				interval := config.Elem().FieldByName("QueueReportInterval")
				if !interval.IsValid() || interval.Kind() != reflect.Int64 {
					t.Fatal("River cadence contract changed: native QueueReportInterval is unavailable")
				}
				if actual := time.Duration(interval.Int()); actual != riverQueueReportInterval {
					t.Fatalf("River cadence contract changed for %s: runtime=%s diagnostics=%s; review observation window before upgrading", queue.String(), actual, riverQueueReportInterval)
				}
			}
		})
	}
}

func TestPostgreSQLWorkerDiagnosticCadenceBoundaries(t *testing.T) {
	pool := diagnosticTestPool(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 18, 12, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	// Freeze the externally observed twelve-minute boundary independently of
	// the implementation's constants, including the first stale microsecond.
	window := 12 * time.Minute
	for _, tc := range []struct {
		name  string
		age   time.Duration
		stale int64
	}{
		{"previous_false_positive", 3 * time.Minute, 0},
		{"before_native_report", riverQueueReportInterval - time.Microsecond, 0},
		{"native_report_due", riverQueueReportInterval, 0},
		{"report_sql_and_jitter", riverQueueReportInterval + 11*time.Second, 0},
		{"grace_boundary", window, 0},
		{"missed_report", window + time.Microsecond, 1},
		{"stopped_worker", 2 * window, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `INSERT INTO river_queue(name,updated_at) VALUES('idle',$1) ON CONFLICT(name) DO UPDATE SET updated_at=excluded.updated_at`, at.Add(-tc.age)); err != nil {
				t.Fatal(err)
			}
			got, err := WorkerDiagnosticCounts(ctx, pool, at, "idle")
			want := map[string]int64{"expected_queues": 1, "fresh_queue_observations": 1 - tc.stale, "stale_queue_observations": tc.stale, "missing_queue_observations": 0, "paused_queues": 0}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("age=%s got=%v want=%v err=%v", tc.age, got, want, err)
			}
		})
	}
	if _, err := pool.Exec(ctx, `INSERT INTO river_queue(name,updated_at,paused_at) VALUES('live',$1,NULL),('paused_live',$1,$1),('paused_stale',$2,$2),('unconfigured',$1,NULL)`, at.Add(-3*time.Minute), at.Add(-window-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	// Completed business work is independent of queue liveness, including the
	// observed production case of 160 completions alongside stale reports.
	if _, err := pool.Exec(ctx, `INSERT INTO river_job(kind,queue,args,state,scheduled_at,finalized_at,max_attempts) SELECT 'diagnostic.idle.fixture','idle','{}','completed',$1,$1,5 FROM generate_series(1,160)`, at); err != nil {
		t.Fatal(err)
	}
	got, err := WorkerDiagnosticCounts(ctx, pool, at, "idle", "live", "paused_live", "paused_stale", "missing")
	want := map[string]int64{"expected_queues": 5, "fresh_queue_observations": 2, "stale_queue_observations": 2, "missing_queue_observations": 1, "paused_queues": 2}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("independent observations=%v want=%v err=%v", got, want, err)
	}
	var updated time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM river_queue WHERE name='idle'`).Scan(&updated); err != nil || !updated.Equal(at.Add(-2*window)) {
		t.Fatalf("diagnostic changed queue evidence: updated=%v err=%v", updated, err)
	}
	jobs, err := DiagnosticCounts(ctx, pool, at)
	if err != nil || jobs["completed_last_hour"] != 160 {
		t.Fatalf("completion evidence changed: %v err=%v", jobs, err)
	}
}

func TestPostgreSQLIdleRuntimeReportsWithoutCompletedJobs(t *testing.T) {
	pool := diagnosticTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runtime, err := NewRuntime(pool, diagnosticWorkers(), "idle_runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := runtime.client.Stop(stopCtx); err != nil {
			t.Errorf("stop idle River runtime: %v", err)
		}
	})
	var updated time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM river_queue WHERE name='idle_runtime'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	got, err := WorkerDiagnosticCounts(ctx, pool, updated.Add(riverQueueReportInterval), "idle_runtime")
	if err != nil || got["fresh_queue_observations"] != 1 || got["stale_queue_observations"] != 0 {
		t.Fatalf("native idle startup report not valid through next report: %v err=%v", got, err)
	}
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("idle heartbeat depends on jobs: jobs=%d err=%v", jobs, err)
	}
}

func diagnosticTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "worker_diagnostics_test_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("cleanup diagnostic test schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.RuntimeParams["application_name"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version string
	if err := pool.QueryRow(ctx, `SHOW server_version_num`).Scan(&version); err != nil || !strings.HasPrefix(version, "16") {
		t.Fatalf("PostgreSQL 16 required: version=%q err=%v", version, err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	return pool
}
