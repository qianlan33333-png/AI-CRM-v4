package app

import (
	"context"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"strconv"
)

func (s *Service) BindMutationRecovery(retrier effectport.TagCatalogMutationRetrier) error {
	if s == nil || retrier == nil || s.retrier != nil {
		return ErrUnavailable
	}
	s.retrier = retrier
	return nil
}
func (s *Service) MutationRecoveries(ctx context.Context) ([]tagport.CatalogMutationRecovery, error) {
	store, ok := s.store.(tagport.CatalogMutationRecoveryStore)
	if !ok {
		return []tagport.CatalogMutationRecovery{}, nil
	}
	var out []tagport.CatalogMutationRecovery
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		out, err = store.ListCatalogMutationRecoveries(tx)
		return err
	})
	return out, err
}
func (s *Service) RetryMutation(ctx context.Context, id int64, c domain.Command) (effectport.Projection, error) {
	if s == nil || s.retrier == nil || s.mutations == nil {
		return effectport.Projection{}, ErrProviderMutationUnavailable
	}
	store, ok := s.store.(tagport.CatalogMutationRecoveryStore)
	if !ok {
		return effectport.Projection{}, ErrUnavailable
	}
	if id < 1 || !domain.ValidCommand(c) {
		return effectport.Projection{}, ErrInvalidCommand
	}
	var result effectport.Projection
	err := s.uow.Within(ctx, func(tx context.Context) error {
		dispatch, err := store.LockCatalogMutationRecovery(tx, id)
		if err != nil {
			return err
		}
		result, err = s.retrier.RetryTagCatalogMutationWithin(tx, effectport.TagCatalogMutationRetryCommand{EffectID: dispatch.EffectRef, SourceRefDigest: effectport.Digest(dispatch.SourceRefDigest), ActorAdminUserID: c.Actor, ReceiptKey: effectport.Hash("tag.catalog.retry.v1", strconv.FormatInt(c.Actor, 10), strconv.FormatInt(id, 10), c.IdempotencyKey)})
		return err
	})
	return result, err
}
