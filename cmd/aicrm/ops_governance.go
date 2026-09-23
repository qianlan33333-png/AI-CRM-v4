package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	operationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/store"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type opsSecurity struct{ requestAccessSecurity }

func (s opsSecurity) ReadPrincipal(ctx context.Context, r *http.Request) (accessdomain.Principal, error) {
	return s.Authenticate(ctx, r)
}

type opsCountReader interface {
	ReadOpsDiagnosticCounts(context.Context, time.Time) (map[string]int64, error)
}
type opsCounts func(context.Context, time.Time) (map[string]int64, error)

func opsCollector(id string, read opsCounts, warning, critical []string) opsport.InspectionCollector {
	return opsport.CollectorFunc{ID: id, Read: func(ctx context.Context, at time.Time) (opsport.CheckObservation, error) {
		metrics, e := read(ctx, at)
		o := opsport.CheckObservation{Status: "ok", Code: "observed_within_scope", ObservedAt: at, Metrics: metrics}
		if e != nil {
			o.Status = "unknown"
			o.Code = "source_unavailable"
			return o, e
		}
		for _, key := range warning {
			if metrics[key] > 0 {
				o.Status = "warning"
				o.Code = "attention_required"
			}
		}
		for _, key := range critical {
			if metrics[key] > 0 {
				o.Status = "critical"
				o.Code = "critical_invariant"
			}
		}
		if id == "config.drift" && metrics["roles_observed"] < 2 {
			o.Status = "unknown"
			o.Code = "runtime_roles_not_fully_observed"
		}
		if id == "jobqueue.workers" && (metrics["fresh_queue_observations"] == 0 || metrics["missing_queue_observations"] > 0 || metrics["stale_queue_observations"] > 0) {
			o.Status = "unknown"
			o.Code = "worker_activity_unconfirmed"
		}
		return o, nil
	}}
}
func opsInspectionCollectors(pool *pgxpool.Pool, effects *externaleffects.Repository, retention *adminops.RetentionService, probe *platformruntime.EndpointProbe, snapshot platformport.UnitOfWork, host platformport.HostMaintenanceReader, expectedQueues []string) []opsport.InspectionCollector {
	specs := []struct {
		id                string
		reader            opsCountReader
		warning, critical []string
	}{
		{"identity.conflicts", identitystore.NewOpsDiagnosticReader(pool), []string{"open_conflicts", "open_merge_candidates", "source_conflicts"}, nil},
		{"customer.projection", customerstore.NewOpsDiagnosticReader(pool), []string{"projection_conflicts", "projection_stale"}, nil},
		{"config.drift", configstore.NewOpsDiagnosticReader(pool), []string{"revision_mismatch", "release_mismatch"}, nil},
		{"payment.recovery", paymentstore.NewOpsDiagnosticReader(pool), []string{"refund_failed", "refund_overdue", "prepay_overdue"}, []string{"refund_unknown"}},
		{"outbound.delivery", outbound.NewOpsDiagnosticReader(pool), []string{"message_failed", "message_overdue", "material_unknown"}, []string{"message_unknown"}},
		{"wecom.sync", wecom.NewOpsDiagnosticReader(pool), []string{"sync_failed", "sync_overdue", "staff_refresh_failed"}, nil},
		{"segment.refresh", segmentstore.NewOpsDiagnosticReader(pool), []string{"refresh_failed", "refresh_overdue"}, nil},
		{"automation.runs", automationstore.NewOpsDiagnosticReader(pool), []string{"runs_unknown", "runs_partial_failed", "runs_overdue"}, nil},
		{"media.storage", mediastore.NewOpsDiagnosticReader(pool), []string{"expired_upload_parts"}, nil},
		{"operationcycle.actions", operationstore.NewOpsDiagnosticReader(pool), []string{"actions_failed", "actions_overdue"}, nil},
		{"survey.submissions", surveystore.NewOpsDiagnosticReader(pool), []string{"identity_conflicts"}, []string{"missing_claim_submission", "claim_owner_mismatch", "definition_mismatch"}},
		{"effects.outcomes", effects, []string{"unknown", "retryable", "final_failed", "queued_overdue"}, []string{"lease_expired"}},
	}
	out := make([]opsport.InspectionCollector, 0, len(specs)+6)
	for _, s := range specs {
		out = append(out, opsCollector(s.id, s.reader.ReadOpsDiagnosticCounts, s.warning, s.critical))
	}
	out = append(out,
		opsPaymentOrderConsistencyCollector(snapshot, paymentstore.NewOpsConsistencyReader(), orderstore.NewOpsConsistencyReader()),
		opsCollector("jobqueue.backlog", func(c context.Context, t time.Time) (map[string]int64, error) {
			return jobqueue.DiagnosticCounts(c, pool, t)
		}, []string{"due_over_10m", "running_over_10m", "discarded_retained"}, nil),
		opsCollector("jobqueue.workers", func(c context.Context, t time.Time) (map[string]int64, error) {
			return jobqueue.WorkerDiagnosticCounts(c, pool, t, expectedQueues...)
		}, []string{"paused_queues"}, nil),
		opsCollector("platform.callbacks", func(c context.Context, t time.Time) (map[string]int64, error) {
			return webhook.DiagnosticCounts(c, pool, t)
		}, []string{"failed", "due_over_10m", "expired_processing_leases"}, nil),
		opsCollector("platform.outbox", func(c context.Context, t time.Time) (map[string]int64, error) {
			return outbox.DiagnosticCounts(c, pool, t, []string{operationport.EvOperationCycleFact})
		}, []string{"expected_consumer_overdue"}, nil),
		opsCollector("runtime.resources", func(c context.Context, t time.Time) (map[string]int64, error) {
			return postgres.DiagnosticCounts(c, pool, t)
		}, []string{"lock_waiters", "long_transactions"}, nil),
		opsport.CollectorFunc{ID: "retention.lifecycle", Read: retention.RetentionHealth},
	)
	out = append(out, opsport.CollectorFunc{ID: "retention.host", Read: func(ctx context.Context, at time.Time) (opsport.CheckObservation, error) {
		o := opsport.CheckObservation{ObservedAt: at, Metrics: map[string]int64{}, Status: "unknown", Code: "host_cleanup_evidence_unavailable"}
		if host == nil {
			return o, nil
		}
		r, err := host.ReadLatest(ctx)
		if err != nil && !errors.Is(err, platformport.ErrHostMaintenancePartial) {
			return o, err
		}
		o.ObservedAt = r.StartedAt
		o.Status = "ok"
		o.Code = "host_cleanup_observed"
		if r.Runtime != nil {
			o.Metrics = map[string]int64{"deleted_files": r.Runtime.Deleted, "deleted_logical_bytes": r.Runtime.Bytes, "protected_files": r.Runtime.Protected}
			if r.Runtime.Remaining {
				o.Status = "warning"
				o.Code = "host_cleanup_backlog"
			}
		}
		if errors.Is(err, platformport.ErrHostMaintenancePartial) {
			o.Status, o.Code = "warning", "host_cleanup_partial"
		}
		return o, nil
	}})
	out = append(out, opsReleaseMaintenanceCollector(host))
	if probe != nil {
		out = append(out, opsCollector("runtime.endpoints", probe.Counts, []string{"failed_endpoints", "version_mismatch"}, []string{"auth_boundary_failed"}))
	}
	out = append(out, opsport.CollectorFunc{ID: "runtime.host", Read: func(ctx context.Context, at time.Time) (opsport.CheckObservation, error) {
		metrics, err := platformruntime.ResourceCounts(ctx)
		o := opsport.CheckObservation{ObservedAt: at, Metrics: metrics, Status: "ok", Code: "host_resources_observed"}
		if err != nil {
			o.Status = "unknown"
			o.Code = "host_resources_unavailable"
			return o, err
		}
		if metrics["filesystem_used_percent"] >= 85 {
			o.Status = "warning"
			o.Code = "filesystem_capacity_low"
		}
		if metrics["filesystem_used_percent"] >= 95 {
			o.Status = "critical"
			o.Code = "filesystem_capacity_critical"
		}
		return o, nil
	}})
	return out
}

