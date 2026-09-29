package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	aiapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	aistore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	queue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"testing"
	"time"
)

func TestPostgreSQLExcelImportReviewDeferredSendAndReceiptJourney(t *testing.T) {
	native, cleanup := aiAssistantHTTPJourneyPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := pg.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := pg.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	seedAIAssistantHTTPJourney(t, native)
	repo, err := aistore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	identities := identityquery.NewPostgreSQL()
	oneid := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	service, err := aiapp.NewService(uow, repo, journeyCustomerReader{}, aiStaffSnapshotAdapter{repository: accessstore.NewPostgreSQL()}, journeyTextMaterials{}, oneid, identities)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	module := effects.NewModuleRegistration()
	if err = module.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	client, err := queue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectsRepo, err := effects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := outbound.NewPrivateMessageRepository(native, effectsRepo)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindOutbound(writer, true); err != nil {
		t.Fatal(err)
	}
	service.ExcelSnapshot = func(context.Context, ai.PlanID, int64) (string, error) { return "frozen-segments-at-approval", nil }
	command := ai.CreatePlanCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: 9}, IdempotencyKey: "excel-fixture-import", Name: "Excel fixture", SourceKind: "excel_batch", SourceDigest: effect.Hash("excel-file"), OccurredAt: time.Now().UTC()}
	for _, u := range []string{"known-union", "unknown-union", "excluded-union"} {
		command.Recipients = append(command.Recipients, ai.RecipientCandidate{DeferredTarget: &ai.DeferredTarget{UnionID: u, Scope: "wechat-open-platform:fixture", SenderUserID: "sender-from-file"}, Content: []ai.ContentBlock{{Kind: ai.ContentText, Text: "reviewed text"}, {Kind: ai.ContentMiniProgram, ExcelCard: &ai.ExcelCard{AppID: "fixture-app", Path: "pages/article/article?lesson_id=1", Title: "案例", CoverDigest: ""}}}})
	}
	created, err := service.CreateExcelPlan(ctx, "fixture-import", command)
	if err != nil {
		t.Fatal(err)
	}
	// Cross-operator re-upload maps to the same native plan.
	command.Actor.ID = 10
	replay, err := service.CreateExcelPlan(ctx, "fixture-import", command)
	if err != nil || !replay.Replayed || replay.Plan.ID != created.Plan.ID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = service.CreatePlan(ctx, command); !errors.Is(err, aiapp.ErrInvalid) {
		t.Fatalf("ordinary intake accepted deferred targets: %v", err)
	}
	page, err := service.ListRecipients(ctx, ai.RecipientPageQuery{PlanID: created.Plan.ID})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("all unresolved users must be reviewable: %v", err)
	}
	for _, r := range page.Items {
		if r.CustomerID != 0 || r.DeferredTarget == nil {
			t.Fatal("import provisioned a customer")
		}
	}
	who := ai.Actor{Kind: ai.ActorAdmin, ID: 9}
	if _, err = service.PreviewApproval(ctx, ai.PreviewApprovalCommand{Actor: who, PlanID: created.Plan.ID, ExpectedVersion: created.Plan.Version}); !errors.Is(err, aiapp.ErrInvalid) {
		t.Fatalf("coverless approval: %v", err)
	}
	withCover, err := service.ApplyExcelCover(ctx, who, created.Plan.ID, created.Plan.Version, "batch-cover-upload", effect.Hash("uploaded-cover"))
	if err != nil {
		t.Fatal(err)
	}
	replayCover, err := service.ApplyExcelCover(ctx, who, created.Plan.ID, created.Plan.Version, "batch-cover-upload", effect.Hash("uploaded-cover"))
	if err != nil || replayCover.Version != withCover.Version {
		t.Fatalf("cover replay: %v", err)
	}
	if _, err = service.ApplyExcelCover(ctx, who, created.Plan.ID, created.Plan.Version, "stale-cover-upload", effect.Hash("different-cover")); !errors.Is(err, aiapp.ErrConflict) {
		t.Fatalf("stale cover mutation: %v", err)
	}
	page, err = service.ListRecipients(ctx, ai.RecipientPageQuery{PlanID: created.Plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, original, err := service.GetRecipient(ctx, created.Plan.ID, page.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]ai.ContentBlock(nil), original.Blocks...)
	cardCopy := *changed[1].ExcelCard
	cardCopy.AppID = "different-app"
	changed[1].ExcelCard = &cardCopy
	if _, err = service.UpdateContent(ctx, ai.UpdateContentCommand{Actor: who, PlanID: created.Plan.ID, RecipientID: page.Items[0].ID, ExpectedVersion: page.Items[0].Version, IdempotencyKey: "change-fixed-app-fixture", Blocks: changed}); !errors.Is(err, aiapp.ErrInvalid) {
		t.Fatalf("fixed AppID could be changed: %v", err)
	}
	exclude := page.Items[2]
	if _, err = service.ReviewRecipient(ctx, ai.ReviewRecipientCommand{Actor: who, PlanID: created.Plan.ID, RecipientID: exclude.ID, ExpectedVersion: exclude.Version, Decision: ai.ReviewRejected, IdempotencyKey: "exclude-row-fixture"}); err != nil {
		t.Fatal(err)
	}
	current, err := service.GetPlan(ctx, created.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstReview, err := service.ReviewRecipient(ctx, ai.ReviewRecipientCommand{Actor: who, PlanID: current.ID, RecipientID: page.Items[0].ID, ExpectedVersion: page.Items[0].Version, Decision: ai.ReviewApproved, IdempotencyKey: "approve-single-before-cover"})
	if err != nil {
		t.Fatal(err)
	}
	current, err = service.GetPlan(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err = service.ApplyExcelCover(ctx, who, current.ID, current.Version, "replace-batch-cover", effect.Hash("replacement-cover"))
	if err != nil {
		t.Fatal(err)
	}
	firstReview, _, err = service.GetRecipient(ctx, current.ID, firstReview.ID)
	if err != nil || firstReview.ReviewState != ai.ReviewPending {
		t.Fatalf("cover change retained approval: %v", err)
	}
	excludedAfter, _, err := service.GetRecipient(ctx, current.ID, exclude.ID)
	if err != nil || excludedAfter.ReviewState != ai.ReviewPending {
		t.Fatalf("cover change did not invalidate rejected row: state=%s err=%v", excludedAfter.ReviewState, err)
	}
	if _, err = service.ReviewRecipient(ctx, ai.ReviewRecipientCommand{Actor: who, PlanID: current.ID, RecipientID: excludedAfter.ID, ExpectedVersion: excludedAfter.Version, Decision: ai.ReviewRejected, IdempotencyKey: "exclude-row-after-cover"}); err != nil {
		t.Fatalf("re-review excluded row after cover: %v", err)
	}
	current, err = service.GetPlan(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewApproval(ctx, ai.PreviewApprovalCommand{Actor: who, PlanID: current.ID, ExpectedVersion: current.Version})
	if err != nil {
		t.Fatal(err)
	}
	approval := ai.ApprovePlanCommand{Actor: who, PlanID: current.ID, ExpectedVersion: current.Version, PreviewDigest: preview.PreviewDigest, IdempotencyKey: "approve-excel-fixture"}
	if _, err = service.ApprovePlan(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ApprovePlan(ctx, approval); err != nil {
		t.Fatal(err)
	}
	var intents, bindings, jobs int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_private_message_intents),(SELECT count(*) FROM ai_assistant_effect_bindings),(SELECT count(*) FROM river_job)`).Scan(&intents, &bindings, &jobs); err != nil {
		t.Fatal(err)
	}
	if intents != 2 || bindings != 2 || jobs != 2 {
		t.Fatalf("duplicate or excluded effects: %d %d %d", intents, bindings, jobs)
	}
	key, _, err := repo.ExcelApproval(ctx, current.ID)
	if err != nil || key != "frozen-segments-at-approval" {
		t.Fatalf("snapshot did not commit with approval: %s %v", key, err)
	}
	// Verified UnionID is added only after import/approval. Resolution happens now.
	_, err = native.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at) VALUES(91,'unionid','wechat-open-platform:fixture','known-union','verified','fixture',1,'active',clock_timestamp())`)
	if err != nil {
		t.Fatal(err)
	}
	targets := aiPrivateTargetResolver{uow: uow, identities: identities, corpID: "corp-1", resolver: oneid, trusted: identities, deferred: repo}
	first, content, err := service.GetRecipient(ctx, current.ID, page.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("aiassistant:%d:%d:%d", current.ID, first.ID, content.ID)
	target, err := targets.ResolveDeferredPrivateMessageTarget(ctx, ref)
	if err != nil || target.StaffUserID != "sender-from-file" || target.ExternalUserID != "external-1" {
		t.Fatalf("deferred resolution changed sender: %+v %v", target, err)
	}
	second, c2, _ := service.GetRecipient(ctx, current.ID, page.Items[1].ID)
	if _, err = targets.ResolveDeferredPrivateMessageTarget(ctx, fmt.Sprintf("aiassistant:%d:%d:%d", current.ID, second.ID, c2.ID)); err == nil {
		t.Fatal("unknown unionid guessed")
	}
	if err = writer.RecordPrivateMessageReceipt(ctx, ref, target, "official-task", ""); err != nil {
		t.Fatal(err)
	}
	receipt, found, err := writer.PrivateMessageReceipt(ctx, ref)
	if err != nil || !found || receipt.SentAt != nil || receipt.Status != nil {
		t.Fatalf("task acceptance became delivery: %+v %v", receipt, err)
	}
	status := 1
	sent := time.Now().UTC().Truncate(time.Second)
	receipt.Status = &status
	receipt.SentAt = &sent
	wrong := receipt
	wrong.ExternalUserID = "different-user"
	if err = writer.SavePrivateMessageDelivery(ctx, ref, wrong); err == nil {
		t.Fatal("mismatched receipt accepted")
	}
	if err = writer.SavePrivateMessageDelivery(ctx, ref, receipt); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordExcelDelivery(ctx, first, receipt); err != nil {
		t.Fatal(err)
	}
	first, _, err = service.GetRecipient(ctx, current.ID, first.ID)
	if err != nil || first.ExecutionState != ai.ExecutionDeliveryProven {
		t.Fatalf("delivery projection: %v %v", first.ExecutionState, err)
	}
	// Queued content is immutable even if the caller submits a stale UI edit.
	if _, err = service.UpdateContent(ctx, ai.UpdateContentCommand{Actor: who, PlanID: current.ID, RecipientID: first.ID, ExpectedVersion: first.Version, IdempotencyKey: "edit-after-submit", Blocks: command.Recipients[0].Content}); err == nil {
		t.Fatal("queued content changed")
	}
}

func TestPostgreSQLApprovalFaultRollsBackReviewAndExternalEffectChain(t *testing.T) {
	native, cleanup := aiAssistantHTTPJourneyPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := pg.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := pg.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	seedAIAssistantHTTPJourney(t, native)
	repo, err := aistore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	identities := identityquery.NewPostgreSQL()
	oneid := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	service, err := aiapp.NewService(uow, repo, journeyCustomerReader{}, aiStaffSnapshotAdapter{repository: accessstore.NewPostgreSQL()}, journeyTextMaterials{}, oneid, identities)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	module := effects.NewModuleRegistration()
	if err = module.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	client, err := queue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectsRepo, err := effects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := outbound.NewPrivateMessageRepository(native, effectsRepo)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindOutbound(writer, true); err != nil {
		t.Fatal(err)
	}

	who := ai.Actor{Kind: ai.ActorAdmin, ID: 9}
	created, err := service.CreatePlan(ctx, ai.CreatePlanCommand{
		Actor: who, IdempotencyKey: "airev05-atomic-plan-create-01", Name: "AIREV-05 atomic approval fixture",
		SourceKind: "manual", SourceDigest: effect.Hash("airev05", "atomic-approval-source"),
		Recipients: []ai.RecipientCandidate{{CustomerID: 91, StaffID: 9, Content: []ai.ContentBlock{{Kind: ai.ContentText, Text: "synthetic approval fixture"}}}},
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ListRecipients(ctx, ai.RecipientPageQuery{PlanID: created.Plan.ID})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("recipient page=%+v err=%v", page, err)
	}
	recipient := page.Items[0]
	if _, err = service.ReviewRecipient(ctx, ai.ReviewRecipientCommand{Actor: who, PlanID: created.Plan.ID, RecipientID: recipient.ID, ExpectedVersion: recipient.Version, Decision: ai.ReviewApproved, IdempotencyKey: "airev05-atomic-recipient-review-01"}); err != nil {
		t.Fatal(err)
	}
	plan, err := service.GetPlan(ctx, created.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewApproval(ctx, ai.PreviewApprovalCommand{Actor: who, PlanID: plan.ID, ExpectedVersion: plan.Version})
	if err != nil {
		t.Fatal(err)
	}
	approval := ai.ApprovePlanCommand{Actor: who, PlanID: plan.ID, ExpectedVersion: plan.Version, PreviewDigest: preview.PreviewDigest, IdempotencyKey: "airev05-atomic-plan-approve-01"}
	beforePlan := plan
	beforeRecipient, _, err := service.GetRecipient(ctx, plan.ID, recipient.ID)
	if err != nil {
		t.Fatal(err)
	}

	type approvalCounts struct {
		operationReceipts, completedReceipts, reviewDecisions, auditEvents, outboxEvents int
		bindings, outboundIntents, acceptReceipts, queueReceipts, effects, generations   int
		effectJobs, attempts, riverJobs                                                  int
	}
	keyDigest := sha256.Sum256([]byte(approval.IdempotencyKey))
	intentPattern := fmt.Sprintf("aiassistant:%d:%%", plan.ID)
	readCounts := func() approvalCounts {
		t.Helper()
		var counts approvalCounts
		err := native.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM ai_assistant_operation_receipts WHERE operation='plan_approve' AND actor_scope='admin:9' AND key_digest=$1),
			(SELECT count(*) FROM ai_assistant_operation_receipts WHERE operation='plan_approve' AND actor_scope='admin:9' AND key_digest=$1 AND state='completed'),
			(SELECT count(*) FROM ai_assistant_review_decisions WHERE plan_id=$2 AND recipient_id IS NULL AND decision='approved'),
			(SELECT count(*) FROM ai_assistant_audit_events WHERE plan_id=$2 AND operation=$3),
			(SELECT count(*) FROM ai_assistant_outbox WHERE plan_id=$2 AND event_type=$3),
			(SELECT count(*) FROM ai_assistant_effect_bindings b JOIN ai_assistant_plan_recipients r ON r.id=b.recipient_id WHERE r.plan_id=$2),
			(SELECT count(*) FROM outbound_private_message_intents WHERE source_reference LIKE $4),
			(SELECT count(*) FROM external_effect_operation_receipts WHERE operation='accept'),
			(SELECT count(*) FROM external_effect_operation_receipts WHERE operation='queue'),
			(SELECT count(*) FROM external_effects),
			(SELECT count(*) FROM external_effect_generations),
			(SELECT count(*) FROM external_effect_jobs),
			(SELECT count(*) FROM external_effect_attempts),
			(SELECT count(*) FROM river_job)`, keyDigest[:], plan.ID, ai.EventPlanApproved, intentPattern).Scan(
			&counts.operationReceipts, &counts.completedReceipts, &counts.reviewDecisions, &counts.auditEvents, &counts.outboxEvents,
			&counts.bindings, &counts.outboundIntents, &counts.acceptReceipts, &counts.queueReceipts, &counts.effects,
			&counts.generations, &counts.effectJobs, &counts.attempts, &counts.riverJobs,
		)
		if err != nil {
			t.Fatal(err)
		}
		return counts
	}
	if got := readCounts(); got != (approvalCounts{}) {
		t.Fatalf("approval/effect records exist before injected approval: %+v", got)
	}

	if _, err = native.Exec(ctx, `CREATE FUNCTION airev05_fail_approved_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.event_type = 'aiassistant.plan_approved.v1' THEN
				RAISE EXCEPTION 'AIREV-05 injected outbox failure' USING ERRCODE='P0001';
			END IF;
			RETURN NEW;
		END;
		$$`); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `CREATE TRIGGER airev05_fail_approved_outbox BEFORE INSERT ON ai_assistant_outbox FOR EACH ROW EXECUTE FUNCTION airev05_fail_approved_outbox()`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ApprovePlan(ctx, approval); err == nil {
		t.Fatal("approval unexpectedly passed the injected PostgreSQL outbox fault")
	} else {
		var sqlErr interface{ SQLState() string }
		if !errors.As(err, &sqlErr) || sqlErr.SQLState() != "P0001" {
			t.Fatalf("approval error=%v, want injected PostgreSQL SQLSTATE P0001", err)
		}
	}
	if _, err = native.Exec(ctx, `DROP TRIGGER airev05_fail_approved_outbox ON ai_assistant_outbox`); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `DROP FUNCTION airev05_fail_approved_outbox()`); err != nil {
		t.Fatal(err)
	}

	afterPlan, err := service.GetPlan(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterRecipient, _, err := service.GetRecipient(ctx, plan.ID, recipient.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterPlan.State != beforePlan.State || afterPlan.Version != beforePlan.Version || afterPlan.PendingCount != beforePlan.PendingCount || afterPlan.ApprovedCount != beforePlan.ApprovedCount ||
		afterRecipient.ReviewState != beforeRecipient.ReviewState || afterRecipient.ExecutionState != beforeRecipient.ExecutionState || afterRecipient.Version != beforeRecipient.Version || afterRecipient.EffectID != beforeRecipient.EffectID {
		t.Fatalf("injected failure left partial approval: plan before=%+v after=%+v recipient before=%+v after=%+v", beforePlan, afterPlan, beforeRecipient, afterRecipient)
	}
	if got := readCounts(); got != (approvalCounts{}) {
		t.Fatalf("injected failure left partial approval/effect records: %+v", got)
	}

	approved, err := service.ApprovePlan(ctx, approval)
	if err != nil || approved.State != ai.PlanDispatching {
		t.Fatalf("same-key approval retry=%+v err=%v", approved, err)
	}
	want := approvalCounts{operationReceipts: 1, completedReceipts: 1, reviewDecisions: 1, auditEvents: 1, outboxEvents: 1, bindings: 1, outboundIntents: 1, acceptReceipts: 1, queueReceipts: 1, effects: 1, generations: 1, effectJobs: 1, riverJobs: 1}
	if got := readCounts(); got != want {
		t.Fatalf("same-key approval retry left duplicate or missing records: got=%+v want=%+v", got, want)
	}
}
