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
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// These fakes stay at the stable App/Port boundary. They make the handler's
// authenticated route and error contracts executable without a database,
// Runner process, queue, or Provider.
type routeContractUOW struct{}

func (routeContractUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type routeContractEvents struct{}

func (routeContractEvents) Append(context.Context, operationport.Event) (operationport.EventID, error) {
	return 1, nil
}

type routeContractDeliveries struct{}

func (routeContractDeliveries) Accept(_ context.Context, _ operationport.EventID, consumer string) error {
	if consumer != operationport.ConsumerOperationCycleFact {
		return errors.New("unexpected operation-cycle event consumer")
	}
	return nil
}

type routeContractStore struct {
	err error

	runKey, actionRequestID string
	versionKey              string
	versionLimit            int32
	versionOffset           int32

	contextKey, contextMode string
	contextLimit            int32
	contextOffset           int32

	proposal      operationapp.ProposalCommand
	decisionID    string
	decision      string
	decisionActor string
	proposalCalls int
	decisionCalls int
}

func (store *routeContractStore) Report(context.Context, operationapp.ReportCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeContractStore) ListStrategies(context.Context, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) GetStrategy(context.Context, string) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) ListRuns(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) GetRun(_ context.Context, key string) (map[string]any, error) {
	store.runKey = key
	return map[string]any{"run_key": key}, store.err
}
func (store *routeContractStore) GetRunByOrdinal(context.Context, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) Start(context.Context, operationapp.StartCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeContractStore) CurrentAction(context.Context, string) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) GetActionResult(_ context.Context, requestID string) (map[string]any, error) {
	store.actionRequestID = requestID
	return map[string]any{"request_id": requestID}, store.err
}
func (store *routeContractStore) Claim(context.Context, string, string, time.Time, time.Duration) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeContractStore) RecordActionEvent(context.Context, operationapp.ActionEventCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeContractStore) RenewActionLease(context.Context, operationapp.ActionLeaseRenewalCommand, time.Time, time.Duration) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) Heartbeat(context.Context, operationapp.RunnerHeartbeatCommand, time.Time) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) ContextIndex(_ context.Context, limit, offset int32) (map[string]any, error) {
	store.contextLimit, store.contextOffset = limit, offset
	return map[string]any{"items": []any{}}, store.err
}
func (store *routeContractStore) StrategyContext(_ context.Context, key, mode string, limit, offset int32, _ map[string]string) (map[string]any, error) {
	store.contextKey, store.contextMode = key, mode
	store.contextLimit, store.contextOffset = limit, offset
	return map[string]any{"strategy_key": key, "mode": mode}, store.err
}
func (store *routeContractStore) CreateProposal(_ context.Context, command operationapp.ProposalCommand, _ time.Time) (map[string]any, bool, error) {
	store.proposalCalls++
	store.proposal = command
	return map[string]any{"proposal_id": "proposal-1", "state": "accepted"}, false, store.err
}
func (store *routeContractStore) ListProposals(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) DecideProposal(_ context.Context, id, decision, actor string, _ time.Time) (map[string]any, error) {
	store.decisionCalls++
	store.decisionID, store.decision, store.decisionActor = id, decision, actor
	return map[string]any{"proposal_id": id, "decision": decision}, store.err
}
func (store *routeContractStore) CreateStrategy(context.Context, operationapp.CreateStrategyCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeContractStore) UpdateStrategy(context.Context, operationapp.UpdateStrategyCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeContractStore) TransitionStrategy(context.Context, operationapp.TransitionStrategyCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeContractStore) ListStrategyVersions(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeContractStore) ListRunVersions(_ context.Context, key string, limit, offset int32) (map[string]any, error) {
	store.versionKey, store.versionLimit, store.versionOffset = key, limit, offset
	return map[string]any{"run_key": key, "items": []any{}}, store.err
}

var _ operationapp.Store = (*routeContractStore)(nil)
var _ platformport.UnitOfWork = routeContractUOW{}

type routeContractSecurity struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (security routeContractSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.authErr
}
func (security routeContractSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.csrfErr
}