func opsReleaseMaintenanceCollector(host platformport.HostMaintenanceReader) opsport.InspectionCollector {
	return opsport.CollectorFunc{ID: "retention.releases", Read: func(ctx context.Context, at time.Time) (opsport.CheckObservation, error) {
		o := opsport.CheckObservation{ObservedAt: at, Metrics: map[string]int64{}, Status: "unknown", Code: "release_cleanup_evidence_unavailable"}
		reader, ok := host.(platformport.ReleaseMaintenanceReader)
		if !ok {
			return o, nil
		}
		r, err := reader.ReadReleaseLatest(ctx)
		if err != nil && !errors.Is(err, platformport.ErrHostMaintenancePartial) {
			return o, err
		}
		o.Status, o.Code = "ok", "release_cleanup_observed"
		if r.State == "gap" {
			o.Status, o.Code = "warning", r.Reason
		}
		// The reader verified evidence against the current installed SHA; its
		// publication date is separate from the time we re-checked that fact.
		o.Metrics["evidence_age_seconds"] = max(0, int64(at.Sub(r.ObservedAt).Seconds()))
		for key, count := range map[string]*int64{"deleted_releases": r.DeletedCount, "deleted_logical_bytes": r.DeletedBytes, "verified_rollbacks": r.RollbackCount} {
			if count != nil {
				o.Metrics[key] = *count
			}
		}
		return o, nil
	}}
}

