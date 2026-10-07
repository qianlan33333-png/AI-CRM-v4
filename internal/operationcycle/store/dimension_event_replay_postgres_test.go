package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// A duplicated local terminal receipt can arrive while the first is waiting
// for its action lock. Both requests must converge on the one durable event.
func TestDimensionPostgreSQLOperationCycleConcurrentCompletionReplays(t *testing.T) {
	pool, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	service := operationapp.NewService(uow, NewRepository(), NewEventJournal(), NewEventJournal())
	const strategyKey, runKey = "dimension.review", "dimension.review.001"
	snapshot := map[string]any{"schema_version": "operation_cycle_snapshot.v1", "strategy_key": strategyKey, "run_key": runKey, "revision": 1, "strategy_version": 1, "status": "active", "title": "Review", "name": "Review", "cron": "weekly", "dot": "#2EA121", "action": "review", "steps": []any{}}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: snapshot, IdempotencyKey: "dimension-event-report", ReporterID: "cycle-runner", ClientID: "cycle-runner-v3"}); err != nil {
		t.Fatal(err)
	}
	definition := executableDefinition()
	definition.Execution.RequiredLocalBindings = []string{strategyKey}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: strategyKey, ExpectedVersion: 1, Title: "Review", Definition: definition, IdempotencyKey: "dimension-event-config", ActorID: "7"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Heartbeat(ctx, operationapp.RunnerHeartbeatCommand{RunnerID: "dimension-runner", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{strategyKey}}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Start(ctx, operationapp.StartCommand{StrategyKey: strategyKey, RunKey: runKey, ActionKey: "start_review", IdempotencyKey: "dimension-event-start", ActorID: "7"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(ctx, "dimension-runner", "operation-cycle-service")
	if err != nil || claimed["claimed"] != true {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	requestID, lease := claimed["request_id"].(string), claimed["lease_token"].(string)
	if _, err = service.RecordActionEvent(ctx, operationapp.ActionEventCommand{RequestID: requestID, EventID: "dimension-event-thread", EventType: "thread_bound", ThreadID: "thread-dimension", LeaseToken: lease}); err != nil {
		t.Fatal(err)
	}
	command := operationapp.ActionEventCommand{RequestID: requestID, EventID: "dimension-event-completed", EventType: "completed", LeaseToken: lease, Result: map[string]any{"outcome": "reviewed"}}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var locked string
	if err = blocker.QueryRow(ctx, `SELECT request_id FROM operation_cycle_action_requests WHERE request_id=$1 FOR UPDATE`, requestID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	results, pids := make(chan error, 2), make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- uow.Within(ctx, func(txctx context.Context) error {
				tx, txErr := platformpostgres.RequireTransaction(txctx)
				if txErr != nil {
					return txErr
				}
				var pid int
				if txErr = tx.QueryRow(txctx, `SELECT pg_backend_pid()`).Scan(&pid); txErr != nil {
					return txErr
				}
				pids <- pid
				result, _, txErr := NewRepository().RecordActionEvent(txctx, command, time.Now().UTC())
				if txErr == nil && result["status"] != "completed" {
					return fmt.Errorf("result=%v", result)
				}
				return txErr
			})
		}()
	}
	pidList := []int{<-pids, <-pids}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid=ANY($1::int[]) AND wait_event_type='Lock'`, pidList).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("duplicate completions did not reach the held action lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	firstErr, secondErr := <-results, <-results
	if firstErr != nil || secondErr != nil {
		t.Fatalf("identical completions must replay: first=%v second=%v", firstErr, secondErr)
	}
	var events int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM operation_cycle_action_request_events WHERE request_id=$1 AND event_id=$2`, requestID, command.EventID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("terminal receipt count=%d, want 1", events)
	}
	drift := command
	drift.Result = map[string]any{"outcome": "different"}
	if _, err = service.RecordActionEvent(ctx, drift); err == nil {
		t.Fatal("same receipt key accepted changed completion payload")
	}
}
