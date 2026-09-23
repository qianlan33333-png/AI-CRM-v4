package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// failingGroupOpsEffectAccepter proves the Group Ops UoW really encompasses
// EER acceptance and its River enqueue: it lets the real EER store write into
// the caller transaction, then fails so the enclosing Group Ops UoW must roll
// every owner and EER fact back together.
type failingGroupOpsEffectAccepter struct {
	delegate effectport.TransactionalAccepter
}

func (a failingGroupOpsEffectAccepter) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	projection, receipt, err := a.delegate.AcceptAndQueueWithin(ctx, command)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	return projection, receipt, errors.New("test external effect acceptance failed after durable EER write")
}

type groupOpsLessonCardRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip groupOpsLessonCardRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func groupOpsLessonCardPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.SetRGBA(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func groupOpsLessonCardInbound(reference, target string) groupopsport.WebhookInboundCommand {
	return groupopsport.WebhookInboundCommand{
		WebhookReference:     reference,
		TargetChatReferences: []string{target},
		Messages: []groupopsport.WebhookMessage{
			{Type: "text", Text: "日课话术"},
			{Type: "miniprogram", AppID: "wx0ca836834b18e989", Path: "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=learn", Title: "日课"},
		},
	}
}

// TestGroupOpsWebhookPostgreSQLJourney proves the dynamic inbound path uses
// the same PostgreSQL UoW for the frozen run, node-less execution/intent and
// EER acceptance. It deliberately stops before any worker/provider attempt.
func TestGroupOpsWebhookPostgreSQLJourney(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}

	var actorID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('groupops-webhook-pg','$argon2id$webhook','Group Ops Webhook','webhook-sender',true,false) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{reader: mediaStore})
	if err != nil {
		t.Fatal(err)
	}
	lessonPNG := groupOpsLessonCardPNG(t)
	var lessonFetches atomic.Int32
	lessonCards := mediaapp.NewWebhookLessonCardResolver(mediaStore, groupOpsLessonCardRoundTripper(func(request *http.Request) (*http.Response, error) {
		lessonFetches.Add(1)
		if request.Method != http.MethodGet || request.URL.String() != "https://ip.lhbl.com.cn/api/share/lesson-card/11111111-2222-3333-4444-555555555555.png" {
			return nil, fmt.Errorf("unexpected lesson cover request %s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: int64(len(lessonPNG)), Body: io.NopCloser(bytes.NewReader(lessonPNG))}, nil
	}))
	materialResolver, err := newGroupOpsMaterialAdapter(mediaStore, freezer, lessonCards)
	if err != nil {
		t.Fatal(err)
	}
	service := groupopsapp.NewService(uow, groupStore, groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: groupStore}, groupStore)
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Webhook PostgreSQL journey", Actor: actorID, IdempotencyKey: "groupops-webhook-pg-create"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: detail.Plan.Name, PlanType: groupopsport.PlanTypeWebhook, Actor: actorID, IdempotencyKey: "groupops-webhook-pg-type"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: actorID, Actor: actorID, IdempotencyKey: "groupops-webhook-pg-member"})
	if err != nil {
		t.Fatal(err)
	}
	for index, target := range []string{"webhook-pg-chat-a", "webhook-pg-chat-b"} {
		detail, err = service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: target, Actor: actorID, IdempotencyKey: "groupops-webhook-pg-asset-" + strconv.Itoa(index)})
		if err != nil {
			t.Fatal(err)
		}
	}
	detail, err = service.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Reference: "groupops-webhook-pg", Actor: actorID, IdempotencyKey: "groupops-webhook-pg-descriptor"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-webhook-pg-activate"})
	if err != nil || detail.Plan.Status != groupopsport.PlanActive || len(detail.Nodes) != 0 {
		t.Fatalf("activate detail=%+v err=%v", detail, err)
	}

	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	effectClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := externaleffects.NewRepository(native, effectClient)
	if err != nil {
		t.Fatal(err)
	}
	runtimeService := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, effectStore, journeyStaff{}, nil, journeySender{}, nil, nil, materialResolver)
	runtimeService.SetDispatchEnabled(true)
	inbound := groupopsport.WebhookInboundCommand{WebhookReference: "groupops-webhook-pg", TargetChatReferences: []string{"webhook-pg-chat-a", "webhook-pg-chat-b"}, Messages: []groupopsport.WebhookMessage{{Type: "text", Text: "动态 PostgreSQL 话术"}}}
	first, err := runtimeService.AcceptWebhook(ctx, "groupops-webhook-pg", "webhook-event-pg-0001", inbound)
	if err != nil || first.Run.ID < 1 || first.Accepted != 2 || len(first.Executions) != 2 || first.RealExternalCallExecuted {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	for _, execution := range first.Executions {
		if execution.NodeID != 0 {
			t.Fatalf("dynamic execution exposed node id=%d", execution.NodeID)
		}
	}
	var nullExecutionCount, nullIntentCount int
	var payloadDigest string
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_executions WHERE run_id=$1 AND node_id IS NULL`, first.Run.ID).Scan(&nullExecutionCount); err != nil || nullExecutionCount != 2 {
		t.Fatalf("node-less executions=%d err=%v", nullExecutionCount, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_execution_intents WHERE run_id=$1 AND node_id IS NULL`, first.Run.ID).Scan(&nullIntentCount); err != nil || nullIntentCount != 2 {
		t.Fatalf("node-less intents=%d err=%v", nullIntentCount, err)
	}
	if err = native.QueryRow(ctx, `SELECT webhook_payload_digest FROM group_ops_runs WHERE id=$1`, first.Run.ID).Scan(&payloadDigest); err != nil || !effectport.ValidDigest(effectport.Digest(payloadDigest)) {
		t.Fatalf("frozen webhook payload digest=%q err=%v", payloadDigest, err)
	}

	// The automatic daily-lesson branch must freeze a locally materialized
	// Media miniprogram in the same UoW as its run and EER. In particular, the
	// Media capture below reads through the transaction rather than a separate
	// pool, so an uncommitted image/card is visible to source freezing.
	miniInbound := groupOpsLessonCardInbound("groupops-webhook-pg", "webhook-pg-chat-a")
	miniSummary, err := runtimeService.AcceptWebhook(ctx, "groupops-webhook-pg", "webhook-event-pg-mini-0001", miniInbound)
	if err != nil || miniSummary.Run.ID < 1 || miniSummary.Accepted != 1 || len(miniSummary.Executions) != 1 || lessonFetches.Load() != 1 {
		t.Fatalf("mini summary=%+v fetches=%d err=%v", miniSummary, lessonFetches.Load(), err)
	}
	var imageCount, miniCount, mediaAuditCount, mediaOutboxCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_images`).Scan(&imageCount); err != nil || imageCount != 1 {
		t.Fatalf("images=%d err=%v", imageCount, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_miniprograms`).Scan(&miniCount); err != nil || miniCount != 1 {
		t.Fatalf("miniprograms=%d err=%v", miniCount, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_audit_events`).Scan(&mediaAuditCount); err != nil || mediaAuditCount != 2 {
		t.Fatalf("media audit=%d err=%v", mediaAuditCount, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_outbox`).Scan(&mediaOutboxCount); err != nil || mediaOutboxCount != 2 {
		t.Fatalf("media outbox=%d err=%v", mediaOutboxCount, err)
	}
	var frozenSnapshot, frozenSources []byte
	if err = native.QueryRow(ctx, `SELECT material_snapshot,material_source_snapshot FROM group_ops_executions WHERE run_id=$1`, miniSummary.Run.ID).Scan(&frozenSnapshot, &frozenSources); err != nil || !bytes.Contains(frozenSnapshot, []byte(`miniprogram`)) || !bytes.Contains(frozenSources, []byte(`thumbnail_image_id`)) || !bytes.Contains(frozenSources, []byte(`wx0ca836834b18e989`)) {
		t.Fatalf("frozen snapshot=%s sources=%s err=%v", frozenSnapshot, frozenSources, err)
	}
	miniReplay, err := runtimeService.AcceptWebhook(ctx, "groupops-webhook-pg", "webhook-event-pg-mini-0001", miniInbound)
	if err != nil || miniReplay.Run.ID != miniSummary.Run.ID || lessonFetches.Load() != 1 {
		t.Fatalf("mini replay=%+v fetches=%d err=%v", miniReplay, lessonFetches.Load(), err)
	}

	// Two first accepts of one event race through independent PostgreSQL UoWs.
	// The plan lock and durable source key must converge them on one sealed run
	// and one EER/intent/execution set, not just return two successful mocks.
	var concurrentRunsBefore, concurrentIntentsBefore, concurrentExecutionsBefore, concurrentEffectsBefore, concurrentImagesBefore, concurrentMiniProgramsBefore int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_runs WHERE plan_id=$1`, detail.Plan.ID).Scan(&concurrentRunsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_execution_intents WHERE plan_id=$1`, detail.Plan.ID).Scan(&concurrentIntentsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_executions WHERE plan_id=$1`, detail.Plan.ID).Scan(&concurrentExecutionsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&concurrentEffectsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_images`).Scan(&concurrentImagesBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_miniprograms`).Scan(&concurrentMiniProgramsBefore); err != nil {
		t.Fatal(err)
	}
	type concurrentAcceptResult struct {
		summary groupopsport.RunSummary
		err     error
	}
	startConcurrentAccept := make(chan struct{})
	concurrentResults := make(chan concurrentAcceptResult, 2)
	concurrentInbound := groupOpsLessonCardInbound("groupops-webhook-pg", "webhook-pg-chat-a")
	for range 2 {
		go func() {
			<-startConcurrentAccept
			summary, acceptErr := runtimeService.AcceptWebhook(ctx, "groupops-webhook-pg", "webhook-event-pg-concurrent", concurrentInbound)
			concurrentResults <- concurrentAcceptResult{summary: summary, err: acceptErr}
		}()
	}
	close(startConcurrentAccept)
	results := make([]concurrentAcceptResult, 0, 2)
	for range 2 {
		select {
		case result := <-concurrentResults:
			results = append(results, result)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent first Webhook accept timed out")
		}
	}
	if results[0].err != nil || results[1].err != nil || results[0].summary.Run.ID < 1 || results[0].summary.Run.ID != results[1].summary.Run.ID {
		t.Fatalf("concurrent accepts=%+v", results)
	}
	var concurrentRunsAfter, concurrentIntentsAfter, concurrentExecutionsAfter, concurrentEffectsAfter, concurrentImagesAfter, concurrentMiniProgramsAfter int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_runs WHERE plan_id=$1`, detail.Plan.ID).Scan(&concurrentRunsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_execution_intents WHERE plan_id=$1`, detail.Plan.ID).Scan(&concurrentIntentsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_executions WHERE plan_id=$1`, detail.Plan.ID).Scan(&concurrentExecutionsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&concurrentEffectsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_images`).Scan(&concurrentImagesAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_miniprograms`).Scan(&concurrentMiniProgramsAfter); err != nil {
		t.Fatal(err)
	}
	if concurrentRunsAfter != concurrentRunsBefore+1 || concurrentIntentsAfter != concurrentIntentsBefore+1 || concurrentExecutionsAfter != concurrentExecutionsBefore+1 || concurrentEffectsAfter != concurrentEffectsBefore+1 || concurrentImagesAfter != concurrentImagesBefore+1 || concurrentMiniProgramsAfter != concurrentMiniProgramsBefore+1 {
		t.Fatalf("concurrent event duplicated facts: runs %d/%d intents %d/%d executions %d/%d effects %d/%d images %d/%d miniprograms %d/%d", concurrentRunsBefore, concurrentRunsAfter, concurrentIntentsBefore, concurrentIntentsAfter, concurrentExecutionsBefore, concurrentExecutionsAfter, concurrentEffectsBefore, concurrentEffectsAfter, concurrentImagesBefore, concurrentImagesAfter, concurrentMiniProgramsBefore, concurrentMiniProgramsAfter)
	}

	// A revision change cannot turn a replay into a second send. Existing runs
	// are resolved using the descriptor/event source key before active-state
	// validation, so this works even after the plan is paused.
	detail, err = service.Pause(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-webhook-pg-pause"})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := runtimeService.AcceptWebhook(ctx, "groupops-webhook-pg", "webhook-event-pg-0001", inbound)
	if err != nil || replayed.Run.ID != first.Run.ID || len(replayed.Executions) != 2 {
		t.Fatalf("revision replay=%+v err=%v", replayed, err)
	}
	changed := inbound
	changed.Messages = []groupopsport.WebhookMessage{{Type: "text", Text: "different frozen payload"}}
	if _, err = runtimeService.AcceptWebhook(ctx, "groupops-webhook-pg", "webhook-event-pg-0001", changed); !errors.Is(err, groupopsapp.ErrConflict) {
		t.Fatalf("same event different payload err=%v", err)
	}

	// 0155 marks legacy protocol rows version 1. They deliberately remain a
	// conflict after upgrade, even if every digest matches, rather than being
	// reinterpreted as a v2 dynamic Webhook event.
	legacyEvent := sha256.Sum256([]byte("legacy-webhook-event"))
	legacyPayload := sha256.Sum256([]byte("legacy-webhook-body"))
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_protocol_replays(client_id,resource_reference,event_id_digest,payload_digest,created_at) VALUES($1,$2,$3,$4,clock_timestamp())`, groupopsport.WebhookClientID, "groupops-webhook-pg", legacyEvent[:], legacyPayload[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = groupStore.ClaimWebhookReplay(ctx, groupopsport.WebhookClientID, "groupops-webhook-pg", legacyEvent, legacyPayload, time.Now().UTC()); !errors.Is(err, groupopsapp.ErrConflict) {
		t.Fatalf("legacy replay admitted after upgrade err=%v", err)
	}

	detail, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-webhook-pg-reactivate"})
	if err != nil {
		t.Fatal(err)
	}
	var runsBefore, intentsBefore, executionsBefore, effectsBefore, effectJobsBefore, riverJobsBefore int
	var imagesBefore, miniprogramsBefore, mediaReceiptsBefore, mediaAuditBefore, mediaOutboxBefore int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_runs WHERE plan_id=$1`, detail.Plan.ID).Scan(&runsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_execution_intents WHERE plan_id=$1`, detail.Plan.ID).Scan(&intentsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_executions WHERE plan_id=$1`, detail.Plan.ID).Scan(&executionsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effect_jobs`).Scan(&effectJobsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&riverJobsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_images`).Scan(&imagesBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_miniprograms`).Scan(&miniprogramsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_operation_receipts`).Scan(&mediaReceiptsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_audit_events`).Scan(&mediaAuditBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_outbox`).Scan(&mediaOutboxBefore); err != nil {
		t.Fatal(err)
	}
	failingRuntime := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, failingGroupOpsEffectAccepter{delegate: effectStore}, journeyStaff{}, nil, journeySender{}, nil, nil, materialResolver)
	failingRuntime.SetDispatchEnabled(true)
	if _, err = failingRuntime.AcceptWebhook(ctx, "groupops-webhook-pg", "webhook-event-pg-mini-rollback", groupOpsLessonCardInbound("groupops-webhook-pg", "webhook-pg-chat-a")); err == nil {
		t.Fatal("accepted a dynamic run despite external-effect transaction failure")
	}
	var runsAfter, intentsAfter, executionsAfter, effectsAfter, effectJobsAfter, riverJobsAfter int
	var imagesAfter, miniprogramsAfter, mediaReceiptsAfter, mediaAuditAfter, mediaOutboxAfter int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_runs WHERE plan_id=$1`, detail.Plan.ID).Scan(&runsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_execution_intents WHERE plan_id=$1`, detail.Plan.ID).Scan(&intentsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_executions WHERE plan_id=$1`, detail.Plan.ID).Scan(&executionsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effect_jobs`).Scan(&effectJobsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&riverJobsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_images`).Scan(&imagesAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_miniprograms`).Scan(&miniprogramsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_operation_receipts`).Scan(&mediaReceiptsAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_audit_events`).Scan(&mediaAuditAfter); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_outbox`).Scan(&mediaOutboxAfter); err != nil {
		t.Fatal(err)
	}
	if runsAfter != runsBefore || intentsAfter != intentsBefore || executionsAfter != executionsBefore || effectsAfter != effectsBefore || effectJobsAfter != effectJobsBefore || riverJobsAfter != riverJobsBefore || imagesAfter != imagesBefore || miniprogramsAfter != miniprogramsBefore || mediaReceiptsAfter != mediaReceiptsBefore || mediaAuditAfter != mediaAuditBefore || mediaOutboxAfter != mediaOutboxBefore {
		t.Fatalf("failed EER acceptance leaked facts: runs %d/%d intents %d/%d executions %d/%d effects %d/%d effect_jobs %d/%d river_jobs %d/%d images %d/%d miniprograms %d/%d receipts %d/%d media_audit %d/%d media_outbox %d/%d", runsBefore, runsAfter, intentsBefore, intentsAfter, executionsBefore, executionsAfter, effectsBefore, effectsAfter, effectJobsBefore, effectJobsAfter, riverJobsBefore, riverJobsAfter, imagesBefore, imagesAfter, miniprogramsBefore, miniprogramsAfter, mediaReceiptsBefore, mediaReceiptsAfter, mediaAuditBefore, mediaAuditAfter, mediaOutboxBefore, mediaOutboxAfter)
	}
}

// TestGroupOpsPostgreSQLJourney exercises the real owner stores and EER
// transaction seam. It intentionally uses opaque local group references and
// a deterministic adapter: no Provider credentials, customer, OneID, or
// audience data enters this Journey.
func TestGroupOpsPostgreSQLJourney(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()

	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}

	var actorID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('groupops-journey','$argon2id$journey','Group Ops Journey','journey-sender',true,false) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}

	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := mediaStore.CreateGroupInvite(ctx, actorID, "groupops-journey-invite-0001", map[string]any{"name": "Journey invite", "title": "Join Journey", "description": "Journey material", "join_url": "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef", "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	inviteID, ok := invite["id"].(int64)
	if !ok || inviteID < 1 {
		t.Fatalf("invite=%+v", invite)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{reader: mediaStore})
	if err != nil {
		t.Fatal(err)
	}
	materialResolver, err := newGroupOpsMaterialAdapter(mediaStore, freezer)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	effectClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := externaleffects.NewRepository(native, effectClient)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := newJourneyCompletionSink(groupStore)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectStore.SetCompletionSink(completion); err != nil {
		t.Fatal(err)
	}

	planID := createJourneyPlan(t, ctx, uow, groupStore, actorID, inviteID)
	evidence := &journeyEvidence{status: 1}
	runtimeService := groupopsapp.NewRuntimeService(
		uow,
		groupStore,
		groupStore,
		effectStore,
		journeyStaff{},
		nil,
		journeySender{},
		evidence,
		journeyReconciler{repository: effectStore},
		materialResolver,
	)
	runtimeService.SetDispatchEnabled(true)

	first, err := runtimeService.AcceptBroadcast(ctx, planID, actorID, "journey-broadcast-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.ID < 1 || len(first.Executions) != 2 || first.Accepted != 2 || first.ProviderExecutionEligible || first.RealExternalCallExecuted {
		t.Fatalf("accepted summary=%+v", first)
	}
	// A replay returns the same local run and execution facts without creating
	// a second EER effect.
	replayed, err := runtimeService.AcceptBroadcast(ctx, planID, actorID, "journey-broadcast-key-0001")
	if err != nil || replayed.Run.ID != first.Run.ID || len(replayed.Executions) != len(first.Executions) {
		t.Fatalf("replay=%+v err=%v first=%+v", replayed, err, first)
	}

	for index, execution := range first.Executions {
		effectID, err := strconv.ParseInt(execution.ExternalEffectID[len("eer_"):], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		var riverJobID int64
		if err = native.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, effectID).Scan(&riverJobID); err != nil {
			t.Fatal(err)
		}
		adapter := journeyProviderAdapter{result: effectport.AdapterResult{
			Completion:               effectport.StateExecuted,
			ReceiptDigest:            effectport.Hash("journey-provider-receipt", execution.ExternalEffectID),
			CallAttempted:            true,
			RealExternalCallExecuted: true,
		}}
		if index == 1 {
			adapter.result = effectport.AdapterResult{
				Completion:               effectport.StateUnknown,
				ReceiptDigest:            effectport.Hash("journey-provider-unknown", execution.ExternalEffectID),
				CallAttempted:            true,
				RealExternalCallExecuted: true,
			}
		}
		if err = effectStore.RunAttempt(ctx, effectID, 1, riverJobID, &adapter); err != nil {
			t.Fatal(err)
		}
	}

	var projected []groupopsport.Execution
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		projected, _, readErr = groupStore.ListExecutions(tx, planID, 10, 0)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(projected) != 2 {
		t.Fatalf("projected=%+v", projected)
	}
	var unknown groupopsport.Execution
	for _, execution := range projected {
		switch execution.State {
		case groupopsport.ExecutionProviderAccepted:
			if !execution.ProviderAccepted || execution.DeliveryProven || !execution.ProviderReceiptPresent {
				t.Fatalf("provider projection=%+v", execution)
			}
		case groupopsport.ExecutionOutcomeUnknown:
			unknown = execution
		default:
			t.Fatalf("unexpected projected state=%+v", execution)
		}
	}
	if unknown.ID < 1 {
		t.Fatal("unknown execution was not projected")
	}
	// An ambiguous Provider result is terminal for automatic continuation. The
	// same actual owner/EER transaction must leave its frozen successor waiting:
	// no successor execution, effect, or fresh idempotency key is created.
	if err = runtimeService.ContinueEffect(ctx, unknown.ExternalEffectID); err != nil {
		t.Fatalf("unknown continuation check: %v", err)
	}
	var unknownSuccessors, allExecutions, allEffects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_execution_intents child JOIN group_ops_execution_intents parent_intent ON parent_intent.id=child.predecessor_intent_id JOIN group_ops_executions parent ON parent.external_effect_id=parent_intent.external_effect_id WHERE parent.id=$1 AND child.state='waiting'`, unknown.ID).Scan(&unknownSuccessors); err != nil || unknownSuccessors != 1 {
		t.Fatalf("unknown successor state=%d err=%v", unknownSuccessors, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM group_ops_executions WHERE plan_id=$1`, planID).Scan(&allExecutions); err != nil || allExecutions != 2 {
		t.Fatalf("unknown produced execution count=%d err=%v", allExecutions, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&allEffects); err != nil || allEffects != 2 {
		t.Fatalf("unknown produced EER count=%d err=%v", allEffects, err)
	}
	var accepted groupopsport.Execution
	for _, execution := range projected {
		if execution.State == groupopsport.ExecutionProviderAccepted {
			accepted = execution
		}
	}
	if accepted.ID < 1 {
		t.Fatal("provider accepted execution was not projected")
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return groupStore.RecordGroupMessageTask(tx, groupopsport.GroupMessageReceipt{ExecutionID: accepted.ID, ExternalEffectID: accepted.ExternalEffectID, MessageID: "journey-msgid", SenderUserID: "journey-sender", ChatID: "chat-journey-1", TaskEvidenceDigest: string(effectport.Hash("journey-task", accepted.ExternalEffectID))})
	}); err != nil {
		t.Fatal(err)
	}
	read, err := runtimeService.ReadProviderDelivery(ctx, groupopsport.ProviderDeliveryReadCommand{ExecutionID: accepted.ID, ActorID: actorID, IdempotencyKey: "journey-delivery-read-0001"})
	if err != nil || !read.DeliveryProven || read.DeliveryStatus == nil || *read.DeliveryStatus != 1 || read.State != groupopsport.ExecutionProviderAccepted {
		t.Fatalf("delivery read=%+v err=%v", read, err)
	}
	acceptedEffectID, err := strconv.ParseInt(accepted.ExternalEffectID[len("eer_"):], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var acceptedEffectState string
	if err = native.QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, acceptedEffectID).Scan(&acceptedEffectState); err != nil || acceptedEffectState != string(effectport.StateExecuted) {
		t.Fatalf("accepted EER state=%q err=%v", acceptedEffectState, err)
	}
	var materialJSON, sourceJSON []byte
	var sourceDigest string
	if err = native.QueryRow(ctx, `SELECT material_snapshot,material_source_snapshot,material_source_digest FROM group_ops_executions WHERE id=$1`, accepted.ID).Scan(&materialJSON, &sourceJSON, &sourceDigest); err != nil {
		t.Fatal(err)
	}
	var sourceValue any
	if json.Unmarshal(sourceJSON, &sourceValue) != nil {
		t.Fatalf("source snapshot=%s", sourceJSON)
	}
	canonicalSource, err := json.Marshal(sourceValue)
	if err != nil || sourceDigest != string(effectport.Hash("group-ops.material.intent.v1", string(canonicalSource))) {
		t.Fatalf("source digest=%q canonical=%s err=%v", sourceDigest, canonicalSource, err)
	}
	// New accepted executions freeze source-only facts. Verify the actual Media
	// capture boundary after the JSONB round trip; credential preparation is
	// deferred to the EER preflight and therefore must not be inferred here.
	readiness := groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaStore, freezer: freezer}
	if err = readiness.VerifyFrozenMaterialSources(ctx, materialJSON, sourceJSON, sourceDigest); err != nil {
		t.Fatalf("real Media source verification after JSONB round trip: %v", err)
	}
	if evidence.calls != 1 {
		t.Fatalf("provider delivery calls=%d", evidence.calls)
	}
	// A same-key retry performs another safe Provider read, then returns the
	// persisted completed receipt without a second local mutation.
	sameKey, err := runtimeService.ReadProviderDelivery(ctx, groupopsport.ProviderDeliveryReadCommand{ExecutionID: accepted.ID, ActorID: actorID, IdempotencyKey: "journey-delivery-read-0001"})
	if err != nil || !sameKey.DeliveryProven || sameKey.DeliveryStatus == nil || *sameKey.DeliveryStatus != 1 || evidence.calls != 2 {
		t.Fatalf("same key=%+v err=%v calls=%d", sameKey, err, evidence.calls)
	}
	evidence.status = 0 // a stale page must never downgrade an established delivery fact.
	replayedDelivery, err := runtimeService.ReadProviderDelivery(ctx, groupopsport.ProviderDeliveryReadCommand{ExecutionID: accepted.ID, ActorID: actorID, IdempotencyKey: "journey-delivery-read-0002"})
	if err != nil || !replayedDelivery.DeliveryProven || replayedDelivery.DeliveryStatus == nil || *replayedDelivery.DeliveryStatus != 1 || evidence.calls != 3 {
		t.Fatalf("stale delivery=%+v err=%v calls=%d", replayedDelivery, err, evidence.calls)
	}
	// Run the two observations in independent real PostgreSQL transactions.
	// The status=0 writer may run before or after status=1, but the owner SQL
	// CAS must leave the persisted fact at status=1/delivery_proven=true.
	if _, err = native.Exec(ctx, `UPDATE group_ops_group_message_tasks SET delivery_status=NULL,delivery_evidence_digest=NULL,delivery_checked_at=NULL WHERE execution_id=$1`, accepted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE group_ops_executions SET delivery_proven=FALSE WHERE id=$1`, accepted.ID); err != nil {
		t.Fatal(err)
	}
	concurrentCtx, cancelConcurrent := context.WithTimeout(ctx, 20*time.Second)
	defer cancelConcurrent()
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, status := range []int{0, 1} {
		status := status
		wg.Add(1)
		go func() {
			defer wg.Done()
			digest := string(effectport.Hash("journey-delivery-concurrent", accepted.ExternalEffectID, strconv.Itoa(status)))
			errCh <- uow.Within(concurrentCtx, func(tx context.Context) error {
				return groupStore.RecordGroupMessageDelivery(tx, groupopsport.GroupMessageReceipt{ExecutionID: accepted.ID, ExternalEffectID: accepted.ExternalEffectID, MessageID: "journey-msgid", SenderUserID: "journey-sender", ChatID: "chat-journey-1", DeliveryStatus: &status, DeliveryEvidenceDigest: digest}, digest)
			})
		}()
	}
	wg.Wait()
	close(errCh)
	for concurrentErr := range errCh {
		if concurrentErr != nil {
			t.Fatal(concurrentErr)
		}
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		accepted, readErr = groupStore.GetExecution(tx, accepted.ID)
		return readErr
	}); err != nil || !accepted.DeliveryProven || accepted.DeliveryStatus == nil || *accepted.DeliveryStatus != 1 {
		t.Fatalf("concurrent monotonic delivery=%+v err=%v", accepted, err)
	}

	var generation, fence int64
	var lease time.Time
	effectID, err := strconv.ParseInt(unknown.ExternalEffectID[len("eer_"):], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, effectID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT generation,lease_fence,lease_expires_at FROM external_effects WHERE id=$1`, effectID).Scan(&generation, &fence, &lease); err != nil {
		t.Fatal(err)
	}
	evidenceDigest := string(effectport.Hash("journey-independent-evidence", unknown.ExternalEffectID))
	reconciled, err := runtimeService.ManualReconcile(ctx, groupopsport.ManualReconcileCommand{
		ExecutionID:    unknown.ID,
		ActorID:        actorID,
		IdempotencyKey: "journey-reconcile-key-0001",
		Generation:     generation,
		Fence:          fence,
		LeaseExpiresAt: lease,
		EvidenceDigest: evidenceDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != groupopsport.ExecutionReconciled || reconciled.DeliveryProven || !reconciled.ReconciliationEvidencePresent {
		t.Fatalf("reconciled=%+v", reconciled)
	}
	var finalEffectState string
	if err = native.QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, effectID).Scan(&finalEffectState); err != nil {
		t.Fatal(err)
	}
	if finalEffectState != string(effectport.StateReconciled) {
		t.Fatalf("EER state=%q", finalEffectState)
	}
}

// TestGroupOpsSharedRiverRuntimeJourney runs the production-shaped Group Ops
// seam: owner store -> EER -> outbound GroupMessageProvider -> local WeCom
// protocol server -> completion receipt -> shared River continuation.  It
// deliberately starts, stops, and starts the shared River runtime around a
// delayed successor.  The only clock advance is the test's River-row fixture;
// the application still persists the successor as a future scheduled job.
func TestGroupOpsSharedRiverRuntimeJourney(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}

	provider := newGroupOpsRuntimeWeCom(t)
	defer provider.Close()
	wecomClient, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "runtime-corp", ContactSecret: "runtime-contact-secret", APIBase: provider.URL(), HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}

	var actorID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('groupops-river','$argon2id$river','Group Ops River','journey-sender',true,false) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{reader: mediaStore})
	if err != nil {
		t.Fatal(err)
	}
	materials, err := newGroupOpsMaterialAdapter(mediaStore, freezer)
	if err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	effectsModule := externaleffects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	continuationWorker := groupopsapp.NewContinuationWorker()
	if err = river.AddWorkerSafely[groupopsapp.ContinuationJobArgs](workers, continuationWorker); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := externaleffects.NewRepository(native, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	// This is the actual Composition Root bridge: Access owns staff activity
	// and the verified WeCom sender. Revoking is_active below therefore tests
	// the real current-qualification read, not a mutable test Port.
	staff := groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: groupStore}
	runtimeService := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, effectStore, staff, nil, staff, nil, journeyReconciler{repository: effectStore}, materials)
	runtimeService.SetDispatchEnabled(true)
	if err = continuationWorker.Bind(runtimeService); err != nil {
		t.Fatal(err)
	}
	readiness := groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaStore, freezer: freezer}
	groupProvider, err := outbound.NewGroupMessageProvider(outbound.GroupMessageProviderConfig{
		Enabled: true, Executions: groupOpsDispatchReader{uow: uow, execution: groupStore, senders: staff}, Materials: readiness, Writer: wecomClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = effectsModule.SetProviderAdapter(groupProvider); err != nil {
		t.Fatal(err)
	}
	if _, err = effectsModule.Bind(effectStore, groupOpsRuntimeEffectSecurity{}); err != nil {
		t.Fatal(err)
	}
	continuations, err := groupopsapp.NewRiverContinuationEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := outbound.NewGroupMessageCompletionSink(groupStore, groupStore)
	if err != nil {
		t.Fatal(err)
	}
	completion.WithContinuation(continuations)
	if err = effectStore.SetCompletionSink(completion); err != nil {
		t.Fatal(err)
	}
	runtimeService.SetEvidenceVerifier(wecomGroupOpsEvidence{uow: uow, receipts: groupStore, reader: wecomClient})

	planID := createRiverJourneyPlan(t, ctx, uow, groupStore, actorID)
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at) VALUES
('chat-river-1',$1,'River one',1,$2,clock_timestamp()),
('chat-river-2',$1,'River two',1,$3,clock_timestamp())`, actorID, string(effectport.Hash("runtime-directory", "chat-river-1")), string(effectport.Hash("runtime-directory", "chat-river-2"))); err != nil {
		t.Fatal(err)
	}

	sharedRuntime, err := platformjobqueue.NewRuntime(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	start, stop := startGroupOpsRiver(t, sharedRuntime)
	first, err := runtimeService.AcceptBroadcast(ctx, planID, actorID, "river-runtime-broadcast-0001")
	if err != nil || first.Accepted != 2 || len(first.Executions) != 2 {
		t.Fatalf("first acceptance=%+v err=%v", first, err)
	}
	// The process is deliberately down while acceptance commits. These are real
	// EER and River rows, so starting the shared runtime afterwards proves that
	// an accepted broadcast survives a process boundary before any Provider
	// call happens.
	assertGroupOpsInitialEffectsPersisted(t, native, planID, 2)
	start()
	waitGroupOpsProviderCalls(t, native, provider, 2)
	waitGroupOpsDelayedSuccessors(t, native, planID, 2)
	stop()

	var scheduled int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM river_job job JOIN external_effect_jobs effect_job ON effect_job.river_job_id=job.id JOIN group_ops_executions execution ON execution.external_effect_id=effect_job.effect_id WHERE execution.plan_id=$1 AND execution.node_position=3 AND job.kind='external_effect.execute.v1' AND job.state='scheduled' AND execution.scheduled_for > clock_timestamp()`, planID).Scan(&scheduled); err != nil || scheduled != 2 {
		t.Fatalf("delayed River jobs=%d err=%v", scheduled, err)
	}

	// Restarting before the persisted delay expires cannot call WeCom again.
	sharedRuntime, err = platformjobqueue.NewRuntime(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	start, stop = startGroupOpsRiver(t, sharedRuntime)
	start()
	time.Sleep(250 * time.Millisecond)
	if got := provider.callCount(); got != 2 {
		t.Fatalf("delayed successor ran before due time: calls=%d", got)
	}
	if tag, err := native.Exec(ctx, `UPDATE river_job job SET state='available', scheduled_at=clock_timestamp()-interval '1 second' FROM external_effect_jobs effect_job JOIN group_ops_executions execution ON execution.external_effect_id=effect_job.effect_id WHERE job.id=effect_job.river_job_id AND execution.plan_id=$1 AND execution.node_position=3 AND job.kind='external_effect.execute.v1' AND job.state='scheduled' AND execution.scheduled_for > clock_timestamp()`, planID); err != nil || tag.RowsAffected() != 2 {
		stop()
		t.Fatalf("advance scheduled fixture rows=%d err=%v", tag.RowsAffected(), err)
	}
	waitGroupOpsProviderCalls(t, native, provider, 4)
	stop()

	if got := provider.callsByChat(); got["chat-river-1"] == nil || got["chat-river-2"] == nil || len(got["chat-river-1"]) != 2 || len(got["chat-river-2"]) != 2 || got["chat-river-1"][0] != "first" || got["chat-river-1"][1] != "second" || got["chat-river-2"][0] != "first" || got["chat-river-2"][1] != "second" {
		t.Fatalf("per-group River ordering=%+v", got)
	}

	page, err := runtimeService.ListExecutions(ctx, planID, 10, 0)
	if err != nil || len(page.Items) != 4 {
		t.Fatalf("execution page=%+v err=%v", page, err)
	}
	var accepted groupopsport.Execution
	for _, execution := range page.Items {
		if execution.State != groupopsport.ExecutionProviderAccepted || !execution.ProviderAccepted || execution.DeliveryProven || !execution.ProviderReceiptPresent {
			t.Fatalf("task acceptance projection=%+v", execution)
		}
		accepted = execution
	}
	delivery, err := runtimeService.ReadProviderDelivery(ctx, groupopsport.ProviderDeliveryReadCommand{ExecutionID: accepted.ID, ActorID: actorID, IdempotencyKey: "river-runtime-delivery-read-0001"})
	if err != nil || !delivery.DeliveryProven || delivery.DeliveryStatus == nil || *delivery.DeliveryStatus != 1 || delivery.State != groupopsport.ExecutionProviderAccepted {
		t.Fatalf("separate delivery proof=%+v err=%v", delivery, err)
	}

	// This interruption happens after the actual local WeCom leaf receives the
	// first-node request. River records outcome_unknown on its original EER;
	// the frozen successor remains waiting and neither a new EER nor a retry
	// key is minted on restart.
	unknownPlanID := createRiverJourneyPlanWithMessages(t, ctx, uow, groupStore, actorID, "provider result intentionally unknown", "must stay blocked")
	unknownRun, acceptUnknownErr := runtimeService.AcceptBroadcast(ctx, unknownPlanID, actorID, "river-runtime-unknown-0001")
	if acceptUnknownErr != nil || unknownRun.Accepted != 2 {
		t.Fatalf("unknown acceptance=%+v err=%v", unknownRun, acceptUnknownErr)
	}
	unknownRuntime, runtimeErr := platformjobqueue.NewRuntime(native, workers)
	if runtimeErr != nil {
		t.Fatal(runtimeErr)
	}
	unknownStart, unknownStop := startGroupOpsRiver(t, unknownRuntime)
	unknownStart()
	waitGroupOpsProviderCalls(t, native, provider, 6)
	waitGroupOpsUnknownFirstNode(t, native, unknownPlanID)
	unknownStop()

	// Each case accepts against an active plan first, then changes one current
	// authorization/binding fact while River is down. Starting the real shared
	// runtime must finalize the EER effects without ever reaching the local
	// WeCom writer.
	planService := groupopsapp.NewService(uow, groupStore, staff, groupStore)
	mutateAcceptedPlan := func(planID int64, bumpRevision bool, change func(*groupopsport.Detail)) error {
		// Active plans are intentionally immutable through the public service.
		// The dispatch guard must still fail closed if its accepted snapshot is
		// older than the current owner fact, so set up that fact through the real
		// Store in one PostgreSQL Unit of Work.
		return uow.Within(ctx, func(tx context.Context) error {
			detail, err := groupStore.Get(tx, planID)
			if err != nil {
				return err
			}
			change(&detail)
			if bumpRevision {
				detail.Plan.Revision++
			}
			detail.Plan.UpdatedAt = time.Now().UTC()
			return groupStore.Save(tx, detail)
		})
	}
	assertPreCallBlock := func(label string, mutate func(int64) error) {
		t.Helper()
		blockedPlanID := createRiverJourneyPlan(t, ctx, uow, groupStore, actorID)
		acceptedRun, acceptErr := runtimeService.AcceptBroadcast(ctx, blockedPlanID, actorID, "river-precall-"+label+"-0001")
		if acceptErr != nil || acceptedRun.Accepted != 2 {
			t.Fatalf("%s acceptance=%+v err=%v", label, acceptedRun, acceptErr)
		}
		before := provider.callCount()
		if mutateErr := mutate(blockedPlanID); mutateErr != nil {
			t.Fatalf("%s mutation: %v", label, mutateErr)
		}
		blockedRuntime, runtimeErr := platformjobqueue.NewRuntime(native, workers)
		if runtimeErr != nil {
			t.Fatal(runtimeErr)
		}
		blockedStart, blockedStop := startGroupOpsRiver(t, blockedRuntime)
		blockedStart()
		waitGroupOpsPreCallFailures(t, native, blockedPlanID, 2)
		blockedStop()
		if after := provider.callCount(); after != before {
			t.Fatalf("%s reached WeCom: before=%d after=%d", label, before, after)
		}
	}
	assertPreCallBlock("revision", func(blockedPlanID int64) error {
		return mutateAcceptedPlan(blockedPlanID, true, func(*groupopsport.Detail) {})
	})
	assertPreCallBlock("group-binding", func(blockedPlanID int64) error {
		// Keep the accepted revision intact: LoadDispatchExecution must reject
		// the current target binding itself, rather than merely the version.
		if err := mutateAcceptedPlan(blockedPlanID, false, func(detail *groupopsport.Detail) {
			detail.GroupAssets = []groupopsport.GroupAsset{}
		}); err != nil {
			return err
		}
		var revision, bindings int64
		if err := native.QueryRow(ctx, `SELECT p.revision,count(a.id) FROM group_ops_plans p LEFT JOIN group_ops_plan_group_assets a ON a.plan_id=p.id WHERE p.id=$1 GROUP BY p.id`, blockedPlanID).Scan(&revision, &bindings); err != nil {
			return err
		}
		if revision != 1 || bindings != 0 {
			return fmt.Errorf("binding guard setup revision=%d bindings=%d", revision, bindings)
		}
		return nil
	})
	assertPreCallBlock("paused", func(blockedPlanID int64) error {
		_, pauseErr := planService.Pause(ctx, groupopsport.TransitionCommand{PlanID: blockedPlanID, ExpectedRevision: 1, Actor: actorID, IdempotencyKey: "river-precall-paused-0001"})
		return pauseErr
	})
	assertPreCallBlock("paused-basic-save", func(blockedPlanID int64) error {
		paused, err := planService.Pause(ctx, groupopsport.TransitionCommand{PlanID: blockedPlanID, ExpectedRevision: 1, Actor: actorID, IdempotencyKey: "river-precall-save-pause"})
		if err != nil {
			return err
		}
		saved, err := planService.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: blockedPlanID, ExpectedRevision: paused.Plan.Revision, Name: "saved while paused", OwnerStaffID: actorID, OwnerStaffIDSet: true, Actor: actorID, IdempotencyKey: "river-precall-save-basic"})
		if err != nil {
			return err
		}
		if saved.Plan.Status != groupopsport.PlanPaused || saved.Plan.Revision != paused.Plan.Revision+1 {
			return fmt.Errorf("basic save changed lifecycle or missed revision")
		}
		return nil
	})
	assertPreCallBlock("paused-reactivated", func(blockedPlanID int64) error {
		paused, pauseErr := planService.Pause(ctx, groupopsport.TransitionCommand{PlanID: blockedPlanID, ExpectedRevision: 1, Actor: actorID, IdempotencyKey: "river-precall-resume-pause"})
		if pauseErr != nil {
			return pauseErr
		}
		// Resuming enables only new valid work. The already accepted execution
		// carries revision 1, so the dispatch reader still blocks it before the
		// Provider boundary after the plan reaches revision 3.
		_, resumeErr := planService.Activate(ctx, groupopsport.TransitionCommand{PlanID: blockedPlanID, ExpectedRevision: paused.Plan.Revision, Actor: actorID, IdempotencyKey: "river-precall-resume-active"})
		return resumeErr
	})
	assertPreCallBlock("archived", func(blockedPlanID int64) error {
		_, archiveErr := planService.Archive(ctx, groupopsport.TransitionCommand{PlanID: blockedPlanID, ExpectedRevision: 1, Actor: actorID, IdempotencyKey: "river-precall-archived-0001"})
		return archiveErr
	})
	assertPreCallBlock("sender-revoked", func(int64) error {
		// Access is the owner of this qualification fact. The accepted EER must
		// fail before the Provider leaf reads its sender, even though the plan
		// still names the same local owner and remains revision 1.
		tag, revokeErr := native.Exec(ctx, `UPDATE admin_users SET is_active=false WHERE id=$1 AND is_active=true`, actorID)
		if revokeErr != nil || tag.RowsAffected() != 1 {
			return fmt.Errorf("revoke Access sender rows=%d: %w", tag.RowsAffected(), revokeErr)
		}
		return nil
	})
}

// TestGroupOpsSharedRiverMaterialPreparationAutoResumes proves that an
// accepted Group Ops image intent needs no second operator action when its
// temporary provider credential is absent: the original group EER snoozes
// before an EER attempt, preparation completes separately, and that same
// River job resumes with the Media-owned credential.
func TestGroupOpsSharedRiverMaterialPreparationAutoResumes(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}

	provider := newGroupOpsRuntimeWeCom(t)
	defer provider.Close()
	releaseUpload := provider.holdUploads()
	defer releaseUpload()
	wecomClient, err := wecomadapter.New(wecomadapter.Config{
		Enabled: true, CorpID: "runtime-corp", AgentID: "runtime-agent", Secret: "runtime-app-secret", ContactSecret: "runtime-contact-secret",
		AdminCallbackURI: "https://example.test/admin-callback", SidebarCallbackURI: "https://example.test/sidebar-callback",
		APIBase: provider.URL(), HTTPClient: provider.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var actorID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('groupops-material-river','$argon2id$material','Group Ops Material River','journey-sender',true,false) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	// This source predates the binding below, as can occur for a retained
	// library record. It is valid and frozen at acceptance but has no cached
	// credential or already-accepted material EER.
	image, err := mediaStore.CreateImage(ctx, actorID, "groupops-material-image-0001", mediastore.ImageInput{FileName: "journey.png", MIME: "image/png", Name: "Group Ops journey", Content: []byte("group-ops-media-fixture"), Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	imageID, ok := image["id"].(int64)
	if !ok || imageID < 1 {
		t.Fatalf("image=%+v", image)
	}

	workers := river.NewWorkers()
	effectsModule := externaleffects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := externaleffects.NewRepository(native, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	scopeDigest := string(effectport.Hash("groupops.material.auto-resume.scope.v1"))
	materialPreparation, err := outbound.NewMaterialPreparationService(uow, effectStore, native, mediaStore)
	if err != nil {
		t.Fatal(err)
	}
	if err = mediaStore.BindMaterialPreparationAccepter(materialPreparation, scopeDigest); err != nil {
		t.Fatal(err)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{sources: mediaStore, preparer: materialPreparation, scopeDigest: scopeDigest})
	if err != nil {
		t.Fatal(err)
	}
	lessonPNG := groupOpsLessonCardPNG(t)
	var lessonCoverReads atomic.Int32
	lessonCards := mediaapp.NewWebhookLessonCardResolver(mediaStore, groupOpsLessonCardRoundTripper(func(request *http.Request) (*http.Response, error) {
		lessonCoverReads.Add(1)
		if request.Method != http.MethodGet || request.URL.String() != "https://ip.lhbl.com.cn/api/share/lesson-card/11111111-2222-3333-4444-555555555555.png" {
			return nil, fmt.Errorf("unexpected daily lesson cover request %s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: int64(len(lessonPNG)), Body: io.NopCloser(bytes.NewReader(lessonPNG))}, nil
	}))
	materials, err := newUnifiedGroupOpsMaterialAdapter(mediaStore, freezer, mediaStore, materialPreparation, scopeDigest, lessonCards)
	if err != nil {
		t.Fatal(err)
	}
	staff := groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: groupStore}
	runtimeService := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, effectStore, staff, nil, staff, nil, journeyReconciler{repository: effectStore}, materials)
	runtimeService.SetDispatchEnabled(true)
	readiness := groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaStore, freezer: freezer}
	groupProvider, err := outbound.NewGroupMessageProvider(outbound.GroupMessageProviderConfig{
		Enabled: true, Executions: groupOpsDispatchReader{uow: uow, execution: groupStore, senders: staff}, Materials: readiness, FrozenSources: readiness,
		Writer: wecomClient, Sources: mediaStore, Preparer: materialPreparation, ScopeDigest: scopeDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	materialUploader, err := wecomadapter.NewMaterialUploader(wecomClient, scopeDigest)
	if err != nil {
		t.Fatal(err)
	}
	materialProvider, err := outbound.NewMaterialPreparationProvider(materialPreparation, materialUploader)
	if err != nil {
		t.Fatal(err)
	}
	providerRouter := outbound.NewProviderRouterWithGroupMessage(nil, groupProvider).WithSidebarMedia(outbound.MaterialEffectMux{GenericProvider: materialProvider})
	if err = effectsModule.SetProviderAdapter(providerRouter); err != nil {
		t.Fatal(err)
	}
	if _, err = effectsModule.Bind(effectStore, groupOpsRuntimeEffectSecurity{}); err != nil {
		t.Fatal(err)
	}
	groupCompletion, err := outbound.NewGroupMessageCompletionSink(groupStore, groupStore)
	if err != nil {
		t.Fatal(err)
	}
	completionRouter, err := outbound.NewCompletionRouter(nil, groupCompletion)
	if err != nil {
		t.Fatal(err)
	}
	completionRouter.WithSidebarMedia(outbound.MaterialEffectMux{GenericCompletion: materialPreparation})
	if err = effectStore.SetCompletionSink(completionRouter); err != nil {
		t.Fatal(err)
	}

	planID := createRiverJourneyPlanWithImage(t, ctx, uow, groupStore, actorID, imageID)
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at) VALUES ('chat-river-1',$1,'River material',1,$2,clock_timestamp())`, actorID, string(effectport.Hash("runtime-directory", "chat-river-1"))); err != nil {
		t.Fatal(err)
	}
	first, err := runtimeService.AcceptBroadcast(ctx, planID, actorID, "river-runtime-material-0001")
	if err != nil || first.Accepted != 1 || len(first.Executions) != 1 {
		t.Fatalf("first acceptance=%+v err=%v", first, err)
	}
	groupEffectID := first.Executions[0].ExternalEffectID
	if groupEffectID == "" {
		t.Fatalf("group execution lacks effect=%+v", first.Executions[0])
	}
	if len(groupEffectID) <= len("eer_") {
		t.Fatalf("invalid group effect ID %q", groupEffectID)
	}
	groupEffectNumeric, parseErr := strconv.ParseInt(groupEffectID[len("eer_"):], 10, 64)
	if parseErr != nil || groupEffectNumeric < 1 {
		t.Fatalf("invalid group effect ID %q: %v", groupEffectID, parseErr)
	}
	var materialRaw []byte
	if err = native.QueryRow(ctx, `SELECT material_snapshot FROM group_ops_executions WHERE external_effect_id=$1`, groupEffectNumeric).Scan(&materialRaw); err != nil {
		t.Fatal(err)
	}
	var intent mediaport.GroupOpsMaterialIntentSnapshot
	if json.Unmarshal(materialRaw, &intent) != nil || mediaport.ValidateGroupOpsMaterialIntentSnapshot(intent) != nil || len(intent.Attachments) != 1 || intent.Attachments[0].MsgType != "image" || intent.Attachments[0].MediaID != "" {
		t.Fatalf("accepted snapshot must freeze source-only intent: %s", materialRaw)
	}
	var preexisting int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_material_preparations WHERE source_ref=$1`, "image:"+strconv.FormatInt(imageID, 10)).Scan(&preexisting); err != nil || preexisting != 0 {
		t.Fatalf("unexpected pre-existing preparation=%d err=%v", preexisting, err)
	}
	var groupJobID int64
	if err = native.QueryRow(ctx, `SELECT job.river_job_id FROM external_effect_jobs job WHERE job.effect_id=$1`, groupEffectNumeric).Scan(&groupJobID); err != nil {
		t.Fatal(err)
	}

	sharedRuntime, err := platformjobqueue.NewRuntime(native, workers, platformjobqueue.OutboundQueue, platformjobqueue.OutboundMediaQueue)
	if err != nil {
		t.Fatal(err)
	}
	start, stop := startGroupOpsRiver(t, sharedRuntime)
	start()
	defer func() { releaseUpload(); stop() }()
	waitGroupOpsMaterialPreflightSnooze(t, native, planID, imageID, groupJobID)
	if got := provider.callCount(); got != 0 {
		t.Fatalf("group provider reached before material credential: calls=%d", got)
	}
	if got := provider.uploadCount(); got != 0 {
		t.Fatalf("material upload completed before release: uploads=%d", got)
	}

	// Releasing the isolated provider fixture completes the Material EER. The
	// original Group Ops River row, rather than a second AcceptBroadcast, must
	// then run and use exactly the returned temporary media ID.
	releaseUpload()
	waitGroupOpsProviderCalls(t, native, provider, 1)
	var persistedGroupJob int64
	var groupState string
	var groupAttempts int
	// The fixture records the HTTP call before the worker commits completion.
	// Wait for that durable receipt, not merely the provider-side call counter.
	completionDeadline := time.Now().Add(12 * time.Second)
	for {
		if err = native.QueryRow(ctx, `SELECT job.river_job_id,effect.state,effect.attempt_count FROM external_effects effect JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation WHERE effect.id=$1`, groupEffectNumeric).Scan(&persistedGroupJob, &groupState, &groupAttempts); err != nil {
			t.Fatal(err)
		}
		if groupState == "executed" || time.Now().After(completionDeadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if persistedGroupJob != groupJobID || groupState != "executed" || groupAttempts != 1 {
		t.Fatalf("group effect did not resume its original River row: job=%d/%d state=%s attempts=%d", persistedGroupJob, groupJobID, groupState, groupAttempts)
	}
	if got := provider.uploadCount(); got != 1 {
		t.Fatalf("material uploads=%d want=1", got)
	}
	if calls := provider.callsByChat(); len(calls["chat-river-1"]) != 1 {
		t.Fatalf("group calls=%+v", calls)
	}
	if mediaIDs := provider.mediaIDsByChat()["chat-river-1"]; len(mediaIDs) != 1 || mediaIDs[0] != "runtime-prepared-media-1" {
		t.Fatalf("group did not use prepared media ID: %+v", provider.mediaIDsByChat())
	}

	// Exercise the new automatic daily-lesson source through the production
	// unified Media adapter. The local image/card is accepted with its Group
	// Ops run, then the existing Material EER prepares its thumbnail and the
	// original Group EER resumes into a miniprogram pic_media_id request.
	planService := groupopsapp.NewService(uow, groupStore, staff, groupStore)
	webhookDetail, err := planService.Create(ctx, groupopsport.CreatePlanCommand{Name: "River daily lesson webhook", Actor: actorID, IdempotencyKey: "groupops-river-webhook-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	webhookDetail, err = planService.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: webhookDetail.Plan.ID, ExpectedRevision: webhookDetail.Plan.Revision, Name: webhookDetail.Plan.Name, PlanType: groupopsport.PlanTypeWebhook, Actor: actorID, IdempotencyKey: "groupops-river-webhook-type-0001"})
	if err != nil {
		t.Fatal(err)
	}
	webhookDetail, err = planService.AddMember(ctx, groupopsport.MemberCommand{PlanID: webhookDetail.Plan.ID, ExpectedRevision: webhookDetail.Plan.Revision, StaffID: actorID, Actor: actorID, IdempotencyKey: "groupops-river-webhook-member-0001"})
	if err != nil {
		t.Fatal(err)
	}
	webhookDetail, err = planService.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: webhookDetail.Plan.ID, ExpectedRevision: webhookDetail.Plan.Revision, AssetRef: "chat-river-1", Actor: actorID, IdempotencyKey: "groupops-river-webhook-group-0001"})
	if err != nil {
		t.Fatal(err)
	}
	webhookDetail, err = planService.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: webhookDetail.Plan.ID, ExpectedRevision: webhookDetail.Plan.Revision, Reference: "river-daily-lesson-hook", Actor: actorID, IdempotencyKey: "groupops-river-webhook-descriptor-0001"})
	if err != nil {
		t.Fatal(err)
	}
	webhookDetail, err = planService.Activate(ctx, groupopsport.TransitionCommand{PlanID: webhookDetail.Plan.ID, ExpectedRevision: webhookDetail.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-river-webhook-activate-0001"})
	if err != nil || webhookDetail.Plan.Status != groupopsport.PlanActive || len(webhookDetail.Nodes) != 0 {
		t.Fatalf("webhook detail=%+v err=%v", webhookDetail, err)
	}
	autoSummary, err := runtimeService.AcceptWebhook(ctx, "river-daily-lesson-hook", "river-daily-lesson-event-0001", groupOpsLessonCardInbound("river-daily-lesson-hook", "chat-river-1"))
	if err != nil || autoSummary.Accepted != 1 || len(autoSummary.Executions) != 1 || lessonCoverReads.Load() != 1 {
		t.Fatalf("daily lesson acceptance=%+v reads=%d err=%v", autoSummary, lessonCoverReads.Load(), err)
	}
	waitGroupOpsProviderCalls(t, native, provider, 2)
	autoEffectID, parseErr := strconv.ParseInt(autoSummary.Executions[0].ExternalEffectID[len("eer_"):], 10, 64)
	if parseErr != nil || autoEffectID < 1 {
		t.Fatalf("daily lesson effect=%q err=%v", autoSummary.Executions[0].ExternalEffectID, parseErr)
	}
	completedAuto := false
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err = native.QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, autoEffectID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "executed" {
			completedAuto = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !completedAuto || provider.uploadCount() != 2 {
		t.Fatalf("daily lesson effect did not complete; uploads=%d", provider.uploadCount())
	}
	if mediaIDs := provider.miniProgramPicMediaIDsByChat()["chat-river-1"]; len(mediaIDs) != 1 || mediaIDs[0] != "runtime-prepared-media-1" {
		t.Fatalf("daily lesson miniprogram did not use pic_media_id: %+v", provider.miniProgramPicMediaIDsByChat())
	}
}

// TestGroupOpsOperationMemberDirectoryPersistsVerifiedNames proves the
// profile overlay is a Group Ops-owned projection: it is restricted to the
// source snapshot and a later provider failure keeps the last verified name
// while reporting an unavailable state instead of clearing the picker.
func TestGroupOpsOperationMemberDirectoryPersistsVerifiedNames(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}
	var first, second int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('member-one','$argon2id$member','本地一','member-one',true,false) RETURNING id`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('member-two','$argon2id$member','本地二','member-two',true,false) RETURNING id`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	directory := &operationMemberDirectoryJourney{items: []groupopsport.OperationMember{
		{StaffID: first, SenderUserID: "member-one", DisplayName: "真实一", Active: true, NameSource: "wecom_profile", ProfileReadState: "unavailable", ProfileReadErrorCode: "provider_permission_denied", ProfileRefreshedAt: &now},
		{StaffID: second, SenderUserID: "member-two", DisplayName: "本地二", Active: true, NameSource: "local_fallback", ProfileReadState: "unavailable", ProfileReadErrorCode: "provider_permission_denied"},
	}}
	staff := operationMemberJourneyStaff{items: []groupopsport.OperationMember{{StaffID: first, SenderUserID: "member-one", DisplayName: "本地一"}, {StaffID: second, SenderUserID: "member-two", DisplayName: "本地二"}}}
	runtimeService := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, nil, staff, directory, journeySender{}, nil, nil)
	page, err := runtimeService.RefreshOperationMembers(ctx, groupopsport.OperationMemberRefreshCommand{ActorID: first, PageSize: 100, IdempotencyKey: "group-ops-member-directory-0001"})
	if err != nil || len(page.Items) != 2 || page.Items[0].DisplayName != "真实一" || page.Items[0].NameSource != "wecom_profile" || page.ProfileReadState != "unavailable" || page.ProfileReadErrorCode != "provider_permission_denied" {
		t.Fatalf("refresh page=%+v err=%v", page, err)
	}
	directory.err = operationMemberDirectoryFailure{}
	page, err = runtimeService.RefreshOperationMembers(ctx, groupopsport.OperationMemberRefreshCommand{ActorID: first, PageSize: 100, IdempotencyKey: "group-ops-member-directory-0002"})
	if err != nil || len(page.Items) != 2 || page.Items[0].DisplayName != "真实一" || page.Items[0].NameSource != "wecom_profile" || page.ProfileReadState != "unavailable" || page.ProfileReadErrorCode != "provider_unavailable" {
		t.Fatalf("preserved page=%+v err=%v", page, err)
	}
}

type operationMemberDirectoryJourney struct {
	items []groupopsport.OperationMember
	err   error
}

func (directory *operationMemberDirectoryJourney) ListOwnedGroups(context.Context, int64, int32) (groupopsport.GroupDirectorySnapshot, error) {
	return groupopsport.GroupDirectorySnapshot{}, errors.New("not used by operation-member journey")
}

func (directory *operationMemberDirectoryJourney) RefreshOperationMembers(context.Context, int32) ([]groupopsport.OperationMember, error) {
	if directory.err != nil {
		return nil, directory.err
	}
	return append([]groupopsport.OperationMember(nil), directory.items...), nil
}

type operationMemberJourneyStaff struct {
	items []groupopsport.OperationMember
}

func (staff operationMemberJourneyStaff) ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error) {
	return append([]groupopsport.OperationMember(nil), staff.items...), nil
}

type operationMemberDirectoryFailure struct{}

func (operationMemberDirectoryFailure) Error() string                   { return "provider unavailable" }
func (operationMemberDirectoryFailure) DirectoryFailureCode() string    { return "provider_unavailable" }
func (operationMemberDirectoryFailure) DirectoryFailureRetryable() bool { return true }

// TestGroupOpsPostgreSQLPausedPlanReactivation proves the frozen list's
// enable action works after a pause against the real receipt/event owner
// store. It is deliberately lifecycle-only: no effect is accepted or sent.
func TestGroupOpsPostgreSQLPausedPlanReactivation(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}
	var actorID, replacementID, inactiveID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('groupops-reactivate','$argon2id$reactivate','Group Ops Reactivate','journey-sender',true,false) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('groupops-replacement','$argon2id$replacement','Group Ops Replacement','replacement-sender',true,false) RETURNING id`).Scan(&replacementID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('groupops-inactive','$argon2id$inactive','Group Ops Inactive','inactive-sender',false,false) RETURNING id`).Scan(&inactiveID); err != nil {
		t.Fatal(err)
	}
	store, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := groupopsapp.NewService(uow, store, groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: store}, store)
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "PG paused reactivation", Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-create"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: actorID, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-member"})
	if err != nil {
		t.Fatal(err)
	}
	// Existing v3 plans may contain several members. A metadata-only update
	// leaves that set untouched; an explicit standard owner command replaces it
	// together with the plan revision and receipt in this PostgreSQL UoW.
	detail, err = service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: replacementID, Actor: actorID, IdempotencyKey: "groupops-pg-owner-legacy-member"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: "PG paused reactivation metadata", Actor: actorID, IdempotencyKey: "groupops-pg-owner-preserve"})
	if err != nil || len(detail.Members) != 2 {
		t.Fatalf("metadata-only members=%+v err=%v", detail.Members, err)
	}
	ownerRevision := detail.Plan.Revision
	ownerCommand := groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: ownerRevision, Name: "PG owner replaced", OwnerStaffID: replacementID, OwnerStaffIDSet: true, Actor: actorID, IdempotencyKey: "groupops-pg-owner-replace"}
	detail, err = service.Update(ctx, ownerCommand)
	if err != nil || len(detail.Members) != 1 || detail.Members[0].StaffID != replacementID {
		t.Fatalf("owner replacement members=%+v err=%v", detail.Members, err)
	}
	var persistedOwnerCount, persistedOwner int64
	if err = native.QueryRow(ctx, `SELECT count(*),coalesce(min(staff_id),0) FROM group_ops_plan_members WHERE plan_id=$1`, detail.Plan.ID).Scan(&persistedOwnerCount, &persistedOwner); err != nil || persistedOwnerCount != 1 || persistedOwner != replacementID {
		t.Fatalf("persisted owner count=%d owner=%d err=%v", persistedOwnerCount, persistedOwner, err)
	}
	replayedOwner, err := service.Update(ctx, ownerCommand)
	if err != nil || replayedOwner.Plan.Revision != detail.Plan.Revision || len(replayedOwner.Members) != 1 || replayedOwner.Members[0].StaffID != replacementID {
		t.Fatalf("owner replay=%+v err=%v", replayedOwner, err)
	}
	_, err = service.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: ownerRevision, Name: "stale replacement", OwnerStaffID: actorID, OwnerStaffIDSet: true, Actor: actorID, IdempotencyKey: "groupops-pg-owner-stale"})
	if !errors.Is(err, groupopsapp.ErrConflict) {
		t.Fatalf("stale owner replacement err=%v", err)
	}
	beforeInactive := detail.Plan.Revision
	_, err = service.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: beforeInactive, Name: "inactive replacement", OwnerStaffID: inactiveID, OwnerStaffIDSet: true, Actor: actorID, IdempotencyKey: "groupops-pg-owner-inactive"})
	if !errors.Is(err, groupopsapp.ErrInvalid) {
		t.Fatalf("inactive owner replacement err=%v", err)
	}
	readbackOwner, err := service.Detail(ctx, detail.Plan.ID)
	if err != nil || readbackOwner.Plan.Revision != beforeInactive || len(readbackOwner.Members) != 1 || readbackOwner.Members[0].StaffID != replacementID {
		t.Fatalf("failed replacement changed persisted owner=%+v err=%v", readbackOwner, err)
	}
	detail, err = service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: "reactivate-group", Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-group"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.AddNode(ctx, groupopsport.NodeCreateCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Position: 1, Kind: groupopsport.NodeMessage, MessageText: "reactivate", MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{}}, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-node"})
	if err != nil {
		t.Fatal(err)
	}
	// The pre-0101 node has no calendar fields. PostgreSQL must preserve its
	// explicit relative-delay provenance, while an equally named Day 1 20:00
	// V3 action is calendar-scheduled even with an empty optional title.
	detail, err = service.AddNode(ctx, groupopsport.NodeCreateCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Position: 2, Kind: groupopsport.NodeMessage, DayIndex: 1, ScheduledTime: "20:00", TriggerTimeLabel: "20:00", Status: "active", MessageText: "calendar-20", MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{}}, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-calendar-node"})
	if err != nil {
		t.Fatal(err)
	}
	var oldSemantics, newSemantics, newTime string
	if err = native.QueryRow(ctx, `SELECT schedule_semantics FROM group_ops_plan_nodes WHERE plan_id=$1 AND position=1`, detail.Plan.ID).Scan(&oldSemantics); err != nil || oldSemantics != "relative_delay" {
		t.Fatalf("legacy node semantics=%q err=%v", oldSemantics, err)
	}
	if err = native.QueryRow(ctx, `SELECT schedule_semantics,scheduled_time FROM group_ops_plan_nodes WHERE plan_id=$1 AND position=2`, detail.Plan.ID).Scan(&newSemantics, &newTime); err != nil || newSemantics != "calendar" || newTime != "20:00" {
		t.Fatalf("new Day 1 20:00 semantics=%q time=%q err=%v", newSemantics, newTime, err)
	}
	detail, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-first"})
	if err != nil || detail.Plan.Status != groupopsport.PlanActive {
		t.Fatalf("first activation=%+v err=%v", detail.Plan, err)
	}
	detail, err = service.Pause(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-pause"})
	if err != nil || detail.Plan.Status != groupopsport.PlanPaused {
		t.Fatalf("pause=%+v err=%v", detail.Plan, err)
	}
	_, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision - 1, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-stale"})
	if !errors.Is(err, groupopsapp.ErrConflict) {
		t.Fatalf("stale activation err=%v", err)
	}
	detail, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-resume"})
	if err != nil || detail.Plan.Status != groupopsport.PlanActive {
		t.Fatalf("resume=%+v err=%v", detail.Plan, err)
	}
	replayed, err := service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision - 1, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-resume"})
	if err != nil || replayed.Plan.ID != detail.Plan.ID || replayed.Plan.Status != detail.Plan.Status || replayed.Plan.Revision != detail.Plan.Revision {
		t.Fatalf("resume replay=%+v err=%v", replayed.Plan, err)
	}
	incomplete, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "PG incomplete", Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-incomplete"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: incomplete.Plan.ID, ExpectedRevision: incomplete.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-pg-reactivate-invalid"})
	if !errors.Is(err, groupopsapp.ErrStateConflict) {
		t.Fatalf("incomplete activation err=%v", err)
	}
	// Multiple plans deliberately start with the same explicit "not
	// configured" webhook state. Configured opaque references remain unique;
	// clearing one restores that shared unconfigured state without inventing a
	// placeholder key.
	other, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "PG other unconfigured", Actor: actorID, IdempotencyKey: "groupops-pg-webhook-other"})
	if err != nil || other.WebhookDescriptor.Configured || other.WebhookDescriptor.Reference != "" {
		t.Fatalf("second unconfigured plan=%+v err=%v", other.WebhookDescriptor, err)
	}
	incomplete, err = service.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: incomplete.Plan.ID, ExpectedRevision: incomplete.Plan.Revision, Reference: "shared-local-webhook", Actor: actorID, IdempotencyKey: "groupops-pg-webhook-set"})
	if err != nil || !incomplete.WebhookDescriptor.Configured {
		t.Fatalf("set configured webhook=%+v err=%v", incomplete.WebhookDescriptor, err)
	}
	_, err = service.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: other.Plan.ID, ExpectedRevision: other.Plan.Revision, Reference: "shared-local-webhook", Actor: actorID, IdempotencyKey: "groupops-pg-webhook-duplicate"})
	if err == nil {
		t.Fatal("duplicate configured webhook reference was accepted")
	}
	incomplete, err = service.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: incomplete.Plan.ID, ExpectedRevision: incomplete.Plan.Revision, Actor: actorID, IdempotencyKey: "groupops-pg-webhook-clear"})
	if err != nil || incomplete.WebhookDescriptor.Configured || incomplete.WebhookDescriptor.Reference != "" {
		t.Fatalf("clear configured webhook=%+v err=%v", incomplete.WebhookDescriptor, err)
	}
	other, err = service.PutWebhookDescriptor(ctx, groupopsport.WebhookDescriptorCommand{PlanID: other.Plan.ID, ExpectedRevision: other.Plan.Revision, Reference: "shared-local-webhook", Actor: actorID, IdempotencyKey: "groupops-pg-webhook-reuse"})
	if err != nil || !other.WebhookDescriptor.Configured {
		t.Fatalf("reuse cleared webhook=%+v err=%v", other.WebhookDescriptor, err)
	}
	third, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "PG third unconfigured", Actor: actorID, IdempotencyKey: "groupops-pg-webhook-third"})
	if err != nil || third.WebhookDescriptor.Configured || third.WebhookDescriptor.Reference != "" {
		t.Fatalf("third unconfigured plan=%+v err=%v", third.WebhookDescriptor, err)
	}
}

type groupOpsRuntimeWeCom struct {
	server        *httptest.Server
	mu            sync.Mutex
	calls         []groupOpsRuntimeWeComCall
	uploads       int
	uploadGate    chan struct{}
	uploadRelease sync.Once
}

type groupOpsRuntimeWeComCall struct {
	chat, text, messageID     string
	mediaIDs, miniPicMediaIDs []string
}

type groupOpsRuntimeEffectSecurity struct{}

func (groupOpsRuntimeEffectSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}
func (groupOpsRuntimeEffectSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}

func newGroupOpsRuntimeWeCom(t *testing.T) *groupOpsRuntimeWeCom {
	t.Helper()
	fixture := &groupOpsRuntimeWeCom{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if secret := request.URL.Query().Get("corpsecret"); secret != "runtime-contact-secret" && secret != "runtime-app-secret" {
				t.Errorf("unexpected runtime secret")
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"runtime-contact-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/add_msg_template":
			var body struct {
				ChatType string   `json:"chat_type"`
				Sender   string   `json:"sender"`
				ChatIDs  []string `json:"chat_id_list"`
				Text     struct {
					Content string `json:"content"`
				} `json:"text"`
				Attachments []struct {
					MessageType string `json:"msgtype"`
					Image       struct {
						MediaID string `json:"media_id"`
					} `json:"image"`
					MiniProgram struct {
						PicMediaID string `json:"pic_media_id"`
					} `json:"miniprogram"`
				} `json:"attachments"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.ChatType != "group" || body.Sender != "journey-sender" || len(body.ChatIDs) != 1 || (body.ChatIDs[0] != "chat-river-1" && body.ChatIDs[0] != "chat-river-2") {
				t.Errorf("invalid frozen group request body=%+v err=%v", body, err)
				http.Error(writer, "invalid request", http.StatusBadRequest)
				return
			}
			mediaIDs := make([]string, 0, len(body.Attachments))
			miniPicMediaIDs := make([]string, 0, len(body.Attachments))
			for _, attachment := range body.Attachments {
				if attachment.MessageType == "image" && attachment.Image.MediaID != "" {
					mediaIDs = append(mediaIDs, attachment.Image.MediaID)
				}
				if attachment.MessageType == "miniprogram" && attachment.MiniProgram.PicMediaID != "" {
					miniPicMediaIDs = append(miniPicMediaIDs, attachment.MiniProgram.PicMediaID)
				}
			}
			fixture.mu.Lock()
			messageID := "runtime-task-" + strconv.Itoa(len(fixture.calls)+1)
			fixture.calls = append(fixture.calls, groupOpsRuntimeWeComCall{chat: body.ChatIDs[0], text: body.Text.Content, messageID: messageID, mediaIDs: mediaIDs, miniPicMediaIDs: miniPicMediaIDs})
			fixture.mu.Unlock()
			if body.Text.Content == "provider result intentionally unknown" {
				connection, _, hijackErr := writer.(http.Hijacker).Hijack()
				if hijackErr != nil {
					t.Errorf("hijack unknown group response: %v", hijackErr)
				} else {
					_ = connection.Close()
				}
				return
			}
			_, _ = fmt.Fprintf(writer, `{"errcode":0,"msgid":%q}`, messageID)
		case "/cgi-bin/media/upload":
			if request.URL.Query().Get("type") != "image" {
				t.Errorf("unexpected material type %q", request.URL.Query().Get("type"))
				http.Error(writer, "invalid material", http.StatusBadRequest)
				return
			}
			if err := request.ParseMultipartForm(2 << 20); err != nil {
				t.Errorf("parse material upload: %v", err)
				http.Error(writer, "invalid material", http.StatusBadRequest)
				return
			}
			file, _, err := request.FormFile("media")
			if err != nil {
				t.Errorf("read material upload: %v", err)
				http.Error(writer, "invalid material", http.StatusBadRequest)
				return
			}
			content, readErr := io.ReadAll(file)
			_ = file.Close()
			if readErr != nil || len(content) == 0 {
				t.Errorf("empty material upload: %v", readErr)
				http.Error(writer, "invalid material", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			gate := fixture.uploadGate
			fixture.mu.Unlock()
			if gate != nil {
				select {
				case <-gate:
				case <-request.Context().Done():
					return
				}
			}
			fixture.mu.Lock()
			fixture.uploads++
			fixture.mu.Unlock()
			_, _ = fmt.Fprintf(writer, `{"errcode":0,"media_id":"runtime-prepared-media-1","created_at":%d}`, time.Now().UTC().Unix())
		case "/cgi-bin/externalcontact/get_groupmsg_send_result":
			var body struct {
				MessageID string `json:"msgid"`
				UserID    string `json:"userid"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.UserID != "journey-sender" {
				t.Errorf("invalid delivery read body=%+v err=%v", body, err)
				http.Error(writer, "invalid read", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			chat := ""
			for _, call := range fixture.calls {
				if call.messageID == body.MessageID {
					chat = call.chat
				}
			}
			fixture.mu.Unlock()
			if chat == "" {
				t.Errorf("unknown delivery message ID %q", body.MessageID)
				http.Error(writer, "unknown message", http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(writer, `{"errcode":0,"send_list":[{"userid":"journey-sender","chat_id":%q,"status":1}],"next_cursor":""}`, chat)
		default:
			t.Errorf("unexpected WeCom endpoint %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	return fixture
}

func (fixture *groupOpsRuntimeWeCom) URL() string          { return fixture.server.URL }
func (fixture *groupOpsRuntimeWeCom) Client() *http.Client { return fixture.server.Client() }
func (fixture *groupOpsRuntimeWeCom) Close()               { fixture.server.Close() }
func (fixture *groupOpsRuntimeWeCom) callCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return len(fixture.calls)
}
func (fixture *groupOpsRuntimeWeCom) uploadCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.uploads
}
func (fixture *groupOpsRuntimeWeCom) holdUploads() func() {
	fixture.mu.Lock()
	fixture.uploadGate = make(chan struct{})
	gate := fixture.uploadGate
	fixture.mu.Unlock()
	return func() {
		fixture.uploadRelease.Do(func() {
			close(gate)
			fixture.mu.Lock()
			if fixture.uploadGate == gate {
				fixture.uploadGate = nil
			}
			fixture.mu.Unlock()
		})
	}
}
func (fixture *groupOpsRuntimeWeCom) callsByChat() map[string][]string {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	items := make(map[string][]string)
	for _, call := range fixture.calls {
		items[call.chat] = append(items[call.chat], call.text)
	}
	return items
}
func (fixture *groupOpsRuntimeWeCom) mediaIDsByChat() map[string][]string {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	items := make(map[string][]string)
	for _, call := range fixture.calls {
		items[call.chat] = append(items[call.chat], call.mediaIDs...)
	}
	return items
}

func (fixture *groupOpsRuntimeWeCom) miniProgramPicMediaIDsByChat() map[string][]string {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	items := make(map[string][]string)
	for _, call := range fixture.calls {
		items[call.chat] = append(items[call.chat], call.miniPicMediaIDs...)
	}
	return items
}

func waitGroupOpsProviderCalls(t *testing.T, native *pgxpool.Pool, provider *groupOpsRuntimeWeCom, expected int) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if provider.callCount() == expected {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var effects, jobs string
	if native != nil {
		_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(id::text || ':' || state, ','),'') FROM external_effects`).Scan(&effects)
		_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(id::text || ':' || kind || ':' || state || ':' || attempt::text, ','),'') FROM river_job`).Scan(&jobs)
	}
	t.Fatalf("provider calls=%d, want %d; effects=%s river=%s", provider.callCount(), expected, effects, jobs)
}

func waitGroupOpsMaterialPreflightSnooze(t *testing.T, native *pgxpool.Pool, planID, imageID, groupJobID int64) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var groupQueued, materialEffects int
		err := native.QueryRow(context.Background(), `SELECT
			(SELECT count(*) FROM group_ops_executions execution JOIN external_effects effect ON effect.id=execution.external_effect_id JOIN external_effect_jobs effectJob ON effectJob.effect_id=effect.id AND effectJob.generation=effect.generation JOIN river_job job ON job.id=effectJob.river_job_id WHERE execution.plan_id=$1 AND effect.state='queued' AND effect.attempt_count=0 AND job.id=$3 AND job.state IN ('available','scheduled') AND COALESCE((job.metadata->>'snoozes')::integer,0)>0),
			(SELECT count(*) FROM outbound_material_preparations preparation JOIN external_effects effect ON effect.id=substring(preparation.effect_id FROM 5)::bigint WHERE preparation.source_ref=$2 AND preparation.state IN ('queued','retryable_failed','accepted','executed'))`, planID, "image:"+strconv.FormatInt(imageID, 10), groupJobID).Scan(&groupQueued, &materialEffects)
		if err == nil && groupQueued == 1 && materialEffects == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var effects, jobs, preparations string
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(id::text || ':' || kind || ':' || state || ':' || attempt_count::text, ','),'') FROM external_effects`).Scan(&effects)
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(id::text || ':' || kind || ':' || state || ':' || attempt::text, ','),'') FROM river_job`).Scan(&jobs)
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(source_ref || ':' || state || ':' || coalesce(effect_id,''), ','),'') FROM outbound_material_preparations`).Scan(&preparations)
	t.Fatalf("Group Ops material preflight did not snooze before an EER attempt; effects=%s jobs=%s preparations=%s", effects, jobs, preparations)
}

