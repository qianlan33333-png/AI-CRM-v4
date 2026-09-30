package wecom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type directoryHTTPFixture struct {
	CustomerSyncStore
	calls    int
	customer customerdomain.CustomerID
	query    wecomport.TagHistoryQuery
}

func (f *directoryHTTPFixture) DirectoryCustomerProfile(_ context.Context, id customerdomain.CustomerID) (wecomport.DirectoryCustomerProfile, error) {
	f.calls++
	f.customer = id
	return wecomport.DirectoryCustomerProfile{CustomerID: id, Availability: "active"}, nil
}
func (f *directoryHTTPFixture) CustomerTagHistory(_ context.Context, q wecomport.TagHistoryQuery) (wecomport.TagHistoryPage, error) {
	f.calls++
	f.query = q
	return wecomport.TagHistoryPage{Items: []wecomport.TagHistoryEvent{{ID: 7, CustomerID: q.CustomerID, EventType: "baseline", RegistrationDate: "2026-09-30"}}}, nil
}
func (f *directoryHTTPFixture) CustomerTagHistoryStatistics(_ context.Context, q wecomport.TagHistoryQuery) ([]wecomport.TagHistoryStatistic, error) {
	f.calls++
	f.query = q
	return []wecomport.TagHistoryStatistic{{Date: "2026-09-30", EventType: "baseline", CustomerCount: 1, EventCount: 2}}, nil
}

type directoryCanonicalFixture struct{}

func (directoryCanonicalFixture) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: 42, Merged: true}, nil
}

func TestDirectoryReadAuthorizationCanonicalIdentityAndBoundedHistory(t *testing.T) {
	for _, test := range []struct {
		path      string
		principal accessdomain.Principal
		authErr   error
		status    int
		calls     int
	}{
		{path: "/api/admin/wecom/customer-profiles/9", authErr: accessdomain.ErrAuthentication, status: 401},
		{path: "/api/admin/wecom/customer-profiles/9", principal: accessdomain.Principal{Kind: accessdomain.KindStaff}, status: 403},
		{path: "/api/admin/wecom/customer-profiles/09", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin}, status: 400},
		{path: "/api/admin/wecom/customer-profiles/9/tag-history?from_date=2026-10-02&to_date=2026-10-01", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin}, status: 400},
		{path: "/api/admin/wecom/customer-profiles/9/tag-history?limit=1001", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin}, status: 400},
		{path: "/api/admin/wecom/customer-profiles/9/tag-history?tag_id=a&tag_id=b", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin}, status: 400},
		{path: "/api/admin/wecom/customer-profiles/9", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin}, status: 200, calls: 1},
		{path: "/api/admin/wecom/customer-profiles/9/tag-history?limit=2&before_id=19&from_date=2026-09-30&employee_id=staff&tag_id=tag", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin}, status: 200, calls: 1},
		{path: "/api/admin/wecom/customer-profiles/9/tag-history/statistics", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin}, status: 200, calls: 1},
	} {
		t.Run(test.path, func(t *testing.T) {
			store := &directoryHTTPFixture{}
			h := CustomerSyncHTTPHandler{Service: CustomerSyncService{Store: store}, Auth: customerSyncHTTPAuth{authPrincipal: test.principal, authErr: test.authErr}, Canonical: directoryCanonicalFixture{}, UOW: directUOW{}}.Routes()
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, test.path, nil))
			if r.Code != test.status || store.calls != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", r.Code, store.calls, r.Body.String())
			}
			if test.status == 200 {
				if !strings.Contains(r.Body.String(), "42") && !strings.Contains(test.path, "statistics") {
					t.Fatal("canonical customer was not used")
				}
				if strings.Contains(test.path, "before_id") && (store.query.BeforeID != 19 || store.query.Limit != 2 || store.query.EmployeeID != "staff" || store.query.ProviderTagID != "tag") {
					t.Fatal(store.query)
				}
			}
		})
	}
}
