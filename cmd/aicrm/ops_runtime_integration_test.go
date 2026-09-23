package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type opsUncertainProvider struct{ calls, deferrals int }

type opsFailOnceCompletion struct {
	next effectport.CompletionSink
	fail bool
}

func (s *opsFailOnceCompletion) CompleteEffect(ctx context.Context, id string, e effectport.Envelope, a effectport.Attempt, r effectport.AdapterResult) error {
	if err := s.next.CompleteEffect(ctx, id, e, a, r); err != nil {
		return err
	}
	if s.fail {
		s.fail = false
		return errors.New("fixture completion transaction failure")
	}
	return nil
}

func (p *opsUncertainProvider) Execute(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
	if p.deferrals > 0 {
		p.deferrals--
		return effectport.AdapterResult{Completion: effectport.StateRetryable, CallAttempted: false, ReceiptDigest: effectport.Hash("dns-before-connect")}, nil
	}
	p.calls++
	return effectport.AdapterResult{Completion: effectport.StateUnknown, CallAttempted: true, ReceiptDigest: effectport.Hash("timeout")}, nil
}

func TestPostgreSQLOpsRuntimePreservesUnknownAndReportAtomicity(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := postgres.Open(ctx, postgres.Config{URL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := postgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	module := externaleffects.NewModuleRegistration()
	if err = module.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	client, err := jobqueue.NewInsertClient(pool.Native(), workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(pool.Native(), client)
	if err != nil {
		t.Fatal(err)
	}
	s, err := adminops.NewInspectionService(pool.Native(), uow, nil, effects, adminops.InspectionOptions{ReleaseSHA: strings.Repeat("a", 40), NotificationEnabled: true, NotificationTargetRef: "fixture-original"})
	if err != nil {
		t.Fatal(err)
	}
	sink := &opsFailOnceCompletion{next: opsCompletion{s}}
	if err = effects.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	ctx, err = diagnostics.WithCorrelation(ctx, diagnostics.Correlation{RequestID: strings.Repeat("b", 32), ReleaseSHA: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	report, err := s.PrepareReport(ctx, hour)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.PrepareReport(ctx, hour)
	if err != nil || report.ID != repeated.ID || report.EffectID != repeated.EffectID {
		t.Fatalf("hour replay=%+v %v", repeated, err)
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(report.EffectID, "eer_"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var jobID int64
	var queue, metadata string
	if err = pool.Native().QueryRow(ctx, `SELECT j.river_job_id,j.queue,r.metadata::text FROM external_effect_jobs j JOIN river_job r ON r.id=j.river_job_id WHERE j.effect_id=$1`, id).Scan(&jobID, &queue, &metadata); err != nil {
		t.Fatal(err)
	}
	if queue != jobqueue.OpsNotificationQueue || !strings.Contains(metadata, report.EffectID) || !strings.Contains(metadata, strings.Repeat("b", 32)) {
		t.Fatalf("lane/context=%s %s", queue, metadata)
	}
	provider := &opsUncertainProvider{deferrals: 1}
	worker := externaleffects.NewWorker(effects, composedProviderRouter{adminops: provider})
	job := &river.Job[externaleffects.EffectJobArgs]{JobRow: &rivertype.JobRow{ID: jobID}, Args: externaleffects.EffectJobArgs{EffectID: id, Generation: 1}}
	if err = worker.Work(ctx, job); err == nil {
		t.Fatal("definite pre-call failure must snooze the same job")
	}
	var state string
	if err = pool.Native().QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, id).Scan(&state); err != nil || state != "queued" || provider.calls != 0 {
		t.Fatalf("pre-call recovery: %s calls=%d %v", state, provider.calls, err)
	}
	sink.fail = true
	if err = worker.Work(ctx, job); err == nil {
		t.Fatal("completion failure was hidden")
	}
	if err = pool.Native().QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, id).Scan(&state); err != nil || state != "attempted" {
		t.Fatalf("completion rollback lost attempted fact: %s %v", state, err)
	}
	if err = worker.Work(ctx, job); err == nil {
		t.Fatal("active attempt lease was acknowledged instead of deferred")
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = worker.Work(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	reports, err := s.Reports(ctx)
	if err != nil || len(reports) != 1 || reports[0].EffectState != "outcome_unknown" || provider.calls != 1 {
		t.Fatalf("unknown replay=%+v calls=%d %v", reports, provider.calls, err)
	}
	_, _, err = effects.Retry(ctx, externaleffects.ControlCommand{EffectID: report.EffectID, ReceiptKey: effectport.Hash("retry"), ActorAdminUserID: 1})
	if !errors.Is(err, externaleffects.ErrTransition) {
		t.Fatalf("unknown retry accepted: %v", err)
	}
	// Acceptance evidence is non-deletable, including the new Owner matrix.
	if _, err = pool.Native().Exec(ctx, `DELETE FROM external_effects WHERE id=$1`, id); err == nil {
		t.Fatal("effect evidence deleted")
	}
	// A report write rolled back by the caller must not leak an accepted effect.
	if err = uow.Within(ctx, func(c context.Context) error {
		_, _, e := effects.AcceptAndQueueWithin(c, effectport.AcceptCommand{Envelope: effectport.Envelope{Owner: effectport.OwnerAdminOps, Kind: effectport.KindFeishuOpsNotification, SourceRefDigest: effectport.Hash("rollback-source"), TargetRefDigest: effectport.Hash("rollback-target"), PayloadDigest: effectport.Hash("rollback-payload"), PolicyVersionHash: effectport.Hash("rollback-policy")}, ReceiptKey: effectport.Hash("rollback-receipt"), Lane: effectport.LaneOpsNotification})
		if e != nil {
			return e
		}
		return errors.New("fixture rollback")
	}); err == nil {
		t.Fatal("expected rollback")
	}
	var count int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("leaked acceptance count=%d %v", count, err)
	}
}
func TestPostgreSQLOpsPlatformFactsUseDueTimeAndExpectedWorkers(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := postgres.Open(ctx, postgres.Config{URL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p := pool.Native()
	at := time.Now().UTC()
	reads := map[string]func() (map[string]int64, error){"postgres": func() (map[string]int64, error) { return postgres.DiagnosticCounts(ctx, p, at) }, "webhook": func() (map[string]int64, error) { return webhook.DiagnosticCounts(ctx, p, at) }, "outbox": func() (map[string]int64, error) {
		return outbox.DiagnosticCounts(ctx, p, at, []string{"fixture.expected"})
	}, "queue": func() (map[string]int64, error) { return jobqueue.DiagnosticCounts(ctx, p, at) }}
	for name, read := range reads {
		if _, err = read(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err = p.Exec(ctx, `INSERT INTO river_job(kind,queue,args,state,scheduled_at,created_at,max_attempts,priority) VALUES('fixture','outbound','{}','scheduled',$1,$2,5,1),('fixture','outbound','{}','scheduled',$3,$2,5,1)`, at.Add(time.Hour), at.Add(-72*time.Hour), at.Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := jobqueue.DiagnosticCounts(ctx, p, at)
	if err != nil || got["future_scheduled"] != 1 || got["due_over_10m"] != 1 {
		t.Fatalf("due time=%v %v", got, err)
	}
	got, err = jobqueue.WorkerDiagnosticCounts(ctx, p, at, "outbound", "referral-campaign", "payment-reconciliation")
	if err != nil || got["missing_queue_observations"] != got["expected_queues"] {
		t.Fatalf("missing worker false green=%v %v", got, err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO river_queue(name,updated_at) VALUES('outbound',$1)`, at); err != nil {
		t.Fatal(err)
	}
	got, err = jobqueue.WorkerDiagnosticCounts(ctx, p, at, "outbound", "referral-campaign", "payment-reconciliation")
	if err != nil || got["fresh_queue_observations"] != 1 || got["missing_queue_observations"] == 0 {
		t.Fatalf("one worker hides others=%v %v", got, err)
	}
}
