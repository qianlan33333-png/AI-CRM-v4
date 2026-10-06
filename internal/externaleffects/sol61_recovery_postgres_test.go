package externaleffects

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/provider"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// The sink requires EER's completion UOW and records the actual dispatched
// outcome. It does not infer an owner outcome from EER's read projection.
type sol61RecoverySink struct{ fail bool }

var sol61OwnerCompletionError = errors.New("injected owner completion failure")

func (s *sol61RecoverySink) CompleteEffect(ctx context.Context, ref string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if s.fail {
		return sol61OwnerCompletionError
	}
	_, err = tx.Exec(ctx, `INSERT INTO sol61_owner_outcomes(effect_ref,kind,state,attempt) VALUES($1,$2,$3,$4)`, ref, envelope.Kind, result.Completion, attempt.Number)
	return err
}

type sol61GenerationWriter struct{ calls int }

func (s *sol61GenerationWriter) CompleteGeneration(ctx context.Context, _ automationport.GenerationCompletion) error {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return err
	}
	s.calls++
	return nil
}

// A caller-supplied evidence digest does not include a model result. The
// actual generation sink cannot turn it into the owner completion contract.
// Reject that command explicitly, retaining unknown and the original attempt.
func TestSol61PostgreSQLUnknownGenerationGenericReconcileIsExplicitConflict(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	writer := &sol61GenerationWriter{}
	sink, err := automationprovider.NewGenerationCompletionSink(writer)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	envelope := envelopeForTest()
	envelope.Owner, envelope.Kind, envelope.SourceRefDigest = OwnerAutomation, KindAIAgentGenerate, Hash("sol61-generation-reconcile")
	projection, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-generation-reconcile-key"), Envelope: envelope})
	if err != nil {
		t.Fatal(err)
	}
	id := parseProjectionID(t, projection.ID)
	jobID := markAttemptedAfterCrash(t, pool, id, "-1 minute")
	if err = repository.RunAttempt(ctx, id, 1, jobID, nil); err != nil {
		t.Fatal(err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	handler, err := NewHTTPHandler(repository, effectTestSecurity{writePrincipal: admin})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/external-effects/"+projection.ID+"/reconcile", strings.NewReader(`{"evidence_digest":"`+string(Hash("sol61-evidence"))+`"}`))
	request.Header.Set("Idempotency-Key", "sol61-generation-reconcile-command")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	current, err := repository.Get(ctx, projection.ID)
	if err != nil || current.State != StateUnknown || current.AttemptCount != 1 || writer.calls != 1 {
		t.Fatalf("unsupported reconciliation changed original unknown: effect=%+v owner_calls=%d err=%v", current, writer.calls, err)
	}
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "state_conflict") {
		t.Fatalf("unsupported generation reconciliation must be domain conflict, got status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSol61PostgreSQLExpiredAttemptsProjectAllRegisteredOutboundOwners(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE sol61_owner_outcomes(effect_ref TEXT PRIMARY KEY,kind TEXT,state TEXT,attempt INTEGER)`); err != nil {
		t.Fatal(err)
	}
	sol61ApplyProductionQueueConstraint(t, pool)
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	sink := &sol61RecoverySink{}
	if err = repository.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{KindOutboundMessage, KindChannelWelcome, KindChannelEntryTag, KindChannelLink, KindWeComContactDescription} {
		t.Run(string(kind), func(t *testing.T) {
			envelope := envelopeForTest()
			envelope.Kind, envelope.SourceRefDigest = kind, Hash("sol61-recovery", string(kind))
			projection, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-recovery-key", string(kind)), Envelope: envelope})
			if err != nil {
				t.Fatal(err)
			}
			id := parseProjectionID(t, projection.ID)
			jobID := markAttemptedAfterCrash(t, pool, id, "-1 minute")
			adapter := &integrationAdapter{}
			if err = repository.RunAttempt(ctx, id, 1, jobID, adapter); err != nil {
				t.Fatal(err)
			}
			current, err := repository.Get(ctx, projection.ID)
			if err != nil || current.State != StateUnknown || current.AttemptCount != 1 || adapter.calls != 0 {
				t.Fatalf("expired attempt recovery=%+v provider_calls=%d err=%v", current, adapter.calls, err)
			}
			var state string
			var number int
			if err = pool.QueryRow(ctx, `SELECT state,attempt FROM sol61_owner_outcomes WHERE effect_ref=$1`, projection.ID).Scan(&state, &number); err != nil || state != string(StateUnknown) || number != 1 {
				t.Fatalf("EER became unknown but owner completion missing: kind=%s owner_state=%q attempt=%d err=%v", kind, state, number, err)
			}
			if err = repository.RunAttempt(ctx, id, 1, jobID, adapter); err != nil || adapter.calls != 0 {
				t.Fatalf("recovered job replay repeated Provider: calls=%d err=%v", adapter.calls, err)
			}
			if _, _, err = repository.Retry(ctx, ControlCommand{EffectID: projection.ID, ReceiptKey: Hash("sol61-unknown-retry", string(kind)), ActorAdminUserID: 7}); !errors.Is(err, ErrTransition) {
				t.Fatalf("unknown must retain original attempt: retry=%v", err)
			}
		})
	}
	t.Run("owner_failure_rolls_back_unknown_and_original_attempt", func(t *testing.T) {
		envelope := envelopeForTest()
		envelope.Kind, envelope.SourceRefDigest = KindChannelWelcome, Hash("sol61-recovery-rollback")
		projection, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-recovery-rollback-key"), Envelope: envelope})
		if err != nil {
			t.Fatal(err)
		}
		id := parseProjectionID(t, projection.ID)
		jobID := markAttemptedAfterCrash(t, pool, id, "-1 minute")
		adapter := &integrationAdapter{}
		sink.fail = true
		if err = repository.RunAttempt(ctx, id, 1, jobID, adapter); err == nil {
			t.Fatal("owner completion failure must roll back EER recovery")
		}
		var effectState, attemptState string
		if err = pool.QueryRow(ctx, `SELECT e.state,a.state FROM external_effects e JOIN external_effect_attempts a ON a.effect_id=e.id AND a.number=1 WHERE e.id=$1`, id).Scan(&effectState, &attemptState); err != nil || effectState != "attempted" || attemptState != "attempted" || adapter.calls != 0 {
			t.Fatalf("non-atomic recovery effect=%s attempt=%s calls=%d err=%v", effectState, attemptState, adapter.calls, err)
		}
		sink.fail = false
		if err = repository.RunAttempt(ctx, id, 1, jobID, adapter); err != nil || adapter.calls != 0 {
			t.Fatalf("same job failed recovery after projector restored: calls=%d err=%v", adapter.calls, err)
		}
	})
}

// The shared lightweight fixture predates Welcome's queue. Read the actual
// production migration's queue DDL so these tests retain all five registered
// queues instead of introducing a narrower test-only constraint.
func sol61ApplyProductionQueueConstraint(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production queue migration")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0187_adminops_notification_effect.sql"))
	if err != nil {
		t.Fatal(err)
	}
	statements := 0
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "ALTER TABLE external_effect_jobs ") {
			continue
		}
		if _, err = pool.Exec(context.Background(), line); err != nil {
			t.Fatal(err)
		}
		statements++
	}
	if statements != 2 {
		t.Fatalf("production queue migration DDL statements=%d, want 2", statements)
	}
}

