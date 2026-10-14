package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	jobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// Source ports seed an isolated accepted batch. The authenticated execution
// read reaches the exact installed binary; no production credentials or
// Provider writes are used by this installed projection contract.
func TestGroupOpsBatchInstalled(t *testing.T) {
	if testconfig.InstalledSurvey().Binary == "" || os.Getenv("AICRM_DATABASE_URL") == "" {
		t.Fatal("exact installed package and isolated PostgreSQL required")
	}
	f := newPublicSurveyPresentationChromiumFixture(t)
	owner := groupOpsBindingFixtureStaff(t, f.ctx, f.native, "batch-owner", "批次运营者")
	pool, err := pg.Wrap(f.native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := pg.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := groupopsstore.NewPostgreSQL(f.native, uow)
	if err != nil {
		t.Fatal(err)
	}
	staff := groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: repo}
	service := groupopsapp.NewService(uow, repo, staff, repo)
	detail, err := service.Create(f.ctx, p.CreatePlanCommand{Name: "单任务多群安装合同", Actor: owner, IdempotencyKey: "batch-installed-create"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.Update(f.ctx, p.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: detail.Plan.Name, PlanType: p.PlanTypeWebhook, Actor: owner, IdempotencyKey: "batch-installed-webhook"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.AddMember(f.ctx, p.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: owner, Actor: owner, IdempotencyKey: "batch-installed-member"})
	if err != nil {
		t.Fatal(err)
	}
	targets := []string{}
	for i := 0; i < 11; i++ {
		target := fmt.Sprintf("batch-installed-%02d", i)
		targets = append(targets, target)
		_, err = f.native.Exec(f.ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,owner_user_id,display_name,member_count,source_digest,refreshed_at) VALUES($1,$2,'batch-owner','批次群',2,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',now())`, target, owner)
		if err != nil {
			t.Fatal(err)
		}
		detail, err = service.AddGroupAsset(f.ctx, p.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: target, Actor: owner, IdempotencyKey: "batch-installed-bind-" + strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	detail, err = service.PutWebhookDescriptor(f.ctx, p.WebhookDescriptorCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Reference: "batch-installed-hook", Actor: owner, IdempotencyKey: "batch-installed-descriptor"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.Activate(f.ctx, p.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: owner, IdempotencyKey: "batch-installed-active"})
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	inserter, err := jobqueue.NewInsertClient(f.native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(f.native, inserter)
	if err != nil {
		t.Fatal(err)
	}
	runtime := groupopsapp.NewRuntimeService(uow, repo, repo, effects, staff, nil, staff, nil, nil, nil)
	runtime.SetDispatchEnabled(true)
	inbound := p.WebhookInboundCommand{WebhookReference: "batch-installed-hook", TargetChatReferences: targets, Messages: []p.WebhookMessage{{Type: "text", Text: "单任务覆盖全部绑定群"}}}
	accepted, err := runtime.AcceptWebhook(f.ctx, "batch-installed-hook", "batch-installed-event-0001", inbound)
	if err != nil || accepted.Accepted != 1 || len(accepted.Executions) != 1 || len(accepted.Executions[0].TargetReferences) != 11 {
		t.Fatal(accepted, err)
	}
	replay, err := runtime.AcceptWebhook(f.ctx, "batch-installed-hook", "batch-installed-event-0001", inbound)
	if err != nil || replay.Run.ID != accepted.Run.ID || len(replay.Executions) != 1 {
		t.Fatal(replay, err)
	}
	var effectsCount int
	if err = f.native.QueryRow(f.ctx, `SELECT count(*) FROM group_ops_executions WHERE run_id=$1`, accepted.Run.ID).Scan(&effectsCount); err != nil || effectsCount != 1 {
		t.Fatal(effectsCount, err)
	}
	request, err := http.NewRequestWithContext(f.ctx, "GET", f.server.URL+"/api/admin/automation-conversion/group-ops/plans/"+strconv.FormatInt(detail.Plan.ID, 10)+"/executions?limit=100", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range f.adminCookies {
		request.AddCookie(cookie)
	}
	request.Header.Set("Accept", "application/json")
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	var page p.ExecutionPage
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &page) != nil || page.Total != 1 || len(page.Items) != 1 || len(page.Items[0].TargetReferences) != 11 {
		t.Fatalf("installed batch read status=%d body=%s err=%v", response.StatusCode, raw, err)
	}
	// Current mutable bindings are validated for every group before dispatch.
	if _, err = f.native.Exec(f.ctx, `DELETE FROM group_ops_plan_group_assets WHERE plan_id=$1 AND asset_reference=$2`, detail.Plan.ID, targets[10]); err != nil {
		t.Fatal(err)
	}
	err = uow.Within(context.Background(), func(tx context.Context) error {
		_, e := repo.LoadDispatchExecution(tx, accepted.Executions[0].ExternalEffectID)
		return e
	})
	if err == nil {
		t.Fatal("removed non-anchor binding remained dispatchable")
	}
	t.Log("single execution / single effect / eleven frozen groups / replay preserved / all bindings checked")
}
