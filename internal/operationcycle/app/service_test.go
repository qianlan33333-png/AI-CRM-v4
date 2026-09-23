package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type operationCycleTestUOW struct{ calls int }

func (uow *operationCycleTestUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	return callback(ctx)
}

var _ platformport.UnitOfWork = (*operationCycleTestUOW)(nil)

type operationCycleTestEvents struct{ events []operationport.Event }

func (events *operationCycleTestEvents) Append(_ context.Context, event operationport.Event) (operationport.EventID, error) {
	events.events = append(events.events, event)
	return operationport.EventID(len(events.events)), nil
}

type operationCycleTestDeliveries struct{ accepted []operationport.EventID }

func (deliveries *operationCycleTestDeliveries) Accept(_ context.Context, eventID operationport.EventID, consumer string) error {
	if consumer != operationport.ConsumerOperationCycleFact {
		return errors.New("unexpected consumer")
	}
	deliveries.accepted = append(deliveries.accepted, eventID)
	return nil
}

type operationCycleStoreStub struct {
	report           func(context.Context, ReportCommand, time.Time) (map[string]any, bool, error)
	claim            func(context.Context, string, string, time.Time, time.Duration) (map[string]any, bool, error)
	event            func(context.Context, ActionEventCommand, time.Time) (map[string]any, bool, error)
	createStrategy   func(context.Context, CreateStrategyCommand, time.Time) (map[string]any, bool, error)
	updateStrategy   func(context.Context, UpdateStrategyCommand, time.Time) (map[string]any, bool, error)
	transitionStatus func(context.Context, TransitionStrategyCommand, time.Time) (map[string]any, bool, error)
	getRunByOrdinal  func(context.Context, int32) (map[string]any, error)
}

func (stub *operationCycleStoreStub) Report(ctx context.Context, command ReportCommand, now time.Time) (map[string]any, bool, error) {
	if stub.report == nil {
		return nil, false, ErrUnavailable
	}
	return stub.report(ctx, command, now)
}
func (*operationCycleStoreStub) ListStrategies(context.Context, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) GetStrategy(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) ListRuns(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) GetRun(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}

func (stub *operationCycleStoreStub) GetRunByOrdinal(ctx context.Context, ordinal int32) (map[string]any, error) {
	if stub.getRunByOrdinal != nil {
		return stub.getRunByOrdinal(ctx, ordinal)
	}
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) Start(context.Context, StartCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, ErrUnavailable
}
func (*operationCycleStoreStub) CurrentAction(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) GetActionResult(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (stub *operationCycleStoreStub) Claim(ctx context.Context, runnerID, principalID string, now time.Time, lease time.Duration) (map[string]any, bool, error) {
	if stub.claim == nil {
		return nil, false, ErrUnavailable
	}
	return stub.claim(ctx, runnerID, principalID, now, lease)
}
func (stub *operationCycleStoreStub) RecordActionEvent(ctx context.Context, command ActionEventCommand, now time.Time) (map[string]any, bool, error) {
	if stub.event == nil {
		return nil, false, ErrUnavailable
	}
	return stub.event(ctx, command, now)
}
func (*operationCycleStoreStub) RenewActionLease(context.Context, ActionLeaseRenewalCommand, time.Time, time.Duration) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) Heartbeat(context.Context, RunnerHeartbeatCommand, time.Time) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) ContextIndex(context.Context, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) StrategyContext(context.Context, string, string, int32, int32, map[string]string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) CreateProposal(context.Context, ProposalCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, ErrUnavailable
}
func (*operationCycleStoreStub) ListProposals(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) DecideProposal(context.Context, string, string, string, time.Time) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (stub *operationCycleStoreStub) CreateStrategy(ctx context.Context, command CreateStrategyCommand, now time.Time) (map[string]any, bool, error) {
	if stub.createStrategy == nil {
		return nil, false, ErrUnavailable
	}
	return stub.createStrategy(ctx, command, now)
}
func (stub *operationCycleStoreStub) UpdateStrategy(ctx context.Context, command UpdateStrategyCommand, now time.Time) (map[string]any, bool, error) {
	if stub.updateStrategy == nil {
		return nil, false, ErrUnavailable
	}
	return stub.updateStrategy(ctx, command, now)
}
func (stub *operationCycleStoreStub) TransitionStrategy(ctx context.Context, command TransitionStrategyCommand, now time.Time) (map[string]any, bool, error) {
	if stub.transitionStatus == nil {
		return nil, false, ErrUnavailable
	}
	return stub.transitionStatus(ctx, command, now)
}
func (*operationCycleStoreStub) ListStrategyVersions(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) ListRunVersions(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}

