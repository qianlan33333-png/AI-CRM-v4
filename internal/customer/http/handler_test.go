package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, run func(context.Context) error) error { return run(ctx) }

type testSecurity struct{ principal accessdomain.Principal }

func (security testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, nil
}
func (security testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, nil
}

type testCustomerStore struct {
	lastQuery customerapp.Query
	detail    customerapp.Detail
	detailErr error
	ownerIDs  []customerdomain.CustomerID
	ownerErr  error
}

func (store *testCustomerStore) List(_ context.Context, query customerapp.Query) (customerapp.PageData, error) {
	store.lastQuery = query
	return customerapp.PageData{Items: []customerapp.Item{}}, nil
}
func (store *testCustomerStore) Detail(context.Context, customerdomain.CustomerID) (customerapp.Detail, error) {
	return store.detail, store.detailErr
}
func (store *testCustomerStore) CustomerIDsForOwner(_ context.Context, staffID int64, limit int) ([]customerdomain.CustomerID, error) {
	if staffID < 1 || limit < 1 {
		return nil, customerapp.ErrInvalidQuery
	}
	return store.ownerIDs, store.ownerErr
}

type testDirectoryTags struct {
	ids []customerdomain.CustomerID
	err error
}

func (tags testDirectoryTags) CustomerIDsForTag(context.Context, int64, int) ([]customerdomain.CustomerID, error) {
	return tags.ids, tags.err
}

type testIdentities struct {
	reveals      int
	phoneQueries []string
	summaries    []identityport.DirectoryIdentitySummary
	phones       []identityport.MaskedPhone
	directoryErr error
}

func (*testIdentities) VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}

func (identities *testIdentities) CustomerForPhone(_ context.Context, phone string) (customerdomain.CustomerID, bool, error) {
	identities.phoneQueries = append(identities.phoneQueries, phone)
	return 42, true, nil
}
func (identities *testIdentities) DirectoryIdentities(context.Context, customerdomain.CustomerID) ([]identityport.DirectoryIdentitySummary, []identityport.MaskedPhone, error) {
	return identities.summaries, identities.phones, identities.directoryErr
}

type testOrders struct {
	summary orderport.CustomerOrderSummary
	err     error
}

func (orders testOrders) CustomerOrderSummary(context.Context, int64, int32) (orderport.CustomerOrderSummary, error) {
	return orders.summary, orders.err
}
func (identities *testIdentities) RevealPhone(context.Context, customerdomain.CustomerID) (string, bool, error) {
	identities.reveals++
	return "13812345678", true, nil
}

type testAudit struct{ events []platformaudit.Event }

func (audit *testAudit) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	audit.events = append(audit.events, event)
	return event, nil
}

type testCanonical struct{}

func (testCanonical) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}

type testOwners struct{}

