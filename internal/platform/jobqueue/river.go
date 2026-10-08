// Package jobqueue owns the PostgreSQL-backed River runtime boundary.
package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

const (
	OutboundQueue        = "outbound"
	OutboundWelcomeQueue = "outbound_welcome"
	OutboundExcelQueue   = "outbound_excel"
	OutboundMediaQueue   = "outbound_media"
	OpsNotificationQueue = "ops_notification"
	OpsInspectionQueue   = "adminops_inspections"
	OpsRetentionQueue    = "adminops_retention"
)

var queueWorkers = map[string]int{
	OutboundQueue: 4, OutboundWelcomeQueue: 4,
	OutboundExcelQueue: 3, OutboundMediaQueue: 2,
	OpsNotificationQueue: 1, OpsInspectionQueue: 1, OpsRetentionQueue: 1,
}

var ErrUnavailable = errors.New("River client unavailable")

// CheckReady verifies the durable River schema without importing River's
// migrator into application runtime code. The effects module consumes this
// platform-level readiness boundary alongside its own schema requirement.
func CheckReady(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return ErrUnavailable
	}
	var complete bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (
        SELECT 1 FROM unnest(ARRAY['river_job','river_leader','river_migration']) AS required(name)
        WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
    )`).Scan(&complete)
	if err != nil {
		return err
	}
	if !complete {
		return errors.New("River schema is not ready")
	}
	return nil
}

// NewInsertClient is deliberately insert-only and permits only registered
// worker kinds at runtime. Queue insertion may be called with an active pgx
// transaction so the caller's business fact and River row commit atomically.
func NewInsertClient(pool *pgxpool.Pool, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	if pool == nil {
		return nil, ErrUnavailable
	}
	if workers == nil {
		return nil, ErrUnavailable
	}
	return river.NewClient(riverpgxv5.New(pool), &river.Config{Workers: workers, Middleware: []rivertype.Middleware{&CorrelationMiddleware{Release: "unknown"}}})
}

func InsertTx(ctx context.Context, client *river.Client[pgx.Tx], tx pgx.Tx, args river.JobArgs) (*rivertype.JobInsertResult, error) {
	return InsertTxAt(ctx, client, tx, args, time.Time{})
}

// InsertTxAt is the shared durable-job insertion seam. A non-zero scheduled
// time is guaranteed not to run before that instant; zero preserves the
// immediate queue behavior used by existing effects.
func InsertTxAt(ctx context.Context, client *river.Client[pgx.Tx], tx pgx.Tx, args river.JobArgs, scheduledAt time.Time) (*rivertype.JobInsertResult, error) {
	if client == nil || tx == nil || args == nil {
		return nil, ErrUnavailable
	}
	return InsertTxWithOptions(ctx, client, tx, args, river.InsertOpts{Queue: OutboundQueue, ScheduledAt: scheduledAt.UTC()})
}

// InsertTxWithOptions is the stable internal-task seam for domain-owned River
// workers. Callers select only a registered queue; the PostgreSQL transaction
// still owns atomic acceptance with their business fact.
func InsertTxWithOptions(ctx context.Context, client *river.Client[pgx.Tx], tx pgx.Tx, args river.JobArgs, opts river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if client == nil || tx == nil || args == nil || opts.Queue == "" {
		return nil, ErrUnavailable
	}
	if opts.MaxAttempts < 0 {
		return nil, ErrUnavailable
	}
	if effect, ok := args.(interface{ DiagnosticEffectRef() string }); ok {
		correlation := diagnostics.FromContext(ctx)
		var err error
		if correlation.RequestID == "" {
			correlation, err = newCorrelation("unknown")
			if err != nil {
				return nil, err
			}
		}
		correlation.EffectRef = effect.DiagnosticEffectRef()
		ctx, err = diagnostics.WithCorrelation(ctx, correlation)
		if err != nil {
			return nil, err
		}
	}
	return client.InsertTx(ctx, tx, args, &opts)
}

type Runtime struct{ client *river.Client[pgx.Tx] }

func NewRuntime(pool *pgxpool.Pool, workers *river.Workers, queueNames ...string) (*Runtime, error) {
	return NewRuntimeWithPeriodic(pool, workers, nil, queueNames...)
}

func NewRuntimeWithPeriodic(pool *pgxpool.Pool, workers *river.Workers, periodicJobs []*river.PeriodicJob, queueNames ...string) (*Runtime, error) {
	return NewRuntimeWithDiagnostics(pool, workers, periodicJobs, "unknown", nil, queueNames...)
}

func NewRuntimeWithDiagnostics(pool *pgxpool.Pool, workers *river.Workers, periodicJobs []*river.PeriodicJob, release string, record diagnostics.Recorder, queueNames ...string) (*Runtime, error) {
	if pool == nil || workers == nil {
		return nil, ErrUnavailable
	}
	if len(queueNames) == 0 {
		queueNames = []string{OutboundQueue}
	}
	queues := make(map[string]river.QueueConfig, len(queueNames))
	for _, name := range queueNames {
		if name == "" {
			return nil, ErrUnavailable
		}
		maxWorkers := 4
		if configured, ok := queueWorkers[name]; ok {
			maxWorkers = configured
		}
		queues[name] = river.QueueConfig{MaxWorkers: maxWorkers}
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Queues: queues, Workers: workers, PeriodicJobs: periodicJobs, Middleware: []rivertype.Middleware{&CorrelationMiddleware{Release: release, Record: record}},
		CompletedJobRetentionPeriod: 30 * 24 * time.Hour,
		CancelledJobRetentionPeriod: 30 * 24 * time.Hour,
		DiscardedJobRetentionPeriod: 30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{client: client}, nil
}
func (r *Runtime) Run(ctx context.Context) error {
	if r == nil || r.client == nil {
		return ErrUnavailable
	}
	if err := r.client.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return r.client.Stop(shutdown)
	case <-r.client.Stopped():
		return errors.New("River stopped unexpectedly")
	}
}

// HasNonDiscardedJobTx also recognizes jobs written before unique insertion
// was enabled. Callers serialize the owning business target in this transaction;
// River's unique insert protects concurrent new producers. A cancelled task is
// deliberately retained rather than undoing an operator's cancellation.
func HasNonDiscardedJobTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, queue string) (bool, error) {
	if tx == nil || args == nil || queue == "" {
		return false, ErrUnavailable
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return false, err
	}
	var found bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM river_job WHERE kind=$1 AND queue=$2 AND args=$3::jsonb AND state<>'discarded')`, args.Kind(), queue, string(raw)).Scan(&found)
	return found, err
}

