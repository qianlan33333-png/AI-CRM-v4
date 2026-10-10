package sidebar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type aiReaderFunc func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error)

func (f aiReaderFunc) ReadAIOperations(c context.Context, p Principal, id customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
	return f(c, p, id)
}

type aiGeneratorFunc func(context.Context, AISuggestionInput) (string, error)

type aiViewerFunc func(context.Context, *http.Request) (Principal, error)

func (f aiViewerFunc) VerifyAIViewer(c context.Context, r *http.Request) (Principal, error) {
	return f(c, r)
}

func (f aiGeneratorFunc) GenerateSidebarSuggestion(c context.Context, i AISuggestionInput) (string, error) {
	return f(c, i)
}

type aiContext struct {
	principal Principal
	customer  customerdomain.CustomerID
}

func (c *aiContext) VerifySidebarContext(context.Context, string) (Principal, customerdomain.CustomerID, error) {
	return c.principal, c.customer, nil
}

func aiFixture() AIWarehouseSnapshot {
	t := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	messages := []AIMessage{{ID: "s1", Role: "sales", Type: "text", Text: "您每周可投入多久？", OccurredAt: t.Add(-time.Minute)}, {ID: "c2", Role: "customer", Type: "text", Text: "两个晚上", OccurredAt: t}}
	return AIWarehouseSnapshot{CustomerID: 42, CorpID: "corp", EmployeeID: "staff", SourceVersion: "warehouse-v1", Summary: "希望安排时间", SummaryUpdatedAt: &t, SyncedAt: &t, Messages: messages, LastCustomerMessage: &messages[1]}
}
func aiHandler(t *testing.T, reader AIWarehouseReader, generator AISuggestionGenerator) *Handler {
	t.Helper()
	products := testProducts{}
	h, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey, AIWarehouse: reader, AISuggestions: generator, AICurrentViewer: aiViewerFunc(func(context.Context, *http.Request) (Principal, error) { return Principal{"corp", "staff"}, nil }), Now: func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func aiCall(h *Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth {
		r.Header.Set("Authorization", "Bearer scoped")
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, r)
	return w
}
func aiToken(t *testing.T, h *Handler) string {
	t.Helper()
	w := aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true)
	if w.Code != 200 {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	var d struct {
		Token string `json:"snapshot_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d.Token
}
func aiBody(token string) string {
	b, _ := json.Marshal(map[string]any{"snapshot_token": token, "confirmed": true})
	return string(b)
}

func TestAIOperationsAuthAndUnconfigured(t *testing.T) {
	h := aiHandler(t, nil, nil)
	if w := aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unconfigured"`) || !strings.Contains(w.Body.String(), `"synced_at":null`) || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := aiCall(h, "GET", "/api/sidebar/v2/ai-operations?customer_id=99", "", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestAIOperationsWarehouseScopeAndLastMessage(t *testing.T) {
	for _, field := range []string{"customer", "corp", "employee", "version", "last", "role", "duplicate"} {
		t.Run(field, func(t *testing.T) {
			s := aiFixture()
			switch field {
			case "customer":
				s.CustomerID = 99
			case "corp":
				s.CorpID = "other"
			case "employee":
				s.EmployeeID = "other"
			case "version":
				s.SourceVersion = ""
			case "last":
				s.LastCustomerMessage = nil
			case "role":
				s.Messages[0].Role = "another_customer"
			case "duplicate":
				s.Messages[0].ID = s.Messages[1].ID
			}
			h := aiHandler(t, aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
				return s, nil
			}), nil)
			w := aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true)
			if w.Code != 503 {
				t.Fatal(w.Code)
			}
		})
	}
}
func TestAISuggestionFrozenBothSidesAndIdempotency(t *testing.T) {
	s := aiFixture()
	var inputs []AISuggestionInput
	h := aiHandler(t, aiReaderFunc(func(_ context.Context, p Principal, id customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
		if p.EmployeeID != "staff" || id != 42 {
			t.Fatal("wrong principal")
		}
		return s, nil
	}), aiGeneratorFunc(func(c context.Context, i AISuggestionInput) (string, error) {
		d, ok := c.Deadline()
		if !ok || time.Until(d) > aiGenerateTimeout {
			t.Fatal("unbounded model request")
		}
		inputs = append(inputs, i)
		return "建议人工确认时间", nil
	}))
	token := aiToken(t, h)
	w := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(token), true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"auto_sent":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(inputs) != 1 || len(inputs[0].Snapshot.Messages) != 2 || inputs[0].Snapshot.Messages[0].Role != "sales" || inputs[0].Snapshot.Messages[1].Role != "customer" {
		t.Fatal("lost one dialogue direction")
	}
	h.config.Now = func() time.Time { return time.Date(2026, 10, 9, 12, 1, 0, 0, time.UTC) }
	w = aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(aiToken(t, h)), true)
	if w.Code != 200 || inputs[0].IdempotencyKey != inputs[1].IdempotencyKey {
		t.Fatal("same content must keep remote idempotency key")
	}
}
func TestAISuggestionRejectsConfirmationTamperScopeAndExpiry(t *testing.T) {
	s := aiFixture()
	calls := 0
	h := aiHandler(t, aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
		return s, nil
	}), aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) { calls++; return "never", nil }))
	token := aiToken(t, h)
	for _, body := range []string{`{"confirmed":false}`, `{"confirmed":true,"snapshot_token":"bad"}`, `{"confirmed":true,"customer_id":99}`} {
		w := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", body, true)
		if w.Code != 400 && w.Code != 409 {
			t.Fatal(w.Code)
		}
	}
	for _, p := range []aiContext{{Principal{"corp", "other"}, 42}, {Principal{"other", "staff"}, 42}, {Principal{"corp", "staff"}, 43}} {
		h.config.Contexts = &p
		w := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(token), true)
		if w.Code != 409 && w.Code != 401 {
			t.Fatal("scope accepted", w.Code)
		}
	}
	h.config.Contexts = testContext{}
	h.config.Now = func() time.Time { return time.Date(2026, 10, 9, 12, 5, 0, 0, time.UTC) }
	if w := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(token), true); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if calls != 0 {
		t.Fatal("invalid request reached model")
	}
}
func TestAISuggestionRejectsChangesBeforeAndAfterModel(t *testing.T) {
	for _, when := range []string{"before", "after"} {
		t.Run(when, func(t *testing.T) {
			s := aiFixture()
			calls := 0
			h := aiHandler(t, aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
				return s, nil
			}), aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) {
				calls++
				s.Messages[0].Text = "新的销售回复"
				return "过期话术", nil
			}))
			token := aiToken(t, h)
			if when == "before" {
				s.Summary = "摘要变化"
			}
			w := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(token), true)
			if w.Code != 409 || strings.Contains(w.Body.String(), "过期话术") {
				t.Fatal(w.Code, w.Body.String())
			}
			if when == "before" && calls != 0 {
				t.Fatal("stale input reached model")
			}
		})
	}
}
func TestAISuggestionUnknownAndInvalidResults(t *testing.T) {
	for _, result := range []string{"timeout", "empty"} {
		t.Run(result, func(t *testing.T) {
			s := aiFixture()
			h := aiHandler(t, aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
				return s, nil
			}), aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) {
				if result == "timeout" {
					return "", context.DeadlineExceeded
				}
				return "", nil
			}))
			w := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(aiToken(t, h)), true)
			expected := 502
			if result == "timeout" {
				expected = 504
			}
			if w.Code != expected {
				t.Fatal(w.Code)
			}
		})
	}
}
func TestAIOperationsReadDoesNotGenerateAndFailureDoesNotLeak(t *testing.T) {
	s := aiFixture()
	s.SyncedAt = nil
	modelCalls := 0
	h := aiHandler(t, aiReaderFunc(func(c context.Context, _ Principal, _ customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
		if _, ok := c.Deadline(); !ok {
			t.Fatal("read unbounded")
		}
		return s, nil
	}), aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) { modelCalls++; return "", nil }))
	w := aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true)
	if w.Code != 200 || modelCalls != 0 || !strings.Contains(w.Body.String(), `"synced_at":null`) || strings.Contains(w.Body.String(), "您每周可投入") {
		t.Fatal(w.Code, w.Body.String())
	}
	h.config.AIWarehouse = aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
		return s, errors.New("private upstream details")
	})
	w = aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAICurrentViewerRevocationAndEmployeeChange(t *testing.T) {
	s := aiFixture()
	modelCalls := 0
	h := aiHandler(t, aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
		return s, nil
	}), aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) { modelCalls++; return "未发送", nil }))
	token := aiToken(t, h)
	for _, current := range []aiViewerFunc{func(context.Context, *http.Request) (Principal, error) { return Principal{"corp", "other"}, nil }, func(context.Context, *http.Request) (Principal, error) { return Principal{}, errors.New("revoked") }} {
		h.config.AICurrentViewer = current
		for _, w := range []*httptest.ResponseRecorder{aiCall(h, "GET", "/api/sidebar/v2/ai-operations", "", true), aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(token), true)} {
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
		}
	}
	if modelCalls != 0 {
		t.Fatal("wrong employee reached model")
	}
	h.config.AICurrentViewer = aiViewerFunc(func(context.Context, *http.Request) (Principal, error) { return Principal{"corp", "staff"}, nil })
	h.config.AISuggestions = aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) {
		h.config.AICurrentViewer = aiViewerFunc(func(context.Context, *http.Request) (Principal, error) { return Principal{}, errors.New("revoked") })
		return "私有参考", nil
	})
	w := aiCall(h, "POST", "/api/sidebar/v2/ai-operations/suggestions", aiBody(token), true)
	if w.Code != 401 || strings.Contains(w.Body.String(), "私有参考") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAIOperationsViewerChangeDuringWarehouseRead(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, change := range []string{"revoked", "employee", "corp"} {
			t.Run(method+"/"+change, func(t *testing.T) {
				s := aiFixture()
				changed, modelCalls := false, 0
				h := aiHandler(t, aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
					return s, nil
				}), aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) {
					modelCalls++
					return "private suggestion", nil
				}))
				token := aiToken(t, h)
				h.config.AICurrentViewer = aiViewerFunc(func(context.Context, *http.Request) (Principal, error) {
					if !changed {
						return Principal{"corp", "staff"}, nil
					}
					switch change {
					case "employee":
						return Principal{"corp", "other"}, nil
					case "corp":
						return Principal{"other", "staff"}, nil
					default:
						return Principal{}, errors.New("revoked")
					}
				})
				h.config.AIWarehouse = aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
					changed = true
					return s, nil
				})
				path, body := "/api/sidebar/v2/ai-operations", ""
				if method == "POST" {
					path += "/suggestions"
					body = aiBody(token)
				}
				w := aiCall(h, method, path, body, true)
				if w.Code != http.StatusUnauthorized || modelCalls != 0 || strings.Contains(w.Body.String(), s.Summary) || strings.Contains(w.Body.String(), "private suggestion") {
					t.Fatalf("viewer change accepted: status=%d model_calls=%d", w.Code, modelCalls)
				}
			})
		}
	}
}

func TestAIOperationsCancelledWarehouseReadIsRejected(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			s := aiFixture()
			modelCalls := 0
			h := aiHandler(t, aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
				return s, nil
			}), aiGeneratorFunc(func(context.Context, AISuggestionInput) (string, error) {
				modelCalls++
				return "private suggestion", nil
			}))
			token := aiToken(t, h)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.config.AIWarehouse = aiReaderFunc(func(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
				cancel()
				return s, nil // An adapter may return success after its caller cancels.
			})
			path, body := "/api/sidebar/v2/ai-operations", ""
			if method == "POST" {
				path += "/suggestions"
				body = aiBody(token)
			}
			r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer scoped")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, r)
			if w.Code != http.StatusServiceUnavailable || modelCalls != 0 || strings.Contains(w.Body.String(), s.Summary) || strings.Contains(w.Body.String(), "private suggestion") {
				t.Fatalf("cancelled read accepted: status=%d model_calls=%d", w.Code, modelCalls)
			}
		})
	}
}
