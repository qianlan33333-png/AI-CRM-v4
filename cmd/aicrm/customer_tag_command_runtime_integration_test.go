package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/http"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	tagdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	taghttp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/http"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// TestCustomerTagCommandCompositionHTTPPostgreSQL mounts the exact cmd/aicrm
// compatibility route and drives its actual Customer app, store, EER receipt,
// River insert, and durable history read through HTTP. Provider is disabled:
// local acceptance is intentionally not presented as a completed WeCom write.
func TestCustomerTagCommandCompositionHTTPPostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := customerTagRuntimePool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('runtime-tag-admin','$argon2id$test','Runtime tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active'); INSERT INTO tag_groups(group_name,sort_order) VALUES('运行时分组',0); INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES(1,'运行时标签',0); INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at) VALUES('runtime-observation','manual','succeeded','wecom-corp:runtime',clock_timestamp()); INSERT INTO wecom_customer_tag_observations(customer_id,corp_scope,employee_id,provider_tag_id,provider_tag_type,observed_name,last_seen_run_id,observed_at) VALUES(1,'wecom-corp:runtime','runtime-staff','observed-runtime-tag',2,'已观察标签',1,clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, runtimeTagGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	security := runtimeTagSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	catalogStore, err := tagstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	profileObservations := wecom.NewPostgreSQLCustomerSyncStore()
	handler, err := customerhttp.NewHandler(customerhttp.Config{
		UnitOfWork: uow, Auth: security, CSRF: security,
		Directory: customerapp.Directory{Store: runtimeTagDirectory{}, SigningKey: []byte("0123456789abcdef0123456789abcdef")}, Store: runtimeTagDirectory{},
		Identities: runtimeTagIdentities{}, Audit: runtimeTagAudit{}, Canonical: runtimeTagCanonical{}, Owners: runtimeTagOwners{}, Tags: customerTagAdapter{uow: uow, observations: profileObservations, names: catalogStore}, Surveys: runtimeTagSurveys{}, Timeline: runtimeTagTimeline{}, Chat: runtimeTagChat{},
		TagCommands: commands, TagHistory: customerstore.TagCommandPostgreSQL{}, ProfileSigningKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	catalogHandler, err := taghttp.NewHandler(tagapp.NewService(uow, catalogStore, nil, nil, nil), &tagapp.SyncService{}, runtimeTagCatalogGate{}, security)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	// This is the production composition ordering: the explicit command route
	// and mountSurveyAPIs compatibility subtree share the exact TagCommand host.
	mux.Handle("/api/v1/customer-tag-commands", handler.TagCommandRoutes())
	mux.Handle("/api/v1/customer-tag-commands/", handler.TagCommandRoutes())
	mux.Handle("/api/admin/wecom/tags", catalogHandler)
	mux.Handle("/api/admin/customers/", handler.Routes())
	mountSurveyAPIs(mux, http.NotFoundHandler(), handler.TagCommandRoutes())
	server := httptest.NewServer(mux)
	defer server.Close()
	// The Host depends on this real catalog projection, including its provider
	// binding schema. Fail at the HTTP boundary instead of timing out in the UI.
	catalogResponse, err := server.Client().Get(server.URL + "/api/admin/wecom/tags")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Items []struct {
			Name string `json:"tag_name"`
		} `json:"items"`
	}
	decodeErr := json.NewDecoder(catalogResponse.Body).Decode(&catalog)
	catalogResponse.Body.Close()
	if catalogResponse.StatusCode != http.StatusOK || decodeErr != nil || len(catalog.Items) != 1 || catalog.Items[0].Name != "运行时标签" {
		t.Fatalf("catalog status=%d decode=%v items=%+v", catalogResponse.StatusCode, decodeErr, catalog.Items)
	}

	body := []byte(`{"customer_ids":[2],"add_tag_ids":[1],"idempotency_key":"runtime-tag-command-key"}`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/customer-tag-commands/preview", bytesReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "runtime-csrf")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d", response.StatusCode)
	}
	var preview customerport.TagCommandResult
	if err = json.NewDecoder(response.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(preview.Lines) != 1 || preview.Lines[0].State != "eligible" || preview.ID != 0 {
		t.Fatalf("preview=%+v", preview)
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/customer-tag-commands", bytesReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "runtime-csrf")
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("accept status=%d", response.StatusCode)
	}
	var accepted customerport.TagCommandResult
	if err = json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if accepted.ID < 1 || accepted.State != "queued" || len(accepted.Lines) != 1 || accepted.Lines[0].EffectRef == "" {
		t.Fatalf("accepted=%+v", accepted)
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/customers/2/tag-commands", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history status=%d", response.StatusCode)
	}
	var history struct {
		Items []customerport.TagCommandResult `json:"items"`
	}
	if err = json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(history.Items) != 1 || history.Items[0].ID != accepted.ID || len(history.Items[0].Lines) != 1 || history.Items[0].Lines[0].EffectRef != accepted.Lines[0].EffectRef {
		t.Fatalf("history=%+v accepted=%+v", history, accepted)
	}
	// The frozen WebShell template and Host script make the real preview and
	// confirmation requests and catalog-name selection against this live mux.
	// The browser harness supplies only the unrelated customer-directory list
	// response plus controlled standard-component readiness and picker Ports.
	browser := exec.Command("node", "customer_tag_command_runtime_e2e.mjs", server.URL)
	browser.Dir = "."
	browser.Env = os.Environ()
	if output, browserErr := browser.CombinedOutput(); browserErr != nil {
		t.Fatalf("frozen Host journey: %v output=%s", browserErr, output)
	}
	for table, want := range map[string]int{"customer_tag_commands": 2, "customer_tag_command_lines": 2, "external_effects": 2, "external_effect_operation_receipts": 4, "river_job": 2, "audit_events": 2, "outbox_events": 2} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
}

