package store

import (
	"context"
	"testing"
	"time"

	aiapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type dimensionStatusReader struct {
	*Repository
	afterPlan func()
}

func (r *dimensionStatusReader) GetPlan(ctx context.Context, id ai.PlanID, lock bool) (ai.Plan, error) {
	plan, err := r.Repository.GetPlan(ctx, id, lock)
	if err == nil && r.afterPlan != nil {
		r.afterPlan()
	}
	return plan, err
}

// Force an owner completion between the plan and summary reads. The public
// status must describe either the old or new committed state, never both.
func TestDimensionPostgreSQLMachineStatusCannotMixCompletionSnapshots(t *testing.T) {
	pool, cleanup := integrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	planID, effects := seedConcurrentCompletionPlan(t, ctx, pool, 3)
	actor, _ := ai.MachineActorFromClientID("dimension-status")
	if _, err = pool.Exec(ctx, `UPDATE ai_assistant_plans SET created_by=NULL,created_actor_kind=$2,created_actor_ref=$3 WHERE id=$1`, planID, actor.Kind, actor.Reference); err != nil {
		t.Fatal(err)
	}
	readStarted, writerSettled := make(chan struct{}), make(chan struct{})
	writerPID, writerResult := make(chan int, 1), make(chan error, 1)
	readResult := make(chan ai.MachineOperationStatus, 1)
	readError := make(chan error, 1)
	reader := &dimensionStatusReader{Repository: repository}
	reader.afterPlan = func() {
		close(readStarted)
		select {
		case <-writerSettled:
		case <-ctx.Done():
		}
	}
	service, err := aiapp.NewService(uow, reader, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		result, statusErr := service.GetMachineOperationStatus(ctx, actor, planID)
		readResult <- result
		readError <- statusErr
	}()
	select {
	case <-readStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		writerResult <- uow.Within(ctx, func(txctx context.Context) error {
			tx, txErr := platformpostgres.RequireTransaction(txctx)
			if txErr != nil {
				return txErr
			}
			var pid int
			if txErr = tx.QueryRow(txctx, `SELECT pg_backend_pid()`).Scan(&pid); txErr != nil {
				return txErr
			}
			writerPID <- pid
			return repository.CompleteExternalEffect(txctx, effects[0], ai.ExecutionOutcomeUnknown, false, false, effect.Hash("dimension-status-unknown"), 1, 1, 1, time.Now().UTC())
		})
	}()
	var pid int
	select {
	case pid = <-writerPID:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	writerFinished := false
	var completionErr error
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case completionErr = <-writerResult:
			writerFinished = true
		default:
		}
		if writerFinished {
			break
		}
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion neither committed nor waited on the status snapshot")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(writerSettled)
	status, statusErr := <-readResult, <-readError
	if !writerFinished {
		completionErr = <-writerResult
	}
	if statusErr != nil || completionErr != nil {
		t.Fatalf("status read=%v completion=%v", statusErr, completionErr)
	}
	if status.OperationState == ai.MachineOperationDispatching && status.OutcomeUnknownCount != 0 {
		t.Fatalf("mixed old plan/new summary: %+v", status)
	}
	if status.OperationState == ai.MachineOperationOutcomeUnknown && status.OutcomeUnknownCount != 1 {
		t.Fatalf("mixed new plan/old summary: %+v", status)
	}
	reader.afterPlan = nil
	current, err := service.GetMachineOperationStatus(ctx, actor, planID)
	if err != nil || current.OperationState != ai.MachineOperationOutcomeUnknown || current.OutcomeUnknownCount != 1 {
		t.Fatalf("completed owner projection=%+v err=%v", current, err)
	}
}
