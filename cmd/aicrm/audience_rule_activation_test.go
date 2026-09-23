package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type ruleOnlyUnusedAgents struct {
	automationport.PublishedAgentReader
}
type ruleOnlyUnusedStaff struct {
	accessport.AutomationOpsStaffReader
}
type ruleOnlyJobs struct{ calls int }

func (j *ruleOnlyJobs) EnqueueRefreshWithin(context.Context, int64) (int64, error) {
	j.calls++
	return 9001, nil
}
func (j *ruleOnlyJobs) EnqueueMemberEventsWithin(context.Context, segmentport.SnapshotID) (int64, error) {
	panic("activation must not publish members")
}

func TestRuleOnlyActivationHTTPDoesNotEnableSending(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer native.Close()
	pool, e := pg.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := pg.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := segmentstore.NewPostgreSQL(native, uow)
	if e != nil {
		t.Fatal(e)
	}
	config := segmentapp.NewService(uow, repo)
	evaluator, e := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, &automationAudienceSource{}, automationAudienceCanonical{})
	if e != nil {
		t.Fatal(e)
	}
	jobs := &ruleOnlyJobs{}
	snapshots, e := segmentapp.NewSnapshotService(uow, repo, evaluator, jobs, jobs)
	if e != nil {
		t.Fatal(e)
	}
	execution, e := segmentapp.NewExecutionService(uow, repo, ruleOnlyUnusedAgents{}, ruleOnlyUnusedStaff{}, false)
	if e != nil {
		t.Fatal(e)
	}
	runtime := segmentapp.NewRuntimeFacade(config, snapshots, execution)
	handler, e := segmenthttp.NewRuntimeHandler(runtime, runtime, commerceFundsSecurity{})
	if e != nil {
		t.Fatal(e)
	}
	pkg, e := config.CreatePackage(ctx, segmentapp.PackageCreateCommand{Name: "Rule only", TemplateKey: "paid_order", Actor: 1, IdempotencyKey: "rule-only-create-0001"})
	if e != nil {
		t.Fatal(e)
	}
	definition := json.RawMessage(`{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["fixture"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true}}`)
	_, e = config.PutConfiguration(ctx, segmentapp.ConfigurationCommand{PackageID: pkg.ID, ExpectedPackageVersion: pkg.Version, Actor: 1, IdempotencyKey: "rule-only-configure-0001", Definition: definition, RefreshMode: "every_3m"})
	if e != nil {
		t.Fatal(e)
	}
	pkg, e = config.GetPackage(ctx, pkg.ID)
	if e != nil {
		t.Fatal(e)
	}
	call := func(action, body, key string) int {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/ai-audience/packages/%d/%s", pkg.ID, action), strings.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	body := fmt.Sprintf(`{"expected_version":%d}`, pkg.Version)
	if status := call("activate", body, "rule-only-activate-0001"); status != 200 {
		t.Fatalf("activate HTTP %d", status)
	}
	if status := call("activate", body, "rule-only-activate-0001"); status != 200 {
		t.Fatalf("activate replay HTTP %d", status)
	}
	check, e := execution.Precheck(ctx, pkg.ID)
	if e != nil || check.Ready {
		t.Fatalf("sending must remain not ready: %v", e)
	}
	if _, e = execution.AudienceExecutionConfiguration(ctx, segmentport.PackageID(pkg.ID)); e == nil {
		t.Fatal("missing binding must not expose send configuration")
	}
	if status := call("refresh", `{"reference_time":"2026-09-11T05:00:00Z"}`, "rule-only-refresh-0001"); status != 202 {
		t.Fatalf("refresh HTTP %d", status)
	}
	if jobs.calls != 1 {
		t.Fatalf("refresh enqueue calls %d", jobs.calls)
	}
	var bindings, senders, snapshotsCount int
	for query, dst := range map[string]*int{"SELECT count(*) FROM segment_audience_automation_binding_versions": &bindings, "SELECT count(*) FROM segment_audience_sender_sets": &senders, "SELECT count(*) FROM segment_audience_snapshots": &snapshotsCount} {
		if e = native.QueryRow(ctx, query).Scan(dst); e != nil {
			t.Fatal(e)
		}
	}
	if bindings != 0 || senders != 0 || snapshotsCount != 0 {
		t.Fatal("activation created unexpected execution data or old members")
	}
}
