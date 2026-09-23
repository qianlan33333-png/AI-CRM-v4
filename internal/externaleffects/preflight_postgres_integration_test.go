package externaleffects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	port "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	queue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

type preflightAdapter struct{ ready, executed bool }

func (a *preflightAdapter) Preflight(context.Context, port.Envelope, string) (bool, time.Duration, error) {
	return a.ready, 2 * time.Second, nil
}

type laneBlockingAdapter struct {
	mu      sync.Mutex
	current map[port.Kind]int
	maximum map[port.Kind]int
	started chan struct{}
	release chan struct{}
}

type mediaReconciliationSink struct {
	pool *pgxpool.Pool
	fail bool
}

func (s *mediaReconciliationSink) CompleteEffect(ctx context.Context, effectID string, _ port.Envelope, _ port.Attempt, result port.AdapterResult) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if s.fail {
		return errors.New("projector unavailable")
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_reconciliation_projection(effect_id,state,failure_code,artifact_digest) VALUES($1,$2,$3,$4) ON CONFLICT(effect_id) DO UPDATE SET state=EXCLUDED.state,failure_code=EXCLUDED.failure_code,artifact_digest=EXCLUDED.artifact_digest`, effectID, result.Completion, result.FailureCode, result.Artifact.Digest)
	return err
}

type mediaRetryOnceAdapter struct{ calls int }

func (a *mediaRetryOnceAdapter) Execute(context.Context, port.Envelope, port.Attempt) (port.AdapterResult, error) {
	a.calls++
	if a.calls == 1 {
		return port.AdapterResult{Completion: port.StateRetryable, ReceiptDigest: port.Hash("provider-rate-limited"), CallAttempted: true, SafeToRetryRejected: true, FailureCode: "45009"}, nil
	}
	artifact := port.ResultArtifact{Kind: "outbound.material.upload.v1", Payload: []byte(`{"media_id":"retried","provider_created_at":"2026-09-10T08:00:00Z"}`)}
	artifact.Digest = port.Hash("external-effect.artifact.v1", artifact.Kind, string(artifact.Payload))
	return port.AdapterResult{Completion: port.StateExecuted, ReceiptDigest: port.Hash("provider-uploaded"), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

type excelRetryOnceAdapter struct{ calls int }

func (a *excelRetryOnceAdapter) Execute(context.Context, port.Envelope, port.Attempt) (port.AdapterResult, error) {
	a.calls++
	if a.calls == 1 {
		return port.AdapterResult{Completion: port.StateRetryable, ReceiptDigest: port.Hash("wecom-add-msg-template-45009"), CallAttempted: true, RealExternalCallExecuted: false, SafeToRetryRejected: true, FailureCode: "wecom_errcode_45009"}, nil
	}
	return port.AdapterResult{Completion: port.StateExecuted, ReceiptDigest: port.Hash("wecom-add-msg-template-msgid"), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func (a *laneBlockingAdapter) Execute(ctx context.Context, e port.Envelope, _ port.Attempt) (port.AdapterResult, error) {
	a.mu.Lock()
	a.current[e.Kind]++
	if a.current[e.Kind] > a.maximum[e.Kind] {
		a.maximum[e.Kind] = a.current[e.Kind]
	}
	a.mu.Unlock()
	a.started <- struct{}{}
	select {
	case <-a.release:
	case <-ctx.Done():
	}
	a.mu.Lock()
	a.current[e.Kind]--
	a.mu.Unlock()
	return port.AdapterResult{Completion: port.StateFinalFailed, ReceiptDigest: port.Hash("blocked", string(e.Kind))}, nil
}
func (a *preflightAdapter) Execute(context.Context, port.Envelope, port.Attempt) (port.AdapterResult, error) {
	a.executed = true
	return port.AdapterResult{Completion: port.StateExecuted, ReceiptDigest: port.Hash("preflight-test-receipt"), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func TestQueuedPreflightSnoozesWithoutMessageAttemptPostgreSQL(t *testing.T) {
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("eer_preflight_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0005_external_effects.sql"} {
		raw, e := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE external_effect_jobs DROP CONSTRAINT external_effect_jobs_queue_check;ALTER TABLE external_effect_jobs ADD CONSTRAINT external_effect_jobs_queue_check CHECK(queue IN ('outbound','outbound_welcome','outbound_excel','outbound_media'))`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE external_effects ADD COLUMN delivery_lane TEXT NOT NULL DEFAULT '' CHECK(delivery_lane IN ('','outbound_excel','outbound_media'))`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE media_reconciliation_projection(effect_id TEXT PRIMARY KEY,state TEXT NOT NULL,failure_code TEXT NOT NULL,artifact_digest TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	adapter := &preflightAdapter{}
	worker := NewWorker(nil, adapter)
	if err = river.AddWorkerSafely[EffectJobArgs](workers, worker); err != nil {
		t.Fatal(err)
	}
	insert, err := queue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(pool, insert)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.BindRepository(repo); err != nil {
		t.Fatal(err)
	}
	envelope := Envelope{Owner: OwnerOutbound, Kind: KindOutboundMessage, SourceRefDigest: Hash("s"), TargetRefDigest: Hash("t"), PayloadDigest: Hash("p"), PolicyVersionHash: Hash("v")}
	projection, _, err := repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("r"), Envelope: envelope, Lane: port.LaneOutboundExcel})
	if err != nil {
		t.Fatal(err)
	}
	err = worker.Work(ctx, &river.Job[EffectJobArgs]{JobRow: &rivertype.JobRow{ID: projection.QueueJobID}, Args: EffectJobArgs{EffectID: parseProjectionID(t, projection.ID), Generation: 1}})
	if err == nil {
		t.Fatal("pending preflight did not snooze")
	}
	var state string
	var attempts int
	if err = pool.QueryRow(ctx, `SELECT state,attempt_count FROM external_effects WHERE id=$1`, parseProjectionID(t, projection.ID)).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || attempts != 0 || adapter.executed {
		t.Fatalf("state=%s attempts=%d executed=%v", state, attempts, adapter.executed)
	}
	var lane string
	if err = pool.QueryRow(ctx, `SELECT queue FROM external_effect_jobs WHERE effect_id=$1`, parseProjectionID(t, projection.ID)).Scan(&lane); err != nil {
		t.Fatal(err)
	}
	if lane != queue.OutboundExcelQueue {
		t.Fatalf("lane=%s", lane)
	}
	adapter.ready = true
	if err = worker.Work(ctx, &river.Job[EffectJobArgs]{JobRow: &rivertype.JobRow{ID: projection.QueueJobID}, Args: EffectJobArgs{EffectID: parseProjectionID(t, projection.ID), Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT state,attempt_count FROM external_effects WHERE id=$1`, parseProjectionID(t, projection.ID)).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "executed" || attempts != 1 || !adapter.executed {
		t.Fatalf("state=%s attempts=%d executed=%v", state, attempts, adapter.executed)
	}
	blocking := &laneBlockingAdapter{current: map[port.Kind]int{}, maximum: map[port.Kind]int{}, started: make(chan struct{}, 9), release: make(chan struct{})}
	runtimeWorkers := river.NewWorkers()
	if err = river.AddWorkerSafely[EffectJobArgs](runtimeWorkers, NewWorker(repo, blocking)); err != nil {
		t.Fatal(err)
	}
	runtime, err := queue.NewRuntime(pool, runtimeWorkers, queue.OutboundExcelQueue, queue.OutboundMediaQueue)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		e := envelope
		e.SourceRefDigest = Hash("excel", strconv.Itoa(i))
		if _, _, err = repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("excel-r", strconv.Itoa(i)), Envelope: e, Lane: port.LaneOutboundExcel}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		e := envelope
		e.Kind = KindOutboundMedia
		e.SourceRefDigest = Hash("media", strconv.Itoa(i))
		if _, _, err = repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("media-r", strconv.Itoa(i)), Envelope: e, Lane: port.LaneOutboundMedia}); err != nil {
			t.Fatal(err)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(runCtx) }()
	for i := 0; i < 5; i++ {
		select {
		case <-blocking.started:
		case <-time.After(8 * time.Second):
			t.Fatal("independent lanes did not reach five concurrent jobs")
		}
	}
	blocking.mu.Lock()
	excelMax, mediaMax := blocking.maximum[KindOutboundMessage], blocking.maximum[KindOutboundMedia]
	blocking.mu.Unlock()
	if excelMax != 3 || mediaMax != 2 {
		t.Fatalf("max concurrency excel=%d media=%d", excelMax, mediaMax)
	}
	close(blocking.release)
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runtime did not stop")
	}
	for i, laneCase := range []struct {
		lane port.Lane
		kind port.Kind
		want string
	}{
		{port.LaneOutboundExcel, KindOutboundMessage, queue.OutboundExcelQueue},
		{port.LaneOutboundMedia, KindOutboundMedia, queue.OutboundMediaQueue},
	} {
		e := envelope
		e.Kind = laneCase.kind
		e.SourceRefDigest = Hash("retry-lane", strconv.Itoa(i))
		p, _, acceptErr := repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("retry-lane-receipt", strconv.Itoa(i)), Envelope: e, Lane: laneCase.lane})
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}
		if _, err = pool.Exec(ctx, `UPDATE external_effects SET state='retryable_failed' WHERE id=$1`, parseProjectionID(t, p.ID)); err != nil {
			t.Fatal(err)
		}
		if _, _, err = repo.Retry(ctx, ControlCommand{EffectID: p.ID, ReceiptKey: Hash("retry-control", strconv.Itoa(i)), ActorAdminUserID: 7}); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT queue FROM external_effect_jobs WHERE effect_id=$1 AND generation=2`, parseProjectionID(t, p.ID)).Scan(&lane); err != nil || lane != laneCase.want {
			t.Fatalf("retry lane=%s want=%s err=%v", lane, laneCase.want, err)
		}
	}
	sink := &mediaReconciliationSink{pool: pool, fail: true}
	if err = repo.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	sink.fail = false
	retryAdapter := &mediaRetryOnceAdapter{}
	retryWorker := NewWorker(repo, retryAdapter)
	retryEnvelope := envelope
	retryEnvelope.Kind = KindOutboundMedia
	retryEnvelope.SourceRefDigest = Hash("automatic-media-retry")
	retryProjection, _, err := repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("automatic-media-retry-receipt"), Envelope: retryEnvelope, Lane: port.LaneOutboundMedia})
	if err != nil {
		t.Fatal(err)
	}
	if err = retryWorker.Work(ctx, &river.Job[EffectJobArgs]{JobRow: &rivertype.JobRow{ID: retryProjection.QueueJobID}, Args: EffectJobArgs{EffectID: parseProjectionID(t, retryProjection.ID), Generation: 1}}); err == nil {
		t.Fatal("safe rate-limit rejection did not durably snooze")
	}
	if err = pool.QueryRow(ctx, `SELECT state,attempt_count FROM external_effects WHERE id=$1`, parseProjectionID(t, retryProjection.ID)).Scan(&state, &attempts); err != nil || state != "queued" || attempts != 1 {
		t.Fatalf("automatic retry queued state=%s attempts=%d err=%v", state, attempts, err)
	}
	if err = retryWorker.Work(ctx, &river.Job[EffectJobArgs]{JobRow: &rivertype.JobRow{ID: retryProjection.QueueJobID}, Args: EffectJobArgs{EffectID: parseProjectionID(t, retryProjection.ID), Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT state,attempt_count FROM external_effects WHERE id=$1`, parseProjectionID(t, retryProjection.ID)).Scan(&state, &attempts); err != nil || state != "executed" || attempts != 2 || retryAdapter.calls != 2 {
		t.Fatalf("automatic retry final state=%s attempts=%d calls=%d err=%v", state, attempts, retryAdapter.calls, err)
	}
	var retryLane string
	if err = pool.QueryRow(ctx, `SELECT queue FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, parseProjectionID(t, retryProjection.ID)).Scan(&retryLane); err != nil || retryLane != queue.OutboundMediaQueue {
		t.Fatalf("automatic retry lane=%s err=%v", retryLane, err)
	}
	excelRetryAdapter := &excelRetryOnceAdapter{}
	excelRetryWorker := NewWorker(repo, excelRetryAdapter)
	excelRetryEnvelope := envelope
	excelRetryEnvelope.SourceRefDigest = Hash("automatic-excel-rate-limit-retry")
	excelRetryProjection, _, err := repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("automatic-excel-rate-limit-retry-receipt"), Envelope: excelRetryEnvelope, Lane: port.LaneOutboundExcel})
	if err != nil {
		t.Fatal(err)
	}
	if err = excelRetryWorker.Work(ctx, &river.Job[EffectJobArgs]{JobRow: &rivertype.JobRow{ID: excelRetryProjection.QueueJobID}, Args: EffectJobArgs{EffectID: parseProjectionID(t, excelRetryProjection.ID), Generation: 1}}); err == nil {
		t.Fatal("known add_msg_template rate limit did not durably snooze")
	}
	if err = pool.QueryRow(ctx, `SELECT state,attempt_count FROM external_effects WHERE id=$1`, parseProjectionID(t, excelRetryProjection.ID)).Scan(&state, &attempts); err != nil || state != "queued" || attempts != 1 {
		t.Fatalf("automatic Excel retry queued state=%s attempts=%d err=%v", state, attempts, err)
	}
	if err = excelRetryWorker.Work(ctx, &river.Job[EffectJobArgs]{JobRow: &rivertype.JobRow{ID: excelRetryProjection.QueueJobID}, Args: EffectJobArgs{EffectID: parseProjectionID(t, excelRetryProjection.ID), Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT state,attempt_count FROM external_effects WHERE id=$1`, parseProjectionID(t, excelRetryProjection.ID)).Scan(&state, &attempts); err != nil || state != "executed" || attempts != 2 || excelRetryAdapter.calls != 2 {
		t.Fatalf("automatic Excel retry final state=%s attempts=%d calls=%d err=%v", state, attempts, excelRetryAdapter.calls, err)
	}
	if err = pool.QueryRow(ctx, `SELECT queue FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, parseProjectionID(t, excelRetryProjection.ID)).Scan(&retryLane); err != nil || retryLane != queue.OutboundExcelQueue {
		t.Fatalf("automatic Excel retry lane=%s err=%v", retryLane, err)
	}
	sink.fail = true
	makeUnknownMedia := func(label string) Projection {
		e := envelope
		e.Kind = KindOutboundMedia
		e.SourceRefDigest = Hash("reconcile-media", label)
		p, _, acceptErr := repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("reconcile-media-receipt", label), Envelope: e, Lane: port.LaneOutboundMedia})
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}
		id := parseProjectionID(t, p.ID)
		if _, updateErr := pool.Exec(ctx, `UPDATE external_effects SET state='outcome_unknown',attempt_count=1,lease_fence=1,lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); updateErr != nil {
			t.Fatal(updateErr)
		}
		if _, updateErr := pool.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state,call_attempted,real_external_call_executed,completed_at) VALUES($1,1,1,1,'outcome_unknown',true,true,clock_timestamp())`, id); updateErr != nil {
			t.Fatal(updateErr)
		}
		return p
	}
	unknown := makeUnknownMedia("no-effect")
	baseReconcile := ControlCommand{EffectID: unknown.ID, ReceiptKey: Hash("reconcile-control"), EvidenceDigest: Hash("verified-evidence"), ActorAdminUserID: 7}
	if _, _, err = repo.Reconcile(ctx, baseReconcile); !errors.Is(err, ErrReconcileRequired) {
		t.Fatalf("untyped media reconcile err=%v", err)
	}
	baseReconcile.ReconciliationOutcome = "no_effect"
	if _, _, err = repo.Reconcile(ctx, baseReconcile); err == nil {
		t.Fatal("completion projection failure did not roll back reconciliation")
	}
	if err = pool.QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, parseProjectionID(t, unknown.ID)).Scan(&state); err != nil || state != "outcome_unknown" {
		t.Fatalf("failed atomic reconcile state=%s err=%v", state, err)
	}
	sink.fail = false
	firstReconcile, firstReceipt, err := repo.Reconcile(ctx, baseReconcile)
	if err != nil || firstReconcile.State != StateReconciled {
		t.Fatalf("no-effect reconcile=%+v err=%v", firstReconcile, err)
	}
	replayed, replayReceipt, err := repo.Reconcile(ctx, baseReconcile)
	if err != nil || replayed.ID != firstReconcile.ID || replayReceipt.ID != firstReceipt.ID {
		t.Fatalf("reconcile replay=%+v receipt=%+v err=%v", replayed, replayReceipt, err)
	}
	var projectionState, failureCode, artifactDigest string
	if err = pool.QueryRow(ctx, `SELECT state,failure_code,artifact_digest FROM media_reconciliation_projection WHERE effect_id=$1`, unknown.ID).Scan(&projectionState, &failureCode, &artifactDigest); err != nil || projectionState != "reconciled" || failureCode != "reconciled_no_effect" || artifactDigest != "" {
		t.Fatalf("no-effect projection state=%s failure=%s artifact=%s err=%v", projectionState, failureCode, artifactDigest, err)
	}
	confirmed := makeUnknownMedia("confirmed")
	receiptPayload, _ := json.Marshal(struct {
		MediaID           string    `json:"media_id"`
		ProviderCreatedAt time.Time `json:"provider_created_at"`
	}{"confirmed-media", time.Now().UTC().Add(-time.Minute)})
	artifact := ResultArtifact{Kind: "outbound.material.upload.v1", Payload: receiptPayload}
	artifact.Digest = Hash("external-effect.artifact.v1", artifact.Kind, string(receiptPayload))
	confirmedCommand := ControlCommand{EffectID: confirmed.ID, ReceiptKey: Hash("confirmed-control"), EvidenceDigest: Hash("confirmed-evidence"), ActorAdminUserID: 7, ReconciliationOutcome: "confirmed_effect", ReconciliationArtifact: artifact}
	if _, _, err = repo.Reconcile(ctx, confirmedCommand); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT state,failure_code,artifact_digest FROM media_reconciliation_projection WHERE effect_id=$1`, confirmed.ID).Scan(&projectionState, &failureCode, &artifactDigest); err != nil || projectionState != "executed" || failureCode != "" || artifactDigest != string(artifact.Digest) {
		t.Fatalf("confirmed projection state=%s failure=%s artifact=%s err=%v", projectionState, failureCode, artifactDigest, err)
	}
	contradiction := baseReconcile
	contradiction.ReconciliationOutcome = "confirmed_effect"
	contradiction.ReconciliationArtifact = artifact
	if _, _, err = repo.Reconcile(ctx, contradiction); !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("contradictory reconcile replay err=%v", err)
	}
}
func parseProjectionID(t *testing.T, v string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscanf(v, "eer_%d", &id); err != nil {
		t.Fatal(err)
	}
	return id
}