type sol61ErrorPreflight struct{ calls atomic.Int32 }

// This exercises only EER's expired-lease owner callback, with no payment
// provider, callback verifier, reconciliation query, or funds settlement.
// The fixture admits the six payment kinds registered by migration 0170.
func TestSol61PostgreSQLExpiredPaymentOwnerInvokesBoundCompletionWithoutAdapter(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE sol61_owner_outcomes(effect_ref TEXT PRIMARY KEY,kind TEXT,state TEXT,attempt INTEGER)`); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SetCompletionSink(&sol61RecoverySink{}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{KindWeChatPayPrepay, KindWeChatPayRefund, KindWeChatShopRefund, effectport.KindWeChatPayReceiverAdd, effectport.KindWeChatPayProfitSharing, effectport.KindWeChatPayProfitUnfreeze} {
		t.Run(string(kind), func(t *testing.T) {
			envelope := envelopeForTest()
			envelope.Owner, envelope.Kind, envelope.SourceRefDigest = OwnerPayment, kind, Hash("sol61-payment-lease", string(kind))
			projection, _, acceptErr := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-payment-lease-key", string(kind)), Envelope: envelope})
			if acceptErr != nil {
				t.Fatal(acceptErr)
			}
			id := parseProjectionID(t, projection.ID)
			jobID := markAttemptedAfterCrash(t, pool, id, "-1 minute")
			adapter := &integrationAdapter{}
			if err = repository.RunAttempt(ctx, id, 1, jobID, adapter); err != nil {
				t.Fatal(err)
			}
			var state string
			var number int
			if err = pool.QueryRow(ctx, `SELECT state,attempt FROM sol61_owner_outcomes WHERE effect_ref=$1`, projection.ID).Scan(&state, &number); err != nil || state != string(StateUnknown) || number != 1 || adapter.calls != 0 {
				t.Fatalf("expired payment effect omitted UOW owner callback: kind=%s owner_state=%q attempt=%d adapter_calls=%d err=%v", kind, state, number, adapter.calls, err)
			}
		})
	}
}

func (p *sol61ErrorPreflight) Preflight(context.Context, effectport.Envelope, string) (bool, time.Duration, error) {
	p.calls.Add(1)
	return false, 0, errors.New("injected prerequisite read unavailable")
}

func (p *sol61ErrorPreflight) Execute(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
	return effectport.AdapterResult{}, errors.New("Provider must never be invoked before preflight is ready")
}

// A post-call completion outage needs the same durable job after its lease,
// even when this was River's final attempt. The original attempted record is
// the only permitted recovery source; retrying Execute would duplicate I/O.
func TestSol61PostgreSQLSettlementFailureDefersOriginalAttemptRecovery(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE sol61_owner_outcomes(effect_ref TEXT PRIMARY KEY,kind TEXT,state TEXT,attempt INTEGER)`); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	sink := &sol61RecoverySink{fail: true}
	if err = repository.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	projection, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-settlement-recovery"), Envelope: envelopeForTest()})
	if err != nil {
		t.Fatal(err)
	}
	id := parseProjectionID(t, projection.ID)
	adapter := &integrationAdapter{result: AdapterResult{Completion: StateExecuted, ReceiptDigest: Hash("sol61-provider-success-before-settlement-outage"), CallAttempted: true, RealExternalCallExecuted: true}}
	worker := NewWorker(repository, adapter)
	job := &river.Job[EffectJobArgs]{JobRow: &rivertype.JobRow{ID: projection.QueueJobID, Attempt: 1, MaxAttempts: 1}, Args: EffectJobArgs{EffectID: id, Generation: 1}}
	err = worker.Work(ctx, job)
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) || snooze.Duration <= 0 || !errors.Is(err, sol61OwnerCompletionError) {
		t.Fatalf("post-call completion failure would exhaust River instead of preserving original job: err=%v", err)
	}
	var state, attemptState string
	if err = pool.QueryRow(ctx, `SELECT e.state,a.state FROM external_effects e JOIN external_effect_attempts a ON a.effect_id=e.id WHERE e.id=$1`, id).Scan(&state, &attemptState); err != nil || state != "attempted" || attemptState != "attempted" || adapter.calls != 1 {
		t.Fatalf("lost settlement did not retain one original attempt: effect=%s attempt=%s calls=%d err=%v", state, attemptState, adapter.calls, err)
	}
	sink.fail = false
	if _, err = pool.Exec(ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = worker.Work(ctx, job); err != nil || adapter.calls != 1 {
		t.Fatalf("restored same-job recovery repeated Provider: calls=%d err=%v", adapter.calls, err)
	}
	current, err := repository.Get(ctx, projection.ID)
	if err != nil || current.State != StateUnknown || current.AttemptCount != 1 {
		t.Fatalf("lost completion must be unknown: effect=%+v err=%v", current, err)
	}
}

