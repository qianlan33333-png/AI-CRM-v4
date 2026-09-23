package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type fakeSecurity struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (f fakeSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return f.principal, f.authErr
}
func (f fakeSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return f.principal, f.csrfErr
}

type fakeApplication struct{}
type configurationCapture struct {
	fakeApplication
	command segmentapp.ConfigurationCommand
}

func (c *configurationCapture) PutConfiguration(_ context.Context, command segmentapp.ConfigurationCommand) (segmentdomain.ConfigurationVersion, error) {
	c.command = command
	return segmentdomain.ConfigurationVersion{ID: 1, PackageID: command.PackageID, Version: 1}, nil
}

type ownerResolverStub struct{}

func (ownerResolverStub) ResolveAudienceOwner(_ context.Context, value string) (accessport.StaffID, bool, error) {
	if value == "wecom-owner" {
		return 9, true, nil
	}
	return 0, false, nil
}

type ownerResolverFailure struct{}

func (ownerResolverFailure) ResolveAudienceOwner(context.Context, string) (accessport.StaffID, bool, error) {
	return 0, false, errors.New("access projection unavailable")
}

type ownerReferenceStub struct{}

func (ownerReferenceStub) AudienceOwnerUserID(_ context.Context, id accessport.StaffID) (string, bool, error) {
	if id == 9 {
		return "bob", true, nil
	}
	return "", false, nil
}

type productReferenceStub struct {
	values map[string]string
}

func (s productReferenceStub) ResolveAudienceProduct(_ context.Context, value string) (string, bool, error) {
	code, ok := s.values[value]
	return code, ok, nil
}

type channelReferenceStub struct{ values map[string]string }

func (s channelReferenceStub) ResolveAudienceChannel(_ context.Context, value string) (string, bool, error) {
	code, ok := s.values[value]
	return code, ok, nil
}

type radarReferenceStub struct{ values map[string]string }

func (s radarReferenceStub) ResolveAudienceRadar(_ context.Context, value string) (string, bool, error) {
	id, ok := s.values[value]
	return id, ok, nil
}

type surveyReferenceStub struct {
	questionnaires map[string]string
	questions      map[string]string
	options        map[string]string
}

func (s surveyReferenceStub) ResolveAudienceQuestionnaire(_ context.Context, value string) (string, bool, error) {
	id, ok := s.questionnaires[value]
	return id, ok, nil
}
func (s surveyReferenceStub) ResolveAudienceQuestion(_ context.Context, questionnaireID, value string) (string, bool, error) {
	id, ok := s.questions[questionnaireID+"/"+value]
	return id, ok, nil
}
func (s surveyReferenceStub) ResolveAudienceOption(_ context.Context, questionnaireID, questionID, value string) (string, bool, error) {
	id, ok := s.options[questionnaireID+"/"+questionID+"/"+value]
	return id, ok, nil
}

type packageListApplication struct{ fakeApplication }

func (packageListApplication) ListPackages(context.Context, int, int, bool) (segmentapp.PackagePage, error) {
	id := int64(9)
	return segmentapp.PackagePage{Items: []segmentdomain.Package{{ID: 7, Name: "近30天活跃客户", Code: "active-30d", Lifecycle: segmentdomain.Paused, Version: 2, PublishedSnapshotID: &id}}, Total: 1, Limit: 50}, nil
}

type snapshotApplication struct{ snapshot segmentport.Snapshot }

func (snapshotApplication) Preview(context.Context, int64, time.Time) (segmentapp.Preview, error) {
	return segmentapp.Preview{}, nil
}
func (snapshotApplication) PreviewDefinition(context.Context, int64, json.RawMessage, time.Time) (segmentapp.Preview, error) {
	return segmentapp.Preview{}, nil
}
func (snapshotApplication) AcceptRefresh(context.Context, segmentapp.RefreshCommand) (segmentdomain.RefreshRun, error) {
	return segmentdomain.RefreshRun{}, nil
}
func (snapshotApplication) GetRefresh(context.Context, int64) (segmentdomain.RefreshRun, error) {
	return segmentdomain.RefreshRun{}, nil
}
func (s snapshotApplication) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return s.snapshot, s.snapshot.ID > 0, nil
}
func (snapshotApplication) Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error) {
	return segmentport.MemberPage{}, nil
}

type previewDefinitionCapture struct {
	snapshotApplication
	definition json.RawMessage
	calls      int
}

func (s *previewDefinitionCapture) PreviewDefinition(_ context.Context, _ int64, definition json.RawMessage, _ time.Time) (segmentapp.Preview, error) {
	s.calls++
	s.definition = append(s.definition[:0], definition...)
	return segmentapp.Preview{}, nil
}

