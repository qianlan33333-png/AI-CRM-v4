package http

import (
	"context"
	"encoding/json"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	access "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type receiptsApp struct {
	capturingIntegrationApplication
	query ai.RecipientPageQuery
	reads int
}

func (a *receiptsApp) ListRecipients(_ context.Context, q ai.RecipientPageQuery) (ai.RecipientPage, error) {
	a.query = q
	a.reads++
	return ai.RecipientPage{Items: []ai.Recipient{{ID: 17, PlanID: 5, CustomerID: 11965, CustomerName: "customer", StaffID: 10, StaffDisplayName: "staff", ContentVersionID: 20, ExecutionState: ai.ExecutionProviderAccepted, DeferredTarget: &ai.DeferredTarget{UnionID: "MUST_NOT_LEAK"}}}, NextCursor: "next-page"}, nil
}
func (a *receiptsApp) ListEffects(context.Context, ai.PlanID) ([]ai.EffectBinding, error) {
	return []ai.EffectBinding{{RecipientID: 17, EffectID: "eer_65771", ProviderAccepted: true}}, nil
}

type receiptsSecurity struct {
	role          access.Role
	authenticated bool
}

func (s receiptsSecurity) Authenticate(context.Context, *http.Request) (access.Principal, error) {
	if !s.authenticated {
		return access.Principal{}, access.ErrAuthentication
	}
	return access.Principal{Kind: access.KindAdmin, InternalID: 10, Roles: []access.Role{s.role}, SessionVersion: 4}, nil
}
func (s receiptsSecurity) AuthorizeCSRF(context.Context, *http.Request) (access.Principal, error) {
	panic("GET must not authorize a mutation")
}

type receiptsReader struct {
	calls int
	ref   string
	live  bool
}

func (s *receiptsReader) ReadExecutionReceipt(_ context.Context, ref string, live bool) (ai.ExecutionReceipt, error) {
	s.calls++
	s.ref = ref
	s.live = live
	return ai.ExecutionReceipt{Found: true, MessageID: "msg-1", SenderUserID: "HuangYouCan", CreationCodeEvidence: "not_recorded"}, nil
}

func TestExecutionReceiptsAdministratorReadAndIsolation(t *testing.T) {
	app, reader := &receiptsApp{}, &receiptsReader{}
	h, err := NewHandler(Config{Application: app, Security: receiptsSecurity{access.RoleAdmin, true}, Authorizer: accessapp.AIAssistantAuthorizer{}, ReceiptReader: reader})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/ai-assistant/plans/5/execution-receipts?live=true&limit=1&cursor=page-1", nil))
	if w.Code != 200 || reader.ref != "aiassistant:5:17:20" || !reader.live || app.query.Cursor != "page-1" || app.query.Limit != 1 {
		t.Fatalf("status=%d reader=%+v query=%+v", w.Code, reader, app.query)
	}
	if strings.Contains(w.Body.String(), "MUST_NOT_LEAK") || strings.Contains(w.Body.String(), "unionid") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("restricted identity or cache boundary failed")
	}
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body["next_cursor"] != "next-page" || body["read_only"] != true {
		t.Fatal(w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"creation_errcode":null`) {
		t.Fatal("missing historical code was invented")
	}
}

func TestExecutionReceiptsRejectsViewerAndUnauthenticatedBeforeReading(t *testing.T) {
	for _, tc := range []struct {
		security receiptsSecurity
		status   int
	}{{receiptsSecurity{access.RoleViewer, true}, 403}, {receiptsSecurity{access.RoleAdmin, false}, 401}} {
		app, reader := &receiptsApp{}, &receiptsReader{}
		h, _ := NewHandler(Config{Application: app, Security: tc.security, Authorizer: accessapp.AIAssistantAuthorizer{}, ReceiptReader: reader})
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/ai-assistant/plans/5/execution-receipts?live=true", nil))
		if w.Code != tc.status || app.reads != 0 || reader.calls != 0 {
			t.Fatalf("status=%d app=%+v reader=%+v", w.Code, app, reader)
		}
	}
}

func TestExecutionReceiptsInvalidLiveDoesNotRead(t *testing.T) {
	app, reader := &receiptsApp{}, &receiptsReader{}
	h, _ := NewHandler(Config{Application: app, Security: receiptsSecurity{access.RoleAdmin, true}, Authorizer: accessapp.AIAssistantAuthorizer{}, ReceiptReader: reader})
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/ai-assistant/plans/5/execution-receipts?live=resend", nil))
	if w.Code != 400 || app.reads != 0 || reader.calls != 0 {
		t.Fatal("invalid read selector entered execution")
	}
}
