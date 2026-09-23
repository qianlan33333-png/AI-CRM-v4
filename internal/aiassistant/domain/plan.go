// Package domain owns AI Assistant review invariants. It has no HTTP,
// persistence, queue or provider dependency.
package domain

import (
	"errors"
	"strings"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var (
	ErrInvalidPlan    = errors.New("invalid AI Assistant plan")
	ErrPlanConflict   = errors.New("AI Assistant plan conflict")
	ErrRecipientMatch = errors.New("AI Assistant recipient does not belong to plan")
)

type Plan struct {
	Projection aiassistantport.Plan
}

func NewPlan(name, sourceKind string, sourceDigest effectport.Digest, targetCount int, actor int64, now time.Time) (Plan, error) {
	if strings.TrimSpace(name) == "" || len(name) > 200 || strings.TrimSpace(sourceKind) == "" || len(sourceKind) > 80 || !effectport.ValidDigest(sourceDigest) || targetCount < 1 || targetCount > aiassistantport.MaxRecipients || actor < 1 || now.IsZero() {
		return Plan{}, ErrInvalidPlan
	}
	return Plan{Projection: aiassistantport.Plan{
		Name: name, SourceKind: sourceKind, SourceDigest: sourceDigest,
		State: aiassistantport.PlanPendingReview, Version: 1,
		TargetCount: targetCount, PendingCount: targetCount,
		CreatedBy: actor, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}}, nil
}

// NewMachinePlan preserves a machine subject as an opaque reference. It never
// substitutes a positive administrator ID for that caller.
func NewMachinePlan(name, sourceKind string, sourceDigest effectport.Digest, targetCount int, actor aiassistantport.MachineActor, now time.Time) (Plan, error) {
	if !actor.Valid() || strings.TrimSpace(name) == "" || len(name) > 200 || strings.TrimSpace(sourceKind) == "" || len(sourceKind) > 80 || !effectport.ValidDigest(sourceDigest) || targetCount < 1 || targetCount > aiassistantport.MaxRecipients || now.IsZero() {
		return Plan{}, ErrInvalidPlan
	}
	return Plan{Projection: aiassistantport.Plan{
		Name: name, SourceKind: sourceKind, SourceDigest: sourceDigest,
		State: aiassistantport.PlanPendingReview, Version: 1,
		TargetCount: targetCount, PendingCount: targetCount,
		CreatedActorKind: actor.Kind, CreatedActorRef: actor.Reference,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}}, nil
}

func Restore(projection aiassistantport.Plan) (Plan, error) {
	plan := Plan{Projection: projection}
	if !plan.Valid() {
		return Plan{}, ErrInvalidPlan
	}
	return plan, nil
}

func (p Plan) Valid() bool {
	v := p.Projection
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 200 || strings.TrimSpace(v.SourceKind) == "" || len(v.SourceKind) > 80 || !effectport.ValidDigest(v.SourceDigest) || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.Version < 1 || v.TargetCount < 1 || v.TargetCount > aiassistantport.MaxRecipients || v.PendingCount < 0 || v.ApprovedCount < 0 || v.RejectedCount < 0 || v.IneligibleCount < 0 || v.NeedsAttentionCount < 0 || v.NeedsAttentionCount > v.TargetCount || v.PendingCount+v.ApprovedCount+v.RejectedCount+v.IneligibleCount != v.TargetCount {
		return false
	}
	if v.CreatedActorKind == aiassistantport.MachineActorKind {
		if v.CreatedBy != 0 || !(aiassistantport.MachineActor{Kind: v.CreatedActorKind, Reference: v.CreatedActorRef}).Valid() {
			return false
		}
	} else if v.CreatedBy < 1 {
		return false
	}
	switch v.State {
	case aiassistantport.PlanPendingReview, aiassistantport.PlanPartiallyApproved, aiassistantport.PlanApproved, aiassistantport.PlanRejected, aiassistantport.PlanDispatching, aiassistantport.PlanNeedsAttention, aiassistantport.PlanCompletedWithFailures, aiassistantport.PlanCompleted:
		return true
	default:
		return false
	}
}

func (p *Plan) ApplyRecipientDecision(previous, next aiassistantport.ReviewState, expectedVersion int64, now time.Time) error {
	if p == nil || !p.Valid() || expectedVersion != p.Projection.Version || now.IsZero() || !reviewablePlanState(p.Projection.State) || previous == next || (next != aiassistantport.ReviewApproved && next != aiassistantport.ReviewRejected) {
		return ErrPlanConflict
	}
	if err := decrement(&p.Projection, previous); err != nil {
		return err
	}
	increment(&p.Projection, next)
	p.Projection.Version++
	p.Projection.UpdatedAt = now.UTC()
	if p.Projection.PendingCount == 0 {
		if p.Projection.ApprovedCount == 0 {
			p.Projection.State = aiassistantport.PlanRejected
		} else {
			p.Projection.State = aiassistantport.PlanPartiallyApproved
		}
	} else if p.Projection.ApprovedCount > 0 || p.Projection.RejectedCount > 0 {
		p.Projection.State = aiassistantport.PlanPartiallyApproved
	}
	if !p.Valid() {
		return ErrInvalidPlan
	}
	return nil
}

func (p *Plan) MarkApproved(expectedVersion int64, now time.Time) error {
	if p == nil || !p.Valid() || expectedVersion != p.Projection.Version || now.IsZero() || !reviewablePlanState(p.Projection.State) || p.Projection.ApprovedCount+p.Projection.PendingCount < 1 {
		return ErrPlanConflict
	}
	p.Projection.ApprovedCount += p.Projection.PendingCount
	p.Projection.PendingCount = 0
	p.Projection.State = aiassistantport.PlanApproved
	p.Projection.Version++
	p.Projection.UpdatedAt = now.UTC()
	return nil
}

func (p *Plan) MarkRejected(expectedVersion int64, now time.Time) error {
	if p == nil || !p.Valid() || expectedVersion != p.Projection.Version || now.IsZero() || !reviewablePlanState(p.Projection.State) {
		return ErrPlanConflict
	}
	p.Projection.RejectedCount += p.Projection.PendingCount + p.Projection.ApprovedCount
	p.Projection.PendingCount, p.Projection.ApprovedCount = 0, 0
	p.Projection.State = aiassistantport.PlanRejected
	p.Projection.Version++
	p.Projection.UpdatedAt = now.UTC()
	return nil
}

func reviewablePlanState(state aiassistantport.PlanState) bool {
	return state == aiassistantport.PlanPendingReview || state == aiassistantport.PlanPartiallyApproved
}

func decrement(plan *aiassistantport.Plan, state aiassistantport.ReviewState) error {
	switch state {
	case aiassistantport.ReviewPending:
		if plan.PendingCount < 1 {
			return ErrInvalidPlan
		}
		plan.PendingCount--
	case aiassistantport.ReviewApproved:
		if plan.ApprovedCount < 1 {
			return ErrInvalidPlan
		}
		plan.ApprovedCount--
	case aiassistantport.ReviewRejected:
		if plan.RejectedCount < 1 {
			return ErrInvalidPlan
		}
		plan.RejectedCount--
	case aiassistantport.ReviewIneligible:
		if plan.IneligibleCount < 1 {
			return ErrInvalidPlan
		}
		plan.IneligibleCount--
	default:
		return ErrInvalidPlan
	}
	return nil
}

func increment(plan *aiassistantport.Plan, state aiassistantport.ReviewState) {
	switch state {
	case aiassistantport.ReviewApproved:
		plan.ApprovedCount++
	case aiassistantport.ReviewRejected:
		plan.RejectedCount++
	}
}