func bytesReader(v []byte) *bytes.Reader { return bytes.NewReader(v) }

type runtimeTagSecurity struct{ principal accessdomain.Principal }

func (s runtimeTagSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (s runtimeTagSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}

type runtimeTagDirectory struct{}

func (runtimeTagDirectory) List(context.Context, customerapp.Query) (customerapp.PageData, error) {
	return customerapp.PageData{}, nil
}
func (runtimeTagDirectory) CustomerIDsForOwner(context.Context, int64, int) ([]customerdomain.CustomerID, error) {
	return []customerdomain.CustomerID{}, nil
}
func (runtimeTagDirectory) Detail(context.Context, customerdomain.CustomerID) (customerapp.Detail, error) {
	return customerapp.Detail{}, nil
}

type runtimeTagIdentities struct{}

func (runtimeTagIdentities) VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}
func (runtimeTagIdentities) CustomerForPhone(context.Context, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}
func (runtimeTagIdentities) DirectoryIdentities(context.Context, customerdomain.CustomerID) ([]identityport.DirectoryIdentitySummary, []identityport.MaskedPhone, error) {
	return nil, nil, nil
}
func (runtimeTagIdentities) RevealPhone(context.Context, customerdomain.CustomerID) (string, bool, error) {
	return "", false, nil
}

type runtimeTagAudit struct{}

func (runtimeTagAudit) Append(_ context.Context, v platformaudit.Event) (platformaudit.Event, error) {
	return v, nil
}

type runtimeTagCanonical struct{}

func (runtimeTagCanonical) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}

type runtimeTagOwners struct{}

func (runtimeTagOwners) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagOwners) CustomerOwners(context.Context, customerdomain.CustomerID) (customerport.OwnerPage, error) {
	return customerport.OwnerPage{}, nil
}

type runtimeTagTags struct{}

func (runtimeTagTags) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagTags) CustomerTags(context.Context, customerdomain.CustomerID) (customerport.TagPage, error) {
	return customerport.TagPage{}, nil
}

type runtimeTagSurveys struct{}

func (runtimeTagSurveys) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagSurveys) CustomerSurveys(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.SurveyPage, error) {
	return customerport.SurveyPage{}, nil
}

type runtimeTagTimeline struct{}

func (runtimeTagTimeline) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagTimeline) CustomerTimeline(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error) {
	return customerport.TimelinePage{}, nil
}

type runtimeTagChat struct{}

func (runtimeTagChat) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionNotReady}
}
func (runtimeTagChat) CustomerChatActivity(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.ChatActivityPage, error) {
	return customerport.ChatActivityPage{}, customerport.ErrCapabilityNotReady
}

type runtimeTagCatalogGate struct{}

func (runtimeTagCatalogGate) Get(context.Context) (tagdomain.ExecutionGate, error) {
	return tagdomain.ExecutionGate{LocalCommandAcceptanceAvailable: true, LocalQueueAvailable: true, ObservedAt: time.Now().UTC()}, nil
}

