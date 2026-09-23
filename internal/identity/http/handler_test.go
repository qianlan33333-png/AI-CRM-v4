package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
)

type transactionMarker struct{}

type testUnitOfWork struct {
	calls   int
	lastErr error
}

func (unit *testUnitOfWork) Within(ctx context.Context, callback func(context.Context) error) error {
	unit.calls++
	unit.lastErr = callback(context.WithValue(ctx, transactionMarker{}, true))
	return unit.lastErr
}

type testAuditor struct {
	event            platformaudit.Event
	err              error
	calls            int
	transactionBound bool
}

func (auditor *testAuditor) Append(ctx context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	auditor.calls++
	auditor.event = event
	auditor.transactionBound = ctx.Value(transactionMarker{}) == true
	return event, auditor.err
}

type testSecurity struct {
	authPrincipal accessdomain.Principal
	authErr       error
	csrfPrincipal accessdomain.Principal
	csrfErr       error
}

func (security *testSecurity) Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return security.authPrincipal, security.authErr
}

func (security *testSecurity) AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return security.csrfPrincipal, security.csrfErr
}

type testOneID struct {
	resolveResult        identityport.ResolveResult
	resolveErr           error
	resolveInput         identitydomain.Reference
	resolveCalls         int
	confirmResult        identityapp.LinkResult
	confirmErr           error
	confirmInput         identityapp.ConfirmMergeCommand
	confirmCalls         int
	reverseResult        identityapp.MergeRecord
	reverseErr           error
	reverseID            int64
	sourceConflict       identityport.HXCSourceConflict
	sourceConflictReplay bool
	sourceConflictErr    error
	sourceConflictInput  identityport.IgnoreHXCSourceConflictCommand
}

func (service *testOneID) Resolve(ctx context.Context, input identitydomain.Reference) (identityport.ResolveResult, error) {
	if ctx.Value(transactionMarker{}) != true {
		return identityport.ResolveResult{}, errors.New("resolve was not transaction bound")
	}
	service.resolveCalls++
	service.resolveInput = input
	return service.resolveResult, service.resolveErr
}

func (service *testOneID) ConfirmMerge(ctx context.Context, input identityapp.ConfirmMergeCommand) (identityapp.LinkResult, error) {
	if ctx.Value(transactionMarker{}) != true {
		return identityapp.LinkResult{}, errors.New("confirm was not transaction bound")
	}
	service.confirmCalls++
	service.confirmInput = input
	return service.confirmResult, service.confirmErr
}

func (service *testOneID) RevertConfirmedMerge(ctx context.Context, mergeID int64) (identityapp.MergeRecord, error) {
	if ctx.Value(transactionMarker{}) != true {
		return identityapp.MergeRecord{}, errors.New("reverse was not transaction bound")
	}
	service.reverseID = mergeID
	return service.reverseResult, service.reverseErr
}

func (service *testOneID) IgnoreHXCSourceConflict(ctx context.Context, input identityport.IgnoreHXCSourceConflictCommand) (identityport.HXCSourceConflict, bool, error) {
	if ctx.Value(transactionMarker{}) != true {
		return identityport.HXCSourceConflict{}, false, errors.New("source conflict write was not transaction bound")
	}
	service.sourceConflictInput = input
	return service.sourceConflict, service.sourceConflictReplay, service.sourceConflictErr
}

type testQueries struct {
	detail             query.CustomerDetail
	detailErr          error
	customerID         customerdomain.CustomerID
	conflictPage       query.ConflictPage
	conflictOptions    query.ListOptions
	candidatePage      query.MergeCandidatePage
	candidateOptions   query.ListOptions
	hxcConflictPage    query.HXCSourceConflictPage
	hxcConflictOptions query.ListOptions
}

func (queries *testQueries) Customer(ctx context.Context, id customerdomain.CustomerID) (query.CustomerDetail, error) {
	if ctx.Value(transactionMarker{}) != true {
		return query.CustomerDetail{}, errors.New("customer query was not transaction bound")
	}
	queries.customerID = id
	return queries.detail, queries.detailErr
}

