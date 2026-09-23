package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type v1ExternalSurveyStub struct {
	queries []surveyport.ExternalSubmissionQuery
	pages   []surveyport.ExternalSubmissionPage
	err     error
}

func (s *v1ExternalSurveyStub) ExternalSubmissions(_ context.Context, query surveyport.ExternalSubmissionQuery) (surveyport.ExternalSubmissionPage, error) {
	s.queries = append(s.queries, query)
	if s.err != nil {
		return surveyport.ExternalSubmissionPage{}, s.err
	}
	if len(s.pages) == 0 {
		return surveyport.ExternalSubmissionPage{}, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

type v1ExternalAliasStub struct {
	union string
	err   error
}

func (s v1ExternalAliasStub) VerifiedUnionID(_ context.Context, _ customerdomain.CustomerID, _ string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	return s.union, s.union != "", nil
}
func (v1ExternalAliasStub) RevealPhoneForMachine(context.Context, customerdomain.CustomerID, accessdomain.MachinePrincipal) (string, bool, error) {
	return "", false, nil
}

func v1SurveyScopes() openPlatformIdentityScopes {
	return openPlatformIdentityScopes{
		UnionScopes:       []string{"wechat-open-platform:survey"},
		SurveyUnionScopes: []string{"wechat-open-platform:survey"},
	}
}

func TestV1QuestionnaireSubmissionsScopesBeforePaginationAndBindsOpaqueCursor(t *testing.T) {
	firstAt := time.Date(2026, 9, 13, 2, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(-time.Minute)
	thirdAt := secondAt.Add(-time.Minute)
	survey := &v1ExternalSurveyStub{pages: []surveyport.ExternalSubmissionPage{
		{Items: []surveyport.ExternalSubmission{
			{SubmissionID: 31, SourceSystem: "legacy-survey", SourceRecordID: "legacy-31", QuestionnaireSourceID: 9, DefinitionVersion: 4, QuestionnaireTitle: "Questionnaire", SubmittedAt: firstAt, FinalTags: json.RawMessage(`["tag"]`), AssessmentResult: json.RawMessage(`{"grade":"A"}`), Answers: []surveyport.ExternalSubmissionAnswer{{QuestionTitle: "Question", TextValue: "snapshot"}}},
			{SubmissionID: 30, SourceSystem: "legacy-survey", SourceRecordID: "legacy-30", QuestionnaireSourceID: 9, DefinitionVersion: 4, QuestionnaireTitle: "Questionnaire", SubmittedAt: secondAt},
		}, Total: 2, Limit: 2},
		{Items: []surveyport.ExternalSubmission{{SubmissionID: 29, SourceSystem: "aicrm_v3", SourceRecordID: "29", QuestionnaireSourceID: 9, DefinitionVersion: 4, QuestionnaireTitle: "Questionnaire", SubmittedAt: thirdAt, Answers: []surveyport.ExternalSubmissionAnswer{}}}, Total: 1, Limit: 2},
	}}
	executor := &openPlatformExecutor{
		survey:              survey,
		surveyAliases:       v1ExternalAliasStub{union: "trusted-union"},
		scopes:              v1SurveyScopes(),
		v1ExternalCursorKey: []byte("external-records-test-key-32bytes"),
	}
	input := []byte(`{"customer_id":7,"questionnaire_id":9,"source_system":"legacy-survey","source_record_id":"legacy-31","submitted_from":1789261200,"submitted_to":1789264800,"limit":1}`)
	principal := accessdomain.MachinePrincipal{ClientID: "external-records", ClientRecord: 7, Audience: "external_integration", AuthVersion: 3}
	first, err := executor.v1QuestionnaireSubmissions(context.Background(), principal, input)
	if err != nil {
		t.Fatal(err)
	}
	data := first.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) != 1 || data["customer_id"] != "7" || items[0]["submission_id"] != "31" || items[0]["questionnaire_id"] != "9" || items[0]["customer_id"] != "7" || items[0]["definition_version"] != int64(4) || items[0]["identity_status"] != "resolved" || items[0]["source_system"] != "legacy-survey" || items[0]["source_record_id"] != "legacy-31" {
		t.Fatalf("result=%#v", first.Data)
	}
	if _, leaked := items[0]["unionid"]; leaked {
		t.Fatalf("historical union leaked: %#v", items[0])
	}
	next, ok := data["next_cursor"].(string)
	if !ok || next == "" || len(survey.queries) != 1 {
		t.Fatalf("missing cursor or query=%+v result=%#v", survey.queries, first.Data)
	}
	firstQuery := survey.queries[0]
	if firstQuery.CustomerID != 7 || len(firstQuery.HistoricalUnionIDs) != 1 || firstQuery.HistoricalUnionIDs[0] != "trusted-union" || !firstQuery.SubmittedEndExclusive || firstQuery.SourceSystem != "legacy-survey" || firstQuery.SourceRecordID != "legacy-31" || firstQuery.Limit != 2 || !firstQuery.SubmittedFrom.Equal(time.Unix(1789261200, 0).UTC()) || !firstQuery.SubmittedTo.Equal(time.Unix(1789264800, 0).UTC()) || !firstQuery.BeforeSubmittedAt.IsZero() {
		t.Fatalf("first owner query=%+v", firstQuery)
	}
	secondInput := []byte(`{"customer_id":7,"questionnaire_id":9,"source_system":"legacy-survey","source_record_id":"legacy-31","submitted_from":1789261200,"submitted_to":1789264800,"limit":1,"cursor":` + mustJSON(t, next) + `}`)
	second, err := executor.v1QuestionnaireSubmissions(context.Background(), principal, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(survey.queries) != 2 || !survey.queries[1].BeforeSubmittedAt.Equal(firstAt) || survey.queries[1].BeforeSubmissionID != 31 || !survey.queries[1].SubmittedTo.Equal(firstQuery.SubmittedTo) || len(second.Data.(map[string]any)["items"].([]map[string]any)) != 1 {
		t.Fatalf("second=%#v queries=%+v", second.Data, survey.queries)
	}
	bad := []byte(`{"customer_id":7,"questionnaire_id":9,"source_system":"legacy-survey","source_record_id":"different","limit":1,"cursor":` + mustJSON(t, next) + `}`)
	if _, err = executor.v1QuestionnaireSubmissions(context.Background(), principal, bad); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation {
		t.Fatalf("filter changed cursor error=%v", err)
	}
}

func TestV1QuestionnaireSubmissionsFreezesImplicitEndAndRejectsInconsistentPage(t *testing.T) {
	at := time.Date(2026, 9, 13, 4, 0, 0, 0, time.UTC)
	survey := &v1ExternalSurveyStub{pages: []surveyport.ExternalSubmissionPage{{Total: 1}}}
	executor := &openPlatformExecutor{
		survey:              survey,
		surveyAliases:       v1ExternalAliasStub{union: "trusted-union"},
		scopes:              v1SurveyScopes(),
		activityNow:         func() time.Time { return at },
		v1ExternalCursorKey: []byte("external-records-test-key-32bytes"),
	}
	_, err := executor.v1QuestionnaireSubmissions(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7}`))
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable || len(survey.queries) != 1 || !survey.queries[0].SubmittedTo.Equal(at) || survey.queries[0].Limit != 101 {
		t.Fatalf("error=%v query=%+v", err, survey.queries)
	}
}

func TestV1QuestionnaireSubmissionsRejectsScopeBeforeCallingSurvey(t *testing.T) {
	survey := &v1ExternalSurveyStub{}
	executor := &openPlatformExecutor{survey: survey, surveyAliases: v1ExternalAliasStub{}, scopes: v1SurveyScopes(), v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	principal := accessdomain.MachinePrincipal{ClientID: "scoped", OwnerScope: accessdomain.OwnerScope{"customer_id": {"8"}}}
	_, err := executor.v1QuestionnaireSubmissions(context.Background(), principal, []byte(`{"customer_id":7}`))
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorNotFound || len(survey.queries) != 0 {
		t.Fatalf("scope error=%v queries=%+v", err, survey.queries)
	}
}

func TestV1QuestionnaireSubmissionsDoesNotTurnAliasFailureIntoEmptyPage(t *testing.T) {
	survey := &v1ExternalSurveyStub{}
	executor := &openPlatformExecutor{survey: survey, surveyAliases: v1ExternalAliasStub{err: errors.New("identity offline")}, scopes: v1SurveyScopes(), v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	_, err := executor.v1QuestionnaireSubmissions(context.Background(), accessdomain.MachinePrincipal{}, []byte(`{"customer_id":7}`))
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable || len(survey.queries) != 0 {
		t.Fatalf("alias error=%v queries=%+v", err, survey.queries)
	}
}
