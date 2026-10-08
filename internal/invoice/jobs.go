package invoice

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	jobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"time"
)

const QueryQueue = "invoice-reconciliation"

type QueryArgs struct {
	ApplicationID string `json:"application_id"`
}

func (QueryArgs) Kind() string { return "invoice.provider-query.v1" }

type QueryEnqueuer struct{ Client *river.Client[pgx.Tx] }

func (q QueryEnqueuer) EnqueueWithin(ctx context.Context, id string, at time.Time) error {
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	args := QueryArgs{ApplicationID: id}
	result, e := jobqueue.InsertTxWithOptions(ctx, q.Client, tx, args, river.InsertOpts{Queue: QueryQueue, MaxAttempts: 20, ScheduledAt: at, UniqueOpts: river.UniqueOpts{ByArgs: true, ByQueue: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable, rivertype.JobStateRunning, rivertype.JobStateScheduled}}})
	if e == nil && at.IsZero() && result.UniqueSkippedAsDuplicate {
		return jobqueue.WakePendingJobTx(ctx, q.Client, tx, result.Job.ID, args, QueryQueue)
	}
	return e
}

type QueryWorker struct {
	river.WorkerDefaults[QueryArgs]
	Service *Service
}

func (*QueryWorker) Timeout(*river.Job[QueryArgs]) time.Duration { return 45 * time.Second }
func (w *QueryWorker) Work(ctx context.Context, j *river.Job[QueryArgs]) error {
	if w.Service == nil || j == nil {
		return p.ErrUnavailable
	}
	e := w.Service.Reconcile(ctx, j.Args.ApplicationID)
	if errors.Is(e, p.ErrPending) {
		return p.ErrPending
	}
	return e
}
