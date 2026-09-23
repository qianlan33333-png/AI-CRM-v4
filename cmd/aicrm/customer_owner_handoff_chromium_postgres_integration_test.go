package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

// TestPostgreSQLOwnerHandoffComposedTransferResultHTTP verifies the fully
// composed outer route binds Customer's read-only transfer-result port to the
// WeCom client. It deliberately runs without Chromium, so a Darwin browser
// sandbox skip cannot conceal a missing Composition Root dependency.
func TestPostgreSQLOwnerHandoffComposedTransferResultHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	var transferResultCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "access_token": "owner-handoff-reader-token", "expires_in": 7200})
		case "/cgi-bin/externalcontact/transfer_result":
			transferResultCalls.Add(1)
			var body struct {
				Source string `json:"handover_userid"`
				Target string `json:"takeover_userid"`
				Cursor string `json:"cursor"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "invalid transfer result request", http.StatusBadRequest)
				return
			}
			if body.Source != "reader-source" || body.Target != "reader-target" || body.Cursor != "" {
				http.Error(writer, "unexpected frozen transfer result request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "customer": []map[string]any{{"external_userid": "reader-external", "status": 1, "takeover_time": 1}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer providerServer.Close()
	application, err := composeWithWeComClientFactory(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://owner-handoff-reader.test",
		ReleaseSHA:   "owner-handoff-reader",
		WorkerOwner:  "owner-handoff-reader",
		WorkerLimit:  1,
		Effects:      platformconfig.Effects{ProviderEnabled: true},
		WeCom:        platformconfig.WeCom{Enabled: true, CorpID: "reader-corp", AgentID: "reader-agent", Secret: "reader-secret", ContactSecret: "reader-contact-secret", ContextSigningKey: "01234567890123456789012345678901"},
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "owner-handoff-reader-webhook"},
		Survey:       platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)},
		Bootstrap:    platformconfig.Bootstrap{Enabled: true, Username: "owner-reader", Password: "owner-reader-password", DisplayName: "Owner Reader"},
	}, func(config wecomadapter.Config) (*wecomadapter.Client, error) {
		config.APIBase = providerServer.URL
		config.HTTPClient = providerServer.Client()
		return wecomadapter.New(config)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "owner-reader", Password: "owner-reader-password", DisplayName: "Owner Reader"}); err != nil {
		t.Fatal(err)
	}
	var actorID, targetID, customerID int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE username='owner-reader'`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `WITH account AS (INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at) VALUES('owner-reader-target','$argon2id$fixture','Reader Target','reader-target',true,true,clock_timestamp()) RETURNING id), role AS (INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account) SELECT id FROM account`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	cipher, err := customer.NewOwnerHandoffCipher(base64.RawStdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	store := customer.NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	var batch customerport.OwnerHandoffBatch
	if err = uow.Within(ctx, func(txctx context.Context) error {
		digest := [32]byte{1}
		record := customerport.OwnerHandoffPreviewRecord{
			ActorAdminUserID: actorID,
			Preview:          customerport.OwnerHandoffPreview{ID: "owner-handoff-reader-preview", Mode: customerport.OwnerHandoffWeComThenCRM, SourceStaffID: actorID, TargetStaffID: targetID, CorpScope: "wecom-corp:reader-corp", ConfirmationPhrase: "CONFIRM", ExpiresAt: time.Now().Add(time.Hour)},
			Candidates:       []customerport.OwnerHandoffCandidate{{CustomerID: customerdomain.CustomerID(customerID), State: "ready", RelationshipDigest: [32]byte{2}, SourceUserID: "reader-source", TargetUserID: "reader-target", ExternalUserID: "reader-external"}},
			RequestDigest:    digest,
		}
		if _, createErr := store.CreateOwnerHandoffPreview(txctx, record); createErr != nil {
			return createErr
		}
		var createErr error
		batch, createErr = store.CreateWeComOwnerHandoffBatch(txctx, customerport.OwnerHandoffBatchRecord{Preview: record, ActorID: actorID, Idempotency: "owner-handoff-reader-confirm", RequestDigest: digest, Lines: []customerport.OwnerHandoffLine{{Line: 1, CustomerID: customerdomain.CustomerID(customerID), State: "provider_accepted"}}})
		if createErr != nil {
			return createErr
		}
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		_, txErr = tx.Exec(txctx, `UPDATE customer_owner_handoff_lines SET state='provider_accepted' WHERE batch_id=$1 AND line_no=1`, batch.ID)
		return txErr
	}); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "owner-reader", "owner-reader-password")
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/admin/customers/owner-handoffs/batches/"+batch.ID+"/transfer-result", strings.NewReader(`{"idempotency_key":"owner-handoff-reader-result"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: session})
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_csrf", Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("composed transfer-result status=%d body=%s", response.Code, response.Body.String())
	}
	var observed customerport.OwnerHandoffBatch
	if err = json.NewDecoder(response.Body).Decode(&observed); err != nil {
		t.Fatal(err)
	}
	if transferResultCalls.Load() != 1 || len(observed.Lines) != 1 || observed.Lines[0].State != "observed" || observed.Lines[0].TransferStatus != 1 {
		t.Fatalf("transfer-result calls=%d batch=%+v", transferResultCalls.Load(), observed)
	}
	var state string
	var transferStatus int
	if err = application.pool.Native().QueryRow(ctx, `SELECT state,COALESCE(transfer_status,0) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND line_no=1`, batch.ID).Scan(&state, &transferStatus); err != nil || state != "observed" || transferStatus != 1 {
		t.Fatalf("transfer-result projection state=%q status=%d err=%v", state, transferStatus, err)
	}
}

