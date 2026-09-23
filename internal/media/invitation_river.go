package media

import (
	"context"
	a "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	q "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
	"time"
)

type InvitationRefreshArgs struct{}

func (InvitationRefreshArgs) Kind() string { return "media.invitation-refresh.v1" }

type InvitationRefreshWorker struct {
	river.WorkerDefaults[InvitationRefreshArgs]
	Service *a.InvitationService
}

func (w *InvitationRefreshWorker) Work(ctx context.Context, _ *river.Job[InvitationRefreshArgs]) error {
	if w.Service == nil {
		return nil
	}
	return w.Service.Refresh(ctx)
}
func InvitationPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return InvitationRefreshArgs{}, &river.InsertOpts{Queue: q.OutboundMediaQueue, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Minute}}
	}, &river.PeriodicJobOpts{ID: "media-invitation-refresh-v1", RunOnStart: true})
}
