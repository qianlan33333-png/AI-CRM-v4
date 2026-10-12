package domain

import (
	"encoding/json"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"testing"
)

func TestContactMappingAndShippingValidation(t *testing.T) {
	q := ordinaryQuestionnaire()
	q.Questions = []surveyport.Question{{ID: 1, Type: surveyport.QuestionMobile, Title: "本人", SidebarProfileField: "contact_phone", SortOrder: 0}, {ID: 2, Type: surveyport.QuestionMobile, Title: "推荐人", SortOrder: 1}}
	if e := ValidateQuestionnaire(q); e != nil {
		t.Fatal("ordinary multiple phones rejected", e)
	}
	q.Questions[1].SidebarProfileField = "contact_phone"
	if ValidateQuestionnaire(q) == nil {
		t.Fatal("duplicate contact source accepted")
	}
	q.Questions[1].Type = surveyport.QuestionShippingAddress
	q.Questions[1].SidebarProfileField = "shipping_contact"
	if ValidateQuestionnaire(q) == nil {
		t.Fatal("shipping must occupy contact source")
	}
	q.Questions[0].SidebarProfileField = ""
	if e := ValidateQuestionnaire(q); e != nil {
		t.Fatal(e)
	}
	v := customerport.ShippingContact{MobileE164: "13800000000", RecipientName: "收件人", ProvinceCode: "11", ProvinceName: "北京市", CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区", DetailAddress: "测试地址"}
	a := []surveyport.SubmissionAnswer{{QuestionID: 2, ShippingAddress: &v}}
	if e := ValidateAnswers(q.Questions, a); e != nil {
		t.Fatal(e)
	}
	v.DetailAddress = ""
	if ValidateAnswers(q.Questions, a) == nil {
		t.Fatal("partial address accepted")
	}
	a[0].ShippingAddress = nil
	if e := ValidateAnswers(q.Questions, a); e != nil {
		t.Fatal("optional blank address rejected", e)
	}
	a[0].QuestionID = 1
	a[0].ShippingAddress = &v
	if ValidateAnswers(q.Questions, a) == nil {
		t.Fatal("shipping smuggled into mobile answer")
	}
}
func TestAbsentShippingPreservesLegacyAnswerJSON(t *testing.T) {
	raw, e := json.Marshal(surveyport.SubmissionAnswer{QuestionID: 4, TextValue: "13800000000"})
	if e != nil {
		t.Fatal(e)
	}
	// Freeze the original request fields and their order. New optional content
	// must not alter the digest for pre-upgrade idempotency keys.
	if string(raw) != `{"question_id":4,"option_ids":null,"text_value":"13800000000"}` {
		t.Fatalf("legacy answer changed: %s", raw)
	}
}
