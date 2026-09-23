package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// machineStore is intentionally additive. Existing human Store consumers keep
// their exact command and receipt serialization surface.
type machineStore interface {
	CreateMachinePlan(context.Context, aiassistantdomain.Plan, []aiassistantport.RecipientCandidate, aiassistantport.MachineActor, time.Time) (aiassistantport.Plan, []aiassistantport.Recipient, error)
	AppendMachineEvent(context.Context, aiassistantport.MachineEvent) error
}

type machineStatusStore interface {
	MachineExecutionSummary(context.Context, aiassistantport.PlanID) (aiassistantport.MachineExecutionSummary, error)
}

func (s *Service) machineStore() (machineStore, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	value, ok := s.store.(machineStore)
	return value, ok
}

func (s *Service) CreateMachinePlan(ctx context.Context, command aiassistantport.MachineCreatePlanCommand) (aiassistantport.MachineCreatePlanResult, error) {
	if s == nil || !command.Valid() {
		return aiassistantport.MachineCreatePlanResult{}, ErrInvalid
	}
	if _, ok := s.machineStore(); !ok {
		return aiassistantport.MachineCreatePlanResult{}, ErrUnavailable
	}
	var result aiassistantport.MachineCreatePlanResult
	err := s.uow.Within(ctx, func(tx context.Context) error {
		recipients, err := s.validateCanonicalRecipients(tx, command.Recipients)
		if err != nil {
			return err
		}
		return s.createMachineWithin(tx, command, recipients, &result)
	})
	return result, classify(err)
}

// CreateMachinePlanWithin keeps the AI review plan and its caller's durable
// fact inside one PostgreSQL Unit of Work. It never opens a nested transaction.
func (s *Service) CreateMachinePlanWithin(ctx context.Context, command aiassistantport.MachineCreatePlanCommand) (aiassistantport.MachineCreatePlanResult, error) {
	if s == nil || !command.Valid() {
		return aiassistantport.MachineCreatePlanResult{}, ErrInvalid
	}
	if _, ok := s.machineStore(); !ok {
		return aiassistantport.MachineCreatePlanResult{}, ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return aiassistantport.MachineCreatePlanResult{}, ErrUnavailable
	}
	recipients, err := s.validateCanonicalRecipients(ctx, command.Recipients)
	if err != nil {
		return aiassistantport.MachineCreatePlanResult{}, classify(err)
	}
	var result aiassistantport.MachineCreatePlanResult
	if err = s.createMachineWithin(ctx, command, recipients, &result); err != nil {
		return aiassistantport.MachineCreatePlanResult{}, classify(err)
	}
	return result, nil
}

func (s *Service) createMachineWithin(ctx context.Context, command aiassistantport.MachineCreatePlanCommand, recipients []aiassistantport.RecipientCandidate, result *aiassistantport.MachineCreatePlanResult) error {
	store, ok := s.machineStore()
	if !ok {
		return ErrUnavailable
	}
	payloadDigest := machineCreatePayload(command)
	receipt, owned, err := s.store.Reserve(ctx, Reservation{
		Operation:     "machine_plan_create",
		ActorScope:    command.Actor.Reference,
		KeyDigest:     sha256.Sum256([]byte(command.IdempotencyKey)),
		PayloadDigest: payloadDigest,
		CreatedAt:     command.OccurredAt,
	})
	if err != nil {
		return err
	}
	if !owned {
		planID := receiptPlanID(receipt.ResultSnapshot)
		if planID < 1 {
			return ErrConflict
		}
		plan, readErr := s.store.GetPlan(ctx, planID, false)
		if readErr != nil {
			return readErr
		}
		if plan.CreatedActorKind != aiassistantport.MachineActorKind || plan.CreatedActorRef != command.Actor.Reference {
			return ErrConflict
		}
		result.Plan, result.Replayed = machinePlanStatus(plan), true
		return nil
	}
	aggregate, err := aiassistantdomain.NewMachinePlan(command.Name, command.SourceKind, command.SourceDigest, len(recipients), command.Actor, command.OccurredAt)
	if err != nil {
		return ErrInvalid
	}
	plan, created, err := store.CreateMachinePlan(ctx, aggregate, recipients, command.Actor, command.OccurredAt)
	if err != nil {
		return err
	}
	for index := range created {
		if err = s.registerContentReferences(ctx, created[index].ContentVersionID, recipients[index].Content); err != nil {
			return err
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"plan_id":      plan.ID,
		"target_count": plan.TargetCount,
		"actor": map[string]string{
			"kind": aiassistantport.MachineActorKind,
			"ref":  command.Actor.Reference,
		},
	})
	if err = store.AppendMachineEvent(ctx, aiassistantport.MachineEvent{
		Type: aiassistantport.EventPlanCreated, AggregateID: plan.ID, Actor: command.Actor,
		IdempotencyKey: command.IdempotencyKey, Payload: payload, OccurredAt: command.OccurredAt,
	}); err != nil {
		return err
	}
	snapshot, _ := json.Marshal(map[string]any{"plan_id": plan.ID})
	if _, err = s.store.Complete(ctx, receipt.ID, snapshot, command.OccurredAt); err != nil {
		return err
	}
	result.Plan = machinePlanStatus(plan)
	return nil
}

