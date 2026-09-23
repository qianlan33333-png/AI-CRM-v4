package wecom

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type customerSyncHTTPStore struct {
	CustomerSyncStore
	run CustomerSyncRun
}

func (store customerSyncHTTPStore) Get(context.Context, int64) (CustomerSyncRun, error) {
	return store.run, nil
}

type customerSyncHTTPStatus struct {
	stats outboundport.ContactDescriptionRunStats
	calls int
}

func (status *customerSyncHTTPStatus) ContactDescriptionRunStats(_ context.Context, runID int64) (outboundport.ContactDescriptionRunStats, error) {
	status.calls++
	if runID < 1 {
		return outboundport.ContactDescriptionRunStats{}, ErrSyncNotFound
	}
	return status.stats, nil
}

type customerSyncHTTPReadbacks struct {
	calls int
	runID int64
}

func (scheduler *customerSyncHTTPReadbacks) ScheduleContactDescriptionReadbacksWithin(_ context.Context, runID int64) (int64, error) {
	scheduler.calls++
	scheduler.runID = runID
	return 2, nil
}

type customerSyncHTTPAuth struct {
	authPrincipal accessdomain.Principal
	authErr       error
	csrfPrincipal accessdomain.Principal
	csrfErr       error
}

func (auth customerSyncHTTPAuth) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return auth.authPrincipal, auth.authErr
}

func (auth customerSyncHTTPAuth) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return auth.csrfPrincipal, auth.csrfErr
}

func TestCustomerSyncMayWriteRequiresAdminKindAndDailyRole(t *testing.T) {
	tests := []struct {
		name      string
		principal accessdomain.Principal
		want      bool
	}{
		{name: "super administrator", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, want: true},
		{name: "administrator", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 2, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, want: true},
		{name: "viewer", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleViewer}}},
		{name: "staff cannot borrow admin role", principal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 4, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := customerSyncMayWrite(test.principal); got != test.want {
				t.Fatalf("customerSyncMayWrite=%t want=%t", got, test.want)
			}
		})
	}
}

func TestContactDescriptionBackfillRoutesRequireGateAndAuthorization(t *testing.T) {
	t.Run("disabled returns a clear unavailable result before any work", func(t *testing.T) {
		security := customerSyncHTTPAuth{csrfPrincipal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
		handler := CustomerSyncHTTPHandler{CSRF: security}.Routes()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/wecom/contact-description-backfills", nil))
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "contact_description_backfill_disabled") {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
	})
	t.Run("disabled endpoint does not reveal configuration to an unauthenticated caller", func(t *testing.T) {
		handler := CustomerSyncHTTPHandler{CSRF: customerSyncHTTPAuth{csrfErr: accessdomain.ErrAuthentication}}.Routes()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/wecom/contact-description-backfills", nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
	})
	t.Run("read route rejects an unauthenticated caller", func(t *testing.T) {
		status := &customerSyncHTTPStatus{}
		handler := CustomerSyncHTTPHandler{DescriptionEnabled: true, DescriptionStatus: status, DescriptionReadbacks: &customerSyncHTTPReadbacks{}, UOW: directUOW{}, Auth: customerSyncHTTPAuth{authErr: accessdomain.ErrAuthentication}}.Routes()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/contact-description-backfills/7", nil))
		if response.Code != http.StatusUnauthorized || status.calls != 0 {
			t.Fatalf("status=%d stats_calls=%d body=%q", response.Code, status.calls, response.Body.String())
		}
	})
	t.Run("read route rejects an authenticated non-administrator", func(t *testing.T) {
		status := &customerSyncHTTPStatus{}
		security := customerSyncHTTPAuth{authPrincipal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
		handler := CustomerSyncHTTPHandler{DescriptionEnabled: true, DescriptionStatus: status, DescriptionReadbacks: &customerSyncHTTPReadbacks{}, UOW: directUOW{}, Auth: security}.Routes()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/contact-description-backfills/7", nil))
		if response.Code != http.StatusForbidden || status.calls != 0 {
			t.Fatalf("status=%d stats_calls=%d body=%q", response.Code, status.calls, response.Body.String())
		}
	})
	t.Run("readback requires csrf and an administrator then only schedules reads", func(t *testing.T) {
		status := &customerSyncHTTPStatus{stats: outboundport.ContactDescriptionRunStats{Discovered: 3, Written: 2, ReadbackFailed: 2}}
		scheduler := &customerSyncHTTPReadbacks{}
		security := customerSyncHTTPAuth{csrfPrincipal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
		service := CustomerSyncService{Store: customerSyncHTTPStore{run: CustomerSyncRun{ID: 7}}, UOW: directUOW{}}
		handler := CustomerSyncHTTPHandler{Service: service, Auth: security, CSRF: security, DescriptionEnabled: true, DescriptionStatus: status, DescriptionReadbacks: scheduler, UOW: directUOW{}}.Routes()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/wecom/contact-description-backfills/7/readback", nil))
		if response.Code != http.StatusAccepted || scheduler.calls != 1 || scheduler.runID != 7 || status.calls != 1 || !strings.Contains(response.Body.String(), `"readback_scheduled":2`) {
			t.Fatalf("status=%d scheduler=%+v stats_calls=%d body=%q", response.Code, scheduler, status.calls, response.Body.String())
		}
	})
}

func TestContactDescriptionCoverageLeavesUnsubmittedProjectedPairsVisible(t *testing.T) {
	coverage, err := contactDescriptionCoverage(ContactDescriptionSourceCoverage{Observed: 5, Projected: 3, Omitted: 2}, outboundport.ContactDescriptionRunStats{Discovered: 2})
	if err != nil || coverage.Observed != 5 || coverage.Projected != 3 || coverage.Omitted != 2 || coverage.Submitted != 2 || coverage.NotSubmitted != 1 {
		t.Fatalf("coverage=%+v err=%v", coverage, err)
	}
	if _, err = contactDescriptionCoverage(ContactDescriptionSourceCoverage{Observed: 5, Projected: 3, Omitted: 2}, outboundport.ContactDescriptionRunStats{Discovered: 4}); !errors.Is(err, ErrSyncCAS) {
		t.Fatalf("over-submitted err=%v", err)
	}
}
