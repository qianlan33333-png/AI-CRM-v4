package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// A cancelling transaction already owns the run lock when a completion arrives.
// Both owner operations must finish without a deadlock or reviving the run.
// Only local generation facts are used; no model or outbound adapter is invoked.
func TestDimensionPostgreSQLGenerationCompletionAndCancellationUseOneLockOrder(t *testing.T) {
	pool, cleanup := automationRuntimeIntegrationPool(t)
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
	runID := insertGenerationTestRun(t, pool)
	item := generationTestItem(runID, 1001, "eer_1")
	if err = uow.Within(ctx, func(tx context.Context) error {
		created, createErr := repository.CreateGenerationItems(tx, []automationdomain.GenerationItem{item})
		if createErr != nil {
			return createErr
		}
		return repository.BindGenerationEffect(tx, created[0].ID, item.EffectID, item.CreatedAt)
	}); err != nil {
		t.Fatal(err)
	}

	runLocked, continueCancel := make(chan struct{}), make(chan struct{})
	cancelResult, settleResult := make(chan error, 1), make(chan error, 1)
	settlePID := make(chan int, 1)
	go func() {
		cancelResult <- uow.Within(ctx, func(txctx context.Context) error {
			tx, lockErr := platformpostgres.RequireTransaction(txctx)
			if lockErr != nil {
				return lockErr
			}
			var locked int64
			if lockErr = tx.QueryRow(txctx, `SELECT id FROM automation_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&locked); lockErr != nil {
				return lockErr
			}
			close(runLocked)
			select {
			case <-continueCancel:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, lockErr = repository.CancelRun(txctx, runID, item.CreatedAt.Add(time.Minute))
			return lockErr
		})
	}()
	select {
	case <-runLocked:
	case err = <-cancelResult:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		settleResult <- uow.Within(ctx, func(txctx context.Context) error {
			tx, txErr := platformpostgres.RequireTransaction(txctx)
			if txErr != nil {
				return txErr
			}
			var pid int
			if txErr = tx.QueryRow(txctx, `SELECT pg_backend_pid()`).Scan(&pid); txErr != nil {
				return txErr
			}
			settlePID <- pid
			_, _, createPlan, txErr := repository.SettleGeneration(txctx, generationTestCompletion(item.EffectID, effectport.StateExecuted, "迟到的生成建议"))
			if txErr == nil && createPlan {
				return fmt.Errorf("cancelled run requested a review plan")
			}
			return txErr
		})
	}()
	var pid int
	select {
	case pid = <-settlePID:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not reach the held run lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(continueCancel)
	cancelErr, settleErr := <-cancelResult, <-settleResult
	if cancelErr != nil || settleErr != nil {
		t.Fatalf("cancel/completion must both commit: cancel=%v completion=%v", cancelErr, settleErr)
	}
	var runState, itemState string
	if err = pool.QueryRow(ctx, `SELECT r.state,i.state FROM automation_runs r JOIN automation_generation_items i ON i.run_id=r.id WHERE r.id=$1`, runID).Scan(&runState, &itemState); err != nil {
		t.Fatal(err)
	}
	if runState != string(automationport.RunCancelled) || itemState != "cancelled" {
		t.Fatalf("late completion revived cancelled facts: run=%s item=%s", runState, itemState)
	}
}
