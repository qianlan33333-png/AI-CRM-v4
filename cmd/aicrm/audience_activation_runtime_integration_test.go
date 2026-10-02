package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type activationRuntimeFacts struct {
	segmenthttp.AudienceSurveyReferenceResolver
	orders      []orderport.PaidAudienceOrder
	submissions []surveyport.AudienceSubmission
}

func (f *activationRuntimeFacts) PaidAudienceOrders(context.Context, time.Time) ([]orderport.PaidAudienceOrder, error) {
	return f.orders, nil
}
func (f *activationRuntimeFacts) AudienceSubmissions(context.Context, time.Time) ([]surveyport.AudienceSubmission, error) {
	return f.submissions, nil
}
func (*activationRuntimeFacts) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}
func (*activationRuntimeFacts) ResolveAudienceProduct(_ context.Context, v string) (string, bool, error) {
	return v, v == "334465678", nil
}
func (*activationRuntimeFacts) ResolveAudienceQuestionnaire(_ context.Context, v string) (string, bool, error) {
	return v, v == "16", nil
}

type activationRuntimeJobs struct{}

func (activationRuntimeJobs) EnqueueRefreshWithin(context.Context, int64) (int64, error) {
	return 991, nil
}
func (activationRuntimeJobs) EnqueueMemberEventsWithin(context.Context, segmentport.SnapshotID) (int64, error) {
	return 992, nil
}

func TestActivationHourlyConfigurationHTTPSaveReadbackAndStickyPublication(t *testing.T) {
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
	at := time.Now().UTC()
	anchor := at.Add(-12 * time.Hour)
	paid := anchor.Add(-48 * time.Hour)
	facts := &activationRuntimeFacts{orders: []orderport.PaidAudienceOrder{{OrderID: 1, CustomerID: 1, ProductCode: "334465678", PaidAt: &paid}, {OrderID: 2, CustomerID: 2, ProductCode: "334465678", PaidAt: &paid}}, submissions: []surveyport.AudienceSubmission{{CustomerID: 2, QuestionnaireID: 16, SubmittedAt: anchor}}}
	source := segmentadapter.CustomerSource{UoW: uow, Legacy: segmentadapter.LegacyTemplateSource{Orders: facts, Submissions: facts, CanonicalCustomers: facts}}
	evaluator, e := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, source, automationAudienceCanonical{})
	if e != nil {
		t.Fatal(e)
	}
	snapshots, e := segmentapp.NewSnapshotService(uow, repo, evaluator, activationRuntimeJobs{}, activationRuntimeJobs{})
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
	handler.BindAudienceProductReferences(facts)
	handler.BindAudienceSurveyReferences(facts)
	pkg, e := config.CreatePackage(ctx, segmentapp.PackageCreateCommand{Name: "Activation hourly", TemplateKey: "paid_order", Actor: 1, IdempotencyKey: "activation-create-0001"})
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, action, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, fmt.Sprintf("/api/admin/ai-audience/packages/%d/%s", pkg.ID, action), strings.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	body := fmt.Sprintf(`{"expected_package_version":%d,"refresh_mode":"every_1h","definition":{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["334465678"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":false,"activation":{"elapsed_hours_min":12,"elapsed_hours_max":36,"questionnaire_status":"unsubmitted","questionnaire_ids":["16"],"enrollment_override":{"at":"%s","order_ids":[1,2]}}}}}`, pkg.Version, anchor.Format(time.RFC3339Nano))
	saved := call(http.MethodPut, "configuration", body, "activation-configure-0001")
	if saved.Code != 200 {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body.String())
	}
	read := call(http.MethodGet, "configuration", "", "")
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"every_1h"`) || !strings.Contains(read.Body.String(), `"enrollment_override"`) {
		t.Fatalf("readback status=%d body=%s", read.Code, read.Body.String())
	}
	pkg, e = config.GetPackage(ctx, pkg.ID)
	if e != nil {
		t.Fatal(e)
	}
	active := call(http.MethodPost, "activate", fmt.Sprintf(`{"expected_version":%d}`, pkg.Version), "activation-activate-0001")
	if active.Code != 200 {
		t.Fatalf("activate status=%d body=%s", active.Code, active.Body.String())
	}
	for i := 0; i < 2; i++ {
		reference := at.Add(time.Duration(i) * time.Hour)
		if i == 1 {
			facts.submissions = append(facts.submissions, surveyport.AudienceSubmission{CustomerID: 1, QuestionnaireID: 16, SubmittedAt: reference})
		}
		run, e := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: pkg.ID, Actor: 1, IdempotencyKey: fmt.Sprintf("activation-refresh-%04d", i), ReferenceTime: reference})
		if e != nil {
			t.Fatal(e)
		}
		if e = snapshots.ProcessRefresh(ctx, run.ID); e != nil {
			t.Fatal(e)
		}
		var members, events int
		e = native.QueryRow(ctx, `SELECT s.member_count,(SELECT count(*) FROM segment_audience_member_events WHERE package_id=$1) FROM segment_audience_packages p JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id WHERE p.id=$1`, pkg.ID).Scan(&members, &events)
		if e != nil || members != 1 || events != 1 {
			t.Fatalf("round=%d members=%d events=%d err=%v", i, members, events, e)
		}
	}
	var bindings int
	if e = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_automation_binding_versions WHERE package_id=$1`, pkg.ID).Scan(&bindings); e != nil || bindings != 0 {
		t.Fatalf("configuration activated unexpected sending: %d %v", bindings, e)
	}
	var parsed map[string]any
	if e = json.Unmarshal(read.Body.Bytes(), &parsed); e != nil {
		t.Fatal(e)
	}
}
