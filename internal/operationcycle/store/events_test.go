package store

import (
	"encoding/json"
	"testing"

	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
)

func TestOperationCycleAuditScopeRecordsAdminAndStrategy(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"fact_type": "operation_cycle.strategy_updated", "data": map[string]any{"actor_id": "7", "strategy_key": "weekly.review"}})
	if err != nil {
		t.Fatal(err)
	}
	actorType, actorID, resourceID := operationCycleAuditScope(operationport.Event{IdempotencyKey: "operation_cycle:ocadmin_digest", Payload: payload})
	if actorType != "admin" || actorID != "7" || resourceID != "weekly.review" {
		t.Fatalf("admin audit scope=%q/%q/%q", actorType, actorID, resourceID)
	}
}

func TestOperationCycleAuditScopeKeepsSystemFallback(t *testing.T) {
	actorType, actorID, resourceID := operationCycleAuditScope(operationport.Event{IdempotencyKey: "operation_cycle:report", Payload: json.RawMessage(`{"fact_type":"operation_cycle.reported","data":{}}`)})
	if actorType != "system" || actorID != "" || resourceID != "operation_cycle:report" {
		t.Fatalf("system audit scope=%q/%q/%q", actorType, actorID, resourceID)
	}
}
