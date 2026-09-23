package main

import (
	"context"
	"encoding/json"
	"fmt"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Called from the full composition journey: canonical customer is provisioned
// through OneID, all requests use the real OAuth issuer and outer Host router.
func coreAudienceOAuthJourney(t *testing.T, ctx context.Context, a *composedApplication, machine *accessapp.MachineService, admin accessdomain.Principal, customerID int64) {
	t.Helper()
	uow, err := platformpostgres.NewUnitOfWork(a.pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := segmentstore.NewPostgreSQL(a.pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	service := segmentapp.NewService(uow, repo)
	pkg, err := service.CreatePackage(ctx, segmentapp.PackageCreateCommand{Name: "API 核心包", TemplateKey: "active_contacts", Actor: admin.InternalID, IdempotencyKey: "core-api-package-0001"})
	if err != nil {
		t.Fatal(err)
	}
	core := segmentapp.NewCoreOperations(service, repo, nil, nil)
	_, err = core.PutProduct(ctx, segmentapp.CoreProductCommand{Product: segmentport.CoreProduct{ID: 1, PackageID: pkg.ID, Name: "API 产品", Description: "适合测试客户", Enabled: true}, Actor: admin.InternalID, IdempotencyKey: "core-api-product-0001"})
	if err != nil {
		t.Fatal(err)
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		_, e := repo.ChangeCoreAssignment(tx, segmentport.CoreAssignment{CustomerID: customerID, CoreProductID: 1, Source: "manual", Reason: "联调验证", EnteredAt: time.Now().Add(-time.Hour)}, 0, false)
		if e != nil {
			return e
		}
		return repo.PublishCoreAssignments(tx, admin.InternalID, "core-api-snapshot-0001", time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{ClientID: "core.audience.integration", DisplayName: "Core API", Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"platform.capabilities.read", "audience.product.read", "audience.member.read", "audience.member.operations.read", "audience.member.history.read", "audience.push.write"}, OwnerScope: accessdomain.OwnerScope{"customer_id": {fmt.Sprint(customerID)}, "package_id": {fmt.Sprint(pkg.ID)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, client.Client.ClientID, client.Secret, true); err != nil {
		t.Fatal(err)
	}
	read := openPlatformV1CompositionOAuthToken(t, a.handler, client.Client.ClientID, client.Secret, "read")
	write := openPlatformV1CompositionOAuthToken(t, a.handler, client.Client.ClientID, client.Secret, "write")
	request := func(method, path, body, token, key string, want int) []byte {
		t.Helper()
		r := openPlatformV1CompositionRequest(method, path, body, token)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s status=%d body=%s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	products := request("GET", "/open/v1/audience/core-products", "", read, "", 200)
	var productEnvelope struct {
		Items json.RawMessage `json:"items"`
		Data  struct {
			Items json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal(products, &productEnvelope) != nil || len(productEnvelope.Items) == 0 || string(productEnvelope.Items) != string(productEnvelope.Data.Items) {
		t.Fatalf("legacy product alias mismatch: %s", products)
	}
	base := fmt.Sprintf("/open/v1/audience/packages/%d/members", pkg.ID)
	members := request("GET", base, "", read, "", 200)
	if !strings.Contains(string(members), `"customer_id":`+fmt.Sprint(customerID)) {
		t.Fatalf("missing member: %s", members)
	}
	detail := fmt.Sprintf("%s/%d/operations", base, customerID)
	request("GET", fmt.Sprintf("%s/%d/history", base, customerID), "", read, "", 200)
	request("GET", fmt.Sprintf("%s/%d/operations", base, customerID+1), "", read, "", 404)
	push := map[string]any{"push_id": "business-push-1", "customer_id": customerID, "package_id": pkg.ID, "materials": []any{map[string]any{"kind": "image", "id": 1}}, "status": "reported", "status_version": 1, "occurred_at": time.Now().UTC().Format(time.RFC3339Nano)}
	raw, _ := json.Marshal(push)
	request("POST", "/open/v1/audience/push-records", string(raw), read, "core-api-push-0001", 403)
	for i := 0; i < 3; i++ {
		request("POST", "/open/v1/audience/push-records", string(raw), write, "core-api-push-0001", 200)
	}
	for _, key := range []string{"", "", "x", "x"} {
		request("POST", "/open/v1/audience/push-records", string(raw), write, key, 200)
	}
	push["status"] = "success"
	raw, _ = json.Marshal(push)
	request("POST", "/open/v1/audience/push-records", string(raw), write, "", 409)
	push["status_version"] = 2
	raw, _ = json.Marshal(push)
	// Same legacy key with a different payload must still conflict.
	request("POST", "/open/v1/audience/push-records", string(raw), write, "x", 409)
	request("POST", "/open/v1/audience/push-records", string(raw), write, "core-api-push-0001", 409)
	request("POST", "/open/v1/audience/push-records", string(raw), write, "core-api-push-0002", 200)
	request("POST", "/open/v1/audience/push-records", string(raw), write, "", 200)
	request("POST", "/open/v1/audience/push-records", string(raw), write, "", 200)
	body := request(http.MethodGet, detail, "", read, "", 200)
	var out struct {
		Data segmentport.CoreMemberDetail `json:"data"`
	}
	if json.Unmarshal(body, &out) != nil || out.Data.Stats.PushCount != 1 || len(out.Data.Pushes) != 1 || out.Data.Pushes[0].Status != "success" {
		t.Fatalf("push count/state: %s", body)
	}
	if out.Data.Pushes[0].Source != "machine:"+client.Client.ClientID {
		t.Fatalf("source not bound: %q", out.Data.Pushes[0].Source)
	}
	mcp := request("POST", "/mcp", `{"jsonrpc":"2.0","id":"members","method":"tools/call","params":{"name":"list_audience_members","arguments":{"package_id":`+fmt.Sprint(pkg.ID)+`}}}`, read, "", 200)
	if strings.Contains(string(mcp), `"isError":true`) {
		t.Fatalf("MCP read failed: %s", mcp)
	}
}
