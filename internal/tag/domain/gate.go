package domain

import "time"

// ExecutionGate is the deliberately small, fail-closed projection exposed by
// the tags admin surface.  It describes local command/queue availability; it
// is not a Provider health result and never proves that a WeCom request ran.
type ExecutionGate struct {
	ProviderExecutionEligible       bool      `json:"provider_execution_eligible"`
	LocalCommandAcceptanceAvailable bool      `json:"local_command_acceptance_available"`
	LocalQueueAvailable             bool      `json:"local_queue_available"`
	SyncExecuted                    bool      `json:"sync_executed"`
	ObservedAt                      time.Time `json:"observed_at"`
	RealExternalCallExecuted        bool      `json:"real_external_call_executed"`
}

// Valid reports whether a gate is a safe local projection.  The current
// donor contract is fail-closed: command acceptance and local queueing may be
// available, while Provider execution and external effects remain false.
func (gate ExecutionGate) Valid() bool {
	return !gate.ProviderExecutionEligible && gate.LocalCommandAcceptanceAvailable &&
		gate.LocalQueueAvailable && !gate.SyncExecuted && !gate.RealExternalCallExecuted &&
		!gate.ObservedAt.IsZero()
}