func TestSol61PostgreSQLPreflightErrorCannotDiscardQueuedEffect(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	workers := river.NewWorkers()
	adapter := &sol61ErrorPreflight{}
	worker := NewWorker(nil, adapter)
	if err := river.AddWorkerSafely[EffectJobArgs](workers, worker); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.BindRepository(repository); err != nil {
		t.Fatal(err)
	}
	projection, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-preflight-exhaustion"), Envelope: envelopeForTest()})
	if err != nil {
		t.Fatal(err)
	}
	// Start at River's last permitted attempt to reproduce actual exhaustion
	// immediately, without sleeping through its production retry backoff.
	if _, err = pool.Exec(ctx, `UPDATE river_job SET max_attempts=1 WHERE id=$1`, projection.QueueJobID); err != nil {
		t.Fatal(err)
	}
	runtime, err := platformjobqueue.NewRuntime(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(runCtx) }()
	defer func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stop River: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("River did not stop")
		}
	}()
	for {
		var jobState, effectState string
		var attempts int
		err = pool.QueryRow(ctx, `SELECT j.state,e.state,e.attempt_count FROM river_job j JOIN external_effect_jobs ej ON ej.river_job_id=j.id JOIN external_effects e ON e.id=ej.effect_id WHERE j.id=$1`, projection.QueueJobID).Scan(&jobState, &effectState, &attempts)
		if err != nil {
			t.Fatal(err)
		}
		if adapter.calls.Load() > 0 && (jobState == "discarded" || jobState == "scheduled" || jobState == "available") {
			if jobState == "discarded" {
				t.Fatalf("prerequisite read consumed last River attempt, leaving effect permanently queued: job=%d river=%s effect=%s effect_attempts=%d", projection.QueueJobID, jobState, effectState, attempts)
			}
			if effectState != "queued" || attempts != 0 {
				t.Fatalf("preflight fabricated Provider attempt: state=%s attempts=%d", effectState, attempts)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(fmt.Errorf("waiting for preflight retry state: %w", ctx.Err()))
		case <-time.After(10 * time.Millisecond):
		}
	}
}
