package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/sidebar"
)

func TestSidebarWarehouseCompositionUsesProtectedReaderOnlyWhenEnabled(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/local/v3/sidebar-ai-operations" || r.URL.Query().Get("employee_id") != "employee" {
			t.Error("composition did not bind scoped reader")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"contract":"crm-sidebar-ai-operations.v1","message_scope_version":"crm-private-message-scope.v1","snapshot":{"customer_id":42,"corp_id":"corp","employee_id":"employee","source_version":"published-fixture","summary":"合成已归档摘要","summary_updated_at":"2026-10-09T01:16:21Z","synced_at":"2026-10-09T01:15:27Z","messages":[],"last_customer_message":null}}`))
	}))
	defer server.Close()
	cfg := platformconfig.SidebarWarehouse{Origin: server.URL, Username: "fixture-operator", Password: "fixture-only", HTTPClient: server.Client()}
	if reader, err := composeSidebarWarehouse(cfg); err != nil || reader != nil || calls != 0 {
		t.Fatal("disabled config activated a reader")
	}
	cfg.Enabled = true
	reader, err := composeSidebarWarehouse(cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.ReadAIOperations(context.Background(), sidebar.Principal{CorpID: "corp", EmployeeID: "employee"}, 42)
	if err != nil || snapshot.CustomerID != 42 || calls != 1 {
		t.Fatalf("protected reader not wired: %v", err)
	}
}

func TestSidebarGenerationCompositionUsesProtectedManualAgentOnlyWhenEnabled(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		user, password, ok := r.BasicAuth()
		if r.Method != "POST" || r.URL.Path != "/api/local/v3/sidebar-manual-generation" || !ok || user != "fixture-operator" || password != "fixture-only" {
			t.Error("composition did not use protected manual Agent")
		}
		_, _ = w.Write([]byte(`{"ok":true,"contract":"crm-sidebar-manual-generation.v1","source_version":"published-fixture","text":"合成参考回复","automatic_send_allowed":false}`))
	}))
	defer server.Close()
	cfg := platformconfig.SidebarWarehouse{Enabled: true, Origin: server.URL, Username: "fixture-operator", Password: "fixture-only", HTTPClient: server.Client()}
	if generator, err := composeSidebarSuggestions(cfg); err != nil || generator != nil || calls != 0 {
		t.Fatal("default enabled Reader also enabled the model")
	}
	cfg.GenerationEnabled = true
	generator, err := composeSidebarSuggestions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := generator.GenerateSidebarSuggestion(context.Background(), sidebar.AISuggestionInput{Principal: sidebar.Principal{CorpID: "corp", EmployeeID: "employee"},
		Snapshot: sidebar.AIWarehouseSnapshot{CustomerID: 42, CorpID: "corp", EmployeeID: "employee", SourceVersion: "published-fixture"}, IdempotencyKey: "synthetic-key"})
	if err != nil || result != "合成参考回复" || calls != 1 {
		t.Fatal("manual generation composition did not call once")
	}
}
