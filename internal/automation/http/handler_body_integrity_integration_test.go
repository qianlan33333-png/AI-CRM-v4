package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestHTTPAgentBodyIntegrityPostgreSQL(t *testing.T) {
	native, cleanup := automationHTTPIntegrationPool(t)
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
	repository, err := automationstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := automationapp.NewAgentService(uow, repository, repository)
	handler, err := NewHandler(service, testSecurity{p: principal()})
	if err != nil {
		t.Fatal(err)
	}

	firstObject := []byte(`{"agent_name":"prefix","agent_code":"body_prefix","automation_type":"agent"}`)
	body := append([]byte(nil), firstObject...)
	body = append(body, bytes.Repeat([]byte(" "), (128<<10)-len(body))...)
	secondObject := []byte(" " + `{"agent_name":"x","automation_type":"agent"}` + " ")
	if len(secondObject) != 46 {
		t.Fatalf("test fixture suffix is %d bytes, want 46", len(secondObject))
	}
	body = append(body, secondObject...)
	if len(body) != 131118 {
		t.Fatalf("test fixture body is %d bytes, want 131118", len(body))
	}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/automation-agents", bytes.NewReader(body))
	request.Header.Set("Idempotency-Key", "body-integrity-trailing-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	counts := automationHTTPWriteCounts(t, native)
	if response.Code != http.StatusBadRequest || counts != [4]int{0, 0, 0, 0} {
		t.Fatalf("trailing JSON status=%d body=%s write counts (agents, receipts, audits, outbox)=%v, want 400 and no writes", response.Code, response.Body.String(), counts)
	}

	rolePrompt := strings.Repeat("🧭", 20_000)
	taskPrompt := strings.Repeat("界", 20_000)
	validBody, err := json.Marshal(map[string]string{
		"agent_name":      "large-body probe",
		"agent_code":      "large_body_probe",
		"automation_type": string(automationport.AutomationTypeAgent),
		"role_prompt":     rolePrompt,
		"task_prompt":     taskPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(validBody) <= 128<<10 {
		t.Fatalf("valid test request is only %d bytes, want more than 128 KiB", len(validBody))
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/automation-agents", bytes.NewReader(validBody))
	request.Header.Set("Idempotency-Key", "body-integrity-large-0001")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("large create status=%d body=%s", response.Code, response.Body.String())
	}

	type agentResponse struct {
		OK    bool `json:"ok"`
		Agent struct {
			ID              int64  `json:"id"`
			DraftRolePrompt string `json:"draft_role_prompt"`
			DraftTaskPrompt string `json:"draft_task_prompt"`
		} `json:"agent"`
	}
	var created agentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !created.OK || created.Agent.ID < 1 || !bytes.Equal([]byte(created.Agent.DraftRolePrompt), []byte(rolePrompt)) || !bytes.Equal([]byte(created.Agent.DraftTaskPrompt), []byte(taskPrompt)) {
		t.Fatal("create response did not preserve prompt bytes")
	}

	request = httptest.NewRequest(http.MethodGet, "/api/admin/automation-agents/"+strconv.FormatInt(created.Agent.ID, 10), nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("detail readback status=%d body=%s", response.Code, response.Body.String())
	}
	var readback agentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &readback); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if readback.Agent.ID != created.Agent.ID || !bytes.Equal([]byte(readback.Agent.DraftRolePrompt), []byte(rolePrompt)) || !bytes.Equal([]byte(readback.Agent.DraftTaskPrompt), []byte(taskPrompt)) {
		t.Fatal("PostgreSQL HTTP readback did not preserve prompt bytes")
	}
}

func automationHTTPWriteCounts(t *testing.T, pool *pgxpool.Pool) [4]int {
	t.Helper()
	var counts [4]int
	err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM automation_agents),
		(SELECT count(*) FROM automation_operation_receipts),
		(SELECT count(*) FROM automation_audit_events),
		(SELECT count(*) FROM automation_outbox)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3])
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

func automationHTTPIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping automation HTTP PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_automation_http_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0013_automation_agents.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply automation migration: %v", err)
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
