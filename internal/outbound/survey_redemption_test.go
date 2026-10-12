package outbound

import (
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"strings"
	"testing"
)

func TestSurveyCompletionRedemptionNeverExportsRawCode(t *testing.T) {
	secret := "TEST-CODE-PRIVATE-00123"
	body, err := marshalSurveyCompletion(surveyport.CompletionPayload{Answers: []surveyport.CompletionAnswer{{QuestionTitle: "兑换码", QuestionType: surveyport.QuestionRedemptionCode, TextValue: secret}}}, "test-user")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) || !strings.Contains(string(body), "兑换码已验证") {
		t.Fatalf("unsafe qualification projection: %s", body)
	}
}
