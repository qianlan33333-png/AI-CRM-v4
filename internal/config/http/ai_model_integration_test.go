package http

import (
	"bytes"
	"context"
	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAIModelSettingsPostgreSQLHTTPAndBrowser(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	sql, e := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../migrations/0179_config_ai_model.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(sql)); e != nil {
		t.Fatal(e)
	}
	wrapped, e := platformpostgres.Wrap(pool, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := configstore.NewPostgreSQL(pool, uow)
	if e != nil {
		t.Fatal(e)
	}
	service, e := configapp.NewAIModelService(uow, repo, bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	security := testSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, e := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, security)
	if e != nil {
		t.Fatal(e)
	}
	handler.WithAIModels(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	command := exec.Command("node", filepath.Join(filepath.Dir(file), "../../webshell/ai_model_host_pg.test.mjs"))
	command.Env = append(os.Environ(), "AICRM_MODEL_TEST_URL="+server.URL)
	if out, e := command.CombinedOutput(); e != nil {
		t.Fatalf("model browser: %v %s", e, out)
	}
	var ciphertext []byte
	var version, audits int64
	if e = pool.QueryRow(ctx, `SELECT key_ciphertext,version FROM config_ai_model`).Scan(&ciphertext, &version); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(ciphertext, []byte("model-test-secret")) || version != 2 {
		t.Fatal("encrypted model version was not persisted")
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM config_ai_model_audits`).Scan(&audits); e != nil || audits != 2 {
		t.Fatalf("audits=%d err=%v", audits, e)
	}
	restarted, e := configapp.NewAIModelService(uow, repo, bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	model, found, e := restarted.ReadAIModelRuntime(ctx)
	if e != nil || !found || model.APIKey != "model-test-secret" || model.Model != "deepseek-reasoner" {
		t.Fatal("restart readback failed")
	}
	for _, test := range []struct {
		role accessdomain.Role
		csrf error
		want int
	}{{accessdomain.RoleViewer, nil, 403}, {accessdomain.RoleAdmin, accessdomain.ErrCSRFRequired, 403}} {
		guard := &Handler{models: service, security: testSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{test.role}}, csrf: test.csrf}}
		request := httptest.NewRequest(http.MethodPut, "/api/admin/config/ai-model", strings.NewReader(`{"provider":"deepseek","model":"deepseek-chat","api_key":"must-not-save","expected_version":2}`))
		result := httptest.NewRecorder()
		guard.aiModel(result, request)
		if result.Code != test.want {
			t.Fatalf("guard status=%d", result.Code)
		}
	}
	public, e := service.ReadAIModel(ctx)
	raw, _ := json.Marshal(public)
	if e != nil || bytes.Contains(raw, []byte("secret")) {
		t.Fatal("public read leaked key")
	}
}
