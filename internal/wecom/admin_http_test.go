package wecom

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
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

type callbackAdminTransactionMarker struct{}

type callbackAdminTestUOW struct {
	calls   int
	lastErr error
}

func (unit *callbackAdminTestUOW) Within(ctx context.Context, function func(context.Context) error) error {
	unit.calls++
	unit.lastErr = function(context.WithValue(ctx, callbackAdminTransactionMarker{}, true))
	return unit.lastErr
}

type callbackAdminTestSecurity struct {
	authPrincipal accessdomain.Principal
	authErr       error
	csrfPrincipal accessdomain.Principal
	csrfErr       error
}

func (security *callbackAdminTestSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.authPrincipal, security.authErr
}

func (security *callbackAdminTestSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.csrfPrincipal, security.csrfErr
}

type callbackAdminTestStore struct {
	items        []CallbackReceipt
	detail       CallbackReceipt
	listPage     CallbackReceiptPage
	beginCalls   int
	beginCommand BeginCallbackRetry
	firstCommand *BeginCallbackRetry
	transaction  bool
	beginErr     error
}

func (store *callbackAdminTestStore) List(ctx context.Context, page CallbackReceiptPage) ([]CallbackReceipt, error) {
	store.transaction = ctx.Value(callbackAdminTransactionMarker{}) == true
	store.listPage = page
	return store.items, nil
}

func (store *callbackAdminTestStore) Get(ctx context.Context, _ int64) (CallbackReceipt, error) {
	store.transaction = ctx.Value(callbackAdminTransactionMarker{}) == true
	return store.detail, nil
}

func (store *callbackAdminTestStore) BeginRetry(ctx context.Context, command BeginCallbackRetry) (CallbackReceipt, bool, error) {
	store.transaction = ctx.Value(callbackAdminTransactionMarker{}) == true
	store.beginCalls++
	store.beginCommand = command
	if store.beginErr != nil {
		return CallbackReceipt{}, false, store.beginErr
	}
	if store.firstCommand == nil {
		copy := command
		store.firstCommand = &copy
		return CallbackReceipt{
			ID: 91, InboxID: 44, Kind: CallbackReceiptRetryRequested,
			TargetReceiptID: command.TargetReceiptID, AttemptNumber: command.ExpectedAttempt,
			PriorInboxStatus: command.ExpectedInboxStatus, ResultingInboxStatus: webhook.StatusRetryable,
			CreatedAt: time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC),
		}, true, nil
	}
	if command.OperationKeyDigest != store.firstCommand.OperationKeyDigest || command.CommandDigest != store.firstCommand.CommandDigest {
		return CallbackReceipt{}, false, ErrCallbackReceiptConflict
	}
	return CallbackReceipt{
		ID: 91, InboxID: 44, Kind: CallbackReceiptRetryRequested,
		TargetReceiptID: command.TargetReceiptID, AttemptNumber: command.ExpectedAttempt,
		PriorInboxStatus: command.ExpectedInboxStatus, ResultingInboxStatus: webhook.StatusRetryable,
		CreatedAt: time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC),
	}, false, nil
}

type callbackAdminTestRetrier struct {
	calls       int
	retry       webhook.Retry
	transaction bool
	err         error
}

func (retrier *callbackAdminTestRetrier) Retry(ctx context.Context, retry webhook.Retry) (webhook.Delivery, error) {
	retrier.calls++
	retrier.retry = retry
	retrier.transaction = ctx.Value(callbackAdminTransactionMarker{}) == true
	return webhook.Delivery{}, retrier.err
}

func TestCallbackAdminReadsRequireCurrentAdminOrStaffAndBoundPagination(t *testing.T) {
	tests := []struct {
		name       string
		principal  accessdomain.Principal
		authErr    error
		wantStatus int
	}{
		{name: "missing session", authErr: accessdomain.ErrAuthentication, wantStatus: http.StatusUnauthorized},
		{name: "customer denied", principal: accessdomain.Principal{Kind: accessdomain.KindCustomer, InternalID: 3}, wantStatus: http.StatusForbidden},
		{name: "admin", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleViewer}}, wantStatus: http.StatusOK},
		{name: "staff", principal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 4}, wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &callbackAdminTestStore{}
			handler := callbackAdminTestHandler(t, &callbackAdminTestUOW{}, &callbackAdminTestSecurity{
				authPrincipal: test.principal, authErr: test.authErr, csrfPrincipal: callbackAdminSuperAdmin(),
			}, store, &callbackAdminTestRetrier{})
			response := callbackAdminRequest(handler, http.MethodGet, "/api/admin/wecom/callback-receipts?before_id=19&limit=7", "", false, "")
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
			}
			if test.wantStatus == http.StatusOK && (store.listPage != (CallbackReceiptPage{BeforeID: 19, Limit: 7}) || !store.transaction) {
				t.Fatalf("page=%+v transaction=%v", store.listPage, store.transaction)
			}
		})
	}
}

