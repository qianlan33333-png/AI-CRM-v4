package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
)

type entrantAdminTransactionMarker struct{}

type entrantAdminTestUOW struct {
	calls   int
	lastErr error
}

func (unit *entrantAdminTestUOW) Within(ctx context.Context, function func(context.Context) error) error {
	unit.calls++
	unit.lastErr = function(context.WithValue(ctx, entrantAdminTransactionMarker{}, true))
	return unit.lastErr
}

type entrantAdminTestSecurity struct {
	authPrincipal accessdomain.Principal
	authErr       error
	csrfPrincipal accessdomain.Principal
	csrfErr       error
}

func (security *entrantAdminTestSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.authPrincipal, security.authErr
}

func (security *entrantAdminTestSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.csrfPrincipal, security.csrfErr
}

type entrantAdminTestStore struct {
	items            []EntrantReceipt
	listPage         EntrantReceiptPage
	reconcileCalls   int
	reconcileCommand ReconcileEntrantReceipt
	firstCommand     *ReconcileEntrantReceipt
	transaction      bool
	err              error
	now              time.Time
}

func (store *entrantAdminTestStore) ListUnassigned(ctx context.Context, page EntrantReceiptPage) ([]EntrantReceipt, error) {
	store.transaction = ctx.Value(entrantAdminTransactionMarker{}) == true
	store.listPage = page
	return store.items, store.err
}

func (store *entrantAdminTestStore) ReconcileAdmin(ctx context.Context, command ReconcileEntrantReceipt) (EntrantReceipt, bool, error) {
	store.transaction = ctx.Value(entrantAdminTransactionMarker{}) == true
	store.reconcileCalls++
	store.reconcileCommand = command
	if store.err != nil {
		return EntrantReceipt{}, false, store.err
	}
	if store.firstCommand == nil {
		copy := command
		store.firstCommand = &copy
		return store.reconciledReceipt(command), true, nil
	}
	if command.OperationKeyDigest != store.firstCommand.OperationKeyDigest || command.CommandDigest != store.firstCommand.CommandDigest {
		return EntrantReceipt{}, false, ErrEntrantReceiptConflict
	}
	return store.reconciledReceipt(*store.firstCommand), false, nil
}

func (store *entrantAdminTestStore) reconciledReceipt(command ReconcileEntrantReceipt) EntrantReceipt {
	value := command.ReconciledAt
	return EntrantReceipt{
		ID: command.ReceiptID, InboxID: 10, ChangeType: "add_external_contact",
		Status: EntrantReconciled, PriorStatus: command.ExpectedStatus,
		BindingID: command.BindingID, ChannelID: 21, AssetKind: AcquisitionAssetQRCode,
		AssetVersion: 3, CustomerID: command.CustomerID,
		OccurredAt: store.now.Add(-time.Hour), ReconciledAt: &value, CreatedAt: store.now.Add(-2 * time.Hour),
	}
}

type entrantAdminTestAuditor struct {
	calls       int
	event       platformaudit.Event
	transaction bool
	err         error
}

func (auditor *entrantAdminTestAuditor) Append(ctx context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	auditor.calls++
	auditor.event = event
	auditor.transaction = ctx.Value(entrantAdminTransactionMarker{}) == true
	return event, auditor.err
}

func TestEntrantAdminReadsRequireCurrentAdminOrStaffAndBoundPagination(t *testing.T) {
	tests := []struct {
		name       string
		principal  accessdomain.Principal
		authErr    error
		wantStatus int
	}{
		{name: "missing session", authErr: accessdomain.ErrAuthentication, wantStatus: http.StatusUnauthorized},
		{name: "customer denied", principal: accessdomain.Principal{Kind: accessdomain.KindCustomer, InternalID: 3}, wantStatus: http.StatusForbidden},
		{name: "admin", principal: entrantAdminAdmin(), wantStatus: http.StatusOK},
		{name: "staff", principal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 4}, wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &entrantAdminTestStore{}
			handler := entrantAdminTestHandler(t, &entrantAdminTestUOW{}, &entrantAdminTestSecurity{
				authPrincipal: test.principal, authErr: test.authErr, csrfPrincipal: entrantAdminSuperAdmin(),
			}, store, &entrantAdminTestAuditor{}, time.Now)
			response := entrantAdminRequest(handler, http.MethodGet, "/api/admin/channel-acquisition-entrant-receipts/unassigned?before_id=19&limit=7", "", false, "")
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
			}
			if test.wantStatus == http.StatusOK && (store.listPage != (EntrantReceiptPage{BeforeID: 19, Limit: 7}) || !store.transaction) {
				t.Fatalf("page=%+v transaction=%v", store.listPage, store.transaction)
			}
		})
	}
}

