package http

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type routeDefinitions struct {
	surveyport.DefinitionApplication
}

type publishVersionDefinitions struct {
	surveyport.DefinitionApplication
	questionnaire     surveyport.Questionnaire
	publishedExpected int64
	publishCalls      int
}

type archiveDefinitions struct {
	surveyport.DefinitionApplication
	questionnaire  surveyport.Questionnaire
	setStatusCalls int
	getCalls       int
	replayKey      string
	replayResult   surveyport.Questionnaire
}

func (d *archiveDefinitions) Get(context.Context, surveyport.ID) (surveyport.Questionnaire, error) {
	d.getCalls++
	return d.questionnaire, nil
}
func (d *archiveDefinitions) SetStatus(_ context.Context, _ surveyport.ID, expected int64, status surveyport.QuestionnaireStatus, _ int64, key string) (surveyport.Questionnaire, error) {
	d.setStatusCalls++
	if key != "" && key == d.replayKey {
		return d.replayResult, nil
	}
	if expected != d.questionnaire.Version || status != surveyport.StatusArchived {
		return surveyport.Questionnaire{}, surveyport.ErrConflict
	}
	d.questionnaire.Status = status
	d.questionnaire.Version++
	d.replayKey, d.replayResult = key, d.questionnaire
	return d.questionnaire, nil
}

func (d *publishVersionDefinitions) Get(context.Context, surveyport.ID) (surveyport.Questionnaire, error) {
	return d.questionnaire, nil
}
func (d *publishVersionDefinitions) Publish(_ context.Context, _ surveyport.ID, expected, _ int64, _ string) (surveyport.Questionnaire, error) {
	d.publishCalls++
	d.publishedExpected = expected
	if expected != d.questionnaire.Version {
		return surveyport.Questionnaire{}, surveyport.ErrConflict
	}
	d.questionnaire.Status = surveyport.StatusPublished
	d.questionnaire.Version++
	return d.questionnaire, nil
}

type routeSurvey struct {
	surveyport.PublicApplication
	surveyport.SubmissionApplication
	questionnaire   surveyport.Questionnaire
	submissions     []surveyport.Submission
	exportCalls     int
	publicStatus    surveyport.PublicSubmissionStatus
	publicStatusErr error
	submitReceipt   surveyport.SubmissionReceipt
	submitErr       error
}

func (s *routeSurvey) ReadPublic(_ context.Context, slug string) (surveyport.Questionnaire, error) {
	if slug != s.questionnaire.Slug {
		return surveyport.Questionnaire{}, surveyport.ErrNotFound
	}
	return s.questionnaire, nil
}

func (s *routeSurvey) PublicSubmissionStatus(_ context.Context, slug string, _ surveyport.SubmissionIdentity) (surveyport.PublicSubmissionStatus, error) {
	if slug != s.questionnaire.Slug {
		return surveyport.PublicSubmissionStatus{}, surveyport.ErrNotFound
	}
	return s.publicStatus, s.publicStatusErr
}

func (s *routeSurvey) Submit(context.Context, surveyport.SubmitCommand) (surveyport.SubmissionReceipt, error) {
	return s.submitReceipt, s.submitErr
}

func (s *routeSurvey) ListSubmissions(context.Context, surveyport.ID, int32, int32, surveyport.IdentityState) (surveyport.SubmissionPage, error) {
	return surveyport.SubmissionPage{Items: s.submissions, Total: int64(len(s.submissions)), Limit: int32(len(s.submissions))}, nil
}

func (s *routeSurvey) RecordExport(context.Context, surveyport.ID, int64, string) error {
	s.exportCalls++
	return nil
}

type routeSecurity struct{}

func (routeSecurity) Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("unused")
}
func (routeSecurity) AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("unused")
}

type operationSecurity struct{ csrfErr error }

func (s operationSecurity) Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s operationSecurity) AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, s.csrfErr
}

type principalSecurity struct{ principal accessdomain.Principal }

func (s principalSecurity) Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (s principalSecurity) AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}

type operationRouteSurvey struct {
	surveyport.PublicApplication
	surveyport.SubmissionApplication
	configuration surveyport.OperationConfiguration
	saveCalls     int
	testCalls     int
	testReceipt   surveyport.CompletionTestReceipt
	testErr       error
	receipts      []surveyport.OperationReceipt
}

type completionTargetCatalogStub struct {
	references []string
	err        error
}

func (s completionTargetCatalogStub) CompletionTargetReferences(context.Context) ([]string, error) {
	return append([]string(nil), s.references...), s.err
}