func (queries *testQueries) Conflicts(ctx context.Context, options query.ListOptions) (query.ConflictPage, error) {
	if ctx.Value(transactionMarker{}) != true {
		return query.ConflictPage{}, errors.New("conflict query was not transaction bound")
	}
	queries.conflictOptions = options
	return queries.conflictPage, nil
}

func (queries *testQueries) MergeCandidates(ctx context.Context, options query.ListOptions) (query.MergeCandidatePage, error) {
	if ctx.Value(transactionMarker{}) != true {
		return query.MergeCandidatePage{}, errors.New("candidate query was not transaction bound")
	}
	queries.candidateOptions = options
	return queries.candidatePage, nil
}

func (queries *testQueries) HXCSourceConflicts(ctx context.Context, options query.ListOptions) (query.HXCSourceConflictPage, error) {
	if ctx.Value(transactionMarker{}) != true {
		return query.HXCSourceConflictPage{}, errors.New("HXC source conflict query was not transaction bound")
	}
	queries.hxcConflictOptions = options
	return queries.hxcConflictPage, nil
}

func TestResolveRejectsClientTrustFieldsAndNeverProvisions(t *testing.T) {
	for _, field := range []string{
		`"assurance":"verified"`, `"verified":true`, `"source":"provider"`,
	} {
		t.Run(field, func(t *testing.T) {
			service := &testOneID{}
			handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, nil)
			body := `{"kind":"unionid","scope":"wechat-open-platform:one","value":"secret",` + field + `}`
			response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve", body, true)
			if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if service.resolveCalls != 0 {
				t.Fatalf("resolve calls=%d", service.resolveCalls)
			}
		})
	}

	service := &testOneID{resolveResult: identityport.ResolveResult{Status: identityport.ResolveNotFound}}
	unit := &testUnitOfWork{}
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, unit)
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve",
		`{"kind":"unionid","scope":"wechat-open-platform:one","value":"secret"}`, true)
	if response.Code != nethttp.StatusOK || response.Body.String() != "{\"status\":\"not_found\"}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if service.resolveCalls != 1 || service.resolveInput.Assurance != identitydomain.AssuranceDeclared ||
		service.resolveInput.Source != "oneid-admin-resolve" || unit.calls != 1 {
		t.Fatalf("resolve input=%#v calls=%d uow=%d", service.resolveInput, service.resolveCalls, unit.calls)
	}
}