func TestCallbackAdminRetryRequiresCSRFDailyAdministratorAndStableIdempotency(t *testing.T) {
	requestBody := `{"expected_status":"failed","expected_attempt":2,"reason":"configuration corrected"}`
	tests := []struct {
		name       string
		security   *callbackAdminTestSecurity
		wantStatus int
		wantCode   string
	}{
		{name: "csrf", security: &callbackAdminTestSecurity{csrfErr: accessdomain.ErrCSRFRequired}, wantStatus: http.StatusForbidden, wantCode: "csrf_required"},
		{name: "viewer", security: &callbackAdminTestSecurity{csrfPrincipal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, wantStatus: http.StatusForbidden, wantCode: "permission_denied"},
		{name: "staff", security: &callbackAdminTestSecurity{csrfPrincipal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 8, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, wantStatus: http.StatusForbidden, wantCode: "permission_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &callbackAdminTestStore{}
			handler := callbackAdminTestHandler(t, &callbackAdminTestUOW{}, test.security, store, &callbackAdminTestRetrier{})
			response := callbackAdminRequest(handler, http.MethodPost, "/api/admin/wecom/callback-receipts/8/retry", requestBody, true, "callback-retry-key")
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantCode) || store.beginCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%q", response.Code, store.beginCalls, response.Body.String())
			}
		})
	}

	adminStore := &callbackAdminTestStore{}
	adminHandler := callbackAdminTestHandler(t, &callbackAdminTestUOW{}, &callbackAdminTestSecurity{csrfPrincipal: callbackAdminAdmin()}, adminStore, &callbackAdminTestRetrier{})
	adminResponse := callbackAdminRequest(adminHandler, http.MethodPost, "/api/admin/wecom/callback-receipts/8/retry", requestBody, true, "callback-admin-key")
	if adminResponse.Code != http.StatusOK || adminStore.beginCalls != 1 {
		t.Fatalf("admin status=%d calls=%d body=%q", adminResponse.Code, adminStore.beginCalls, adminResponse.Body.String())
	}

	unit := &callbackAdminTestUOW{}
	store := &callbackAdminTestStore{}
	retrier := &callbackAdminTestRetrier{}
	handler := callbackAdminTestHandler(t, unit, &callbackAdminTestSecurity{csrfPrincipal: callbackAdminSuperAdmin()}, store, retrier)
	for iteration := range 2 {
		response := callbackAdminRequest(handler, http.MethodPost, "/api/admin/wecom/callback-receipts/8/retry", requestBody, true, "callback-retry-key")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"replayed":`+[]string{"false", "true"}[iteration]) {
			t.Fatalf("iteration=%d status=%d body=%q", iteration, response.Code, response.Body.String())
		}
	}
	if store.beginCalls != 2 || retrier.calls != 1 || !store.transaction || !retrier.transaction || unit.calls != 2 {
		t.Fatalf("begin=%d retry=%d store_tx=%v retry_tx=%v uow=%d", store.beginCalls, retrier.calls, store.transaction, retrier.transaction, unit.calls)
	}
	if store.beginCommand.TargetReceiptID != 8 || store.beginCommand.ActorAdminUserID != 1 || store.beginCommand.ExpectedAttempt != 2 ||
		store.beginCommand.ExpectedInboxStatus != webhook.StatusFailed || retrier.retry != (webhook.Retry{ID: 44, Provider: callbackProvider, ExpectedAttempt: 2, ExpectedStatus: webhook.StatusFailed}) {
		t.Fatalf("command=%+v retry=%+v", store.beginCommand, retrier.retry)
	}
	if store.beginCommand.OperationKeyDigest == [32]byte{} || store.beginCommand.CommandDigest == [32]byte{} {
		t.Fatal("operation and command digests must be present")
	}

	drift := `{"expected_status":"failed","expected_attempt":2,"reason":"different reason"}`
	response := callbackAdminRequest(handler, http.MethodPost, "/api/admin/wecom/callback-receipts/8/retry", drift, true, "callback-retry-key")
	if response.Code != http.StatusConflict || retrier.calls != 1 {
		t.Fatalf("drift status=%d retries=%d body=%q", response.Code, retrier.calls, response.Body.String())
	}
}

func TestCallbackAdminRetryCASFailureAbortsUOW(t *testing.T) {
	unit := &callbackAdminTestUOW{}
	retrier := &callbackAdminTestRetrier{err: webhook.ErrConcurrentUpdate}
	handler := callbackAdminTestHandler(t, unit, &callbackAdminTestSecurity{csrfPrincipal: callbackAdminSuperAdmin()}, &callbackAdminTestStore{}, retrier)
	response := callbackAdminRequest(handler, http.MethodPost, "/api/admin/wecom/callback-receipts/8/retry",
		`{"expected_status":"retryable","expected_attempt":3,"reason":"dependency repaired"}`, true, "callback-retry-cas")
	if response.Code != http.StatusConflict || !errors.Is(unit.lastErr, webhook.ErrConcurrentUpdate) || retrier.calls != 1 {
		t.Fatalf("status=%d uow_err=%v retries=%d body=%q", response.Code, unit.lastErr, retrier.calls, response.Body.String())
	}
}

func TestCallbackAdminStrictJSONBodyAndQueryLimits(t *testing.T) {
	handler := callbackAdminTestHandler(t, &callbackAdminTestUOW{}, &callbackAdminTestSecurity{
		authPrincipal: callbackAdminAdmin(), csrfPrincipal: callbackAdminSuperAdmin(),
	}, &callbackAdminTestStore{}, &callbackAdminTestRetrier{})
	for _, test := range []struct {
		name, method, target, body, key string
		jsonBody                        bool
		wantStatus                      int
	}{
		{name: "unknown", method: http.MethodPost, target: "/api/admin/wecom/callback-receipts/1/retry", body: `{"expected_status":"failed","expected_attempt":1,"reason":"fixed","extra":true}`, key: "callback-key-1", jsonBody: true, wantStatus: 400},
		{name: "trailing", method: http.MethodPost, target: "/api/admin/wecom/callback-receipts/1/retry", body: `{"expected_status":"failed","expected_attempt":1,"reason":"fixed"}{}`, key: "callback-key-2", jsonBody: true, wantStatus: 400},
		{name: "media", method: http.MethodPost, target: "/api/admin/wecom/callback-receipts/1/retry", body: `{}`, key: "callback-key-3", wantStatus: 400},
		{name: "missing key", method: http.MethodPost, target: "/api/admin/wecom/callback-receipts/1/retry", body: `{}`, jsonBody: true, wantStatus: 400},
		{name: "oversized", method: http.MethodPost, target: "/api/admin/wecom/callback-receipts/1/retry", body: `{"expected_status":"failed","expected_attempt":1,"reason":"` + strings.Repeat("x", int(callbackAdminMaxBodyBytes)) + `"}`, key: "callback-key-4", jsonBody: true, wantStatus: 413},
		{name: "unknown query", method: http.MethodGet, target: "/api/admin/wecom/callback-receipts?cursor=x", wantStatus: 400},
		{name: "malformed query", method: http.MethodGet, target: "/api/admin/wecom/callback-receipts?limit=%zz", wantStatus: 400},
		{name: "duplicate query", method: http.MethodGet, target: "/api/admin/wecom/callback-receipts?limit=1&limit=2", wantStatus: 400},
		{name: "limit", method: http.MethodGet, target: "/api/admin/wecom/callback-receipts?limit=101", wantStatus: 400},
		{name: "detail query", method: http.MethodGet, target: "/api/admin/wecom/callback-receipts/1?limit=1", wantStatus: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := callbackAdminRequest(handler, test.method, test.target, test.body, test.jsonBody, test.key)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wecom/callback-receipts/1/retry",
		strings.NewReader(`{"expected_status":"failed","expected_attempt":1,"reason":"fixed"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Add("Idempotency-Key", "callback-key-a")
	request.Header.Add("Idempotency-Key", "callback-key-b")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate idempotency header status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestCallbackAdminResponseUsesExplicitSafeShape(t *testing.T) {
	now := time.Date(2026, 9, 3, 2, 3, 4, 0, time.UTC)
	receipt := CallbackReceipt{
		ID: 7, InboxID: 8, Kind: CallbackReceiptProcessing, AttemptNumber: 2,
		EventType: "change_external_contact", ChangeType: "add_external_contact",
		PriorInboxStatus: webhook.StatusProcessing, ResultingInboxStatus: webhook.StatusFailed,
		ResultCodes: []CallbackResultCode{CallbackFailedTerminal}, ErrorCode: "identity_conflict_terminal", CreatedAt: now,
	}
	store := &callbackAdminTestStore{items: []CallbackReceipt{receipt}, detail: receipt}
	handler := callbackAdminTestHandler(t, &callbackAdminTestUOW{}, &callbackAdminTestSecurity{authPrincipal: callbackAdminAdmin()}, store, &callbackAdminTestRetrier{})
	response := callbackAdminRequest(handler, http.MethodGet, "/api/admin/wecom/callback-receipts/7", "", false, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"attempt_number", "change_type", "created_at", "error_code", "event_type", "id", "inbox_id", "kind", "prior_inbox_status", "result_codes", "resulting_inbox_status"}
	gotKeys := make([]string, 0, len(value))
	for key := range value {
		gotKeys = append(gotKeys, key)
	}
	sortStrings(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("response keys=%v body=%s", gotKeys, response.Body.String())
	}
	for _, forbidden := range []string{"external_userid", "state", "digest", "raw", "operation", "reason", "command"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, response.Body.String())
		}
	}
}

func callbackAdminTestHandler(t *testing.T, unit *callbackAdminTestUOW, security *callbackAdminTestSecurity, store *callbackAdminTestStore, retrier *callbackAdminTestRetrier) http.Handler {
	t.Helper()
	handler, err := NewCallbackAdminHandler(CallbackAdminConfig{
		UnitOfWork: unit, Authenticator: security, CSRF: security, Receipts: store, Retrier: retrier,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}

func callbackAdminRequest(handler http.Handler, method, target, body string, jsonBody bool, key string) *httptest.ResponseRecorder {
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

func callbackAdminAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
}

func callbackAdminSuperAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
}

func sortStrings(values []string) {
	for left := range values {
		for right := left + 1; right < len(values); right++ {
			if values[right] < values[left] {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
}
