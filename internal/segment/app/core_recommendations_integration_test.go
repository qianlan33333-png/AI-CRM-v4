package app

import (
	"context"
	"fmt"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"testing"
	"time"
)

type coreCanonical struct{}

func (coreCanonical) CanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return ids, nil
}

type coreContext struct{}

func (coreContext) FreezeGenerationContext(context.Context, customerdomain.CustomerID) (automationport.GenerationContext, error) {
	return automationport.GenerationContext{Questionnaire: "已填写问卷"}, nil
}
func (coreContext) GenerationModelPolicy(context.Context) (automationport.GenerationModelPolicy, error) {
	return automationport.GenerationModelPolicy{Mode: "enabled", Endpoint: "https://model.test/chat/completions", Model: "test", Temperature: .4}, nil
}

type coreEffects struct{ n int }

func (s *coreEffects) AcceptAndQueueWithin(ctx context.Context, c effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	if _, e := platformpostgres.RequireTransaction(ctx); e != nil {
		return effectport.Projection{}, effectport.Receipt{}, e
	}
	if !c.Envelope.Valid() || c.Envelope.Kind != effectport.KindAIRecommend {
		return effectport.Projection{}, effectport.Receipt{}, fmt.Errorf("bad envelope")
	}
	s.n++
	id := fmt.Sprintf("eer_core_%d", s.n)
	return effectport.Projection{ID: id}, effectport.Receipt{QueueReceiptID: "queue"}, nil
}
func TestPostgreSQLCoreRecommendationPreviewAndHumanPrecedence(t *testing.T) {
	ctx := context.Background()
	native, cleanup := scheduleRuntimeDatabase(t, ctx)
	defer cleanup()
	applySegmentRuntimeMigration(t, native, "0183_segment_core_operations.sql")
	applySegmentRuntimeMigration(t, native, "0200_segment_core_recommendation_failure_code.sql")
	pool, e := platformpostgres.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := segmentstore.NewPostgreSQL(native, uow)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(uow, repo)
	core := NewCoreOperations(s, repo, coreCanonical{}, nil)
	effects := &coreEffects{}
	core.BindRecommendationRuntime(effects, coreContext{}, coreContext{})
	for i := int64(1); i <= 2; i++ {
		p, e := s.CreatePackage(ctx, PackageCreateCommand{Name: fmt.Sprintf("产品%d", i), TemplateKey: "active_contacts", Actor: 7, IdempotencyKey: fmt.Sprintf("core-test-package-%d", i)})
		if e != nil {
			t.Fatal(e)
		}
		_, e = core.PutProduct(ctx, CoreProductCommand{Product: segmentport.CoreProduct{ID: i, PackageID: p.ID, Name: "产品", Description: "描述", Enabled: true}, Actor: 7, IdempotencyKey: fmt.Sprintf("core-test-product-%d", i)})
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e = core.SavePrompt(ctx, CorePromptCommand{Body: "根据问卷选择产品", ExpectedVersion: 1, Publish: true, Actor: 7, IdempotencyKey: "core-test-prompt-v1"}); e != nil {
		t.Fatal(e)
	}
	preview, e := core.Recommend(ctx, CoreRecommendCommand{CustomerIDs: []int64{100}, Preview: true, Actor: 7, IdempotencyKey: "core-test-preview-1"})
	if e != nil {
		t.Fatal(e)
	}
	finish := func(effect string) {
		t.Helper()
		e = uow.Within(ctx, func(tx context.Context) error {
			return core.CompleteGeneration(tx, automationport.GenerationCompletion{EffectID: effect, State: effectport.StateExecuted, Artifact: effectport.ResultArtifact{Payload: []byte(`{"product_id":1,"reason":"适合","evidence":"问卷"}`)}, CompletedAt: time.Now().UTC()})
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	finish("eer_core_1")
	v, e := core.Recommendation(ctx, preview.Items[0].ID)
	if e != nil || v.State != "previewed" {
		t.Fatalf("preview %+v %v", v, e)
	}
	batch, e := core.Recommend(ctx, CoreRecommendCommand{CustomerIDs: []int64{100}, Actor: 7, IdempotencyKey: "core-test-assignment-1"})
	if e != nil {
		t.Fatal(e)
	}
	human, e := core.ChangeAssignment(ctx, CoreAssignmentCommand{CustomerID: 100, CoreProductID: 2, Reason: "人工判断", Actor: 7, IdempotencyKey: "core-test-human-1"})
	if e != nil {
		t.Fatal(e)
	}
	finish("eer_core_2")
	v, e = core.Recommendation(ctx, batch.Items[0].ID)
	if e != nil || v.State != "stale" {
		t.Fatalf("stale %+v %v", v, e)
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		a, e := repo.CoreAssignment(tx, 100)
		if e != nil {
			return e
		}
		if a.ID != human.ID || a.CoreProductID != 2 {
			t.Fatal("AI overwrote manual direction")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Replay preserves one job even after the prompt is edited.
	_, e = core.Recommend(ctx, CoreRecommendCommand{CustomerIDs: []int64{100}, Actor: 7, IdempotencyKey: "core-test-assignment-1"})
	if e != nil || effects.n != 2 {
		t.Fatalf("replay effects=%d err=%v", effects.n, e)
	}
	assigned, e := core.Recommend(ctx, CoreRecommendCommand{CustomerIDs: []int64{101}, Actor: 7, IdempotencyKey: "core-test-assignment-success"})
	if e != nil {
		t.Fatal(e)
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		return core.CompleteGeneration(tx, automationport.GenerationCompletion{EffectID: "eer_core_3", State: effectport.StateRetryable, FailureCode: "generation_call_unknown", CompletedAt: time.Now().UTC()})
	})
	if e != nil {
		t.Fatal(e)
	}
	uncertain, e := core.Recommendation(ctx, assigned.Items[0].ID)
	if e != nil || uncertain.FailureCode != "generation_call_unknown" || uncertain.State != string(effectport.StateRetryable) {
		t.Fatalf("failure code was not persisted: %+v err=%v", uncertain, e)
	}
	if _, found, e := core.GenerationDispatch(ctx, "eer_core_3"); e != nil || !found {
		t.Fatalf("retry lost frozen dispatch: %v", e)
	}
	finish("eer_core_3")
	v, e = core.Recommendation(ctx, assigned.Items[0].ID)
	if e != nil || v.State != "assigned" {
		t.Fatalf("assignment=%+v err=%v", v, e)
	}
	page, e := core.CoreMembers(ctx, 1, "", 50)
	if e != nil || len(page.Items) != 1 || page.Items[0].CustomerID != 101 || page.Items[0].Operations == nil {
		t.Fatalf("existing members=%+v err=%v", page, e)
	}
	// Replaying a completion cannot create a new assignment or move the customer.
	finish("eer_core_3")
	var count int
	if e = native.QueryRow(ctx, `SELECT count(*) FROM segment_core_assignments WHERE customer_id=101`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("duplicate completion count=%d err=%v", count, e)
	}

}
