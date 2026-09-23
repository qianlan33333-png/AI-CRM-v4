package domain

import (
	"testing"
	"time"
)

func TestExecutionGateValidatesFailClosedProjection(t *testing.T) {
	valid := ExecutionGate{
		LocalCommandAcceptanceAvailable: true,
		LocalQueueAvailable:             true,
		ObservedAt:                      time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	}
	if !valid.Valid() {
		t.Fatal("local command/queue gate should be valid")
	}
	for name, mutate := range map[string]func(*ExecutionGate){
		"provider eligible":   func(gate *ExecutionGate) { gate.ProviderExecutionEligible = true },
		"command unavailable": func(gate *ExecutionGate) { gate.LocalCommandAcceptanceAvailable = false },
		"queue unavailable":   func(gate *ExecutionGate) { gate.LocalQueueAvailable = false },
		"sync executed":       func(gate *ExecutionGate) { gate.SyncExecuted = true },
		"external call":       func(gate *ExecutionGate) { gate.RealExternalCallExecuted = true },
		"missing observation": func(gate *ExecutionGate) { gate.ObservedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if candidate.Valid() {
				t.Fatalf("gate %#v unexpectedly valid", candidate)
			}
		})
	}
}