func adminRouteContractSecurity() routeContractSecurity {
	return routeContractSecurity{principal: accessdomain.Principal{InternalID: 42, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
}

func newRouteContractHandler(t *testing.T, store *routeContractStore, security routeContractSecurity, token string) *Handler {
	t.Helper()
	handler, err := NewHandler(operationapp.NewService(routeContractUOW{}, store, routeContractEvents{}, routeContractDeliveries{}), security, token)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAdminReadAndProposalDecisionRouteContracts(t *testing.T) {
	store := &routeContractStore{}
	handler := newRouteContractHandler(t, store, adminRouteContractSecurity(), "")

	for _, test := range []struct {
		path, field, want string
	}{
		{"/api/admin/operation-cycles/runs/run.weekly.001", "run_key", "run.weekly.001"},
		{"/api/admin/operation-cycles/action-requests/request-9/result", "request_id", "request-9"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"`+test.field+`":"`+test.want+`"`) {
			t.Fatalf("path=%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}
	if store.runKey != "run.weekly.001" || store.actionRequestID != "request-9" {
		t.Fatalf("read forwarding run=%q action=%q", store.runKey, store.actionRequestID)
	}
	authDeniedStore := &routeContractStore{}
	authDeniedSecurity := adminRouteContractSecurity()
	authDeniedSecurity.authErr = errors.New("session absent")
	authDenied := httptest.NewRecorder()
	newRouteContractHandler(t, authDeniedStore, authDeniedSecurity, "").ServeHTTP(authDenied, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/runs/run.weekly.001", nil))
	if authDenied.Code != http.StatusUnauthorized || authDeniedStore.runKey != "" {
		t.Fatalf("admin read authentication status=%d run=%q body=%s", authDenied.Code, authDeniedStore.runKey, authDenied.Body.String())
	}

	versions := httptest.NewRecorder()
	handler.ServeHTTP(versions, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/runs/run.weekly.001/versions?limit=2&offset=3", nil))
	if versions.Code != http.StatusOK || store.versionKey != "run.weekly.001" || store.versionLimit != 2 || store.versionOffset != 3 {
		t.Fatalf("versions status=%d key=%q page=%d/%d body=%s", versions.Code, store.versionKey, store.versionLimit, store.versionOffset, versions.Body.String())
	}

	store.err = operationapp.ErrUnavailable
	unavailable := httptest.NewRecorder()
	handler.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/runs/run.weekly.001/versions", nil))
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("unavailable status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
	store.err = operationapp.ErrNotFound
	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/action-requests/missing/result", nil))
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Body.String(), `"code":"not_found"`) {
		t.Fatalf("not found status=%d body=%s", notFound.Code, notFound.Body.String())
	}

	store.err = nil
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"accept"}`)))
	if accepted.Code != http.StatusOK || store.decisionID != "proposal-1" || store.decision != "accept" || store.decisionActor != "42" {
		t.Fatalf("decision status=%d values=%q/%q/%q body=%s", accepted.Code, store.decisionID, store.decision, store.decisionActor, accepted.Body.String())
	}
	decisionsBeforeInvalid := store.decisionCalls
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"hold"}`)))
	if invalid.Code != http.StatusBadRequest || store.decisionCalls != decisionsBeforeInvalid {
		t.Fatalf("invalid decision status=%d decision calls=%d body=%s", invalid.Code, store.decisionCalls, invalid.Body.String())
	}
	store.err = operationapp.ErrConflict
	conflict := httptest.NewRecorder()
	handler.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"accept"}`)))
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"conflict"`) {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	csrfStore := &routeContractStore{}
	csrfSecurity := adminRouteContractSecurity()
	csrfSecurity.csrfErr = errors.New("csrf")
	csrfDenied := httptest.NewRecorder()
	newRouteContractHandler(t, csrfStore, csrfSecurity, "").ServeHTTP(csrfDenied, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"accept"}`)))
	if csrfDenied.Code != http.StatusUnauthorized || csrfStore.decisionCalls != 0 {
		t.Fatalf("csrf denial status=%d decision calls=%d body=%s", csrfDenied.Code, csrfStore.decisionCalls, csrfDenied.Body.String())
	}
}

