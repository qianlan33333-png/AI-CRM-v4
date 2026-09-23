package store

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLTagRecoveryRetainsOriginalDispatchAndFencesQueued(t *testing.T) {
	pool, cleanup := tagIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := tagapp.NewService(uow, repo, repo, repo, repo)
	group, tag, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "recovery-create-test", GroupName: "Recovery", FirstTagName: "First"})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := reserveCatalogMutation(t, ctx, uow, repo, tagport.CatalogMutationPlan{Operation: tagport.CatalogGroupCreate, Actor: 7, IdempotencyKey: "recovery-create-test", GroupID: group.ID, TagID: tag.ID, GroupName: group.Name, TagName: tag.Name})
	completion := tagport.CatalogMutationCompletion{EffectRef: dispatch.EffectRef, State: "final_failed", ResultDigest: "sha256:" + strings.Repeat("a", 64), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()}
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.CompleteCatalogMutation(tx, completion) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		rows, e := repo.ListCatalogMutationRecoveries(tx)
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			t.Fatalf("recoveries=%+v", rows)
		}
		got, e := repo.LockCatalogMutationRecovery(tx, rows[0].ID)
		if e != nil {
			return e
		}
		if got.EffectRef != dispatch.EffectRef || got.GroupID != group.ID || got.TagID != tag.ID {
			t.Fatalf("recovery changed dispatch %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	completion.State = "queued"
	completion.ResultDigest = "sha256:" + strings.Repeat("b", 64)
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.CompleteCatalogMutation(tx, completion) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-generation reset=%v", err)
	}
	completion.Generation = 2
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.CompleteCatalogMutation(tx, completion) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.CompleteCatalogMutation(tx, completion) }); err != nil {
		t.Fatalf("queued replay=%v", err)
	}
	// The original dispatch survives requeue with no replacement group/tag.
	if err = uow.Within(ctx, func(tx context.Context) error {
		got, e := repo.ReadCatalogMutationDispatch(tx, dispatch.SourceRefDigest)
		if e == nil && got.EffectRef != dispatch.EffectRef {
			t.Fatal("effect replaced")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	completion.State = "outcome_unknown"
	completion.Attempt = 2
	completion.Fence = 2
	completion.ResultDigest = "sha256:" + strings.Repeat("c", 64)
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.CompleteCatalogMutation(tx, completion) }); err != nil {
		t.Fatal(err)
	}
	completion.State = "queued"
	completion.Generation = 3
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.CompleteCatalogMutation(tx, completion) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown reset=%v", err)
	}
}

type rollbackRecoveryRetrier struct {
	repository *Repository
	completion tagport.CatalogMutationCompletion
}

func (r rollbackRecoveryRetrier) RetryTagCatalogMutationWithin(ctx context.Context, c effectport.TagCatalogMutationRetryCommand) (effectport.Projection, error) {
	if err := r.repository.CompleteCatalogMutation(ctx, r.completion); err != nil {
		return effectport.Projection{}, err
	}
	return effectport.Projection{}, effectport.ErrReconciliationConflict
}
func TestPostgreSQLTagRecoveryConflictRollsBackOwnerProjectionAndReleasesLock(t *testing.T) {
	pool, cleanup := tagIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, _ := platformpostgres.Wrap(pool, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapped)
	repo, _ := NewPostgreSQL(pool, uow)
	service := tagapp.NewService(uow, repo, repo, repo, repo)
	group, tag, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "recovery-rollback-create", GroupName: "Rollback", FirstTagName: "First"})
	if err != nil {
		t.Fatal(err)
	}
	d := reserveCatalogMutation(t, ctx, uow, repo, tagport.CatalogMutationPlan{Operation: tagport.CatalogGroupCreate, Actor: 7, IdempotencyKey: "recovery-rollback-create", GroupID: group.ID, TagID: tag.ID, GroupName: group.Name, TagName: tag.Name})
	c := tagport.CatalogMutationCompletion{EffectRef: d.EffectRef, State: "final_failed", ResultDigest: "sha256:" + strings.Repeat("a", 64), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()}
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.CompleteCatalogMutation(tx, c) }); err != nil {
		t.Fatal(err)
	}
	c.State = "queued"
	c.Generation = 2
	c.ResultDigest = "sha256:" + strings.Repeat("b", 64)
	if err = service.BindProviderMutations(serialCatalogMutationEnqueuer{}); err != nil {
		t.Fatal(err)
	}
	if err = service.BindMutationRecovery(rollbackRecoveryRetrier{repo, c}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RetryMutation(ctx, d.ID, domain.Command{Actor: 7, IdempotencyKey: "recovery-rollback-retry"}); !errors.Is(err, effectport.ErrReconciliationConflict) {
		t.Fatalf("retry=%v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		got, e := repo.GetTag(tx, tag.ID)
		if e != nil {
			return e
		}
		if got.ProviderMutationState != "final_failed" {
			t.Fatalf("owner state escaped rollback: %+v", got)
		}
		_, e = repo.LockCatalogMutationRecovery(tx, d.ID)
		return e
	}); err != nil {
		t.Fatalf("rollback did not release owner locks: %v", err)
	}
}
