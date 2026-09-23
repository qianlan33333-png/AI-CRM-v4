package externaleffects

import (
	"testing"
	"time"
)

func TestPushControlEnvelopeMatchesFrozenDTOGuards(t *testing.T) {
	completed := time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)
	envelope := pushControlEnvelope(42, "pending", "manual_retry", completed, 7)
	if envelope["ok"] != true || envelope["fallback_used"] != false || envelope["local_fact_only"] != true || envelope["real_external_call_executed"] != false || envelope["delivery_proven"] != false || envelope["provider_execution_eligible"] != false {
		t.Fatalf("outer Push DTO guard=%+v", envelope)
	}
	receipt, ok := envelope["control_receipt"].(map[string]any)
	if !ok || receipt["task_id"] != int64(42) || receipt["task_status"] != "pending" || receipt["operation"] != "manual_retry" || receipt["completed_at"] != completed || receipt["actor_admin_user_id"] != int64(7) || receipt["local_fact_only"] != true || receipt["real_external_call_executed"] != false || receipt["delivery_proven"] != false {
		t.Fatalf("control receipt DTO guard=%+v", receipt)
	}
}

func TestPushStatusDoesNotClaimReconciliationWasDelivered(t *testing.T) {
	if got := pushStatus(string(StateExecuted)); got != "sent" {
		t.Fatalf("executed status=%q", got)
	}
	if got := pushStatus(string(StateReconciled)); got != "reconciled" {
		t.Fatalf("reconciled status=%q must not claim sent", got)
	}
}