func assertGroupOpsInitialEffectsPersisted(t *testing.T, native *pgxpool.Pool, planID int64, expected int) {
	t.Helper()
	var persisted int
	// EER acceptance atomically creates a River job and advances the external
	// effect projection to queued. The Group Ops execution remains accepted
	// until the Provider result arrives.
	err := native.QueryRow(context.Background(), `SELECT count(*) FROM group_ops_executions execution JOIN external_effects effect ON execution.external_effect_id=effect.id JOIN external_effect_jobs effect_job ON effect_job.effect_id=effect.id AND effect_job.generation=effect.generation JOIN river_job job ON job.id=effect_job.river_job_id WHERE execution.plan_id=$1 AND execution.node_position=1 AND execution.state='accepted' AND effect.state='queued' AND job.kind='external_effect.execute.v1' AND job.state IN ('available','scheduled')`, planID).Scan(&persisted)
	if err != nil || persisted != expected {
		t.Fatalf("persisted initial Group Ops effects=%d want=%d err=%v", persisted, expected, err)
	}
}

func waitGroupOpsDelayedSuccessors(t *testing.T, native *pgxpool.Pool, planID int64, expected int) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var persisted int
		err := native.QueryRow(context.Background(), `SELECT count(*) FROM group_ops_execution_intents intent JOIN group_ops_executions execution ON execution.external_effect_id=intent.external_effect_id JOIN external_effects effect ON execution.external_effect_id=effect.id JOIN external_effect_jobs effect_job ON effect_job.effect_id=effect.id AND effect_job.generation=effect.generation JOIN river_job job ON job.id=effect_job.river_job_id WHERE intent.plan_id=$1 AND intent.node_position=3 AND intent.state='accepted' AND execution.state='accepted' AND effect.state='queued' AND job.kind='external_effect.execute.v1' AND job.state='scheduled' AND execution.scheduled_for > clock_timestamp()`, planID).Scan(&persisted)
		if err == nil && persisted == expected {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var intents, executions, effects, jobs string
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(node_position::text || ':' || state || ':' || coalesce(external_effect_id::text,'nil'), ','),'') FROM group_ops_execution_intents WHERE plan_id=$1`, planID).Scan(&intents)
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(node_position::text || ':' || state || ':' || external_effect_id::text, ','),'') FROM group_ops_executions WHERE plan_id=$1`, planID).Scan(&executions)
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(id::text || ':' || state, ','),'') FROM external_effects`).Scan(&effects)
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(id::text || ':' || kind || ':' || state, ','),'') FROM river_job`).Scan(&jobs)
	t.Fatalf("delayed Group Ops successors did not persist; intents=%s executions=%s effects=%s river=%s", intents, executions, effects, jobs)
}

