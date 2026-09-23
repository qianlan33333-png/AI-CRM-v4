package segment

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	"github.com/riverqueue/river"
)

const AudienceRefreshQueue = "segment-audience-refresh"

type AudienceRefreshJobArgs struct {
	RefreshRunID int64 `json:"refresh_run_id"`
}

func (AudienceRefreshJobArgs) Kind() string { return "segment.audience-refresh.v1" }

type RiverRefreshEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverRefreshEnqueuer(client *river.Client[pgx.Tx]) (*RiverRefreshEnqueuer, error) {
	if client == nil {
		return nil, segmentapp.ErrNotReady
	}
	return &RiverRefreshEnqueuer{client}, nil
}
func (e *RiverRefreshEnqueuer) EnqueueRefreshWithin(ctx context.Context, runID int64) (int64, error) {
	if e == nil || e.client == nil || runID < 1 {
		return 0, segmentapp.ErrNotReady
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	result, err := platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, AudienceRefreshJobArgs{runID}, river.InsertOpts{Queue: AudienceRefreshQueue, MaxAttempts: 12})
	if err != nil {
		return 0, err
	}
	return result.Job.ID, nil
}

type AudienceRefreshApplication interface {
	ProcessRefresh(context.Context, int64) error
	FailRefresh(context.Context, int64, string) error
}
type AudienceRefreshWorker struct {
	river.WorkerDefaults[AudienceRefreshJobArgs]
	service AudienceRefreshApplication
}

func NewAudienceRefreshWorker() *AudienceRefreshWorker { return &AudienceRefreshWorker{} }
func (*AudienceRefreshWorker) Timeout(*river.Job[AudienceRefreshJobArgs]) time.Duration {
	return 45 * time.Minute
}
func (w *AudienceRefreshWorker) BindService(service AudienceRefreshApplication) error {
	if w == nil || w.service != nil || service == nil {
		return segmentapp.ErrNotReady
	}
	w.service = service
	return nil
}
func (w *AudienceRefreshWorker) Work(ctx context.Context, job *river.Job[AudienceRefreshJobArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || job.Args.RefreshRunID < 1 {
		return segmentapp.ErrNotReady
	}
	err := w.service.ProcessRefresh(ctx, job.Args.RefreshRunID)
	if err == nil {
		return nil
	}
	if job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts {
		failErr := w.service.FailRefresh(ctx, job.Args.RefreshRunID, segmentapp.RefreshErrorCode(err))
		return errors.Join(failErr)
	}
	return err
}
