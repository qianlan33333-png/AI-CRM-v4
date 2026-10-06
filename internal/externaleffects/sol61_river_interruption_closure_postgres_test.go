package externaleffects

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

const sol61RiverAbruptExitCode = 86

type sol61RiverCrashMarker struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Nonce      string `json:"nonce"`
	Schema     string `json:"schema"`
	EffectID   string `json:"effect_id"`
	Attempt    int32  `json:"attempt"`
}

// No network or Provider is used here. This private helper exits after the
// actual EER claim has committed, so neither Work nor River can return a result.
type sol61RiverAbruptAdapter struct {
	marker, nonce, schema string
}

func (a *sol61RiverAbruptAdapter) Execute(_ context.Context, envelope Envelope, attempt Attempt) (AdapterResult, error) {
	if envelope.Owner != OwnerOutbound || envelope.Kind != KindOutboundMessage {
		return AdapterResult{}, ErrInvalid
	}
	executable, err := os.Executable()
	if err != nil {
		return AdapterResult{}, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return AdapterResult{}, err
	}
	file, err := os.OpenFile(a.marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return AdapterResult{}, err
	}
	err = json.NewEncoder(file).Encode(sol61RiverCrashMarker{PID: os.Getpid(), Executable: executable, Nonce: a.nonce, Schema: a.schema, EffectID: attempt.EffectID, Attempt: attempt.Number})
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return AdapterResult{}, errors.Join(err, closeErr)
	}
	os.Exit(sol61RiverAbruptExitCode)
	return AdapterResult{}, errors.New("private abrupt helper unexpectedly returned")
}

