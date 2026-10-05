package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// The API's active-action check and the database partial unique index must
// jointly prevent two different idempotency keys from opening concurrent
// actions for one strategy. This test uses the real Owner store/UoW only; it
// does not start a local runner or contact Codex or a Provider.
func TestPostgreSQLOperationCycleAllowsOnlyOneConcurrentActiveAction(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	service := operationapp.NewService(uow, NewRepository(), NewEventJournal(), NewEventJournal())
	const strategyKey = "single-use.review"
	const runKey = "single-use.review.001"
	snapshot := map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": strategyKey, "run_key": runKey,
		"revision": 1, "strategy_version": 1, "status": "active", "title": "Single-use review",
		"name": "Single-use review", "cron": "weekly", "dot": "#2EA121", "action": "review", "steps": []any{},
	}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: snapshot, IdempotencyKey: "single-use-report", ReporterID: "cycle-runner", ClientID: "cycle-runner-v3"}); err != nil {
		t.Fatal(err)
	}
	definition := executableDefinition()
	definition.Execution.RequiredLocalBindings = []string{strategyKey}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: strategyKey, ExpectedVersion: 1, Title: "Single-use review", Definition: definition, IdempotencyKey: "single-use-config", ActorID: "7"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Heartbeat(ctx, operationapp.RunnerHeartbeatCommand{RunnerID: "single-use-runner", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{strategyKey}}); err != nil {
		t.Fatal(err)
	}

	commands := []operationapp.StartCommand{
		{StrategyKey: strategyKey, RunKey: runKey, ActionKey: "start_review", IdempotencyKey: "single-use-start-a", ActorID: "7"},
		{StrategyKey: strategyKey, RunKey: runKey, ActionKey: "start_review", IdempotencyKey: "single-use-start-b", ActorID: "7"},
	}
	start := make(chan struct{})
	type result struct {
		command operationapp.StartCommand
		value   map[string]any
		err     error
	}
	results := make(chan result, len(commands))
	var wait sync.WaitGroup
	for _, command := range commands {
		command := command
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			value, startErr := service.Start(ctx, command)
			results <- result{command: command, value: value, err: startErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var winner result
	var accepted, conflicts int
	for attempt := range results {
		switch {
		case attempt.err == nil:
			accepted++
			winner = attempt
		case errors.Is(attempt.err, operationapp.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent action start failed unexpectedly: command=%+v err=%v", attempt.command, attempt.err)
		}
	}
	if accepted != 1 || conflicts != 1 || winner.value["request_id"] == nil {
		t.Fatalf("concurrent starts accepted=%d conflicts=%d winner=%#v", accepted, conflicts, winner.value)
	}
	var actionCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM operation_cycle_action_requests WHERE strategy_key=$1 AND status IN ('queued','claimed','thread_bound','turn_started')`, strategyKey).Scan(&actionCount); err != nil {
		t.Fatal(err)
	}
	if actionCount != 1 {
		t.Fatalf("active action count=%d, want exactly one", actionCount)
	}
	replay, err := service.Start(ctx, winner.command)
	if err != nil || replay["request_id"] != winner.value["request_id"] {
		t.Fatalf("winner receipt replay=%#v initial=%#v err=%v", replay, winner.value, err)
	}
	var allActions int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM operation_cycle_action_requests WHERE strategy_key=$1`, strategyKey).Scan(&allActions); err != nil {
		t.Fatal(err)
	}
	if allActions != 1 {
		t.Fatalf("action rows after exact winning-key replay=%d, want one", allActions)
	}
}
