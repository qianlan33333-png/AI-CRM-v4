package wecom

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

const StaffDirectoryRefreshQueue = "wecom-staff-directory"

type StaffDirectoryRefreshJobArgs struct {
	RunKey  string `json:"run_key" river:"unique"`
	Trigger string `json:"trigger" river:"unique"`
}

func (StaffDirectoryRefreshJobArgs) Kind() string { return "wecom.staff-directory-refresh.v1" }

type StaffDirectoryRefreshWorker struct {
	river.WorkerDefaults[StaffDirectoryRefreshJobArgs]
	service *StaffDirectoryRefreshService
}

func NewStaffDirectoryRefreshWorker() *StaffDirectoryRefreshWorker {
	return &StaffDirectoryRefreshWorker{}
}

func (*StaffDirectoryRefreshWorker) Timeout(*river.Job[StaffDirectoryRefreshJobArgs]) time.Duration {
	return 5 * time.Minute
}

func (worker *StaffDirectoryRefreshWorker) BindService(service StaffDirectoryRefreshService) error {
	if worker == nil || worker.service != nil || !service.Ready() {
		return ErrStaffDirectoryRefreshNotReady
	}
	worker.service = &service
	return nil
}

func (worker *StaffDirectoryRefreshWorker) Work(ctx context.Context, job *river.Job[StaffDirectoryRefreshJobArgs]) error {
	if worker == nil || worker.service == nil || job == nil || job.JobRow == nil {
		return ErrStaffDirectoryRefreshNotReady
	}
	terminal := job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts
	return worker.service.Refresh(ctx, job.Args.RunKey, job.Args.Trigger, terminal)
}

func StaffDirectoryPeriodicJob(interval time.Duration, now func() time.Time) *river.PeriodicJob {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return river.NewPeriodicJob(river.PeriodicInterval(interval), func() (river.JobArgs, *river.InsertOpts) {
		bucket := now().UTC().Truncate(interval).Format("20060102T150405Z")
		return StaffDirectoryRefreshJobArgs{RunKey: "periodic-" + bucket, Trigger: "periodic"}, &river.InsertOpts{
			Queue: StaffDirectoryRefreshQueue, MaxAttempts: 12,
			UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: interval},
		}
	}, &river.PeriodicJobOpts{ID: "wecom-staff-directory-refresh-v1", RunOnStart: true})
}