// TestPostgreSQLOwnerHandoffComposedExecutionUsesCustomerUOW fixes the
// Composition seam: the EER worker invokes outbound outside a transaction, so
// its frozen Customer execution must pass through customerOwnerHandoffExecutionAdapter.
// This runs the real outer preview/confirm HTTP flow and River provider call
// without a browser; the Chromium journey separately verifies page behavior.
func TestPostgreSQLOwnerHandoffComposedExecutionUsesCustomerUOW(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	var transferCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "access_token": "owner-handoff-execution-token", "expires_in": 7200})
		case "/cgi-bin/externalcontact/transfer_customer":
			transferCalls.Add(1)
			var body struct {
				Source   string   `json:"handover_userid"`
				Target   string   `json:"takeover_userid"`
				External []string `json:"external_userid"`
				Welcome  string   `json:"transfer_success_msg"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Source != "execution-source" || body.Target != "execution-target" || len(body.External) != 1 || body.External[0] != "execution-external" || body.Welcome != "execution welcome" {
				http.Error(writer, "unexpected frozen transfer request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "customer": []map[string]any{{"external_userid": body.External[0], "errcode": 0}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer providerServer.Close()
	application, err := composeWithWeComClientFactory(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://owner-handoff-execution.test",
		ReleaseSHA:   "owner-handoff-execution",
		WorkerOwner:  "owner-handoff-execution",
		WorkerLimit:  1,
		Effects:      platformconfig.Effects{ProviderEnabled: true},
		WeCom:        platformconfig.WeCom{Enabled: true, CorpID: "execution-corp", AgentID: "execution-agent", Secret: "execution-secret", ContactSecret: "execution-contact-secret", ContextSigningKey: "01234567890123456789012345678901"},
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "owner-handoff-execution-webhook"},
		Survey:       platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)},
		Bootstrap:    platformconfig.Bootstrap{Enabled: true, Username: "owner-execution", Password: "owner-execution-password", DisplayName: "Owner Execution"},
	}, func(config wecomadapter.Config) (*wecomadapter.Client, error) {
		config.APIBase = providerServer.URL
		config.HTTPClient = providerServer.Client()
		return wecomadapter.New(config)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "owner-execution", Password: "owner-execution-password", DisplayName: "Owner Execution"}); err != nil {
		t.Fatal(err)
	}
	var sourceID, targetID, customerID int64
	if err = application.pool.Native().QueryRow(ctx, `WITH account AS (INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('owner-execution-source','$argon2id$fixture','Execution Source','execution-source',false,false) RETURNING id), role AS (INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account) SELECT id FROM account`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `WITH account AS (INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at) VALUES('owner-execution-target','$argon2id$fixture','Execution Target','execution-target',true,true,clock_timestamp()) RETURNING id), role AS (INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account) SELECT id FROM account`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('execution-corp','execution-source',$1,true)`, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:execution-corp','execution-external','verified','execution_fixture',1,clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "owner-execution", "owner-execution-password")
	requestJSON := func(path string, value any) *httptest.ResponseRecorder {
		body, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: session})
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_csrf", Value: csrf})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		return response
	}
	previewResponse := requestJSON("/api/admin/customers/owner-handoffs/previews", map[string]any{
		"mode": "wecom_then_crm", "scope": "excel_include", "source_staff_id": sourceID, "target_staff_id": targetID,
		"customer_ids": []int64{}, "external_userids": []string{"execution-external"}, "welcome_message": "execution welcome",
		"confirmation_phrase": "EXECUTION CONFIRM", "idempotency_key": "owner-handoff-execution-preview-001",
	})
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("composed preview status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview customerport.OwnerHandoffPreview
	if err = json.NewDecoder(previewResponse.Body).Decode(&preview); err != nil || len(preview.Rows) != 1 {
		t.Fatalf("composed preview=%+v err=%v", preview, err)
	}
	confirmResponse := requestJSON("/api/admin/customers/owner-handoffs/confirm", map[string]any{
		"preview_id": preview.ID, "preview_hash": preview.Hash, "confirmation_phrase": "EXECUTION CONFIRM", "idempotency_key": "owner-handoff-execution-confirm-001",
	})
	if confirmResponse.Code != http.StatusAccepted {
		t.Fatalf("composed confirm status=%d body=%s", confirmResponse.Code, confirmResponse.Body.String())
	}
	var batch customerport.OwnerHandoffBatch
	if err = json.NewDecoder(confirmResponse.Body).Decode(&batch); err != nil || batch.ID == "" {
		t.Fatalf("composed batch=%+v err=%v", batch, err)
	}
	effectsCtx, stopEffects := context.WithCancel(ctx)
	effectsDone := make(chan error, 1)
	go func() { effectsDone <- application.effectsRuntime.Run(effectsCtx) }()
	defer func() {
		stopEffects()
		if runtimeErr := <-effectsDone; runtimeErr != nil && !errors.Is(runtimeErr, context.Canceled) {
			t.Errorf("owner handoff effects runtime: %v", runtimeErr)
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		var owner int64
		err = application.pool.Native().QueryRow(ctx, `SELECT line.state,local.staff_id FROM customer_owner_handoff_lines line LEFT JOIN customer_local_owners local ON local.customer_id=line.customer_id WHERE line.batch_id=$1 AND line.line_no=1`, batch.ID).Scan(&state, &owner)
		if err == nil && state == "provider_accepted" && owner == targetID && transferCalls.Load() == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var state, effectState string
	_ = application.pool.Native().QueryRow(ctx, `SELECT line.state,COALESCE(effect.state,'') FROM customer_owner_handoff_lines line LEFT JOIN external_effects effect ON effect.id=regexp_replace(line.effect_id,'^eer_','')::bigint WHERE line.batch_id=$1 AND line.line_no=1`, batch.ID).Scan(&state, &effectState)
	t.Fatalf("composed EER did not complete state=%q effect_state=%q provider_calls=%d", state, effectState, transferCalls.Load())
}

// TestPostgreSQLOwnerHandoffJourneyFinalCountsQueryUsesAliases executes the
// exact final Chromium-journey query against PostgreSQL without a browser. Both
// handoff tables expose mode/state fields, so every predicate must name its
// table alias to avoid hiding this regression behind a Darwin Chromium skip.
func TestPostgreSQLOwnerHandoffJourneyFinalCountsQueryUsesAliases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 4, MinConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actor, source, target int64
	seed, err := pool.Native().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Rollback(ctx)
	if err = seed.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,login_enabled,access_granted_at) VALUES('final-count-actor','$argon2id$fixture','Final Count Actor',true,true,clock_timestamp()) RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err = seed.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,login_enabled,access_granted_at) VALUES('final-count-source','$argon2id$fixture','Final Count Source',true,clock_timestamp()) RETURNING id`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err = seed.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,login_enabled,access_granted_at) VALUES('final-count-target','$argon2id$fixture','Final Count Target',true,clock_timestamp()) RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES($1,'super_admin'),($2,'viewer'),($3,'viewer')`, actor, source, target); err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(ctx, `INSERT INTO access_super_admin_control(singleton,admin_user_id,version,updated_at) VALUES(TRUE,$1,1,clock_timestamp())`, actor); err != nil {
		t.Fatal(err)
	}
	if err = seed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var localCustomer, acceptedCustomer, observedCustomer int64
	for _, customerID := range []*int64{&localCustomer, &acceptedCustomer, &observedCustomer} {
		if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(customerID); err != nil {
			t.Fatal(err)
		}
	}
	digest := make([]byte, 32)
	for _, fixture := range []struct {
		preview, batch, key, mode string
	}{
		{preview: "final-count-local-preview", batch: "final-count-local-batch", key: "final-count-local-key", mode: "local_only"},
		{preview: "final-count-wecom-preview", batch: "final-count-wecom-batch", key: "final-count-wecom-key", mode: "wecom_then_crm"},
	} {
		if _, err = pool.Native().Exec(ctx, `INSERT INTO customer_owner_handoff_previews(id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at) VALUES($1,$2,$3,$4,$5,'wecom-corp:final-count',$6,'FINAL COUNT',clock_timestamp()+interval '1 hour')`, fixture.preview, actor, fixture.mode, source, target, digest); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Native().Exec(ctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'wecom-corp:final-count','completed')`, fixture.batch, fixture.preview, actor, fixture.key, digest, fixture.mode, source, target); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct {
		batch, mode, state string
		customer           int64
		transferStatus     *int
	}{
		{batch: "final-count-local-batch", mode: "local_only", state: "local_updated", customer: localCustomer},
		{batch: "final-count-wecom-batch", mode: "wecom_then_crm", state: "provider_accepted", customer: acceptedCustomer},
		{batch: "final-count-wecom-batch", mode: "wecom_then_crm", state: "observed", customer: observedCustomer, transferStatus: func() *int { value := 1; return &value }()},
	} {
		if _, err = pool.Native().Exec(ctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,relation_digest,state,transfer_status) VALUES($1,(SELECT COALESCE(MAX(line_no),0)+1 FROM customer_owner_handoff_lines WHERE batch_id=$1),$2,$3,$4,$5,$6,$7,$8)`, fixture.batch, fixture.customer, fixture.mode, source, target, digest, fixture.state, fixture.transferStatus); err != nil {
			t.Fatal(err)
		}
	}
	var local, wecom, accepted, observed int
	err = pool.Native().QueryRow(ctx, `SELECT count(*) FILTER (WHERE b.mode='local_only'), count(*) FILTER (WHERE b.mode='wecom_then_crm'), count(*) FILTER (WHERE l.state='provider_accepted'), count(*) FILTER (WHERE l.state='observed' AND l.transfer_status=1) FROM customer_owner_handoff_batches b LEFT JOIN customer_owner_handoff_lines l ON l.batch_id=b.id`).Scan(&local, &wecom, &accepted, &observed)
	if err != nil {
		t.Fatalf("qualified final journey counts query: %v", err)
	}
	if local != 1 || wecom != 2 || accepted != 1 || observed != 1 {
		t.Fatalf("fixture counts=%d/%d/%d/%d want=1/2/1/1", local, wecom, accepted, observed)
	}
}