type runtimeTagGate struct{}

func (runtimeTagGate) FreezeTagCommandTarget(_ context.Context, target customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	target.StaffID = 1
	return customerport.FrozenTagCommandTarget{TagCommandTarget: target, BindingDigest: string(effectport.Hash("runtime-tag-binding")), TargetDigest: string(effectport.Hash("customer.tag.command.target.v1", "runtime-staff", "runtime-external"))}, nil
}

type customerTagCompositionGate struct{}

func (customerTagCompositionGate) FreezeTagCommandTarget(_ context.Context, target customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	target.StaffID = 1
	return customerport.FrozenTagCommandTarget{TagCommandTarget: target, BindingDigest: string(effectport.Hash("tag-command-integration-binding")), TargetDigest: string(effectport.Hash("tag-command-integration-target"))}, nil
}

type tagCommandFailingOutbox struct{}

func (tagCommandFailingOutbox) Append(context.Context, platformoutbox.Event) (platformoutbox.Event, error) {
	return platformoutbox.Event{}, errors.New("outbox rejected")
}

func TestCustomerTagCommandCompositionAcceptanceAtomicConcurrentReplay(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := customerTagRuntimePool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-atomic','$argon2id$test','Tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active')`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, customerTagCompositionGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	command := customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "atomic-replay-key", IdempotencyKey: "atomic-replay-key", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, AddTagIDs: []int64{1}}}}
	results := make(chan customerport.TagCommandResult, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() {
			result, submitErr := service.SubmitTagCommand(ctx, command)
			if submitErr != nil {
				failures <- submitErr
				return
			}
			results <- result
		}()
	}
	var first customerport.TagCommandResult
	for range 2 {
		select {
		case submitErr := <-failures:
			t.Fatalf("concurrent command=%v", submitErr)
		case result := <-results:
			if first.ID == 0 {
				first = result
			} else if result.ID != first.ID || len(result.Lines) != 1 {
				t.Fatalf("replay result=%+v first=%+v", result, first)
			}
		}
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 1, "external_effects": 1, "external_effect_operation_receipts": 2, "river_job": 1, "audit_events": 1, "outbox_events": 1} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	// A post-acceptance append failure rolls every business and EER fact back;
	// no compensating provider path is used.
	failing, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, customerTagCompositionGate{}, platformaudit.NewPostgreSQLStore(), tagCommandFailingOutbox{})
	if err != nil {
		t.Fatal(err)
	}
	failed := command
	failed.SourceRef, failed.IdempotencyKey = "atomic-rollback-key", "atomic-rollback-key"
	if _, err = failing.SubmitTagCommand(ctx, failed); err == nil {
		t.Fatal("outbox failure must abort acceptance")
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 1, "external_effects": 1, "river_job": 1} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("rollback %s=%d want=%d err=%v", table, got, want, err)
		}
	}
}