func TestResolveDoesNotCreateCustomerOrIdentity(t *testing.T) {
	store := identityapp.NewMemoryStore()
	service := &identityapp.OneIDService{Store: store}
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, nil)
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve",
		`{"kind":"ext","scope":"ext:admin-search","value":"not-present"}`, true)
	if response.Code != nethttp.StatusOK || !strings.Contains(response.Body.String(), `"status":"not_found"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if store.CustomerCount() != 0 || store.ActiveIdentityCount() != 0 {
		t.Fatalf("resolve created customers=%d identities=%d", store.CustomerCount(), store.ActiveIdentityCount())
	}
}

func TestReadAuthenticationAndWriteAuthorization(t *testing.T) {
	tests := []struct {
		name           string
		auth           accessdomain.Principal
		authErr        error
		csrf           accessdomain.Principal
		csrfErr        error
		method, target string
		body           string
		wantStatus     int
		wantCode       string
	}{
		{name: "missing session", authErr: accessdomain.ErrAuthentication, method: nethttp.MethodGet,
			target: "/api/admin/oneid/conflicts", wantStatus: nethttp.StatusUnauthorized, wantCode: "authentication_required"},
		{name: "customer principal", auth: accessdomain.Principal{Kind: accessdomain.KindCustomer, InternalID: 9}, method: nethttp.MethodGet,
			target: "/api/admin/oneid/conflicts", wantStatus: nethttp.StatusForbidden, wantCode: "permission_denied"},
		{name: "missing csrf", csrfErr: accessdomain.ErrCSRFRequired, method: nethttp.MethodPost,
			target: "/api/admin/oneid/merge-candidates/1/confirm", body: `{"survivor_customer_id":1}`,
			wantStatus: nethttp.StatusForbidden, wantCode: "csrf_required"},
		{name: "reverse missing csrf", csrfErr: accessdomain.ErrCSRFRequired, method: nethttp.MethodPost,
			target:     "/api/admin/oneid/merges/1/reverse",
			wantStatus: nethttp.StatusForbidden, wantCode: "csrf_required"},
		{name: "viewer", csrf: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 8, Roles: []accessdomain.Role{accessdomain.RoleViewer}}, method: nethttp.MethodPost,
			target: "/api/admin/oneid/merge-candidates/1/confirm", body: `{"survivor_customer_id":1}`,
			wantStatus: nethttp.StatusForbidden, wantCode: "permission_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			security := &testSecurity{authPrincipal: test.auth, authErr: test.authErr, csrfPrincipal: test.csrf, csrfErr: test.csrfErr}
			handler := configuredHandler(t, &testUnitOfWork{}, security, &testOneID{}, &testQueries{}, &testAuditor{})
			response := perform(handler, test.method, test.target, test.body, test.body != "")
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantCode) {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestConfirmOperatorComesOnlyFromPrincipal(t *testing.T) {
	service := &testOneID{confirmResult: identityapp.LinkResult{
		Status: identityapp.LinkMerged, CustomerID: 20,
		Merge: &identityapp.MergeRecord{ID: 71, CandidateID: 8, FromCustomerID: 10, ToCustomerID: 20},
	}}
	auditor := &testAuditor{}
	unit := &testUnitOfWork{}
	security := &testSecurity{authPrincipal: activeAdmin(), csrfPrincipal: activeAdmin()}
	handler := configuredHandler(t, unit, security, service, &testQueries{}, auditor)
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/merge-candidates/8/confirm",
		`{"survivor_customer_id":20,"operator":"forged"}`, true)
	if response.Code != nethttp.StatusBadRequest || service.confirmCalls != 0 {
		t.Fatalf("forged operator status=%d calls=%d body=%q", response.Code, service.confirmCalls, response.Body.String())
	}
	response = perform(handler, nethttp.MethodPost, "/api/admin/oneid/merge-candidates/8/confirm",
		`{"survivor_customer_id":20}`, true)
	if response.Code != nethttp.StatusOK || service.confirmCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%q", response.Code, service.confirmCalls, response.Body.String())
	}
	if service.confirmInput.CandidateID != 8 || service.confirmInput.SurvivorCustomerID != 20 || service.confirmInput.Operator != "admin:7" {
		t.Fatalf("command=%#v", service.confirmInput)
	}
	if auditor.calls != 1 || !auditor.transactionBound || auditor.event.Action != "identity.merge_confirmed" ||
		auditor.event.ActorType != "admin" || auditor.event.ActorID != "7" ||
		auditor.event.ResourceType != "customer_merge" || auditor.event.ResourceID != "71" ||
		string(auditor.event.IdempotencyKey) != "identity:merge-confirmed:71" {
		t.Fatalf("audit calls=%d transaction=%v event=%#v", auditor.calls, auditor.transactionBound, auditor.event)
	}
	if payload := string(auditor.event.Payload); strings.Contains(payload, "forged") || strings.Contains(payload, "operator") ||
		!strings.Contains(payload, `"candidate_id":8`) || !strings.Contains(payload, `"survivor_customer_id":20`) {
		t.Fatalf("audit payload=%s", payload)
	}
}

func TestStaleCandidateRejectionCommitsAndReturnsConflictWithoutMergeAudit(t *testing.T) {
	service := &testOneID{confirmResult: identityapp.LinkResult{
		Status: identityapp.LinkCandidateRejected, CustomerID: 20,
		Candidate: &identityapp.MergeCandidate{ID: 8, Status: "rejected"},
	}}
	auditor := &testAuditor{}
	unit := &testUnitOfWork{}
	security := &testSecurity{authPrincipal: activeAdmin(), csrfPrincipal: activeSuperAdmin()}
	handler := configuredHandler(t, unit, security, service, &testQueries{}, auditor)

	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/merge-candidates/8/confirm",
		`{"survivor_customer_id":20}`, true)

	if response.Code != nethttp.StatusConflict || response.Body.String() != "{\"candidate_id\":8,\"error\":\"identity_conflict\",\"ok\":false}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if unit.calls != 1 || unit.lastErr != nil {
		t.Fatalf("uow calls=%d error=%v", unit.calls, unit.lastErr)
	}
	if service.confirmCalls != 1 || auditor.calls != 0 {
		t.Fatalf("confirm calls=%d audit calls=%d", service.confirmCalls, auditor.calls)
	}
}

func TestCustomerAndMutationResponsesCannotRepresentPIIOrEvidence(t *testing.T) {
	now := time.Now().UTC()
	queries := &testQueries{detail: query.CustomerDetail{
		CustomerID: 1, Status: customerdomain.StatusMerged, CanonicalCustomerID: 2, CanonicalStatus: customerdomain.StatusActive,
		Identities:   []query.IdentitySummary{{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:tenant"}},
		MergeLineage: []query.MergeLineageSummary{{ID: 9, FromCustomerID: 1, ToCustomerID: 2, ReversibleStatus: "not_reversed", MergedAt: now}},
	}}
	service := &testOneID{reverseResult: identityapp.MergeRecord{
		ID: 9, CandidateID: 5, FromCustomerID: 1, ToCustomerID: 2, Operator: "secret-operator",
		Evidence: identitydomain.LinkEvidence{Digest: "raw-secret-digest", EventID: "external-user-secret"},
	}}
	auditor := &testAuditor{}
	security := &testSecurity{authPrincipal: activeAdmin(), csrfPrincipal: activeSuperAdmin()}
	handler := configuredHandler(t, &testUnitOfWork{}, security, service, queries, auditor)
	responses := []*httptest.ResponseRecorder{
		perform(handler, nethttp.MethodGet, "/api/admin/oneid/customers/1", "", false),
		perform(handler, nethttp.MethodPost, "/api/admin/oneid/merges/9/reverse", "", false),
	}
	for _, response := range responses {
		if response.Code != nethttp.StatusOK {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, forbidden := range []string{"normalized_value", "external-user-secret", "+13800138000", "raw-secret-digest", "secret-operator", "evidence", "operator"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("response exposed %q: %s", forbidden, body)
			}
		}
	}
	if auditor.calls != 1 || !auditor.transactionBound || auditor.event.Action != "identity.merge_reversed" ||
		auditor.event.ActorType != "admin" || auditor.event.ActorID != "1" || auditor.event.ResourceID != "9" ||
		string(auditor.event.IdempotencyKey) != "identity:merge-reversed:9" {
		t.Fatalf("reverse audit calls=%d transaction=%v event=%#v", auditor.calls, auditor.transactionBound, auditor.event)
	}
	for _, forbidden := range []string{"external-user-secret", "raw-secret-digest", "secret-operator", "operator", "evidence"} {
		if strings.Contains(string(auditor.event.Payload), forbidden) {
			t.Fatalf("reverse audit exposed %q: %s", forbidden, auditor.event.Payload)
		}
	}
}

func TestAuditFailureReturnsUnitOfWorkError(t *testing.T) {
	auditErr := errors.New("audit unavailable")
	tests := []struct {
		name, target, body string
		service            *testOneID
	}{
		{name: "confirm", target: "/api/admin/oneid/merge-candidates/8/confirm", body: `{"survivor_customer_id":20}`,
			service: &testOneID{confirmResult: identityapp.LinkResult{Status: identityapp.LinkMerged, CustomerID: 20,
				Merge: &identityapp.MergeRecord{ID: 71, CandidateID: 8, FromCustomerID: 10, ToCustomerID: 20}}}},
		{name: "reverse", target: "/api/admin/oneid/merges/71/reverse",
			service: &testOneID{reverseResult: identityapp.MergeRecord{ID: 71, CandidateID: 8, FromCustomerID: 10, ToCustomerID: 20}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unit := &testUnitOfWork{}
			auditor := &testAuditor{err: auditErr}
			security := &testSecurity{authPrincipal: activeAdmin(), csrfPrincipal: activeSuperAdmin()}
			handler := configuredHandler(t, unit, security, test.service, &testQueries{}, auditor)
			response := perform(handler, nethttp.MethodPost, test.target, test.body, test.body != "")
			if response.Code != nethttp.StatusInternalServerError || response.Body.String() != "{\"error\":\"internal_error\",\"ok\":false}\n" {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if !errors.Is(unit.lastErr, auditErr) || auditor.calls != 1 || !auditor.transactionBound {
				t.Fatalf("uow error=%v audit calls=%d transaction=%v", unit.lastErr, auditor.calls, auditor.transactionBound)
			}
		})
	}
}

func TestListPaginationDefaultsBoundsAndStatusWhitelist(t *testing.T) {
	queries := &testQueries{conflictPage: query.ConflictPage{Items: []query.Conflict{}}, candidatePage: query.MergeCandidatePage{Items: []query.MergeCandidate{}}}
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), &testOneID{}, queries, nil)
	response := perform(handler, nethttp.MethodGet, "/api/admin/oneid/conflicts", "", false)
	if response.Code != nethttp.StatusOK || queries.conflictOptions != (query.ListOptions{Status: "open", Limit: 50}) {
		t.Fatalf("status=%d options=%#v body=%q", response.Code, queries.conflictOptions, response.Body.String())
	}
	response = perform(handler, nethttp.MethodGet, "/api/admin/oneid/merge-candidates?status=confirmed&limit=100&offset=2", "", false)
	if response.Code != nethttp.StatusOK || queries.candidateOptions != (query.ListOptions{Status: "confirmed", Limit: 100, Offset: 2}) {
		t.Fatalf("status=%d options=%#v body=%q", response.Code, queries.candidateOptions, response.Body.String())
	}
	for _, target := range []string{
		"/api/admin/oneid/conflicts?limit=101", "/api/admin/oneid/conflicts?status=deleted",
		"/api/admin/oneid/conflicts?limit=1&limit=2", "/api/admin/oneid/conflicts?cursor=secret",
	} {
		response = perform(handler, nethttp.MethodGet, target, "", false)
		if response.Code != nethttp.StatusBadRequest {
			t.Fatalf("target=%q status=%d body=%q", target, response.Code, response.Body.String())
		}
	}
}

func TestHXCSourceConflictsAreSafeAndIgnoreIsSuperAdminCAS(t *testing.T) {
	queries := &testQueries{hxcConflictPage: query.HXCSourceConflictPage{Items: []query.HXCSourceConflict{
		{
			ID: 9, SubjectRef: "HXC-aabbccddeeff", Reason: "unionid_phone_cross_root", LeftCustomerID: 11, RightCustomerID: 22,
			EvidenceDigest: strings.Repeat("a", 64), Status: "open", Version: 3,
			Observations: []query.HXCSourceObservation{{Kind: "unionid", Scope: "wechat-open-platform:app", DisplayValue: "stored", Assurance: "verified", Status: "active"}, {Kind: "phone", Scope: "phone:cn11", DisplayValue: "138****8000", Assurance: "declared", Status: "active"}},
		},
	}}}
	service := &testOneID{sourceConflict: identityport.HXCSourceConflict{ID: 9, SubjectRef: "HXC-aabbccddeeff", Reason: identityport.HXCReasonCrossRoot, Status: "ignored", Version: 4}}
	auditor := &testAuditor{}
	security := &testSecurity{authPrincipal: activeAdmin(), csrfPrincipal: activeSuperAdmin()}
	handler := configuredHandler(t, &testUnitOfWork{}, security, service, queries, auditor)

	response := perform(handler, nethttp.MethodGet, "/api/admin/oneid/source-conflicts?source=hxc&status=open&limit=100&offset=0", "", false)
	if response.Code != nethttp.StatusOK || queries.hxcConflictOptions != (query.ListOptions{Status: "open", Limit: 100}) {
		t.Fatalf("status=%d options=%#v body=%q", response.Code, queries.hxcConflictOptions, response.Body.String())
	}
	for _, forbidden := range []string{"raw-unionid", "normalized_value", "13800138000"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("source conflict response leaked %q: %s", forbidden, response.Body.String())
		}
	}

	request := httptest.NewRequest(nethttp.MethodPost, "/api/admin/oneid/source-conflicts/9/ignore", strings.NewReader(`{"expected_version":3,"reason":"source_data_error"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "hxc-ignore-case-9")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.sourceConflictInput.ConflictID != 9 || service.sourceConflictInput.ExpectedVersion != 3 || service.sourceConflictInput.Operator != "admin:1" || service.sourceConflictInput.IdempotencyKey != "hxc-ignore-case-9" {
		t.Fatalf("status=%d input=%#v body=%q", response.Code, service.sourceConflictInput, response.Body.String())
	}
	if auditor.calls != 1 || auditor.event.Action != "identity.hxc_source_conflict_ignored" || !auditor.transactionBound {
		t.Fatalf("audit=%#v calls=%d transaction=%v", auditor.event, auditor.calls, auditor.transactionBound)
	}
}

