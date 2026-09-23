package app

import (
	"context"
	"strconv"
	"testing"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type runProjectionPlans struct {
	plan       aiassistantport.Plan
	recipients []aiassistantport.Recipient
}

func (p runProjectionPlans) CreatePlanWithin(context.Context, aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error) {
	return aiassistantport.CreatePlanResult{}, nil
}
func (p runProjectionPlans) GetPlan(context.Context, aiassistantport.PlanID) (aiassistantport.Plan, error) {
	return p.plan, nil
}
func (p runProjectionPlans) ListPlans(context.Context, aiassistantport.PlanListQuery) (aiassistantport.PlanPage, error) {
	return aiassistantport.PlanPage{}, nil
}
func (p runProjectionPlans) GetRecipient(context.Context, aiassistantport.PlanID, aiassistantport.RecipientID) (aiassistantport.Recipient, aiassistantport.ContentVersion, error) {
	return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, nil
}
func (p runProjectionPlans) ListRecipients(_ context.Context, query aiassistantport.RecipientPageQuery) (aiassistantport.RecipientPage, error) {
	if query.Limit != 0 {
		return aiassistantport.RecipientPage{}, strconv.ErrSyntax
	}
	start := 0
	if query.Cursor != "" {
		var err error
		start, err = strconv.Atoi(query.Cursor)
		if err != nil {
			return aiassistantport.RecipientPage{}, err
		}
	}
	end := start + 50
	if end > len(p.recipients) {
		end = len(p.recipients)
	}
	page := aiassistantport.RecipientPage{Items: append([]aiassistantport.Recipient(nil), p.recipients[start:end]...)}
	if end < len(p.recipients) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func TestProjectAIPlanNeedsAttentionDistinguishesRetryableAndUnknown(t *testing.T) {
	for _, test := range []struct {
		name        string
		recipients  []aiassistantport.Recipient
		wantState   automationport.RunState
		wantUnknown int64
	}{
		{name: "retryable", recipients: []aiassistantport.Recipient{{ID: 1, ExecutionState: aiassistantport.ExecutionRetryableFailed}}, wantState: automationport.RunPartialFailed},
		{name: "unknown", recipients: []aiassistantport.Recipient{{ID: 1, ExecutionState: aiassistantport.ExecutionOutcomeUnknown}, {ID: 2, ExecutionState: aiassistantport.ExecutionRetryableFailed}}, wantState: automationport.RunOutcomeUnknown, wantUnknown: 1},
		{name: "second page unknown", recipients: append(func() []aiassistantport.Recipient {
			out := make([]aiassistantport.Recipient, 50)
			for index := range out {
				out[index] = aiassistantport.Recipient{ID: aiassistantport.RecipientID(index + 1), ExecutionState: aiassistantport.ExecutionRetryableFailed}
			}
			return out
		}(), aiassistantport.Recipient{ID: 51, ExecutionState: aiassistantport.ExecutionOutcomeUnknown}), wantState: automationport.RunOutcomeUnknown, wantUnknown: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &RuntimeService{reviewPlans: runProjectionPlans{plan: aiassistantport.Plan{ID: 7, State: aiassistantport.PlanNeedsAttention}, recipients: test.recipients}}
			run := automationdomain.RuntimeRun{AIPlanID: 7}
			if err := service.projectAIPlanState(context.Background(), &run); err != nil || run.State != test.wantState || run.OutcomeUnknownCount != test.wantUnknown {
				t.Fatalf("run=%+v err=%v", run, err)
			}
		})
	}
}
