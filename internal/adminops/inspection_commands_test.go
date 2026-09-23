package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func inspectionCommandQueue(t *testing.T, pool *pgxpool.Pool, uow platformport.UnitOfWork, now func() time.Time) opsport.ManualInspectionEnqueuer {
	t.Helper()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, NewInspectionWorker())
	client, err := jobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer, err := NewInspectionManualEnqueuer(uow, client, now)
	if err != nil {
		t.Fatal(err)
	}
	return enqueuer
}
func acceptedInspectionArgs(t *testing.T, pool *pgxpool.Pool, jobID int64) InspectionJobArgs {
	t.Helper()
	var encoded []byte
	if err := pool.QueryRow(context.Background(), `SELECT args FROM river_job WHERE id=$1 AND kind='adminops.inspection.v1' AND queue='adminops_inspections'`, jobID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var args InspectionJobArgs
	if err := json.Unmarshal(encoded, &args); err != nil {
		t.Fatal(err)
	}
	return args
}

type inspectionFaultUoW struct {
	base platformport.UnitOfWork
	fail *atomic.Bool
}

func (s inspectionFaultUoW) Within(ctx context.Context, fn func(context.Context) error) error {
	return s.base.Within(ctx, func(txctx context.Context) error {
		if err := fn(txctx); err != nil {
			return err
		}
		if s.fail.Swap(false) {
			return errors.New("fixture transaction rejected")
		}
		return nil
	})
}
func TestPostgreSQLInspectionManualAcceptAtomicConcurrentAndRetryAfterPruning(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var fail atomic.Bool
	fault := inspectionFaultUoW{base: uow, fail: &fail}
	enqueuer := inspectionCommandQueue(t, pool, fault, func() time.Time { return now })
	fail.Store(true)
	if _, err := enqueuer.EnqueueManualInspection(ctx, 7, "atomic-rollback-command"); err == nil {
		t.Fatal("forced commit failure accepted")
	}
	var jobs, receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM adminops_inspection_commands`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || receipts != 0 {
		t.Fatalf("split acceptance: jobs=%d receipts=%d", jobs, receipts)
	}
	var group sync.WaitGroup
	results := make(chan opsport.ManualInspectionAcceptance, 6)
	errs := make(chan error, 6)
	for range 6 {
		group.Add(1)
		go func() {
			defer group.Done()
			accepted, err := enqueuer.EnqueueManualInspection(ctx, 7, "same-durable-command")
			results <- accepted
			errs <- err
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var accepted opsport.ManualInspectionAcceptance
	fresh := 0
	for result := range results {
		if accepted.JobID != 0 && accepted.JobID != result.JobID {
			t.Fatal("duplicate River jobs")
		}
		accepted = result
		if !result.Replay {
			fresh++
		}
	}
	if accepted.State != "accepted" || fresh != 1 {
		t.Fatalf("acceptance=%+v fresh=%d", accepted, fresh)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM adminops_inspection_runs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("HTTP acceptance scanned: %d %v", jobs, err)
	}
	args := acceptedInspectionArgs(t, pool, accepted.JobID)
	if !args.Slot.IsZero() || !manualInspectionDigestPattern.MatchString(args.ManualRequestDigest) {
		t.Fatalf("unsafe arguments=%+v", args)
	}
	var calls atomic.Int64
	collector := opsport.CollectorFunc{ID: "identity.conflicts", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
		calls.Add(1)
		return opsport.CheckObservation{Status: "ok", Code: "observed", ObservedAt: now}, nil
	}}
	service, err := NewInspectionService(pool, fault, []opsport.InspectionCollector{collector}, nil, InspectionOptions{ReleaseSHA: "test", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	worker := NewInspectionWorker()
	if err = worker.BindService(service); err != nil {
		t.Fatal(err)
	}
	job := &river.Job[InspectionJobArgs]{Args: args}
	fail.Store(true)
	if err = worker.Work(ctx, job); err == nil {
		t.Fatal("first worker failure swallowed")
	}
	var completed bool
	if err = pool.QueryRow(ctx, `SELECT completed_at IS NOT NULL FROM adminops_inspection_commands`).Scan(&completed); err != nil || completed {
		t.Fatalf("failed completion receipt=%v %v", completed, err)
	}
	if err = worker.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("retry calls=%d", calls.Load())
	}
	if err = pool.QueryRow(ctx, `SELECT completed_at IS NOT NULL FROM adminops_inspection_commands`).Scan(&completed); err != nil || !completed {
		t.Fatalf("completion receipt=%v %v", completed, err)
	}
	if err = worker.Work(ctx, job); err != nil || calls.Load() != 2 {
		t.Fatalf("completed job replay=%v calls=%d", err, calls.Load())
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM adminops_inspection_runs`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("retry duplicated scan=%d %v", jobs, err)
	}
	// Simulate 30-day process cleanup. Permanent acceptance still points to
	// the original job/run IDs without retaining or recreating their rows.
	if _, err = pool.Exec(ctx, `DELETE FROM adminops_inspection_results;DELETE FROM adminops_inspection_runs;DELETE FROM river_job`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(721 * time.Hour)
	replay, err := enqueuer.EnqueueManualInspection(ctx, 7, "same-durable-command")
	if err != nil || !replay.Replay || replay.JobID != accepted.JobID {
		t.Fatalf("old command replay=%+v %v", replay, err)
	}
	if err = worker.Work(ctx, job); err != nil || calls.Load() != 2 {
		t.Fatalf("pruned command replay=%v calls=%d", err, calls.Load())
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("recreated pruned job=%d %v", jobs, err)
	}
	if _, err = enqueuer.EnqueueManualInspection(ctx, 8, "same-durable-command"); !errors.Is(err, ErrInspectionConflict) {
		t.Fatalf("actor changed=%v", err)
	}
	for _, statement := range []string{`UPDATE adminops_inspection_commands SET job_id=job_id+1`, `UPDATE adminops_inspection_commands SET completed_at=NULL,run_id=NULL`, `DELETE FROM adminops_inspection_commands`, `TRUNCATE adminops_inspection_commands`} {
		if _, err = pool.Exec(ctx, statement); err == nil {
			t.Fatalf("permanent evidence mutated: %s", statement)
		}
	}
}
func TestPostgreSQLInspectionManualHTTPAcceptedAndHourlyLimits(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	now := time.Now().UTC()
	enqueuer := inspectionCommandQueue(t, pool, uow, func() time.Time { return now })
	service, err := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewInspectionHandler(service, inspectionTestSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	send := func(key string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/admin/ops-inspections/runs", strings.NewReader(`{}`))
		r.Header.Set("Idempotency-Key", key)
		h.ServeHTTP(w, r)
		return w
	}
	if w := send("unconfigured-command"); w.Code != 503 {
		t.Fatalf("unconfigured synchronous fallback=%d", w.Code)
	}
	if err = h.BindManualEnqueuer(enqueuer); err != nil {
		t.Fatal(err)
	}
	if w := send("short"); w.Code != 400 {
		t.Fatalf("bad key=%d", w.Code)
	}
	for i := range 6 {
		w := send(fmt.Sprintf("hourly-command-%d", i))
		if w.Code != 202 || !strings.Contains(w.Body.String(), `"state":"accepted"`) || strings.Contains(w.Body.String(), `"results"`) {
			t.Fatalf("accept=%d %s", w.Code, w.Body.String())
		}
	}
	if w := send("actor-over-budget"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("actor rate=%d %s", w.Code, w.Body.String())
	}
	if w := send("hourly-command-0"); w.Code != 202 || !strings.Contains(w.Body.String(), `"replay":true`) {
		t.Fatalf("replay charged budget=%d %s", w.Code, w.Body.String())
	}
	for i := range 6 {
		if _, err = enqueuer.EnqueueManualInspection(context.Background(), 8, fmt.Sprintf("other-hourly-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = enqueuer.EnqueueManualInspection(context.Background(), 9, "global-over-budget"); !errors.Is(err, ErrInspectionRateLimited) {
		t.Fatalf("global rate=%v", err)
	}
	now = now.Add(time.Hour)
	if w := send("next-hour-command"); w.Code != 202 {
		t.Fatalf("next hour=%d %s", w.Code, w.Body.String())
	}
}
