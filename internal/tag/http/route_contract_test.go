package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type routeContractTagSecurity struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (security routeContractTagSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.authErr
}
func (security routeContractTagSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.csrfErr
}

type routeContractTagGate struct {
	value domain.ExecutionGate
	err   error
}

func (gate routeContractTagGate) Get(context.Context) (domain.ExecutionGate, error) {
	return gate.value, gate.err
}

type routeContractSyncStore struct {
	handlerStore
	command      tagport.SyncCommand
	reserveCalls int
}

func (store *routeContractSyncStore) ReserveSync(_ context.Context, command tagport.SyncCommand) (tagport.SyncReceipt, error) {
	store.reserveCalls++
	store.command = command
	return tagport.SyncReceipt{ID: 17, Command: command, State: tagport.SyncReserved}, nil
}
func (store *routeContractSyncStore) AcceptSync(_ context.Context, receiptID, eventID int64, effect tagport.SyncEffectReceipt) (tagport.SyncReceipt, error) {
	return tagport.SyncReceipt{ID: receiptID, Command: store.command, State: tagport.SyncAccepted, EventID: eventID, Effect: effect}, nil
}

func newRouteContractTagHandler(t *testing.T, security routeContractTagSecurity, gate routeContractTagGate, store *routeContractSyncStore) *Handler {
	t.Helper()
	handler, err := NewHandler(
		tagapp.NewService(handlerUOW{}, store, nil, nil, nil),
		tagapp.NewSyncService(handlerUOW{}, store, handlerSyncEvents{}, handlerSyncEnqueuer{}),
		gate,
		security,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestTagLiveGateRouteContract(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	gate := domain.ExecutionGate{LocalCommandAcceptanceAvailable: true, LocalQueueAvailable: true, ObservedAt: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)}
	store := &routeContractSyncStore{}
	handler := newRouteContractTagHandler(t, routeContractTagSecurity{principal: admin}, routeContractTagGate{value: gate}, store)

	success := httptest.NewRecorder()
	handler.ServeHTTP(success, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/live/gate", nil))
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"local_command_acceptance_available":true`) {
		t.Fatalf("live gate success status=%d body=%s", success.Code, success.Body.String())
	}

	unauthenticated := httptest.NewRecorder()
	newRouteContractTagHandler(t, routeContractTagSecurity{authErr: errors.New("session absent")}, routeContractTagGate{value: gate}, store).ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/live/gate", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("live gate unauthenticated status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}

	missingInternalID := httptest.NewRecorder()
	viewer := accessdomain.Principal{Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	newRouteContractTagHandler(t, routeContractTagSecurity{principal: viewer}, routeContractTagGate{value: gate}, store).ServeHTTP(missingInternalID, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/live/gate", nil))
	if missingInternalID.Code != http.StatusForbidden {
		t.Fatalf("live gate missing internal ID status=%d body=%s", missingInternalID.Code, missingInternalID.Body.String())
	}

	unavailable := httptest.NewRecorder()
	newRouteContractTagHandler(t, routeContractTagSecurity{principal: admin}, routeContractTagGate{err: errors.New("gate unavailable")}, store).ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/live/gate", nil))
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"error":"unavailable"`) {
		t.Fatalf("live gate unavailable status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
}

func TestTagSyncDueRouteContract(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	store := &routeContractSyncStore{}
	handler := newRouteContractTagHandler(t, routeContractTagSecurity{principal: admin}, routeContractTagGate{}, store)

	request := httptest.NewRequest(http.MethodPost, "/api/admin/wecom/tags/sync-due", strings.NewReader(`{"trace_id":"route-contract"}`))
	request.Header.Set("Idempotency-Key", "tag-sync-due-route-0001")
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusAccepted || store.command.Kind != tagport.SyncDue || store.command.Actor != 9 || store.command.IdempotencyKey != "tag-sync-due-route-0001" || store.command.TraceID != "route-contract" || !strings.Contains(accepted.Body.String(), `"state":"queued"`) {
		t.Fatalf("sync due status=%d command=%+v body=%s", accepted.Code, store.command, accepted.Body.String())
	}

	csrfStore := &routeContractSyncStore{}
	csrfDenied := httptest.NewRecorder()
	newRouteContractTagHandler(t, routeContractTagSecurity{principal: admin, csrfErr: errors.New("csrf")}, routeContractTagGate{}, csrfStore).ServeHTTP(csrfDenied, httptest.NewRequest(http.MethodPost, "/api/admin/wecom/tags/sync-due", strings.NewReader(`{}`)))
	if csrfDenied.Code != http.StatusForbidden || csrfStore.reserveCalls != 0 {
		t.Fatalf("sync due csrf status=%d reserve calls=%d body=%s", csrfDenied.Code, csrfStore.reserveCalls, csrfDenied.Body.String())
	}

	unknownStore := &routeContractSyncStore{}
	unknownJSON := httptest.NewRecorder()
	newRouteContractTagHandler(t, routeContractTagSecurity{principal: admin}, routeContractTagGate{}, unknownStore).ServeHTTP(unknownJSON, httptest.NewRequest(http.MethodPost, "/api/admin/wecom/tags/sync-due", strings.NewReader(`{"extra":true}`)))
	if unknownJSON.Code != http.StatusBadRequest || unknownStore.reserveCalls != 0 {
		t.Fatalf("sync due unknown JSON status=%d reserve calls=%d body=%s", unknownJSON.Code, unknownStore.reserveCalls, unknownJSON.Body.String())
	}
}
