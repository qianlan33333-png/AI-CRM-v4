package http

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type testSecurity struct{}

func (testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7}, nil
}

type testManager struct {
	created radarport.CreateCommand
	listed  radarport.ListQuery
	page    radarport.LinkPage
}

func (m *testManager) List(_ context.Context, query radarport.ListQuery) (radarport.LinkPage, error) {
	m.listed = query
	if m.page.Items != nil {
		return m.page, nil
	}
	return radarport.LinkPage{Items: []radarport.LinkSummary{{Link: testLink(), StatisticsStatus: radarport.LinkStatisticsReady}}, Total: 1, Limit: 20}, nil
}

func TestAdminListPassesBoundedSearchContentTypeAndPageToExistingQuery(t *testing.T) {
	manager := &testManager{page: radarport.LinkPage{Items: []radarport.LinkSummary{{Link: testLink(), StatisticsStatus: radarport.LinkStatisticsReady}}, Total: 21, Limit: 20, Offset: 20, HasMore: false}}
	handler, err := NewHandler(manager, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links?search=Guide&content_type=pdf&status=enabled&limit=20&offset=20", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if manager.listed.Search != "Guide" || manager.listed.ContentType != radar.ContentTypePDF || manager.listed.Status != radar.StatusEnabled || manager.listed.Limit != 20 || manager.listed.Offset != 20 {
		t.Fatalf("query=%+v", manager.listed)
	}
	var payload struct {
		Total    int64 `json:"total"`
		Limit    int32 `json:"limit"`
		Offset   int32 `json:"offset"`
		HasMore  bool  `json:"has_more"`
		Local    bool  `json:"local_projection"`
		External bool  `json:"real_external_call_executed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 21 || payload.Limit != 20 || payload.Offset != 20 || payload.HasMore || !payload.Local || payload.External {
		t.Fatalf("payload=%+v", payload)
	}
}
func (m *testManager) Get(context.Context, radar.RadarID) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{Link: testLink()}, nil
}
func (m *testManager) Create(_ context.Context, c radarport.CreateCommand) (radarport.LinkDetail, error) {
	m.created = c
	return radarport.LinkDetail{Link: testLink()}, nil
}
func (m *testManager) Update(context.Context, radarport.UpdateCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{Link: testLink()}, nil
}
func (m *testManager) SetStatus(context.Context, radarport.SetStatusCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{Link: testLink()}, nil
}

type testQuery struct {
	events   radarport.EventPage
	visitors radarport.VisitorPage
}

func (testQuery) Stats(context.Context, radar.RadarID) (radarport.Stats, error) {
	return radarport.Stats{TotalEvents: 3, TotalLandings: 1, AuthorizedUsers: 1, ViewCount: 2}, nil
}
func (q testQuery) Events(context.Context, radarport.EventQuery) (radarport.EventPage, error) {
	return q.events, nil
}
func (q testQuery) Visitors(context.Context, radarport.VisitorQuery, string) (radarport.VisitorPage, error) {
	return q.visitors, nil
}

type testPublic struct{ openErr error }

func (p testPublic) Open(context.Context, radar.PublicCode, string) (radarport.PublicAccess, error) {
	return radarport.PublicAccess{}, p.openErr
}
func (testPublic) CompleteOAuth(context.Context, string, string) (string, string, error) {
	return "", "", radarport.ErrUnavailable
}
func (testPublic) Content(context.Context, radar.PublicCode, string) (radarport.Content, error) {
	return radarport.Content{}, radarport.ErrNotFound
}
func (testPublic) Record(context.Context, radar.PublicCode, string, radarport.EventStage, string) (radarport.EventProjection, bool, error) {
	return radarport.EventProjection{}, false, radarport.ErrNotFound
}
func testLink() radar.Link {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	return radar.Link{ID: 1, PublicCode: "rd_abcdefghijklmnopqrstuv", Name: "Guide", Title: "Guide", Content: radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://example.com"}, AuthPolicy: radar.AuthPolicyUnionIDRequired, Status: radar.StatusDraft, Version: 1, CreatedBy: 7, UpdatedBy: 7, CreatedAt: now, UpdatedAt: now}
}

func TestAdminCreateDefaultsToUnionIDAndEmitsNoExternalIdentity(t *testing.T) {
	manager := &testManager{}
	handler, err := NewHandler(manager, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/radar-links", strings.NewReader(`{"expected_version":0,"name":"Guide","title":"Guide","destination_url":"https://example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if manager.created.AuthPolicy != radar.AuthPolicyUnionIDRequired {
		t.Fatalf("policy=%s", manager.created.AuthPolicy)
	}
	body := strings.ToLower(response.Body.String())
	for _, forbidden := range []string{"unionid\"", "openid", "external_userid", "phone"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked forbidden field %q: %s", forbidden, body)
		}
	}
	var payload map[string]any
	if json.Unmarshal(response.Body.Bytes(), &payload) != nil {
		t.Fatal("invalid JSON")
	}
}
func TestDisabledPublicLinkIsGone(t *testing.T) {
	handler, _ := NewHandler(&testManager{}, testQuery{}, testPublic{openErr: radarport.ErrGone}, testSecurity{}, "https://crm.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/r/rd_abcdefghijklmnopqrstuv", nil))
	if response.Code != http.StatusGone {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestPublicViewerCSPAllowsOnlySameOriginEventTracking(t *testing.T) {
	handler, err := NewHandler(&testManager{}, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/r/rd_abcdefghijklmnopqrstuv", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	policy := response.Header().Get("Content-Security-Policy")
	wantPolicy := "default-src 'none'; img-src 'self'; frame-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'"
	if policy != wantPolicy {
		t.Fatalf("viewer CSP=%q want=%q", policy, wantPolicy)
	}
	if !strings.Contains(response.Body.String(), "'/api/public/radar/'+code+'/events'") {
		t.Fatalf("viewer no longer contains the existing same-origin tracking endpoint: %s", response.Body.String())
	}
}

func TestAdminListMapsMeasuredAndUnavailableStatisticsWithoutFallbacks(t *testing.T) {
	lastViewedAt := time.Date(2026, 9, 12, 9, 30, 0, 0, time.UTC)
	manager := &testManager{page: radarport.LinkPage{Items: []radarport.LinkSummary{
		{Link: testLink(), StatisticsStatus: radarport.LinkStatisticsReady, TotalLandings: 7, AuthorizedUsers: 3, AuthorizedViews: 2, ViewCount: 4, LastViewedAt: &lastViewedAt},
		{Link: radar.Link{ID: 2, PublicCode: "rd_zyxwvutsrqponmlkjihgfe", Name: "Unavailable", Title: "Unavailable", Content: radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://example.com/unavailable"}, AuthPolicy: radar.AuthPolicyAnonymous, Status: radar.StatusDraft, Version: 1, CreatedBy: 7, UpdatedBy: 7, CreatedAt: lastViewedAt, UpdatedAt: lastViewedAt}, StatisticsStatus: radarport.LinkStatisticsUnavailable},
	}, Total: 2, Limit: 20}}
	handler, err := NewHandler(manager, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			LinkID           int64   `json:"link_id"`
			StatisticsStatus string  `json:"statistics_status"`
			TotalLandings    *int64  `json:"total_landings"`
			AuthorizedUsers  *int64  `json:"authorized_users"`
			AuthorizedViews  *int64  `json:"authorized_views"`
			ViewCount        *int64  `json:"view_count"`
			LastViewedAt     *string `json:"last_viewed_at"`
		} `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("items=%+v", payload.Items)
	}
	ready := payload.Items[0]
	if ready.StatisticsStatus != "ready" || ready.TotalLandings == nil || *ready.TotalLandings != 7 || ready.AuthorizedUsers == nil || *ready.AuthorizedUsers != 3 || ready.AuthorizedViews == nil || *ready.AuthorizedViews != 2 || ready.ViewCount == nil || *ready.ViewCount != 4 || ready.LastViewedAt == nil || *ready.LastViewedAt == "" {
		t.Fatalf("ready=%+v", ready)
	}
	unavailable := payload.Items[1]
	if unavailable.StatisticsStatus != "unavailable" || unavailable.TotalLandings != nil || unavailable.AuthorizedUsers != nil || unavailable.AuthorizedViews != nil || unavailable.ViewCount != nil || unavailable.LastViewedAt != nil {
		t.Fatalf("unavailable=%+v", unavailable)
	}
}

func TestEventExportFormatsBusinessTimestampsInShanghai(t *testing.T) {
	query := testQuery{events: radarport.EventPage{Items: []radarport.EventProjection{{
		ReceiptID: "rre_export", RadarID: 1, Stage: radarport.EventLanding,
		Attribution: radarport.AttributionResolved, CustomerRef: "customer:7",
		OccurredAt: time.Date(2026, time.September, 5, 0, 1, 2, 611265000, time.UTC),
	}}, Total: 1, Limit: 500}}
	handler, err := NewHandler(&testManager{}, query, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/1/events/export", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "2026-09-05 08:01:02") || strings.Contains(response.Body.String(), "2026-09-05T00:01:02") || !strings.Contains(response.Body.String(), "访问落地页") || !strings.Contains(response.Body.String(), "已关联客户") || strings.Contains(response.Body.String(), ",landing,resolved,") {
		t.Fatalf("business CSV did not use Shanghai display time: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestEventExportRequiresBusinessRole(t *testing.T) {
	viewer := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 8, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	handler, err := NewHandler(&testManager{}, testQuery{}, testPublic{}, visitorSecurity{auth: viewer, csrf: viewer}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/1/events/export", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer event export status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestOAuthFailureOffersOnlyValidatedManualRetry(t *testing.T) {
	handler, _ := NewHandler(&testManager{}, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	for _, code := range []string{"rd_abcdefghijklmnopqrstuv", "//evil.example"} {
		request := httptest.NewRequest(http.MethodGet, "/api/public/radar/oauth/callback?code=failed&state=opaque", nil)
		request.AddCookie(&http.Cookie{Name: "radar_oauth_return", Value: code})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 503 || response.Header().Get("Location") != "" || !strings.Contains(response.Body.String(), "微信授权未完成") {
			t.Fatalf("unexpected failure page: %d", response.Code)
		}
		if strings.Contains(response.Body.String(), `href="/r/`) != (code == "rd_abcdefghijklmnopqrstuv") {
			t.Fatal("unsafe or missing retry link")
		}
	}
}

type visitorSecurity struct {
	auth    accessdomain.Principal
	csrf    accessdomain.Principal
	authErr error
	csrfErr error
}

func (security visitorSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.auth, security.authErr
}
func (security visitorSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.csrf, security.csrfErr
}

func TestVisitorEndpointsRequireCSRFAuthorizationAndProtectCSVCells(t *testing.T) {
	name, external, oneID := " \t=nickname", "\r=external", "\n@OneID"
	page := radarport.VisitorPage{Items: []radarport.Visitor{{
		Nickname:              &name,
		ExternalContactID:     &external,
		ExternalContactStatus: radarport.VisitorExternalContactAvailable,
		OneID:                 &oneID,
		OpenedAt:              time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC),
		AttributionStatus:     radarport.AttributionResolved,
	}}, Total: 1, Limit: 500}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	handler, err := NewHandler(&testManager{}, testQuery{visitors: page}, testPublic{}, visitorSecurity{auth: admin, csrf: admin}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/1/visitors?search=known", nil))
	if list.Code != http.StatusOK || list.Header().Get("Cache-Control") != "no-store" || !strings.Contains(list.Body.String(), `"external_contact_status":"available"`) || !strings.Contains(list.Body.String(), `"oneid":"\n@OneID"`) {
		t.Fatalf("visitor response=%d %q", list.Code, list.Body.String())
	}
	export := httptest.NewRecorder()
	handler.ServeHTTP(export, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/1/visitors/export?search=known", nil))
	if export.Code != http.StatusOK || export.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("visitor export=%d %q", export.Code, export.Body.String())
	}
	reader := csv.NewReader(strings.NewReader(export.Body.String()))
	rows, readErr := reader.ReadAll()
	if readErr != nil || len(rows) != 2 || !strings.HasPrefix(rows[1][0], "'") || !strings.HasPrefix(rows[1][1], "'") || !strings.HasPrefix(rows[1][3], "'") {
		t.Fatalf("CSV formula protection rows=%q err=%v", rows, readErr)
	}
	for _, testCase := range []struct {
		name     string
		security visitorSecurity
		want     int
	}{
		{"anonymous", visitorSecurity{authErr: errors.New("missing session")}, http.StatusUnauthorized},
		{"csrf", visitorSecurity{auth: admin, csrfErr: errors.New("missing csrf")}, http.StatusForbidden},
		{"viewer", visitorSecurity{auth: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleViewer}}, csrf: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, http.StatusForbidden},
		{"staff-admin-role", visitorSecurity{auth: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, csrf: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			denied, newErr := NewHandler(&testManager{}, testQuery{visitors: page}, testPublic{}, testCase.security, "https://crm.example")
			if newErr != nil {
				t.Fatal(newErr)
			}
			response := httptest.NewRecorder()
			denied.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/1/visitors", nil))
			if response.Code != testCase.want || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d response=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestVisitorExportRejectsPartialResultAndMalformedQuery(t *testing.T) {
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	handler, err := NewHandler(&testManager{}, testQuery{visitors: radarport.VisitorPage{HasMore: true, Limit: 500}}, testPublic{}, visitorSecurity{auth: admin, csrf: admin}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/admin/radar-links/1/visitors/export",
		"/api/admin/radar-links/1/visitors?search=x&search=y",
		"/api/admin/radar-links/1/visitors/export?offset=1",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		want := http.StatusConflict
		if strings.Contains(path, "search=x") || strings.Contains(path, "offset=1") {
			want = http.StatusBadRequest
		}
		if response.Code != want {
			t.Fatalf("path=%s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}
