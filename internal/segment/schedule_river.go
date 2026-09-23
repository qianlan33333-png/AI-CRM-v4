package segment

import (
	"context"
	"time"

	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	"github.com/riverqueue/river"
)

type AudienceScheduleScanJobArgs struct{}

func (AudienceScheduleScanJobArgs) Kind() string { return "segment.audience-schedule-scan.v1" }

type AudienceScheduleApplication interface{ ScanScheduled(context.Context) error }

type AudienceScheduleScanWorker struct {
	river.WorkerDefaults[AudienceScheduleScanJobArgs]
	service AudienceScheduleApplication
}

func NewAudienceScheduleScanWorker() *AudienceScheduleScanWorker {
	return &AudienceScheduleScanWorker{}
}
func (*AudienceScheduleScanWorker) Timeout(*river.Job[AudienceScheduleScanJobArgs]) time.Duration {
	return 10 * time.Minute
}
func (worker *AudienceScheduleScanWorker) BindService(service AudienceScheduleApplication) error {
	if worker == nil || worker.service != nil || service == nil {
		return segmentapp.ErrNotReady
	}
	worker.service = service
	return nil
}
func (worker *AudienceScheduleScanWorker) Work(ctx context.Context, job *river.Job[AudienceScheduleScanJobArgs]) error {
	if worker == nil || worker.service == nil || job == nil || job.JobRow == nil {
		return segmentapp.ErrNotReady
	}
	return worker.service.ScanScheduled(ctx)
}

func AudienceSchedulePeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return AudienceScheduleScanJobArgs{}, &river.InsertOpts{Queue: AudienceRefreshQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Minute}}
	}, &river.PeriodicJobOpts{ID: "segment-audience-schedule-scan-v1", RunOnStart: true})
}
