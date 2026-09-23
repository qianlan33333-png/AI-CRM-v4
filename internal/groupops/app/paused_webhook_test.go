package app

import (
	"context"
	"errors"
	"testing"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

func TestPausedWebhookConfigurationPreservesLifecycleCASAndReceipt(t *testing.T) {
	service, _, events := newTestService()
	ctx := context.Background()
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Webhook setup", Actor: 7, IdempotencyKey: "paused-webhook-create"})
	check := func(value groupopsport.Detail, failure error) {
		t.Helper()
		if failure != nil {
			t.Fatal(failure)
		}
		detail = value
	}
	check(detail, err)
	check(service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: 19, Actor: 7, IdempotencyKey: "paused-webhook-member"}))
	check(service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: "local-group-18", Actor: 7, IdempotencyKey: "paused-webhook-asset"}))
	check(service.AddNode(ctx, groupopsport.NodeCreateCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Position: 1, Kind: groupopsport.NodeMessage, MessageText: "欢迎加入", Actor: 7, IdempotencyKey: "paused-webhook-node"}))
	check(service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "paused-webhook-active"}))
	command := groupopsport.WebhookDescriptorCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Reference: "groupops-descriptor-example", Actor: 7, IdempotencyKey: "paused-webhook-reject-active"}
	if _, err := service.PutWebhookDescriptor(ctx, command); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("active mutation must fail: %v", err)
	}
	check(service.Pause(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "paused-webhook-pause"}))
	before := detail.Plan.Revision
	beforeEvents := len(events.items)
	command.ExpectedRevision, command.IdempotencyKey = before, "paused-webhook-save"
	check(service.PutWebhookDescriptor(ctx, command))
	if detail.Plan.Status != groupopsport.PlanPaused || detail.Plan.Revision != before+1 || detail.WebhookDescriptor.Reference != command.Reference || detail.ProviderExecutionEligible || detail.RealExternalCallExecuted || len(events.items) != beforeEvents+1 {
		t.Fatalf("paused save changed lifecycle or missed audit: detail=%+v events=%d", detail, len(events.items))
	}
	saved := detail
	check(service.PutWebhookDescriptor(ctx, command))
	if !sameSavedDetail(saved, detail) || len(events.items) != beforeEvents+1 {
		t.Fatal("replay changed state or duplicated event")
	}
	command.IdempotencyKey = "paused-webhook-stale-revision"
	if _, err := service.PutWebhookDescriptor(ctx, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision must fail: %v", err)
	}
	current, err := service.Detail(ctx, detail.Plan.ID)
	if err != nil || !sameSavedDetail(saved, current) || len(events.items) != beforeEvents+1 {
		t.Fatalf("conflict changed saved state or audit: %v", err)
	}
}

func TestWebhookPlanActivatesWithoutNodesButRequiresDescriptorBoundGroupAndMember(t *testing.T) {
	service, _, _ := newTestService()
	ctx := context.Background()
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Webhook dynamic content", Actor: 7, IdempotencyKey: "webhook-dynamic-create"})
	if err != nil {
		t.Fatal(err)
	}
	// A paused Webhook plan can carry read-compatible node history. That
	// history is deliberately not a dynamic Webhook execution prerequisite.
	detail, err = service.AddNode(ctx, groupopsport.NodeCreateCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Position: 1, Kind: groupopsport.NodeMessage, MessageText: "retired node", MaterialRef: "legacy-material", Actor: 7, IdempotencyKey: "webhook-dynamic-legacy-node"})
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
	update(groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, PlanType: groupopsport.PlanTypeWebhook, Name: detail.Plan.Name, Actor: 7, IdempotencyKey: "webhook-dynamic-type"})
	preview, err := service.Preview(ctx, detail.Plan.ID)
	if err != nil || len(preview.IssueCodes) != 3 || preview.IssueCodes[0] != "group_asset_required" || preview.IssueCodes[1] != "member_required" || preview.IssueCodes[2] != "webhook_descriptor_required" {
		t.Fatalf("Webhook preview included irrelevant node validation: preview=%+v err=%v", preview, err)
	}

	if _, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "webhook-dynamic-no-config"}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("activation without descriptor/group err=%v", err)
	}

	value, err := service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: 19, Actor: 7, IdempotencyKey: "webhook-dynamic-member"})
	if err != nil {
		t.Fatal(err)
	}
	detail = value
	value, err = service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: "stable-chat-1", Actor: 7, IdempotencyKey: "webhook-dynamic-group"})
	if err != nil {
		t.Fatal(err)
	}
	detail = value
	value, err = service.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Reference: "webhook-dynamic-example", Actor: 7, IdempotencyKey: "webhook-dynamic-descriptor"})
	if err != nil {
		t.Fatal(err)
	}
	detail = value
	value, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "webhook-dynamic-activate"})
	if err != nil {
		t.Fatal(err)
	}
	if value.Plan.Status != groupopsport.PlanActive || len(value.Nodes) != 1 || len(value.Members) != 1 || value.Plan.Type != groupopsport.PlanTypeWebhook {
		t.Fatalf("unexpected activated webhook detail=%+v", value)
	}
}

func TestStandardPlanStillRequiresNodeForActivation(t *testing.T) {
	service, _, _ := newTestService()
	ctx := context.Background()
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Standard requires node", Actor: 7, IdempotencyKey: "standard-node-create"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: 19, Actor: 7, IdempotencyKey: "standard-node-member"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: "stable-chat-1", Actor: 7, IdempotencyKey: "standard-node-group"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "standard-node-activate"}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("node-free standard activation err=%v", err)
	}
}