func waitGroupOpsUnknownFirstNode(t *testing.T, native *pgxpool.Pool, planID int64) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var unknown, successors, effects int
		err := native.QueryRow(context.Background(), `SELECT
			(SELECT count(*) FROM group_ops_executions execution JOIN external_effects effect ON effect.id=execution.external_effect_id WHERE execution.plan_id=$1 AND execution.node_position=1 AND execution.state='outcome_unknown' AND effect.state='outcome_unknown'),
			(SELECT count(*) FROM group_ops_executions WHERE plan_id=$1 AND node_position=3),
			(SELECT count(*) FROM external_effects effect JOIN group_ops_executions execution ON execution.external_effect_id=effect.id WHERE execution.plan_id=$1)`, planID).Scan(&unknown, &successors, &effects)
		if err == nil && unknown == 2 && successors == 0 && effects == 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var states string
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(node_position::text || ':' || state || ':' || external_effect_id, ','),'') FROM group_ops_executions WHERE plan_id=$1`, planID).Scan(&states)
	t.Fatalf("unknown first node did not block frozen successors: %s", states)
}

func waitGroupOpsPreCallFailures(t *testing.T, native *pgxpool.Pool, planID int64, expected int) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var failed int
		err := native.QueryRow(context.Background(), `SELECT count(*) FROM group_ops_executions execution JOIN external_effects effect ON effect.id=execution.external_effect_id WHERE execution.plan_id=$1 AND execution.node_position=1 AND execution.state='final_failed' AND effect.state='final_failed'`, planID).Scan(&failed)
		if err == nil && failed == expected {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var executions, effects string
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(node_position::text || ':' || target_reference || ':' || state, ','),'') FROM group_ops_executions WHERE plan_id=$1`, planID).Scan(&executions)
	_ = native.QueryRow(context.Background(), `SELECT coalesce(string_agg(effect.id::text || ':' || effect.state, ','),'') FROM external_effects effect JOIN group_ops_executions execution ON execution.external_effect_id=effect.id WHERE execution.plan_id=$1`, planID).Scan(&effects)
	t.Fatalf("pre-call block did not finalize; executions=%s effects=%s", executions, effects)
}

