package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func executableDefinition() operationapp.StrategyDefinition {
	return operationapp.StrategyDefinition{
		Schedule: "每周一 09:00", IndicatorColor: "#2EA121", PrimaryAction: "start_review",
		Stages:    []operationapp.StrategyStage{{Key: "review", Label: "复盘", Color: "#2EA121", State: "current"}},
		Execution: &operationapp.StrategyExecution{Title: "冻结复盘", Objective: "完成冻结复盘", CodexPrompt: "仅处理冻结上下文", RequiredLocalBindings: []string{"weekly.review"}, ResultSchema: map[string]any{"type": "object"}},
	}
}

// The cycle domain is local-only: this Journey verifies report persistence,
// receipt replay/drift, runner lease and terminal evidence on a real PG16
// schema. No identity, recipient or Provider table is used by the commands.
func TestPostgreSQLOperationCycleReportToTerminalActionJourney(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	snapshot := map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "weekly.review", "run_key": "weekly.review.001",
		"revision": 1, "strategy_version": 1, "status": "active", "title": "每周复盘",
		"name": "每周复盘", "cron": "每周一 09:00", "dot": "#2EA121", "action": "开始复盘",
		"steps": []any{map[string]any{"label": "复盘", "color": "#2EA121", "dim": false}},
	}
	report := operationapp.ReportCommand{Snapshot: snapshot, IdempotencyKey: "operation-cycle-report-key-0001", ReporterID: "cycle-runner", ClientID: "cycle-runner-v3"}
	if _, err = service.Report(ctx, report); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: "weekly.review", ExpectedVersion: 1, Title: "每周复盘", Definition: executableDefinition(), IdempotencyKey: "operation-cycle-execution-config", ActorID: "7"}); err != nil {
		t.Fatalf("configure executable strategy: %v", err)
	}
	if _, err = service.Report(ctx, report); err != nil {
		t.Fatalf("report replay: %v", err)
	}
	drift := operationapp.ReportCommand{Snapshot: map[string]any{"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "weekly.review", "run_key": "weekly.review.002"}, IdempotencyKey: report.IdempotencyKey, ReporterID: report.ReporterID, ClientID: report.ClientID}
	if _, err = service.Report(ctx, drift); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("report payload drift=%v, want conflict", err)
	}

	strategies, err := service.ListStrategies(ctx, 10, 0)
	if err != nil || len(strategies["items"].([]map[string]any)) != 1 {
		t.Fatalf("persisted strategies=%#v err=%v", strategies, err)
	}
	if _, err = service.Heartbeat(ctx, operationapp.RunnerHeartbeatCommand{RunnerID: "cycle-runner", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{"weekly.review"}}); err != nil {
		t.Fatalf("runner heartbeat: %v", err)
	}
	start := operationapp.StartCommand{StrategyKey: "weekly.review", RunKey: "weekly.review.001", ActionKey: "start_review", IdempotencyKey: "operation-cycle-start-key-0001", ActorID: "7"}
	queued, err := service.Start(ctx, start)
	if err != nil || queued["status"] != "queued" {
		t.Fatalf("queue action=%#v err=%v", queued, err)
	}
	if replayed, replayErr := service.Start(ctx, start); replayErr != nil || replayed["request_id"] != queued["request_id"] {
		t.Fatalf("action replay=%#v err=%v", replayed, replayErr)
	}
	start.RunKey = "weekly.review.002"
	if _, err = service.Start(ctx, start); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("action payload drift=%v, want conflict", err)
	}
	claimed, err := service.Claim(ctx, "cycle-runner", "operation-cycle-service")
	if err != nil || claimed["claimed"] != true {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	requestID, lease := claimed["request_id"].(string), claimed["lease_token"].(string)
	for _, event := range []operationapp.ActionEventCommand{
		{RequestID: requestID, EventID: "cycle-thread-bound-0001", EventType: "thread_bound", LeaseToken: lease, ThreadID: "thread-1"},
		{RequestID: requestID, EventID: "cycle-turn-started-0001", EventType: "turn_started", LeaseToken: lease, ThreadID: "thread-1", TurnID: "turn-1"},
		{RequestID: requestID, EventID: "cycle-completed-0001", EventType: "completed", LeaseToken: lease, Result: map[string]any{"outcome": "outcome_unknown"}},
	} {
		if _, err = service.RecordActionEvent(ctx, event); err != nil {
			t.Fatalf("record %s: %v", event.EventType, err)
		}
	}
	result, err := service.GetActionResult(ctx, requestID)
	if err != nil || result["status"] != "completed" {
		t.Fatalf("terminal action=%#v err=%v", result, err)
	}
	var reports, actions, events, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM operation_cycle_report_receipts),
		(SELECT count(*) FROM operation_cycle_action_requests),
		(SELECT count(*) FROM operation_cycle_action_request_events),
		(SELECT count(*) FROM audit_events WHERE resource_type='operation_cycle'),
		(SELECT count(*) FROM outbox_events WHERE aggregate_type='operation_cycle')`).Scan(&reports, &actions, &events, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if reports != 1 || actions != 1 || events != 3 || audits != 8 || outbox != 8 {
		t.Fatalf("local facts reports/actions/events/audits/outbox=%d/%d/%d/%d/%d", reports, actions, events, audits, outbox)
	}
}

// Runner selection uses the immutable execution's local capabilities. The
// strategy key identifies business configuration and is deliberately unrelated
// to a reviewed local binding such as an Excel workspace.
func TestPostgreSQLOperationCycleStartSelectsRunnerByEveryRequiredBinding(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	strategyKey := "binding.selection.review"
	runKey := strategyKey + ".001"
	snapshot := map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": strategyKey, "run_key": runKey,
		"revision": 1, "strategy_version": 1, "status": "active", "title": "binding selection",
		"name": "binding selection", "cron": "weekly", "dot": "#2EA121", "action": "review", "steps": []any{},
	}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: snapshot, IdempotencyKey: "binding-selection-report", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatal(err)
	}
	definition := executableDefinition()
	definition.Execution.RequiredLocalBindings = []string{"excel_workspace", "review_templates"}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: strategyKey, ExpectedVersion: 1, Title: "binding selection", Definition: definition, IdempotencyKey: "binding-selection-config", ActorID: "7"}); err != nil {
		t.Fatal(err)
	}
	for _, heartbeat := range []operationapp.RunnerHeartbeatCommand{
		{RunnerID: "runner-missing-binding", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{strategyKey, "excel_workspace"}},
		{RunnerID: "runner-with-bindings", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{"review_templates", "excel_workspace"}},
	} {
		if _, err = service.Heartbeat(ctx, heartbeat); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := service.Start(ctx, operationapp.StartCommand{StrategyKey: strategyKey, RunKey: runKey, ActionKey: "start_review", IdempotencyKey: "binding-selection-start", ActorID: "7"})
	if err != nil {
		t.Fatalf("start with exactly one capable runner: %v", err)
	}
	if missing, claimErr := service.Claim(ctx, "runner-missing-binding", "operation-cycle-service"); claimErr != nil || missing["claimed"] != false {
		t.Fatalf("runner missing a frozen binding claimed action: %#v err=%v", missing, claimErr)
	}
	claimed, err := service.Claim(ctx, "runner-with-bindings", "operation-cycle-service")
	if err != nil || claimed["claimed"] != true || claimed["request_id"] != queued["request_id"] {
		t.Fatalf("runner with all frozen bindings did not receive action: %#v err=%v", claimed, err)
	}
}

// The admin Journey is entirely local: typed strategy definition changes,
// immutable versions, receipts, audit and outbox commit in one PostgreSQL UoW.
// A newly constructed service proves that a browser refresh reads persisted
// state rather than an in-process projection.
func TestPostgreSQLOperationCycleAdminJourneyPersistsImmutableHistory(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	newService := func() *operationapp.Service {
		return operationapp.NewService(uow, NewRepository(), NewEventJournal(), NewEventJournal())
	}
	service := newService()
	definition := operationapp.StrategyDefinition{
		Schedule: "每周一 09:00", IndicatorColor: "#2EA121", PrimaryAction: "start_review",
		Stages: []operationapp.StrategyStage{{Key: "prepare", Label: "准备", Color: "#3370FF", State: "completed"}, {Key: "retro", Label: "复盘", Color: "#2EA121", State: "current"}},
	}
	create := operationapp.CreateStrategyCommand{StrategyKey: "admin.weekly.review", Title: "每周复盘", Definition: definition, IdempotencyKey: "admin-create-weekly-review", ActorID: "7"}
	created, err := service.CreateStrategy(ctx, create)
	if err != nil || created["status"] != "draft" || created["version"] != int32(1) {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	replayed, err := service.CreateStrategy(ctx, create)
	if err != nil || replayed["strategy_key"] != create.StrategyKey {
		t.Fatalf("create replay=%#v err=%v", replayed, err)
	}
	drift := create
	drift.Title = "漂移标题"
	if _, err = service.CreateStrategy(ctx, drift); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("create payload drift=%v, want conflict", err)
	}
	definition.Schedule = "每周二 10:00"
	updated, err := service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 1, Title: "每周复盘 v2", Definition: definition, IdempotencyKey: "admin-update-weekly-review", ActorID: "7"})
	if err != nil || updated["version"] != int32(2) {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 1, Title: "陈旧更新", Definition: definition, IdempotencyKey: "admin-stale-update", ActorID: "7"}); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("stale CAS=%v, want conflict", err)
	}
	active, err := service.TransitionStrategy(ctx, operationapp.TransitionStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 2, Status: "active", IdempotencyKey: "admin-activate-weekly-review", ActorID: "7"})
	if err != nil || active["version"] != int32(3) {
		t.Fatalf("activate=%#v err=%v", active, err)
	}
	paused, err := service.TransitionStrategy(ctx, operationapp.TransitionStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 3, Status: "paused", IdempotencyKey: "admin-pause-weekly-review", ActorID: "7"})
	if err != nil || paused["version"] != int32(4) {
		t.Fatalf("pause=%#v err=%v", paused, err)
	}
	concurrent := []operationapp.UpdateStrategyCommand{
		{StrategyKey: create.StrategyKey, ExpectedVersion: 4, Title: "并发更新 A", Definition: definition, IdempotencyKey: "admin-concurrent-a", ActorID: "7"},
		{StrategyKey: create.StrategyKey, ExpectedVersion: 4, Title: "并发更新 B", Definition: definition, IdempotencyKey: "admin-concurrent-b", ActorID: "7"},
	}
	results := make(chan error, len(concurrent))
	for _, command := range concurrent {
		command := command
		go func() {
			_, updateErr := service.UpdateStrategy(ctx, command)
			results <- updateErr
		}()
	}
	successes, conflicts := 0, 0
	for range concurrent {
		switch updateErr := <-results; {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, operationapp.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent CAS error=%v", updateErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent CAS successes/conflicts=%d/%d, want 1/1", successes, conflicts)
	}

	refreshed := newService()
	persisted, err := refreshed.GetStrategy(ctx, create.StrategyKey)
	if err != nil || persisted["status"] != "paused" || persisted["version"] != int32(5) {
		t.Fatalf("refreshed strategy=%#v err=%v", persisted, err)
	}
	history, err := refreshed.ListStrategyVersions(ctx, create.StrategyKey, 10, 0)
	if err != nil {
		t.Fatalf("list immutable strategy history: %v", err)
	}
	versions, ok := history["items"].([]map[string]any)
	if !ok || len(versions) != 5 || versions[0]["version"] != int32(5) || versions[4]["version"] != int32(1) {
		t.Fatalf("immutable strategy history=%#v err=%v", history, err)
	}
	var receipts, strategyVersions, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM operation_cycle_admin_receipts),
		(SELECT count(*) FROM operation_cycle_strategy_versions WHERE strategy_key=$1),
		(SELECT count(*) FROM audit_events WHERE resource_type='operation_cycle'),
		(SELECT count(*) FROM outbox_events WHERE aggregate_type='operation_cycle')`, create.StrategyKey).Scan(&receipts, &strategyVersions, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if receipts != 5 || strategyVersions != 5 || audits != 5 || outbox != 5 {
		t.Fatalf("receipts/versions/audits/outbox=%d/%d/%d/%d, want 5/5/5/5", receipts, strategyVersions, audits, outbox)
	}
	var actorType, actorID, resourceID string
	if err = native.QueryRow(ctx, `SELECT actor_type, actor_id, resource_id
		FROM audit_events
		WHERE resource_type='operation_cycle' AND actor_id=$1
		ORDER BY id LIMIT 1`, create.ActorID).Scan(&actorType, &actorID, &resourceID); err != nil {
		t.Fatal(err)
	}
	if actorType != "admin" || actorID != create.ActorID || resourceID != create.StrategyKey {
		t.Fatalf("admin audit attribution=%q/%q/%q, want admin/%s/%s", actorType, actorID, resourceID, create.ActorID, create.StrategyKey)
	}
	if _, err = native.Exec(ctx, `UPDATE operation_cycle_strategy_versions SET title='mutated' WHERE strategy_key=$1 AND version=1`, create.StrategyKey); err == nil {
		t.Fatal("immutable strategy version accepted direct mutation")
	}
}

