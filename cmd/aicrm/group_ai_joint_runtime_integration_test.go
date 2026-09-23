package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistanthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/http"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/segment"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
)

// TestAIAssistantAndGroupOpsShareRiverOutboundAndEffects exercises both real
// outbound leaves in one PostgreSQL schema. It starts River only after both
// acceptances commit, then repeats their command receipts across a restart.
func TestAIAssistantAndGroupOpsShareRiverOutboundAndEffects(t *testing.T) {
	native, cleanup := aiAssistantHTTPJourneyPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, migration := range []string{"0012_group_ops.sql", "0016_media_content_packages.sql", "0078_group_ops_provider_tasks.sql", "0081_group_ops_webhook_unconfigured_reference.sql", "0101_group_ops_ui_metadata.sql", "0116_group_ops_operation_member_directory.sql", "0155_group_ops_webhook_dynamic_executions.sql"} {
		if err := applyAIAssistantHTTPJourneyMigration(ctx, native, migration); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	seedAIAssistantHTTPJourney(t, native)
	var groupActor int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('joint-group','$argon2id$joint','Joint Group','journey-sender',true,false) RETURNING id`).Scan(&groupActor); err != nil {
		t.Fatal(err)
	}

	var privateCalls, groupCalls, messageSequence atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			secret := r.URL.Query().Get("corpsecret")
			if secret != "journey-contact-secret" && secret != "runtime-contact-secret" {
				t.Errorf("unexpected contact secret")
				http.Error(w, "bad secret", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "joint-token", "expires_in": 7200})
		case "/cgi-bin/externalcontact/add_msg_template":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode joint WeCom request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			sender, _ := request["sender"].(string)
			switch sender {
			case "staff-1":
				privateCalls.Add(1)
			case "journey-sender":
				if request["chat_type"] != "group" {
					t.Errorf("group request=%#v", request)
				}
				groupCalls.Add(1)
			default:
				t.Errorf("unknown sender=%q request=%#v", sender, request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "msgid": fmt.Sprintf("joint-msg-%d", messageSequence.Add(1))})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	privateWeCom, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "corp-1", ContactSecret: "journey-contact-secret", APIBase: providerServer.URL, HTTPClient: providerServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	groupWeCom, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "corp-1", ContactSecret: "runtime-contact-secret", APIBase: providerServer.URL, HTTPClient: providerServer.Client()})
	if err != nil {
		t.Fatal(err)
	}

	accessRepository := accessstore.NewPostgreSQL()
	identities := identityquery.NewPostgreSQL()
	aiStore, err := aiassistantstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	aiService, err := aiassistantapp.NewService(uow, aiStore, journeyCustomerReader{}, aiStaffSnapshotAdapter{repository: accessRepository}, journeyTextMaterials{}, identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, identities)
	if err != nil {
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
	continuation := groupopsapp.NewContinuationWorker()
	if err = river.AddWorkerSafely[groupopsapp.ContinuationJobArgs](workers, continuation); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	privateWriter, err := outbound.NewPrivateMessageRepository(native, effects)
	if err != nil {
		t.Fatal(err)
	}
	if err = aiService.BindOutbound(privateWriter, true); err != nil {
		t.Fatal(err)
	}
	if err = aiService.BindReconciler(effects); err != nil {
		t.Fatal(err)
	}
	staff := groupOpsStaffAdapter{access: accessRepository, owners: groupStore}
	groupRuntime := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, effects, staff, nil, staff, nil, journeyReconciler{repository: effects}, materials)
	groupRuntime.SetDispatchEnabled(true)
	if err = continuation.Bind(groupRuntime); err != nil {
		t.Fatal(err)
	}
	privateProvider, err := outbound.NewPrivateMessageProvider(true, privateWriter, aiPrivateTargetResolver{uow: uow, identities: identities, access: accessRepository, relationships: wecom.NewPostgreSQLFollowRelationshipStore(), corpID: "corp-1"}, aiPrivatePayloadReader{content: aiStore}, privateWeCom)
	if err != nil {
		t.Fatal(err)
	}
	readiness := groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaStore, freezer: freezer}
	groupProvider, err := outbound.NewGroupMessageProvider(outbound.GroupMessageProviderConfig{Enabled: true, Executions: groupOpsDispatchReader{uow: uow, execution: groupStore, senders: staff}, Materials: readiness, Writer: groupWeCom})
	if err != nil {
		t.Fatal(err)
	}
	if err = effectsModule.SetProviderAdapter(outbound.NewProviderRouterWithPrivate(nil, groupProvider, privateProvider)); err != nil {
		t.Fatal(err)
	}
	groupCompletion, err := outbound.NewGroupMessageCompletionSink(groupStore, groupStore)
	if err != nil {
		t.Fatal(err)
	}
	continuations, err := groupopsapp.NewRiverContinuationEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	groupCompletion.WithContinuation(continuations)
	privateCompletion, err := outbound.NewPrivateMessageCompletionSink(privateWriter, aiStore)
	if err != nil {
		t.Fatal(err)
	}
	completionRouter, err := outbound.NewCompletionRouterWithPrivate(nil, groupCompletion, privateCompletion)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(completionRouter); err != nil {
		t.Fatal(err)
	}
	if _, err = effectsModule.Bind(effects, journeyHTTPSecurity{}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	handler, err := aiassistanthttp.NewHandler(aiassistanthttp.Config{Application: aiService, Security: journeyHTTPSecurity{}, Authorizer: accessapp.AIAssistantAuthorizer{}, Integration: aiassistanthttp.IntegrationConfig{Enabled: true, Key: "journey", Secret: "01234567890123456789012345678901", ActorID: 9, WeComCorpID: "corp-1", MaxSkew: 5 * time.Minute}, DispatchReady: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()
	created := journeySignedJSON(t, routes, now, "01234567890123456789012345678901", "joint-nonce-00000001", "joint-intake-key-00001", map[string]any{"external_userid": "external-1", "owner_userid": "staff-1", "content_package": map[string]any{"content_text": "joint private"}, "external_event_id": "joint-event-1"})
	var intake struct {
		Plan struct {
			ID int64 `json:"id"`
		} `json:"plan"`
	}
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &intake) != nil || intake.Plan.ID < 1 {
		t.Fatalf("AI intake=%d %s", created.Code, created.Body.String())
	}
	list := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/recipients?limit=50", "", nil)
	var recipients struct {
		Items []struct {
			ID      int64 `json:"id"`
			Version int64 `json:"version"`
		} `json:"items"`
	}
	if err = json.Unmarshal(list.Body.Bytes(), &recipients); err != nil || len(recipients.Items) != 1 {
		t.Fatalf("AI recipients=%s err=%v", list.Body.String(), err)
	}
	recipient := recipients.Items[0]
	if reply := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/recipients/"+itoa(recipient.ID)+"/review", "joint-review-1", map[string]any{"expected_version": recipient.Version, "decision": "approved"}); reply.Code != http.StatusOK {
		t.Fatalf("AI review=%d %s", reply.Code, reply.Body.String())
	}
	plan := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID), "", nil)
	var planState struct {
		Plan struct {
			Version int64 `json:"version"`
		} `json:"plan"`
	}
	if err = json.Unmarshal(plan.Body.Bytes(), &planState); err != nil {
		t.Fatal(err)
	}
	preview := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/preview-approval", "joint-preview-1", map[string]any{"expected_version": planState.Plan.Version})
	var previewBody struct {
		PreviewDigest string `json:"preview_digest"`
	}
	if err = json.Unmarshal(preview.Body.Bytes(), &previewBody); err != nil || previewBody.PreviewDigest == "" {
		t.Fatalf("AI preview=%s err=%v", preview.Body.String(), err)
	}
	approve := func() *httptest.ResponseRecorder {
		return journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/approve", "joint-approve-1", map[string]any{"expected_version": planState.Plan.Version, "preview_digest": previewBody.PreviewDigest})
	}
	if reply := approve(); reply.Code != http.StatusOK {
		t.Fatalf("AI approve=%d %s", reply.Code, reply.Body.String())
	}

	groupPlan := createRiverJourneyPlan(t, ctx, uow, groupStore, groupActor)
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at) VALUES ('chat-river-1',$1,'Joint one',1,'sha256:2a4d94c0173fb5ae03b9c51223a7e16b48f261aa2603ae1bd91a8d5de54b7ec5',clock_timestamp()),('chat-river-2',$1,'Joint two',1,'sha256:4679c40a8551974f87b357c95f2d3a9b7030ddd442a4e440fc1aa094624bca6e',clock_timestamp())`, groupActor); err != nil {
		t.Fatal(err)
	}
	accepted, err := groupRuntime.AcceptBroadcast(ctx, groupPlan, groupActor, "joint-group-accept-1")
	if err != nil || accepted.Accepted != 2 {
		t.Fatalf("group acceptance=%+v err=%v", accepted, err)
	}
	var effectsCount, fingerprints, groupEffect, privateEffect int
	if err = native.QueryRow(ctx, `SELECT count(*),count(DISTINCT envelope_fingerprint),count(*) FILTER (WHERE kind='group_message'),count(*) FILTER (WHERE kind='outbound_message') FROM external_effects`).Scan(&effectsCount, &fingerprints, &groupEffect, &privateEffect); err != nil || effectsCount != 3 || fingerprints != 3 || groupEffect != 2 || privateEffect != 1 {
		t.Fatalf("shared EER namespace effects=%d fingerprints=%d group=%d private=%d err=%v", effectsCount, fingerprints, groupEffect, privateEffect, err)
	}

	runtimeService, err := platformjobqueue.NewRuntime(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	start, stop := startGroupOpsRiver(t, runtimeService)
	start()
	deadline := time.Now().Add(12 * time.Second)
	for privateCalls.Load()+groupCalls.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	waitGroupOpsDelayedSuccessors(t, native, groupPlan, 2)
	var effectBaseline int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectBaseline); err != nil || effectBaseline != 5 {
		stop()
		t.Fatalf("first group completion must persist two delayed successors: effects=%d err=%v", effectBaseline, err)
	}
	stop()
	if privateCalls.Load() != 1 || groupCalls.Load() != 2 {
		t.Fatalf("shared leaves private=%d group=%d", privateCalls.Load(), groupCalls.Load())
	}
	if replay, replayErr := groupRuntime.AcceptBroadcast(ctx, groupPlan, groupActor, "joint-group-accept-1"); replayErr != nil || replay.Run.ID != accepted.Run.ID {
		t.Fatalf("group replay=%+v err=%v", replay, replayErr)
	}
	if reply := approve(); reply.Code != http.StatusOK {
		t.Fatalf("AI approval replay=%d %s", reply.Code, reply.Body.String())
	}
	restarted, err := platformjobqueue.NewRuntime(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	start, stop = startGroupOpsRiver(t, restarted)
	start()
	time.Sleep(250 * time.Millisecond)
	stop()
	if privateCalls.Load() != 1 || groupCalls.Load() != 2 {
		t.Fatalf("replay/restart duplicated provider calls: private=%d group=%d", privateCalls.Load(), groupCalls.Load())
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectsCount); err != nil || effectsCount != effectBaseline {
		t.Fatalf("replay/restart minted effects=%d baseline=%d err=%v", effectsCount, effectBaseline, err)
	}
}

// TestAutomationAIAssistantAndGroupOpsShareRiverRuntime is the final 05/06/07
// joint journey: a real Segment refresh creates Automation enrollment work,
// manual audience confirmation creates an AI pending plan, and GroupOps shares
// the same EER, River runtime, router and completion sink.
func TestAutomationAIAssistantAndGroupOpsShareRiverRuntime(t *testing.T) {
	ctx := context.Background()
	native, cleanup := automationAudienceRuntimePool(t)
	defer cleanup()
	for _, migration := range []string{"0004_wecom.sql", "0009_customer_activation.sql", "0022_customer_profile_sections.sql", "0012_group_ops.sql", "0016_media_content_packages.sql", "0078_group_ops_provider_tasks.sql", "0081_group_ops_webhook_unconfigured_reference.sql", "0101_group_ops_ui_metadata.sql", "0116_group_ops_operation_member_directory.sql", "0155_group_ops_webhook_dynamic_executions.sql"} {
		if err := applyAIAssistantHTTPJourneyMigration(ctx, native, migration); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	staffID := automationAudienceInsertProviderStaff(t, ctx, native)
	var groupActor int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('joint-runtime-group','$argon2id$joint','Joint Runtime Group','journey-sender',true,false) RETURNING id`).Scan(&groupActor); err != nil {
		t.Fatal(err)
	}
	segmentRepo, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	automationRepo, err := automationstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	aiRepo, err := aiassistantstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaRepo, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaService, err := mediaapp.NewHTTPFacade(mediaRepo)
	if err != nil {
		t.Fatal(err)
	}
	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	accessRepository := accessstore.NewPostgreSQL()
	identities := identityquery.NewPostgreSQL()
	customersRead := customerstore.NewPostgreSQL()
	aiCustomers := aiCustomerSnapshotAdapter{read: func(ctx context.Context, id customerdomain.CustomerID) (customerdomain.CustomerID, customerdomain.Status, string, string, error) {
		detail, readErr := customersRead.Detail(ctx, id)
		return detail.CustomerID, detail.CustomerStatus, detail.DisplayName, detail.OneIDLabel, readErr
	}}
	aiService, err := aiassistantapp.NewService(uow, aiRepo, aiCustomers, aiStaffSnapshotAdapter{repository: accessRepository}, aiMaterialAdapter{capturer: mediaRepo, references: mediaRepo}, identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, identities)
	if err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	refreshWorker, memberWorker := segment.NewAudienceRefreshWorker(), segment.NewAudienceMemberEventDispatchWorker()
	if err = river.AddWorkerSafely[segment.AudienceRefreshJobArgs](workers, refreshWorker); err != nil {
		t.Fatal(err)
	}
	if err = river.AddWorkerSafely[segment.AudienceMemberEventDispatchJobArgs](workers, memberWorker); err != nil {
		t.Fatal(err)
	}
	continuation := groupopsapp.NewContinuationWorker()
	if err = river.AddWorkerSafely[groupopsapp.ContinuationJobArgs](workers, continuation); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	refreshJobs, err := segment.NewRiverRefreshEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	memberJobs, err := segment.NewRiverMemberEventEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	customers := automationAudienceInsertProviderCustomers(t, ctx, native)
	for _, customerID := range customers {
		if _, err = native.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,source_version,updated_at) VALUES($1,'active','Joint runtime customer',$2,'active','joint-runtime',1,clock_timestamp())`, customerID, fmt.Sprintf("CID-%d", customerID)); err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('runtime-corp','sender-a',$1,true)`, customerID); err != nil {
			t.Fatal(err)
		}
	}
	source := &automationAudienceSource{}
	evaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, source, segmentadapter.CanonicalCustomers{UoW: uow, Resolver: canonicalCustomerAdapter{reader: identities}})
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := segmentapp.NewSnapshotService(uow, segmentRepo, evaluator, refreshJobs, memberJobs)
	if err != nil {
		t.Fatal(err)
	}
	if err = refreshWorker.BindService(snapshots); err != nil {
		t.Fatal(err)
	}
	materials := automationAudienceCreateMedia(t, ctx, mediaRepo, staffID)
	if _, err = mediaRepo.UpdateMiniProgram(ctx, materials.miniID, staffID, "joint-runtime-mini-revised-0001", map[string]any{"title": "Runtime card revised"}); err != nil {
		t.Fatal(err)
	}
	// The retained library sources predate this composition binding. Their
	// first accepted message must therefore create generic preparation work in
	// preflight, then reuse the resulting provider credential across Automation
	// and AI instead of falling back to per-message byte uploads.
	materialScopeDigest := string(effectport.Hash("joint-runtime.material.scope.v1"))
	materialPreparation, err := outbound.NewMaterialPreparationService(uow, effects, native, mediaRepo)
	if err != nil {
		t.Fatal(err)
	}
	if err = mediaRepo.BindMaterialPreparationAccepter(materialPreparation, materialScopeDigest); err != nil {
		t.Fatal(err)
	}
	automationService := automationapp.NewAgentServiceWithMediaReferences(uow, automationRepo, mediaRepo, mediaRepo, mediaRepo, mediaRepo, automationRepo)
	agent := automationAudiencePublishedAgent(t, ctx, automationService, staffID, materials)
	published, found, err := automationService.PublishedAgent(ctx, agent.ID)
	if err != nil || !found {
		t.Fatalf("published agent=%+v found=%t err=%v", published, found, err)
	}
	packageID := automationAudiencePackage(t, ctx, uow, segmentRepo, time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	segmentStaff := automationOpsStaffAdapter{uow: uow, users: accessRepository}
	execution, err := segmentapp.NewExecutionService(uow, segmentRepo, automationService, segmentStaff, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = execution.PutBinding(ctx, segmentapp.BindingCommand{PackageID: packageID, ExpectedPackageVersion: 2, AgentID: agent.ID, ExpectedPublishedVersion: published.PublishedVersion, ExpectedAgentDigest: automationAudienceCombinedDigest(published.ContentDigest, published.MaterialsDigest), Actor: staffID, IdempotencyKey: "joint-runtime-binding-0001"}); err != nil {
		t.Fatal(err)
	}
	if _, err = execution.ReplaceSenders(ctx, segmentapp.SendersCommand{PackageID: packageID, ExpectedPackageVersion: 3, ProviderMemberIDs: []string{"sender-a"}, Actor: staffID, IdempotencyKey: "joint-runtime-senders-0001"}); err != nil {
		t.Fatal(err)
	}

	privateWriter, err := outbound.NewPrivateMessageRepository(native, effects)
	if err != nil {
		t.Fatal(err)
	}
	if err = aiService.BindOutbound(privateWriter, true); err != nil {
		t.Fatal(err)
	}
	if err = aiService.BindReconciler(effects); err != nil {
		t.Fatal(err)
	}
	messages, err := outbound.NewMessageService(native, uow, effects, automationRepo)
	if err != nil {
		t.Fatal(err)
	}
	automationWeCom := newAutomationAudienceWeComServer(t)
	defer automationWeCom.Close()
	automationWriter, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "runtime-corp", ContactSecret: "runtime-contact-secret", APIBase: automationWeCom.URL, HTTPClient: automationWeCom.Client()})
	if err != nil {
		t.Fatal(err)
	}
	materialClient, err := wecomadapter.New(wecomadapter.Config{Enabled: true, CorpID: "runtime-corp", AgentID: "runtime-agent", Secret: "runtime-app-secret", ContactSecret: "runtime-contact-secret", AdminCallbackURI: "https://example.test/admin-callback", SidebarCallbackURI: "https://example.test/sidebar-callback", APIBase: automationWeCom.URL, HTTPClient: automationWeCom.Client()})
	if err != nil {
		t.Fatal(err)
	}
	materialUploader, err := wecomadapter.NewMaterialUploader(materialClient, materialScopeDigest)
	if err != nil {
		t.Fatal(err)
	}
	materialProvider, err := outbound.NewMaterialPreparationProvider(materialPreparation, materialUploader)
	if err != nil {
		t.Fatal(err)
	}
	frozen := automationFrozenPayloadReader{preparer: aiPrivatePayloadReader{images: mediaService, materials: mediaRepo, attachments: mediaService, uow: uow, capturer: mediaRepo, sources: mediaRepo, preparer: materialPreparation, scopeDigest: materialScopeDigest}}
	messageProvider, err := outbound.NewMessageProvider(outbound.MessageProviderConfig{Enabled: true, CorpScope: "wecom-corp:runtime-corp", Executions: messages, Identities: outboundIdentityAdapter{uow: uow, reader: identities}, Staff: segmentStaff, Content: automationService, Payloads: frozen, Writer: automationWriter})
	if err != nil {
		t.Fatal(err)
	}
	privateProvider, err := outbound.NewPrivateMessageProvider(true, privateWriter, aiPrivateTargetResolver{uow: uow, identities: identities, access: accessRepository, relationships: wecom.NewPostgreSQLFollowRelationshipStore(), corpID: "runtime-corp"}, aiPrivatePayloadReader{content: aiRepo, images: mediaService, materials: mediaRepo, attachments: mediaService, uow: uow, capturer: mediaRepo, sources: mediaRepo, preparer: materialPreparation, scopeDigest: materialScopeDigest}, automationWriter)
	if err != nil {
		t.Fatal(err)
	}
	groupWeCom := newGroupOpsRuntimeWeCom(t)
	defer groupWeCom.Close()
	groupWriter, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "runtime-corp", ContactSecret: "runtime-contact-secret", APIBase: groupWeCom.URL(), HTTPClient: groupWeCom.Client()})
	if err != nil {
		t.Fatal(err)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{reader: mediaRepo})
	if err != nil {
		t.Fatal(err)
	}
	groupMaterials, err := newGroupOpsMaterialAdapter(mediaRepo, freezer)
	if err != nil {
		t.Fatal(err)
	}
	groupStaff := groupOpsStaffAdapter{access: accessRepository, owners: groupStore}
	groupRuntime := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, effects, groupStaff, nil, groupStaff, nil, journeyReconciler{repository: effects}, groupMaterials)
	groupRuntime.SetDispatchEnabled(true)
	if err = continuation.Bind(groupRuntime); err != nil {
		t.Fatal(err)
	}
	groupProvider, err := outbound.NewGroupMessageProvider(outbound.GroupMessageProviderConfig{Enabled: true, Executions: groupOpsDispatchReader{uow: uow, execution: groupStore, senders: groupStaff}, Materials: groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaRepo, freezer: freezer}, Writer: groupWriter})
	if err != nil {
		t.Fatal(err)
	}
	effectWorker := externaleffects.NewWorker(nil, outbound.NewProviderRouterWithMessages(nil, groupProvider, messageProvider).WithPrivateMessage(privateProvider).WithSidebarMedia(outbound.MaterialEffectMux{GenericProvider: materialProvider}))
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, effectWorker); err != nil {
		t.Fatal(err)
	}
	if err = effectWorker.BindRepository(effects); err != nil {
		t.Fatal(err)
	}
	groupCompletion, err := outbound.NewGroupMessageCompletionSink(groupStore, groupStore)
	if err != nil {
		t.Fatal(err)
	}
	continuations, err := groupopsapp.NewRiverContinuationEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	groupCompletion.WithContinuation(continuations)
	privateCompletion, err := outbound.NewPrivateMessageCompletionSink(privateWriter, aiRepo)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := outbound.NewCompletionRouterWithPrivate(nil, groupCompletion, privateCompletion)
	if err != nil {
		t.Fatal(err)
	}
	completion.WithAutomationMessage(messages)
	completion.WithSidebarMedia(outbound.MaterialEffectMux{GenericCompletion: materialPreparation})
	if err = effects.SetCompletionSink(completion); err != nil {
		t.Fatal(err)
	}

	runtimeService, err := automationapp.NewRuntimeService(uow, automationRepo, execution, snapshots, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetMessageAccepter(messages); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetReviewPlanIntake(aiService, automationService); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetOutboundContentFreezer(automationOutboundContentFreezer{capturer: mediaRepo}); err != nil {
		t.Fatal(err)
	}
	if err = memberWorker.Bind(snapshots, automationAudienceEnrollmentSink{runtime: runtimeService}); err != nil {
		t.Fatal(err)
	}

	// Bootstrap the empty and one-member snapshots while no policy is active.
	// The shared runtime below is stopped for all three business acceptances.
	bootstrap, err := platformjobqueue.NewRuntime(native, workers, segment.AudienceRefreshQueue, platformjobqueue.OutboundQueue, platformjobqueue.OutboundMediaQueue)
	if err != nil {
		t.Fatal(err)
	}
	stopBootstrap := automationAudienceStartRuntime(t, bootstrap)
	var stopBootstrapOnce sync.Once
	stopBootstrapSafely := func() { stopBootstrapOnce.Do(stopBootstrap) }
	defer stopBootstrapSafely()
	source.Set(nil)
	if _, err = snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "joint-runtime-empty-0001", ReferenceTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	automationAudienceEventually(t, "joint empty snapshot", func() bool {
		snapshot, ok, e := snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		return e == nil && ok && snapshot.MemberCount == 0
	})
	source.Set(customers[:1])
	if _, err = snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "joint-runtime-one-member-0001", ReferenceTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	var bootstrapSnapshotID int64
	automationAudienceEventually(t, "joint one-member snapshot", func() bool {
		snapshot, ok, e := snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		if e != nil || !ok || snapshot.MemberCount != 1 {
			return false
		}
		bootstrapSnapshotID = int64(snapshot.ID)
		return true
	})
	// The baseline member-entered fact must be consumed while no policy is
	// active. Otherwise stopping this bootstrap runtime can defer it until after
	// activation, incorrectly enrolling the baseline customer in the joint run.
	automationAudienceEventually(t, "bootstrap member-event dispatch", func() bool {
		var completed, effectsNow int
		err := native.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='segment.audience-member-entered-dispatch.v1' AND args->>'snapshot_id'=$1 AND state='completed'`, automationAudienceInt(bootstrapSnapshotID)).Scan(&completed)
		if err != nil || completed != 1 {
			return false
		}
		return native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectsNow) == nil && effectsNow == 0
	})
	stopBootstrapSafely()
	policy, err := runtimeService.CreatePolicy(ctx, automationapp.PolicyCommand{Code: "joint-runtime-entry", Name: "Joint runtime entry", PackageID: segmentport.PackageID(packageID), TriggerKind: automationport.TriggerAudienceMemberEnteredV1, ActionKind: automationport.ActionOutboundMessage, ActionConfig: json.RawMessage(fmt.Sprintf(`{"agent_id":%d}`, agent.ID)), QuietHours: automationAudienceNonBlockingQuietHours(time.Now()), SingleRunLimit: 100, ApprovalStaffID: &staffID, Actor: staffID, IdempotencyKey: "joint-runtime-policy-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtimeService.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: policy.ID, ExpectedVersion: policy.Version, Actor: staffID, Target: automationdomain.PolicyActive, IdempotencyKey: "joint-runtime-policy-active-0001"}); err != nil {
		t.Fatal(err)
	}

	preview, err := runtimeService.CreateBroadcastPreview(ctx, packageID, staffID)
	if err != nil {
		t.Fatal(err)
	}
	manual, err := runtimeService.ConfirmRun(ctx, automationapp.RunConfirmCommand{PackageID: packageID, PackageVersion: preview.PackageVersion, SnapshotID: preview.SnapshotID, AgentID: preview.AgentID, AgentPublishedVersion: preview.AgentPublishedVersion, PreviewDigest: automationapp.PreviewDigestString(preview), Actor: staffID, IdempotencyKey: "joint-runtime-manual-confirm-0001"})
	if err != nil || manual.State != automationport.RunPendingReview || manual.AIPlanID < 1 {
		t.Fatalf("manual=%+v err=%v", manual, err)
	}
	if effectsNow := jointEffectCount(t, ctx, native); effectsNow != 0 {
		t.Fatalf("pending AI review accepted effects=%d", effectsNow)
	}
	plan, err := aiService.GetPlan(ctx, aiassistantport.PlanID(manual.AIPlanID))
	if err != nil || plan.State != aiassistantport.PlanPendingReview {
		t.Fatalf("manual plan=%+v err=%v", plan, err)
	}
	recipients, err := aiService.ListRecipients(ctx, aiassistantport.RecipientPageQuery{PlanID: plan.ID, Limit: 1})
	if err != nil || len(recipients.Items) != 1 || recipients.Items[0].ReviewState != aiassistantport.ReviewPending {
		t.Fatalf("manual recipients=%+v err=%v", recipients, err)
	}
	if _, err = aiService.ReviewRecipient(ctx, aiassistantport.ReviewRecipientCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: staffID}, PlanID: plan.ID, RecipientID: recipients.Items[0].ID, ExpectedVersion: recipients.Items[0].Version, Decision: aiassistantport.ReviewApproved, IdempotencyKey: "joint-runtime-recipient-review-0001"}); err != nil {
		t.Fatal(err)
	}
	if effectsNow := jointEffectCount(t, ctx, native); effectsNow != 0 {
		t.Fatalf("single AI review accepted effects=%d", effectsNow)
	}
	plan, err = aiService.GetPlan(ctx, plan.ID)
	if err != nil || plan.State != aiassistantport.PlanPartiallyApproved {
		t.Fatalf("single-review plan=%+v err=%v", plan, err)
	}
	const sharedLocalKey = "joint-runtime-shared-local-key-0001"
	groupPlan := createRiverJourneyPlan(t, ctx, uow, groupStore, groupActor)
	if groupPlan != int64(plan.ID) {
		t.Fatalf("fixture must exercise equal local plan IDs across sources: group=%d AI=%d", groupPlan, plan.ID)
	}
	unknownPlan := createRiverJourneyPlanWithMessages(t, ctx, uow, groupStore, groupActor, "provider result intentionally unknown", "blocked")
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at) VALUES ('chat-river-1',$1,'Joint one',1,$2,clock_timestamp()),('chat-river-2',$1,'Joint two',1,$3,clock_timestamp())`, groupActor, string(effectport.Hash("joint-directory", "1")), string(effectport.Hash("joint-directory", "2"))); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		plan int64
		key  string
	}{{groupPlan, sharedLocalKey}, {unknownPlan, "joint-runtime-group-unknown-0001"}} {
		if out, e := groupRuntime.AcceptBroadcast(ctx, item.plan, groupActor, item.key); e != nil || out.Accepted != 2 {
			t.Fatalf("group acceptance=%+v err=%v", out, e)
		}
	}
	approval, err := aiService.PreviewApproval(ctx, aiassistantport.PreviewApprovalCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: staffID}, PlanID: plan.ID, ExpectedVersion: plan.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = aiService.ApprovePlan(ctx, aiassistantport.ApprovePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: staffID}, PlanID: plan.ID, ExpectedVersion: plan.Version, PreviewDigest: approval.PreviewDigest, IdempotencyKey: sharedLocalKey}); err != nil {
		t.Fatal(err)
	}
	// This accepted refresh is durable while the only shared runtime is down.
	source.Set(customers)
	if _, err = snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "joint-runtime-entered-0001", ReferenceTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if effectsNow := jointEffectCount(t, ctx, native); effectsNow != 5 {
		t.Fatalf("pre-start effects=%d want group+manual 5", effectsNow)
	}
	var distinctPreStartEffects int
	if err = native.QueryRow(ctx, `SELECT count(DISTINCT envelope_fingerprint) FROM external_effects`).Scan(&distinctPreStartEffects); err != nil || distinctPreStartEffects != 5 {
		t.Fatalf("pre-start distinct effects=%d want 5 err=%v", distinctPreStartEffects, err)
	}
	if automationWeCom.Calls() != 0 || groupWeCom.callCount() != 0 {
		t.Fatal("provider was called before shared runtime start")
	}

	shared, err := platformjobqueue.NewRuntime(native, workers, segment.AudienceRefreshQueue, platformjobqueue.OutboundQueue, platformjobqueue.OutboundMediaQueue)
	if err != nil {
		t.Fatal(err)
	}
	stopShared := automationAudienceStartRuntime(t, shared)
	var stopSharedOnce sync.Once
	stopSharedSafely := func() { stopSharedOnce.Do(stopShared) }
	defer stopSharedSafely()
	automationAudienceEventuallyWithDiagnostics(t, "joint source completions", func() bool {
		return automationWeCom.Calls() == 2 && groupWeCom.callCount() == 4 && jointInitialCompletionsPersisted(ctx, native, policy.ID, plan.ID, groupPlan, unknownPlan, customers[1], staffID)
	}, func() string { return jointRuntimeDiagnostics(ctx, native) })
	// Image and miniprogram thumbnail share one frozen Media source. The file
	// is the only other source, so both Automation and AI messages reuse two
	// generic credentials instead of performing three byte uploads per message.
	if uploads := automationWeCom.Uploads(); uploads != 2 {
		t.Fatalf("frozen mixed-media uploads=%d want=2 unique source credentials", uploads)
	}
	waitGroupOpsDelayedSuccessors(t, native, groupPlan, 2)
	var earliestDelayedDue time.Time
	if err = native.QueryRow(ctx, `SELECT min(scheduled_for) FROM group_ops_executions WHERE plan_id=$1 AND node_position=3 AND state='accepted'`, groupPlan).Scan(&earliestDelayedDue); err != nil || !earliestDelayedDue.After(time.Now().UTC()) {
		t.Fatalf("stored delayed GroupOps due=%s err=%v", earliestDelayedDue, err)
	}
	unknownBeforeRestart := jointUnknownEffectIDs(t, ctx, native)
	if unknownBeforeRestart == "" {
		t.Fatal("response-disconnected GroupOps effects were not retained as unknown")
	}
	stopSharedSafely()
	effectsBeforeReplay := jointEffectCount(t, ctx, native)
	if replay, e := groupRuntime.AcceptBroadcast(ctx, groupPlan, groupActor, sharedLocalKey); e != nil || replay.Run.ID < 1 {
		t.Fatalf("group replay=%+v err=%v", replay, e)
	}
	if _, err = aiService.ApprovePlan(ctx, aiassistantport.ApprovePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: staffID}, PlanID: plan.ID, ExpectedVersion: plan.Version, PreviewDigest: approval.PreviewDigest, IdempotencyKey: sharedLocalKey}); err != nil {
		t.Fatal(err)
	}
	if effectsAfterReplay := jointEffectCount(t, ctx, native); effectsAfterReplay != effectsBeforeReplay {
		t.Fatalf("replayed acceptance minted effects=%d want %d", effectsAfterReplay, effectsBeforeReplay)
	}
	// Restart must not re-send the unknown group effects or the delayed normal nodes.
	restarted, err := platformjobqueue.NewRuntime(native, workers, segment.AudienceRefreshQueue, platformjobqueue.OutboundQueue, platformjobqueue.OutboundMediaQueue)
	if err != nil {
		t.Fatal(err)
	}
	stopRestart := automationAudienceStartRuntime(t, restarted)
	var stopRestartOnce sync.Once
	stopRestartSafely := func() { stopRestartOnce.Do(stopRestart) }
	defer stopRestartSafely()
	time.Sleep(250 * time.Millisecond)
	if automationWeCom.Calls() != 2 || groupWeCom.callCount() != 4 {
		stopRestartSafely()
		t.Fatalf("restart duplicated calls private=%d group=%d", automationWeCom.Calls(), groupWeCom.callCount())
	}
	if unknownAfterRestart := jointUnknownEffectIDs(t, ctx, native); unknownAfterRestart != unknownBeforeRestart {
		stopRestartSafely()
		t.Fatalf("restart changed unknown effects=%q want %q", unknownAfterRestart, unknownBeforeRestart)
	}
	// The preceding assertion proves the persisted GroupOps due is in the
	// future and the restarted runtime does not run it early. Advance only this
	// isolated River job's test clock to exercise its durable successor path.
	if tag, e := native.Exec(ctx, `UPDATE river_job job SET state='available',scheduled_at=clock_timestamp()-interval '1 second' FROM external_effect_jobs effect_job JOIN group_ops_executions execution ON execution.external_effect_id=effect_job.effect_id WHERE job.id=effect_job.river_job_id AND execution.plan_id=$1 AND execution.node_position=3 AND job.state='scheduled'`, groupPlan); e != nil || tag.RowsAffected() != 2 {
		stopRestartSafely()
		t.Fatalf("advance delayed=%d err=%v", tag.RowsAffected(), e)
	}
	waitGroupOpsProviderCalls(t, native, groupWeCom, 6)
	automationAudienceEventually(t, "joint delayed owner completions", func() bool {
		return jointCompletionOwnershipPersisted(ctx, native, policy.ID, manual.ID, plan.ID, groupPlan, customers[0], customers[1], staffID)
	})
	stopRestartSafely()
	var unknown int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE state='outcome_unknown'`).Scan(&unknown); err != nil || unknown != 2 {
		t.Fatalf("unknown effects=%d err=%v", unknown, err)
	}
	if unknownAfterRestart := jointUnknownEffectIDs(t, ctx, native); unknownAfterRestart != unknownBeforeRestart {
		t.Fatalf("delayed successor changed unknown effects=%q want %q", unknownAfterRestart, unknownBeforeRestart)
	}
	jointAssertCompletionOwnership(t, ctx, native, policy.ID, manual.ID, plan.ID, groupPlan, customers[0], customers[1], staffID)
	manualRun, err := runtimeService.Run(ctx, manual.ID)
	if err != nil || manualRun.State != automationport.RunCompleted || manualRun.AIPlanState != string(aiassistantport.PlanCompleted) {
		t.Fatalf("manual run projection=%+v err=%v", manualRun, err)
	}
}

func jointEffectCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func jointUnknownEffectIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var ids string
	if err := pool.QueryRow(ctx, `SELECT coalesce(string_agg(id::text,',' ORDER BY id),'') FROM external_effects WHERE state='outcome_unknown'`).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	return ids
}

func jointRuntimeDiagnostics(ctx context.Context, pool *pgxpool.Pool) string {
	if pool == nil {
		return "pool unavailable"
	}
	var summary []byte
	err := pool.QueryRow(ctx, `SELECT json_build_object(
		'automation_runs',(SELECT coalesce(json_agg(json_build_object('id',id,'state',state,'policy_id',policy_id)),'[]'::json) FROM automation_runs),
		'automation_recipients',(SELECT coalesce(json_agg(json_build_object('run_id',run_id,'state',state,'effect_id',effect_id)),'[]'::json) FROM automation_run_recipients),
		'automation_intents',(SELECT coalesce(json_agg(json_build_object('state',state,'source_kind',source_kind,'effect_id',effect_id)),'[]'::json) FROM outbound_message_intents),
		'ai_plans',(SELECT coalesce(json_agg(json_build_object('id',id,'state',state,'needs_attention_count',needs_attention_count)),'[]'::json) FROM ai_assistant_plans),
		'ai_bindings',(SELECT coalesce(json_agg(json_build_object('state',state,'effect_id',external_effect_id,'provider_accepted',provider_accepted)),'[]'::json) FROM ai_assistant_effect_bindings),
		'group_executions',(SELECT coalesce(json_agg(json_build_object('plan_id',plan_id,'node_position',node_position,'state',state)),'[]'::json) FROM group_ops_executions),
		'effects',(SELECT coalesce(json_agg(json_build_object('id',id,'kind',kind,'state',state)),'[]'::json) FROM external_effects)
	)`).Scan(&summary)
	if err != nil {
		return "diagnostics query: " + err.Error()
	}
	return string(summary)
}

