package store

import (
	"context"
	"testing"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// This tests the owner race independently of any model adapter. Cancellation
// has already committed when a previously accepted generation completion
// arrives. It must not revive the run or request a new review plan.
func TestSol61PostgreSQLManualGenerationCompletionCannotReviveCancelledRun(t *testing.T) {
	pool, cleanup := automationRuntimeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	if _, err = pool.Exec(ctx, `UPDATE automation_runs SET target_count=1 WHERE id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	item := generationTestItem(runID, 1001, "eer_1")
	if err = uow.Within(ctx, func(tx context.Context) error {
		items, createErr := repository.CreateGenerationItems(tx, []automationdomain.GenerationItem{item})
		if createErr != nil {
			return createErr
		}
		return repository.BindGenerationEffect(tx, items[0].ID, item.EffectID, item.CreatedAt)
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		run, cancelErr := repository.CancelRun(tx, runID, item.CreatedAt.Add(30*time.Second))
		if cancelErr == nil && (run.PolicyID != 0 || run.State != automationport.RunCancelled) {
			t.Fatalf("expected cancelled manual run: %+v", run)
		}
		return cancelErr
	}); err != nil {
		t.Fatal(err)
	}
	var createPlan bool
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, _, createPlan, err = repository.SettleGeneration(tx, generationTestCompletion(item.EffectID, effectport.StateExecuted, "模型已返回的一条建议"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = pool.QueryRow(ctx, `SELECT state FROM automation_runs WHERE id=$1`, runID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(automationport.RunCancelled) || createPlan {
		t.Fatalf("late completion revived cancelled manual run: state=%s create_review_plan=%t", state, createPlan)
	}
}

// One unknown generation changes the aggregate run state while another item
// is still queued. Cancellation must retain that unknown fact and exclude the
// remaining item from generating a new review plan on a late completion.
func TestSol61PostgreSQLUnknownGenerationCancellationRetainsUnknownAndCannotRevive(t *testing.T) {
	pool, cleanup := automationRuntimeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	items := []automationdomain.GenerationItem{generationTestItem(runID, 1001, "eer_1"), generationTestItem(runID, 1002, "eer_2")}
	if err = uow.Within(ctx, func(tx context.Context) error {
		created, createErr := repository.CreateGenerationItems(tx, items)
		if createErr != nil {
			return createErr
		}
		for index := range created {
			if bindErr := repository.BindGenerationEffect(tx, created[index].ID, items[index].EffectID, items[index].CreatedAt); bindErr != nil {
				return bindErr
			}
		}
		_, run, plan, settleErr := repository.SettleGeneration(tx, generationTestCompletion(items[0].EffectID, effectport.StateUnknown, ""))
		if settleErr == nil && (run.State != automationport.RunOutcomeUnknown || plan) {
			t.Fatalf("expected one unknown and one queued: run=%+v create_plan=%t", run, plan)
		}
		return settleErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, cancelErr := repository.CancelRun(tx, runID, items[0].CreatedAt.Add(2*time.Minute))
		return cancelErr
	}); err != nil {
		t.Fatal(err)
	}
	var createPlan bool
	if err = uow.Within(ctx, func(tx context.Context) error {
		completion := generationTestCompletion(items[1].EffectID, effectport.StateExecuted, "另一条已返回建议")
		completion.CompletedAt = items[0].CreatedAt.Add(3 * time.Minute)
		_, _, createPlan, err = repository.SettleGeneration(tx, completion)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var runState, unknownState, remainingState string
	if err = pool.QueryRow(ctx, `SELECT r.state,a.state,b.state FROM automation_runs r JOIN automation_generation_items a ON a.run_id=r.id AND a.effect_id='eer_1' JOIN automation_generation_items b ON b.run_id=r.id AND b.effect_id='eer_2' WHERE r.id=$1`, runID).Scan(&runState, &unknownState, &remainingState); err != nil {
		t.Fatal(err)
	}
	if runState != string(automationport.RunCancelled) || unknownState != "outcome_unknown" || remainingState != "cancelled" || createPlan {
		t.Fatalf("cancellation changed unknown or late completion revived run: run=%s original_unknown=%s remaining=%s create_plan=%t", runState, unknownState, remainingState, createPlan)
	}
}
