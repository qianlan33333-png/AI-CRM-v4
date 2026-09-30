package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/provider"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type promptChainSecurity struct{}

func (promptChainSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (promptChainSecurity) AuthorizeCSRF(ctx context.Context, req *http.Request) (accessdomain.Principal, error) {
	return promptChainSecurity{}.Authenticate(ctx, req)
}

func promptChainService(t *testing.T) (*pgxpool.Pool, *automationapp.Service, *automationapp.RuntimeService, *Repository, *platformpostgres.UnitOfWork, *automationhttp.Handler, func()) {
	t.Helper()
	native, cleanup := automationRuntimeIntegrationPool(t)
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	service := automationapp.NewAgentService(uow, repository, repository)
	runtimeService, err := automationapp.NewRuntimeService(uow, repository, automationExecutionReader{packageID: 1}, automationSnapshotReader{}, 1)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	handler, err := automationhttp.NewHandler(service, promptChainSecurity{})
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	return native, service, runtimeService, repository, uow, handler, func() { wrapped.Close(); cleanup() }
}

func promptChainHash(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func promptChainRequest(t *testing.T, handler http.Handler, method, path, key string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type promptChainAgentResponse struct {
	OK    bool `json:"ok"`
	Agent struct {
		ID                  int64  `json:"id"`
		DraftRolePrompt     string `json:"draft_role_prompt"`
		DraftTaskPrompt     string `json:"draft_task_prompt"`
		PublishedRolePrompt string `json:"published_role_prompt"`
		PublishedTaskPrompt string `json:"published_task_prompt"`
		DraftVersion        int64  `json:"draft_version"`
		PublishedVersion    int64  `json:"published_version"`
		Status              string `json:"status"`
	} `json:"agent"`
}

func promptChainCreate(t *testing.T, handler http.Handler, code, role, task string, suffix string) int64 {
	t.Helper()
	body, err := json.Marshal(map[string]any{"agent_name": "prompt full chain", "agent_code": code, "automation_type": "agent", "role_prompt": role, "task_prompt": task})
	if err != nil {
		t.Fatal(err)
	}
	response := promptChainRequest(t, handler, http.MethodPost, "/api/admin/automation-agents", "scratch-create-key-"+suffix, body)
	if response.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded promptChainAgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || !decoded.OK || decoded.Agent.ID < 1 {
		t.Fatalf("create response=%s err=%v", response.Body.String(), err)
	}
	t.Logf("HTTP POST create status=%d request_bytes=%d request_sha256=%s response_bytes=%d response_sha256=%s", response.Code, len(body), promptChainHash(body), response.Body.Len(), promptChainHash(response.Body.Bytes()))
	return decoded.Agent.ID
}

func promptChainSavePublishActivate(t *testing.T, handler http.Handler, id int64, role, task, suffix string) {
	t.Helper()
	path := "/api/admin/automation-agents/" + strconv.FormatInt(id, 10)
	var response *httptest.ResponseRecorder
	for field, value := range map[string]string{"role_prompt": role, "task_prompt": task} {
		body, err := json.Marshal(map[string]string{field: value})
		if err != nil {
			t.Fatal(err)
		}
		response = promptChainRequest(t, handler, http.MethodPatch, path, "prompt-update-"+suffix+field, body)
		if response.Code != http.StatusOK {
			t.Fatalf("update %s status=%d", field, response.Code)
		}
		t.Logf("HTTP PATCH %s request_bytes=%d sha256=%s", field, len(body), promptChainHash(body))
	}
	for _, step := range []struct{ tail, key string }{{"/publish", "publish"}, {"/activate", "activate"}} {
		response = promptChainRequest(t, handler, http.MethodPost, path+step.tail, "scratch-"+step.key+"-key-"+suffix, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", step.tail, response.Code, response.Body.String())
		}
		t.Logf("HTTP POST %s status=%d request_bytes=0 response_bytes=%d response_sha256=%s", step.tail, response.Code, response.Body.Len(), promptChainHash(response.Body.Bytes()))
	}
}

func testPromptChain(t *testing.T, role, task string) {
	native, service, runtimeService, repository, uow, handler, cleanup := promptChainService(t)
	defer cleanup()
	ctx := context.Background()
	initialRole, initialTask := role, "task-initial"
	id := promptChainCreate(t, handler, "prompt_full_chain", initialRole, initialTask, "pass-001")

	promptChainSavePublishActivate(t, handler, id, role, task, "pass-001")
	path := "/api/admin/automation-agents/" + strconv.FormatInt(id, 10)
	response := promptChainRequest(t, handler, http.MethodGet, path, "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.Code, response.Body.String())
	}
	var detail promptChainAgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil || !detail.OK || detail.Agent.PublishedRolePrompt != role || detail.Agent.PublishedTaskPrompt != task {
		t.Fatalf("detail prompts mismatch role=%d/%d task=%d/%d err=%v", len(detail.Agent.PublishedRolePrompt), len(role), len(detail.Agent.PublishedTaskPrompt), len(task), err)
	}
	t.Logf("HTTP GET detail status=%d response_bytes=%d response_sha256=%s; published field bytes role=%d sha256=%s task=%d sha256=%s", response.Code, response.Body.Len(), promptChainHash(response.Body.Bytes()), len([]byte(detail.Agent.PublishedRolePrompt)), promptChainHash([]byte(detail.Agent.PublishedRolePrompt)), len([]byte(detail.Agent.PublishedTaskPrompt)), promptChainHash([]byte(detail.Agent.PublishedTaskPrompt)))
	var dbDraftRole, dbDraftTask, dbPublishedRole, dbPublishedTask string
	err := native.QueryRow(ctx, `SELECT draft_role_prompt,draft_task_prompt,published_role_prompt,published_task_prompt FROM automation_agents WHERE id=$1`, id).Scan(&dbDraftRole, &dbDraftTask, &dbPublishedRole, &dbPublishedTask)
	if err != nil || dbDraftRole != role || dbDraftTask != task || dbPublishedRole != role || dbPublishedTask != task {
		t.Fatalf("DB prompt byte equality failed draft role=%d task=%d published role=%d task=%d err=%v", len([]byte(dbDraftRole)), len([]byte(dbDraftTask)), len([]byte(dbPublishedRole)), len([]byte(dbPublishedTask)), err)
	}
	t.Logf("DB exact-read draft_role=%dB sha256=%s draft_task=%dB sha256=%s published_role=%dB sha256=%s published_task=%dB sha256=%s", len([]byte(dbDraftRole)), promptChainHash([]byte(dbDraftRole)), len([]byte(dbDraftTask)), promptChainHash([]byte(dbDraftTask)), len([]byte(dbPublishedRole)), promptChainHash([]byte(dbPublishedRole)), len([]byte(dbPublishedTask)), promptChainHash([]byte(dbPublishedTask)))
	published, found, err := service.PublishedGeneration(ctx, automationport.AgentID(id), detail.Agent.PublishedVersion)
	if err != nil || !found || !published.Valid() || published.RolePrompt != role || published.TaskPrompt != task {
		t.Fatalf("published read found=%v valid=%v role=%d task=%d err=%v", found, published.Valid(), len(published.RolePrompt), len(published.TaskPrompt), err)
	}

	if _, found, err := service.PublishedGeneration(ctx, automationport.AgentID(id), detail.Agent.PublishedVersion+1); err != nil || found {
		t.Fatalf("stale published version resolved: found=%v err=%v", found, err)
	}

	config := automationprovider.GenerationConfig{Enabled: true, APIKey: "synthetic-only", Model: "synthetic-model", Timeout: 5 * time.Second}
	var observedSystem, observedUser string
	var observedProviderBody []byte
	reject := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var readErr error
		observedProviderBody, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read local model request: %v", readErr)
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(observedProviderBody, &request); err != nil {
			t.Errorf("decode local model request: %v", err)
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		for _, message := range request.Messages {
			switch message.Role {
			case "system":
				observedSystem = message.Content
			case "user":
				observedUser = message.Content
			}
		}
		if reject {
			http.Error(w, "context window exceeded", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"这是一条合成建议。"}}]}`))
	}))
	defer server.Close()
	config.BaseURL, config.Client = server.URL, server.Client()
	policy, err := config.Policy()
	if err != nil {
		t.Fatal(err)
	}
	runID := insertGenerationTestRun(t, native)
	item := generationTestItem(runID, 1001, "eer_1")
	item.AgentID, item.AgentPublishedVersion, item.AgentCode = int64(published.AgentID), published.PublishedVersion, published.AgentCode
	item.RolePrompt, item.TaskPrompt, item.ModelPolicy = published.RolePrompt, published.TaskPrompt, policy
	// Upgrading from 0115 retains both nonempty checks, and never rewrites frozen data.
	var nonemptyChecks int
	if err := native.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='automation_generation_items'::regclass AND conname IN ('automation_generation_items_role_prompt_check','automation_generation_items_task_prompt_check') AND pg_get_constraintdef(oid) NOT LIKE '%16000%'`).Scan(&nonemptyChecks); err != nil || nonemptyChecks != 2 {
		t.Fatalf("prompt migration checks=%d err=%v", nonemptyChecks, err)
	}
	var createdItem automationdomain.GenerationItem
	err = uow.Within(ctx, func(tx context.Context) error {
		items, createErr := repository.CreateGenerationItems(tx, []automationdomain.GenerationItem{item})
		if createErr != nil {
			return createErr
		}
		createdItem = items[0]
		return repository.BindGenerationEffect(tx, createdItem.ID, "eer_1", time.Now().UTC())
	})
	if err != nil {
		t.Fatalf("insert synthetic queued generation row: %v", err)
	}
	for _, column := range []string{"role_prompt", "task_prompt"} {
		if _, err := native.Exec(ctx, "UPDATE automation_generation_items SET "+column+"='' WHERE id=$1", createdItem.ID); err == nil {
			t.Fatalf("database accepted empty %s", column)
		}
	}
	dispatch, found, err := runtimeService.GenerationDispatch(ctx, "eer_1")
	if err != nil || !found || dispatch.RolePrompt != role || dispatch.TaskPrompt != task || dispatch.ModelPolicy != policy {
		t.Fatalf("DB GenerationDispatch found=%v role=%d/%d task=%d/%d err=%v", found, len([]byte(dispatch.RolePrompt)), len([]byte(role)), len([]byte(dispatch.TaskPrompt)), len([]byte(task)), err)
	}
	payloadDigest := effectport.Digest(dispatch.PayloadDigest)
	provider, err := automationprovider.NewGenerationProvider(config, runtimeService)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(ctx, effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate, PayloadDigest: payloadDigest}, effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || observedSystem != role || observedUser != task+"\n\n【问卷】\n"+item.Context.Questionnaire+"\n\n【最近20条聊天】\n"+item.Context.RecentChats+"\n\n【用户标签】\n"+item.Context.Tags+"\n\n【激活信息】\n"+item.Context.Activation {
		t.Fatalf("local generation completion=%s called=%v executed=%v system=%d/%d user_prefix=%v err=%v", result.Completion, result.CallAttempted, result.RealExternalCallExecuted, len(observedSystem), len(role), strings.HasPrefix(observedUser, task), err)
	}
	t.Logf("DB GenerationDispatch effect=%s item=%d state=queued role_bytes=%d role_sha256=%s task_bytes=%d task_sha256=%s", dispatch.EffectID, dispatch.ItemID, len([]byte(dispatch.RolePrompt)), promptChainHash([]byte(dispatch.RolePrompt)), len([]byte(dispatch.TaskPrompt)), promptChainHash([]byte(dispatch.TaskPrompt)))
	t.Logf("PROVIDER local httptest request_bytes=%d request_sha256=%s system_bytes=%d system_sha256=%s user_task_prefix_bytes=%d task_sha256=%s; Adapter completion=%s (loopback synthetic only)", len(observedProviderBody), promptChainHash(observedProviderBody), len([]byte(observedSystem)), promptChainHash([]byte(observedSystem)), len([]byte(task)), promptChainHash([]byte(task)), result.Completion)
	t.Logf("PASS save→publish→read→DB GenerationDispatch→provider byte-equality: separate role/task PATCH requests; DB+published+generation row+provider system/task bytes exact beyond former prompt bounds")
	reject = true
	rejected, err := provider.Execute(ctx, effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate, PayloadDigest: payloadDigest}, effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || rejected.Completion != effectport.StateFinalFailed || rejected.FailureCode != "generation_http_rejected" || !rejected.CallAttempted {
		t.Fatalf("provider rejection was hidden: %+v err=%v", rejected, err)
	}

}

func TestPostgreSQLLongPromptHTTPPublishDispatchProvider(t *testing.T) {
	for _, tc := range []struct{ name, role, task string }{
		{"ASCII", strings.Repeat("R", 20001), strings.Repeat("T", 24000)},
		{"CJK", strings.Repeat("中", 20001), strings.Repeat("文", 24000)},
		{"emoji", strings.Repeat("😀", 20001), strings.Repeat("🚀", 24000)},
	} {
		t.Run(tc.name, func(t *testing.T) { testPromptChain(t, tc.role, tc.task) })
	}
}

// Exercise an actual upgrade with an existing frozen item, not only clean schema creation.
func TestPostgreSQLPromptMigrationPreservesFrozenItems(t *testing.T) {
	native, cleanup := automationIntegrationPoolWithMigrations(t, []string{"0005_external_effects.sql", "0013_automation_agents.sql", "0043_automation_runtime.sql", "0087_automation_manual_ai_review.sql", "0015_config_adminops.sql", "0094_runtime_config_releases.sql", "0115_automation_dynamic_text_generation.sql"})
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	runID := insertGenerationTestRun(t, native)
	item := generationTestItem(runID, 1001, "eer_1")
	err = uow.Within(ctx, func(tx context.Context) error {
		_, err := repository.CreateGenerationItems(tx, []automationdomain.GenerationItem{item})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("😀", 24000)
	if _, err = native.Exec(ctx, `UPDATE automation_generation_items SET role_prompt=$1, task_prompt=$1`, long); err == nil {
		t.Fatal("old constraint unexpectedly admitted long prompts")
	}
	var before, after string
	if err = native.QueryRow(ctx, `SELECT row_to_json(i)::text FROM automation_generation_items i`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, source, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../migrations/0212_automation_prompt_length_unbounded.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT row_to_json(i)::text FROM automation_generation_items i`).Scan(&after); err != nil || before != after {
		t.Fatalf("migration changed frozen row: err=%v", err)
	}
	if _, err = native.Exec(ctx, `UPDATE automation_generation_items SET role_prompt=$1, task_prompt=$1`, long); err != nil {
		t.Fatal(err)
	}
	var role, task string
	if err = native.QueryRow(ctx, `SELECT role_prompt, task_prompt FROM automation_generation_items`).Scan(&role, &task); err != nil || role != long || task != long {
		t.Fatalf("upgraded prompt byte mismatch: err=%v", err)
	}
}
