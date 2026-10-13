package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	aiapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	aistore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	openport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// Pause after the real Owner plan read, then commit a completion and scope
// change on a second connection. No sleeps determine the ordering.
type snapshotPlanStore struct {
	*aistore.Repository
	observed chan struct{}
	resume   chan struct{}
}

func (s *snapshotPlanStore) GetPlan(ctx context.Context, id aiport.PlanID, lock bool) (aiport.Plan, error) {
	p, err := s.Repository.GetPlan(ctx, id, lock)
	if err == nil && s.observed != nil {
		close(s.observed)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return p, ctx.Err()
		}
	}
	return p, err
}

// A local synthetic Owner projection exercises the actual composed adapter
// and the scope Port without touching any live directory or Provider.
type snapshotOwnerProjection struct{ fail bool }

func (s *snapshotOwnerProjection) AudiencePrimaryOwners(ctx context.Context, ids []customer.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	if s.fail {
		return nil, errors.New("synthetic owner dependency failure")
	}
	tx, err := postgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	var owner, isolation, readonly string
	if err = tx.QueryRow(ctx, `SELECT owner,current_setting('transaction_isolation'),current_setting('transaction_read_only') FROM test_operation_owner WHERE customer_id=$1`, ids[0]).Scan(&owner, &isolation, &readonly); err != nil {
		return nil, err
	}
	if isolation != "repeatable read" || readonly != "on" {
		return nil, fmt.Errorf("wrong snapshot boundary %s/%s", isolation, readonly)
	}
	return []wecomport.AudiencePrimaryOwner{{CustomerID: ids[0], CorpScope: "snapshot-corp", OwnerUserID: owner, Status: "known"}}, nil
}

func TestOpenPlatformOperationSnapshotPostgreSQLConcurrentCompletionAndScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	url, cleanup := openPlatformMachineTestDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err = openPlatformV1AIMigrate(ctx, native); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	command, err := postgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := postgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := aistore.NewPostgreSQL(native, command)
	if err != nil {
		t.Fatal(err)
	}
	gate := &snapshotPlanStore{Repository: repo, observed: make(chan struct{}), resume: make(chan struct{})}
	service, err := aiapp.NewService(command, gate, openPlatformV1AICustomers{}, openPlatformV1AIStaff{}, openPlatformV1AIMaterials{}, openPlatformV1AIIdentities{}, openPlatformV1AIIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := aiport.MachineActorFromClientID("client-a")
	if _, err = service.GetMachineOperationWithin(ctx, actor, 1); !errors.Is(err, postgres.ErrTransactionNeeded) {
		t.Fatalf("unbound read err=%v", err)
	}
	var planID int64
	if err = native.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,target_count,pending_count,approved_count,created_actor_kind,created_actor_ref) VALUES('snapshot','open_platform',decode(repeat('01',32),'hex'),'dispatching',1,0,1,'machine','machine:client-a') RETURNING id`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,review_state,execution_state) VALUES($1,42,8,'approved','queued');`, planID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `CREATE TABLE test_operation_owner(customer_id bigint PRIMARY KEY,owner text NOT NULL);INSERT INTO test_operation_owner VALUES(42,'owner-a')`); err != nil {
		t.Fatal(err)
	}
	projection := &snapshotOwnerProjection{}
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	executor.scopes.WeComScope = "snapshot-corp"
	executor.owners = openPlatformOwnerAdapter{uow: command, reader: projection}
	if err = executor.BindV1AI(service, service, command, snapshot); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{ClientID: "client-a", OwnerScope: accessdomain.OwnerScope{"owner_userid": []string{"owner-a"}}}
	raw := json.RawMessage(fmt.Sprintf(`{"operation_id":"%s%d"}`, v1AIReviewPlanOperationPrefix, planID))
	type outcome struct {
		result openport.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() { r, e := executor.v1OperationStatus(ctx, principal, raw); done <- outcome{r, e} }()
	select {
	case <-gate.observed:
	case early := <-done:
		t.Fatalf("query ended before barrier: %v", early.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = command.Within(ctx, func(txctx context.Context) error {
		if _, e := repo.GetPlan(txctx, aiport.PlanID(planID), true); e != nil {
			return e
		}
		tx, e := postgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `UPDATE ai_assistant_plan_recipients SET execution_state='outcome_unknown',version=version+1 WHERE plan_id=$1`, planID); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `UPDATE ai_assistant_plans SET state='needs_attention',needs_attention_count=1,version=version+1 WHERE id=$1`, planID); e != nil {
			return e
		}
		_, e = tx.Exec(txctx, `UPDATE test_operation_owner SET owner='owner-b' WHERE customer_id=42`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	close(gate.resume)
	first := <-done
	if first.err != nil {
		t.Fatal(first.err)
	}
	assertOperationSnapshot(t, first.result, "dispatching", 0, "queued")
	gate.observed = nil
	denied, e := executor.v1OperationStatus(ctx, principal, raw)
	if openport.ErrorCodeOf(e) != openport.ErrorPermission || denied.Data != nil {
		t.Fatalf("changed scope leaked aggregate result=%+v err=%v", denied, e)
	}
	principal.OwnerScope = nil
	current, e := executor.v1OperationStatus(ctx, principal, raw)
	if e != nil {
		t.Fatal(e)
	}
	assertOperationSnapshot(t, current, "outcome_unknown", 1, "outcome_unknown")
	principal.ClientID = "client-b"
	denied, e = executor.v1OperationStatus(ctx, principal, raw)
	if openport.ErrorCodeOf(e) != openport.ErrorNotFound || denied.Data != nil {
		t.Fatalf("foreign machine result=%+v err=%v", denied, e)
	}
	principal.ClientID = "client-a"
	principal.OwnerScope = accessdomain.OwnerScope{"owner_userid": []string{"owner-b"}}
	projection.fail = true
	denied, e = executor.v1OperationStatus(ctx, principal, raw)
	if e == nil || denied.Data != nil {
		t.Fatalf("dependency failure leaked partial result=%+v err=%v", denied, e)
	}
	var receipts, audits int
	if e = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM ai_assistant_operation_receipts),(SELECT count(*) FROM ai_assistant_audit_events)`).Scan(&receipts, &audits); e != nil || receipts != 0 || audits != 0 {
		t.Fatalf("read produced writes receipts=%d audits=%d err=%v", receipts, audits, e)
	}
}

func assertOperationSnapshot(t *testing.T, r openport.Result, state string, unknown int, member string) {
	t.Helper()
	d, ok := r.Data.(map[string]any)
	if !ok {
		t.Fatalf("data=%T", r.Data)
	}
	items, ok := d["recipients"].([]map[string]any)
	if !ok || len(items) != 1 || fmt.Sprint(d["operation_state"]) != state || d["outcome_unknown_count"] != unknown || fmt.Sprint(items[0]["execution_state"]) != member {
		t.Fatalf("incoherent aggregate=%+v", d)
	}
}