func (testOwners) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testOwners) CustomerOwners(context.Context, customerdomain.CustomerID) (customerport.OwnerPage, error) {
	return customerport.OwnerPage{Items: []customerport.OwnerItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testTags struct{}

func (testTags) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testTags) CustomerTags(context.Context, customerdomain.CustomerID) (customerport.TagPage, error) {
	return customerport.TagPage{Items: []customerport.TagItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testSurveys struct{}

func (testSurveys) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testSurveys) CustomerSurveys(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.SurveyPage, error) {
	return customerport.SurveyPage{Items: []customerport.SurveyItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testTimeline struct{}

func (testTimeline) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testTimeline) CustomerTimeline(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error) {
	return customerport.TimelinePage{Items: []customerport.TimelineItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testChat struct{}

func (testChat) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionNotReady}
}
func (testChat) CustomerChatActivity(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.ChatActivityPage, error) {
	return customerport.ChatActivityPage{}, customerport.ErrCapabilityNotReady
}

type testOwnerHandoffService struct{}

func (testOwnerHandoffService) PreviewOwnerHandoff(context.Context, customerport.OwnerHandoffPreviewCommand) (customerport.OwnerHandoffPreview, error) {
	return customerport.OwnerHandoffPreview{}, nil
}
func (testOwnerHandoffService) ConfirmOwnerHandoff(context.Context, customerport.OwnerHandoffConfirmCommand) (customerport.OwnerHandoffBatch, error) {
	return customerport.OwnerHandoffBatch{}, nil
}

type testOwnerHandoffReader struct{}

func (testOwnerHandoffReader) OwnerHandoffPreview(context.Context, string) (customerport.OwnerHandoffPreview, error) {
	return customerport.OwnerHandoffPreview{}, nil
}
func (testOwnerHandoffReader) OwnerHandoffBatch(context.Context, string) (customerport.OwnerHandoffBatch, error) {
	return customerport.OwnerHandoffBatch{}, nil
}

type testOwnerHandoffStaffDirectory struct{}

func (testOwnerHandoffStaffDirectory) ListOwnerHandoffStaff(context.Context) ([]customerport.OwnerHandoffStaff, error) {
	return []customerport.OwnerHandoffStaff{{ID: 12, UserID: "inactive-source", DisplayName: "Inactive source", Active: false}, {ID: 13, UserID: "active-target", DisplayName: "Active target", Active: true}}, nil
}

func testConfig(security testSecurity, store *testCustomerStore, identities *testIdentities, audit *testAudit) Config {
	key := []byte("0123456789abcdef0123456789abcdef")
	return Config{UnitOfWork: testUOW{}, Auth: security, CSRF: security, Directory: customerapp.Directory{Store: store, SigningKey: key},
		Store: store, Identities: identities, Audit: audit, Canonical: testCanonical{}, Owners: testOwners{}, Tags: testTags{},
		Surveys: testSurveys{}, Timeline: testTimeline{}, Chat: testChat{}, ProfileSigningKey: key}
}

func TestOwnerHandoffContextRouteUsesAccessProjection(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}
	config := testConfig(security, &testCustomerStore{}, &testIdentities{}, &testAudit{})
	config.OwnerHandoff = testOwnerHandoffService{}
	config.OwnerHandoffReader = testOwnerHandoffReader{}
	config.OwnerHandoffStaff = testOwnerHandoffStaffDirectory{}
	config.OwnerHandoffCorpScope = "wecom-corp:fixture"
	handler, err := NewHandler(config)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers/owner-handoffs/context", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Body.String(), `"operator":"管理员 #7"`) || !strings.Contains(response.Body.String(), `"UserID":"inactive-source"`) || !strings.Contains(response.Body.String(), `"UserID":"active-target"`) {
		t.Fatalf("context status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
}

func TestOwnerHandoffPickerKeepsStaffAndWeComIDsDistinct(t *testing.T) {
	for _, role := range []accessdomain.Role{accessdomain.RoleSuperAdmin, accessdomain.RoleAdmin, accessdomain.RoleViewer} {
		security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{role}}}
		config := testConfig(security, &testCustomerStore{}, &testIdentities{}, &testAudit{})
		config.OwnerHandoffStaff = testOwnerHandoffStaffDirectory{}
		handler, err := NewHandler(config)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.OwnerHandoffOperationMembersHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?scope=owner_migration", nil))
		if role == accessdomain.RoleViewer {
			if response.Code != http.StatusForbidden {
				t.Fatalf("unprivileged directory status=%d", response.Code)
			}
			continue
		}
		var result struct {
			Items []struct {
				StaffID int64  `json:"staff_id"`
				UserID  string `json:"user_id"`
			} `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Items) != 1 || result.Items[0].StaffID != 13 || result.Items[0].UserID != "active-target" {
			t.Fatalf("picker must preserve distinct trusted identifiers: status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestPhoneRevealEnforcesRoleAndNoStoreAudit(t *testing.T) {
	for _, test := range []struct {
		name       string
		role       accessdomain.Role
		wantStatus int
		wantReveal int
		wantAudit  int
	}{{"viewer", accessdomain.RoleViewer, 403, 0, 0}, {"admin", accessdomain.RoleAdmin, 200, 1, 1}, {"super", accessdomain.RoleSuperAdmin, 200, 1, 1}} {
		t.Run(test.name, func(t *testing.T) {
			identities := &testIdentities{}
			audit := &testAudit{}
			security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{test.role}}}
			store := &testCustomerStore{}
			handler, err := NewHandler(testConfig(security, store, identities, audit))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/admin/customers/1/phone-reveal", nil)
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.wantStatus || identities.reveals != test.wantReveal || len(audit.events) != test.wantAudit {
				t.Fatalf("status=%d reveals=%d audits=%d body=%s", response.Code, identities.reveals, len(audit.events), response.Body.String())
			}
			if test.wantStatus == 200 && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
			}
			if test.wantStatus == 200 && (strings.Contains(response.Body.String(), "+86") || !strings.Contains(response.Body.String(), "13812345678")) {
				t.Fatalf("phone response=%s", response.Body.String())
			}
			if test.wantAudit == 1 && !strings.Contains(string(audit.events[0].Payload), `"purpose":"customer_detail_query"`) {
				t.Fatalf("audit payload=%s", audit.events[0].Payload)
			}
		})
	}
}

func TestPhoneSearchAcceptsOnlyStrictLocalCNFormat(t *testing.T) {
	identities := &testIdentities{}
	audit := &testAudit{}
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := &testCustomerStore{}
	handler, _ := NewHandler(testConfig(security, store, identities, audit))

	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?phone=13812345678", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(identities.phoneQueries) != 1 || identities.phoneQueries[0] != "13812345678" || store.lastQuery.Filters.PhoneCustomerID != 42 {
		t.Fatalf("queries=%v filter=%+v", identities.phoneQueries, store.lastQuery.Filters)
	}

	for _, value := range []string{"123", "%2B8613812345678", "138%201234%205678", "12812345678"} {
		response = httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?phone="+value, nil))
		if response.Code != http.StatusBadRequest || len(identities.phoneQueries) != 1 {
			t.Fatalf("invalid=%s status=%d queries=%v", value, response.Code, identities.phoneQueries)
		}
	}

	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?activation_status=active", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("removed activation filter status=%d", response.Code)
	}
}

func TestCustomerDirectoryOwnerAndTagFiltersReachTheirExactLocalPredicates(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := &testCustomerStore{ownerIDs: []customerdomain.CustomerID{5, 2, 5}}
	config := testConfig(security, store, &testIdentities{}, &testAudit{})
	config.Directory.Tags = testDirectoryTags{ids: []customerdomain.CustomerID{8, 2, 8}}
	handler, err := NewHandler(config)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?owner_staff_id=9&tag_id=12", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	filters := store.lastQuery.Filters
	if filters.OwnerStaffID != 9 || filters.TagID != 12 || !filters.OwnerMatchNone && len(filters.OwnerCustomerIDs) != 2 || !filters.TagMatchNone && len(filters.TagCustomerIDs) != 2 {
		t.Fatalf("filters=%+v", filters)
	}
	for _, raw := range []string{"0", "09", "%209", "-1", "x"} {
		response = httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?owner_staff_id="+raw, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("owner=%q status=%d body=%s", raw, response.Code, response.Body.String())
		}
	}
}

func TestCustomerDirectoryValidFilterFailureIsRetryableServiceError(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := &testCustomerStore{ownerErr: errors.New("owner projection unavailable")}
	handler, err := NewHandler(testConfig(security, store, &testIdentities{}, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?owner_staff_id=9", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "directory_filter_unavailable") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCustomerDetailReturnsOnlySafeIdentityAndPhoneSummaries(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := &testCustomerStore{detail: customerapp.Detail{Item: customerapp.Item{CustomerID: 42, CustomerStatus: customerdomain.StatusActive,
		DisplayName: "Alice", AvatarURL: "https://provider/avatar", OneIDLabel: "CID-42", PhoneMasked: "138****5678",
		PhoneAssurance: string(identitydomain.AssuranceDeclared), ActivationState: "active"}, CorpName: "Example", Source: "wecom_directory_sync"}}
	identities := &testIdentities{summaries: []identityport.DirectoryIdentitySummary{{Kind: identitydomain.KindWeComExternalUserID,
		Scope: "wecom-corp:raw-corp", Assurance: identitydomain.AssuranceVerified, Status: "active", Source: "secret-source"}},
		phones: []identityport.MaskedPhone{{Masked: "138****5678", Assurance: identitydomain.AssuranceDeclared}}}
	handler, err := NewHandler(testConfig(security, store, identities, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers/42", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"raw-corp", "secret-source", "declared", "verified", "+86", "provider/avatar", "activation_status", "phone_assurance"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("detail leaked %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{"企微身份已验证", "138****5678", `"chat_activity":{"status":"not_ready"}`} {
		if !strings.Contains(body, required) {
			t.Fatalf("detail missing %q: %s", required, body)
		}
	}
}

func TestChatSectionIsExplicitlyNotReady(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	handler, err := NewHandler(testConfig(security, &testCustomerStore{}, &testIdentities{}, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers/42/chat-activity?limit=20", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "capability_not_ready") || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
}

func TestCustomer360ContainsOnlyApprovedLocalSections(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	store := &testCustomerStore{detail: customerapp.Detail{Item: customerapp.Item{CustomerID: 42, CustomerStatus: customerdomain.StatusActive, DisplayName: "Alice", OneIDLabel: "CID-42"}}}
	handler, err := NewHandler(testConfig(security, store, &testIdentities{}, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers/42/360", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, required := range []string{"identity_summary", "profile", "order_summary", "questionnaire_summary", "risk", "recent_touchpoints"} {
		if !strings.Contains(body, `"`+required+`"`) {
			t.Fatalf("missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{"message_summary", `"tags"`, `"owners"`, "user_ops_status", "automation_status", "chat_activity"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("forbidden %q: %s", forbidden, body)
		}
	}
}

func TestCustomer360RiskDegradesWhenRequiredSectionsAreUnavailable(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	tests := []struct {
		name            string
		identities      *testIdentities
		orders          testOrders
		wantOrderStatus string
		wantRiskStatus  string
		wantRiskLevel   string
		wantRiskReasons []string
	}{
		{
			name:            "order read error",
			identities:      &testIdentities{},
			orders:          testOrders{err: errors.New("order projection unavailable")},
			wantOrderStatus: "degraded", wantRiskStatus: "degraded", wantRiskLevel: "unknown",
			wantRiskReasons: []string{"order_section_unavailable"},
		},
		{
			name:            "order read timeout",
			identities:      &testIdentities{},
			orders:          testOrders{err: context.DeadlineExceeded},
			wantOrderStatus: "degraded", wantRiskStatus: "degraded", wantRiskLevel: "unknown",
			wantRiskReasons: []string{"order_section_unavailable"},
		},
		{
			name:            "empty order result is complete",
			identities:      &testIdentities{},
			orders:          testOrders{},
			wantOrderStatus: "ready", wantRiskStatus: "ready", wantRiskLevel: "low",
			wantRiskReasons: []string{},
		},
		{
			name:            "identity failure preserves refunded order risk",
			identities:      &testIdentities{directoryErr: errors.New("identity projection unavailable")},
			orders:          testOrders{summary: orderport.CustomerOrderSummary{Refunded: 1}},
			wantOrderStatus: "ready", wantRiskStatus: "degraded", wantRiskLevel: "unknown",
			wantRiskReasons: []string{"identity_section_unavailable", "refunds_present"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &testCustomerStore{detail: customerapp.Detail{Item: customerapp.Item{CustomerID: 42, CustomerStatus: customerdomain.StatusActive, DisplayName: "Alice", OneIDLabel: "CID-42"}}}
			config := testConfig(security, store, test.identities, &testAudit{})
			config.Orders = test.orders
			handler, err := NewHandler(config)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers/42/360", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body struct {
				OrderSummary struct {
					Status string `json:"status"`
				} `json:"order_summary"`
				Risk struct {
					Status string `json:"status"`
					Data   struct {
						Level   string   `json:"level"`
						Reasons []string `json:"reasons"`
					} `json:"data"`
				} `json:"risk"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.OrderSummary.Status != test.wantOrderStatus || body.Risk.Status != test.wantRiskStatus || body.Risk.Data.Level != test.wantRiskLevel || strings.Join(body.Risk.Data.Reasons, ",") != strings.Join(test.wantRiskReasons, ",") {
				t.Fatalf("order=%q risk=%+v want order=%q status=%q level=%q reasons=%v", body.OrderSummary.Status, body.Risk, test.wantOrderStatus, test.wantRiskStatus, test.wantRiskLevel, test.wantRiskReasons)
			}
		})
	}
}

