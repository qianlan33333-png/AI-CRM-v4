package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	access "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLAIExecutionReceiptsHTTP(t *testing.T) { testAIReceiptsComposition(t, false) }
func TestPostgreSQLAIExecutionReceiptsInstalled(t *testing.T) {
	if testconfig.InstalledProduct().Binary == "" {
		t.Fatal("exact installed package inputs required")
	}
	validateOrdersOAuthInstalledProduct(t, testconfig.InstalledProduct())
	testAIReceiptsComposition(t, true)
}

// The installed variant replaces only the transport with the existing package
// fixture proxy. Isolated facts are synthetic; no Worker or Provider write runs.
func testAIReceiptsComposition(t *testing.T, installed bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	application, err := compose(ctx, config.Runtime{Role: config.RoleAPI, DatabaseURL: db, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "receipt-fixture", WorkerOwner: "receipt-fixture", WorkerLimit: 1, GroupOps: config.GroupOps{WebhookSecret: "receipt-fixture"}, Survey: config.Survey{DataKey: key, IdentityPhoneDataKey: key}, WeChatPay: config.WeChatPay{OrderContactDataKey: key}})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	owner, _, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "receipt-owner", Password: "receipt-owner-fixture-password", DisplayName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	principal := access.Principal{Kind: access.KindAdmin, InternalID: owner.ID, Roles: []access.Role{access.RoleSuperAdmin}}
	admin, err := application.management.AddUser(ctx, principal, accessapp.AddUserInput{Username: "receipt-admin", Password: "receipt-admin-fixture-password", DisplayName: "Admin", Roles: []access.Role{access.RoleAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.management.AddUser(ctx, principal, accessapp.AddUserInput{Username: "receipt-viewer", Password: "receipt-viewer-fixture-password", DisplayName: "Viewer", Roles: []access.Role{access.RoleViewer}})
	if err != nil {
		t.Fatal(err)
	}
	pool := application.pool.Native()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var customer, plan, recipient, content, intent int64
	queries := []struct {
		sql    string
		args   []any
		target *int64
	}{
		{`INSERT INTO customers DEFAULT VALUES RETURNING id`, nil, &customer},
	}
	for _, q := range queries {
		if err = tx.QueryRow(ctx, q.sql, q.args...).Scan(q.target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,display_name,customer_status) VALUES($1,'Receipt Customer','active')`, customer); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,target_count,pending_count,approved_count,created_by) VALUES('receipt','fixture',decode(repeat('ab',32),'hex'),'completed',1,0,1,$1) RETURNING id`, admin.ID).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,review_state,execution_state) VALUES($1,$2,$3,'approved','provider_accepted') RETURNING id`, plan, customer, admin.ID).Scan(&recipient); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by) VALUES($1,1,decode(repeat('bc',32),'hex'),'[{"kind":"text","text":"PRIVATE_BODY"}]',$2) RETURNING id`, recipient, admin.ID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2 WHERE id=$1`, recipient, content); err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("aiassistant:%d:%d:%d", plan, recipient, content)
	if err = tx.QueryRow(ctx, `INSERT INTO outbound_private_message_intents(source_reference,customer_id,staff_id,payload_reference,source_digest,target_digest,payload_digest,policy_hash,receipt_key,external_effect_id,state) VALUES($1,$2,$3,$1,decode(repeat('ab',32),'hex'),decode(repeat('bc',32),'hex'),decode(repeat('cd',32),'hex'),decode(repeat('de',32),'hex'),decode(repeat('ef',32),'hex'),'eer_900001','provider_accepted') RETURNING id`, ref, customer, admin.ID).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_assistant_effect_bindings(recipient_id,outbound_intent_id,external_effect_id,payload_digest,state,attempt_count,provider_accepted) VALUES($1,$2,'eer_900001',decode(repeat('cd',32),'hex'),'provider_accepted',1,true)`, recipient, intent); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_private_message_receipts(payload_reference,message_id,sender_userid,external_userid) VALUES($1,'msg-original-fixture','HuangYouCan-fixture','PRIVATE_TARGET')`, ref); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if installed {
		process := useInstalledOrderExportFixture(t, server, db, key)
		defer stopInvoiceSettingsInstalledProcess(t, process)
	} else {
		server.Config.Handler = application.handler
	}
	server.StartTLS()
	handler := orderExportHTTPBridge(t, server)
	session, _ := adminAccessLogin(t, handler, "receipt-admin", "receipt-admin-fixture-password")
	viewer, _ := adminAccessLogin(t, handler, "receipt-viewer", "receipt-viewer-fixture-password")
	path := fmt.Sprintf("/api/admin/ai-assistant/plans/%d/execution-receipts", plan)
	for _, tc := range []struct {
		session string
		want    int
	}{{"", 401}, {viewer, 403}, {session, 200}} {
		result := automationFixedContentRequest(t, handler, http.MethodGet, path, nil, tc.session, "", "")
		if result.Code != tc.want {
			t.Fatalf("read=%d want=%d body=%s", result.Code, tc.want, result.Body.String())
		}
		if tc.want == 200 && (!bytes.Contains(result.Body.Bytes(), []byte(`"msgid":"msg-original-fixture"`)) || !bytes.Contains(result.Body.Bytes(), []byte(`"creation_errcode":null`))) {
			t.Fatal(result.Body.String())
		}
		for _, secret := range []string{"PRIVATE_TARGET", "PRIVATE_BODY"} {
			if strings.Contains(result.Body.String(), secret) {
				t.Fatal("restricted value leaked")
			}
		}
	}
	var attempts, observations int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM external_effect_attempts),(SELECT count(*) FROM outbound_private_message_receipts WHERE delivery_status IS NOT NULL)`).Scan(&attempts, &observations); err != nil || attempts != 0 || observations != 0 {
		t.Fatalf("read created effects or observation writes: %d/%d %v", attempts, observations, err)
	}
	var role string
	if err = pool.QueryRow(ctx, `SELECT role_code FROM admin_user_roles WHERE admin_user_id=$1`, admin.ID).Scan(&role); err != nil || role != "admin" {
		t.Fatal("administrator role changed", err)
	}
	if err = pool.QueryRow(ctx, `SELECT role_code FROM admin_user_roles WHERE admin_user_id=$1`, owner.ID).Scan(&role); err != nil || role != "super_admin" {
		t.Fatal("unique owner changed", err)
	}
	t.Logf("aiassistant_execution_receipts: PASS admin/read viewer/deny no_send no_observation_write installed=%t", installed)
}
