package main

import (
	"encoding/base64"
	"testing"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

func TestSurveyCompletionTargetsPreserveSnakeCasePolicyFields(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	raw := `{"survey.legacy.v1":{"endpoint":"https://receiver.example.test/api/webhook/questionnaire","signing_key":"` + key + `","client_id":"survey-v3","version":"legacy-v1","identity_kind":"unionid","identity_scope":"wechat-open-platform:primary","day":15,"frequency":3,"expires_at_ts":2147483647,"type":"questionnaire","remark":"fixture","custom_params":{"campaign":"closeout"}}}`
	targets, err := surveyCompletionTargets(raw)
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	target := targets[0]
	if target.IdentityKind != identitydomain.KindUnionID || target.Day == nil || *target.Day != 15 || target.Frequency == nil || *target.Frequency != 3 || target.ExpiresAtTS == nil || *target.ExpiresAtTS != 2147483647 {
		t.Fatalf("snake_case policy fields were not preserved: %+v", target)
	}
	if target.PushType != "questionnaire" || target.Remark != "fixture" || target.CustomParams["campaign"] != "closeout" {
		t.Fatalf("target metadata=%+v", target)
	}
}
