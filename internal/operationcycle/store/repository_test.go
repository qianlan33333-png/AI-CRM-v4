package store

import (
	"testing"

	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
)

func TestOperationActionIdempotencyIsActorScopedAndRejectsPayloadDrift(t *testing.T) {
	first := operationActionKeyDigest("admin:7", "same-key")
	if first == operationActionKeyDigest("admin:8", "same-key") {
		t.Fatal("idempotency digest must be actor scoped")
	}
	command := operationapp.StartCommand{StrategyKey: "growth", RunKey: "run-1", ActionKey: "review", ParentRequest: "parent-1", ActorID: "admin:7"}
	if !sameActionCommand("growth", "run-1", "review", "parent-1", "admin:7", command) {
		t.Fatal("exact replay must match")
	}
	for _, drift := range []operationapp.StartCommand{
		{StrategyKey: "other", RunKey: "run-1", ActionKey: "review", ParentRequest: "parent-1", ActorID: "admin:7"},
		{StrategyKey: "growth", RunKey: "run-2", ActionKey: "review", ParentRequest: "parent-1", ActorID: "admin:7"},
		{StrategyKey: "growth", RunKey: "run-1", ActionKey: "execute", ParentRequest: "parent-1", ActorID: "admin:7"},
		{StrategyKey: "growth", RunKey: "run-1", ActionKey: "review", ParentRequest: "parent-2", ActorID: "admin:7"},
		{StrategyKey: "growth", RunKey: "run-1", ActionKey: "review", ParentRequest: "parent-1", ActorID: "admin:8"},
	} {
		if sameActionCommand("growth", "run-1", "review", "parent-1", "admin:7", drift) {
			t.Fatalf("payload drift matched: %#v", drift)
		}
	}
}
