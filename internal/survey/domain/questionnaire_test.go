package domain

import (
	"errors"
	"testing"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func intPointer(value int) *int           { return &value }
func floatPointer(value float64) *float64 { return &value }

func ordinaryQuestionnaire() surveyport.Questionnaire {
	return surveyport.Questionnaire{
		Name: "customer-checkin", Title: "客户需求调研", Description: "请如实填写", Mode: surveyport.ModeSurvey,
		AnswerDisplayMode: surveyport.DisplayAllInOne, Slug: "customer-checkin", Status: surveyport.StatusDraft,
		Questions: []surveyport.Question{
			{ID: 1, Type: surveyport.QuestionSingleChoice, Title: "目标", Required: true, SortOrder: 0, Validation: surveyport.Validation{MinimumSelections: intPointer(1), MaximumSelections: intPointer(1)}, Options: []surveyport.Option{{ID: 11, Text: "增长", SortOrder: 0}, {ID: 12, Text: "稳定", SortOrder: 1}}},
			{ID: 2, Type: surveyport.QuestionMultiChoice, Title: "关注点", Required: true, SortOrder: 1, Validation: surveyport.Validation{MinimumSelections: intPointer(1), MaximumSelections: intPointer(2)}, Options: []surveyport.Option{{ID: 21, Text: "获客", SortOrder: 0}, {ID: 22, Text: "成交", SortOrder: 1}, {ID: 23, Text: "其它", IsOther: true, OtherMaximumLength: 50, OtherPlaceholder: "请输入", SortOrder: 2}}},
			{ID: 3, Type: surveyport.QuestionTextarea, Title: "补充", Required: false, SortOrder: 2, Validation: surveyport.Validation{MaximumLength: intPointer(200)}, Options: []surveyport.Option{}},
			{ID: 4, Type: surveyport.QuestionMobile, Title: "手机号", Required: true, SortOrder: 3, Options: []surveyport.Option{}},
		},
		ScoreRules: []surveyport.ScoreRule{},
	}
}

func TestQuestionnaireSupportsFourQuestionTypes(t *testing.T) {
	questionnaire := ordinaryQuestionnaire()
	if err := ValidateQuestionnaire(questionnaire); err != nil {
		t.Fatalf("valid questionnaire: %v", err)
	}
	answers := []surveyport.SubmissionAnswer{
		{QuestionID: 1, OptionIDs: []surveyport.ID{11}},
		{QuestionID: 2, OptionIDs: []surveyport.ID{21, 23}, TextValue: "私域运营"},
		{QuestionID: 3, TextValue: "希望了解完整方案"},
		{QuestionID: 4, TextValue: "13812345678"},
	}
	if err := ValidateAnswers(questionnaire.Questions, answers); err != nil {
		t.Fatalf("valid answers: %v", err)
	}
}

func TestAnswerValidationFailsClosed(t *testing.T) {
	questionnaire := ordinaryQuestionnaire()
	tests := []struct {
		name    string
		answers []surveyport.SubmissionAnswer
	}{
		{"unknown option", []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{999}}, {QuestionID: 2, OptionIDs: []surveyport.ID{21}}, {QuestionID: 4, TextValue: "13812345678"}}},
		{"duplicate option", []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{11, 11}}, {QuestionID: 2, OptionIDs: []surveyport.ID{21}}, {QuestionID: 4, TextValue: "13812345678"}}},
		{"other without text", []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{11}}, {QuestionID: 2, OptionIDs: []surveyport.ID{23}}, {QuestionID: 4, TextValue: "13812345678"}}},
		{"invalid mobile", []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{11}}, {QuestionID: 2, OptionIDs: []surveyport.ID{21}}, {QuestionID: 4, TextValue: "138 1234 5678"}}},
		{"missing required", []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{11}}, {QuestionID: 4, TextValue: "13812345678"}}},
		{"extra question", []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{11}}, {QuestionID: 2, OptionIDs: []surveyport.ID{21}}, {QuestionID: 4, TextValue: "13812345678"}, {QuestionID: 99, TextValue: "x"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateAnswers(questionnaire.Questions, test.answers); !errors.Is(err, ErrInvalidAnswer) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestQuestionnaireRejectsDefinitionDrift(t *testing.T) {
	for _, mutate := range []func(*surveyport.Questionnaire){
		func(value *surveyport.Questionnaire) { value.Slug = "Bad Slug" },
		func(value *surveyport.Questionnaire) { value.Slug = "trailing-" },
		func(value *surveyport.Questionnaire) { value.Questions[1].SortOrder = 9 },
		func(value *surveyport.Questionnaire) { value.Questions[0].Options[1].SortOrder = 0 },
		func(value *surveyport.Questionnaire) {
			value.Questions[1].Options[1].IsOther = true
			value.Questions[1].Options[1].OtherMaximumLength = 10
		},
		func(value *surveyport.Questionnaire) { value.Questions[2].Options = []surveyport.Option{{Text: "x"}} },
	} {
		value := ordinaryQuestionnaire()
		mutate(&value)
		if err := ValidateQuestionnaire(value); !errors.Is(err, ErrInvalidQuestionnaire) {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestScoreRulesAreOrderedAndNonOverlapping(t *testing.T) {
	questionnaire := ordinaryQuestionnaire()
	questionnaire.ScoreRules = []surveyport.ScoreRule{
		{MinimumScore: floatPointer(0), MaximumScore: floatPointer(49), TagCodes: []string{"needs-help"}, SortOrder: 0},
		{MinimumScore: floatPointer(50), MaximumScore: floatPointer(100), TagCodes: []string{"ready"}, SortOrder: 1},
	}
	if err := ValidateQuestionnaire(questionnaire); err != nil {
		t.Fatal(err)
	}
	questionnaire.ScoreRules[1].MinimumScore = floatPointer(49)
	if err := ValidateQuestionnaire(questionnaire); !errors.Is(err, ErrInvalidQuestionnaire) {
		t.Fatalf("overlap error=%v", err)
	}
}