func TestCustomerTagCommandCompositionBatchOver100QueuesIndependentRiverEffects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	// This is intentionally a Composition journey instead of an insert-only
	// receipt check. It exercises the real session/CSRF command acceptance,
	// River runtime, outbound provider adapter and completion sink across an
	// actual stop/rebuild on the same PostgreSQL schema.
	provider := newCustomerTagRestartProvider(8)
	defer provider.Close()
	config := customerTagRestartRuntimeConfig(databaseURL, provider)
	first, err := compose(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if first != nil {
			first.Close()
		}
	}()
	if err = first.bootstrap(ctx, config.Bootstrap); err != nil {
		t.Fatal(err)
	}
	if err = seedCustomerTagRestartJourney(ctx, first, 101); err != nil {
		t.Fatal(err)
	}

	session, csrf := adminAccessLogin(t, first.handler, "tag-restart-owner", "tag-restart-owner-password")
	targets := make([]int64, 0, 101)
	for customerID := int64(1); customerID <= 101; customerID++ {
		targets = append(targets, customerID)
	}
	body, err := json.Marshal(map[string]any{
		"customer_ids":    targets,
		"add_tag_ids":     []int64{1},
		"idempotency_key": "0b9c708d-3e4f-4475-a695-54dc17d1198b",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/customer-tag-commands", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	acceptedResponse := httptest.NewRecorder()
	first.handler.ServeHTTP(acceptedResponse, request)
	acceptedBody := acceptedResponse.Body.String()
	var accepted customerport.TagCommandResult
	if err = json.Unmarshal([]byte(acceptedBody), &accepted); err != nil || acceptedResponse.Code != http.StatusAccepted || accepted.ID < 1 || len(accepted.Lines) != 101 || accepted.State != "queued" {
		t.Fatalf("accept status=%d result=%+v err=%v body=%s", acceptedResponse.Code, accepted, err, acceptedBody)
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 101} {
		var got int
		if err = first.pool.Native().QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	// Maintenance may enqueue unrelated River work while this Composition is
	// starting. Verify the business invariant through the command's immutable
	// line-to-effect-to-job bindings: every one of the 101 accepted targets has
	// exactly one distinct External Effect and River job.
	var boundLines, distinctEffects, distinctJobs int
	if err = first.pool.Native().QueryRow(ctx, `SELECT
		count(*), count(DISTINCT effect.id), count(DISTINCT job.river_job_id)
		FROM customer_tag_command_lines line
		JOIN external_effects effect ON line.effect_ref='eer_' || effect.id::text
		JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation
		WHERE line.command_id=$1`, accepted.ID).Scan(&boundLines, &distinctEffects, &distinctJobs); err != nil || boundLines != 101 || distinctEffects != 101 || distinctJobs != 101 {
		t.Fatalf("customer tag effect bindings lines=%d effects=%d jobs=%d want=101 err=%v", boundLines, distinctEffects, distinctJobs, err)
	}

	firstRun, stopFirst := startCustomerTagRestartRuntime(first, ctx)
	if !provider.WaitForBlocked(ctx, 1) {
		stopCustomerTagRestartRuntime(t, stopFirst, firstRun)
		t.Fatalf("first runtime did not reach an in-flight provider call; calls=%v", provider.Calls())
	}
	stopCustomerTagRestartRuntime(t, stopFirst, firstRun)

	var beforeExecuted, beforeUnknown, beforePending int
	if err = first.pool.Native().QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE state='executed'),
		count(*) FILTER (WHERE state='outcome_unknown'),
		count(*) FILTER (WHERE state IN ('accepted','queued','attempted','retryable_failed'))
		FROM customer_tag_command_lines`).Scan(&beforeExecuted, &beforeUnknown, &beforePending); err != nil {
		t.Fatal(err)
	}
	if beforeExecuted < 1 || beforeUnknown < 1 || beforePending < 1 {
		t.Fatalf("first runtime must leave executed, unknown and pending lines: executed=%d unknown=%d pending=%d", beforeExecuted, beforeUnknown, beforePending)
	}
	first.Close()
	first = nil

	// Recompose from the same database after the first runtime has stopped. The
	// fixture releases only now, so any second provider call for terminal work
	// would be visible as a duplicate rather than hidden by a test helper.
	second, err := compose(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondRun, stopSecond := startCustomerTagRestartRuntime(second, ctx)
	provider.Resume()
	defer stopCustomerTagRestartRuntime(t, stopSecond, secondRun)
	if !waitForCustomerTagRestartTerminal(ctx, second.pool.Native(), accepted.ID, 101) {
		t.Fatal("second runtime did not finish the queued batch")
	}

	var commandState string
	var executed, unknown, pending int
	if err = second.pool.Native().QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, accepted.ID).Scan(&commandState); err != nil {
		t.Fatal(err)
	}
	if err = second.pool.Native().QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE state='executed'),
		count(*) FILTER (WHERE state='outcome_unknown'),
		count(*) FILTER (WHERE state IN ('accepted','queued','attempted','retryable_failed'))
		FROM customer_tag_command_lines WHERE command_id=$1`, accepted.ID).Scan(&executed, &unknown, &pending); err != nil {
		t.Fatal(err)
	}
	if commandState != "outcome_unknown" || executed < beforeExecuted || unknown < beforeUnknown || executed+unknown != 101 || pending != 0 {
		t.Fatalf("restart aggregate state=%q executed=%d unknown=%d pending=%d; first executed=%d unknown=%d", commandState, executed, unknown, pending, beforeExecuted, beforeUnknown)
	}
	calls := provider.Calls()
	if len(calls) != 101 {
		t.Fatalf("provider targets=%d want=101", len(calls))
	}
	for externalUserID, count := range calls {
		if count != 1 {
			t.Fatalf("provider call duplicated for %q: %d", externalUserID, count)
		}
	}
}

