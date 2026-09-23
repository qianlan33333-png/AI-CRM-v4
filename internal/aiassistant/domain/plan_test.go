package domain

import (
	"testing"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func TestIndividualApprovalDoesNotMarkPlanApproved(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	plan, err := NewPlan("review", "automation", effectport.Hash("source"), 2, 9, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.ApplyRecipientDecision(aiassistantport.ReviewPending, aiassistantport.ReviewApproved, 1, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if plan.Projection.State != aiassistantport.PlanPartiallyApproved || plan.Projection.ApprovedCount != 1 || plan.Projection.PendingCount != 1 {
		t.Fatalf("unexpected projection: %+v", plan.Projection)
	}
	if plan.Projection.State == aiassistantport.PlanApproved {
		t.Fatal("individual approval must not cross the whole-plan dispatch gate")
	}
}

func TestWholePlanApprovalConsumesPendingTargets(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	plan, _ := NewPlan("review", "automation", effectport.Hash("source"), 3, 9, now)
	if err := plan.ApplyRecipientDecision(aiassistantport.ReviewPending, aiassistantport.ReviewRejected, 1, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := plan.MarkApproved(2, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if plan.Projection.State != aiassistantport.PlanApproved || plan.Projection.ApprovedCount != 2 || plan.Projection.RejectedCount != 1 || plan.Projection.PendingCount != 0 || !plan.Valid() {
		t.Fatalf("unexpected approved projection: %+v", plan.Projection)
	}
}

func TestStalePlanVersionIsRejected(t *testing.T) {
	plan, _ := NewPlan("review", "automation", effectport.Hash("source"), 1, 9, time.Now().UTC())
	if err := plan.MarkApproved(99, time.Now().UTC()); err == nil {
		t.Fatal("expected stale version conflict")
	}
}

func TestRestoreRejectsInvalidPersistedProjection(t *testing.T) {
	plan, _ := NewPlan("review", "automation", effectport.Hash("source"), 1, 9, time.Now().UTC())
	plan.Projection.NeedsAttentionCount = 2
	if _, err := Restore(plan.Projection); err == nil {
		t.Fatal("expected invalid persisted projection")
	}
}
