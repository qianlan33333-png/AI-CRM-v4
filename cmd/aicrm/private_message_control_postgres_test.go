package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	access "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	out "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	jobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecom "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostgreSQLPrivateMessageControlHTTPAndLifecycle(t *testing.T) {
	testPrivateMessageControlLifecycle(t, false)
}
func TestPostgreSQLPrivateMessageControlInstalled(t *testing.T) {
	if testconfig.InstalledProduct().Binary == "" {
		t.Fatal("exact installed private control package required")
	}
	validateOrdersOAuthInstalledProduct(t, testconfig.InstalledProduct())
	testPrivateMessageControlLifecycle(t, true)
}
func testPrivateMessageControlLifecycle(t *testing.T, installed bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	var stops atomic.Int32
	var lateSent atomic.Bool
	sent := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/gettoken" {
			fmt.Fprint(w, `{"errcode":0,"access_token":"FIXTURE_TOKEN","expires_in":7200}`)
			return
		}
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("invalid provider body")
			return
		}
		switch r.URL.Path {
		case "/cgi-bin/externalcontact/get_groupmsg_send_result":
			status := 0
			timestamp := int64(0)
			if strings.HasPrefix(fmt.Sprint(in["msgid"]), "failed-task-") {
				status, _ = strconv.Atoi(strings.TrimPrefix(fmt.Sprint(in["msgid"]), "failed-task-"))
			}
			if in["msgid"] == "sent-task" || (in["msgid"] == "late-reconcile-task" && lateSent.Load()) || (in["msgid"] == "send-race-task" && stops.Load() > 2) {
				status = 1
				timestamp = sent.Unix()
			}
			fmt.Fprintf(w, `{"errcode":0,"next_cursor":"","send_list":[{"userid":"fixture-sender","external_userid":"PRIVATE_TARGET","status":%d,"send_time":%d}]}`, status, timestamp)
		case "/cgi-bin/externalcontact/cancel_groupmsg_send":
			stops.Add(1)
			if in["msgid"] == "unknown-task" {
				fmt.Fprint(w, `{"errcode":`)
			} else {
				fmt.Fprint(w, `{"errcode":0}`)
			}
		default:
			t.Errorf("unexpected provider write: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	client, err := wecom.NewDirectory(wecom.Config{Enabled: true, CorpID: "fixture-corp", ContactSecret: "fixture-secret", APIBase: provider.URL, HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	app, err := composeWithWeComClientFactory(ctx, config.Runtime{Role: config.RoleAPI, DatabaseURL: db, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "control-fixture", WorkerOwner: "control-fixture", WorkerLimit: 1, Effects: config.Effects{ProviderEnabled: true}, AIAssistant: config.AIAssistant{DispatchEnabled: true, ProviderPermission: "private-message-authorized"}, WeCom: config.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact", ContextSigningKey: "fixture-control-context-signing-key-32"}, GroupOps: config.GroupOps{WebhookSecret: "control-fixture"}, Survey: config.Survey{DataKey: key, IdentityPhoneDataKey: key}, WeChatPay: config.WeChatPay{OrderContactDataKey: key}}, func(wecom.Config) (*wecom.Client, error) { return client, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	owner, _, err := app.management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "control-owner", Password: "control-owner-fixture-password", DisplayName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	principal := access.Principal{Kind: access.KindAdmin, InternalID: owner.ID, Roles: []access.Role{access.RoleSuperAdmin}}
	admin, err := app.management.AddUser(ctx, principal, accessapp.AddUserInput{Username: "control-admin", Password: "control-admin-fixture-password", DisplayName: "Admin", Roles: []access.Role{access.RoleAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.management.AddUser(ctx, principal, accessapp.AddUserInput{Username: "control-viewer", Password: "control-viewer-fixture-password", DisplayName: "Viewer", Roles: []access.Role{access.RoleViewer}})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = app.handler
	if installed {
		// Every HTTP acceptance/query/auth request crosses the exact installed
		// API. The real EER/River worker below uses only the local fake connector;
		// no installed Provider write process is started and network is blocked.
		process := useInstalledOrderExportFixture(t, server, db, key,
			"AICRM_AI_ASSISTANT_DISPATCH_ENABLED=true", "AICRM_AI_ASSISTANT_PROVIDER_PERMISSION=private-message-authorized",
			"AICRM_OUTBOUND_PROVIDER_ENABLED=true", "AICRM_WECOM_ENABLED=true", "AICRM_WECOM_CORP_ID=fixture-corp", "AICRM_WECOM_AGENT_ID=fixture-agent",
			"AICRM_WECOM_SECRET=fixture-secret", "AICRM_WECOM_CONTACT_SECRET=fixture-contact", "AICRM_WECOM_CONTEXT_SIGNING_KEY=fixture-control-context-signing-key-32")
		defer stopInvoiceSettingsInstalledProcess(t, process)
	}
	server.StartTLS()
	handler := orderExportHTTPBridge(t, server)
	session, csrf := adminAccessLogin(t, handler, "control-admin", "control-admin-fixture-password")
	viewer, viewerCSRF := adminAccessLogin(t, handler, "control-viewer", "control-viewer-fixture-password")
	pool := app.pool.Native()
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[effects.EffectJobArgs](workers, effects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := jobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := effects.NewRepository(pool, insert)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := outbound.NewPrivateMessageRepository(pool, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SetCompletionSink(writer); err != nil {
		t.Fatal(err)
	}
	uow, err := postgres.NewUnitOfWork(app.pool)
	if err != nil {
		t.Fatal(err)
	}
	seed := func(msg string) (int64, int64, string) {
		t.Helper()
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		var customer, plan, recipient, content int64
		if e = tx.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customer); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,display_name,customer_status)VALUES($1,'Control Fixture','active')`, customer); e != nil {
			t.Fatal(e)
		}
		if e = tx.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,target_count,pending_count,approved_count,created_by)VALUES('control','fixture',decode(repeat('ab',32),'hex'),'completed',1,0,1,$1)RETURNING id`, admin.ID).Scan(&plan); e != nil {
			t.Fatal(e)
		}
		if e = tx.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,review_state,execution_state)VALUES($1,$2,$3,'approved','provider_accepted')RETURNING id`, plan, customer, admin.ID).Scan(&recipient); e != nil {
			t.Fatal(e)
		}
		if e = tx.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by)VALUES($1,1,decode(repeat('bc',32),'hex'),'[{"kind":"text","text":"PRIVATE_BODY"}]',$2)RETURNING id`, recipient, admin.ID).Scan(&content); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2 WHERE id=$1`, recipient, content); e != nil {
			t.Fatal(e)
		}
		ref := fmt.Sprintf("aiassistant:%d:%d:%d", plan, recipient, content)
		digest, _ := hex.DecodeString(strings.TrimPrefix(string(effect.Hash("original-intent", ref)), "sha256:"))
		var intent int64
		originalEffect := fmt.Sprintf("eer_%d", 1000000+recipient)
		if e = tx.QueryRow(ctx, `INSERT INTO outbound_private_message_intents(source_reference,customer_id,staff_id,payload_reference,source_digest,target_digest,payload_digest,policy_hash,receipt_key,external_effect_id,state) VALUES($1,$2,$3,$1,$4,$4,$4,$4,$4,$5,'provider_accepted') RETURNING id`, ref, customer, admin.ID, digest, originalEffect).Scan(&intent); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO ai_assistant_effect_bindings(recipient_id,outbound_intent_id,external_effect_id,payload_digest,state,attempt_count,provider_accepted)VALUES($1,$2,$3,$4,'provider_accepted',1,true)`, recipient, intent, originalEffect, digest); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO outbound_private_message_receipts(payload_reference,message_id,sender_userid,external_userid)VALUES($1,$2,'fixture-sender','PRIVATE_TARGET')`, ref, msg); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		return plan, recipient, ref
	}
	execute := func(id string) {
		t.Helper()
		var job, generation int64
		numeric, e := strconv.ParseInt(strings.TrimPrefix(id, "eer_"), 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		if e = pool.QueryRow(ctx, `SELECT river_job_id,generation FROM external_effect_jobs WHERE effect_id=$1 ORDER BY generation DESC LIMIT 1`, numeric).Scan(&job, &generation); e != nil {
			t.Fatal(e)
		}
		worker := effects.NewWorker(repository, &outbound.PrivateMessageControlProvider{Enabled: true, Store: writer, Reader: client, Stopper: client})
		if e = worker.Work(ctx, &river.Job[effects.EffectJobArgs]{JobRow: &rivertype.JobRow{ID: job}, Args: effects.EffectJobArgs{EffectID: numeric, Generation: generation}}); e != nil {
			t.Fatal(e)
		}
	}
	plan, recipient, ref := seed("pending-task")
	path := fmt.Sprintf("/api/admin/ai-assistant/plans/%d/recipients/%d/stop", plan, recipient)
	body := []byte(`{"expected_version":1,"reason":"discard superseded copy"}`)
	for _, tc := range []struct {
		session, csrf string
		want          int
	}{{"", "", 403}, {viewer, viewerCSRF, 403}, {session, "", 403}} {
		r := automationFixedContentRequest(t, handler, http.MethodPost, path, body, tc.session, tc.csrf, "reject-stop-key")
		if r.Code != tc.want {
			t.Fatalf("authorization=%d want=%d body=%s", r.Code, tc.want, r.Body.String())
		}
	}
	r := automationFixedContentRequest(t, handler, http.MethodPost, path, body, session, csrf, "first-stop-key")
	if r.Code != 202 {
		t.Fatalf("accept=%d %s", r.Code, r.Body.String())
	}
	var accepted struct {
		Control out.PrivateMessageControl `json:"control"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			e := uow.Within(ctx, func(tx context.Context) error {
				_, e := writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: ref, Action: "stop", ActorID: admin.ID, RequestKey: effect.Hash("different-stop-key", strconv.Itoa(index))})
				return e
			})
			errorsCh <- e
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*)FROM outbound_private_message_controls WHERE payload_reference=$1 AND action='stop'`, ref).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent stop duplicated", count, err)
	}
	execute(accepted.Control.EffectID)
	if stops.Load() != 1 {
		t.Fatal("stop calls", stops.Load())
	}
	result, err := writer.ReadPrivateMessageControl(ctx, ref, "stop")
	if err != nil || result.State != "stopped" || result.StoppedAt == nil || result.SentAt != nil {
		t.Fatal(result, err)
	}
	confirmedStopTime := *result.StoppedAt
	replay := automationFixedContentRequest(t, handler, http.MethodPost, path, body, session, csrf, "first-stop-key")
	if replay.Code != 202 || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatal("idempotency replay", replay.Code, replay.Body.String())
	}
	drift := automationFixedContentRequest(t, handler, http.MethodPost, path, []byte(`{"expected_version":1,"reason":"different reason"}`), session, csrf, "first-stop-key")
	if drift.Code != 409 {
		t.Fatal("same key drift", drift.Code)
	}
	query := automationFixedContentRequest(t, handler, http.MethodGet, strings.TrimSuffix(path, "stop")+"control", nil, session, "", "")
	if query.Code != 200 || !strings.Contains(query.Body.String(), `"state":"stopped"`) {
		t.Fatal("query", query.Code, query.Body.String())
	}
	for _, secret := range []string{"PRIVATE_TARGET", "PRIVATE_BODY", "FIXTURE_TOKEN", "pending-task"} {
		if strings.Contains(query.Body.String(), secret) {
			t.Fatal("control result leaked restricted values")
		}
	}
	sentinel := errors.New("force rollback")
	err = uow.Within(ctx, func(tx context.Context) error {
		_, e := writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: ref, Action: "reconcile", ActorID: admin.ID, RequestKey: effect.Hash("rollback")})
		if e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*)FROM outbound_private_message_controls WHERE payload_reference=$1 AND action='reconcile'`, ref).Scan(&count); err != nil || count != 0 {
		t.Fatal("control/effect rollback leaked", count, err)
	}
	var adopted out.PrivateMessageControl
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		adopted, e = writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: ref, Action: "reconcile", ActorID: admin.ID, RequestKey: effect.Hash("adopt-existing-stop")})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	execute(adopted.EffectID)
	observedStop, err := writer.ReadPrivateMessageControl(ctx, ref, "reconcile", adopted.EffectID)
	if err != nil || observedStop.State != "stopped" || observedStop.StoppedAt == nil || !observedStop.StoppedAt.Equal(confirmedStopTime) {
		t.Fatal("stop time changed on a later observation", observedStop, err)
	}
	var attempted, executed bool
	if err = pool.QueryRow(ctx, `SELECT call_attempted,real_external_call_executed FROM external_effect_attempts WHERE effect_id=$1 ORDER BY id DESC LIMIT 1`, strings.TrimPrefix(adopted.EffectID, "eer_")).Scan(&attempted, &executed); err != nil || attempted || executed {
		t.Fatal("cached owner proof invented a Provider call", attempted, executed, err)
	}
	_, _, sentRef := seed("sent-task")
	var sentControl out.PrivateMessageControl
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		sentControl, e = writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: sentRef, Action: "stop", ActorID: admin.ID, RequestKey: effect.Hash("sent-stop")})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	execute(sentControl.EffectID)
	result, err = writer.ReadPrivateMessageControl(ctx, sentRef, "stop")
	if err != nil || result.State != "delivered" || result.SentAt == nil || !result.SentAt.Equal(sent) || stops.Load() != 1 {
		t.Fatal("sent message was stopped or original time lost", result, stops.Load(), err)
	}
	_, _, unknownRef := seed("unknown-task")
	var unknownControl out.PrivateMessageControl
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		unknownControl, e = writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: unknownRef, Action: "stop", ActorID: admin.ID, RequestKey: effect.Hash("unknown-stop")})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	execute(unknownControl.EffectID)
	result, err = writer.ReadPrivateMessageControl(ctx, unknownRef, "stop")
	if err != nil || result.State != "outcome_unknown" {
		t.Fatal("unknown response falsely stopped", result, err)
	}
	execute(unknownControl.EffectID)
	if stops.Load() != 2 {
		t.Fatal("unknown stop was retried", stops.Load())
	}
	_, _, lateRef := seed("late-reconcile-task")
	for _, status := range []int{2, 3, 4} {
		_, _, failedRef := seed(fmt.Sprintf("failed-task-%d", status))
		var control out.PrivateMessageControl
		err = uow.Within(ctx, func(tx context.Context) error {
			var e error
			control, e = writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: failedRef, Action: "reconcile", ActorID: admin.ID, RequestKey: effect.Hash("failed-observation", strconv.Itoa(status))})
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		execute(control.EffectID)
		result, err = writer.ReadPrivateMessageControl(ctx, failedRef, "reconcile", control.EffectID)
		if err != nil || result.State != "final_failed" || stops.Load() != 2 {
			t.Fatal("negative delivery was mislabeled pending or stopped", status, result, err)
		}
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			lateSent.Store(true)
		}
		var control out.PrivateMessageControl
		err = uow.Within(ctx, func(tx context.Context) error {
			var e error
			control, e = writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: lateRef, Action: "reconcile", ActorID: admin.ID, RequestKey: effect.Hash("observation", strconv.Itoa(i))})
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		execute(control.EffectID)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*)FROM outbound_private_message_controls WHERE payload_reference=$1 AND action='reconcile'`, lateRef).Scan(&count); err != nil || count != 2 {
		t.Fatal("fresh receipt observations were frozen", count, err)
	}
	var role string
	if err = pool.QueryRow(ctx, `SELECT role_code FROM admin_user_roles WHERE admin_user_id=$1`, owner.ID).Scan(&role); err != nil || role != "super_admin" {
		t.Fatal("sole super administrator changed", err)
	}
	result, err = writer.ReadPrivateMessageControl(ctx, lateRef, "reconcile")
	if err != nil || result.State != "delivered" || result.SentAt == nil || !result.SentAt.Equal(sent) {
		t.Fatal("late delivery was not refreshed", result, err)
	}
	_, _, raceRef := seed("send-race-task")
	var raceControl out.PrivateMessageControl
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		raceControl, e = writer.RequestPrivateMessageControlWithin(tx, out.PrivateMessageControlCommand{PayloadReference: raceRef, Action: "stop", ActorID: admin.ID, RequestKey: effect.Hash("send-stop-race")})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	execute(raceControl.EffectID)
	result, err = writer.ReadPrivateMessageControl(ctx, raceRef, "stop")
	if err != nil || result.State != "delivered" || result.StoppedAt != nil || result.SentAt == nil || !result.SentAt.Equal(sent) || stops.Load() != 3 {
		t.Fatal("simultaneous send was erased by stop", result, err)
	}
	t.Log("private control HTTP/PG: administrator allow; viewer/CSRF deny; replay/concurrency/rollback; stopped/delivered/unknown; repeat read observations; no send")
}