// RetryRescuedDiscardedJobTx wakes the exact original job after native River
// rescue exhausted its attempts. Lock before using JobRetryTx: that API also
// retries completed/cancelled jobs, which must not be revived by maintenance.
func RetryRescuedDiscardedJobTx(ctx context.Context, client *river.Client[pgx.Tx], tx pgx.Tx, id int64, args river.JobArgs, queue string) (bool, error) {
	if client == nil || tx == nil || id < 1 || args == nil || queue == "" {
		return false, ErrUnavailable
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return false, err
	}
	var lockedID int64
	err = tx.QueryRow(ctx, `SELECT id FROM river_job WHERE id=$1 AND kind=$2 AND queue=$3 AND args=$4::jsonb
        AND state='discarded'
        FOR UPDATE`, id, args.Kind(), queue, string(raw)).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	job, err := client.JobGetTx(ctx, tx, lockedID)
	if err != nil {
		return false, err
	}
	var metadata struct {
		CancelAttemptedAt time.Time `json:"cancel_attempted_at"`
	}
	if json.Unmarshal(job.Metadata, &metadata) != nil || !metadata.CancelAttemptedAt.IsZero() || len(job.Errors) == 0 {
		return false, nil
	}
	last := job.Errors[len(job.Errors)-1]
	if last.Error != "Stuck job rescued by JobRescuer" || last.Attempt != job.Attempt {
		return false, nil
	}
	job, err = client.JobRetryTx(ctx, tx, lockedID)
	if err != nil {
		return false, err
	}
	if job.ID != id || job.State != rivertype.JobStateAvailable {
		return false, ErrUnavailable
	}
	return true, nil
}

// WakePendingJobTx advances a scheduled/retryable task when new authenticated
// evidence arrives. It keeps the same River job; running and cancelled tasks
// are left alone. Callers supply its exact registered kind, queue and args.
func WakePendingJobTx(ctx context.Context, client *river.Client[pgx.Tx], tx pgx.Tx, id int64, args river.JobArgs, queue string) error {
	if client == nil || tx == nil || id < 1 || args == nil || queue == "" {
		return ErrUnavailable
	}
	raw, e := json.Marshal(args)
	if e != nil {
		return e
	}
	var locked int64
	e = tx.QueryRow(ctx, `SELECT id FROM river_job WHERE id=$1 AND kind=$2 AND queue=$3 AND args=$4::jsonb AND state IN ('scheduled','retryable') FOR UPDATE SKIP LOCKED`, id, args.Kind(), queue, string(raw)).Scan(&locked)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = client.JobRetryTx(ctx, tx, locked)
	return e
}
