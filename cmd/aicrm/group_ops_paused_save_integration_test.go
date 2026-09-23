package main

import (
	"context"
	"errors"
	"testing"
	"time"

	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestGroupOpsPostgreSQLPausedBasicSave(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var actor, inactive int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,login_enabled) VALUES('paused-save','$argon2id$test','Operator',true,false) RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,login_enabled) VALUES('paused-inactive','$argon2id$test','Inactive',false,false) RETURNING id`).Scan(&inactive); err != nil {
		t.Fatal(err)
	}
	store, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := groupopsapp.NewService(uow, store, groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: store}, store)
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Imported disabled plan", Actor: actor, IdempotencyKey: "paused-save-create"})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy imports are paused with no local owner. Reproduce that owner-owned
	// definition without enabling an incomplete plan just to make it editable.
	if err = uow.Within(ctx, func(tx context.Context) error {
		detail.Plan.Status = groupopsport.PlanPaused
		return store.Save(tx, detail)
	}); err != nil {
		t.Fatal(err)
	}
	command := groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: "Configured disabled plan", OwnerStaffID: actor, OwnerStaffIDSet: true, Actor: actor, IdempotencyKey: "paused-save-owner"}
	saved, err := service.Update(ctx, command)
	if err != nil || saved.Plan.Status != groupopsport.PlanPaused || saved.Plan.Revision != detail.Plan.Revision+1 || len(saved.Members) != 1 || saved.Members[0].StaffID != actor {
		t.Fatalf("save=%+v err=%v", saved, err)
	}
	replay, err := service.Update(ctx, command)
	if err != nil || replay.Plan.Revision != saved.Plan.Revision {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	command.IdempotencyKey = "paused-save-stale"
	if _, err = service.Update(ctx, command); !errors.Is(err, groupopsapp.ErrConflict) {
		t.Fatalf("stale=%v", err)
	}
	command.ExpectedRevision = saved.Plan.Revision
	command.IdempotencyKey = "paused-save-inactive"
	command.OwnerStaffID = inactive
	if _, err = service.Update(ctx, command); !errors.Is(err, groupopsapp.ErrInvalid) {
		t.Fatalf("inactive=%v", err)
	}
	got, err := service.Detail(ctx, saved.Plan.ID)
	if err != nil || got.Plan.Revision != saved.Plan.Revision || got.Plan.Name != saved.Plan.Name || got.Plan.Status != groupopsport.PlanPaused || len(got.Members) != 1 || got.Members[0].StaffID != actor {
		t.Fatalf("failed write changed owner=%+v err=%v", got, err)
	}
	// Incomplete content still cannot activate; saving basic fields never queues.
	if _, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: got.Plan.ID, ExpectedRevision: got.Plan.Revision, Actor: actor, IdempotencyKey: "paused-save-incomplete-enable"}); !errors.Is(err, groupopsapp.ErrStateConflict) {
		t.Fatalf("incomplete activation=%v", err)
	}
	for table, want := range map[string]int{"group_ops_operation_receipts": 2, "group_ops_audit_events": 2, "external_effects": 0, "river_job": 0} {
		var count int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
}

func TestGroupOpsPostgreSQLActivePlanRequiresExplicitPauseBeforeConfiguration(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var actor int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,login_enabled) VALUES('active-pause','$argon2id$test','Operator',true,false) RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	store, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := groupopsapp.NewService(uow, store, groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: store}, store)
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Active Webhook", Actor: actor, IdempotencyKey: "active-pause-create"})
	if err != nil {
		t.Fatal(err)
	}
	update := func(command groupopsport.UpdatePlanCommand) {
		t.Helper()
		value, updateErr := service.Update(ctx, command)
		if updateErr != nil {
			t.Fatal(updateErr)
		}
		detail = value
	}
	update(groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: "Active Webhook", PlanType: groupopsport.PlanTypeWebhook, OwnerStaffID: actor, OwnerStaffIDSet: true, Actor: actor, IdempotencyKey: "active-pause-configure"})
	if value, addErr := service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: "synthetic-group-1", Actor: actor, IdempotencyKey: "active-pause-group"}); addErr != nil {
		t.Fatal(addErr)
	} else {
		detail = value
	}
	if value, descriptorErr := service.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Reference: "active-pause-webhook", Actor: actor, IdempotencyKey: "active-pause-descriptor"}); descriptorErr != nil {
		t.Fatal(descriptorErr)
	} else {
		detail = value
	}
	if value, activateErr := service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: actor, IdempotencyKey: "active-pause-activate"}); activateErr != nil {
		t.Fatal(activateErr)
	} else {
		detail = value
	}
	activeRevision := detail.Plan.Revision
	if detail.Plan.Status != groupopsport.PlanActive {
		t.Fatalf("status=%s want active", detail.Plan.Status)
	}
	if _, err = service.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: activeRevision, Name: "must not overwrite active", Actor: actor, IdempotencyKey: "active-pause-reject-update"}); !errors.Is(err, groupopsapp.ErrStateConflict) {
		t.Fatalf("active update=%v", err)
	}
	if _, err = service.Pause(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: activeRevision - 1, Actor: actor, IdempotencyKey: "active-pause-stale"}); !errors.Is(err, groupopsapp.ErrConflict) {
		t.Fatalf("stale pause=%v", err)
	}
	paused, err := service.Pause(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: activeRevision, Actor: actor, IdempotencyKey: "active-pause-confirm"})
	if err != nil || paused.Plan.Status != groupopsport.PlanPaused || paused.Plan.Revision != activeRevision+1 {
		t.Fatalf("pause=%+v err=%v", paused, err)
	}
	saved, err := service.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: paused.Plan.ID, ExpectedRevision: paused.Plan.Revision, Name: "Saved after explicit pause", Actor: actor, IdempotencyKey: "active-pause-save"})
	if err != nil || saved.Plan.Status != groupopsport.PlanPaused || saved.Plan.Revision != paused.Plan.Revision+1 {
		t.Fatalf("paused save=%+v err=%v", saved, err)
	}
	if _, err = service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: saved.Plan.ID, ExpectedRevision: saved.Plan.Revision, AssetRef: "must-remain-draft-only", Actor: actor, IdempotencyKey: "active-pause-reject-group"}); !errors.Is(err, groupopsapp.ErrStateConflict) {
		t.Fatalf("paused group mutation=%v", err)
	}
	for table, want := range map[string]int{"external_effects": 0, "river_job": 0} {
		var count int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
}