type opsCompletion struct{ observer opsport.OpsReportObserver }

func (s opsCompletion) CompleteEffect(ctx context.Context, id string, e effectport.Envelope, a effectport.Attempt, r effectport.AdapterResult) error {
	return s.observer.ObserveOpsReportWithin(ctx, effectport.Projection{ID: id, Owner: e.Owner, Kind: e.Kind, State: r.Completion, AttemptCount: a.Number, Generation: a.Generation, UpdatedAt: time.Now().UTC()})
}

func mountOpsGovernance(next http.Handler, inspection, retention, cpuProfiles, outcomes http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/admin/ops-governance/", outcomes)
	for _, path := range []string{"/api/admin/ops-diagnostics/cpu-profiles", "/api/admin/ops-diagnostics/cpu-profiles/"} {
		mux.Handle(path, cpuProfiles)
	}
	for _, path := range []string{"/api/admin/ops-inspections", "/api/admin/ops-inspections/", "/api/admin/ops-diagnostics", "/api/admin/ops-diagnostics/"} {
		mux.Handle(path, inspection)
	}
	for _, path := range []string{"/api/admin/ops-retention", "/api/admin/ops-retention/"} {
		mux.Handle(path, retention)
	}
	mux.Handle("/", next)
	return mux
}

// Each adapter invokes its Owner's maintenance Port in the same local transaction.
func bindOpsOwnerRetention(service *adminops.RetentionService, config configport.ProcessRetention, archive archiveport.ProcessRetention) error {
	if err := service.BindProcessPolicy("config_usage", opsport.ProcessRetentionFunc(func(ctx context.Context, c opsport.ProcessRetentionCommand) (opsport.ProcessRetentionReport, error) {
		r, e := config.CleanupProcessDetailWithin(ctx, configport.ProcessRetentionCommand{Before: c.Before, Limit: c.Limit, Apply: c.Apply})
		return opsport.ProcessRetentionReport{Before: r.Before, Candidates: r.Candidates, Deleted: r.Deleted, Bytes: r.Bytes, Remaining: r.Remaining}, e
	})); err != nil {
		return err
	}
	return service.BindProcessPolicy("archive_sync_runs", opsport.ProcessRetentionFunc(func(ctx context.Context, c opsport.ProcessRetentionCommand) (opsport.ProcessRetentionReport, error) {
		r, e := archive.CleanupProcessDetailWithin(ctx, archiveport.ProcessRetentionCommand{Before: c.Before, Limit: c.Limit, Apply: c.Apply})
		return opsport.ProcessRetentionReport{Before: r.Before, Candidates: r.Candidates, Deleted: r.Deleted, Bytes: r.Bytes, Remaining: r.Remaining}, e
	}))
}
