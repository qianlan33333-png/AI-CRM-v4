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

func TestSurveyCompletionAttachmentUsesSafeMarker(t *testing.T) {
	body, err := marshalSurveyCompletion(surveyport.CompletionPayload{Answers: []surveyport.CompletionAnswer{{QuestionTitle: "图片", QuestionType: surveyport.QuestionAttachment, TextValue: "/api/admin/private-image"}}}, "test-user")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private-image") || !strings.Contains(string(body), "已上传图片") {
		t.Fatal("unsafe attachment projection")
	}
}