func (s *operationRouteSurvey) GetOperationConfiguration(context.Context, surveyport.ID) (surveyport.OperationConfiguration, error) {
	return s.configuration, nil
}
func (s *operationRouteSurvey) ListOperationReceipts(context.Context, surveyport.ID, int32, int32) ([]surveyport.OperationReceipt, int64, error) {
	return s.receipts, int64(len(s.receipts)), nil
}
func (s *operationRouteSurvey) SaveOperationConfiguration(_ context.Context, value surveyport.OperationConfiguration, _ int64, _ string) (surveyport.OperationConfiguration, error) {
	s.saveCalls++
	s.configuration = value
	s.configuration.Version++
	return s.configuration, nil
}
func (s *operationRouteSurvey) QueueCompletionTest(context.Context, surveyport.ID, int64, string) (surveyport.CompletionTestReceipt, error) {
	s.testCalls++
	return s.testReceipt, s.testErr
}

type routeOAuth struct {
	completeErr error
	enabled     bool
	identity    surveyport.SubmissionIdentity
}

func (o routeOAuth) Enabled() bool { return o.enabled }
func (routeOAuth) Start(context.Context, string) (string, error) {
	return "https://open.weixin.qq.com/authorize", nil
}
func (o routeOAuth) Complete(context.Context, string, string) (string, string, error) {
	return "session", "/h5/all.html?slug=growth", o.completeErr
}
func (o routeOAuth) ResolveSession(context.Context, string) (surveyport.SubmissionIdentity, error) {
	return o.identity, nil
}

