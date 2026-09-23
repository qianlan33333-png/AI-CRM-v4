package automation

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

type DirectPushReconcileArgs struct{}

func (DirectPushReconcileArgs) Kind() string { return "automation.audience-direct-push-reconcile.v1" }

type DirectPushObserveArgs struct {
	ItemID int64 `json:"item_id" river:"unique"`
}

func (DirectPushObserveArgs) Kind() string { return "automation.audience-direct-push-observe.v1" }

type DirectPushRuntimeApplication interface {
	ReconcileDirectPushes(context.Context) error
	ObserveDirectPush(context.Context, int64) error
}

type DirectPushReconcileWorker struct {
	river.WorkerDefaults[DirectPushReconcileArgs]
	service DirectPushRuntimeApplication
}

func NewDirectPushReconcileWorker() *DirectPushReconcileWorker { return &DirectPushReconcileWorker{} }
func (w *DirectPushReconcileWorker) Bind(service DirectPushRuntimeApplication) error {
	if w == nil || w.service != nil || service == nil {
		return automationapp.ErrDirectPushInvalid
	}
	w.service = service
	return nil
}
func (w *DirectPushReconcileWorker) Work(ctx context.Context, job *river.Job[DirectPushReconcileArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil {
		return automationapp.ErrDirectPushUnavailable
	}
	return w.service.ReconcileDirectPushes(ctx)
}

type DirectPushObserveWorker struct {
	river.WorkerDefaults[DirectPushObserveArgs]
	service DirectPushRuntimeApplication
}

func NewDirectPushObserveWorker() *DirectPushObserveWorker { return &DirectPushObserveWorker{} }
func (w *DirectPushObserveWorker) Bind(service DirectPushRuntimeApplication) error {
	if w == nil || w.service != nil || service == nil {
		return automationapp.ErrDirectPushInvalid
	}
	w.service = service
	return nil
}
func (w *DirectPushObserveWorker) Work(ctx context.Context, job *river.Job[DirectPushObserveArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || job.Args.ItemID < 1 {
		return automationapp.ErrDirectPushUnavailable
	}
	err := w.service.ObserveDirectPush(ctx, job.Args.ItemID)
	if errors.Is(err, automationapp.ErrDirectPushObserveLater) {
		return river.JobSnooze(5 * time.Minute)
	}
	return err
}

type RiverDirectPushObservationScheduler struct{ client *river.Client[pgx.Tx] }

func NewRiverDirectPushObservationScheduler(client *river.Client[pgx.Tx]) (*RiverDirectPushObservationScheduler, error) {
	if client == nil {
		return nil, automationapp.ErrDirectPushUnavailable
	}
	return &RiverDirectPushObservationScheduler{client: client}, nil
}
func (s *RiverDirectPushObservationScheduler) ScheduleDirectPushObservationWithin(ctx context.Context, itemID int64, at time.Time) error {
	if s == nil || s.client == nil || itemID < 1 || at.IsZero() {
		return automationapp.ErrDirectPushInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, s.client, tx, DirectPushObserveArgs{ItemID: itemID}, river.InsertOpts{Queue: platformjobqueue.OutboundQueue, ScheduledAt: at.UTC(), MaxAttempts: 1000, UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

func DirectPushReconcilePeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return DirectPushReconcileArgs{}, &river.InsertOpts{Queue: platformjobqueue.OutboundQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Minute}}
	}, &river.PeriodicJobOpts{ID: "automation-audience-direct-push-reconcile-v1", RunOnStart: true})
}

var _ automationapp.DirectPushObservationScheduler = (*RiverDirectPushObservationScheduler)(nil)
