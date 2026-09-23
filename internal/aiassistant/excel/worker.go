package excel

import (
	"context"
	platform "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"time"
)

type RefreshArgs struct{}

func (RefreshArgs) Kind() string { return "ai_excel_observations_v1" }

type Worker struct {
	river.WorkerDefaults[RefreshArgs]
	Bridge *Bridge
}

func (w *Worker) Work(ctx context.Context, _ *river.Job[RefreshArgs]) error {
	if w.Bridge == nil {
		return nil
	}
	return w.Bridge.Refresh(ctx)
}
func (w *Worker) Timeout(*river.Job[RefreshArgs]) time.Duration { return 30 * time.Minute }
func Periodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return RefreshArgs{}, &river.InsertOpts{Queue: platform.OutboundQueue, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}
	}, &river.PeriodicJobOpts{RunOnStart: true})
}