// Only the parent below selects this helper and supplies its private schema.
// Ordinary package runs skip this entry without database or process effects.
func TestSol61RiverAbruptPrivateProcessHelper(t *testing.T) {
	if os.Getenv("SOL61_RIVER_PRIVATE_CHILD") != "1" {
		t.Skip("private child-process entry only; not a standalone business test")
	}
	schema := os.Getenv("SOL61_RIVER_PRIVATE_SCHEMA")
	nonce := os.Getenv("SOL61_RIVER_PRIVATE_NONCE")
	marker := os.Getenv("SOL61_RIVER_PRIVATE_MARKER")
	if schema != "sol61_river_"+nonce || len(nonce) != 32 || !filepath.IsAbs(marker) {
		t.Fatal("missing private abrupt helper identity")
	}
	config := sol61RiverLoopbackConfig(t)
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	worker := NewWorker(nil, &sol61RiverAbruptAdapter{marker: marker, nonce: nonce, schema: schema})
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[EffectJobArgs](workers, worker); err != nil {
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
	if err = river.AddWorkerSafely[RecoveryJobArgs](workers, NewRecoveryWorker(repository)); err != nil {
		t.Fatal(err)
	}
	runner, err := platformjobqueue.NewRuntimeWithPeriodic(pool, workers, []*river.PeriodicJob{RecoveryPeriodicJob()})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	t.Fatal("private abrupt helper runtime returned")
}

type sol61RiverObservedWorker struct {
	*Worker
	calls atomic.Int32
}

func (w *sol61RiverObservedWorker) Work(ctx context.Context, job *river.Job[EffectJobArgs]) error {
	w.calls.Add(1)
	return w.Worker.Work(ctx, job)
}

type sol61RiverNoNetworkAdapter struct{ calls atomic.Int32 }

func (a *sol61RiverNoNetworkAdapter) Execute(context.Context, Envelope, Attempt) (AdapterResult, error) {
	a.calls.Add(1)
	return AdapterResult{}, errors.New("unexpected local adapter replay")
}

type sol61RiverOwnerSink struct{}

func (*sol61RiverOwnerSink) CompleteEffect(ctx context.Context, effectID string, envelope Envelope, attempt Attempt, result AdapterResult) error {
	if envelope.Owner != OwnerOutbound || envelope.Kind != KindOutboundMessage {
		return ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sol61_river_owner_outcomes(effect_ref,state,attempt) VALUES($1,$2,$3)`, effectID, result.Completion, attempt.Number)
	return err
}

// One finite non-payment recovery contract: the original attempted effect must
// reach unknown after a private worker exits on River's last allowed attempt.
// Native rescue and the actual registered recovery periodic job must retain the
// original binding and let RunAttempt project unknown without another Execute.
func TestSol61PostgreSQLFinalRiverAttemptAbruptExitPreservesRecovery(t *testing.T) {
	pool, schema, nonce, cleanupSafe := sol61RiverPrivatePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	adapter := &sol61RiverNoNetworkAdapter{}
	worker := &sol61RiverObservedWorker{Worker: NewWorker(nil, adapter)}
	workers := river.NewWorkers()
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
	if err = river.AddWorkerSafely[RecoveryJobArgs](workers, NewRecoveryWorker(repository)); err != nil {
		t.Fatal(err)
	}
	if err = repository.SetCompletionSink(&sol61RiverOwnerSink{}); err != nil {
		t.Fatal(err)
	}
	projection, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("sol61-private-river-closure", nonce), Envelope: envelopeForTest()})
	if err != nil {
		t.Fatal(err)
	}
	if tag, updateErr := pool.Exec(ctx, `UPDATE river_job SET max_attempts=1 WHERE id=$1 AND state='available'`, projection.QueueJobID); updateErr != nil || tag.RowsAffected() != 1 {
		t.Fatalf("prepare private final River attempt rows=%d err=%v", tag.RowsAffected(), updateErr)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(t.TempDir(), "private-abrupt-marker.json")
	command := exec.Command(executable, "-test.run=^TestSol61RiverAbruptPrivateProcessHelper$", "-test.timeout=20s")
	command.Env = append(os.Environ(), "SOL61_RIVER_PRIVATE_CHILD=1", "SOL61_RIVER_PRIVATE_SCHEMA="+schema, "SOL61_RIVER_PRIVATE_NONCE="+nonce, "SOL61_RIVER_PRIVATE_MARKER="+markerPath)
	output, childErr := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(childErr, &exitError) || !command.ProcessState.Exited() || command.ProcessState.ExitCode() != sol61RiverAbruptExitCode {
		t.Fatalf("private helper did not self-exit with expected code: err=%v output=%s", childErr, output)
	}
	markerRaw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker sol61RiverCrashMarker
	if err = json.Unmarshal(markerRaw, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.PID <= 1 || marker.PID == os.Getpid() || marker.PID != command.Process.Pid || marker.PID != command.ProcessState.Pid() || marker.Executable != executable || marker.Nonce != nonce || marker.Schema != schema || marker.EffectID != projection.ID || marker.Attempt != 1 {
		t.Fatalf("private child identity mismatch: marker=%+v parent_pid=%d child_pid=%d executable=%s", marker, os.Getpid(), command.Process.Pid, executable)
	}
	t.Logf("private_self_exit pid=%d parent_pid=%d executable=%s code=%d schema=%s effect=%s job=%d", marker.PID, os.Getpid(), marker.Executable, command.ProcessState.ExitCode(), schema, projection.ID, projection.QueueJobID)

	var jobState, effectState, attemptState string
	var riverAttempt, maxAttempts, effectAttempts int
	var generation, fence int64
	if err = pool.QueryRow(ctx, `SELECT j.state,j.attempt,j.max_attempts,e.state,e.attempt_count,e.generation,e.lease_fence,a.state FROM river_job j JOIN external_effect_jobs ej ON ej.river_job_id=j.id JOIN external_effects e ON e.id=ej.effect_id JOIN external_effect_attempts a ON a.effect_id=e.id AND a.generation=e.generation WHERE j.id=$1`, projection.QueueJobID).Scan(&jobState, &riverAttempt, &maxAttempts, &effectState, &effectAttempts, &generation, &fence, &attemptState); err != nil || jobState != "running" || riverAttempt != 1 || maxAttempts != 1 || effectState != "attempted" || effectAttempts != 1 || generation != 1 || fence != 1 || attemptState != "attempted" {
		t.Fatalf("private abrupt exit did not preserve original committed claim: river=%s attempt=%d max=%d effect=%s attempts=%d generation=%d fence=%d original=%s err=%v", jobState, riverAttempt, maxAttempts, effectState, effectAttempts, generation, fence, attemptState, err)
	}
	// Only this fixture's exact dead job and lease are aged. The real runtime's
	// default one-hour rescue and worker's 4m30s timeout remain unchanged.
	if tag, updateErr := pool.Exec(ctx, `UPDATE river_job SET attempted_at=clock_timestamp()-interval '2 hours' WHERE id=$1 AND state='running' AND attempt=1 AND max_attempts=1`, projection.QueueJobID); updateErr != nil || tag.RowsAffected() != 1 {
		t.Fatalf("age private dead River job rows=%d err=%v", tag.RowsAffected(), updateErr)
	}
	if tag, updateErr := pool.Exec(ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1 AND state='attempted' AND generation=1 AND lease_fence=1`, parseProjectionID(t, projection.ID)); updateErr != nil || tag.RowsAffected() != 1 {
		t.Fatalf("age private original EER lease rows=%d err=%v", tag.RowsAffected(), updateErr)
	}
	runner, err := platformjobqueue.NewRuntimeWithPeriodic(pool, workers, []*river.PeriodicJob{RecoveryPeriodicJob()})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	cleanupSafe.Store(false)
	go func() { done <- runner.Run(runCtx) }()
	defer func() {
		stop()
		select {
		case stopErr := <-done:
			cleanupSafe.Store(true)
			if stopErr != nil {
				t.Errorf("stop private recovery River runtime: %v", stopErr)
			} else {
				t.Log("private_recovery_runtime_stopped=true")
			}
		case <-time.After(20 * time.Second):
			t.Error("private recovery River runtime did not stop; fixture is retained")
		}
	}()
	var rescued bool
	for {
		if err = pool.QueryRow(ctx, `SELECT j.state,e.state,EXISTS(SELECT 1 FROM unnest(j.errors) AS error_data WHERE error_data->>'error'='Stuck job rescued by JobRescuer') FROM river_job j JOIN external_effect_jobs ej ON ej.river_job_id=j.id JOIN external_effects e ON e.id=ej.effect_id WHERE j.id=$1`, projection.QueueJobID).Scan(&jobState, &effectState, &rescued); err != nil {
			t.Fatal(err)
		}
		if jobState == "completed" && effectState == "outcome_unknown" && rescued {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("periodic recovery did not close final dead attempt on its original job: river=%s effect=%s rescuer=%t err=%v", jobState, effectState, rescued, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	var ownerRows int
	var expired bool
	if err = pool.QueryRow(ctx, `SELECT e.state,e.attempt_count,e.generation,e.lease_fence,e.lease_expires_at<=clock_timestamp(),a.state,(SELECT count(*) FROM sol61_river_owner_outcomes) FROM external_effects e JOIN external_effect_attempts a ON a.effect_id=e.id AND a.generation=e.generation WHERE e.id=$1`, parseProjectionID(t, projection.ID)).Scan(&effectState, &effectAttempts, &generation, &fence, &expired, &attemptState, &ownerRows); err != nil {
		t.Fatal(err)
	}
	if worker.calls.Load() != 1 || adapter.calls.Load() != 0 || effectAttempts != 1 || generation != 1 || fence != 1 || !expired {
		t.Fatalf("periodic recovery did not replay only the original executor or replaced its EER claim: worker_calls=%d adapter_calls=%d attempts=%d generation=%d fence=%d expired=%t", worker.calls.Load(), adapter.calls.Load(), effectAttempts, generation, fence, expired)
	}
	if _, _, err = repository.Retry(ctx, ControlCommand{EffectID: projection.ID, ReceiptKey: Hash("sol61-private-abrupt-generic-retry", nonce), ActorAdminUserID: 7}); !errors.Is(err, ErrTransition) {
		t.Fatalf("original attempted/unknown effect allowed replacement generic retry: %v", err)
	}
	var bindingJobID int64
	var generationRows, attemptRows, bindingRows int
	if err = pool.QueryRow(ctx, `SELECT ej.river_job_id,(SELECT count(*) FROM external_effect_generations WHERE effect_id=e.id),(SELECT count(*) FROM external_effect_attempts WHERE effect_id=e.id),(SELECT count(*) FROM external_effect_jobs WHERE effect_id=e.id) FROM external_effects e JOIN external_effect_jobs ej ON ej.effect_id=e.id AND ej.generation=e.generation WHERE e.id=$1`, parseProjectionID(t, projection.ID)).Scan(&bindingJobID, &generationRows, &attemptRows, &bindingRows); err != nil || bindingJobID != projection.QueueJobID || generationRows != 1 || attemptRows != 1 || bindingRows != 1 {
		t.Fatalf("periodic recovery replaced original binding/history: job=%d want=%d generations=%d attempts=%d bindings=%d err=%v", bindingJobID, projection.QueueJobID, generationRows, attemptRows, bindingRows, err)
	}
	t.Logf("native_rescue_periodic_recovery river=%s rescuer=%t effect=%s original_attempt=%s effect_attempts=%d generation=%d fence=%d lease_expired=%t worker_calls=%d adapter_calls=%d owner_rows=%d generic_retry=state_conflict", jobState, rescued, effectState, attemptState, effectAttempts, generation, fence, expired, worker.calls.Load(), adapter.calls.Load(), ownerRows)
	if effectState != "outcome_unknown" || attemptState != "outcome_unknown" || ownerRows != 1 {
		t.Fatalf("periodic recovery failed original expired EER claim: job=%d river=%s effect=%s original_attempt=%s effect_attempts=%d worker_calls=%d adapter_calls=%d owner_rows=%d", projection.QueueJobID, jobState, effectState, attemptState, effectAttempts, worker.calls.Load(), adapter.calls.Load(), ownerRows)
	}
}

func sol61RiverLoopbackConfig(t *testing.T) *pgxpool.Config {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Fatal("isolated local database configuration is required")
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid isolated local database configuration")
	}
	if config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.Port != 55476 || config.ConnConfig.Database != "aicrm_test_sol61_async" || len(config.ConnConfig.Fallbacks) != 0 {
		t.Fatal("River interruption closure requires the authorized dedicated loopback test database with no fallback")
	}
	return config
}

// The shared EER fixture loads unrelated domain migrations and a public
// function. This small fixture uses only River and EER migration 0005, with
// that audit trigger function qualified to its own schema before execution.
func sol61RiverPrivatePool(t *testing.T) (*pgxpool.Pool, string, string, *atomic.Bool) {
	t.Helper()
	config := sol61RiverLoopbackConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err = admin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version); err != nil || version < 160000 || version >= 170000 {
		admin.Close()
		t.Fatalf("isolated PostgreSQL 16 required: version=%d err=%v", version, err)
	}
	bytes := make([]byte, 16)
	if _, err = rand.Read(bytes); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(bytes)
	schema := "sol61_river_" + nonce
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	cleanupSafe := &atomic.Bool{}
	cleanupSafe.Store(true)
	t.Cleanup(func() {
		if !cleanupSafe.Load() {
			t.Logf("warning: private River runtime stop unconfirmed; preserving pool and schema %s", schema)
			admin.Close()
			return
		}
		if pool != nil {
			pool.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, cleanupErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); cleanupErr != nil {
			t.Logf("warning: private River fixture cleanup failed: %v", cleanupErr)
		} else {
			t.Logf("private_fixture_schema_dropped=%s", schema)
		}
		admin.Close()
	})
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	pool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate non-payment EER migration")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0005_external_effects.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ddl := strings.ReplaceAll(string(content), "public.external_effects_reject_delete", pgx.Identifier{schema, "external_effects_reject_delete"}.Sanitize())
	if strings.Contains(ddl, "public.") {
		t.Fatal("fixture migration must not write public objects")
	}
	if _, err = pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE external_effects ADD COLUMN delivery_lane TEXT NOT NULL DEFAULT '' CHECK(delivery_lane IN ('','outbound_excel','outbound_media')); CREATE TABLE sol61_river_owner_outcomes(effect_ref TEXT PRIMARY KEY,state TEXT NOT NULL,attempt INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	var actualSchema string
	if err = pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&actualSchema); err != nil || actualSchema != schema {
		t.Fatalf("private schema binding mismatch: schema=%s err=%v", actualSchema, err)
	}
	t.Logf("private_fixture PostgreSQL=%d schema=%s migrations=River+0005 no_public_function_write=true", version, schema)
	return pool, schema, nonce, cleanupSafe
}
