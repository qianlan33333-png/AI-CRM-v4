package adminops

import (
	"context"
	"github.com/riverqueue/river"
	"time"
)

type InspectionJobArgs struct {
	Slot                time.Time `json:"slot,omitempty"`
	ManualRequestDigest string    `json:"manual_request_digest,omitempty"`
}

func (InspectionJobArgs) Kind() string { return "adminops.inspection.v1" }

type OpsReportJobArgs struct {
	Hour time.Time `json:"hour"`
}

func (OpsReportJobArgs) Kind() string { return "adminops.hourly-report.v1" }

type InspectionWorker struct {
	river.WorkerDefaults[InspectionJobArgs]
	service *InspectionService
}
type ReportWorker struct {
	river.WorkerDefaults[OpsReportJobArgs]
	service *InspectionService
}

func NewInspectionWorker() *InspectionWorker { return &InspectionWorker{} }
func NewReportWorker() *ReportWorker         { return &ReportWorker{} }
func (w *InspectionWorker) BindService(s *InspectionService) error {
	if s == nil || w.service != nil {
		return ErrInspectionInvalid
	}
	w.service = s
	return nil
}
func (w *ReportWorker) BindService(s *InspectionService) error {
	if s == nil || w.service != nil {
		return ErrInspectionInvalid
	}
	w.service = s
	return nil
}
func (*InspectionWorker) Timeout(*river.Job[InspectionJobArgs]) time.Duration { return 2 * time.Minute }
func (*ReportWorker) Timeout(*river.Job[OpsReportJobArgs]) time.Duration      { return 2 * time.Minute }
func (w *InspectionWorker) Work(ctx context.Context, j *river.Job[InspectionJobArgs]) error {
	if w.service == nil || j == nil {
		return ErrInspectionInvalid
	}
	if j.Args.ManualRequestDigest != "" {
		if !j.Args.Slot.IsZero() {
			return ErrInspectionInvalid
		}
		return w.service.executeManualInspection(ctx, j.Args.ManualRequestDigest)
	}
	_, e := w.service.Scan(ctx, j.Args.Slot)
	return e
}
func (w *ReportWorker) Work(ctx context.Context, j *river.Job[OpsReportJobArgs]) error {
	if w.service == nil || j == nil {
		return ErrInspectionInvalid
	}
	if _, e := w.service.Scan(ctx, w.service.now()); e != nil {
		return e
	}
	_, e := w.service.PrepareReport(ctx, j.Args.Hour)
	return e
}
func InspectionPeriodicJobs() []*river.PeriodicJob {
	// Production uses one durable hourly poll. The report worker performs the
	// scan and freezes the previous complete hour in the same River lane; there
	// is no separate five-minute inspection loop.
	return []*river.PeriodicJob{
		river.NewPeriodicJob(opsHourlySchedule{}, func() (river.JobArgs, *river.InsertOpts) {
			return OpsReportJobArgs{Hour: time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)}, &river.InsertOpts{Queue: InspectionQueue, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Hour}}
		}, &river.PeriodicJobOpts{ID: "adminops-hourly-report-v1", RunOnStart: false}),
	}
}

// Five minutes past each clock hour, including after restart; the durable job
// records the previous hour key rather than process-start-relative cadence.
type opsHourlySchedule struct{}

func (opsHourlySchedule) Next(at time.Time) time.Time {
	at = at.UTC()
	next := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), 5, 0, 0, time.UTC)
	if !next.After(at) {
		next = next.Add(time.Hour)
	}
	return next
}
