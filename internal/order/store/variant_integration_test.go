package store

import (
	"context"
	"testing"
	"time"

	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLOrderVariantSnapshotSurvivesReadsAndReplay(t *testing.T) {
	pool, cleanup := orderIntegrationPool(t)
	defer cleanup()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repo)
	cmd := nativeCommand("variant-snapshot")
	id := int64(5)
	cmd.Input.Items[0].ProductID = &id
	cmd.Input.Items[0].Variant = &domain.VariantSnapshot{ID: 23, Version: 4, Name: "三人成行版", Description: "购买时说明", Attributes: []domain.VariantAttribute{{Key: "版本", Value: "A"}}}
	first, err := service.Create(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), first.ID)
	if err != nil || got.Items[0].Variant == nil || got.Items[0].Variant.ID != 23 || got.Items[0].DisplayName() != "课程 · 三人成行版" {
		t.Fatal(got, err)
	}
	replay, err := service.Create(context.Background(), cmd)
	if err != nil || replay.ID != first.ID || replay.Items[0].Variant.Version != 4 {
		t.Fatal(replay, err)
	}
	got.Items[0].Variant.Attributes[0].Value = "changed"
	again, err := service.Get(context.Background(), first.ID)
	if err != nil || again.Items[0].Variant.Attributes[0].Value != "A" {
		t.Fatal("snapshot mutated", err)
	}
}
