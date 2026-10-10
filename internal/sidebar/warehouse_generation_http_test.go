package sidebar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func generationFixture() map[string]any {
	return map[string]any{"ok": true, "contract": warehouseGenerationContract, "text": "合成销售参考：先确认每天盘点由谁负责。", "source_version": aiFixture().SourceVersion, "automatic_send_allowed": false}
}

func TestWarehouseGenerationSendsFrozenBilateralSnapshotWithStableKey(t *testing.T) {
	var reads, posts atomic.Int64
	var keyMu sync.Mutex
	var firstKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-operator" || password != "fixture-only" {
			t.Error("protected server credential missing")
		}
		if r.Method == http.MethodGet && r.URL.Path == warehouseSidebarPath {
			reads.Add(1)
			_ = json.NewEncoder(w).Encode(warehouseFixture())
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != warehouseGenerationPath || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid private generation request")
		}
		posts.Add(1)
		var body struct {
			Contract       string `json:"contract"`
			IdempotencyKey string `json:"idempotency_key"`
			Snapshot       struct {
				CustomerID    int64       `json:"customer_id"`
				CorpID        string      `json:"corp_id"`
				EmployeeID    string      `json:"employee_id"`
				SourceVersion string      `json:"source_version"`
				Messages      []AIMessage `json:"messages"`
			} `json:"snapshot"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Contract != warehouseGenerationContract || body.IdempotencyKey == "" || body.Snapshot.CustomerID != 42 ||
			body.Snapshot.CorpID != "corp" || body.Snapshot.EmployeeID != "staff" || body.Snapshot.SourceVersion != aiFixture().SourceVersion || len(body.Snapshot.Messages) != len(aiFixture().Messages) {
			t.Error("frozen scope or bilateral messages missing")
		}
		for i, msg := range body.Snapshot.Messages {
			if msg != aiFixture().Messages[i] {
				t.Error("private dialogue changed in transit")
			}
		}
		keyMu.Lock()
		if firstKey == "" {
			firstKey = body.IdempotencyKey
		} else if firstKey != body.IdempotencyKey {
			t.Error("token refresh changed idempotency key for the same content and employee")
		}
		keyMu.Unlock()
		_ = json.NewEncoder(w).Encode(generationFixture())
	}))
	defer server.Close()
	reader, _ := NewHTTPWarehouseReader(server.URL, "fixture-operator", "fixture-only", server.Client())
	generator, _ := NewHTTPWarehouseGenerator(server.URL, "fixture-operator", "fixture-only", server.Client())
	h := aiHandler(t, reader, generator)
	for i := 0; i < 2; i++ {
		view := aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true)
		var snapshot struct {
			Token string `json:"snapshot_token"`
		}
		if view.Code != 200 || json.Unmarshal(view.Body.Bytes(), &snapshot) != nil || snapshot.Token == "" {
			t.Fatal("snapshot unavailable")
		}
		body, _ := json.Marshal(map[string]any{"snapshot_token": snapshot.Token, "confirmed": true})
		result := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", string(body), true)
		if result.Code != 200 || !strings.Contains(result.Body.String(), "合成销售参考") || strings.Contains(result.Body.String(), "fixture-only") || strings.Contains(result.Body.String(), "您每周可投入多久") || !strings.Contains(result.Body.String(), `"auto_sent":false`) {
			t.Fatalf("invalid draft or private input exposed: status=%d", result.Code)
		}
	}
	if posts.Load() != 2 || reads.Load() != 6 {
		t.Fatalf("unexpected HTTP actions: reads=%d posts=%d", reads.Load(), posts.Load())
	}
}

func TestWarehouseGenerationRejectsUnsafeRepliesWithoutRetry(t *testing.T) {
	for _, field := range []string{"contract", "version", "send", "missing-send-flag", "empty", "too-large", "http-error", "non-json", "oversized-body"} {
		t.Run(field, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if field == "http-error" {
					w.WriteHeader(504)
					_, _ = w.Write([]byte("private upstream error"))
					return
				}
				if field == "non-json" {
					_, _ = w.Write([]byte("private upstream non-JSON"))
					return
				}
				if field == "oversized-body" {
					_, _ = w.Write([]byte(strings.Repeat(" ", (256<<10)+1)))
					return
				}
				data := generationFixture()
				switch field {
				case "contract":
					data["contract"] = "legacy-copy-generate"
				case "version":
					data["source_version"] = "changed"
				case "send":
					data["automatic_send_allowed"] = true
				case "missing-send-flag":
					delete(data, "automatic_send_allowed")
				case "empty":
					data["text"] = " "
				case "too-large":
					data["text"] = strings.Repeat("x", 32001)
				}
				_ = json.NewEncoder(w).Encode(data)
			}))
			defer server.Close()
			g, _ := NewHTTPWarehouseGenerator(server.URL, "fixture-operator", "fixture-only", server.Client())
			_, err := g.GenerateSidebarSuggestion(context.Background(), AISuggestionInput{Principal: Principal{"corp", "staff"}, Snapshot: aiFixture(), IdempotencyKey: "synthetic-key"})
			if err == nil || strings.Contains(err.Error(), "private upstream") || calls.Load() != 1 {
				t.Fatal("unsafe reply accepted, disclosed, or retried")
			}
		})
	}
}

func TestWarehouseGenerationCancellationRedirectAndScopeNeverRepeatPost(t *testing.T) {
	var targetCalls, calls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	g, _ := NewHTTPWarehouseGenerator(server.URL, "fixture-operator", "fixture-only", server.Client())
	in := AISuggestionInput{Principal: Principal{"corp", "staff"}, Snapshot: aiFixture(), IdempotencyKey: "synthetic-key"}
	if _, err := g.GenerateSidebarSuggestion(context.Background(), in); err == nil || calls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatal("redirect forwarded private content or retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.GenerateSidebarSuggestion(ctx, in); err == nil || calls.Load() != 1 {
		t.Fatal("cancelled generation reached server")
	}
	in.Principal.EmployeeID = "other"
	if _, err := g.GenerateSidebarSuggestion(context.Background(), in); err == nil || calls.Load() != 1 {
		t.Fatal("invalid viewer scope reached server")
	}
	var accepted atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		// Keep the request accepted until the real client deadline expires.
		<-r.Context().Done()
	}))
	defer slow.Close()
	client := slow.Client()
	client.Timeout = 2 * time.Second
	g, _ = NewHTTPWarehouseGenerator(slow.URL, "fixture-operator", "fixture-only", client)
	in.Principal.EmployeeID = "staff"
	if _, err := g.GenerateSidebarSuggestion(context.Background(), in); err == nil || accepted.Load() != 1 {
		t.Fatal("accepted timed-out generation was retried")
	}
}