func newRouteHandler(t *testing.T, oauth routeOAuth) *Handler {
	t.Helper()
	handler, err := NewHandler(&routeDefinitions{}, &routeSurvey{questionnaire: surveyport.Questionnaire{Slug: "growth", Status: surveyport.StatusPublished, AnswerDisplayMode: surveyport.DisplayAllInOne}}, routeSecurity{}, oauth)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestPublicPublishUsesExplicitQuestionnaireVersionCAS(t *testing.T) {
	definitions := &publishVersionDefinitions{questionnaire: surveyport.Questionnaire{ID: 7, Version: 4, Status: surveyport.StatusDraft, Slug: "versioned"}}
	handler, err := NewHandler(definitions, &routeSurvey{}, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	stale := httptest.NewRequest(nethttp.MethodPost, "/api/admin/questionnaires/7/public-publish", strings.NewReader(`{"expected_questionnaire_version":3}`))
	stale.Header.Set("Idempotency-Key", "survey-public-publish-stale-0001")
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != nethttp.StatusConflict || definitions.publishedExpected != 3 || definitions.publishCalls != 1 {
		t.Fatalf("stale public publish response=%d expected=%d calls=%d body=%s", staleResponse.Code, definitions.publishedExpected, definitions.publishCalls, staleResponse.Body.String())
	}
	current := httptest.NewRequest(nethttp.MethodPost, "/api/admin/questionnaires/7/public-publish", strings.NewReader(`{"expected_questionnaire_version":4}`))
	current.Header.Set("Idempotency-Key", "survey-public-publish-current-0002")
	currentResponse := httptest.NewRecorder()
	handler.ServeHTTP(currentResponse, current)
	if currentResponse.Code != nethttp.StatusOK || definitions.publishedExpected != 4 || definitions.questionnaire.Status != surveyport.StatusPublished {
		t.Fatalf("current public publish response=%d expected=%d questionnaire=%+v body=%s", currentResponse.Code, definitions.publishedExpected, definitions.questionnaire, currentResponse.Body.String())
	}
}

func TestQuestionnaireExportFormatsBusinessTimestampsInShanghai(t *testing.T) {
	survey := &routeSurvey{submissions: []surveyport.Submission{{
		ID: 8, QuestionnaireID: 7, SubmittedAt: time.Date(2026, time.September, 5, 0, 1, 2, 611265000, time.UTC), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved},
	}}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/admin/questionnaires/7/export", nil))
	if response.Code != nethttp.StatusOK || survey.exportCalls != 1 || !strings.Contains(response.Body.String(), "2026-09-05 08:01:02") || strings.Contains(response.Body.String(), "2026-09-05T00:01:02") || !strings.Contains(response.Body.String(), "已关联客户") || strings.Contains(response.Body.String(), ",resolved,") {
		t.Fatalf("business CSV did not use Shanghai display time: status=%d calls=%d body=%q", response.Code, survey.exportCalls, response.Body.String())
	}
}

func TestQuestionnaireExportRejectsViewer(t *testing.T) {
	survey := &routeSurvey{}
	viewer := accessdomain.Principal{InternalID: 8, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	handler, err := NewHandler(&routeDefinitions{}, survey, principalSecurity{principal: viewer})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/admin/questionnaires/7/export", nil))
	if response.Code != nethttp.StatusForbidden || survey.exportCalls != 0 {
		t.Fatalf("viewer export status=%d calls=%d body=%s", response.Code, survey.exportCalls, response.Body.String())
	}
}

func TestDefinitionRequestAppliesFrozenSingleChoiceDefaultOnlyWhenAbsent(t *testing.T) {
	request := definitionRequest{Questions: []surveyport.Question{{Type: surveyport.QuestionSingleChoice, Title: "选择", Options: []surveyport.Option{{Text: "A"}, {Text: "B"}}}}}
	questionnaire := request.questionnaire()
	if questionnaire.Questions[0].Validation.MaximumSelections == nil || *questionnaire.Questions[0].Validation.MaximumSelections != 1 {
		t.Fatalf("frozen single-choice default=%+v", questionnaire.Questions[0].Validation)
	}
	explicit := 2
	request.Questions[0].Validation.MaximumSelections = &explicit
	questionnaire = request.questionnaire()
	if questionnaire.Questions[0].Validation.MaximumSelections == nil || *questionnaire.Questions[0].Validation.MaximumSelections != 2 {
		t.Fatalf("explicit validation was overwritten=%+v", questionnaire.Questions[0].Validation)
	}
}

func TestDefinitionRequestStripsOnlyFrozenHiddenOrdinaryDefaults(t *testing.T) {
	request := definitionRequest{
		Name: "frozen-normal", Title: "冻结普通问卷", AnswerDisplayMode: surveyport.DisplayAllInOne, Slug: "frozen-normal",
		AssessmentConfig: []byte(`{"dimensions":[{"hidden":true}]}`),
		Questions: []surveyport.Question{{Type: surveyport.QuestionSingleChoice, Title: "选择", SortOrder: 0, Options: []surveyport.Option{
			{Text: "A", SortOrder: 0, OtherMaximumLength: 80},
			{Text: "B", SortOrder: 1, OtherMaximumLength: 80},
		}}},
	}
	questionnaire := request.questionnaire()
	if string(questionnaire.AssessmentConfig) != "{}" || questionnaire.Questions[0].Options[0].OtherMaximumLength != 0 || questionnaire.Questions[0].Options[1].OtherMaximumLength != 0 {
		t.Fatalf("hidden frozen defaults were retained=%+v", questionnaire)
	}
	if err := surveydomain.ValidateQuestionnaire(questionnaire); err != nil {
		t.Fatalf("normalized frozen ordinary definition is invalid: %v", err)
	}
	request.AssessmentEnabled = true
	request.AssessmentConfig = []byte(`{"template_id":"template-1"}`)
	request.Questions[0].Options[0].IsOther = true
	request.Questions[0].Options[0].OtherPlaceholder = "请填写"
	request.Questions[0].Options[0].OtherMaximumLength = 80
	assessment := request.questionnaire()
	if string(assessment.AssessmentConfig) != `{"template_id":"template-1"}` || !assessment.Questions[0].Options[0].IsOther || assessment.Questions[0].Options[0].OtherMaximumLength != 80 {
		t.Fatalf("enabled configuration was changed=%+v", assessment)
	}
}

func TestDefinitionResponseMarksDraftAsDisabledForFrozenEditor(t *testing.T) {
	draft := definitionResponse(surveyport.Questionnaire{Status: surveyport.StatusDraft})
	published := definitionResponse(surveyport.Questionnaire{Status: surveyport.StatusPublished})
	archived := definitionResponse(surveyport.Questionnaire{Status: surveyport.StatusArchived})
	if draft["is_disabled"] != true || draft["status"] != "disabled" || published["is_disabled"] != false || published["status"] != "active" || archived["is_disabled"] != true || archived["status"] != "archived" {
		t.Fatalf("frozen editor lifecycle DTO draft=%+v published=%+v archived=%+v", draft, published, archived)
	}
}

func TestDefinitionDeleteArchivesWithFrozenVersionAndExactReplay(t *testing.T) {
	definitions := &archiveDefinitions{questionnaire: surveyport.Questionnaire{ID: 7, Version: 4, Status: surveyport.StatusPublished}}
	handler, err := NewHandler(definitions, &routeSurvey{}, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(nethttp.MethodDelete, "/api/admin/questionnaires/7", strings.NewReader(`{"expected_version":4}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "survey-definition-archive-http-0001")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != nethttp.StatusOK || definitions.questionnaire.Status != surveyport.StatusArchived || !strings.Contains(response.Body.String(), `"write_model_status":"archived"`) || strings.Contains(response.Body.String(), "hard_delete") {
			t.Fatalf("attempt=%d archive response=%d body=%s state=%+v calls=%d", attempt, response.Code, response.Body.String(), definitions.questionnaire, definitions.setStatusCalls)
		}
	}
	if definitions.getCalls != 0 {
		t.Fatalf("DELETE must not replace the displayed version with a server GET, calls=%d", definitions.getCalls)
	}
	if definitions.setStatusCalls != 2 {
		t.Fatalf("exact replay was not delegated with its original body/key calls=%d", definitions.setStatusCalls)
	}

	stale := httptest.NewRequest(nethttp.MethodDelete, "/api/admin/questionnaires/7", strings.NewReader(`{"expected_version":3}`))
	stale.Header.Set("Content-Type", "application/json")
	stale.Header.Set("Idempotency-Key", "survey-definition-archive-http-stale-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, stale)
	if response.Code != nethttp.StatusConflict || definitions.questionnaire.Version != 5 {
		t.Fatalf("stale archive must preserve the later definition: status=%d body=%s state=%+v", response.Code, response.Body.String(), definitions.questionnaire)
	}
}

func TestPublicSurveyCannotBypassOAuth(t *testing.T) {
	handler := newRouteHandler(t, routeOAuth{enabled: true})
	for _, path := range []string{"/api/public/questionnaires/growth", "/api/public/questionnaires/growth/submissions"} {
		method := nethttp.MethodGet
		if strings.HasSuffix(path, "/submissions") {
			method = nethttp.MethodPost
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != nethttp.StatusUnauthorized || !strings.Contains(response.Body.String(), "survey_oauth_required") {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestSubmittedPublicRoutesExposeOnlyCompletionAction(t *testing.T) {
	customerID := customerdomain.CustomerID(42)
	survey := &routeSurvey{
		questionnaire: surveyport.Questionnaire{Slug: "growth", Status: surveyport.StatusPublished, AnswerDisplayMode: surveyport.DisplayAllInOne},
		publicStatus:  surveyport.PublicSubmissionStatus{Submitted: true, CompletionAction: surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: "https://go.example.test/complete"}},
		submitErr:     &surveyport.AlreadySubmittedError{CompletionAction: surveyport.CompletionAction{Type: surveyport.CompletionActionLeadQR, LeadQR: &surveyport.CompletionLeadQRCode{URL: "https://cdn.example.test/lead.png"}}},
	}
	handler, err := NewHandler(&routeDefinitions{}, survey, routeSecurity{}, routeOAuth{enabled: true, identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customerID}})
	if err != nil {
		t.Fatal(err)
	}
	cookie := &nethttp.Cookie{Name: "__Host-aicrm_survey_identity", Value: strings.Repeat("a", 43)}

	definition := httptest.NewRequest(nethttp.MethodGet, "/api/public/questionnaires/growth", nil)
	definition.AddCookie(cookie)
	definitionResponse := httptest.NewRecorder()
	handler.ServeHTTP(definitionResponse, definition)
	if definitionResponse.Code != nethttp.StatusConflict || !strings.Contains(definitionResponse.Body.String(), `"code":"already_submitted"`) || !strings.Contains(definitionResponse.Body.String(), `"redirect_url":"https://go.example.test/complete"`) || strings.Contains(definitionResponse.Body.String(), "42") {
		t.Fatalf("definition status=%d body=%s", definitionResponse.Code, definitionResponse.Body.String())
	}

	session := httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/session?slug=growth", nil)
	session.AddCookie(cookie)
	sessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(sessionResponse, session)
	if sessionResponse.Code != nethttp.StatusOK || !strings.Contains(sessionResponse.Body.String(), `"submitted":true`) || strings.Contains(sessionResponse.Body.String(), "42") {
		t.Fatalf("session status=%d body=%s", sessionResponse.Code, sessionResponse.Body.String())
	}

	entry := httptest.NewRequest(nethttp.MethodGet, "/q/growth", nil)
	entry.AddCookie(cookie)
	entryResponse := httptest.NewRecorder()
	handler.ServeHTTP(entryResponse, entry)
	if entryResponse.Code != nethttp.StatusSeeOther || entryResponse.Header().Get("Location") != "https://go.example.test/complete" {
		t.Fatalf("entry status=%d location=%q", entryResponse.Code, entryResponse.Header().Get("Location"))
	}

	submit := httptest.NewRequest(nethttp.MethodPost, "/api/public/questionnaires/growth/submissions", strings.NewReader(`{"version":1,"submission_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","answers":[]}`))
	submit.AddCookie(cookie)
	submitResponse := httptest.NewRecorder()
	handler.ServeHTTP(submitResponse, submit)
	if submitResponse.Code != nethttp.StatusConflict || !strings.Contains(submitResponse.Body.String(), `"type":"lead_qr"`) || !strings.Contains(submitResponse.Body.String(), `"lead_qr":{"url":"https://cdn.example.test/lead.png"}`) || strings.Contains(submitResponse.Body.String(), "42") {
		t.Fatalf("submit status=%d body=%s", submitResponse.Code, submitResponse.Body.String())
	}

	// Stored completion targets may intentionally be same-origin paths. The
	// session response and a revisited /q entry must carry the exact same safe
	// action rather than falling back to the answer form or done carrier.
	survey.publicStatus = surveyport.PublicSubmissionStatus{Submitted: true, CompletionAction: surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: "/h5/finished?source=survey"}}
	relativeSession := httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/session?slug=growth", nil)
	relativeSession.AddCookie(cookie)
	relativeSessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(relativeSessionResponse, relativeSession)
	if relativeSessionResponse.Code != nethttp.StatusOK || !strings.Contains(relativeSessionResponse.Body.String(), `"redirect_url":"/h5/finished?source=survey"`) {
		t.Fatalf("relative session status=%d body=%s", relativeSessionResponse.Code, relativeSessionResponse.Body.String())
	}
	relativeEntry := httptest.NewRequest(nethttp.MethodGet, "/q/growth", nil)
	relativeEntry.AddCookie(cookie)
	relativeEntryResponse := httptest.NewRecorder()
	handler.ServeHTTP(relativeEntryResponse, relativeEntry)
	if relativeEntryResponse.Code != nethttp.StatusSeeOther || relativeEntryResponse.Header().Get("Location") != "/h5/finished?source=survey" {
		t.Fatalf("relative entry status=%d location=%q", relativeEntryResponse.Code, relativeEntryResponse.Header().Get("Location"))
	}
}

func TestPublicSubmissionSuccessEmitsCompletionActionAndLegacyResultTokenAtTopLevel(t *testing.T) {
	customerID := customerdomain.CustomerID(42)
	survey := &routeSurvey{
		questionnaire: surveyport.Questionnaire{Slug: "growth", Status: surveyport.StatusPublished, AnswerDisplayMode: surveyport.DisplayAllInOne},
		submitReceipt: surveyport.SubmissionReceipt{
			QuestionnaireID: 7, QuestionnaireSlug: "growth", DefinitionVersion: 1, SubmissionID: 9, ResultToken: strings.Repeat("r", 43),
			CompletionAction: surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: "https://go.example.test/complete"},
		},
	}
	handler, err := NewHandler(&routeDefinitions{}, survey, routeSecurity{}, routeOAuth{enabled: true, identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customerID}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(nethttp.MethodPost, "/api/public/questionnaires/growth/submissions", strings.NewReader(`{"version":1,"submission_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","answers":[]}`))
	request.AddCookie(&nethttp.Cookie{Name: "__Host-aicrm_survey_identity", Value: strings.Repeat("a", 43)})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != nethttp.StatusCreated || strings.Count(body, `"completion_action"`) != 1 || !strings.Contains(body, `"completion_action":{"type":"redirect","redirect_url":"https://go.example.test/complete"}`) || !strings.Contains(body, `"receipt":{"questionnaire_id":7,"questionnaire_slug":"growth","definition_version":1,"submission_id":9}`) || !strings.Contains(body, `"result_token":"`+strings.Repeat("r", 43)+`"`) || strings.Contains(body, `"submission_id":9,"result_token"`) {
		t.Fatalf("submit status=%d body=%s", response.Code, body)
	}
}

func TestCompletionLocationFallsBackToDoneCarrier(t *testing.T) {
	if got := completionLocation("growth", surveyport.DefaultCompletionAction()); got != "/h5/done.html?slug=growth" {
		t.Fatalf("default location=%q", got)
	}
	if got := completionLocation("growth", surveyport.CompletionAction{Type: surveyport.CompletionActionLeadQR}); got != "/h5/done.html?slug=growth" {
		t.Fatalf("lead QR location=%q", got)
	}
	if got := completionLocation("growth", surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: "http://unsafe.example.test"}); got != "/h5/done.html?slug=growth" {
		t.Fatalf("unsafe redirect location=%q", got)
	}
	if got := completionLocation("growth", surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: "https://safe.example.test/complete#fragment"}); got != "/h5/done.html?slug=growth" {
		t.Fatalf("fragment redirect location=%q", got)
	}
	if got := completionLocation("growth", surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: "/h5/finished?source=survey"}); got != "/h5/finished?source=survey" {
		t.Fatalf("same-origin redirect location=%q", got)
	}
	if got := completionLocation("growth", surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: "https://10.0.0.1/complete"}); got != "/h5/done.html?slug=growth" {
		t.Fatalf("private redirect location=%q", got)
	}
}

func TestOperationMetadataPUTRequiresCSRFAndCurrentConfigurationVersion(t *testing.T) {
	survey := &operationRouteSurvey{configuration: surveyport.OperationConfiguration{QuestionnaireID: 7, ExternalPushEnabled: false, ExternalPushConfigurationRef: "push.v2", ExternalPushMetadata: []byte(`{"remark":"changed-by-b"}`), Version: 2}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"push.v1","metadata":{"remark":"stale-a"},"configuration_version":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "survey-operation-http-cas-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusConflict || !strings.Contains(response.Body.String(), "configuration_conflict") || survey.saveCalls != 0 {
		t.Fatalf("stale response=%d body=%s saves=%d", response.Code, response.Body.String(), survey.saveCalls)
	}
	if survey.configuration.ExternalPushEnabled || survey.configuration.ExternalPushConfigurationRef != "push.v2" {
		t.Fatalf("stale HTTP request overwrote concurrent config: %+v", survey.configuration)
	}

	csrfHandler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{csrfErr: errors.New("csrf rejected")})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":false,"configuration_reference":"push.v2","metadata":{},"configuration_version":2}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "survey-operation-http-csrf-0002")
	response = httptest.NewRecorder()
	csrfHandler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusForbidden || !strings.Contains(response.Body.String(), "csrf_invalid") || survey.saveCalls != 0 {
		t.Fatalf("csrf response=%d body=%s saves=%d", response.Code, response.Body.String(), survey.saveCalls)
	}
}

func TestLegacyQuestionnaireOpsSaveJourneyPreservesExternalPushMetadata(t *testing.T) {
	// This is the exact existing web save order: completion PUT, followed by an
	// external-push PUT that contains only the toggle and opaque reference.
	survey := &operationRouteSurvey{configuration: surveyport.OperationConfiguration{QuestionnaireID: 7, ExternalPushEnabled: true, ExternalPushConfigurationRef: "push.v1", ExternalPushMetadata: []byte(`{"remark":"keep-me","custom_params":{"campaign":"autumn"}}`), Version: 1}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	completion := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/completion", strings.NewReader(`{"navigation_target_id":"completion.done","channel_id":19}`))
	completion.Header.Set("Content-Type", "application/json")
	completion.Header.Set("Idempotency-Key", "survey-legacy-ops-completion-0001")
	completionResponse := httptest.NewRecorder()
	handler.ServeHTTP(completionResponse, completion)
	if completionResponse.Code != nethttp.StatusOK || survey.configuration.Version != 2 {
		t.Fatalf("legacy completion response=%d config=%+v", completionResponse.Code, survey.configuration)
	}

	push := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"push.v2"}`))
	push.Header.Set("Content-Type", "application/json")
	push.Header.Set("Idempotency-Key", "survey-legacy-ops-push-0002")
	pushResponse := httptest.NewRecorder()
	handler.ServeHTTP(pushResponse, push)
	if pushResponse.Code != nethttp.StatusOK || survey.saveCalls != 2 || survey.configuration.Version != 3 || !survey.configuration.ExternalPushEnabled || survey.configuration.ExternalPushConfigurationRef != "push.v2" || string(survey.configuration.ExternalPushMetadata) != `{"remark":"keep-me","custom_params":{"campaign":"autumn"}}` {
		t.Fatalf("legacy save journey lost operations data: status=%d config=%+v saves=%d", pushResponse.Code, survey.configuration, survey.saveCalls)
	}

	newMetadataWithoutVersion := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"push.v2","metadata":{"remark":"must-carry-version"}}`))
	newMetadataWithoutVersion.Header.Set("Content-Type", "application/json")
	newMetadataWithoutVersion.Header.Set("Idempotency-Key", "survey-legacy-ops-metadata-0003")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, newMetadataWithoutVersion)
	if metadataResponse.Code != nethttp.StatusBadRequest || !strings.Contains(metadataResponse.Body.String(), "configuration_version_required") || survey.saveCalls != 2 {
		t.Fatalf("metadata without version response=%d body=%s saves=%d", metadataResponse.Code, metadataResponse.Body.String(), survey.saveCalls)
	}
}

func TestOperationConfigurationListsAndEnforcesCompositionTargetCatalog(t *testing.T) {
	survey := &operationRouteSurvey{configuration: surveyport.OperationConfiguration{QuestionnaireID: 7, Version: 4}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	handler.SetCompletionTargetCatalog(completionTargetCatalogStub{references: []string{"survey.crm.primary", "survey.crm.trial"}})

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(nethttp.MethodGet, "/api/admin/questionnaires/7/operations", nil))
	if read.Code != nethttp.StatusOK || !strings.Contains(read.Body.String(), `"target_catalog_available":true`) || !strings.Contains(read.Body.String(), `"available_configuration_references":["survey.crm.primary","survey.crm.trial"]`) || strings.Contains(read.Body.String(), "endpoint") || strings.Contains(read.Body.String(), "signing_key") {
		t.Fatalf("catalog read status=%d body=%s", read.Code, read.Body.String())
	}

	unknown := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"operator-typed-unknown"}`))
	unknown.Header.Set("Content-Type", "application/json")
	unknown.Header.Set("Idempotency-Key", "survey-operation-catalog-unknown-0001")
	unknownResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != nethttp.StatusBadRequest || !strings.Contains(unknownResponse.Body.String(), "configuration_reference_unavailable") || survey.saveCalls != 0 {
		t.Fatalf("unknown target status=%d body=%s saves=%d", unknownResponse.Code, unknownResponse.Body.String(), survey.saveCalls)
	}

	known := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"survey.crm.primary"}`))
	known.Header.Set("Content-Type", "application/json")
	known.Header.Set("Idempotency-Key", "survey-operation-catalog-known-0002")
	knownResponse := httptest.NewRecorder()
	handler.ServeHTTP(knownResponse, known)
	if knownResponse.Code != nethttp.StatusOK || survey.saveCalls != 1 || survey.configuration.ExternalPushConfigurationRef != "survey.crm.primary" || !survey.configuration.ExternalPushEnabled {
		t.Fatalf("known target status=%d body=%s config=%+v saves=%d", knownResponse.Code, knownResponse.Body.String(), survey.configuration, survey.saveCalls)
	}
}

func boolTestPointer(value bool) *bool    { return &value }
func int32TestPointer(value int32) *int32 { return &value }

func TestExternalPushLogsPreserveSyntheticRunAndTerminalStatus(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	survey := &operationRouteSurvey{receipts: []surveyport.OperationReceipt{
		{ID: 8, QuestionnaireID: 7, SourcePK: "questionnaire-test-0123456789abcdef0123456789abcdef", Status: "queued", OccurrenceCount: 0, OccurredAt: now},
		{ID: 9, QuestionnaireID: 7, SourcePK: "questionnaire-test-fedcba9876543210fedcba9876543210", Status: "executed", OccurrenceCount: 99, ProviderCallAttempted: boolTestPointer(true), ProviderRealCallExecuted: boolTestPointer(true), ProviderResultReceived: boolTestPointer(true), ProviderAttemptNumber: int32TestPointer(1), RealEffectExecuted: true, OccurredAt: now},
		{ID: 10, QuestionnaireID: 7, SourcePK: "questionnaire-test-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "outcome_unknown", OccurrenceCount: 99, ProviderCallAttempted: boolTestPointer(true), ProviderRealCallExecuted: boolTestPointer(true), ProviderResultReceived: boolTestPointer(false), ProviderAttemptNumber: int32TestPointer(1), RealEffectExecuted: true, OccurredAt: now},
		{ID: 11, QuestionnaireID: 7, Status: "disabled", OccurrenceCount: 0, ReadOnlyLegacy: true, OccurredAt: now},
		{ID: 12, QuestionnaireID: 7, Status: "legacy_success", OccurrenceCount: 4, ReadOnlyLegacy: true, OccurredAt: now},
		{ID: 13, QuestionnaireID: 7, Status: "legacy_failed", OccurrenceCount: 2, ReadOnlyLegacy: true, OccurredAt: now},
	}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/admin/questionnaires/external-push-logs", nil))
	body := response.Body.String()
	if response.Code != nethttp.StatusOK || !strings.Contains(body, `"test_run_id":"questionnaire-test-0123456789abcdef0123456789abcdef"`) || !strings.Contains(body, `"status":"executed"`) || !strings.Contains(body, `"status":"outcome_unknown"`) || !strings.Contains(body, `"test_run_id":11`) || !strings.Contains(body, `"status":"disabled"`) || !strings.Contains(body, `"status":"legacy_success"`) || !strings.Contains(body, `"status":"legacy_failed"`) || !strings.Contains(body, `"attempt_count":1`) || !strings.Contains(body, `"provider_result_received":false`) || strings.Contains(body, `"attempt_count":99`) || strings.Contains(body, `"local_only":true`) {
		t.Fatalf("logs response=%d body=%s", response.Code, body)
	}
}

func TestExternalPushTestUsesSyntheticQueueAndFailsClosedWhenDisabled(t *testing.T) {
	survey := &operationRouteSurvey{testReceipt: surveyport.CompletionTestReceipt{QuestionnaireID: 7, TestRunID: "questionnaire-test-0123456789abcdef0123456789abcdef", EffectID: "eer_7", State: "queued"}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(nethttp.MethodPost, "/api/admin/questionnaires/7/operations/external-push/test", nil)
	request.Header.Set("Idempotency-Key", "survey-completion-test-http-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusAccepted || survey.testCalls != 1 || !strings.Contains(response.Body.String(), "questionnaire-test-") || !strings.Contains(response.Body.String(), "synthetic_data") || !strings.Contains(response.Body.String(), "\"accepted\":true") || strings.Contains(response.Body.String(), "attempt_count") || strings.Contains(response.Body.String(), "provider_result_received") {
		t.Fatalf("test queue response=%d body=%s calls=%d", response.Code, response.Body.String(), survey.testCalls)
	}
	survey.testErr = surveyport.ErrEffectUnavailable
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusConflict || !strings.Contains(response.Body.String(), "provider_disabled") || survey.testCalls != 2 {
		t.Fatalf("disabled test response=%d body=%s calls=%d", response.Code, response.Body.String(), survey.testCalls)
	}
}

func TestExternalPushTestRequiresCSRFBeforeAcceptingSyntheticEffect(t *testing.T) {
	survey := &operationRouteSurvey{testReceipt: surveyport.CompletionTestReceipt{QuestionnaireID: 7, TestRunID: "questionnaire-test-0123456789abcdef0123456789abcdef", EffectID: "eer_7", State: "queued"}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{csrfErr: errors.New("csrf rejected")})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(nethttp.MethodPost, "/api/admin/questionnaires/7/operations/external-push/test", nil)
	request.Header.Set("Idempotency-Key", "survey-completion-test-http-csrf-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusForbidden || !strings.Contains(response.Body.String(), "csrf_invalid") || survey.testCalls != 0 {
		t.Fatalf("csrf response=%d body=%s calls=%d", response.Code, response.Body.String(), survey.testCalls)
	}
}

func TestSessionEndpointFailsClosedAndHidesCustomerIdentity(t *testing.T) {
	disabled := newRouteHandler(t, routeOAuth{})
	response := httptest.NewRecorder()
	disabled.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/session?slug=growth", nil))
	if response.Code != nethttp.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "survey_oauth_unavailable") {
		t.Fatalf("disabled status=%d body=%s", response.Code, response.Body.String())
	}

	customerID := customerdomain.CustomerID(42)
	enabled := newRouteHandler(t, routeOAuth{enabled: true, identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customerID}})
	request := httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/session?slug=growth", nil)
	request.AddCookie(&nethttp.Cookie{Name: "__Host-aicrm_survey_identity", Value: strings.Repeat("a", 43)})
	response = httptest.NewRecorder()
	enabled.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || strings.Contains(response.Body.String(), "42") || strings.Contains(response.Body.String(), "openid") || strings.Contains(response.Body.String(), "unionid") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPublicEntryUsesUnifiedOAuthGate(t *testing.T) {
	handler := newRouteHandler(t, routeOAuth{enabled: true})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/q/growth", nil))
	if response.Code != nethttp.StatusSeeOther || response.Header().Get("Location") != "/h5/auth.html?slug=growth" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	request := httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/oauth/start?slug=growth", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), "survey_wechat_required") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOAuthFailureReturnsToNonAutomaticRetry(t *testing.T) {
	handler := newRouteHandler(t, routeOAuth{enabled: true, completeErr: fmt.Errorf("provider failure")})
	request := httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/oauth/callback?state=opaque&code=bad", nil)
	request.AddCookie(&nethttp.Cookie{Name: "survey_oauth_return", Value: "growth"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 303 || response.Header().Get("Location") != "/h5/auth.html?oauth_error=1&slug=growth" {
		t.Fatalf("unexpected retry: %d %s", response.Code, response.Header().Get("Location"))
	}
}