func (s *Service) GetMachinePlan(ctx context.Context, actor aiassistantport.MachineActor, id aiassistantport.PlanID) (aiassistantport.MachinePlan, error) {
	if s == nil || !actor.Valid() || id < 1 {
		return aiassistantport.MachinePlan{}, ErrInvalid
	}
	var result aiassistantport.MachinePlan
	err := s.uow.Within(ctx, func(tx context.Context) error {
		plan, err := s.store.GetPlan(tx, id, false)
		if err != nil {
			return err
		}
		if plan.CreatedActorKind != aiassistantport.MachineActorKind || plan.CreatedActorRef != actor.Reference {
			return ErrNotFound
		}
		result = machinePlanStatus(plan)
		return nil
	})
	return result, classify(err)
}

func machineCreatePayload(command aiassistantport.MachineCreatePlanCommand) [32]byte {
	payload, _ := json.Marshal(struct {
		ActorScope string                                   `json:"actor_scope"`
		Command    aiassistantport.MachineCreatePlanCommand `json:"command"`
	}{ActorScope: command.Actor.Reference, Command: command})
	return sha256.Sum256(payload)
}

// GetMachineOperationStatus returns only a plan owned by actor. It derives the
// operation phase from existing plan and recipient execution facts in one
// read UoW, so a review approval is never represented as delivery completion.
func (s *Service) GetMachineOperationStatus(ctx context.Context, actor aiassistantport.MachineActor, id aiassistantport.PlanID) (aiassistantport.MachineOperationStatus, error) {
	if s == nil || !actor.Valid() || id < 1 {
		return aiassistantport.MachineOperationStatus{}, ErrInvalid
	}
	statusStore, ok := s.store.(machineStatusStore)
	if !ok {
		return aiassistantport.MachineOperationStatus{}, ErrUnavailable
	}
	var result aiassistantport.MachineOperationStatus
	err := s.uow.Within(ctx, func(tx context.Context) error {
		plan, err := s.store.GetPlan(tx, id, false)
		if err != nil {
			return err
		}
		if plan.CreatedActorKind != aiassistantport.MachineActorKind || plan.CreatedActorRef != actor.Reference {
			return ErrNotFound
		}
		summary, err := statusStore.MachineExecutionSummary(tx, id)
		if err != nil {
			return err
		}
		result = machineOperationStatus(plan, summary)
		return nil
	})
	return result, classify(err)
}

func machinePlanStatus(plan aiassistantport.Plan) aiassistantport.MachinePlan {
	review := aiassistantport.ReviewPending
	switch plan.State {
	case aiassistantport.PlanApproved, aiassistantport.PlanDispatching, aiassistantport.PlanNeedsAttention, aiassistantport.PlanCompletedWithFailures, aiassistantport.PlanCompleted:
		review = aiassistantport.ReviewApproved
	case aiassistantport.PlanRejected:
		review = aiassistantport.ReviewRejected
	}
	return aiassistantport.MachinePlan{
		ID: plan.ID, ReviewState: review, Version: plan.Version,
		TargetCount: plan.TargetCount, PendingCount: plan.PendingCount,
		ApprovedCount: plan.ApprovedCount, RejectedCount: plan.RejectedCount,
		IneligibleCount: plan.IneligibleCount, CreatedAt: plan.CreatedAt, UpdatedAt: plan.UpdatedAt,
	}
}

func machineOperationStatus(plan aiassistantport.Plan, summary aiassistantport.MachineExecutionSummary) aiassistantport.MachineOperationStatus {
	state := aiassistantport.MachineOperationPendingReview
	switch plan.State {
	case aiassistantport.PlanPartiallyApproved:
		state = aiassistantport.MachineOperationPartiallyApproved
	case aiassistantport.PlanApproved:
		state = aiassistantport.MachineOperationApproved
	case aiassistantport.PlanRejected:
		state = aiassistantport.MachineOperationRejected
	case aiassistantport.PlanDispatching:
		state = aiassistantport.MachineOperationDispatching
	case aiassistantport.PlanNeedsAttention:
		state = aiassistantport.MachineOperationNeedsAttention
		if summary.OutcomeUnknownCount > 0 {
			state = aiassistantport.MachineOperationOutcomeUnknown
		}
	case aiassistantport.PlanCompletedWithFailures:
		state = aiassistantport.MachineOperationCompletedFailures
	case aiassistantport.PlanCompleted:
		state = aiassistantport.MachineOperationCompleted
	}
	return aiassistantport.MachineOperationStatus{
		PlanID: plan.ID, ReviewState: machinePlanStatus(plan).ReviewState, OperationState: state,
		Version: plan.Version, TargetCount: plan.TargetCount,
		OutcomeUnknownCount: summary.OutcomeUnknownCount, RetryableFailureCount: summary.RetryableFailureCount,
		CreatedAt: plan.CreatedAt, UpdatedAt: plan.UpdatedAt,
	}
}

var _ aiassistantport.MachineIntake = (*Service)(nil)
var _ aiassistantport.MachineTransactionalIntake = (*Service)(nil)
var _ aiassistantport.MachineReader = (*Service)(nil)