// TestPostgreSQLOwnerHandoffChromiumJourney drives both authorized Owner
// Migration modes through the real login, Host, HTTP handlers and PostgreSQL.
// OneID is read only: the WeCom customer uses an already verified external
// identity. The separate test Provider is injected at composition and the
// River runtime is started by this journey; no production endpoint is used.
func TestPostgreSQLOwnerHandoffChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	_, sourceFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate owner handoff Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	// The frozen new-shell menu is a release artifact. Build it and compose
	// from the repository root so the browser follows its actual ownerMig.html
	// link instead of a package-local fallback.
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	var providerCalls atomic.Int32
	var transferResultCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "access_token": "owner-handoff-test-token", "expires_in": 7200})
		case "/cgi-bin/externalcontact/transfer_customer":
			providerCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "bad transfer", http.StatusBadRequest)
				return
			}
			ids, ok := body["external_userid"].([]any)
			if !ok || len(ids) != 1 {
				http.Error(writer, "bad customers", http.StatusBadRequest)
				return
			}
			if body["transfer_success_msg"] != "您好，后续将由新的服务同事继续为您服务。" {
				http.Error(writer, "uninitialized welcome message", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "customer": []map[string]any{{"external_userid": ids[0], "errcode": 0}}})
		case "/cgi-bin/externalcontact/transfer_result":
			transferResultCalls.Add(1)
			// Keep this response slower than the former browser harness polling
			// interval. The journey must still issue exactly one Provider read.
			time.Sleep(250 * time.Millisecond)
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "customer": []map[string]any{{"external_userid": "browser-external", "status": 1, "takeover_time": 1}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer providerServer.Close()
	application, err := composeWithWeComClientFactory(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "owner-handoff-chromium", WorkerOwner: "owner-handoff-chromium", WorkerLimit: 1, Effects: platformconfig.Effects{ProviderEnabled: true}, WeCom: platformconfig.WeCom{Enabled: true, CorpID: "browser-corp", AgentID: "browser-agent", Secret: "browser-secret", ContactSecret: "browser-contact-secret", ContextSigningKey: "01234567890123456789012345678901"}, GroupOps: platformconfig.GroupOps{WebhookSecret: "owner-handoff-chromium-webhook"}, Survey: platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)}, Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "owner-browser", Password: "owner-browser-password", DisplayName: "Owner Browser"}}, func(config wecomadapter.Config) (*wecomadapter.Client, error) {
		config.APIBase = providerServer.URL
		config.HTTPClient = providerServer.Client()
		return wecomadapter.New(config)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	effectsCtx, stopEffects := context.WithCancel(ctx)
	effectsDone := make(chan error, 1)
	go func() { effectsDone <- application.effectsRuntime.Run(effectsCtx) }()
	defer func() {
		stopEffects()
		if runtimeErr := <-effectsDone; runtimeErr != nil {
			t.Errorf("owner handoff River runtime: %v", runtimeErr)
		}
	}()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "owner-browser", Password: "owner-browser-password", DisplayName: "Owner Browser"}); err != nil {
		t.Fatal(err)
	}
	var source, target, localCustomer, wecomCustomer, primaryOnlyCustomer, locallyReassignedCustomer, mixedCustomer int64
	if err = application.pool.Native().QueryRow(ctx, `WITH account AS (INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('owner-browser-source','$argon2id$fixture','Inactive Source','browser-source',false,false) RETURNING id), role AS (INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account) SELECT id FROM account`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `WITH account AS (INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at) VALUES('owner-browser-target','$argon2id$fixture','Target','browser-target',true,true,clock_timestamp()) RETURNING id), role AS (INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account) SELECT id FROM account`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&localCustomer); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&wecomCustomer); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []*int64{&primaryOnlyCustomer, &locallyReassignedCustomer, &mixedCustomer} {
		if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_local_owners(customer_id,staff_id,source) VALUES($1,$2,'owner_handoff_local_only')`, localCustomer, source); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_local_owners(customer_id,staff_id,source) VALUES($1,$2,'owner_handoff_local_only'),($3,$4,'owner_handoff_local_only')`, locallyReassignedCustomer, target, mixedCustomer, source); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('browser-corp','browser-source',$1,true)`, wecomCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:browser-corp','browser-external','verified','chromium_fixture',1,clock_timestamp())`, wecomCustomer); err != nil {
		t.Fatal(err)
	}
	var primaryRun int64
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at) VALUES('owner-handoff-primary-fixture','manual','succeeded','wecom-corp:browser-corp',clock_timestamp()) RETURNING id`).Scan(&primaryRun); err != nil {
		t.Fatal(err)
	}
	for index, customerID := range []int64{primaryOnlyCustomer, locallyReassignedCustomer, mixedCustomer} {
		var identityID int64
		externalID := "browser-local-primary-" + strconv.Itoa(index+1)
		if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:browser-corp',$2,'verified','chromium_fixture',1,clock_timestamp()) RETURNING id`, customerID, externalID).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id) VALUES($1,'wecom-corp:browser-corp',$2,decode(repeat('01',32),'hex'),$3,clock_timestamp(),'browser-source',$3)`, customerID, identityID, primaryRun); err != nil {
			t.Fatal(err)
		}
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_customer_owner_observations(customer_id,corp_scope,employee_id,relationship_status,last_seen_run_id,observed_at,primary_owner_userid) VALUES($1,'wecom-corp:browser-corp','browser-source','active',$2,clock_timestamp(),'browser-source')`, customerID, primaryRun); err != nil {
			t.Fatal(err)
		}
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	// Exercise the fully composed outer router before Chromium. The frozen
	// shared picker asks for an inactive source and an active target on the
	// exact compatibility URL; a second outer registration must not shadow the
	// Customer scope dispatcher. Group Ops retains its normal exact read through
	// that same dispatcher, while its /sync subtree remains separately owned.
	session, _ := adminAccessLogin(t, application.handler, "owner-browser", "owner-browser-password")
	// The browser must start from an actual Composition response, rather than
	// reading web/dist directly: module mounts and production asset serving are
	// part of the route contract.
	const menuEntryPath = "/admin/customers.html"
	menuEntryPage := httptest.NewRecorder()
	menuEntryRequest := httptest.NewRequest(http.MethodGet, menuEntryPath, nil)
	menuEntryRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	application.handler.ServeHTTP(menuEntryPage, menuEntryRequest)
	menuEntryHTML := menuEntryPage.Body.Bytes()
	if menuEntryPage.Code != http.StatusOK || !bytes.Contains(menuEntryHTML, []byte(`href="ownerMig.html"`)) || !bytes.Contains(menuEntryHTML, []byte(`href="/admin/operation-cycles"`)) {
		t.Fatalf("outer new-shell menu routes status=%d owner_alias=%t canonical_cycles=%t", menuEntryPage.Code, bytes.Contains(menuEntryHTML, []byte(`href="ownerMig.html"`)), bytes.Contains(menuEntryHTML, []byte(`href="/admin/operation-cycles"`)))
	}
	operationCyclesPage := httptest.NewRecorder()
	operationCyclesRequest := httptest.NewRequest(http.MethodGet, "/admin/operation-cycles", nil)
	operationCyclesRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	application.handler.ServeHTTP(operationCyclesPage, operationCyclesRequest)
	operationCyclesHTML := operationCyclesPage.Body.Bytes()
	if operationCyclesPage.Code != http.StatusOK || !bytes.Contains(operationCyclesHTML, []byte(`data-page="cycles"`)) || !bytes.Contains(operationCyclesHTML, []byte(`operationCyclesHost-`)) {
		t.Fatalf("canonical Operation Cycles Host status=%d page=%t host_asset=%t", operationCyclesPage.Code, bytes.Contains(operationCyclesHTML, []byte(`data-page="cycles"`)), bytes.Contains(operationCyclesHTML, []byte(`operationCyclesHost-`)))
	}
	retiredCyclesPage := httptest.NewRecorder()
	retiredCyclesRequest := httptest.NewRequest(http.MethodGet, "/admin/cycles.html", nil)
	retiredCyclesRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	application.handler.ServeHTTP(retiredCyclesPage, retiredCyclesRequest)
	if retiredCyclesPage.Code != http.StatusNotFound {
		t.Fatalf("retired cycles document status=%d, want 404", retiredCyclesPage.Code)
	}
	menuOwnerPage := httptest.NewRecorder()
	menuOwnerRequest := httptest.NewRequest(http.MethodGet, "/admin/ownerMig.html", nil)
	menuOwnerRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	application.handler.ServeHTTP(menuOwnerPage, menuOwnerRequest)
	menuOwnerHTML := menuOwnerPage.Body.Bytes()
	if menuOwnerPage.Code != http.StatusOK || !bytes.Contains(menuOwnerHTML, []byte(`data-owner-handoff-host`)) || !bytes.Contains(menuOwnerHTML, []byte(`/static/admin_console/owner_handoff_host.js`)) || bytes.Contains(menuOwnerHTML, []byte(`id="ownerMigCsv"`)) {
		t.Fatalf("new-shell menu alias did not mount V3 owner Host status=%d host=%t asset=%t retired_template=%t", menuOwnerPage.Code, bytes.Contains(menuOwnerHTML, []byte(`data-owner-handoff-host`)), bytes.Contains(menuOwnerHTML, []byte(`/static/admin_console/owner_handoff_host.js`)), bytes.Contains(menuOwnerHTML, []byte(`id="ownerMigCsv"`)))
	}
	contactHistoryPage := httptest.NewRecorder()
	contactHistoryRequest := httptest.NewRequest(http.MethodGet, "/admin/ownerMig.html?contact_history=1", nil)
	contactHistoryRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	application.handler.ServeHTTP(contactHistoryPage, contactHistoryRequest)
	contactHistoryHTML := contactHistoryPage.Body.Bytes()
	// contact_history remains on the frozen V1 document and its read-only
	// contact-history adapter. It must bypass the mutation-capable V3 Host;
	// the full DOM/read-only request behavior is covered by web/scripts/e2e.mjs.
	if contactHistoryPage.Code != http.StatusOK || bytes.Contains(contactHistoryHTML, []byte(`data-owner-handoff-host`)) || !bytes.Contains(contactHistoryHTML, []byte(`data-page="ownerMig"`)) || !bytes.Contains(contactHistoryHTML, []byte(`src="../assets/admin-`)) {
		t.Fatalf("owner contact-history entry did not retain its frozen read-only document status=%d host=%t page=%t admin_runtime=%t", contactHistoryPage.Code, bytes.Contains(contactHistoryHTML, []byte(`data-owner-handoff-host`)), bytes.Contains(contactHistoryHTML, []byte(`data-page="ownerMig"`)), bytes.Contains(contactHistoryHTML, []byte(`src="../assets/admin-`)))
	}
	operationMembers := func(rawQuery string) []struct {
		UserID string `json:"user_id"`
		Active bool   `json:"active"`
	} {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?"+rawQuery, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: session})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("operation members query=%q status=%d body=%s", rawQuery, response.Code, response.Body.String())
		}
		var payload struct {
			Items []struct {
				UserID string `json:"user_id"`
				Active bool   `json:"active"`
			} `json:"items"`
		}
		if decodeErr := json.NewDecoder(response.Body).Decode(&payload); decodeErr != nil {
			t.Fatalf("operation members query=%q decode: %v", rawQuery, decodeErr)
		}
		return payload.Items
	}
	containsMember := func(items []struct {
		UserID string `json:"user_id"`
		Active bool   `json:"active"`
	}, userID string, active bool) bool {
		for _, item := range items {
			if item.UserID == userID && item.Active == active {
				return true
			}
		}
		return false
	}
	if !containsMember(operationMembers("scope=owner_migration&include_inactive=true"), "browser-source", false) {
		t.Fatal("fully composed owner-migration picker did not return the inactive source")
	}
	if containsMember(operationMembers("scope=owner_migration&include_inactive=false"), "browser-source", false) || !containsMember(operationMembers("scope=owner_migration&include_inactive=false"), "browser-target", true) {
		t.Fatal("fully composed owner-migration picker did not enforce source/target active visibility")
	}
	groupRequest := httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?scope=group_ops", nil)
	groupRequest.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: session})
	groupResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(groupResponse, groupRequest)
	if groupResponse.Code != http.StatusOK {
		t.Fatalf("fully composed Group Ops operation-member query status=%d body=%s", groupResponse.Code, groupResponse.Body.String())
	}
	var groupPayload struct {
		Scope string `json:"scope"`
	}
	if decodeErr := json.NewDecoder(groupResponse.Body).Decode(&groupPayload); decodeErr != nil || groupPayload.Scope != "group_ops" {
		t.Fatalf("fully composed Group Ops operation-member response scope=%q err=%v", groupPayload.Scope, decodeErr)
	}
	runJourney := func(mode string, readback bool, scope string) {
		command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(sourceFile), "..", "..", "internal", "webshell", "owner_handoff_chromium.test.mjs"))
		command.Env = append(os.Environ(), "AICRM_OWNER_HANDOFF_TEST_URL="+server.URL, "AICRM_OWNER_HANDOFF_TEST_USERNAME=owner-browser", "AICRM_OWNER_HANDOFF_TEST_PASSWORD=owner-browser-password", "AICRM_OWNER_HANDOFF_TEST_MENU_ENTRY="+menuEntryPath, "AICRM_OWNER_HANDOFF_TEST_SOURCE="+strconv.FormatInt(source, 10), "AICRM_OWNER_HANDOFF_TEST_TARGET="+strconv.FormatInt(target, 10), "AICRM_OWNER_HANDOFF_TEST_SOURCE_USERID=browser-source", "AICRM_OWNER_HANDOFF_TEST_TARGET_USERID=browser-target", "AICRM_OWNER_HANDOFF_TEST_MODE="+mode, "AICRM_OWNER_HANDOFF_TEST_SCOPE="+scope, "AICRM_OWNER_HANDOFF_TEST_READ_TRANSFER="+strconv.FormatBool(readback))
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			if goruntime.GOOS == "darwin" && (strings.Contains(string(output), "Chromium remote debugging did not become ready") || strings.Contains(string(output), "Chromium exited before remote debugging")) {
				t.Skipf("Chromium cannot start in this local sandbox: %s", strings.TrimSpace(string(output)))
			}
			t.Fatalf("owner handoff Chromium %s journey: %v output=%s", mode, runErr, strings.TrimSpace(string(output)))
		}
		if !strings.Contains(string(output), "owner_handoff_chromium: PASS") {
			t.Fatalf("owner handoff Chromium %s did not report success: %q", mode, output)
		}
	}
	waitOwner := func(customerID int64, want int64, message string) {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var got int64
			err = application.pool.Native().QueryRow(ctx, `SELECT staff_id FROM customer_local_owners WHERE customer_id=$1`, customerID).Scan(&got)
			if err == nil && got == want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("%s", message)
	}
	runJourney("local_only", false, "all")
	waitOwner(localCustomer, target, "local_only did not update the local owner through River")
	waitOwner(primaryOnlyCustomer, target, "local_only did not include the only-WeCom-primary customer")
	waitOwner(mixedCustomer, target, "local_only did not retain Customer local-owner precedence for the mixed customer")
	var locallyReassignedVersion int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT version FROM customer_local_owners WHERE customer_id=$1`, locallyReassignedCustomer).Scan(&locallyReassignedVersion); err != nil || locallyReassignedVersion != 1 {
		t.Fatalf("local-only reselected a customer already assigned to another staff: version=%d err=%v", locallyReassignedVersion, err)
	}
	var localRangeLines int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM customer_owner_handoff_lines line JOIN customer_owner_handoff_batches batch ON batch.id=line.batch_id WHERE batch.mode='local_only'`).Scan(&localRangeLines); err != nil || localRangeLines != 3 {
		t.Fatalf("local all-range lines=%d want=3 (local source + only-primary + mixed; no re-assigned row), err=%v", localRangeLines, err)
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("local_only unexpectedly called test Provider: %d", providerCalls.Load())
	}
	runJourney("wecom_then_crm", true, "excel_include")
	waitOwner(wecomCustomer, target, "provider_accepted WeCom line did not update the local owner")
	if providerCalls.Load() != 1 {
		t.Fatalf("test Provider transfer_customer calls=%d want=1", providerCalls.Load())
	}
	if transferResultCalls.Load() != 1 {
		t.Fatalf("test Provider transfer_result calls=%d want=1", transferResultCalls.Load())
	}
	var local, wecom, accepted, observed int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FILTER (WHERE b.mode='local_only'), count(*) FILTER (WHERE b.mode='wecom_then_crm'), count(*) FILTER (WHERE l.state='provider_accepted'), count(*) FILTER (WHERE l.state='observed' AND l.transfer_status=1) FROM customer_owner_handoff_batches b LEFT JOIN customer_owner_handoff_lines l ON l.batch_id=b.id`).Scan(&local, &wecom, &accepted, &observed); err != nil || local < 1 || wecom < 1 || accepted+observed < 1 || observed < 1 {
		t.Fatalf("batches local/wecom/accepted/observed=%d/%d/%d/%d err=%v", local, wecom, accepted, observed, err)
	}
}
