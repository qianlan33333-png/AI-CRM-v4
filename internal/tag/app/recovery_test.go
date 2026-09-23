package app

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"testing"
)

type recoveryStore struct {
	*providerMutationStore
	dispatch tagport.CatalogMutationDispatch
}

func (s *recoveryStore) ListCatalogMutationRecoveries(context.Context) ([]tagport.CatalogMutationRecovery, error) {
	return nil, s.check()
}
func (s *recoveryStore) LockCatalogMutationRecovery(_ context.Context, id int64) (tagport.CatalogMutationDispatch, error) {
	if id != 3 {
		return tagport.CatalogMutationDispatch{}, ErrNotFound
	}
	return s.dispatch, s.check()
}

type recoveryRetrier struct {
	uow   *catalogUOW
	calls []effectport.TagCatalogMutationRetryCommand
	err   error
}

func (r *recoveryRetrier) RetryTagCatalogMutationWithin(_ context.Context, c effectport.TagCatalogMutationRetryCommand) (effectport.Projection, error) {
	if !r.uow.in {
		return effectport.Projection{}, errors.New("outside UoW")
	}
	r.calls = append(r.calls, c)
	return effectport.Projection{ID: c.EffectID, State: effectport.StateQueued}, r.err
}
func TestRecoveryRequiresEnabledBoundaryAndRetainsOriginalEffect(t *testing.T) {
	uow := &catalogUOW{}
	store := &recoveryStore{providerMutationStore: &providerMutationStore{catalogStore: &catalogStore{uow: uow}}, dispatch: tagport.CatalogMutationDispatch{EffectRef: "eer_44", SourceRefDigest: string(effectport.Hash("original-source"))}}
	service := NewService(uow, store, store, nil, nil)
	retrier := &recoveryRetrier{uow: uow}
	if err := service.BindMutationRecovery(retrier); err != nil {
		t.Fatal(err)
	}
	command := domain.Command{Actor: 7, IdempotencyKey: "tag-retry-original-key"}
	if _, err := service.RetryMutation(context.Background(), 3, command); !errors.Is(err, ErrProviderMutationUnavailable) {
		t.Fatalf("gate=%v", err)
	}
	enqueuer := &providerMutationEnqueuer{}
	if err := service.BindProviderMutations(enqueuer); err != nil {
		t.Fatal(err)
	}
	result, err := service.RetryMutation(context.Background(), 3, command)
	if err != nil || result.ID != "eer_44" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err = service.RetryMutation(context.Background(), 3, command); err != nil {
		t.Fatal(err)
	}
	if len(retrier.calls) != 2 || retrier.calls[0] != retrier.calls[1] || enqueuer.calls != 0 || len(store.intents) != 0 {
		t.Fatal("recovery replaced original effect/intent/key")
	}
	retrier.err = effectport.ErrReconciliationConflict
	if _, err = service.RetryMutation(context.Background(), 3, command); !errors.Is(err, effectport.ErrReconciliationConflict) {
		t.Fatalf("unsafe outcome hidden=%v", err)
	}
}