func (fakeApplication) ListGroups(context.Context) ([]segmentdomain.Group, error) { return nil, nil }
func (fakeApplication) CreateGroup(context.Context, segmentapp.GroupCommand) (segmentdomain.Group, error) {
	return segmentdomain.Group{}, nil
}
func (fakeApplication) UpdateGroup(context.Context, segmentapp.GroupCommand) (segmentdomain.Group, error) {
	return segmentdomain.Group{}, nil
}
func (fakeApplication) DeleteGroup(context.Context, segmentapp.VersionCommand) error { return nil }
func (fakeApplication) ListPackages(context.Context, int, int, bool) (segmentapp.PackagePage, error) {
	return segmentapp.PackagePage{}, nil
}
func (fakeApplication) GetPackage(context.Context, int64) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) CreatePackage(context.Context, segmentapp.PackageCreateCommand) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) UpdatePackage(context.Context, segmentapp.PackageUpdateCommand) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) CopyPackage(context.Context, segmentapp.VersionCommand) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) TransitionPackage(context.Context, segmentapp.VersionCommand, segmentdomain.Lifecycle) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, segmentapp.ErrNotReady
}
func (fakeApplication) PutConfiguration(context.Context, segmentapp.ConfigurationCommand) (segmentdomain.ConfigurationVersion, error) {
	return segmentdomain.ConfigurationVersion{}, nil
}
func (fakeApplication) CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return segmentdomain.ConfigurationVersion{}, nil
}

func TestHandlerAuthRBACCSRFAndClosedTemplates(t *testing.T) {
	viewer := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	tests := []struct {
		name, method, path, body string
		security                 fakeSecurity
		want                     int
	}{
		{"unauthenticated", http.MethodGet, "/api/admin/ai-audience/templates", "", fakeSecurity{authErr: errors.New("no")}, 401},
		{"viewer templates", http.MethodGet, "/api/admin/ai-audience/templates", "", fakeSecurity{principal: viewer}, 200},
		{"viewer cannot mutate", http.MethodPost, "/api/admin/ai-audience/packages", `{"name":"x","template_key":"active_contacts"}`, fakeSecurity{principal: viewer}, 403},
		{"csrf required", http.MethodPost, "/api/admin/ai-audience/packages", `{"name":"x","template_key":"active_contacts"}`, fakeSecurity{principal: admin, csrfErr: errors.New("csrf")}, 403},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := NewHandler(fakeApplication{}, test.security)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Idempotency-Key", "1234567890abcdef")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.name == "viewer templates" && !strings.Contains(response.Body.String(), `"wecom_contact_registration"`) {
				t.Fatalf("body=%s", response.Body.String())
			}
		})
	}
}