func TestPostgreSQLOperationCycleRunHistoryRejectsDestructiveOverwrite(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	base := map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "runner.weekly.review", "run_key": "runner.weekly.review.001",
		"revision": 1, "strategy_version": 1, "status": "active", "title": "运行周期", "name": "运行周期", "cron": "每周一", "dot": "#2EA121", "action": "查看进度",
		"steps": []any{map[string]any{"label": "复盘", "color": "#2EA121", "dim": false}},
	}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: base, IdempotencyKey: "history-report-1", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatal(err)
	}
	overwrite := make(map[string]any, len(base))
	for key, value := range base {
		overwrite[key] = value
	}
	overwrite["title"] = "相同版本被覆盖"
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: overwrite, IdempotencyKey: "history-report-overwrite", ReporterID: "cycle-runner", ClientID: "v3-runner"}); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("destructive overwrite=%v, want conflict", err)
	}
	second := make(map[string]any, len(base))
	for key, value := range base {
		second[key] = value
	}
	second["revision"] = 2
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: second, IdempotencyKey: "history-report-2", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatalf("revision 2: %v", err)
	}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: base, IdempotencyKey: "history-report-old-replay", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatalf("older immutable report replay: %v", err)
	}
	history, err := service.ListRunVersions(ctx, "runner.weekly.review.001", 10, 0)
	if err != nil {
		t.Fatalf("list immutable run history: %v", err)
	}
	versions, ok := history["items"].([]map[string]any)
	if !ok || len(versions) != 2 || versions[0]["snapshot_revision"] != int32(2) || versions[1]["snapshot_revision"] != int32(1) {
		t.Fatalf("immutable run history=%#v err=%v", history, err)
	}
	persisted, err := service.GetRun(ctx, "runner.weekly.review.001")
	if err != nil || persisted["snapshot_revision"] != int32(2) {
		t.Fatalf("latest run projection=%#v err=%v", persisted, err)
	}
	strategy, err := service.GetStrategy(ctx, "runner.weekly.review")
	strategySnapshot, _ := strategy["snapshot"].(map[string]any)
	if err != nil || strategySnapshot["revision"] != float64(2) {
		t.Fatalf("latest strategy projection regressed=%#v err=%v", strategy, err)
	}
}