func customerTagRestartRuntimeConfig(databaseURL string, provider *customerTagRestartProvider) platformconfig.Runtime {
	key := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{'r'}, 32))
	return platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://customer-tag-restart.example.test",
		ReleaseSHA: "customer-tag-restart", WorkerOwner: "customer-tag-restart", WorkerLimit: 1,
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "customer-tag-restart-webhook-secret"},
		Survey:    platformconfig.Survey{DataKey: key, IdentityPhoneDataKey: key},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "tag-restart-owner", Password: "tag-restart-owner-password", DisplayName: "Tag Restart Owner"},
		Effects:   platformconfig.Effects{ProviderEnabled: true},
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact-secret", ContextSigningKey: "customer-tag-restart-context-key-32", CustomerTagProviderEnabled: true, APIBase: provider.URL(), HTTPClient: provider.Client()},
	}
}

func startCustomerTagRestartRuntime(application *composedApplication, parent context.Context) (<-chan error, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	go func() { done <- application.effectsRuntime.Run(ctx) }()
	return done, cancel
}

func stopCustomerTagRestartRuntime(t *testing.T, stop context.CancelFunc, done <-chan error) {
	t.Helper()
	stop()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("effects runtime stop: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("effects runtime did not stop")
	}
}

func waitForCustomerTagRestartTerminal(ctx context.Context, pool *pgxpool.Pool, commandID int64, count int) bool {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		var terminal int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM customer_tag_command_lines WHERE command_id=$1 AND state IN ('executed','outcome_unknown','final_failed','reconciled','cancelled','rejected')`, commandID).Scan(&terminal)
		if err == nil && terminal == count {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

type customerTagRestartProvider struct {
	server           *httptest.Server
	mu               sync.Mutex
	calls            map[string]int
	firstPassSuccess int
	successes        int
	unknownIssued    bool
	blocked          chan struct{}
	resume           chan struct{}
	resumeOnce       sync.Once
}

func newCustomerTagRestartProvider(firstPassSuccess int) *customerTagRestartProvider {
	fixture := &customerTagRestartProvider{
		calls:            make(map[string]int),
		firstPassSuccess: firstPassSuccess,
		blocked:          make(chan struct{}, 4),
		resume:           make(chan struct{}),
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/mark_tag":
			var request struct {
				ExternalUserID string   `json:"external_userid"`
				UserID         string   `json:"userid"`
				AddTag         []string `json:"add_tag"`
				RemoveTag      []string `json:"remove_tag"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.UserID != "fixture-staff" || len(request.AddTag) != 1 || request.AddTag[0] != "fixture-provider-add" || len(request.RemoveTag) != 0 || request.ExternalUserID == "" {
				http.Error(w, "unexpected tag mutation", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.calls[request.ExternalUserID]++
			unknown := !fixture.unknownIssued
			if unknown {
				fixture.unknownIssued = true
			}
			block := !unknown && fixture.successes >= fixture.firstPassSuccess
			if !unknown && !block {
				fixture.successes++
			}
			fixture.mu.Unlock()
			if unknown {
				// A plain HTTP 500 has no trusted Provider errcode. The real adapter
				// therefore records outcome_unknown and must never resend it.
				http.Error(w, "fixture plain response failure", http.StatusInternalServerError)
				return
			}
			if block {
				select {
				case fixture.blocked <- struct{}{}:
				default:
				}
				select {
				case <-fixture.resume:
				case <-r.Context().Done():
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/get":
			externalUserID := r.URL.Query().Get("external_userid")
			if externalUserID == "" {
				http.Error(w, "missing external user", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errcode":          0,
				"external_contact": map[string]any{"external_userid": externalUserID, "name": "fixture", "type": 1, "gender": 0},
				"follow_user":      []any{map[string]any{"userid": "fixture-staff", "tags": []any{map[string]any{"tag_id": "fixture-provider-add", "name": "fixture observed", "type": 1}}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return fixture
}

func (fixture *customerTagRestartProvider) URL() string          { return fixture.server.URL }
func (fixture *customerTagRestartProvider) Client() *http.Client { return fixture.server.Client() }
func (fixture *customerTagRestartProvider) Close()               { fixture.server.Close() }
func (fixture *customerTagRestartProvider) Resume() {
	fixture.resumeOnce.Do(func() { close(fixture.resume) })
}
func (fixture *customerTagRestartProvider) WaitForBlocked(ctx context.Context, want int) bool {
	for received := 0; received < want; received++ {
		select {
		case <-fixture.blocked:
		case <-ctx.Done():
			return false
		case <-time.After(20 * time.Second):
			return false
		}
	}
	return true
}
func (fixture *customerTagRestartProvider) Calls() map[string]int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	copy := make(map[string]int, len(fixture.calls))
	for externalUserID, count := range fixture.calls {
		copy[externalUserID] = count
	}
	return copy
}

func seedCustomerTagRestartJourney(ctx context.Context, application *composedApplication, count int) error {
	if application == nil || application.pool == nil || count < 1 {
		return errors.New("customer tag restart fixture is unavailable")
	}
	pool := application.pool.Native()
	if _, err := pool.Exec(ctx, `UPDATE admin_users SET wecom_userid='fixture-staff' WHERE id=1`); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE SELECT value,'active' FROM generate_series(1,$1) value`, count); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		SELECT value,'wecom_external_userid','wecom-corp:fixture-corp','fixture-restart-' || lpad(value::text,3,'0'),'verified','restart_fixture',1,clock_timestamp() FROM generate_series(1,$1) value`, count); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,started_at,completed_at) VALUES('customer-tag-restart-seed','manual','succeeded','wecom-corp:fixture-corp',jsonb_build_array('fixture-staff'),clock_timestamp(),clock_timestamp())`); err != nil {
		return err
	}
	var err error
	var runID int64
	if err = pool.QueryRow(ctx, `SELECT id FROM wecom_customer_sync_runs WHERE run_key='customer-tag-restart-seed'`).Scan(&runID); err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,activation_status,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id)
		SELECT value,'wecom-corp:fixture-corp',identity.id,'fixture restart ' || value::text,'active',decode(repeat('00',32),'hex'),$1,clock_timestamp(),'fixture-staff',$1
		FROM generate_series(1,$2) value JOIN customer_identities identity ON identity.customer_id=value AND identity.kind='wecom_external_userid'`, runID, count); err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) SELECT 'fixture-corp','fixture-staff',value,true FROM generate_series(1,$1) value`, count); err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,last_synced_at,updated_at)
		SELECT value,'active','fixture restart ' || value::text,'customer #' || value::text,'active','restart_fixture',clock_timestamp(),clock_timestamp() FROM generate_series(1,$1) value`, count); err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, `INSERT INTO tag_groups(group_name,sort_order) VALUES('fixture restart group',0)`); err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, `INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES((SELECT id FROM tag_groups WHERE group_name='fixture restart group'),'fixture restart add',0)`); err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('fixture-provider-add',(SELECT id FROM tag_catalog_tags WHERE tag_name='fixture restart add'))`); err != nil {
		return err
	}
	var staff, profiles, relationships, identities, bindings, localTagID int
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM admin_users WHERE wecom_userid='fixture-staff' AND is_active),
		(SELECT count(*) FROM wecom_external_contact_profiles profile JOIN wecom_customer_sync_runs run ON run.id=profile.primary_owner_run_id AND run.status='succeeded' WHERE profile.corp_scope='wecom-corp:fixture-corp' AND profile.primary_owner_userid='fixture-staff' AND profile.activation_status='active'),
		(SELECT count(*) FROM wecom_follow_relationships WHERE corp_id='fixture-corp' AND employee_id='fixture-staff' AND active),
		(SELECT count(*) FROM customer_identities WHERE kind='wecom_external_userid' AND scope_key='wecom-corp:fixture-corp' AND assurance='verified' AND status='active'),
		(SELECT count(*) FROM tag_provider_tag_bindings WHERE provider_tag_id='fixture-provider-add'),
		(SELECT id FROM tag_catalog_tags WHERE tag_name='fixture restart add')`).Scan(&staff, &profiles, &relationships, &identities, &bindings, &localTagID); err != nil {
		return err
	}
	if staff != 1 || profiles != count || relationships != count || identities != count || bindings != 1 || localTagID != 1 {
		return fmt.Errorf("customer tag restart fixture facts staff=%d profiles=%d relationships=%d identities=%d bindings=%d local_tag_id=%d count=%d", staff, profiles, relationships, identities, bindings, localTagID, count)
	}
	return nil
}

