package worker

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/app"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

const Queue = "hxc-dashboard"

type Args struct {
	RunID int64 `json:"run_id"`
}

func (Args) Kind() string { return "hxc.dashboard-refresh.v1" }

type Enqueuer struct{ client *river.Client[pgx.Tx] }

func NewEnqueuer(client *river.Client[pgx.Tx]) (*Enqueuer, error) {
	if client == nil {
		return nil, app.ErrNotReady
	}
	return &Enqueuer{client: client}, nil
}
func (e *Enqueuer) Enqueue(ctx context.Context, id int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = e.client.InsertTx(ctx, tx, Args{RunID: id}, &river.InsertOpts{Queue: Queue, MaxAttempts: 8})
	return err
}

type Worker struct {
	river.WorkerDefaults[Args]
	Service *app.Service
}

func (*Worker) Timeout(*river.Job[Args]) time.Duration { return 30 * time.Minute }
func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	if w == nil || w.Service == nil || job == nil {
		return app.ErrNotReady
	}
	return w.Service.Refresh(ctx, job.Args.RunID)
}