func startGroupOpsRiver(t *testing.T, runtime *platformjobqueue.Runtime) (func(), func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	start := func() { go func() { errs <- runtime.Run(ctx) }() }
	stop := func() {
		cancel()
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("shared River stop: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("shared River did not stop")
		}
	}
	return start, stop
}

func createRiverJourneyPlan(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *groupopsstore.Repository, actorID int64) int64 {
	return createRiverJourneyPlanWithMessages(t, ctx, uow, repository, actorID, "first", "second")
}

func createRiverJourneyPlanWithImage(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *groupopsstore.Repository, actorID, imageID int64) int64 {
	t.Helper()
	now := time.Now().UTC()
	var planID int64
	err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		planID, err = repository.Create(tx, groupopsport.Plan{Name: "River material auto-resume", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		return repository.Save(tx, groupopsport.Detail{Plan: groupopsport.Plan{ID: planID, Name: "River material auto-resume", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now}, Members: []groupopsport.Member{{StaffID: actorID}}, GroupAssets: []groupopsport.GroupAsset{{AssetRef: "chat-river-1"}}, Nodes: []groupopsport.Node{{Position: 1, Kind: groupopsport.NodeMessage, MessageText: "prepared image", MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: imageID}}}}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return planID
}

func createRiverJourneyPlanWithMessages(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *groupopsstore.Repository, actorID int64, firstMessage, secondMessage string) int64 {
	t.Helper()
	now := time.Now().UTC()
	var planID int64
	err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		planID, err = repository.Create(tx, groupopsport.Plan{Name: "River runtime journey", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		emptyMaterials := groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{}}
		return repository.Save(tx, groupopsport.Detail{Plan: groupopsport.Plan{ID: planID, Name: "River runtime journey", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now}, Members: []groupopsport.Member{{StaffID: actorID}}, GroupAssets: []groupopsport.GroupAsset{{AssetRef: "chat-river-1"}, {AssetRef: "chat-river-2"}}, Nodes: []groupopsport.Node{{Position: 1, Kind: groupopsport.NodeMessage, MessageText: firstMessage, MaterialPlan: emptyMaterials}, {Position: 2, Kind: groupopsport.NodeDelay, DelayMinutes: 1, MaterialPlan: emptyMaterials}, {Position: 3, Kind: groupopsport.NodeMessage, MessageText: secondMessage, MaterialPlan: emptyMaterials}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return planID
}

type journeyStaff struct{}

func (journeyStaff) IsActiveStaff(_ context.Context, staffID int64) (bool, error) {
	return staffID == 1, nil
}

func (journeyStaff) ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error) {
	return []groupopsport.OperationMember{{StaffID: 1, SenderUserID: "journey-sender", DisplayName: "Journey"}}, nil
}

type journeySender struct{}

func (journeySender) ResolveExecutionSender(_ context.Context, target string) (string, bool, error) {
	if target == "" {
		return "", false, nil
	}
	return "journey-sender", true, nil
}

// runtimeJourneySender models the single currently authorized sender lookup
// used by the production-shaped dispatch reader. It lets the integration
// journey revoke permission after EER acceptance and before River executes.
type runtimeJourneySender struct {
	mu       sync.RWMutex
	eligible bool
}

func (sender *runtimeJourneySender) ResolveExecutionSender(_ context.Context, target string) (string, bool, error) {
	if sender == nil || target == "" {
		return "", false, nil
	}
	sender.mu.RLock()
	eligible := sender.eligible
	sender.mu.RUnlock()
	if !eligible {
		return "", false, nil
	}
	return "journey-sender", true, nil
}

func (sender *runtimeJourneySender) SetEligible(eligible bool) {
	if sender != nil {
		sender.mu.Lock()
		sender.eligible = eligible
		sender.mu.Unlock()
	}
}

type journeyEvidence struct{ status, calls int }

func (*journeyEvidence) VerifyReconciliationEvidence(_ context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.ReconciliationEvidenceResult, error) {
	if input.ExecutionID < 1 || input.ExternalEffectID == "" || !effectport.ValidDigest(effectport.Digest(input.EvidenceDigest)) {
		return groupopsport.ReconciliationEvidenceResult{}, errors.New("invalid journey evidence")
	}
	return groupopsport.ReconciliationEvidenceResult{EvidenceDigest: input.EvidenceDigest}, nil
}

func (evidence *journeyEvidence) ReadProviderDelivery(_ context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.GroupMessageReceipt, bool, error) {
	if input.ExecutionID < 1 || input.ExternalEffectID == "" {
		return groupopsport.GroupMessageReceipt{}, false, errors.New("invalid journey delivery")
	}
	evidence.calls++
	status := evidence.status
	return groupopsport.GroupMessageReceipt{ExecutionID: input.ExecutionID, ExternalEffectID: input.ExternalEffectID, MessageID: "journey-msgid", SenderUserID: "journey-sender", ChatID: "chat-journey-1", DeliveryStatus: &status, DeliveryEvidenceDigest: string(effectport.Hash("journey-delivery", input.ExternalEffectID))}, true, nil
}

type journeyReconciler struct {
	repository *externaleffects.Repository
}

func (reconciler journeyReconciler) ReconcileExternalEffect(ctx context.Context, command groupopsport.ExternalReconcileCommand) error {
	return reconciler.repository.ReconcileWithin(ctx, externaleffects.ControlCommand{
		EffectID:         command.EffectID,
		ReceiptKey:       externaleffects.Hash("journey-reconcile", command.EffectID, command.ReceiptKey),
		EvidenceDigest:   externaleffects.Digest(command.EvidenceDigest),
		ActorAdminUserID: command.ActorID,
		Generation:       command.Generation,
		Fence:            command.Fence,
		LeaseExpiresAt:   command.LeaseExpiresAt,
	})
}

type journeyProviderAdapter struct {
	result effectport.AdapterResult
}

func (adapter *journeyProviderAdapter) Execute(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
	return adapter.result, nil
}

func newJourneyCompletionSink(projector groupopsport.RuntimeStore) (*journeyCompletionSink, error) {
	if projector == nil {
		return nil, errors.New("journey projector unavailable")
	}
	return &journeyCompletionSink{projector: projector}, nil
}

// journeyCompletionSink is intentionally test-local and mirrors the stable
// outbound owner projection: EER state is translated into local GroupOps
// execution state in the same transaction.
type journeyCompletionSink struct {
	projector groupopsport.RuntimeStore
}

func (sink *journeyCompletionSink) CompleteEffect(ctx context.Context, effectRef string, _ effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	state := groupopsport.ExecutionOutcomeUnknown
	switch result.Completion {
	case effectport.StateExecuted:
		state = groupopsport.ExecutionProviderAccepted
	case effectport.StateUnknown:
		state = groupopsport.ExecutionOutcomeUnknown
	default:
		return errors.New("unsupported journey completion")
	}
	return sink.projector.CompleteEffect(ctx, effectRef, state, result.Completion == effectport.StateExecuted, false, string(result.ReceiptDigest), attempt.Number, time.Now().UTC())
}

func createJourneyPlan(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *groupopsstore.Repository, actorID, inviteID int64) int64 {
	t.Helper()
	now := time.Now().UTC()
	var planID int64
	err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		planID, err = repository.Create(tx, groupopsport.Plan{Name: "PG Journey", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		return repository.Save(tx, groupopsport.Detail{
			Plan:    groupopsport.Plan{ID: planID, Name: "PG Journey", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now},
			Members: []groupopsport.Member{{StaffID: actorID}},
			GroupAssets: []groupopsport.GroupAsset{
				{AssetRef: "chat-journey-1"},
				{AssetRef: "chat-journey-2"},
			},
			Nodes: []groupopsport.Node{
				{Position: 1, Kind: groupopsport.NodeMessage, MessageText: "PG journey", MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "group_invite", ID: inviteID}}}},
				{Position: 2, Kind: groupopsport.NodeDelay, DelayMinutes: 1, MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{}}},
				{Position: 3, Kind: groupopsport.NodeMessage, MessageText: "PG successor", MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "group_invite", ID: inviteID}}}},
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return planID
}

func groupOpsIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Group Ops PostgreSQL Journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	rawSchema := make([]byte, 8)
	if _, err = rand.Read(rawSchema); err != nil {
		t.Fatal(err)
	}
	schema := "group_ops_journey_" + hex.EncodeToString(rawSchema)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Group Ops Journey test")
	}
	for _, migration := range []string{"0003_access.sql", "0005_external_effects.sql", "0007_media.sql", "0012_group_ops.sql", "0016_media_content_packages.sql", "0036_ai_assistant_review.sql", "0078_group_ops_provider_tasks.sql", "0081_group_ops_webhook_unconfigured_reference.sql", "0101_group_ops_ui_metadata.sql", "0116_group_ops_operation_member_directory.sql", "0119_group_ops_unnamed_groups.sql", "0120_excel_batches.sql", "0124_operation_excel_batch_lifecycle.sql", "0125_outbound_material_preparation.sql", "0126_media_material_source_snapshots.sql", "0155_group_ops_webhook_dynamic_executions.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", migration))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("%s: %v", migration, execErr)
		}
	}
	if err = ensureAccessLoginFixtureSchema(ctx, native); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