func jointInitialCompletionsPersisted(ctx context.Context, pool *pgxpool.Pool, policyID int64, manualPlanID aiassistantport.PlanID, groupPlanID, unknownPlanID int64, automaticCustomer customerdomain.CustomerID, staffID int64) bool {
	var automatic, manual, regularGroup, unknownGroup int
	if pool.QueryRow(ctx, `SELECT count(*)
		FROM automation_runs run
		JOIN automation_run_recipients recipient ON recipient.run_id=run.id
		JOIN outbound_message_intents intent ON intent.run_recipient_id=recipient.id
		JOIN external_effects effect ON ('eer_'||effect.id::text)=intent.effect_id
		WHERE run.policy_id=$1 AND run.state='executing' AND recipient.customer_id=$2 AND recipient.sender_staff_id=$3
		  AND recipient.state='provider_accepted' AND intent.source_kind='automation_enrollment' AND intent.state='provider_accepted'
		  AND intent.receipt_digest IS NOT NULL AND effect.kind='automation_message' AND effect.state='executed'`, policyID, automaticCustomer, staffID).Scan(&automatic) != nil || automatic != 1 {
		return false
	}
	if pool.QueryRow(ctx, `SELECT count(*)
		FROM ai_assistant_plans plan
		JOIN ai_assistant_plan_recipients recipient ON recipient.plan_id=plan.id
		JOIN ai_assistant_effect_bindings binding ON binding.recipient_id=recipient.id
		JOIN outbound_private_message_intents intent ON intent.id=binding.outbound_intent_id AND intent.external_effect_id=binding.external_effect_id
		JOIN external_effects effect ON ('eer_'||effect.id::text)=binding.external_effect_id
		WHERE plan.id=$1 AND plan.state='completed' AND recipient.execution_state='provider_accepted'
		  AND binding.state='provider_accepted' AND binding.provider_receipt_digest IS NOT NULL
		  AND intent.state='provider_accepted' AND effect.kind='outbound_message' AND effect.state='executed'`, manualPlanID).Scan(&manual) != nil || manual != 1 {
		return false
	}
	if pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE execution.plan_id=$1 AND execution.state='provider_accepted' AND execution.provider_receipt_digest IS NOT NULL AND effect.state='executed'),
		count(*) FILTER (WHERE execution.plan_id=$2 AND execution.state='outcome_unknown' AND effect.state='outcome_unknown')
		FROM group_ops_executions execution
		JOIN external_effects effect ON effect.id=execution.external_effect_id
		WHERE execution.plan_id IN ($1,$2) AND execution.node_position=1`, groupPlanID, unknownPlanID).Scan(&regularGroup, &unknownGroup) != nil {
		return false
	}
	return regularGroup == 2 && unknownGroup == 2
}

func jointCompletionOwnershipPersisted(ctx context.Context, pool *pgxpool.Pool, policyID, manualRunID int64, manualPlanID aiassistantport.PlanID, groupPlanID int64, manualCustomer, automaticCustomer customerdomain.CustomerID, staffID int64) bool {
	var automatic, manual, group int
	if pool.QueryRow(ctx, `SELECT count(*)
		FROM automation_runs run
		JOIN automation_run_recipients recipient ON recipient.run_id=run.id
		JOIN outbound_message_intents intent ON intent.run_recipient_id=recipient.id
		JOIN external_effects effect ON ('eer_'||effect.id::text)=intent.effect_id
		WHERE run.policy_id=$1 AND run.state='executing' AND recipient.customer_id=$2 AND recipient.sender_staff_id=$3
		  AND recipient.state='provider_accepted' AND intent.source_kind='automation_enrollment' AND intent.state='provider_accepted'
		  AND intent.receipt_digest IS NOT NULL AND effect.kind='automation_message' AND effect.state='executed'`, policyID, automaticCustomer, staffID).Scan(&automatic) != nil || automatic != 1 {
		return false
	}
	if pool.QueryRow(ctx, `SELECT count(*)
		FROM automation_runs run
		JOIN ai_assistant_plans plan ON plan.id=run.ai_plan_id
		JOIN ai_assistant_plan_recipients recipient ON recipient.plan_id=plan.id
		JOIN ai_assistant_effect_bindings binding ON binding.recipient_id=recipient.id
		JOIN outbound_private_message_intents intent ON intent.id=binding.outbound_intent_id AND intent.external_effect_id=binding.external_effect_id
		JOIN external_effects effect ON ('eer_'||effect.id::text)=binding.external_effect_id
		WHERE run.id=$1 AND plan.id=$2 AND plan.state='completed' AND recipient.customer_id=$3 AND recipient.staff_id=$4
		  AND recipient.execution_state='provider_accepted' AND binding.state='provider_accepted' AND binding.provider_receipt_digest IS NOT NULL
		  AND intent.state='provider_accepted' AND effect.kind='outbound_message' AND effect.state='executed'`, manualRunID, manualPlanID, manualCustomer, staffID).Scan(&manual) != nil || manual != 1 {
		return false
	}
	if pool.QueryRow(ctx, `SELECT count(*)
		FROM group_ops_executions execution
		JOIN external_effects effect ON effect.id=execution.external_effect_id
		WHERE execution.plan_id=$1 AND execution.state='provider_accepted' AND execution.provider_receipt_digest IS NOT NULL
		  AND effect.kind='group_message' AND effect.state='executed'`, groupPlanID).Scan(&group) != nil || group != 4 {
		return false
	}
	return true
}

func jointAssertCompletionOwnership(t *testing.T, ctx context.Context, pool *pgxpool.Pool, policyID, manualRunID int64, manualPlanID aiassistantport.PlanID, groupPlanID int64, manualCustomer, automaticCustomer customerdomain.CustomerID, staffID int64) {
	t.Helper()
	var automatic, manual, group int
	if err := pool.QueryRow(ctx, `SELECT count(*)
		FROM automation_runs run
		JOIN automation_run_recipients recipient ON recipient.run_id=run.id
		JOIN outbound_message_intents intent ON intent.run_recipient_id=recipient.id
		JOIN external_effects effect ON ('eer_'||effect.id::text)=intent.effect_id
		WHERE run.policy_id=$1 AND run.state='executing' AND recipient.customer_id=$2 AND recipient.sender_staff_id=$3
		  AND recipient.state='provider_accepted' AND intent.source_kind='automation_enrollment' AND intent.state='provider_accepted'
		  AND intent.receipt_digest IS NOT NULL AND effect.kind='automation_message' AND effect.state='executed'`, policyID, automaticCustomer, staffID).Scan(&automatic); err != nil || automatic != 1 {
		t.Fatalf("automatic owner->intent->effect=%d want 1 err=%v", automatic, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)
		FROM automation_runs run
		JOIN ai_assistant_plans plan ON plan.id=run.ai_plan_id
		JOIN ai_assistant_plan_recipients recipient ON recipient.plan_id=plan.id
		JOIN ai_assistant_effect_bindings binding ON binding.recipient_id=recipient.id
		JOIN outbound_private_message_intents intent ON intent.id=binding.outbound_intent_id AND intent.external_effect_id=binding.external_effect_id
		JOIN external_effects effect ON ('eer_'||effect.id::text)=binding.external_effect_id
		WHERE run.id=$1 AND plan.id=$2 AND plan.state='completed' AND recipient.customer_id=$3 AND recipient.staff_id=$4
		  AND recipient.execution_state='provider_accepted' AND binding.state='provider_accepted'
		  AND binding.provider_receipt_digest IS NOT NULL AND intent.state='provider_accepted' AND effect.kind='outbound_message' AND effect.state='executed'`, manualRunID, manualPlanID, manualCustomer, staffID).Scan(&manual); err != nil || manual != 1 {
		t.Fatalf("manual run->AI plan->intent->effect=%d want 1 err=%v", manual, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)
		FROM group_ops_executions execution
		JOIN external_effects effect ON effect.id=execution.external_effect_id
		WHERE execution.plan_id=$1 AND execution.state='provider_accepted' AND execution.provider_receipt_digest IS NOT NULL
		  AND effect.kind='group_message' AND effect.state='executed'`, groupPlanID).Scan(&group); err != nil || group != 4 {
		t.Fatalf("GroupOps execution->effect receipts=%d want 4 err=%v", group, err)
	}
}