func TestPackageListProjectsPublishedSnapshotCountAndTime(t *testing.T) {
	viewer := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	publishedAt := time.Date(2026, 9, 4, 8, 30, 0, 0, time.UTC)
	handler, err := NewRuntimeHandler(packageListApplication{}, snapshotApplication{snapshot: segmentport.Snapshot{ID: 9, PackageID: 7, MemberCount: 23460, ReferenceTime: publishedAt, PublishedAt: &publishedAt}}, fakeSecurity{principal: viewer})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/ai-audience/packages", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"member_count":23460`) || !strings.Contains(response.Body.String(), `"published_at":"2026-09-04T08:30:00Z"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerRejectsUnknownAndOversizedBodiesAndFailsClosedActivation(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	handler, _ := NewHandler(fakeApplication{}, fakeSecurity{principal: admin})
	for name, fixture := range map[string]struct {
		path, body string
		want       int
	}{
		"unknown field":        {"/api/admin/ai-audience/packages", `{"name":"x","template_key":"active_contacts","token":"secret"}`, 400},
		"oversized":            {"/api/admin/ai-audience/packages", `{"name":"` + strings.Repeat("x", 70<<10) + `","template_key":"active_contacts"}`, 413},
		"activation not ready": {"/api/admin/ai-audience/packages/1/activate", `{"expected_version":1}`, 503},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(fixture.body))
			request.Header.Set("Idempotency-Key", "1234567890abcdef")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != fixture.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestConfigurationConvertsFrozenOwnerUserIDsThroughAccess(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	capture := &configurationCapture{}
	handler, err := NewRuntimeHandlerWithOwners(capture, snapshotApplication{}, fakeSecurity{principal: admin}, ownerResolverStub{})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_userids":["wecom-owner"],"contact_statuses":["active"],"registration_status":"any"}}}`
	request := httptest.NewRequest(http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "1234567890abcdef")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(string(capture.command.Definition), `"owner_staff_ids":["9"]`) || strings.Contains(string(capture.command.Definition), "owner_userids") {
		t.Fatalf("status=%d definition=%s", response.Code, capture.command.Definition)
	}
}

func TestPreviewDraftDefinitionNormalizesOwnersWithoutSaving(t *testing.T) {
	viewer := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	capture := &previewDefinitionCapture{}
	handler, err := NewRuntimeHandlerWithOwners(fakeApplication{}, capture, fakeSecurity{principal: viewer}, ownerResolverStub{})
	if err != nil {
		t.Fatal(err)
	}
	handler.BindAudienceProductReferences(productReferenceStub{values: map[string]string{"course-v3": "course-v3"}})
	body := `{"reference_time":"2026-09-05T08:00:00Z","definition":{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["course-v3"],"paid_at_from":"2026-09-05T08:00:00Z","paid_at_to":"2026-09-05T09:00:00Z","owner_scope":"specified","owner_userids":["wecom-owner"],"require_active_wecom_contact":true}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-audience/packages/7/preview", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || capture.calls != 1 || !strings.Contains(string(capture.definition), `"owner_staff_ids":["9"]`) || strings.Contains(string(capture.definition), "owner_userids") {
		t.Fatalf("status=%d calls=%d definition=%s", response.Code, capture.calls, capture.definition)
	}
}

func TestPaidOrderProductReferencesNormalizeForConfigurationAndPreview(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	definition := `{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["中文商品"],"owner_scope":"all","owner_userids":[],"require_active_wecom_contact":true}}`
	resolver := productReferenceStub{values: map[string]string{"中文商品": "course-v3", "course-v3": "course-v3"}}

	t.Run("configuration persists canonical code", func(t *testing.T) {
		capture := &configurationCapture{}
		handler, err := NewRuntimeHandlerWithOwners(capture, snapshotApplication{}, fakeSecurity{principal: admin}, nil)
		if err != nil {
			t.Fatal(err)
		}
		handler.BindAudienceProductReferences(resolver)
		request := httptest.NewRequest(http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", strings.NewReader(`{"expected_package_version":1,"refresh_cron_utc":"","definition":`+definition+`}`))
		request.Header.Set("Idempotency-Key", "1234567890abcdef")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(string(capture.command.Definition), `"product_codes":["course-v3"]`) {
			t.Fatalf("status=%d definition=%s", response.Code, capture.command.Definition)
		}
	})

	t.Run("preview uses canonical code", func(t *testing.T) {
		capture := &previewDefinitionCapture{}
		handler, err := NewRuntimeHandlerWithOwners(fakeApplication{}, capture, fakeSecurity{principal: admin}, nil)
		if err != nil {
			t.Fatal(err)
		}
		handler.BindAudienceProductReferences(resolver)
		request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-audience/packages/7/preview", strings.NewReader(`{"reference_time":"2026-09-05T08:00:00Z","definition":`+definition+`}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || capture.calls != 1 || !strings.Contains(string(capture.definition), `"product_codes":["course-v3"]`) {
			t.Fatalf("status=%d calls=%d definition=%s", response.Code, capture.calls, capture.definition)
		}
	})
}

