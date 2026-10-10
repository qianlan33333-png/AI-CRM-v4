package sidebar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func warehouseFixture() map[string]any {
	s := aiFixture()
	return map[string]any{"ok": true, "contract": warehouseSidebarContract, "message_scope_version": warehousePrivateScopeContract,
		"snapshot": map[string]any{"customer_id": s.CustomerID, "corp_id": s.CorpID, "employee_id": s.EmployeeID, "source_version": s.SourceVersion,
			"summary": s.Summary, "summary_updated_at": s.SummaryUpdatedAt, "synced_at": s.SyncedAt, "messages": s.Messages, "last_customer_message": s.LastCustomerMessage}}
}

func TestWarehouseReaderBindsServerScopeAndKeepsDialoguePrivate(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		u, p, ok := r.BasicAuth()
		if !ok || u != "fixture-operator" || p != "fixture-only" || r.Method != "GET" || r.URL.Path != warehouseSidebarPath ||
			r.URL.Query().Get("customer_id") != "42" || r.URL.Query().Get("corp_id") != "corp" || r.URL.Query().Get("employee_id") != "staff" || len(r.URL.Query()) != 3 {
			t.Error("request did not use the exact authenticated private reader scope")
		}
		_ = json.NewEncoder(w).Encode(warehouseFixture())
	}))
	defer server.Close()
	reader, err := NewHTTPWarehouseReader(server.URL, "fixture-operator", "fixture-only", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	h := aiHandler(t, reader, nil)
	w := aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true)
	if w.Code != 200 || requests.Load() != 1 || !strings.Contains(w.Body.String(), "两个晚上") || strings.Contains(w.Body.String(), "您每周可投入多久") || strings.Contains(w.Body.String(), `"messages"`) || strings.Contains(w.Body.String(), "fixture-only") {
		t.Fatalf("dialogue or credential exposed / invalid response: %d", w.Code)
	}
}

func TestWarehouseReaderRejectsUnscopedAndUnpublishedResponses(t *testing.T) {
	for _, field := range []string{"contract", "scope-version", "customer", "corp", "employee", "watermark", "future-watermark", "empty-version", "last-message", "group-role", "too-many", "oversized-body", "non-json", "http-forbidden"} {
		t.Run(field, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if field == "http-forbidden" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte("private source error must not be propagated"))
					return
				}
				if field == "non-json" {
					_, _ = w.Write([]byte("<html>operator login</html>"))
					return
				}
				if field == "oversized-body" {
					_, _ = w.Write([]byte(strings.Repeat(" ", warehouseResponseLimit+1)))
					return
				}
				data := warehouseFixture()
				s := data["snapshot"].(map[string]any)
				switch field {
				case "contract":
					data["contract"] = "customer-wide-workbench"
				case "scope-version":
					delete(data, "message_scope_version")
				case "customer":
					s["customer_id"] = 43
				case "corp":
					s["corp_id"] = "other-corp"
				case "employee":
					s["employee_id"] = "other-staff"
				case "watermark":
					s["synced_at"] = nil
				case "future-watermark":
					s["synced_at"] = time.Now().Add(time.Hour)
				case "empty-version":
					s["source_version"] = ""
				case "last-message":
					s["last_customer_message"] = nil
				case "group-role":
					messages := aiFixture().Messages
					messages[0].Role = "group"
					s["messages"] = messages
				case "too-many":
					s["messages"] = make([]AIMessage, 41)
				}
				_ = json.NewEncoder(w).Encode(data)
			}))
			defer server.Close()
			reader, err := NewHTTPWarehouseReader(server.URL, "fixture-operator", "fixture-only", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			w := aiCall(aiHandler(t, reader, nil), "GET", "/api/sidebar/v2/ai-operations", "", true)
			if w.Code != 503 || strings.Contains(w.Body.String(), "private source error") || strings.Contains(w.Body.String(), "fixture-only") {
				t.Fatalf("invalid source accepted or upstream data leaked: %d", w.Code)
			}
		})
	}
}

func TestWarehouseReaderNeverFollowsRedirectOrRetries(t *testing.T) {
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	var requests atomic.Int64
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	reader, err := NewHTTPWarehouseReader(source.URL, "fixture-operator", "fixture-only", source.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ReadAIOperations(context.Background(), Principal{"corp", "staff"}, 42); err == nil || requests.Load() != 1 || redirected.Load() != 0 {
		t.Fatal("reader followed redirect or retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = reader.ReadAIOperations(ctx, Principal{"corp", "staff"}, 42); err == nil || requests.Load() != 1 {
		t.Fatal("cancelled read reached service")
	}
}

func TestWarehouseReaderAcceptsBoundedEscapedBilateralContract(t *testing.T) {
	s := aiFixture()
	s.Summary = strings.Repeat("\x01", 32000)
	s.Messages = make([]AIMessage, 40)
	for i := range s.Messages {
		role := "customer"
		if i%2 != 0 {
			role = "sales"
		}
		s.Messages[i] = AIMessage{ID: strconv.Itoa(i + 1), Role: role, Type: "text", Text: strings.Repeat("\x01", 16000), OccurredAt: s.LastCustomerMessage.OccurredAt.Add(time.Duration(i) * time.Second)}
	}
	s.LastCustomerMessage = &s.Messages[38]
	data := warehouseFixture()
	data["snapshot"] = map[string]any{"customer_id": s.CustomerID, "corp_id": s.CorpID, "employee_id": s.EmployeeID, "source_version": s.SourceVersion,
		"summary": s.Summary, "summary_updated_at": s.SummaryUpdatedAt, "synced_at": s.SyncedAt, "messages": s.Messages, "last_customer_message": s.LastCustomerMessage}
	body, _ := json.Marshal(data)
	if len(body) <= 1<<20 || len(body) > warehouseResponseLimit {
		t.Fatal("fixture does not cover the valid escaped response size")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	r, _ := NewHTTPWarehouseReader(server.URL, "fixture-operator", "fixture-only", server.Client())
	result, err := r.ReadAIOperations(context.Background(), Principal{"corp", "staff"}, 42)
	if err != nil || len(result.Messages) != 40 || len(result.Summary) != 32000 || result.Messages[1].Text != s.Messages[1].Text {
		t.Fatal("valid bounded dialogue was truncated or rejected")
	}
}