func TestRunnerContextAndProposalRouteContracts(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	store := &routeContractStore{}
	handler := newRouteContractHandler(t, store, routeContractSecurity{}, token)

	indexRequest := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/context-index?limit=4&offset=2", nil)
	indexRequest.Header.Set("Authorization", "Bearer "+token)
	index := httptest.NewRecorder()
	handler.ServeHTTP(index, indexRequest)
	if index.Code != http.StatusOK || store.contextLimit != 4 || store.contextOffset != 2 {
		t.Fatalf("index status=%d page=%d/%d body=%s", index.Code, store.contextLimit, store.contextOffset, index.Body.String())
	}

	contextRequest := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/strategies/weekly.review/context?mode=review&limit=3&offset=1", nil)
	contextRequest.Header.Set("Authorization", "Bearer "+token)
	strategyContext := httptest.NewRecorder()
	handler.ServeHTTP(strategyContext, contextRequest)
	if strategyContext.Code != http.StatusOK || store.contextKey != "weekly.review" || store.contextMode != "review" || store.contextLimit != 3 || store.contextOffset != 1 {
		t.Fatalf("context status=%d values=%q/%q/%d/%d body=%s", strategyContext.Code, store.contextKey, store.contextMode, store.contextLimit, store.contextOffset, strategyContext.Body.String())
	}

	wrongToken := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/context-index", nil)
	wrongToken.Header.Set("Authorization", "Bearer wrong")
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, wrongToken)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}

	store.err = operationapp.ErrUnavailable
	unavailableRequest := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/context-index", nil)
	unavailableRequest.Header.Set("Authorization", "Bearer "+token)
	unavailable := httptest.NewRecorder()
	handler.ServeHTTP(unavailable, unavailableRequest)
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("runner unavailable status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
	store.err = nil

	proposalRequest := httptest.NewRequest(http.MethodPost, "/api/operation-cycles/strategy-change-proposals", strings.NewReader(`{"schema_version":"operation_cycle_strategy_change_proposal.v1","strategy_key":"weekly.review","change":"pause"}`))
	proposalRequest.Header.Set("Authorization", "Bearer "+token)
	proposalRequest.Header.Set("Idempotency-Key", "proposal-route-0001")
	proposal := httptest.NewRecorder()
	handler.ServeHTTP(proposal, proposalRequest)
	if proposal.Code != http.StatusAccepted || store.proposal.IdempotencyKey != "proposal-route-0001" || store.proposal.ActorID != "operation-cycle-service" || store.proposal.Payload["strategy_key"] != "weekly.review" {
		t.Fatalf("proposal status=%d command=%+v body=%s", proposal.Code, store.proposal, proposal.Body.String())
	}

	proposalsBeforeMalformed := store.proposalCalls
	malformedRequest := httptest.NewRequest(http.MethodPost, "/api/operation-cycles/strategy-change-proposals", strings.NewReader(`{"schema_version":`))
	malformedRequest.Header.Set("Authorization", "Bearer "+token)
	malformedRequest.Header.Set("Idempotency-Key", "proposal-malformed-route-0001")
	malformed := httptest.NewRecorder()
	handler.ServeHTTP(malformed, malformedRequest)
	if malformed.Code != http.StatusBadRequest || store.proposalCalls != proposalsBeforeMalformed {
		t.Fatalf("malformed proposal status=%d proposal calls=%d body=%s", malformed.Code, store.proposalCalls, malformed.Body.String())
	}

	invalidModeRequest := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/strategies/weekly.review/context?mode=wrong", nil)
	invalidModeRequest.Header.Set("Authorization", "Bearer "+token)
	invalidMode := httptest.NewRecorder()
	handler.ServeHTTP(invalidMode, invalidModeRequest)
	if invalidMode.Code != http.StatusBadRequest {
		t.Fatalf("invalid context mode status=%d body=%s", invalidMode.Code, invalidMode.Body.String())
	}
}
