package domain

import (
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"testing"
)

func TestRedemptionQualificationCannotBeOptionalOrMapped(t *testing.T) {
	q := surveyport.Question{ID: 1, Type: surveyport.QuestionRedemptionCode, Title: "兑换码", Required: true}
	if err := ValidateQuestion(q, surveyport.ModeSurvey); err != nil {
		t.Fatal(err)
	}
	optional := q
	optional.Required = false
	mapped := q
	mapped.SidebarProfileField = "goal"
	option := q
	option.Options = []surveyport.Option{{Text: "绕过"}}
	for _, bad := range []surveyport.Question{optional, mapped, option} {
		if ValidateQuestion(bad, surveyport.ModeSurvey) == nil {
			t.Fatal("invalid qualification definition accepted")
		}
	}
	if ValidateAnswers([]surveyport.Question{q}, nil) == nil {
		t.Fatal("missing code accepted")
	}
	if ValidateAnswers([]surveyport.Question{q}, []surveyport.SubmissionAnswer{{QuestionID: 1, TextValue: "  "}}) == nil {
		t.Fatal("blank code accepted")
	}
	if ValidateAnswers([]surveyport.Question{q}, []surveyport.SubmissionAnswer{{QuestionID: 1, TextValue: "00123"}}) != nil {
		t.Fatal("normal code rejected")
	}
}
