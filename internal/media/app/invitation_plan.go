package app

import (
	"context"
	"errors"
	e "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	g "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	d "github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	"time"
)

type InvitationStore interface {
	ReadInvitationPlan(context.Context, int64) (p.InvitationPlan, error)
	ReadPublicInvitation(context.Context, string) (p.InvitationPlan, error)
	InvitationPlanIDs(context.Context, bool) ([]int64, error)
	SaveInvitationPlan(context.Context, p.InvitationInput, int64, string, string, e.TransactionalAccepter) (p.InvitationPlan, error)
	ApplyInvitationEvaluation(context.Context, p.InvitationPlan, p.InvitationPlan) error
	InvitationHistory(context.Context, int64) ([]p.InvitationSwitch, error)
}
type InvitationService struct {
	Store        InvitationStore
	Catalog      g.Catalog
	Effects      e.TransactionalAccepter
	Origin       string
	WriteEnabled bool
}

func (s *InvitationService) Save(ctx context.Context, v p.InvitationInput, actor int64, key string) (p.InvitationPlan, error) {
	if d.ValidateInvitationInput(v) != nil {
		return p.InvitationPlan{}, d.ErrInvitationPlan
	}
	if !s.WriteEnabled {
		old, err := s.Store.ReadInvitationPlan(ctx, v.ID)
		if err != nil || old.Token == "" {
			return p.InvitationPlan{}, errors.New("invitation code provider disabled")
		}
		known := map[string]bool{}
		for _, b := range old.Bindings {
			known[b.ChatID] = true
		}
		for _, id := range v.ChatIDs {
			if !known[id] {
				return p.InvitationPlan{}, errors.New("invitation code provider disabled")
			}
		}
	}
	for _, id := range v.ChatIDs {
		if !v.Enabled {
			continue
		}
		fact, err := s.Catalog.ReadCatalogGroup(ctx, id)
		if err == nil && (fact.ObservedAt == nil || time.Since(*fact.ObservedAt) >= 5*time.Minute) {
			fact, err = s.Catalog.RefreshCatalogGroup(ctx, id)
		}
		if err != nil || fact.ObservedAt == nil {
			return p.InvitationPlan{}, errors.New("group details unavailable")
		}
	}
	plan, err := s.Store.SaveInvitationPlan(ctx, v, actor, key, s.Origin, s.Effects)
	if err != nil {
		return plan, err
	}
	if err = s.evaluate(ctx, plan); err != nil {
		return plan, err
	}
	return s.Store.ReadInvitationPlan(ctx, plan.ID)
}
func (s *InvitationService) evaluate(ctx context.Context, plan p.InvitationPlan) error {
	facts := map[string]g.CatalogGroup{}
	for _, b := range plan.Bindings {
		if b.Retired {
			continue
		}
		v, err := s.Catalog.ReadCatalogGroup(ctx, b.ChatID)
		if err == nil {
			facts[b.ChatID] = v
		}
	}
	next := d.EvaluateInvitation(plan, facts, time.Now().UTC())
	if withEffects, ok := s.Store.(interface {
		ApplyInvitationEvaluationWithEffects(context.Context, p.InvitationPlan, p.InvitationPlan, e.TransactionalAccepter) error
	}); ok && s.Effects != nil {
		return withEffects.ApplyInvitationEvaluationWithEffects(ctx, plan, next, s.Effects)
	}
	return s.Store.ApplyInvitationEvaluation(ctx, plan, next)
}
func (s *InvitationService) Refresh(ctx context.Context) error {
	ids, err := s.Store.InvitationPlanIDs(ctx, true)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var failures []error
	for _, id := range ids {
		plan, err := s.Store.ReadInvitationPlan(ctx, id)
		if err != nil {
			return err
		}
		for _, b := range plan.Bindings {
			if b.Retired || seen[b.ChatID] {
				continue
			}
			seen[b.ChatID] = true
			fact, err := s.Catalog.ReadCatalogGroup(ctx, b.ChatID)
			if err != nil || fact.ObservedAt == nil || time.Since(*fact.ObservedAt) >= 5*time.Minute {
				if _, err = s.Catalog.RefreshCatalogGroup(ctx, b.ChatID); err != nil {
					failures = append(failures, err)
				}
			}
		}
		if err = s.evaluate(ctx, plan); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (s *InvitationService) Public(ctx context.Context, token string) (p.InvitationPlan, error) {
	// Compare-and-swap prevents public reads from overwriting a concurrent edit.
	// Only local observations are used; this path never calls WeCom.
	for attempt := 0; attempt < 3; attempt++ {
		plan, err := s.Store.ReadPublicInvitation(ctx, token)
		if err != nil {
			return plan, err
		}
		if err = s.evaluate(ctx, plan); errors.Is(err, p.ErrInvitationVersion) {
			continue
		} else if err != nil {
			return p.InvitationPlan{}, err
		}
		return s.Store.ReadPublicInvitation(ctx, token)
	}
	return p.InvitationPlan{}, p.ErrInvitationVersion
}
