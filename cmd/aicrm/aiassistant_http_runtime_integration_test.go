package main

// This Journey deliberately enters through the signed HTTP contract and lets
// River execute the persisted effect. It does not call RunAttempt directly.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistanthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/http"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func TestAIAssistantSignedHTTPRiverWeComJourney(t *testing.T) {
	native, cleanup := aiAssistantHTTPJourneyPool(t)
	defer cleanup()
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

	aiStore, err := aiassistantstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	accessRepository := accessstore.NewPostgreSQL()
	identities := identityquery.NewPostgreSQL()
	service, err := aiassistantapp.NewService(uow, aiStore, journeyCustomerReader{}, aiStaffSnapshotAdapter{repository: accessRepository}, journeyTextMaterials{}, identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, identities)
	if err != nil {
		t.Fatal(err)
	}

	var providerCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "journey-token", "expires_in": 7200})
		case "/cgi-bin/externalcontact/add_msg_template":
			providerCalls.Add(1)
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("provider request: %v", err)
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			if request["sender"] != "staff-1" {
				t.Errorf("unexpected provider sender: %#v", request)
			}
			if text, _ := request["text"].(map[string]any); text != nil && text["content"] == "provider result intentionally unknown" {
				connection, _, hijackErr := w.(http.Hijacker).Hijack()
				if hijackErr != nil {
					t.Errorf("hijack provider response: %v", hijackErr)
				} else {
					_ = connection.Close()
				}
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "msgid": "wecom-msg-journey-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	wecomClient, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "corp-1", ContactSecret: "journey-contact-secret", APIBase: providerServer.URL, HTTPClient: providerServer.Client()})
	if err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	effectsModule := externaleffects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	effectClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, effectClient)
	if err != nil {
		t.Fatal(err)
	}
	privateWriter, err := outbound.NewPrivateMessageRepository(native, effects)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindOutbound(privateWriter, true); err != nil {
		t.Fatal(err)
	}
	if err = service.BindReconciler(effects); err != nil {
		t.Fatal(err)
	}
	privateProvider, err := outbound.NewPrivateMessageProvider(true, privateWriter, aiPrivateTargetResolver{uow: uow, identities: identities, access: accessRepository, relationships: wecom.NewPostgreSQLFollowRelationshipStore(), corpID: "corp-1"}, aiPrivatePayloadReader{content: aiStore}, wecomClient)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectsModule.SetProviderAdapter(outbound.NewProviderRouterWithPrivate(nil, nil, privateProvider)); err != nil {
		t.Fatal(err)
	}
	completion, err := outbound.NewPrivateMessageCompletionSink(privateWriter, aiStore)
	if err != nil {
		t.Fatal(err)
	}
	router, err := outbound.NewCompletionRouterWithPrivate(nil, nil, completion)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(router); err != nil {
		t.Fatal(err)
	}
	if _, err = effectsModule.Bind(effects, journeyHTTPSecurity{}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	handler, err := aiassistanthttp.NewHandler(aiassistanthttp.Config{Application: service, Security: journeyHTTPSecurity{}, Authorizer: accessapp.AIAssistantAuthorizer{}, Integration: aiassistanthttp.IntegrationConfig{Enabled: true, Key: "journey", Secret: "01234567890123456789012345678901", ActorID: 9, WeComCorpID: "corp-1", MaxSkew: 5 * time.Minute}, DispatchReady: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()

	intake := map[string]any{"external_userid": "external-1", "owner_userid": "staff-1", "content_package": map[string]any{"content_text": "first approved wording"}, "external_event_id": "legacy-event-journey-1", "operator": "metadata-only"}
	created := journeySignedJSON(t, routes, now, "01234567890123456789012345678901", "nonce-journey-0001", "signed-intake-key-0001", intake)
	if created.Code != http.StatusAccepted {
		t.Fatalf("intake code=%d body=%s", created.Code, created.Body.String())
	}
	var createBody struct {
		Plan struct {
			ID int64 `json:"id"`
		} `json:"plan"`
		Resolution map[string]int `json:"resolution"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &createBody); err != nil {
		t.Fatal(err)
	}
	if createBody.Plan.ID < 1 || createBody.Resolution["found"] != 1 {
		t.Fatalf("intake=%s", created.Body.String())
	}
	// A fresh authenticated nonce for the same frozen donor event returns the
	// same plan rather than letting request authentication alter business identity.
	replayed := journeySignedJSON(t, routes, now, "01234567890123456789012345678901", "nonce-journey-0002", "signed-intake-key-0002", intake)
	if replayed.Code != http.StatusAccepted || !bytes.Contains(replayed.Body.Bytes(), []byte(`"replayed":true`)) {
		t.Fatalf("intake replay=%d %s", replayed.Code, replayed.Body.String())
	}

	list := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/recipients?limit=50", "", nil)
	var recipients struct {
		Items []struct {
			ID      int64 `json:"id"`
			Version int64 `json:"version"`
		} `json:"items"`
	}
	if err = json.Unmarshal(list.Body.Bytes(), &recipients); err != nil || len(recipients.Items) != 1 {
		t.Fatalf("list=%s err=%v", list.Body.String(), err)
	}
	recipientID := recipients.Items[0].ID
	oldPreview := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/preview-approval", "preview-before-edit-0001", map[string]any{"expected_version": 1})
	var preview struct {
		PreviewDigest effectport.Digest `json:"preview_digest"`
	}
	if err = json.Unmarshal(oldPreview.Body.Bytes(), &preview); err != nil || preview.PreviewDigest == "" {
		t.Fatalf("preview=%s err=%v", oldPreview.Body.String(), err)
	}
	updated := journeyAdminJSON(t, routes, http.MethodPatch, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/recipients/"+itoa(recipientID)+"/content", "content-edit-journey-0001", map[string]any{"expected_version": recipients.Items[0].Version, "blocks": []map[string]any{{"kind": "text", "text": "edited wording needs review"}}})
	if updated.Code != http.StatusOK {
		t.Fatalf("edit=%d %s", updated.Code, updated.Body.String())
	}
	staleApprove := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/approve", "approve-stale-journey-0001", map[string]any{"expected_version": 1, "preview_digest": preview.PreviewDigest})
	if staleApprove.Code != http.StatusConflict {
		t.Fatalf("stale approval=%d %s", staleApprove.Code, staleApprove.Body.String())
	}

	detail := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/recipients/"+itoa(recipientID), "", nil)
	var current struct {
		Recipient struct {
			Version int64 `json:"version"`
		} `json:"recipient"`
		Content struct {
			Blocks []struct {
				Text string `json:"text"`
			} `json:"blocks"`
		} `json:"content"`
	}
	if err = json.Unmarshal(detail.Body.Bytes(), &current); err != nil || len(current.Content.Blocks) != 1 || current.Content.Blocks[0].Text != "edited wording needs review" {
		t.Fatalf("detail=%s err=%v", detail.Body.String(), err)
	}
	review := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/recipients/"+itoa(recipientID)+"/review", "review-journey-0001", map[string]any{"expected_version": current.Recipient.Version, "decision": "approved"})
	if review.Code != http.StatusOK || providerCalls.Load() != 0 {
		t.Fatalf("recipient review=%d calls=%d body=%s", review.Code, providerCalls.Load(), review.Body.String())
	}

	plan := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID), "", nil)
	var planBody struct {
		Plan struct {
			Version int64 `json:"version"`
		} `json:"plan"`
	}
	if err = json.Unmarshal(plan.Body.Bytes(), &planBody); err != nil {
		t.Fatal(err)
	}
	freshPreview := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/preview-approval", "preview-after-review-0001", map[string]any{"expected_version": planBody.Plan.Version})
	if err = json.Unmarshal(freshPreview.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	approved := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/approve", "approve-journey-0001", map[string]any{"expected_version": planBody.Plan.Version, "preview_digest": preview.PreviewDigest})
	if approved.Code != http.StatusOK || providerCalls.Load() != 0 {
		t.Fatalf("approve=%d calls=%d body=%s", approved.Code, providerCalls.Load(), approved.Body.String())
	}
	// The effect has committed before any provider call. Starting a new River
	// runtime afterwards models a process restart recovering the durable job.
	runtimeService, err := platformjobqueue.NewRuntime(native, workers, platformjobqueue.OutboundQueue)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtimeService.Run(runCtx) }()
	deadline := time.Now().Add(12 * time.Second)
	for providerCalls.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	stop()
	if runErr := <-done; runErr != nil {
		t.Fatal(runErr)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("River did not execute actual WeCom leaf; calls=%d", providerCalls.Load())
	}
	effectsResponse := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/effects", "", nil)
	if !bytes.Contains(effectsResponse.Body.Bytes(), []byte(`"provider_accepted"`)) {
		t.Fatalf("effect receipt=%s", effectsResponse.Body.String())
	}
	approveReplay := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(createBody.Plan.ID)+"/approve", "approve-journey-0001", map[string]any{"expected_version": planBody.Plan.Version, "preview_digest": preview.PreviewDigest})
	if approveReplay.Code != http.StatusOK || providerCalls.Load() != 1 {
		t.Fatalf("approval replay=%d calls=%d", approveReplay.Code, providerCalls.Load())
	}

	// A transport interruption after the WeCom leaf receives the request is an
	// unknown outcome. It remains attached to its original EER key and a replay
	// of plan approval cannot mint a replacement effect.
	unknownIntake := map[string]any{"external_userid": "external-1", "owner_userid": "staff-1", "content_package": map[string]any{"content_text": "provider result intentionally unknown"}, "external_event_id": "legacy-event-journey-unknown"}
	unknownCreated := journeySignedJSON(t, routes, now, "01234567890123456789012345678901", "nonce-journey-unknown-01", "signed-intake-key-unknown-01", unknownIntake)
	var unknownCreate struct {
		Plan struct {
			ID int64 `json:"id"`
		} `json:"plan"`
	}
	if err = json.Unmarshal(unknownCreated.Body.Bytes(), &unknownCreate); err != nil || unknownCreate.Plan.ID < 1 {
		t.Fatalf("unknown intake=%s err=%v", unknownCreated.Body.String(), err)
	}
	unknownList := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(unknownCreate.Plan.ID)+"/recipients?limit=50", "", nil)
	var unknownRecipients struct {
		Items []struct {
			ID      int64 `json:"id"`
			Version int64 `json:"version"`
		} `json:"items"`
	}
	if err = json.Unmarshal(unknownList.Body.Bytes(), &unknownRecipients); err != nil || len(unknownRecipients.Items) != 1 {
		t.Fatalf("unknown recipients=%s err=%v", unknownList.Body.String(), err)
	}
	unknownRecipient := unknownRecipients.Items[0]
	if response := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(unknownCreate.Plan.ID)+"/recipients/"+itoa(unknownRecipient.ID)+"/review", "review-journey-unknown-01", map[string]any{"expected_version": unknownRecipient.Version, "decision": "approved"}); response.Code != http.StatusOK {
		t.Fatalf("unknown review=%d %s", response.Code, response.Body.String())
	}
	unknownPlan := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(unknownCreate.Plan.ID), "", nil)
	if err = json.Unmarshal(unknownPlan.Body.Bytes(), &planBody); err != nil {
		t.Fatal(err)
	}
	unknownPreview := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(unknownCreate.Plan.ID)+"/preview-approval", "preview-unknown-journey-01", map[string]any{"expected_version": planBody.Plan.Version})
	if err = json.Unmarshal(unknownPreview.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	unknownApprove := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(unknownCreate.Plan.ID)+"/approve", "approve-journey-unknown-01", map[string]any{"expected_version": planBody.Plan.Version, "preview_digest": preview.PreviewDigest})
	if unknownApprove.Code != http.StatusOK {
		t.Fatalf("unknown approve=%d %s", unknownApprove.Code, unknownApprove.Body.String())
	}
	unknownRuntime, err := platformjobqueue.NewRuntime(native, workers, platformjobqueue.OutboundQueue)
	if err != nil {
		t.Fatal(err)
	}
	unknownRunCtx, unknownStop := context.WithCancel(context.Background())
	unknownDone := make(chan error, 1)
	go func() { unknownDone <- unknownRuntime.Run(unknownRunCtx) }()
	unknownDeadline := time.Now().Add(12 * time.Second)
	for providerCalls.Load() != 2 && time.Now().Before(unknownDeadline) {
		time.Sleep(25 * time.Millisecond)
	}
	unknownStop()
	if runErr := <-unknownDone; runErr != nil {
		t.Fatal(runErr)
	}
	if providerCalls.Load() != 2 {
		t.Fatalf("unknown WeCom leaf call count=%d", providerCalls.Load())
	}
	unknownEffects := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(unknownCreate.Plan.ID)+"/effects", "", nil)
	if !bytes.Contains(unknownEffects.Body.Bytes(), []byte(`"outcome_unknown"`)) {
		t.Fatalf("unknown effect=%s", unknownEffects.Body.String())
	}
	unknownReplay := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(unknownCreate.Plan.ID)+"/approve", "approve-journey-unknown-01", map[string]any{"expected_version": planBody.Plan.Version, "preview_digest": preview.PreviewDigest})
	if unknownReplay.Code != http.StatusOK || providerCalls.Load() != 2 {
		t.Fatalf("unknown approval replay=%d calls=%d", unknownReplay.Code, providerCalls.Load())
	}
}

type journeyCustomerReader struct{}

func (journeyCustomerReader) CustomerSnapshot(_ context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	return aiassistantapp.CustomerSnapshot{CanonicalID: id, Status: customerdomain.StatusActive, DisplayName: "Journey customer", OneIDLabel: "OneID #91"}, nil
}

type journeyTextMaterials struct{}

func (journeyTextMaterials) ResolveMaterial(_ context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	return block, nil
}
func (journeyTextMaterials) RegisterMaterialReference(context.Context, aiassistantport.ContentBlock, effectport.Digest) error {
	return nil
}

type journeyHTTPSecurity struct{}

func (journeyHTTPSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, nil
}
func (journeyHTTPSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return journeyHTTPSecurity{}.Authenticate(context.Background(), nil)
}

func journeySignedJSON(t *testing.T, routes http.Handler, at time.Time, secret, nonce, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	timestamp := itoa(at.Unix())
	message := timestamp + "\n" + nonce + "\n" + key + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/ai-assistant/review-plans", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-AICRM-Integration-Key", "journey")
	req.Header.Set("X-AICRM-Nonce", nonce)
	req.Header.Set("X-AICRM-Timestamp", timestamp)
	req.Header.Set("X-AICRM-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, req)
	return response
}
func journeyAdminJSON(t *testing.T, routes http.Handler, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("X-CSRF-Token", "journey-csrf")
	}
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, req)
	return response
}
func seedAIAssistantHTTPJourney(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(91,'active')`,
		`INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at) VALUES(91,'wecom_external_userid','wecom-corp:corp-1','external-1','verified','journey',1,'active',clock_timestamp())`,
		`INSERT INTO admin_users(id,username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at) OVERRIDING SYSTEM VALUE VALUES(9,'journey-admin','$argon2id$journey','Journey Admin','staff-1',true,true,clock_timestamp())`,
		`INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES(9,'super_admin')`,
		`INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('corp-1','staff-1',91,true)`,
	} {
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
}

func aiAssistantHTTPJourneyPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping AI Assistant signed HTTP River journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_ai_http_journey_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0007_media.sql", "0036_ai_assistant_review.sql", "0037_outbound_private_messages.sql", "0100_ai_assistant_machine_actor.sql", "0120_excel_batches.sql", "0121_excel_delivery_receipts.sql", "0124_operation_excel_batch_lifecycle.sql", "0125_outbound_material_preparation.sql", "0126_media_material_source_snapshots.sql"} {
		if err = applyAIAssistantHTTPJourneyMigration(ctx, pool, name); err != nil {
			t.Fatal(err)
		}
	}
	if err = ensureAccessLoginFixtureSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(closeCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(closeCtx)
	}
}
func applyAIAssistantHTTPJourneyMigration(ctx context.Context, pool *pgxpool.Pool, name string) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, string(raw))
	return err
}
