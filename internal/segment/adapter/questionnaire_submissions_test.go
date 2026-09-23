package adapter

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"reflect"
	"testing"
	"time"
)

type submissionFacts struct {
	rows []surveyport.AudienceSubmission
}

func (f submissionFacts) AudienceSubmissions(context.Context, time.Time) ([]surveyport.AudienceSubmission, error) {
	return f.rows, nil
}
func (f submissionFacts) AudienceRecognizedContacts(_ context.Context, scope string, _ time.Time) ([]customerdomain.CustomerID, error) {
	if scope != "wecom-corp:test" {
		return nil, ErrCustomerReadUnavailable
	}
	return []customerdomain.CustomerID{1, 2}, nil
}
func TestSubmissionRuleAnyQuestionnaireCanonicalDedupAndRecognizedIdentity(t *testing.T) {
	at := time.Now().UTC()
	f := submissionFacts{[]surveyport.AudienceSubmission{
		{CustomerID: 1, QuestionnaireID: 21, SubmissionID: 1, SubmittedAt: at},
		{CustomerID: 1, QuestionnaireID: 37, SubmissionID: 2, SubmittedAt: at},
		{CustomerID: 2, QuestionnaireID: 37, SubmissionID: 3, SubmittedAt: at},
		{CustomerID: 3, QuestionnaireID: 21, SubmissionID: 4, SubmittedAt: at},
		{CustomerID: 2, QuestionnaireID: 55, SubmissionID: 5, SubmittedAt: at},
		{CustomerID: 4, QuestionnaireID: 21, SubmissionID: 6, SubmittedAt: at.Add(time.Hour)},
		{CustomerID: 0, QuestionnaireID: 21, SubmissionID: 7, SubmittedAt: at},
	}}
	s := LegacyTemplateSource{Submissions: f, RecognizedContacts: f, PrimaryOwnerCorpScope: "wecom-corp:test"}
	for _, c := range []struct {
		required string
		want     []customerdomain.CustomerID
	}{{"true", []customerdomain.CustomerID{1, 2}}, {"false", []customerdomain.CustomerID{1, 2, 3}}} {
		d := legacyDefinition(t, segmentdsl.QuestionnaireSubmissions, `{"questionnaire_ids":["21","37"],"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":`+c.required+`}`)
		got, err := s.Evaluate(context.Background(), d, at)
		if err != nil || !reflect.DeepEqual(got.CustomerIDs, c.want) {
			t.Fatalf("got=%v err=%v", got.CustomerIDs, err)
		}
	}
	s.RecognizedContacts = nil
	d := legacyDefinition(t, segmentdsl.QuestionnaireSubmissions, `{"questionnaire_ids":["21"],"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":true}`)
	if _, err := s.Evaluate(context.Background(), d, at); err == nil {
		t.Fatal("missing evidence reader must fail closed")
	}
}