func TestPaidOrderProductReferenceUnknownFailsClosed(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	fixtures := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", `{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["同名商品"],"owner_scope":"all","owner_userids":[]}}}`},
		{http.MethodPost, "/api/admin/ai-audience/packages/7/preview", `{"reference_time":"2026-09-05T08:00:00Z","definition":{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["不存在商品"],"owner_scope":"all","owner_userids":[]}}}`},
	}
	for _, fixture := range fixtures {
		capture := &configurationCapture{}
		handler, err := NewRuntimeHandlerWithOwners(capture, snapshotApplication{}, fakeSecurity{principal: admin}, nil)
		if err != nil {
			t.Fatal(err)
		}
		handler.BindAudienceProductReferences(productReferenceStub{values: map[string]string{}})
		request := httptest.NewRequest(fixture.method, fixture.path, strings.NewReader(fixture.body))
		request.Header.Set("Idempotency-Key", "1234567890abcdef")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"error":"reference_unknown"`) {
			t.Fatalf("%s %s status=%d body=%s", fixture.method, fixture.path, response.Code, response.Body.String())
		}
	}
}

func TestChannelAndRadarReferencesNormalizeForConfigurationAndPreview(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	channels := channelReferenceStub{values: map[string]string{"渠道标题": "channel-v3", "channel-v3": "channel-v3"}}
	radars := radarReferenceStub{values: map[string]string{"雷达标题": "88", "88": "88"}}
	fixtures := []struct {
		name, method, path, body, expected string
		bind                               func(*Handler)
		preview                            bool
	}{
		{"channel title configuration", http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", `{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"channel_entry","parameters":{"channel_codes":["渠道标题"],"entered_days_min":0,"owner_scope":"all","owner_userids":[]}}}`, `"channel_codes":["channel-v3"]`, func(h *Handler) { h.BindAudienceChannelReferences(channels) }, false},
		{"channel code preview", http.MethodPost, "/api/admin/ai-audience/packages/7/preview", `{"reference_time":"2026-09-05T08:00:00Z","definition":{"schema_version":1,"template_key":"channel_entry","parameters":{"channel_codes":["channel-v3"],"entered_days_min":0,"owner_scope":"all","owner_userids":[]}}}`, `"channel_codes":["channel-v3"]`, func(h *Handler) { h.BindAudienceChannelReferences(channels) }, true},
		{"radar title preview", http.MethodPost, "/api/admin/ai-audience/packages/7/preview", `{"reference_time":"2026-09-05T08:00:00Z","definition":{"schema_version":1,"template_key":"radar_first_click_elapsed","parameters":{"radar_ids":["雷达标题"],"elapsed_min":0,"elapsed_unit":"day","owner_scope":"all","owner_userids":[]}}}`, `"radar_ids":["88"]`, func(h *Handler) { h.BindAudienceRadarReferences(radars) }, true},
		{"radar id configuration", http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", `{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"radar_first_click_elapsed","parameters":{"radar_ids":["88"],"elapsed_min":0,"elapsed_unit":"day","owner_scope":"all","owner_userids":[]}}}`, `"radar_ids":["88"]`, func(h *Handler) { h.BindAudienceRadarReferences(radars) }, false},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			capture := &configurationCapture{}
			preview := &previewDefinitionCapture{}
			handler, err := NewRuntimeHandlerWithOwners(capture, preview, fakeSecurity{principal: admin}, nil)
			if err != nil {
				t.Fatal(err)
			}
			fixture.bind(handler)
			request := httptest.NewRequest(fixture.method, fixture.path, strings.NewReader(fixture.body))
			request.Header.Set("Idempotency-Key", "1234567890abcdef")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			definition := capture.command.Definition
			if fixture.preview {
				definition = preview.definition
			}
			if !strings.Contains(string(definition), fixture.expected) {
				t.Fatalf("definition=%s", definition)
			}
		})
	}
}

func TestChannelAndRadarReferencesRejectUnknown(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	for _, fixture := range []struct {
		body string
		bind func(*Handler)
	}{
		{`{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"channel_entry","parameters":{"channel_codes":["同名渠道"],"entered_days_min":0,"owner_scope":"all","owner_userids":[]}}}`, func(h *Handler) { h.BindAudienceChannelReferences(channelReferenceStub{}) }},
		{`{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"radar_first_click_elapsed","parameters":{"radar_ids":["同名雷达"],"elapsed_min":0,"elapsed_unit":"day","owner_scope":"all","owner_userids":[]}}}`, func(h *Handler) { h.BindAudienceRadarReferences(radarReferenceStub{}) }},
	} {
		handler, err := NewRuntimeHandlerWithOwners(&configurationCapture{}, snapshotApplication{}, fakeSecurity{principal: admin}, nil)
		if err != nil {
			t.Fatal(err)
		}
		fixture.bind(handler)
		request := httptest.NewRequest(http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", strings.NewReader(fixture.body))
		request.Header.Set("Idempotency-Key", "1234567890abcdef")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"error":"reference_unknown"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestSurveyReferencesNormalizeForConfigurationAndPreview(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	resolver := surveyReferenceStub{
		questionnaires: map[string]string{"客户调研": "101", "101": "101"},
		questions:      map[string]string{"101/获客方式": "202", "101/202": "202"},
		options:        map[string]string{"101/202/内容": "303", "101/202/303": "303"},
	}
	definition := `{"schema_version":1,"template_key":"questionnaire_choice_answers","parameters":{"questionnaire_id":"客户调研","conditions":[{"question_id":"获客方式","option_ids":["内容"]}],"owner_scope":"all","owner_userids":[]}}`
	for _, fixture := range []struct {
		method, path, body string
		preview            bool
	}{
		{http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", `{"expected_package_version":1,"refresh_cron_utc":"","definition":` + definition + `}`, false},
		{http.MethodPost, "/api/admin/ai-audience/packages/7/preview", `{"reference_time":"2026-09-05T08:00:00Z","definition":` + definition + `}`, true},
	} {
		capture := &configurationCapture{}
		preview := &previewDefinitionCapture{}
		handler, err := NewRuntimeHandlerWithOwners(capture, preview, fakeSecurity{principal: admin}, nil)
		if err != nil {
			t.Fatal(err)
		}
		handler.BindAudienceSurveyReferences(resolver)
		request := httptest.NewRequest(fixture.method, fixture.path, strings.NewReader(fixture.body))
		request.Header.Set("Idempotency-Key", "1234567890abcdef")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		value := capture.command.Definition
		if fixture.preview {
			value = preview.definition
		}
		if !strings.Contains(string(value), `"questionnaire_id":"101"`) || !strings.Contains(string(value), `"question_id":"202"`) || !strings.Contains(string(value), `"option_ids":["303"]`) {
			t.Fatalf("definition=%s", value)
		}
	}
}

func TestSurveyReferencesRejectUnknownOrUnscopedValues(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	resolver := surveyReferenceStub{questionnaires: map[string]string{"客户调研": "101"}, questions: map[string]string{"101/题目": "202"}, options: map[string]string{}}
	body := `{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"questionnaire_choice_answers","parameters":{"questionnaire_id":"客户调研","conditions":[{"question_id":"题目","option_ids":["另一题目的同名选项"]}],"owner_scope":"all","owner_userids":[]}}}`
	handler, err := NewRuntimeHandlerWithOwners(&configurationCapture{}, snapshotApplication{}, fakeSecurity{principal: admin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.BindAudienceSurveyReferences(resolver)
	request := httptest.NewRequest(http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "1234567890abcdef")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"error":"reference_unknown"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestConfigurationOwnerIdentifierFailuresAndAllScopeEmptyCompatibility(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	requestFor := func(parameters string) *http.Request {
		body := `{"expected_package_version":1,"refresh_cron_utc":"","definition":{"schema_version":1,"template_key":"wecom_contact_registration","parameters":` + parameters + `}}`
		request := httptest.NewRequest(http.MethodPut, "/api/admin/ai-audience/packages/7/configuration", strings.NewReader(body))
		request.Header.Set("Idempotency-Key", "1234567890abcdef")
		return request
	}
	for _, fixture := range []struct {
		name, parameters, wantCode string
		owners                     accessport.AudienceOwnerResolver
	}{
		{"all empty without resolver", `{"owner_scope":"all","owner_userids":[],"contact_statuses":["active"],"registration_status":"any"}`, "", nil},
		{"specified local ids without resolver", `{"owner_scope":"specified","owner_staff_ids":["9"],"contact_statuses":["active"],"registration_status":"any"}`, "owner_unavailable", nil},
		{"mixed identifiers", `{"owner_scope":"specified","owner_userids":["wecom-owner"],"owner_staff_ids":["9"],"contact_statuses":["active"],"registration_status":"any"}`, "owner_invalid", ownerResolverStub{}},
		{"unknown owner", `{"owner_scope":"specified","owner_userids":["unknown"],"contact_statuses":["active"],"registration_status":"any"}`, "owner_unknown", ownerResolverStub{}},
		{"resolver unavailable", `{"owner_scope":"specified","owner_userids":["wecom-owner"],"contact_statuses":["active"],"registration_status":"any"}`, "owner_unavailable", ownerResolverFailure{}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			capture := &configurationCapture{}
			handler, err := NewRuntimeHandlerWithOwners(capture, snapshotApplication{}, fakeSecurity{principal: admin}, fixture.owners)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, requestFor(fixture.parameters))
			if fixture.wantCode == "" {
				if response.Code != http.StatusOK || !strings.Contains(string(capture.command.Definition), `"owner_staff_ids":[]`) || strings.Contains(string(capture.command.Definition), "owner_userids") {
					t.Fatalf("status=%d definition=%s", response.Code, capture.command.Definition)
				}
				return
			}
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"error":"`+fixture.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestOwnerReferencesRehydrateThroughAccessPort(t *testing.T) {
	viewer := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	handler, err := NewRuntimeHandlerWithOwnerReferences(fakeApplication{}, snapshotApplication{}, fakeSecurity{principal: viewer}, nil, ownerReferenceStub{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/ai-audience/packages/7/owner-references?staff_id=9", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"owner_userids":["bob"]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