func TestPostgreSQLOperationCycleRunOrdinalSurvivesConcurrentInsertAndResort(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	report := func(strategyKey, runKey, idempotencyKey string) error {
		_, reportErr := service.Report(ctx, operationapp.ReportCommand{
			IdempotencyKey: idempotencyKey, ReporterID: "cycle-runner", ClientID: "v3-runner",
			Snapshot: map[string]any{
				"schema_version": "operation_cycle_snapshot.v1", "strategy_key": strategyKey, "run_key": runKey,
				"revision": 1, "strategy_version": 1, "status": "active", "title": strategyKey, "name": strategyKey,
				"cron": "每周一", "dot": "#2EA121", "action": "查看进度", "steps": []any{},
			},
		})
		return reportErr
	}
	if err = report("stable.review", "stable.review.001", "stable-report"); err != nil {
		t.Fatal(err)
	}
	listed, err := service.ListStrategies(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	items, ok := listed["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("initial strategies=%#v", listed)
	}
	ordinal, ok := items[0]["run_ordinal"].(int32)
	if !ok || ordinal < 1 {
		t.Fatalf("stable run ordinal=%#v", items[0]["run_ordinal"])
	}

	errorsByInsert := make(chan error, 2)
	for _, incoming := range []struct{ strategy, run, key string }{
		{strategy: "concurrent.alpha", run: "concurrent.alpha.001", key: "concurrent-alpha-report"},
		{strategy: "concurrent.beta", run: "concurrent.beta.001", key: "concurrent-beta-report"},
	} {
		incoming := incoming
		go func() { errorsByInsert <- report(incoming.strategy, incoming.run, incoming.key) }()
	}
	for range 2 {
		if insertErr := <-errorsByInsert; insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	listed, err = service.ListStrategies(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	items, ok = listed["items"].([]map[string]any)
	if !ok || len(items) != 3 || items[0]["strategy_key"] == "stable.review" {
		t.Fatalf("strategy ordering did not change after concurrent inserts: %#v", listed)
	}
	resolved, err := service.GetRunByOrdinal(ctx, ordinal)
	if err != nil || resolved["run_key"] != "stable.review.001" || resolved["run_ordinal"] != ordinal {
		t.Fatalf("stable ordinal resolved=%#v err=%v", resolved, err)
	}
	if _, err = native.Exec(ctx, `UPDATE operation_cycle_run_ordinals SET run_key='concurrent.alpha.001' WHERE ordinal=$1`, ordinal); err == nil {
		t.Fatal("immutable run ordinal accepted direct mutation")
	}
}

func operationCycleIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping operation-cycle PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_operation_cycle_test_" + hex.EncodeToString(raw)
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate operation-cycle integration test")
	}
	migrations := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	entries, err := os.ReadDir(migrations)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() > "0023_operation_cycle_admin_history.sql" && entry.Name() != "0104_operation_cycle_action_execution_snapshots.sql" {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	for _, name := range files {
		sql, readErr := os.ReadFile(filepath.Join(migrations, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanupCtx)
	}
}

// This verifies the action Owner's restart path with a real PostgreSQL
// transaction. It never contacts a Codex socket or any business Provider.
func TestPostgreSQLOperationCycleLeaseRenewalAndExpiredRecoveryAreFenced(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	snapshot := map[string]any{"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "restart.review", "run_key": "restart.review.001", "revision": 1, "strategy_version": 1, "status": "active", "title": "restart", "name": "restart", "cron": "weekly", "dot": "#2EA121", "action": "review", "steps": []any{}}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: snapshot, IdempotencyKey: "restart-report-key", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatal(err)
	}
	definition := executableDefinition()
	definition.Execution.RequiredLocalBindings = []string{"restart.review"}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: "restart.review", ExpectedVersion: 1, Title: "restart", Definition: definition, IdempotencyKey: "restart-execution-config", ActorID: "7"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Heartbeat(ctx, operationapp.RunnerHeartbeatCommand{RunnerID: "restart-runner", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{"restart.review"}}); err != nil {
		t.Fatal(err)
	}
	queued, err := service.Start(ctx, operationapp.StartCommand{StrategyKey: "restart.review", RunKey: "restart.review.001", ActionKey: "start_review", IdempotencyKey: "restart-start-key", ActorID: "7"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Claim(ctx, "restart-runner", "operation-cycle-service")
	if err != nil || first["claimed"] != true {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	requestID, oldLease := first["request_id"].(string), first["lease_token"].(string)
	if _, err = service.RecordActionEvent(ctx, operationapp.ActionEventCommand{RequestID: requestID, EventID: "restart-thread", EventType: "thread_bound", LeaseToken: oldLease, ThreadID: "thread-persisted"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RecordActionEvent(ctx, operationapp.ActionEventCommand{RequestID: requestID, EventID: "restart-turn", EventType: "turn_started", LeaseToken: oldLease, ThreadID: "thread-persisted", TurnID: "turn-persisted"}); err != nil {
		t.Fatal(err)
	}
	if renewed, renewErr := service.RenewActionLease(ctx, operationapp.ActionLeaseRenewalCommand{RequestID: requestID, LeaseToken: oldLease}); renewErr != nil || renewed["request_id"] != requestID {
		t.Fatalf("renewed=%#v err=%v", renewed, renewErr)
	}
	if _, err = native.Exec(ctx, `UPDATE operation_cycle_action_requests SET lease_expires_at=now()-interval '1 second' WHERE request_id=$1`, requestID); err != nil {
		t.Fatal(err)
	}

	results := make(chan map[string]any, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			value, claimErr := service.Claim(ctx, "restart-runner", "operation-cycle-service")
			results <- value
			errs <- claimErr
		}()
	}
	var recovered map[string]any
	for range 2 {
		value, claimErr := <-results, <-errs
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if value["claimed"] == true {
			if recovered != nil {
				t.Fatalf("two reclaimers: %#v and %#v", recovered, value)
			}
			recovered = value
		}
	}
	if recovered == nil || recovered["recovered"] != true || recovered["status"] != "turn_started" || recovered["thread_id"] != "thread-persisted" || recovered["turn_id"] != "turn-persisted" {
		t.Fatalf("recovered=%#v queued=%#v", recovered, queued)
	}
	newLease := recovered["lease_token"].(string)
	if newLease == oldLease {
		t.Fatal("reclaim reused its expired lease token")
	}
	if _, err = service.RecordActionEvent(ctx, operationapp.ActionEventCommand{RequestID: requestID, EventID: "old-lease-terminal", EventType: "completed", LeaseToken: oldLease, Result: map[string]any{"outcome": "outcome_unknown"}}); !errors.Is(err, operationapp.ErrLeaseInvalid) {
		t.Fatalf("old lease result=%v", err)
	}
	completed := operationapp.ActionEventCommand{RequestID: requestID, EventID: "restart-completed", EventType: "completed", LeaseToken: newLease, Result: map[string]any{"outcome": "outcome_unknown"}}
	if _, err = service.RecordActionEvent(ctx, completed); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RecordActionEvent(ctx, completed); err != nil {
		t.Fatalf("exact event replay=%v", err)
	}
	completed.Result = map[string]any{"outcome": "executed"}
	if _, err = service.RecordActionEvent(ctx, completed); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("payload drift=%v", err)
	}
	terminal, err := service.GetActionResult(ctx, requestID)
	if err != nil || terminal["status"] != "completed" {
		t.Fatalf("terminal=%#v err=%v", terminal, err)
	}
}

func TestPostgreSQLOperationCycleExecutionSnapshotFreezesVersionsAndBlocksLegacyActions(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	report := func(revision int, title, key string) {
		snapshot := map[string]any{"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "frozen.review", "run_key": "frozen.review.001", "revision": revision, "strategy_version": revision, "status": "active", "title": title, "name": title, "cron": "weekly", "dot": "#2EA121", "action": "开始复盘", "steps": []any{}}
		if _, reportErr := service.Report(ctx, operationapp.ReportCommand{Snapshot: snapshot, IdempotencyKey: key, ReporterID: "cycle-runner", ClientID: "v3-runner"}); reportErr != nil {
			t.Fatal(reportErr)
		}
	}
	report(1, "run one", "frozen-report-one")
	report(2, "run two", "frozen-report-two")
	firstDefinition := executableDefinition()
	firstDefinition.Execution.Title = "frozen v3"
	firstDefinition.Execution.Objective = "objective from frozen version"
	firstDefinition.Execution.CodexPrompt = "prompt from frozen version"
	firstDefinition.Execution.RequiredLocalBindings = []string{"frozen.review"}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: "frozen.review", ExpectedVersion: 2, Title: "configured", Definition: firstDefinition, IdempotencyKey: "frozen-config-v3", ActorID: "7"}); err != nil {
		t.Fatal(err)
	}
	// A later report is live run data. It must not erase the Owner-managed
	// execution DTO or replace its strategy version with a source snapshot.
	liveReport := map[string]any{"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "frozen.review", "run_key": "frozen.review.live", "revision": 1, "strategy_version": 99, "status": "active", "title": "source-owned title", "name": "live report", "cron": "weekly", "dot": "#2EA121", "action": "开始复盘", "steps": []any{}}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: liveReport, IdempotencyKey: "frozen-live-report", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatal(err)
	}
	persisted, err := service.GetStrategy(ctx, "frozen.review")
	if err != nil || persisted["version"] != int32(3) {
		t.Fatalf("report rewrote configured strategy version: %#v err=%v", persisted, err)
	}
	persistedDefinition, definitionOK := persisted["definition"].(map[string]any)
	persistedExecution, executionOK := persistedDefinition["execution"].(map[string]any)
	if !definitionOK || !executionOK || persistedExecution["objective"] != "objective from frozen version" {
		t.Fatalf("report erased configured execution: %#v", persisted)
	}
	if _, err = service.Heartbeat(ctx, operationapp.RunnerHeartbeatCommand{RunnerID: "frozen-runner", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{"frozen.review"}}); err != nil {
		t.Fatal(err)
	}
	queued, err := service.Start(ctx, operationapp.StartCommand{StrategyKey: "frozen.review", RunKey: "frozen.review.001", ActionKey: "start_review", IdempotencyKey: "frozen-start", ActorID: "7"})
	if err != nil {
		t.Fatal(err)
	}
	secondDefinition := firstDefinition
	secondDefinition.Execution = &operationapp.StrategyExecution{Title: "later v4", Objective: "later objective", CodexPrompt: "later prompt", RequiredLocalBindings: []string{"later.binding"}, ResultSchema: map[string]any{"type": "object"}}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: "frozen.review", ExpectedVersion: 3, Title: "later", Definition: secondDefinition, IdempotencyKey: "frozen-config-v4", ActorID: "7"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(ctx, "frozen-runner", "operation-cycle-service")
	if err != nil {
		t.Fatal(err)
	}
	execution, ok := claimed["execution"].(map[string]any)
	contextSummary, contextOK := claimed["context_summary"].(map[string]any)
	if !ok || !contextOK || execution["objective"] != "objective from frozen version" || execution["codex_prompt"] != "prompt from frozen version" || contextSummary["snapshot_revision"] != float64(2) || claimed["request_id"] != queued["request_id"] {
		t.Fatalf("claim did not retain immutable execution/run snapshot: %#v", claimed)
	}
	if _, err = native.Exec(ctx, `DELETE FROM operation_cycle_action_execution_snapshots WHERE request_id=$1`, queued["request_id"]); err != nil {
		t.Fatal(err)
	}
	// A legacy row must not be rebuilt from the now-current v4 definition. Force
	// a new fenced claim and require the explicit manual-review block instead.
	if _, err = native.Exec(ctx, `UPDATE operation_cycle_action_requests SET lease_expires_at=now()-interval '1 second' WHERE request_id=$1`, queued["request_id"]); err != nil {
		t.Fatal(err)
	}
	legacy, err := service.Claim(ctx, "frozen-runner", "operation-cycle-service")
	if err != nil || legacy["recovered"] != true || legacy["blocked_code"] != "missing_execution_snapshot" || legacy["execution"] != nil {
		t.Fatalf("legacy action was silently rebuilt: %#v err=%v", legacy, err)
	}
}
