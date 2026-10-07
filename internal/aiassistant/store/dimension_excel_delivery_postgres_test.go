package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// A browser receipt refresh and the River observation worker may hold
// different observations of one local Excel send. Once success commits, a
// previously read failure must not erase its proof. No Provider is called.
func TestDimensionPostgreSQLExcelConcurrentOldFailureCannotEraseDeliveryProof(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	planID, effects := seedConcurrentCompletionPlan(t, ctx, native, 1)
	target := &ai.DeferredTarget{UnionID: "synthetic-union", Scope: "wechat-open-platform:synthetic", SenderUserID: "synthetic-staff"}
	targetJSON, _ := json.Marshal(target)
	var recipientID ai.RecipientID
	if err := native.QueryRow(ctx, `UPDATE ai_assistant_plan_recipients r SET customer_id=0,staff_id=0,deferred_target=$2::jsonb FROM ai_assistant_effect_bindings b WHERE b.recipient_id=r.id AND b.external_effect_id=$1 RETURNING r.id`, effects[0], targetJSON).Scan(&recipientID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET execution_state='provider_accepted' WHERE plan_id=$1 AND id<>$2`, planID, recipientID); err != nil {
		t.Fatal(err)
	}
	config := native.Config().Copy()
	appName := fmt.Sprintf("dimension-excel-proof-%d", time.Now().UnixNano())
	config.ConnConfig.RuntimeParams["application_name"] = appName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
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
	blocker, err := native.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var locked int64
	if err = blocker.QueryRow(ctx, `SELECT id FROM ai_assistant_plans WHERE id=$1 FOR UPDATE`, planID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	waitFor := func(want int) {
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting int
			if err = native.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'`, appName).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("observations waiting on plan=%d, want %d", waiting, want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	success, failure := 1, 2
	at := time.Now().UTC()
	recipient := ai.Recipient{ID: recipientID, PlanID: planID, DeferredTarget: target}
	result := make(chan error, 2)
	go func() {
		result <- repository.RecordExcelDelivery(ctx, recipient, outbound.PrivateMessageDelivery{MessageID: "synthetic-message", SenderUserID: target.SenderUserID, ExternalUserID: "synthetic-external", Status: &success, SentAt: &at})
	}()
	waitFor(1)
	go func() {
		result <- repository.RecordExcelDelivery(ctx, recipient, outbound.PrivateMessageDelivery{MessageID: "synthetic-message", SenderUserID: target.SenderUserID, ExternalUserID: "synthetic-external", Status: &failure})
	}()
	waitFor(2)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	firstErr, secondErr := <-result, <-result
	if firstErr != nil || secondErr != nil {
		t.Fatalf("observation errors: success=%v old_failure=%v", firstErr, secondErr)
	}
	var proof bool
	var bindingState, recipientState, planState string
	if err = native.QueryRow(ctx, `SELECT b.delivery_proven,b.state,r.execution_state,p.state FROM ai_assistant_effect_bindings b JOIN ai_assistant_plan_recipients r ON r.id=b.recipient_id JOIN ai_assistant_plans p ON p.id=r.plan_id WHERE b.external_effect_id=$1`, effects[0]).Scan(&proof, &bindingState, &recipientState, &planState); err != nil {
		t.Fatal(err)
	}
	if !proof || bindingState != string(ai.ExecutionDeliveryProven) || recipientState != string(ai.ExecutionDeliveryProven) || planState != string(ai.PlanCompleted) {
		t.Fatalf("old failure erased delivery proof: proof=%t binding=%s recipient=%s plan=%s", proof, bindingState, recipientState, planState)
	}
}