func TestGetRunByOrdinalUsesStableStoreLookup(t *testing.T) {
	uow := &operationCycleTestUOW{}
	seen := int32(0)
	service := NewService(uow, &operationCycleStoreStub{getRunByOrdinal: func(_ context.Context, ordinal int32) (map[string]any, error) {
		seen = ordinal
		return map[string]any{"run_ordinal": ordinal, "run_key": "stable.review.001"}, nil
	}}, &operationCycleTestEvents{}, &operationCycleTestDeliveries{})
	result, err := service.GetRunByOrdinal(context.Background(), 73)
	if err != nil || seen != 73 || result["run_key"] != "stable.review.001" || uow.calls != 1 {
		t.Fatalf("stable ordinal result=%#v seen=%d uow=%d err=%v", result, seen, uow.calls, err)
	}
	for _, invalid := range []int32{0, -1, 1000000000} {
		if _, err = service.GetRunByOrdinal(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ordinal=%d err=%v, want invalid", invalid, err)
		}
	}
}

func TestReportIdempotentReplayDoesNotAppendAnotherEvent(t *testing.T) {
	uow := &operationCycleTestUOW{}
	events := &operationCycleTestEvents{}
	deliveries := &operationCycleTestDeliveries{}
	storeCalls := 0
	service := NewService(uow, &operationCycleStoreStub{report: func(_ context.Context, _ ReportCommand, _ time.Time) (map[string]any, bool, error) {
		storeCalls++
		return map[string]any{"accepted": true}, storeCalls > 1, nil
	}}, events, deliveries)
	command := ReportCommand{IdempotencyKey: "report-1", ReporterID: "runner-a", ClientID: "client-a", Snapshot: map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "growth", "run_key": "run-1",
	}}
	if _, err := service.Report(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Report(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if uow.calls != 2 || len(events.events) != 1 || len(deliveries.accepted) != 1 {
		t.Fatalf("uow/events/deliveries=%d/%d/%d, want 2/1/1", uow.calls, len(events.events), len(deliveries.accepted))
	}
	if events.events[0].Type != operationport.EvOperationCycleFact {
		t.Fatalf("event type=%q", events.events[0].Type)
	}
}

func TestCompletedOutcomeUnknownIsRecordedWithoutAutomaticRetry(t *testing.T) {
	uow := &operationCycleTestUOW{}
	events := &operationCycleTestEvents{}
	deliveries := &operationCycleTestDeliveries{}
	storeCalls := 0
	service := NewService(uow, &operationCycleStoreStub{event: func(_ context.Context, command ActionEventCommand, _ time.Time) (map[string]any, bool, error) {
		storeCalls++
		if command.EventType != "completed" || command.Result["outcome"] != "outcome_unknown" {
			return nil, false, ErrInvalid
		}
		return map[string]any{"request_id": command.RequestID, "status": "completed", "result": command.Result}, false, nil
	}}, events, deliveries)
	_, err := service.RecordActionEvent(context.Background(), ActionEventCommand{
		RequestID: "ocact_0123456789012345678901234567", EventID: "event-1", EventType: "completed", LeaseToken: "lease-1",
		Result: map[string]any{"outcome": "outcome_unknown"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if storeCalls != 1 || len(events.events) != 1 || len(deliveries.accepted) != 1 {
		t.Fatalf("store/events/deliveries=%d/%d/%d, want one terminal fact only", storeCalls, len(events.events), len(deliveries.accepted))
	}
	var payload map[string]any
	if err = json.Unmarshal(events.events[0].Payload, &payload); err != nil || payload["fact_type"] != "operation_cycle.action_completed" {
		t.Fatalf("payload=%v err=%v", payload, err)
	}
}

func TestDigestTreatsEquivalentJSONNumbersTheSameAndRejectsForbiddenScopeInput(t *testing.T) {
	integerDigest, err := Digest(map[string]any{"value": 1})
	if err != nil {
		t.Fatal(err)
	}
	decimalDigest, err := Digest(map[string]any{"value": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if integerDigest != decimalDigest {
		t.Fatal("numeric-equivalent JSON digests drifted")
	}
	service := NewService(&operationCycleTestUOW{}, &operationCycleStoreStub{}, &operationCycleTestEvents{}, &operationCycleTestDeliveries{})
	_, err = service.Report(context.Background(), ReportCommand{IdempotencyKey: "report-scope", ReporterID: "runner-a", ClientID: "client-a", Snapshot: map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "growth", "run_key": "run-1", "ten" + "ant_id": "forbidden",
	}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("forbidden scope report error=%v, want ErrInvalid", err)
	}
}

func TestReportProjectsBeforeStoreAuditAndOutbox(t *testing.T) {
	uow := &operationCycleTestUOW{}
	events := &operationCycleTestEvents{}
	deliveries := &operationCycleTestDeliveries{}
	store := &operationCycleStoreStub{report: func(_ context.Context, command ReportCommand, _ time.Time) (map[string]any, bool, error) {
		if _, exists := command.Snapshot["unknown"]; exists {
			t.Fatal("unprojected report field reached store")
		}
		if command.Snapshot["status"] != "active" || command.Snapshot["revision"] != float64(1) {
			t.Fatalf("typed defaults did not reach store: %#v", command.Snapshot)
		}
		return map[string]any{"accepted": true}, false, nil
	}}
	service := NewService(uow, store, events, deliveries)
	command := ReportCommand{IdempotencyKey: "report-projection", ReporterID: "runner-a", ClientID: "client-a", Snapshot: map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "growth", "run_key": "run-1",
	}}
	if _, err := service.Report(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if len(events.events) != 1 || bytes.Contains(events.events[0].Payload, []byte("unknown")) || bytes.Contains(events.events[0].Payload, []byte("run-1")) {
		t.Fatalf("event payload leaked report input: %#v", events.events)
	}
	for _, unsafe := range []string{"token", "password", "cookie", "private_key", "13800138000", "person@example.test"} {
		candidate := ReportCommand{IdempotencyKey: "report-" + strings.ReplaceAll(unsafe, "@", "-"), ReporterID: "runner-a", ClientID: "client-a", Snapshot: map[string]any{"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "growth", "run_key": "run-1", unsafe: unsafe}}
		if _, err := service.Report(context.Background(), candidate); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe report %q error = %v, want ErrInvalid", unsafe, err)
		}
	}
}

func TestStrategyContextRejectsExcludedDataPlaneFilters(t *testing.T) {
	service := NewService(&operationCycleTestUOW{}, &operationCycleStoreStub{}, &operationCycleTestEvents{}, &operationCycleTestDeliveries{})
	for _, key := range []string{"order_id", "entitlement_id", "membership_id", "subscription_id"} {
		_, err := service.StrategyContext(context.Background(), "weekly.review", "review", 10, 0, map[string]string{key: "opaque"})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("filter %q error=%v, want ErrInvalid", key, err)
		}
	}
}

func TestAdminStrategyMutationIsTypedAuditedAndIdempotent(t *testing.T) {
	uow := &operationCycleTestUOW{}
	events := &operationCycleTestEvents{}
	deliveries := &operationCycleTestDeliveries{}
	storeCalls := 0
	store := &operationCycleStoreStub{createStrategy: func(_ context.Context, command CreateStrategyCommand, _ time.Time) (map[string]any, bool, error) {
		storeCalls++
		return map[string]any{"strategy_key": command.StrategyKey, "status": "draft", "version": 1}, storeCalls > 1, nil
	}}
	service := NewService(uow, store, events, deliveries)
	command := CreateStrategyCommand{
		StrategyKey: "weekly.review", Title: "每周复盘", IdempotencyKey: "create-weekly-review", ActorID: "7",
		Definition: StrategyDefinition{Schedule: "每周一 09:00", IndicatorColor: "#2EA121", PrimaryAction: "start_review", Stages: []StrategyStage{{Key: "retro", Label: "复盘", Color: "#2EA121", State: "current"}}},
	}
	if _, err := service.CreateStrategy(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateStrategy(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if storeCalls != 2 || len(events.events) != 1 || len(deliveries.accepted) != 1 {
		t.Fatalf("store/events/deliveries=%d/%d/%d, want 2/1/1", storeCalls, len(events.events), len(deliveries.accepted))
	}
	if events.events[0].Type != operationport.EvOperationCycleFact || !bytes.Contains(events.events[0].Payload, []byte(`"actor_id":"7"`)) {
		t.Fatalf("admin audit payload=%s", events.events[0].Payload)
	}
}

func TestAdminStrategyRejectsDatabaseInvalidKeyAndUntypedDefinition(t *testing.T) {
	service := NewService(&operationCycleTestUOW{}, &operationCycleStoreStub{}, &operationCycleTestEvents{}, &operationCycleTestDeliveries{})
	base := CreateStrategyCommand{
		StrategyKey: "weekly.review", Title: "每周复盘", IdempotencyKey: "create-weekly-review", ActorID: "7",
		Definition: StrategyDefinition{Schedule: "每周一 09:00", IndicatorColor: "#2EA121", PrimaryAction: "start_review", Stages: []StrategyStage{{Key: "retro", Label: "复盘", Color: "#2EA121", State: "current"}}},
	}
	invalidKey := base
	invalidKey.StrategyKey = "weekly review"
	if _, err := service.CreateStrategy(context.Background(), invalidKey); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid strategy key error=%v", err)
	}
	invalidDefinition := base
	invalidDefinition.Definition.PrimaryAction = "arbitrary_json_action"
	if _, err := service.CreateStrategy(context.Background(), invalidDefinition); !errors.Is(err, ErrInvalid) {
		t.Fatalf("untyped definition error=%v", err)
	}
	piiKey := base
	piiKey.IdempotencyKey = "person@example.test"
	if _, err := service.CreateStrategy(context.Background(), piiKey); !errors.Is(err, ErrInvalid) {
		t.Fatalf("PII idempotency key error=%v", err)
	}
}

func TestAdminAuditKeyIsBoundedScopedAndDoesNotExposeRawIdempotencyKey(t *testing.T) {
	raw := strings.Repeat("a", 200)
	key := adminEventKey("operation_cycle.strategy_created", "7", raw)
	if len(key) != len("ocadmin_")+64 || strings.Contains(key, raw) {
		t.Fatalf("admin audit key is not bounded/digested: %q", key)
	}
	if key == adminEventKey("operation_cycle.strategy_updated", "7", raw) || key == adminEventKey("operation_cycle.strategy_created", "8", raw) {
		t.Fatal("admin audit key is not operation/actor scoped")
	}
}

func TestClaimFactNeverPersistsRawLeaseToken(t *testing.T) {
	uow := &operationCycleTestUOW{}
	events := &operationCycleTestEvents{}
	service := NewService(uow, &operationCycleStoreStub{claim: func(context.Context, string, string, time.Time, time.Duration) (map[string]any, bool, error) {
		return map[string]any{"claimed": true, "request_id": "ocact_0123456789012345678901234567", "lease_token": "lease-private-value"}, true, nil
	}}, events, &operationCycleTestDeliveries{})
	if _, err := service.Claim(context.Background(), "runner-a", "operation-cycle-service"); err != nil {
		t.Fatal(err)
	}
	if len(events.events) != 1 {
		t.Fatalf("events=%d", len(events.events))
	}
	if bytes.Contains(events.events[0].Payload, []byte("lease-private-value")) {
		t.Fatalf("lease token leaked into persisted event: %s", events.events[0].Payload)
	}
}
