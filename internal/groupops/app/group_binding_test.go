package app

import (
	"context"
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	"testing"
)

type bindingOwners map[string]int64

func (b bindingOwners) ResolveExecutionOwner(_ context.Context, ref string) (int64, bool, error) {
	owner, ok := b[ref]
	return owner, ok, nil
}

func TestPausedGroupBindingsValidateOwnerReplayAndCAS(t *testing.T) {
	s, store, _ := newTestService()
	ctx := context.Background()
	s.groupOwners = bindingOwners{"owned": 19, "other": 20}
	d, err := s.Create(ctx, p.CreatePlanCommand{Name: "Bindings", Actor: 7, IdempotencyKey: "groupops-binding-create"})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.AddMember(ctx, p.MemberCommand{PlanID: d.Plan.ID, ExpectedRevision: d.Plan.Revision, StaffID: 19, Actor: 7, IdempotencyKey: "groupops-binding-member"})
	if err != nil {
		t.Fatal(err)
	}
	d.Plan.Status = p.PlanPaused
	store.details[d.Plan.ID] = d
	command := p.GroupAssetCommand{PlanID: d.Plan.ID, ExpectedRevision: d.Plan.Revision, AssetRef: "owned", Actor: 7, IdempotencyKey: "groupops-binding-add"}
	saved, err := s.AddGroupAsset(ctx, command)
	if err != nil || saved.Plan.Status != p.PlanPaused || len(saved.GroupAssets) != 1 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	replay, err := s.AddGroupAsset(ctx, command)
	if err != nil || replay.Plan.Revision != saved.Plan.Revision {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	command.IdempotencyKey = "groupops-binding-stale"
	if _, err = s.AddGroupAsset(ctx, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale=%v", err)
	}
	command.ExpectedRevision = saved.Plan.Revision
	command.AssetRef = "other"
	command.IdempotencyKey = "groupops-binding-other"
	if _, err = s.AddGroupAsset(ctx, command); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other owner=%v", err)
	}
	command.AssetRef = "missing"
	command.IdempotencyKey = "groupops-binding-missing"
	if _, err = s.AddGroupAsset(ctx, command); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing=%v", err)
	}
	// Historic bindings remain removable even after ownership changes.
	s.groupOwners = bindingOwners{"owned": 20}
	command.AssetRef = "owned"
	command.IdempotencyKey = "groupops-binding-remove"
	removed, err := s.RemoveGroupAsset(ctx, command)
	if err != nil || len(removed.GroupAssets) != 0 || removed.Plan.Status != p.PlanPaused {
		t.Fatalf("remove=%+v err=%v", removed, err)
	}
}