func TestEntrantAdminReconcileRequiresCSRFSuperAdminAndAtomicallyAudits(t *testing.T) {
	body := `{"expected_status":"channel_unmatched","binding_id":44,"customer_id":55,"reason":"verified local binding"}`
	for _, test := range []struct {
		name       string
		security   *entrantAdminTestSecurity
		wantStatus int
		wantCode   string
	}{
		{name: "csrf", security: &entrantAdminTestSecurity{csrfErr: accessdomain.ErrCSRFRequired}, wantStatus: 403, wantCode: "csrf_required"},
		{name: "viewer role", security: &entrantAdminTestSecurity{csrfPrincipal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 8, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, wantStatus: 403, wantCode: "permission_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &entrantAdminTestStore{}
			handler := entrantAdminTestHandler(t, &entrantAdminTestUOW{}, test.security, store, &entrantAdminTestAuditor{}, time.Now)
			response := entrantAdminRequest(handler, http.MethodPost, "/api/admin/channel-acquisition-entrant-receipts/8/reconcile", body, true, "entrant-reconcile-key")
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantCode) || store.reconcileCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%q", response.Code, store.reconcileCalls, response.Body.String())
			}
		})
	}

	now := time.Date(2026, 9, 3, 3, 4, 5, 123000000, time.UTC)
	unit := &entrantAdminTestUOW{}
	store := &entrantAdminTestStore{now: now}
	auditor := &entrantAdminTestAuditor{}
	handler := entrantAdminTestHandler(t, unit, &entrantAdminTestSecurity{csrfPrincipal: entrantAdminAdmin()}, store, auditor, func() time.Time { return now })
	for iteration := range 2 {
		response := entrantAdminRequest(handler, http.MethodPost, "/api/admin/channel-acquisition-entrant-receipts/8/reconcile", body, true, "entrant-reconcile-key")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"replayed":`+[]string{"false", "true"}[iteration]) {
			t.Fatalf("iteration=%d status=%d body=%q", iteration, response.Code, response.Body.String())
		}
	}
	if store.reconcileCalls != 2 || auditor.calls != 1 || unit.calls != 2 || !store.transaction || !auditor.transaction {
		t.Fatalf("reconcile=%d audit=%d uow=%d store_tx=%v audit_tx=%v", store.reconcileCalls, auditor.calls, unit.calls, store.transaction, auditor.transaction)
	}
	command := store.reconcileCommand
	if command.ReceiptID != 8 || command.ExpectedStatus != EntrantChannelUnmatched || command.BindingID != 44 ||
		command.CustomerID != customerdomain.CustomerID(55) || command.ActorAdminUserID != 7 || command.Reason != "verified local binding" ||
		command.OperationKeyDigest == [32]byte{} || command.CommandDigest == [32]byte{} {
		t.Fatalf("command=%+v", command)
	}
	if auditor.event.Action != "channel.acquisition_entrant_reconciled" || auditor.event.ActorID != "7" ||
		auditor.event.ResourceID != "8" || auditor.event.IdempotencyKey == "" || !auditor.event.OccurredAt.Equal(now) {
		t.Fatalf("audit=%+v", auditor.event)
	}
	for _, forbidden := range []string{"verified local binding", "reason", "operation", "digest", "external_userid", "state"} {
		if strings.Contains(strings.ToLower(string(auditor.event.Payload)), forbidden) {
			t.Fatalf("audit payload exposed %q: %s", forbidden, auditor.event.Payload)
		}
	}

	drift := `{"expected_status":"channel_unmatched","binding_id":45,"customer_id":55,"reason":"verified local binding"}`
	response := entrantAdminRequest(handler, http.MethodPost, "/api/admin/channel-acquisition-entrant-receipts/8/reconcile", drift, true, "entrant-reconcile-key")
	if response.Code != http.StatusConflict || auditor.calls != 1 {
		t.Fatalf("drift status=%d audits=%d body=%q", response.Code, auditor.calls, response.Body.String())
	}
}

func TestEntrantAdminAuditFailureAbortsUOW(t *testing.T) {
	now := time.Date(2026, 9, 3, 3, 4, 5, 0, time.UTC)
	unit := &entrantAdminTestUOW{}
	auditErr := errors.New("audit unavailable")
	handler := entrantAdminTestHandler(t, unit, &entrantAdminTestSecurity{csrfPrincipal: entrantAdminSuperAdmin()},
		&entrantAdminTestStore{now: now}, &entrantAdminTestAuditor{err: auditErr}, func() time.Time { return now })
	response := entrantAdminRequest(handler, http.MethodPost, "/api/admin/channel-acquisition-entrant-receipts/8/reconcile",
		`{"expected_status":"channel_ambiguous","binding_id":44,"customer_id":55,"reason":"review completed"}`, true, "entrant-audit-fail")
	if response.Code != http.StatusInternalServerError || !errors.Is(unit.lastErr, auditErr) {
		t.Fatalf("status=%d uow_err=%v body=%q", response.Code, unit.lastErr, response.Body.String())
	}
}

func TestEntrantAdminStrictJSONBodyAndQueryLimits(t *testing.T) {
	handler := entrantAdminTestHandler(t, &entrantAdminTestUOW{}, &entrantAdminTestSecurity{
		authPrincipal: entrantAdminAdmin(), csrfPrincipal: entrantAdminSuperAdmin(),
	}, &entrantAdminTestStore{}, &entrantAdminTestAuditor{}, time.Now)
	validPrefix := `{"expected_status":"channel_unmatched","binding_id":1,"customer_id":2,"reason":"fixed"`
	for _, test := range []struct {
		name, method, target, body, key string
		jsonBody                        bool
		wantStatus                      int
	}{
		{name: "unknown", method: http.MethodPost, target: "/api/admin/channel-acquisition-entrant-receipts/1/reconcile", body: validPrefix + `,"extra":true}`, key: "entrant-key-1", jsonBody: true, wantStatus: 400},
		{name: "trailing", method: http.MethodPost, target: "/api/admin/channel-acquisition-entrant-receipts/1/reconcile", body: validPrefix + `}{}`, key: "entrant-key-2", jsonBody: true, wantStatus: 400},
		{name: "media", method: http.MethodPost, target: "/api/admin/channel-acquisition-entrant-receipts/1/reconcile", body: `{}`, key: "entrant-key-3", wantStatus: 400},
		{name: "missing key", method: http.MethodPost, target: "/api/admin/channel-acquisition-entrant-receipts/1/reconcile", body: `{}`, jsonBody: true, wantStatus: 400},
		{name: "oversized", method: http.MethodPost, target: "/api/admin/channel-acquisition-entrant-receipts/1/reconcile", body: `{"expected_status":"channel_unmatched","binding_id":1,"customer_id":2,"reason":"` + strings.Repeat("x", int(entrantAdminMaxBodyBytes)) + `"}`, key: "entrant-key-4", jsonBody: true, wantStatus: 413},
		{name: "unknown query", method: http.MethodGet, target: "/api/admin/channel-acquisition-entrant-receipts/unassigned?cursor=x", wantStatus: 400},
		{name: "malformed query", method: http.MethodGet, target: "/api/admin/channel-acquisition-entrant-receipts/unassigned?limit=%zz", wantStatus: 400},
		{name: "duplicate query", method: http.MethodGet, target: "/api/admin/channel-acquisition-entrant-receipts/unassigned?limit=1&limit=2", wantStatus: 400},
		{name: "limit", method: http.MethodGet, target: "/api/admin/channel-acquisition-entrant-receipts/unassigned?limit=101", wantStatus: 400},
		{name: "write query", method: http.MethodPost, target: "/api/admin/channel-acquisition-entrant-receipts/1/reconcile?limit=1", body: validPrefix + `}`, key: "entrant-key-5", jsonBody: true, wantStatus: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := entrantAdminRequest(handler, test.method, test.target, test.body, test.jsonBody, test.key)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/channel-acquisition-entrant-receipts/1/reconcile",
		strings.NewReader(validPrefix+`}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Add("Idempotency-Key", "entrant-key-a")
	request.Header.Add("Idempotency-Key", "entrant-key-b")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate idempotency header status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestEntrantAdminResponseUsesExplicitSafeShape(t *testing.T) {
	now := time.Date(2026, 9, 3, 2, 3, 4, 0, time.UTC)
	store := &entrantAdminTestStore{now: now, items: []EntrantReceipt{{
		ID: 7, InboxID: 8, ChangeType: "add_external_contact",
		Status: EntrantChannelUnmatched, PriorStatus: EntrantChannelUnmatched,
		CustomerID: 99, OccurredAt: now.Add(-time.Hour), CreatedAt: now,
	}}}
	handler := entrantAdminTestHandler(t, &entrantAdminTestUOW{}, &entrantAdminTestSecurity{authPrincipal: entrantAdminAdmin()}, store, &entrantAdminTestAuditor{}, time.Now)
	response := entrantAdminRequest(handler, http.MethodGet, "/api/admin/channel-acquisition-entrant-receipts/unassigned", "", false, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var envelope struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Items) != 1 {
		t.Fatalf("decode err=%v body=%s", err, response.Body.String())
	}
	wantKeys := []string{"change_type", "created_at", "customer_id", "id", "inbox_id", "occurred_at", "prior_status", "status"}
	gotKeys := make([]string, 0, len(envelope.Items[0]))
	for key := range envelope.Items[0] {
		gotKeys = append(gotKeys, key)
	}
	entrantSortStrings(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("response keys=%v body=%s", gotKeys, response.Body.String())
	}
	for _, forbidden := range []string{"external_userid", "state", "digest", "raw", "operation", "reason", "command"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, response.Body.String())
		}
	}
}

func entrantAdminTestHandler(t *testing.T, unit *entrantAdminTestUOW, security *entrantAdminTestSecurity, store *entrantAdminTestStore, auditor *entrantAdminTestAuditor, now func() time.Time) http.Handler {
	t.Helper()
	handler, err := NewEntrantAdminHandler(EntrantAdminConfig{
		UnitOfWork: unit, Authenticator: security, CSRF: security,
		Receipts: store, Audit: auditor, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}

func entrantAdminRequest(handler http.Handler, method, target, body string, jsonBody bool, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if jsonBody {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func entrantAdminAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
}

func entrantAdminSuperAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
}

func entrantSortStrings(values []string) {
	for left := range values {
		for right := left + 1; right < len(values); right++ {
			if values[right] < values[left] {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
}
