package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// The real composition authenticates each admin request, stores the capability
// and receipt, and joins Order/Customer membership through their runtime ports.
// No external worker or Provider is started by this fixture.
func TestPostgreSQLProductScopedShareBoundsAndArchivedRevocation(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 120*time.Second, productExternalPushChromiumFixtureOptions{deferEffectsWorker: true})
	session, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	pool := f.application.pool.Native()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i, days := range []int{5, 40} {
		var customerID int64
		if err := pool.QueryRow(f.ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(f.ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,avatar_url,gender,contact_type,corp_name,oneid_label,phone_masked,activation_status,source,source_version,last_synced_at,updated_at) VALUES($1,'active','PRIVATE-MEMBER-NAME','',0,0,'','','','active','share-audit',1,$2,$2)`, customerID, now); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("share-audit-%d", i)))
		if _, err := pool.Exec(f.ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('share-audit',$1,$2,$3,'会员','active',$4,$5,'PRIVATE-REMARK',$6,$4,$4)`, fmt.Sprintf("share-audit-%d", i), customerID, f.serviceProductID, now.Add(-time.Hour), now.Add(time.Duration(days)*24*time.Hour), digest[:]); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/admin/service-period-products/%d/member-grid/scoped-shares", f.serviceProductID)
	createBody := `{"mode":"details","fields":["remaining_days","token_usage"],"config":{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"remaining_days","operator":"lte","value":7}]},"sorts":[],"groups":[],"presentation":{"metrics":["active"]}}}`
	request := func(method, target, body, key string) (int, []byte) {
		response := productExternalPushAdminMutation(t, f.application.handler, method, target, body, session, csrf, key)
		return response.Code, response.Body.Bytes()
	}
	sameJSON := func(a, b []byte) bool {
		var first, second any
		return json.Unmarshal(a, &first) == nil && json.Unmarshal(b, &second) == nil && reflect.DeepEqual(first, second)
	}
	code, body := request(http.MethodPost, path, createBody, "share-audit-issue")
	if code != 200 {
		t.Fatalf("issue status=%d body=%s", code, body)
	}
	var issued struct {
		Token string `json:"token"`
		Share struct {
			ID      int64 `json:"id"`
			Version int64 `json:"version"`
		} `json:"share"`
	}
	if err := json.Unmarshal(body, &issued); err != nil || len(issued.Token) != 43 || issued.Share.ID < 1 || issued.Share.Version != 1 {
		t.Fatalf("issue response=%s err=%v", body, err)
	}
	code, replay := request(http.MethodPost, path, createBody, "share-audit-issue")
	if code != 200 || !sameJSON(replay, body) {
		t.Fatalf("issue replay status=%d body=%s", code, replay)
	}
	query := func(config string) (int, []byte) {
		payload, err := json.Marshal(map[string]any{"token": issued.Token, "config": json.RawMessage(config)})
		if err != nil {
			t.Fatal(err)
		}
		response := productExternalPushAdminMutation(t, f.application.handler, http.MethodPost, "/api/public/service-period-member-grid/scoped-query", string(payload), "", "", "")
		return response.Code, response.Body.Bytes()
	}
	code, body = query(`{"schema_version":1}`)
	var result struct {
		Total   int              `json:"total"`
		Items   []map[string]any `json:"items"`
		Metrics map[string]any   `json:"metrics"`
	}
	if err := json.Unmarshal(body, &result); err != nil || code != 200 || result.Total != 1 || len(result.Items) != 1 || len(result.Metrics) != 1 || result.Metrics["active"] != float64(1) {
		t.Fatalf("frozen scope response=%d %s err=%v", code, body, err)
	}
	item := result.Items[0]
	if len(item) != 5 || len(fmt.Sprint(item["user_ref"])) != 22 || item["token_usage"] != "unavailable" || item["remaining_days"] == nil {
		t.Fatalf("redacted/unknown-value item=%v", item)
	}
	for _, key := range []string{"customer_id", "entitlement_id", "record_id", "member", "remark", "display_name"} {
		if _, present := item[key]; present {
			t.Fatalf("private field leaked: %s", key)
		}
	}
	code, body = query(`{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"remaining_days","operator":"gte","value":30}]}}`)
	if code != 200 || json.Unmarshal(body, &result) != nil || result.Total != 0 || len(result.Items) != 0 {
		t.Fatalf("visitor escaped frozen base: %d %s", code, body)
	}
	code, body = query(`{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"member","operator":"contains","value":"PRIVATE"}]}}`)
	if code != 400 {
		t.Fatalf("private-field probe status=%d body=%s", code, body)
	}
	viewer, err := f.application.management.AddUser(f.ctx, accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, accessapp.AddUserInput{Username: "share-audit-viewer", Password: "share-audit-viewer-password", DisplayName: "Viewer", Roles: []accessdomain.Role{accessdomain.RoleViewer}})
	if err != nil || viewer.ID < 1 {
		t.Fatal(err)
	}
	viewerSession, viewerCSRF := adminAccessLogin(t, f.application.handler, "share-audit-viewer", "share-audit-viewer-password")
	for _, attempt := range []struct {
		session, csrf string
		expected      int
	}{{"", "", 401}, {session, "", 403}, {viewerSession, viewerCSRF, 403}} {
		response := productExternalPushAdminMutation(t, f.application.handler, http.MethodPost, path, createBody, attempt.session, attempt.csrf, "share-audit-denied")
		if response.Code != attempt.expected {
			t.Fatalf("unauthorized issue expected=%d actual=%d body=%s", attempt.expected, response.Code, response.Body.String())
		}
	}
	productPath := fmt.Sprintf("/api/admin/service-period-products/%d", f.serviceProductID)
	code, body = request(http.MethodPost, productPath+"/copy", `{"expected_version":1}`, "share-audit-other-product")
	var copied struct {
		Product struct {
			ID int64 `json:"service_product_id"`
		} `json:"product"`
	}
	if code != 201 || json.Unmarshal(body, &copied) != nil || copied.Product.ID < 1 {
		t.Fatalf("copy for cross-resource check=%d %s", code, body)
	}
	revokeBody := fmt.Sprintf(`{"id":%d,"version":1}`, issued.Share.ID)
	otherPath := fmt.Sprintf("/api/admin/service-period-products/%d/member-grid/scoped-shares", copied.Product.ID)
	code, body = request(http.MethodDelete, otherPath, revokeBody, "share-audit-wrong-resource")
	if code != 409 {
		t.Fatalf("cross-product revocation=%d %s", code, body)
	}
	code, body = query(`{"schema_version":1}`)
	if code != 200 {
		t.Fatalf("rejected cross-product command changed share=%d %s", code, body)
	}
	code, body = request(http.MethodDelete, productPath, `{"expected_version":1}`, "share-audit-archive")
	if code != 200 {
		t.Fatalf("archive=%d %s", code, body)
	}
	code, body = request(http.MethodPost, path, createBody, "share-audit-archived-issue")
	if code != 404 {
		t.Fatalf("archived product issued new share=%d %s", code, body)
	}
	// Historical data may remain readable, but its existing capability must
	// always remain revocable without reopening the archived product.
	code, body = request(http.MethodDelete, path, revokeBody, "share-audit-revoke")
	if code != 200 {
		t.Fatalf("archived share cannot be revoked: status=%d body=%s", code, body)
	}
	code, replay = request(http.MethodDelete, path, revokeBody, "share-audit-revoke")
	if code != 200 || !sameJSON(replay, body) {
		t.Fatalf("revocation replay=%d %s", code, replay)
	}
	code, body = query(`{"schema_version":1}`)
	if code != 410 {
		t.Fatalf("revoked capability still reads=%d %s", code, body)
	}
	var enabled bool
	var version, count int64
	if err := pool.QueryRow(f.ctx, `SELECT enabled,version,(SELECT count(*) FROM product_dashboard_shares) FROM product_dashboard_shares WHERE id=$1`, issued.Share.ID).Scan(&enabled, &version, &count); err != nil || enabled || version != 2 || count != 1 {
		t.Fatalf("durable share enabled=%v version=%d count=%d err=%v", enabled, version, count, err)
	}
}