func TestCustomerTagProviderCompositionKeepsChannelAndGenericFlagsIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, test := range []struct {
		name, wantChannel, wantGeneric string
		channelEnabled, genericEnabled bool
		wantExternalUserID             string
	}{
		{
			name: "legacy channel tag remains authorized when generic flag is off", channelEnabled: true, genericEnabled: false,
			wantChannel: "executed", wantGeneric: "final_failed", wantExternalUserID: "fixture-restart-001",
		},
		{
			name: "generic tag cannot authorize disabled channel entry tag", channelEnabled: false, genericEnabled: true,
			wantChannel: "final_failed", wantGeneric: "executed", wantExternalUserID: "fixture-restart-002",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
			defer cleanup()
			provider := newCustomerTagRestartProvider(100)
			// This matrix isolates capability routing. T04 above covers the
			// outcome_unknown/restart path with the same real Provider adapter.
			provider.unknownIssued = true
			provider.Resume()
			defer provider.Close()
			config := customerTagRestartRuntimeConfig(databaseURL, provider)
			config.WeCom.CustomerTagProviderEnabled = test.genericEnabled
			config.WeCom.ChannelTagProviderEnabled = test.channelEnabled
			config.WeCom.CallbackEnabled = true
			config.WeCom.CallbackToken = "customer-tag-flag-matrix-token"
			config.WeCom.CallbackAESKey = strings.Repeat("a", 43)
			config.WeCom.ChannelStateHMACKey = strings.Repeat("h", 32)
			application, err := compose(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer application.Close()
			if err = application.bootstrap(ctx, config.Bootstrap); err != nil {
				t.Fatal(err)
			}
			if err = seedCustomerTagRestartJourney(ctx, application, 2); err != nil {
				t.Fatal(err)
			}
			// Accept through the real Channel EntrantAction store. It creates the
			// linked entrant action and Customer command in one transaction, so the
			// completion sink exercises the real channel contract rather than a
			// synthetic source string.
			if application.channelEntrantActions == nil {
				t.Fatal("composition did not retain Channel entrant actions")
			}
			uow, err := platformpostgres.NewUnitOfWork(application.pool)
			if err != nil {
				t.Fatal(err)
			}
			digester, err := wecom.NewHMACStateDigester([]byte(config.WeCom.ChannelStateHMACKey))
			if err != nil {
				t.Fatal(err)
			}
			channelFixture := seedChannelWelcomeFixture(t, ctx, uow, channelstore.NewPostgreSQLStore(), digester, 1, "customer-tag-flag-matrix", "customer-tag-flag-matrix-state", false, 1, 1)
			if err = uow.Within(ctx, func(tx context.Context) error {
				return application.channelEntrantActions.AcceptEntrantActions(tx, channelport.EntrantActionCommand{
					CallbackID: "customer-tag-flag-matrix-channel", CustomerID: 1, Resolution: channelFixture.resolution, OccurredAt: time.Now().UTC(),
				})
			}); err != nil {
				t.Fatal(err)
			}

			// The generic command follows the actual authenticated HTTP route. Its
			// source is independent from the Channel callback source above.
			session, csrf := adminAccessLogin(t, application.handler, "tag-restart-owner", "tag-restart-owner-password")
			body, err := json.Marshal(map[string]any{"customer_ids": []int64{2}, "add_tag_ids": []int64{1}, "idempotency_key": "7ebfa1d1-8a94-4b4c-8c1f-6980e4aa8b8e"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/customer-tag-commands", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-CSRF-Token", csrf)
			request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
			request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
			acceptedResponse := httptest.NewRecorder()
			application.handler.ServeHTTP(acceptedResponse, request)
			genericBody := acceptedResponse.Body.String()
			var generic customerport.TagCommandResult
			if err = json.Unmarshal([]byte(genericBody), &generic); err != nil || acceptedResponse.Code != http.StatusAccepted || generic.ID < 1 || len(generic.Lines) != 1 || generic.State != "queued" {
				t.Fatalf("generic status=%d command=%+v err=%v body=%s", acceptedResponse.Code, generic, err, genericBody)
			}
			runtimeDone, stopRuntime := startCustomerTagRestartRuntime(application, ctx)
			defer stopCustomerTagRestartRuntime(t, stopRuntime, runtimeDone)
			if !waitForCustomerTagSourceTerminal(ctx, application.pool.Native(), 2) {
				t.Fatal("composition runtime did not reach terminal tag lines")
			}
			states := map[string]string{}
			rows, err := application.pool.Native().Query(ctx, `SELECT command.source,line.state FROM customer_tag_command_lines line JOIN customer_tag_commands command ON command.id=line.command_id ORDER BY command.id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var source, state string
				if err = rows.Scan(&source, &state); err != nil {
					t.Fatal(err)
				}
				states[source] = state
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if states["channel_entry_tag"] != test.wantChannel || states["admin_customer_ui"] != test.wantGeneric {
				t.Fatalf("source states=%v want channel=%q generic=%q", states, test.wantChannel, test.wantGeneric)
			}
			var entrantState string
			if err = application.pool.Native().QueryRow(ctx, `SELECT state FROM channel_entrant_actions WHERE callback_id='customer-tag-flag-matrix-channel' AND action_kind='entry_tag'`).Scan(&entrantState); err != nil || entrantState != test.wantChannel {
				t.Fatalf("channel entrant state=%q want=%q err=%v", entrantState, test.wantChannel, err)
			}
			calls := provider.Calls()
			if len(calls) != 1 || calls[test.wantExternalUserID] != 1 {
				t.Fatalf("provider calls=%v want only %q once", calls, test.wantExternalUserID)
			}
		})
	}
}

func waitForCustomerTagSourceTerminal(ctx context.Context, pool *pgxpool.Pool, count int) bool {
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		var terminal int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM customer_tag_command_lines WHERE state IN ('executed','outcome_unknown','final_failed','reconciled','cancelled','rejected')`).Scan(&terminal)
		if err == nil && terminal == count {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

func TestCustomerTagCommandCompositionSerializesDifferentKeysUntilTerminal(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := customerTagRuntimePool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-order-admin','$argon2id$test','Tag order admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active')`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, customerTagCompositionGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}

	// Two distinct keys for the same Customer race. Customer-row locking makes
	// exactly one accepted; the other cannot reorder an add/remove call.
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	var wg sync.WaitGroup
	for _, command := range []customerport.TagCommand{
		{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "same-customer-add", IdempotencyKey: "same-customer-add", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 2, AddTagIDs: []int64{1}}}},
		{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "same-customer-remove", IdempotencyKey: "same-customer-remove", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 2, RemoveTagIDs: []int64{1}}}},
	} {
		wg.Add(1)
		go func(command customerport.TagCommand) {
			defer wg.Done()
			<-start
			_, submitErr := service.SubmitTagCommand(ctx, command)
			outcomes <- submitErr
		}(command)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	accepted, conflicts := 0, 0
	for submitErr := range outcomes {
		if submitErr == nil {
			accepted++
		} else if errors.Is(submitErr, customerport.ErrTagCommandConflict) {
			conflicts++
		} else {
			t.Fatalf("same-customer submission=%v", submitErr)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("accepted=%d conflicts=%d", accepted, conflicts)
	}

	first, err := service.SubmitTagCommand(ctx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "unknown-add", IdempotencyKey: "unknown-add", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, AddTagIDs: []int64{1}}}})
	if err != nil || len(first.Lines) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return customerstore.TagCommandPostgreSQL{}.CompleteTagCommand(tx, customerport.TagCommandCompletion{EffectRef: first.Lines[0].EffectRef, State: "outcome_unknown", ResultDigest: string(effectport.Hash("unknown-before-opposite")), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.SubmitTagCommand(ctx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "unknown-remove", IdempotencyKey: "unknown-remove", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, RemoveTagIDs: []int64{1}}}})
	if !errors.Is(err, customerport.ErrTagCommandConflict) {
		t.Fatalf("opposite after unknown=%v", err)
	}
	var effectsCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectsCount); err != nil || effectsCount != 2 {
		t.Fatalf("effects=%d want=2 err=%v", effectsCount, err)
	}
}

func customerTagRuntimePool(t *testing.T, ctx context.Context, url string) (*platformpostgres.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	schema := "aicrm_tag_runtime_" + hex.EncodeToString(raw)
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0019_tag_catalog_sync_projection.sql", "0022_customer_profile_sections.sql", "0093_customer_tag_commands.sql", "0107_tag_catalog_mutation_receipts.sql", "0125_outbound_material_preparation.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(body)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	}
}
