package main

import (
	"context"
	"testing"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type audienceSurveyDefinitionsStub struct {
	pages map[string]surveyport.Page
	items map[surveyport.ID]surveyport.Questionnaire
}

func (s audienceSurveyDefinitionsStub) List(_ context.Context, _, _ int32, search string, _ surveyport.QuestionnaireStatus) (surveyport.Page, error) {
	return s.pages[search], nil
}
func (s audienceSurveyDefinitionsStub) Get(_ context.Context, id surveyport.ID) (surveyport.Questionnaire, error) {
	item, ok := s.items[id]
	if !ok {
		return surveyport.Questionnaire{}, surveyport.ErrNotFound
	}
	return item, nil
}

func TestAudienceSurveyReferenceResolvesWithinQuestionnaireAndQuestionScopes(t *testing.T) {
	questionnaire := surveyport.Questionnaire{ID: 101, Title: "客户调研", Questions: []surveyport.Question{
		{ID: 202, Title: "获客方式", Options: []surveyport.Option{{ID: 303, Text: "内容"}, {ID: 305, Text: "投放"}}},
		{ID: 203, Title: "成交方式", Options: []surveyport.Option{{ID: 304, Text: "内容"}}},
	}}
	other := surveyport.Questionnaire{ID: 102, Title: "另一问卷", Questions: []surveyport.Question{{ID: 402, Title: "获客方式", Options: []surveyport.Option{{ID: 403, Text: "内容"}}}}}
	reader := audienceSurveyDefinitionsStub{
		pages: map[string]surveyport.Page{"客户调研": {Items: []surveyport.Questionnaire{questionnaire}, Total: 1}},
		items: map[surveyport.ID]surveyport.Questionnaire{101: questionnaire, 102: other},
	}
	resolver := audienceSurveyReferenceAdapter{surveys: reader}
	if id, found, err := resolver.ResolveAudienceQuestionnaire(context.Background(), "客户调研"); err != nil || !found || id != "101" {
		t.Fatalf("questionnaire = (%q, %t, %v)", id, found, err)
	}
	if id, found, err := resolver.ResolveAudienceQuestion(context.Background(), "101", "获客方式"); err != nil || !found || id != "202" {
		t.Fatalf("question = (%q, %t, %v)", id, found, err)
	}
	if id, found, err := resolver.ResolveAudienceOption(context.Background(), "101", "202", "内容"); err != nil || !found || id != "303" {
		t.Fatalf("scoped option = (%q, %t, %v)", id, found, err)
	}
	if id, found, err := resolver.ResolveAudienceOption(context.Background(), "101", "202", "304"); err != nil || found || id != "" {
		t.Fatalf("cross-question option = (%q, %t, %v), want empty false nil", id, found, err)
	}
	if id, found, err := resolver.ResolveAudienceQuestion(context.Background(), "101", "402"); err != nil || found || id != "" {
		t.Fatalf("cross-questionnaire question = (%q, %t, %v), want empty false nil", id, found, err)
	}
}

func TestAudienceSurveyReferenceRejectsAmbiguousOrTruncatedQuestionnaireTitle(t *testing.T) {
	first := surveyport.Questionnaire{ID: 101, Title: "同名问卷"}
	second := surveyport.Questionnaire{ID: 102, Title: "同名问卷"}
	for name, page := range map[string]surveyport.Page{
		"ambiguous": {Items: []surveyport.Questionnaire{first, second}, Total: 2},
		"truncated": {Items: []surveyport.Questionnaire{first}, Total: 101},
	} {
		t.Run(name, func(t *testing.T) {
			resolver := audienceSurveyReferenceAdapter{surveys: audienceSurveyDefinitionsStub{pages: map[string]surveyport.Page{"同名问卷": page}}}
			id, found, err := resolver.ResolveAudienceQuestionnaire(context.Background(), "同名问卷")
			if err != nil || found || id != "" {
				t.Fatalf("%s = (%q, %t, %v), want empty false nil", name, id, found, err)
			}
		})
	}
}