func TestBodyLimitAndInternalErrorsAreSafe(t *testing.T) {
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), &testOneID{}, &testQueries{}, nil)
	large := `{"kind":"ext","scope":"ext:test","value":"` + strings.Repeat("x", int(maxRequestBodyBytes)) + `"}`
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve", large, true)
	if response.Code != nethttp.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "invalid_request") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	service := &testOneID{resolveErr: errors.New("SELECT normalized_value FROM customer_identities: password=secret")}
	handler = newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, nil)
	response = perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve",
		`{"kind":"ext","scope":"ext:test","value":"opaque"}`, true)
	if response.Code != nethttp.StatusInternalServerError || response.Body.String() != "{\"error\":\"internal_error\",\"ok\":false}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	service = &testOneID{resolveErr: identityapp.ErrInsufficientLinkEvidence}
	handler = newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, nil)
	response = perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve",
		`{"kind":"ext","scope":"ext:test","value":"opaque"}`, true)
	if response.Code != nethttp.StatusConflict || !strings.Contains(response.Body.String(), `"identity_conflict"`) {
		t.Fatalf("insufficient evidence status=%d body=%q", response.Code, response.Body.String())
	}
}

func newTestHandler(t *testing.T, read, write accessdomain.Principal, service OneIDService, queries *testQueries, unit *testUnitOfWork) nethttp.Handler {
	t.Helper()
	if unit == nil {
		unit = &testUnitOfWork{}
	}
	security := &testSecurity{authPrincipal: read, csrfPrincipal: write}
	return configuredHandler(t, unit, security, service, queries, &testAuditor{})
}

func configuredHandler(t *testing.T, unit *testUnitOfWork, security *testSecurity, service OneIDService, queries *testQueries, auditor Auditor) nethttp.Handler {
	t.Helper()
	manager, ok := service.(identityport.HXCSourceConflictManager)
	if !ok {
		manager = &testOneID{}
	}
	handler, err := NewHandler(Config{UnitOfWork: unit, Authenticator: security, CSRF: security, OneID: service, SourceConflicts: manager, Queries: queries, Audit: auditor})
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}

func perform(handler nethttp.Handler, method, target, body string, jsonBody bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if jsonBody {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func activeAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
}

func activeSuperAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
}