type testTagCommands struct{ command customerport.TagCommand }

func (t *testTagCommands) SubmitTagCommand(_ context.Context, command customerport.TagCommand) (customerport.TagCommandResult, error) {
	t.command = command
	return customerport.TagCommandResult{ID: 7, State: "queued", Lines: []customerport.TagCommandLine{{CustomerID: command.Targets[0].CustomerID, State: "queued", EffectRef: "eer_7"}}}, nil
}
func (t *testTagCommands) SubmitTagCommandWithin(context.Context, customerport.TagCommand) (customerport.TagCommandResult, error) {
	return customerport.TagCommandResult{}, errors.New("unexpected")
}
func TestCustomerTagCompatibilityRouteUsesCSRFAndCanonicalCommand(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	h, err := NewHandler(testConfig(security, &testCustomerStore{}, &testIdentities{}, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	commands := &testTagCommands{}
	h.tagCommands = commands
	request := httptest.NewRequest(http.MethodPut, "/api/v1/customers/42/tags/9", nil)
	request.Header.Set("Idempotency-Key", "123e4567-e89b-12d3-a456-426614174000")
	response := httptest.NewRecorder()
	h.TagCommandRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(commands.command.Targets) != 1 || commands.command.Targets[0].CustomerID != 42 || len(commands.command.Targets[0].AddTagIDs) != 1 || commands.command.Targets[0].AddTagIDs[0] != 9 {
		t.Fatalf("command=%+v", commands.command)
	}
}

func (t *testTagCommands) PreviewTagCommand(_ context.Context, command customerport.TagCommand) (customerport.TagCommandResult, error) {
	return customerport.TagCommandResult{State: "preview", Lines: []customerport.TagCommandLine{{CustomerID: command.Targets[0].CustomerID, State: "eligible"}}}, nil
}

type testTagHistory struct {
	values []customerport.TagCommandResult
}

func (h testTagHistory) ListTagCommands(_ context.Context, id customerdomain.CustomerID, limit int) ([]customerport.TagCommandResult, error) {
	if id != 42 || limit != 20 {
		return nil, errors.New("unexpected history query")
	}
	return h.values, nil
}
func TestCustomerTagPreviewAndHistoryRoutesKeepProviderIDsOut(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	h, err := NewHandler(testConfig(security, &testCustomerStore{}, &testIdentities{}, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	h.tagCommands = &testTagCommands{}
	h.tagHistory = testTagHistory{values: []customerport.TagCommandResult{{ID: 8, Source: "admin_customer_ui", State: "partial", Lines: []customerport.TagCommandLine{{CustomerID: 42, AddTagIDs: []int64{9}, State: "executed", EffectRef: "eer_8"}}}}}
	body := strings.NewReader(`{"customer_ids":[42],"add_tag_ids":[9],"idempotency_key":"123e4567-e89b-12d3-a456-426614174000"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/customer-tag-commands/preview", body)
	response := httptest.NewRecorder()
	h.TagCommandRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"eligible"`) {
		t.Fatalf("preview=%d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	h.TagCommandRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/customers/42/tag-commands", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"partial"`) || strings.Contains(response.Body.String(), "provider-a") {
		t.Fatalf("history=%d %s", response.Code, response.Body.String())
	}
}
