package groupops

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	queue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"time"
)

const CatalogQueue = "group-catalog"

type CatalogArgs struct {
	RunID int64 `json:"run_id"`
}

func (CatalogArgs) Kind() string { return "groupops.catalog.v1" }

type CatalogEnqueuer struct {
	Client *river.Client[pgx.Tx]
	UoW    platformport.UnitOfWork
}

func (e CatalogEnqueuer) Enqueue(ctx context.Context, id int64) error {
	tx, err := postgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = queue.InsertTxWithOptions(ctx, e.Client, tx, CatalogArgs{id}, river.InsertOpts{Queue: CatalogQueue, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

type CatalogWorker struct {
	river.WorkerDefaults[CatalogArgs]
	Service *app.CatalogService
}

func (w *CatalogWorker) Work(ctx context.Context, j *river.Job[CatalogArgs]) error {
	err := w.Service.ProcessCatalog(ctx, j.Args.RunID)
	if err != nil && j.Attempt >= j.MaxAttempts {
		return errors.Join(err, w.Service.FailCatalog(ctx, j.Args.RunID))
	}
	return err
}
func (*CatalogWorker) Timeout(*river.Job[CatalogArgs]) time.Duration { return 45 * time.Minute }

type CatalogScheduleArgs struct{}

func (CatalogScheduleArgs) Kind() string { return "groupops.catalog-schedule.v1" }

type CatalogScheduleWorker struct {
	river.WorkerDefaults[CatalogScheduleArgs]
	Service *app.CatalogService
}

func (w *CatalogScheduleWorker) Work(ctx context.Context, _ *river.Job[CatalogScheduleArgs]) error {
	if w.Service == nil || !w.Service.Enabled {
		return nil
	}
	_, err := w.Service.RequestCatalogSync(ctx, false)
	return err
}
func CatalogPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return CatalogScheduleArgs{}, &river.InsertOpts{Queue: CatalogQueue, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Minute}}
	}, &river.PeriodicJobOpts{ID: "group-catalog-v1", RunOnStart: true})
}

// Detail repair uses River's existing retry policy and deduplication. It never
// starts another full scan and cannot generate a Provider write.
type CatalogDetailArgs struct {
	ChatID string `json:"chat_id"`
}

func (CatalogDetailArgs) Kind() string { return "groupops.catalog-detail.v1" }
func (e CatalogEnqueuer) EnqueueDetail(ctx context.Context, id string) error {
	return e.UoW.Within(ctx, func(ctx context.Context) error {
		tx, err := postgres.RequireTransaction(ctx)
		if err != nil {
			return err
		}
		_, err = queue.InsertTxWithOptions(ctx, e.Client, tx, CatalogDetailArgs{id}, river.InsertOpts{Queue: CatalogQueue, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true}})
		return err
	})
}

type CatalogDetailWorker struct {
	river.WorkerDefaults[CatalogDetailArgs]
	Service *app.CatalogService
}

func (w *CatalogDetailWorker) Work(ctx context.Context, j *river.Job[CatalogDetailArgs]) error {
	_, err := w.Service.RefreshCatalogGroup(ctx, j.Args.ChatID)
	return err
}
