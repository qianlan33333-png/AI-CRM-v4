package app

import (
	"context"
	"testing"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

// Embed the full Store contract so this focused test only replaces the three
// owner calls made after EER has opened its completion transaction.
type dynamicCompletionStoreStub struct {
	RuntimeStore
	run      automationdomain.RuntimeRun
	items    []automationdomain.GenerationItem
	attachID int64
}

func (s *dynamicCompletionStoreStub) SettleGeneration(context.Context, automationport.GenerationCompletion) (automationdomain.GenerationItem, automationdomain.RuntimeRun, bool, error) {
	return s.items[0], s.run, true, nil
}
func (s *dynamicCompletionStoreStub) GenerationItemsForPlan(context.Context, int64) ([]automationdomain.GenerationItem, error) {
	return append([]automationdomain.GenerationItem(nil), s.items...), nil
}
func (s *dynamicCompletionStoreStub) AttachGenerationPlan(_ context.Context, runID, planID int64, _ time.Time) error {
	if runID != s.run.ID {
		return ErrRuntimeConflict
	}
	s.attachID = planID
	return nil
}

type dynamicReviewPlanStub struct {
	aiassistantport.TransactionalIntake
	aiassistantport.Reader
	command aiassistantport.CreatePlanCommand
}

func (s *dynamicReviewPlanStub) CreatePlanWithin(_ context.Context, command aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error) {
	s.command = command
	return aiassistantport.CreatePlanResult{Plan: aiassistantport.Plan{ID: 73}}, nil
}

func TestCompleteGenerationCreatesOneExistingAIPendingReviewPlanForSuccessfulItems(t *testing.T) {
	store := &dynamicCompletionStoreStub{
		run:   automationdomain.RuntimeRun{ID: 17, PackageID: 9, CreatedBy: 42, PreviewDigest: generationDigest("preview")},
		items: []automationdomain.GenerationItem{{CustomerID: 501, SenderStaffID: 8, GeneratedText: "根据你的目标，建议先完成第一步。"}},
	}
	plans := &dynamicReviewPlanStub{}
	service := &RuntimeService{store: store, reviewPlans: plans}
	completedAt := time.Date(2026, 9, 8, 8, 1, 0, 0, time.UTC)
	if err := service.CompleteGeneration(context.Background(), automationport.GenerationCompletion{EffectID: "eer_1", State: effectport.StateExecuted, Attempt: effectport.Attempt{EffectID: "eer_1", Number: 1}, ReceiptDigest: effectport.Hash("completion"), CompletedAt: completedAt}); err != nil {
		t.Fatal(err)
	}
	if store.attachID != 73 || plans.command.Actor != (aiassistantport.Actor{Kind: aiassistantport.ActorService, ID: 42}) || plans.command.SourceKind != "automation.dynamic_text_generation.v1" || plans.command.IdempotencyKey != "automation-dynamic-review-17" || len(plans.command.Recipients) != 1 || plans.command.Recipients[0].CustomerID != 501 || plans.command.Recipients[0].StaffID != 8 || len(plans.command.Recipients[0].Content) != 1 || plans.command.Recipients[0].Content[0].Text != store.items[0].GeneratedText || !plans.command.OccurredAt.Equal(completedAt) {
		t.Fatalf("attach=%d command=%+v", store.attachID, plans.command)
	}
}
