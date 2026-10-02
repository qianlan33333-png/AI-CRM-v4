package adapter

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"reflect"
	"testing"
	"time"
)

type activationRegistration map[customerdomain.CustomerID]hxcport.RegistrationFact

func (f activationRegistration) RegistrationFacts(_ context.Context, ids []customerdomain.CustomerID, _ time.Time) (map[customerdomain.CustomerID]hxcport.RegistrationFact, error) {
	out := map[customerdomain.CustomerID]hxcport.RegistrationFact{}
	for _, id := range ids {
		if v, ok := f[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}
func TestActivationPaidOverrideAccountAndQuestionnaireAND(t *testing.T) {
	anchor := time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)
	at := anchor.Add(12 * time.Hour)
	old := anchor.Add(-48 * time.Hour)
	newPaid := at.Add(-time.Hour)
	facts := legacyFacts{orders: []orderport.PaidAudienceOrder{
		{OrderID: 11, CustomerID: 11, ProductCode: "334465678", PaidAt: &old},
		{OrderID: 12, CustomerID: 12, ProductCode: "334465678", PaidAt: &old},
		{OrderID: 13, CustomerID: 13, ProductCode: "334465678", PaidAt: &old},
		{OrderID: 14, CustomerID: 14, ProductCode: "334465678", PaidAt: &newPaid},
		{OrderID: 15, CustomerID: 15, ProductCode: "other", PaidAt: &old},
	}, contacts: []wecomport.AudienceContact{{CustomerID: 1, Status: "active"}, {CustomerID: 2, Status: "active"}, {CustomerID: 3, Status: "active"}, {CustomerID: 14, Status: "active"}}}
	submissions := submissionFacts{rows: []surveyport.AudienceSubmission{{CustomerID: 2, QuestionnaireID: 16, SubmittedAt: anchor}}}
	source := LegacyTemplateSource{Orders: facts, Contacts: facts, Submissions: submissions, CanonicalCustomers: canonicalCustomerMap{11: 1, 12: 2, 13: 3}, HXCRegistration: activationRegistration{1: {State: "registered", AsOf: at, Version: 1}, 2: {State: "registered", AsOf: at, Version: 1}, 3: {State: "unknown", AsOf: at, Version: 1}, 14: {State: "registered", AsOf: at, Version: 1}}}
	definition := legacyDefinition(t, segmentdsl.PaidOrder, `{"product_codes":["334465678"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true,"activation":{"elapsed_hours_min":12,"elapsed_hours_max":36,"registration_status":"registered","questionnaire_status":"unsubmitted","questionnaire_ids":["16"],"enrollment_override":{"at":"2026-10-02T12:00:00+08:00","order_ids":[11,12,13,15]}}}`)
	for _, c := range []struct {
		reference time.Time
		want      []customerdomain.CustomerID
	}{{at.Add(-time.Second), []customerdomain.CustomerID{}}, {at, []customerdomain.CustomerID{1}}, {anchor.Add(36 * time.Hour), []customerdomain.CustomerID{14}}} {
		// Refresh registration evidence to this evaluation without changing statuses.
		for id, v := range source.HXCRegistration.(activationRegistration) {
			v.AsOf = c.reference
			source.HXCRegistration.(activationRegistration)[id] = v
		}
		got, e := source.Evaluate(context.Background(), definition, c.reference)
		if e != nil || !reflect.DeepEqual(got.CustomerIDs, c.want) {
			t.Fatalf("reference=%s got=%v want=%v err=%v", c.reference, got.CustomerIDs, c.want, e)
		}
	}
	if !facts.orders[0].PaidAt.Equal(old) {
		t.Fatal("operational override changed payment evidence")
	}
}
func TestActivationQuestionnaireUsesFirstSubmissionAndRealSevenDayTokenTime(t *testing.T) {
	at := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	first := at.Add(-12 * time.Hour)
	recentSubmission := at.Add(-time.Hour)
	oldUsage := at.Add(-8 * 24 * time.Hour)
	boundary := at.Add(-7 * 24 * time.Hour)
	recentUsage := at.Add(-time.Hour)
	rows := []surveyport.AudienceSubmission{}
	for id := 1; id <= 6; id++ {
		rows = append(rows, surveyport.AudienceSubmission{CustomerID: customerdomain.CustomerID(id), QuestionnaireID: 16, SubmittedAt: first})
	}
	rows = append(rows, surveyport.AudienceSubmission{CustomerID: 1, QuestionnaireID: 16, SubmittedAt: recentSubmission})
	shared := map[customerdomain.CustomerID]hxcport.SharedFacts{}
	for id := 1; id <= 6; id++ {
		shared[customerdomain.CustomerID(id)] = hxcport.SharedFacts{CustomerID: customerdomain.CustomerID(id), Availability: hxcport.SharedFactsAvailable, SourceAsOf: at, TokenUsageObservedAt: &at, LastUsedAt: &recentUsage}
	}
	f := shared[1]
	f.HasTokenUsage = true
	f.LastTokenUsedAt = &oldUsage
	shared[1] = f
	f = shared[2]
	f.LastTokenUsedAt = &boundary
	shared[2] = f
	f = shared[3]
	f.LastTokenUsedAt = &recentUsage
	shared[3] = f
	f = shared[4]
	f.TokenUsageObservedAt = nil
	shared[4] = f
	f = shared[5]
	f.Availability = hxcport.SharedFactsAmbiguous
	shared[5] = f
	source := LegacyTemplateSource{Submissions: submissionFacts{rows: rows}, CanonicalCustomers: canonicalCustomerMap{}, MemberFacts: legacyFacts{shared: shared}}
	definition := legacyDefinition(t, segmentdsl.QuestionnaireSubmissions, `{"questionnaire_ids":["16"],"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":false,"activation":{"elapsed_hours_min":12,"elapsed_hours_max":36,"token_usage_status":"unused_7d"}}`)
	got, e := source.Evaluate(context.Background(), definition, at)
	if e != nil || !reflect.DeepEqual(got.CustomerIDs, []customerdomain.CustomerID{1, 6}) {
		t.Fatalf("got=%v err=%v", got.CustomerIDs, e)
	}
	for _, offset := range []time.Duration{-3 * time.Hour, time.Hour} {
		observed := at.Add(offset)
		f := shared[6]
		f.TokenUsageObservedAt = &observed
		shared[6] = f
		if _, e := source.Evaluate(context.Background(), definition, at); e == nil {
			t.Fatalf("invalid observation %s accepted", offset)
		}
	}
}

func TestActivationOverrideDoesNotMakeLaterFriendshipQualifyAtOriginalPayment(t *testing.T) {
	anchor := time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)
	at := anchor.Add(12 * time.Hour)
	paid := anchor.Add(-48 * time.Hour)
	laterFriend := anchor.Add(-time.Hour)
	earlierFriend := paid.Add(-time.Hour)
	f := legacyFacts{orders: []orderport.PaidAudienceOrder{{OrderID: 1, CustomerID: 1, ProductCode: "334465678", PaidAt: &paid}}, contacts: []wecomport.AudienceContact{{CustomerID: 1, Status: "active", OwnerUserID: "staff-a", FollowedAt: &laterFriend}}}
	s := LegacyTemplateSource{Orders: f, Contacts: f, Owners: f, CanonicalCustomers: canonicalCustomerMap{}}
	definition := legacyDefinition(t, segmentdsl.PaidOrder, `{"product_codes":["334465678"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true,"require_wecom_friend_at_paid_time":true,"friend_owner_staff_ids":["19"],"activation":{"elapsed_hours_min":12,"elapsed_hours_max":36,"enrollment_override":{"at":"2026-10-02T12:00:00+08:00","order_ids":[1]}}}`)
	got, e := s.Evaluate(context.Background(), definition, at)
	if e != nil || len(got.CustomerIDs) != 0 {
		t.Fatalf("later friend became eligible: %v %v", got.CustomerIDs, e)
	}
	f.contacts[0].FollowedAt = &earlierFriend
	s.Contacts = f
	got, e = s.Evaluate(context.Background(), definition, at)
	if e != nil || len(got.CustomerIDs) != 1 || !got.QualifiedPaidOrder[1].PaidAt.Equal(paid) {
		t.Fatalf("original payment evidence changed: %+v %v", got, e)
	}
}

func TestActivationSubmittedAliasMatchesRecognizedCanonicalCustomer(t *testing.T) {
	at := time.Now().UTC()
	first := at.Add(-12 * time.Hour)
	s := LegacyTemplateSource{Submissions: submissionFacts{rows: []surveyport.AudienceSubmission{{CustomerID: 11, QuestionnaireID: 16, SubmittedAt: first}}}, CanonicalCustomers: canonicalCustomerMap{11: 1}, RecognizedContacts: submissionFacts{}, PrimaryOwnerCorpScope: "wecom-corp:test"}
	definition := legacyDefinition(t, segmentdsl.QuestionnaireSubmissions, `{"questionnaire_ids":["16"],"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":true,"activation":{"elapsed_hours_min":12,"elapsed_hours_max":36}}`)
	got, e := s.Evaluate(context.Background(), definition, at)
	if e != nil || !reflect.DeepEqual(got.CustomerIDs, []customerdomain.CustomerID{1}) {
		t.Fatalf("alias submission lost canonical recognition: %v %v", got.CustomerIDs, e)
	}
}
