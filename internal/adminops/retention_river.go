package adminops

import (
	"context"
	"errors"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/riverqueue/river"
	"time"
)

type RetentionJobArgs struct {
	Hour time.Time `json:"hour"`
}

func (RetentionJobArgs) Kind() string { return "adminops.retention.v1" }

type RetentionWorker struct {
	river.WorkerDefaults[RetentionJobArgs]
	service *RetentionService
	host    platformport.HostMaintenance
}

func NewRetentionWorker() *RetentionWorker { return &RetentionWorker{} }
func (w *RetentionWorker) BindService(s *RetentionService) error {
	if s == nil || w.service != nil {
		return ErrInspectionInvalid
	}
	w.service = s
	return nil
}
func (*RetentionWorker) Timeout(*river.Job[RetentionJobArgs]) time.Duration { return 4 * time.Minute }
func (w *RetentionWorker) Work(ctx context.Context, j *river.Job[RetentionJobArgs]) error {
	if w.service == nil || j == nil {
		return ErrInspectionInvalid
	}
	// A persisted job may survive a deployment that disables maintenance.
	// Re-check the execution gate before both database and privileged host work.
	if !w.service.enabled {
		return nil
	}
	pending := false
	var failures []error
	for _, p := range w.service.Policies() {
		if !validRetentionPolicy(p.ID) {
			continue
		}
		if out, e := w.service.PruneRetentionBatch(ctx, p.ID); e != nil {
			failures = append(failures, e)
		} else if out.State == "running" {
			pending = true
		}
	}
	if w.host != nil {
		r, e := w.host.Run(ctx)
		if e != nil {
			failures = append(failures, errors.New("host_retention_failed"))
		} else if r.Runtime != nil && r.Runtime.Remaining {
			pending = true
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if pending {
		return river.JobSnooze(time.Second)
	}
	return nil
}
func RetentionPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
		return RetentionJobArgs{Hour: time.Now().UTC().Truncate(time.Hour)}, &river.InsertOpts{Queue: "adminops_retention", MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Hour}}
	}, &river.PeriodicJobOpts{ID: "adminops-retention-v1", RunOnStart: true})
}

func (w *RetentionWorker) BindHostMaintenance(host platformport.HostMaintenance) error {
	if host == nil || w.host != nil {
		return ErrInspectionInvalid
	}
	w.host = host
	return nil
}
