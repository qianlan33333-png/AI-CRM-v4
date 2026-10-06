package externaleffects

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// These guards use only the private River/EER fixture from the abrupt-exit
// contract. They do not start a runtime or execute any adapter. Historical
// attempted/executed rows below are explicit local fixtures, not new calls.
func TestSol61PostgreSQLDiscardedAttemptRecoveryPreservesGuardedFacts(t *testing.T) {
	pool, _, nonce, _ := sol61RiverPrivatePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repository, _ := sol61OriginalJobGuardRepository(t, pool)
	type guardCase struct {
		name string
		edit func(*testing.T, Projection, int64)
	}
	cases := []guardCase{
		{"executed_effect", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE external_effects SET state='executed' WHERE id=$1`, id)
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE external_effect_attempts SET state='executed',receipt_digest=$2,call_attempted=true,real_external_call_executed=true,completed_at=clock_timestamp() WHERE effect_id=$1`, id, Hash("sol61-original-executed", p.ID))
		}},
		{"cancelled_effect", func(t *testing.T, p Projection, id int64) {
			// Cancel the actual queued owner before constructing any attempt.
			// This special case is prepared separately below.
		}},
		{"queued_effect", func(t *testing.T, p Projection, id int64) {}},
		{"completed_original_job", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET state='completed' WHERE id=$1`, p.QueueJobID)
		}},
		{"cancelled_original_job", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET state='cancelled' WHERE id=$1`, p.QueueJobID)
		}},
		{"active_effect_lease", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE external_effects SET lease_expires_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, id)
		}},
		{"foreign_current_generation", func(t *testing.T, p Projection, id int64) {
			// The old native-discarded job belongs to generation 1. A real
			// retry creates generation 2 and its own original job; generation
			// 1 must not be woken for the current attempted effect.
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE external_effects SET state='retryable_failed' WHERE id=$1`, id)
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE external_effect_attempts SET state='retryable_failed',receipt_digest=$2,completed_at=clock_timestamp() WHERE effect_id=$1`, id, Hash("sol61-original-before-call-failure", p.ID))
			if _, _, err := repository.Retry(ctx, ControlCommand{EffectID: p.ID, ReceiptKey: Hash("sol61-original-next-generation", p.ID), ActorAdminUserID: 7}); err != nil {
				t.Fatal(err)
			}
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE external_effects SET state='attempted',attempt_count=2,lease_fence=2,lease_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1 AND generation=2`, id)
			sol61OriginalJobGuardExec(t, ctx, pool, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state) VALUES($1,2,2,2,'attempted')`, id)
		}},
		{"mismatched_effect_args", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET args=$2::jsonb WHERE id=$1`, p.QueueJobID, sol61OriginalJobGuardJSON(t, EffectJobArgs{EffectID: id + 100000, Generation: 1}))
		}},
		{"mismatched_generation_args", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET args=$2::jsonb WHERE id=$1`, p.QueueJobID, sol61OriginalJobGuardJSON(t, EffectJobArgs{EffectID: id, Generation: 2}))
		}},
		{"mismatched_job_kind", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET kind='sol61.unrelated.v1' WHERE id=$1`, p.QueueJobID)
		}},
		{"mismatched_job_queue", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET queue='sol61_unrelated' WHERE id=$1`, p.QueueJobID)
		}},
		{"non_native_latest_error", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET errors=array_append(errors,$2::jsonb) WHERE id=$1`, p.QueueJobID, sol61OriginalJobGuardJSON(t, rivertype.AttemptError{At: time.Now().UTC(), Attempt: 1, Error: "local non-native failure"}))
		}},
		{"native_error_from_previous_attempt", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET attempt=2,max_attempts=2 WHERE id=$1`, p.QueueJobID)
		}},
		{"operator_cancel_metadata", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET metadata=$2::jsonb WHERE id=$1`, p.QueueJobID, sol61OriginalJobGuardJSON(t, map[string]any{"cancel_attempted_at": time.Now().UTC()}))
		}},
		{"malformed_cancel_metadata", func(t *testing.T, p Projection, id int64) {
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET metadata='{"cancel_attempted_at":"invalid-time"}'::jsonb WHERE id=$1`, p.QueueJobID)
		}},
		{"original_attempt_already_executed", func(t *testing.T, p Projection, id int64) {
			// Deliberate inconsistent historical state: maintenance must not
			// overwrite or wake an already executed original attempt.
			sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE external_effect_attempts SET state='executed',receipt_digest=$2,call_attempted=true,real_external_call_executed=true,completed_at=clock_timestamp() WHERE effect_id=$1`, id, Hash("sol61-original-executed-attempt", p.ID))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := sol61OriginalJobGuardAccept(t, ctx, repository, nonce+"/"+tc.name)
			id := parseProjectionID(t, p.ID)
			if tc.name == "cancelled_effect" {
				if _, _, err := repository.Cancel(ctx, ControlCommand{EffectID: p.ID, ReceiptKey: Hash("sol61-original-cancelled", p.ID), ActorAdminUserID: 7}); err != nil {
					t.Fatal(err)
				}
			} else if tc.name != "queued_effect" {
				markAttemptedAfterCrash(t, pool, id, "-1 minute")
			}
			sol61OriginalJobGuardDiscard(t, ctx, pool, p.QueueJobID)
			tc.edit(t, p, id)
			beforeEER, beforeRiver := sol61OriginalJobGuardSnapshot(t, ctx, pool, id)
			if err := repository.RecoverDiscardedAttempts(ctx); err != nil {
				t.Fatal(err)
			}
			afterEER, afterRiver := sol61OriginalJobGuardSnapshot(t, ctx, pool, id)
			if afterEER != beforeEER || afterRiver != beforeRiver {
				t.Fatalf("guarded facts changed: eer_before=%s eer_after=%s river_before=%s river_after=%s", beforeEER, afterEER, beforeRiver, afterRiver)
			}
			t.Logf("guard=%s unchanged_eer=true unchanged_original_jobs=true adapters=0", tc.name)
		})
	}
}

func TestSol61PostgreSQLConcurrentDiscardedAttemptRecoveryRetriesOnlyOriginalJob(t *testing.T) {
	pool, schema, nonce, _ := sol61RiverPrivatePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repository, client := sol61OriginalJobGuardRepository(t, pool)
	p := sol61OriginalJobGuardAccept(t, ctx, repository, nonce+"/concurrent")
	id := parseProjectionID(t, p.ID)
	markAttemptedAfterCrash(t, pool, id, "-1 minute")
	sol61OriginalJobGuardDiscard(t, ctx, pool, p.QueueJobID)
	beforeEER, beforeRiver := sol61OriginalJobGuardSnapshot(t, ctx, pool, id)

	// JobRetryTx must use the provided repository transaction, rather than
	// committing an independent River mutation ahead of owner recovery.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	retried, err := platformjobqueue.RetryRescuedDiscardedJobTx(ctx, client, tx, p.QueueJobID, EffectJobArgs{EffectID: id, Generation: 1}, platformjobqueue.OutboundQueue)
	if err != nil || !retried {
		t.Fatalf("local native retry fixture was not eligible: retried=%t err=%v", retried, err)
	}
	var insideState string
	if err = tx.QueryRow(ctx, `SELECT state FROM river_job WHERE id=$1`, p.QueueJobID).Scan(&insideState); err != nil || insideState != "available" {
		t.Fatalf("retry missing inside original transaction: state=%s err=%v", insideState, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	rolledEER, rolledRiver := sol61OriginalJobGuardSnapshot(t, ctx, pool, id)
	if rolledEER != beforeEER || rolledRiver != beforeRiver {
		t.Fatal("rolling back native retry changed the original job or EER facts")
	}

	// A private audit probe counts committed discarded -> available changes.
	// It lives entirely in this test's schema; no public object is created.
	probeTable := pgx.Identifier{schema, "sol61_original_retry_events"}.Sanitize()
	probeFunction := pgx.Identifier{schema, "sol61_original_retry_probe"}.Sanitize()
	ddl := `CREATE TABLE ` + probeTable + `(job_id BIGINT NOT NULL);
CREATE FUNCTION ` + probeFunction + `() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN INSERT INTO ` + probeTable + `(job_id) VALUES(NEW.id); RETURN NEW; END; $$;
CREATE TRIGGER sol61_original_retry_probe AFTER UPDATE ON river_job FOR EACH ROW
WHEN (OLD.state='discarded' AND NEW.state='available') EXECUTE FUNCTION ` + probeFunction + `();`
	if _, err = pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- repository.RecoverDiscardedAttempts(ctx)
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		select {
		case err = <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("two local recovery scans did not finish within their bound")
		}
	}
	afterEER, _ := sol61OriginalJobGuardSnapshot(t, ctx, pool, id)
	if afterEER != beforeEER {
		t.Fatal("recovery changed original EER effect, attempt, generation, binding, or receipts")
	}
	var state, actualArgs, actualQueue, remainingFacts string
	var jobID int64
	var attempt, maxAttempts, jobs, events int
	if err = pool.QueryRow(ctx, `SELECT id,state,args::text,queue,attempt,max_attempts,(to_jsonb(j)-'state'-'max_attempts'-'finalized_at'-'scheduled_at')::text FROM river_job j WHERE id=$1`, p.QueueJobID).Scan(&jobID, &state, &actualArgs, &actualQueue, &attempt, &maxAttempts, &remainingFacts); err != nil {
		t.Fatal(err)
	}
	var originalFacts string
	if err = pool.QueryRow(ctx, `SELECT (($1::jsonb->0)-'state'-'max_attempts'-'finalized_at'-'scheduled_at')::text`, beforeRiver).Scan(&originalFacts); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM `+probeTable+` WHERE job_id=$1`, p.QueueJobID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	wantArgs := sol61OriginalJobGuardJSON(t, EffectJobArgs{EffectID: id, Generation: 1})
	var argsEqual bool
	if err = pool.QueryRow(ctx, `SELECT $1::jsonb=$2::jsonb`, actualArgs, wantArgs).Scan(&argsEqual); err != nil {
		t.Fatal(err)
	}
	if jobID != p.QueueJobID || state != "available" || !argsEqual || actualQueue != platformjobqueue.OutboundQueue || attempt != 1 || maxAttempts != 2 || jobs != 1 || events != 1 || remainingFacts != originalFacts {
		t.Fatalf("original job was duplicated or changed beyond native retry: id=%d want=%d state=%s args_equal=%t queue=%s attempt=%d max=%d jobs=%d transitions=%d retained=%t", jobID, p.QueueJobID, state, argsEqual, actualQueue, attempt, maxAttempts, jobs, events, remainingFacts == originalFacts)
	}
	// A later scan must also be inert while the original job is available.
	beforeRepeatEER, beforeRepeatRiver := sol61OriginalJobGuardSnapshot(t, ctx, pool, id)
	if err = repository.RecoverDiscardedAttempts(ctx); err != nil {
		t.Fatal(err)
	}
	afterRepeatEER, afterRepeatRiver := sol61OriginalJobGuardSnapshot(t, ctx, pool, id)
	if beforeRepeatEER != afterRepeatEER || beforeRepeatRiver != afterRepeatRiver {
		t.Fatal("repeated scan changed an already restored original job")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM `+probeTable+` WHERE job_id=$1`, p.QueueJobID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("repeat caused another committed native retry: transitions=%d err=%v", events, err)
	}
	t.Logf("original_job=%d committed_native_retries=1 jobs=1 river_attempt=1 max_attempts=2 eer_generation=1 fence=1 attempts=1 facts_retained=true native_retry_rollback=true adapters=0", p.QueueJobID)
}

func sol61OriginalJobGuardRepository(t *testing.T, pool *pgxpool.Pool) (*Repository, *river.Client[pgx.Tx]) {
	t.Helper()
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
	return repository, client
}

func sol61OriginalJobGuardAccept(t *testing.T, ctx context.Context, repository *Repository, label string) Projection {
	t.Helper()
	envelope := envelopeForTest()
	envelope.SourceRefDigest = Hash("sol61-original-job-source", label)
	p, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-original-job-accept", label), Envelope: envelope})
	if err != nil {
		t.Fatal(err)
	}
	if p.QueueJobID < 1 || p.Generation != 1 || p.State != StateQueued || p.Owner != OwnerOutbound || p.Kind != KindOutboundMessage {
		t.Fatalf("invalid private original-job fixture: %+v", p)
	}
	return p
}

func sol61OriginalJobGuardDiscard(t *testing.T, ctx context.Context, pool *pgxpool.Pool, jobID int64) {
	t.Helper()
	last := sol61OriginalJobGuardJSON(t, rivertype.AttemptError{At: time.Now().UTC(), Attempt: 1, Error: "Stuck job rescued by JobRescuer"})
	sol61OriginalJobGuardExec(t, ctx, pool, `UPDATE river_job SET state='discarded',attempt=1,max_attempts=1,attempted_at=clock_timestamp()-interval '2 hours',finalized_at=clock_timestamp(),errors=ARRAY[$2::jsonb] WHERE id=$1`, jobID, last)
}

func sol61OriginalJobGuardJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func sol61OriginalJobGuardExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	tag, err := pool.Exec(ctx, sql, args...)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("private original-job fixture changed %d rows: err=%v", tag.RowsAffected(), err)
	}
}

func sol61OriginalJobGuardSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) (string, string) {
	t.Helper()
	var eer, jobs string
	err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
'effect',(SELECT to_jsonb(e) FROM external_effects e WHERE e.id=$1),
'attempts',COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY a.generation,a.number) FROM external_effect_attempts a WHERE a.effect_id=$1),'[]'::jsonb),
'generations',(SELECT jsonb_agg(to_jsonb(g) ORDER BY g.generation) FROM external_effect_generations g WHERE g.effect_id=$1),
'bindings',(SELECT jsonb_agg(to_jsonb(b) ORDER BY b.generation) FROM external_effect_jobs b WHERE b.effect_id=$1),
'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM external_effect_operation_receipts r WHERE r.effect_id=$1))::text,
(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id)::text FROM river_job j JOIN external_effect_jobs b ON b.river_job_id=j.id WHERE b.effect_id=$1)`, id).Scan(&eer, &jobs)
	if err != nil {
		t.Fatal("snapshot private effect " + strconv.FormatInt(id, 10) + ": " + err.Error())
	}
	return eer, jobs
}
