package store

import (
	"context"
	"testing"
	"time"
)

func TestPostgreSQLAutomationAcceptedWaitIsNotSystemOverdue(t *testing.T) {
	pool, cleanup := automationRuntimeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO automation_runs(package_id,package_version,snapshot_id,agent_id,agent_published_version,binding_version,sender_set_version,preview_digest,state,target_count,skipped_count,created_by,created_at,updated_at) VALUES(1,1,1,1,1,1,1,decode(repeat('1a',32),'hex'),'executing',2,0,7,$1,$1) RETURNING id`, old).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO automation_run_recipients(run_id,customer_id,sender_staff_id,state,created_at,updated_at) VALUES($1,1,1,'provider_accepted',$2,$2),($1,2,1,'delivery_proven',$2,$2)`, id, old); err != nil {
		t.Fatal(err)
	}
	reader := NewOpsDiagnosticReader(pool)
	check := func(overdue, waiting int64) {
		t.Helper()
		got, e := reader.ReadOpsDiagnosticCounts(ctx, now)
		if e != nil || got["runs_overdue"] != overdue || got["runs_awaiting_delivery"] != waiting {
			t.Fatalf("observations=%v err=%v", got, e)
		}
	}
	check(0, 1)
	var state string
	if err = pool.QueryRow(ctx, `SELECT state FROM automation_runs WHERE id=$1`, id).Scan(&state); err != nil || state != "executing" {
		t.Fatal("diagnostics rewrote business state")
	}
	if _, err = pool.Exec(ctx, `UPDATE automation_run_recipients SET state='attempted' WHERE customer_id=2`); err != nil {
		t.Fatal(err)
	}
	check(1, 0)
	if _, err = pool.Exec(ctx, `UPDATE automation_run_recipients SET state='outcome_unknown' WHERE customer_id=2`); err != nil {
		t.Fatal(err)
	}
	check(1, 0)
	if _, err = pool.Exec(ctx, `UPDATE automation_run_recipients SET state='delivery_proven' WHERE customer_id=2`); err != nil {
		t.Fatal(err)
	}
	check(0, 1)
}
