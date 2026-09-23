package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type legacyFacts struct {
	contacts     []wecomport.AudienceContact
	survey       []surveyport.AudienceChoiceAnswer
	orders       []orderport.PaidAudienceOrder
	channels     []channelport.AudienceEntry
	radar        []radarport.AudienceFirstClick
	shared       map[customerdomain.CustomerID]hxcport.SharedFacts
	registration map[customerdomain.CustomerID]customerport.AudienceRegistrationFact
	sharedErr    error
}

func (legacyFacts) AudienceOwnerUserID(_ context.Context, id accessport.StaffID) (string, bool, error) {
	switch id {
	case 9:
		return "bob", true, nil
	case 19:
		return "staff-a", true, nil
	case 20:
		return "staff-b", true, nil
	}
	return "", false, nil
}

func (f legacyFacts) AudienceContacts(context.Context, time.Time) ([]wecomport.AudienceContact, error) {
	return f.contacts, nil
}
func (f legacyFacts) FirstCompleteAudienceChoices(context.Context, time.Time) ([]surveyport.AudienceChoiceAnswer, error) {
	return f.survey, nil
}
func (f legacyFacts) PaidAudienceOrders(context.Context, time.Time) ([]orderport.PaidAudienceOrder, error) {
	return f.orders, nil
}
func (f legacyFacts) AudienceEntries(context.Context, time.Time) ([]channelport.AudienceEntry, error) {
	return f.channels, nil
}
func (f legacyFacts) AudienceFirstClicks(context.Context, time.Time) ([]radarport.AudienceFirstClick, error) {
	return f.radar, nil
}
func (f legacyFacts) AudienceRegistrationFacts(_ context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, error) {
	out := make(map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, len(customerIDs))
	for _, customerID := range customerIDs {
		if fact, found := f.registration[customerID]; found {
			out[customerID] = fact
		}
	}
	return out, nil
}

func (legacyFacts) CurrentSharedFactsVersion(context.Context) (int64, error) { return 1, nil }
func (f legacyFacts) SharedFactsAtVersion(_ context.Context, version int64, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]hxcport.SharedFacts, error) {
	if f.sharedErr != nil {
		return nil, f.sharedErr
	}
	if version != 1 {
		return nil, hxcport.ErrSharedFactsVersionUnavailable
	}
	out := make(map[customerdomain.CustomerID]hxcport.SharedFacts, len(customerIDs))
	for _, customerID := range customerIDs {
		if fact, found := f.shared[customerID]; found {
			out[customerID] = fact
		}
	}
	return out, nil
}

type primaryOwnerFacts []wecomport.AudiencePrimaryOwner

func (f primaryOwnerFacts) AudiencePrimaryOwners(context.Context, []customerdomain.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	return f, nil
}

func legacyDefinition(t *testing.T, template segmentdsl.Template, params string) segmentport.Definition {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &raw); err != nil {
		t.Fatal(err)
	}
	expression, err := json.Marshal(segmentdsl.AST{SchemaVersion: 1, Template: template, Parameters: raw})
	if err != nil {
		t.Fatal(err)
	}
	return segmentport.Definition{SchemaVersion: 1, Expression: expression}
}

func assertAudienceIDs(t *testing.T, result segmentport.Evaluation, want ...customerdomain.CustomerID) {
	t.Helper()
	if len(result.CustomerIDs) != len(want) {
		t.Fatalf("customers=%v want=%v", result.CustomerIDs, want)
	}
	seen := map[customerdomain.CustomerID]bool{}
	for _, id := range result.CustomerIDs {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("customers=%v want=%v", result.CustomerIDs, want)
		}
	}
}

func TestLegacyTemplateSourcesEvaluateFrozenConditions(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	paidInside := at.Add(-2 * time.Hour)
	used := at.Add(-time.Hour)
	expires := at.Add(24 * time.Hour)
	facts := legacyFacts{
		contacts: []wecomport.AudienceContact{
			{CustomerID: 1, OwnerUserID: "staff-a", Status: "active"},
			{CustomerID: 2, OwnerUserID: "staff-b", Status: "active"},
			{CustomerID: 3, OwnerUserID: "staff-a", Status: "deleted"},
		},
		survey: []surveyport.AudienceChoiceAnswer{
			{CustomerID: 1, QuestionnaireID: 9, StaffID: "staff-a", QuestionID: 11, OptionIDs: []surveyport.ID{101}},
			{CustomerID: 1, QuestionnaireID: 9, StaffID: "staff-a", QuestionID: 12, OptionIDs: []surveyport.ID{202}},
			{CustomerID: 2, QuestionnaireID: 9, StaffID: "staff-b", QuestionID: 11, OptionIDs: []surveyport.ID{102}},
		},
		orders: []orderport.PaidAudienceOrder{
			{CustomerID: 1, ProductCode: "paid-course", OwnerReference: "staff-a", PaidAt: &paidInside},
			{CustomerID: 2, ProductCode: "paid-course", OwnerReference: "staff-b", PaidAt: &paidInside},
			{CustomerID: 3, ProductCode: "paid-course"},
		},
		channels:     []channelport.AudienceEntry{{CustomerID: 1, ChannelID: 7, ChannelCode: "channel-7", OwnerReference: "staff-a", LastEnteredAt: at.Add(-48 * time.Hour)}},
		radar:        []radarport.AudienceFirstClick{{CustomerID: 1, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: "staff-a"}},
		shared:       map[customerdomain.CustomerID]hxcport.SharedFacts{1: {CustomerID: 1, Availability: hxcport.SharedFactsAvailable, Registered: true, MembershipRecordFound: true, MembershipSource: "subscription", IsMember: true, Tier: "pro", MembershipStatus: "active", ExpiresAt: &expires, HasRealUsage: true, LastUsedAt: &used}},
		registration: map[customerdomain.CustomerID]customerport.AudienceRegistrationFact{1: {CustomerID: 1, Known: true, Registered: true, Source: "people.mobile", UpdatedAt: at}},
	}
	source := CustomerSource{UoW: passthroughUoW{}, Legacy: LegacyTemplateSource{Contacts: facts, Survey: facts, Orders: facts, Channels: facts, Radar: facts, MemberFacts: facts, RegistrationFacts: facts, Owners: facts}}
	cases := []struct {
		name, parameters string
		template         segmentdsl.Template
		want             []customerdomain.CustomerID
	}{
		{"contact-registration", `{"owner_scope":"specified","owner_staff_ids":["19"],"contact_statuses":["active"],"registration_status":"registered"}`, segmentdsl.WeComContactRegistration, []customerdomain.CustomerID{1}},
		// Question conditions use AND across questions and OR inside each option list.
		{"first-questionnaire-choice", `{"questionnaire_id":"9","conditions":[{"question_id":"11","option_ids":["101","102"]},{"question_id":"12","option_ids":["202"]}],"owner_scope":"specified","owner_staff_ids":["19"]}`, segmentdsl.QuestionnaireChoiceAnswers, []customerdomain.CustomerID{1}},
		// A time window is [from,to); unknown PaidAt cannot enter it. Owner scope
		// remains effective even when the active-contact switch is false.
		{"paid-payer-window-owner", `{"product_codes":["paid-course"],"paid_at_from":"2026-09-05T09:00:00Z","paid_at_to":"2026-09-05T12:00:00Z","owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":false}`, segmentdsl.PaidOrder, []customerdomain.CustomerID{1}},
		{"channel-entry", `{"channel_codes":["channel-7"],"entered_days_min":2,"entered_days_max":3,"owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":true}`, segmentdsl.ChannelEntry, []customerdomain.CustomerID{1}},
		// The source supplies the immutable first click, so a later click cannot
		// reset this three-day elapsed result.
		{"radar-first-click", `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["19"]}`, segmentdsl.RadarFirstClickElapsed, []customerdomain.CustomerID{1}},
		{"member-real-usage", `{"owner_scope":"specified","owner_staff_ids":["19"],"service_period":"active","registration_status":"registered","usage_status":"used","membership_tiers":["pro"],"membership_statuses":["active"]}`, segmentdsl.MemberUsageStatus, []customerdomain.CustomerID{1}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			result, err := source.Evaluate(context.Background(), legacyDefinition(t, tt.template, tt.parameters), at)
			if err != nil {
				t.Fatal(err)
			}
			assertAudienceIDs(t, result, tt.want...)
		})
	}
}

func TestLegacyTemplateSourceDoesNotBorrowCurrentContactOwnerOrExpireMembershipByGuess(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{contacts: []wecomport.AudienceContact{{CustomerID: 9, OwnerUserID: "staff-a", Status: "active"}}, orders: []orderport.PaidAudienceOrder{{CustomerID: 9, ProductCode: "course"}}, shared: map[customerdomain.CustomerID]hxcport.SharedFacts{9: {CustomerID: 9, Availability: hxcport.SharedFactsAvailable, Registered: true, IsMember: false, Tier: "pro", MembershipStatus: "unknown"}}}
	source := LegacyTemplateSource{Contacts: facts, Orders: facts, MemberFacts: facts, Owners: facts}
	paid, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.PaidOrder, `{"product_codes":["course"],"paid_at_from":"","paid_at_to":"","owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":false}`), at)
	if err != nil || len(paid.CustomerIDs) != 0 {
		t.Fatalf("paid=%+v err=%v", paid, err)
	}
	active, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.MemberUsageStatus, `{"owner_scope":"specified","owner_staff_ids":["19"],"service_period":"active","registration_status":"registered","usage_status":"any","membership_tiers":[],"membership_statuses":[]}`), at)
	if err != nil || len(active.CustomerIDs) != 0 {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	expired, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.MemberUsageStatus, `{"owner_scope":"specified","owner_staff_ids":["19"],"service_period":"expired","registration_status":"registered","usage_status":"any","membership_tiers":[],"membership_statuses":[]}`), at)
	if err != nil || len(expired.CustomerIDs) != 0 {
		t.Fatalf("non-membership cannot be guessed expired: result=%+v err=%v", expired, err)
	}
}

func TestLegacyTemplateSourceDoesNotTurnSharedFactsFailureIntoAnEmptyAudience(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{
		contacts:  []wecomport.AudienceContact{{CustomerID: 9, OwnerUserID: "staff-a", Status: "active"}},
		sharedErr: errors.New("shared facts temporary read failure"),
	}
	source := LegacyTemplateSource{Contacts: facts, MemberFacts: facts, Owners: facts}
	_, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.MemberUsageStatus, `{"owner_scope":"specified","owner_staff_ids":["19"],"service_period":"any","registration_status":"any","usage_status":"any","membership_tiers":[],"membership_statuses":[]}`), at)
	if !errors.Is(err, ErrCustomerReadUnavailable) {
		t.Fatalf("shared-facts failure must surface, got %v", err)
	}
}

func TestLegacyMemberTemplateRequiresDurableMembershipSource(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{
		contacts: []wecomport.AudienceContact{{CustomerID: 9, OwnerUserID: "staff-a", Status: "active"}},
		shared: map[customerdomain.CustomerID]hxcport.SharedFacts{9: {
			CustomerID: 9, Availability: hxcport.SharedFactsAvailable, Registered: true,
			// A HXC dashboard row can establish registration and usage, but it is
			// not a membership fact until the Owner supplies its source.
			MembershipRecordFound: false, MembershipSource: "", HasRealUsage: true,
		}},
	}
	source := LegacyTemplateSource{Contacts: facts, MemberFacts: facts, Owners: facts}
	result, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.MemberUsageStatus, `{"owner_scope":"specified","owner_staff_ids":["19"],"service_period":"any","registration_status":"any","usage_status":"any","membership_tiers":[],"membership_statuses":[]}`), at)
	if err != nil || len(result.CustomerIDs) != 0 {
		t.Fatalf("membership without provenance result=%+v err=%v", result, err)
	}
}

func TestRadarFirstClickUsesHistoricalOwnerAndFailsClosedWhenAbsent(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{
		// The current contact belongs to staff-a. The first click was recorded
		// for staff-b and therefore must not be reassigned from this relation.
		contacts: []wecomport.AudienceContact{{CustomerID: 1, OwnerUserID: "staff-a", Status: "active"}},
		radar: []radarport.AudienceFirstClick{
			{CustomerID: 1, RadarID: 8, FirstClickEventID: 10, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: "staff-b"},
			{CustomerID: 2, RadarID: 8, FirstClickEventID: 11, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: ""},
		},
	}
	source := LegacyTemplateSource{Contacts: facts, Radar: facts, Owners: facts}
	all, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"all","owner_staff_ids":[]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, all, 1, 2)
	staffA, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["19"]}`), at)
	if err != nil || len(staffA.CustomerIDs) != 0 {
		t.Fatalf("staff-a result=%+v err=%v", staffA, err)
	}
	staffB, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["20"]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, staffB, 1)
}

func TestRadarFirstClickPrefersTrustedPrimaryOwnerOverHistoricalFallback(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{radar: []radarport.AudienceFirstClick{
		{CustomerID: 1, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: "staff-b"},
		{CustomerID: 2, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour)},
		// A matching userid from another corp scope cannot be used as a
		// fallback; otherwise a cross-corp userid collision misattributes it.
		{CustomerID: 3, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: "staff-b"},
	}}
	source := LegacyTemplateSource{Radar: facts, Owners: facts, PrimaryOwnerCorpScope: "wecom-corp:test", PrimaryOwners: primaryOwnerFacts{
		{CustomerID: 1, CorpScope: "wecom-corp:test", OwnerUserID: "staff-a", Status: "known"},
		{CustomerID: 2, CorpScope: "wecom-corp:test", Status: "ambiguous"},
		{CustomerID: 3, CorpScope: "wecom-corp:other", OwnerUserID: "staff-a", Status: "known"},
	}}
	staffA, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["19"]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, staffA, 1)
	staffB, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["20"]}`), at)
	if err != nil || len(staffB.CustomerIDs) != 0 {
		t.Fatalf("staff-b result=%+v err=%v", staffB, err)
	}
}

func TestChannelOwnerStaffIDDoesNotCollideWithProviderUserID(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	nine := int64(9)
	facts := legacyFacts{channels: []channelport.AudienceEntry{{CustomerID: 1, ChannelCode: "c", OwnerStaffID: &nine, LastEnteredAt: at.Add(-24 * time.Hour)}, {CustomerID: 2, ChannelCode: "c", OwnerReference: "9", LastEnteredAt: at.Add(-24 * time.Hour)}}}
	s := LegacyTemplateSource{Channels: facts, Owners: facts}
	r, err := s.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"specified","owner_staff_ids":["9"],"require_active_wecom_contact":false}`), at)
	if err != nil || len(r.CustomerIDs) != 1 || r.CustomerIDs[0] != 1 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
}

func TestLegacyTemplateOwnerReferencesRejectNonCanonicalIDsAndNeedAccess(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{channels: []channelport.AudienceEntry{{CustomerID: 1, ChannelCode: "c", LastEnteredAt: at.Add(-24 * time.Hour)}}}
	withOwners := LegacyTemplateSource{Channels: facts, Owners: facts}
	for _, ids := range []string{
		`["bob"]`, `["09"]`, `["0"]`, `["-1"]`, `[""]`, `["9223372036854775808"]`, `["19","bob"]`,
	} {
		t.Run(ids, func(t *testing.T) {
			definition := legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"specified","owner_staff_ids":`+ids+`,"require_active_wecom_contact":false}`)
			if _, err := withOwners.Evaluate(context.Background(), definition, at); err == nil {
				t.Fatalf("non-canonical owner ids accepted: %s", ids)
			}
		})
	}
	definition := legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":false}`)
	if _, err := (LegacyTemplateSource{Channels: facts}).Evaluate(context.Background(), definition, at); err == nil {
		t.Fatal("specified owner succeeded without Access reader")
	}
	allDefinition := legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":false}`)
	result, err := (LegacyTemplateSource{Channels: facts}).Evaluate(context.Background(), allDefinition, at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, result, 1)
}

func TestWeComRegistrationUsesDirectoryPhonePresenceInsteadOfHXC(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{
		contacts: []wecomport.AudienceContact{{CustomerID: 1, Status: "active"}, {CustomerID: 2, Status: "active"}, {CustomerID: 3, Status: "active"}, {CustomerID: 4, Status: "active"}},
		// Deliberately disagree with the directory projection. HXC cannot make
		// a WeCom registration match, and absence of a directory row is unknown.
		shared: map[customerdomain.CustomerID]hxcport.SharedFacts{1: {CustomerID: 1, Availability: hxcport.SharedFactsAvailable, Registered: true}, 2: {CustomerID: 2, Availability: hxcport.SharedFactsAvailable, Registered: false}},
		registration: map[customerdomain.CustomerID]customerport.AudienceRegistrationFact{
			1: {CustomerID: 1, Known: true, Registered: false, Source: "people.mobile", UpdatedAt: at},
			2: {CustomerID: 2, Known: true, Registered: true, Source: "people.mobile", UpdatedAt: at},
			3: {CustomerID: 3, Known: true, Registered: false, Source: "people.mobile", UpdatedAt: at},
		},
	}
	source := LegacyTemplateSource{Contacts: facts, RegistrationFacts: facts, MemberFacts: facts, Owners: facts}
	registered, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.WeComContactRegistration, `{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"registered"}`), at)
	if err != nil || len(registered.CustomerIDs) != 1 || registered.CustomerIDs[0] != 2 {
		t.Fatalf("registered=%+v err=%v", registered, err)
	}
	unregistered, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.WeComContactRegistration, `{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"unregistered"}`), at)
	if err != nil || len(unregistered.CustomerIDs) != 2 || !(unregistered.CustomerIDs[0] == 1 || unregistered.CustomerIDs[1] == 1) || !(unregistered.CustomerIDs[0] == 3 || unregistered.CustomerIDs[1] == 3) {
		t.Fatalf("unregistered=%+v err=%v", unregistered, err)
	}
}

func TestMemberRegistrationCombinesHXCAndDirectoryPhoneFacts(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	expires := at.Add(time.Hour)
	facts := legacyFacts{
		contacts: []wecomport.AudienceContact{{CustomerID: 1, Status: "active"}, {CustomerID: 2, Status: "active"}, {CustomerID: 3, Status: "active"}},
		shared: map[customerdomain.CustomerID]hxcport.SharedFacts{
			// This member is not registered in HXC, but the frozen old view also
			// includes people.mobile, which makes it registered.
			1: {CustomerID: 1, Availability: hxcport.SharedFactsAvailable, MembershipRecordFound: true, MembershipSource: "snapshot", IsMember: true, MembershipStatus: "active", ExpiresAt: &expires},
			2: {CustomerID: 2, Availability: hxcport.SharedFactsAvailable, MembershipRecordFound: true, MembershipSource: "snapshot", IsMember: true, MembershipStatus: "active", ExpiresAt: &expires},
			3: {CustomerID: 3, Availability: hxcport.SharedFactsAvailable, Registered: true, MembershipRecordFound: true, MembershipSource: "snapshot", IsMember: true, MembershipStatus: "active", ExpiresAt: &expires},
		},
		registration: map[customerdomain.CustomerID]customerport.AudienceRegistrationFact{
			1: {CustomerID: 1, Known: true, Registered: true, Source: "people.mobile", UpdatedAt: at},
			2: {CustomerID: 2, Known: true, Registered: false, Source: "people.mobile", UpdatedAt: at},
			// No directory row for 3: HXC's positive fact remains sufficient for
			// registered, but no absent directory row is invented for customer 2.
		},
	}
	source := LegacyTemplateSource{Contacts: facts, MemberFacts: facts, RegistrationFacts: facts}
	registered, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.MemberUsageStatus, `{"owner_scope":"all","owner_staff_ids":[],"service_period":"active","registration_status":"registered","usage_status":"any","membership_tiers":[],"membership_statuses":[]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, registered, 1, 3)
	unregistered, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.MemberUsageStatus, `{"owner_scope":"all","owner_staff_ids":[],"service_period":"active","registration_status":"unregistered","usage_status":"any","membership_tiers":[],"membership_statuses":[]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, unregistered, 2)
}

type batchedRegistrationFacts struct {
	facts  map[customerdomain.CustomerID]customerport.AudienceRegistrationFact
	failAt int
	calls  int
	sizes  []int
}

func (f *batchedRegistrationFacts) AudienceRegistrationFacts(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, error) {
	f.calls++
	f.sizes = append(f.sizes, len(ids))
	if f.failAt == f.calls {
		return nil, errors.New("directory unavailable")
	}
	out := make(map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, len(ids))
	for _, id := range ids {
		out[id] = f.facts[id]
	}
	return out, nil
}

func TestWeComRegistrationBatchesCustomerFactsWithoutPartialResult(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	contacts := make([]wecomport.AudienceContact, 0, customerport.MaxAudienceRegistrationCustomerIDs+1)
	facts := make(map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, customerport.MaxAudienceRegistrationCustomerIDs+1)
	for id := 1; id <= customerport.MaxAudienceRegistrationCustomerIDs+1; id++ {
		customerID := customerdomain.CustomerID(id)
		contacts = append(contacts, wecomport.AudienceContact{CustomerID: customerID, Status: "active"})
		facts[customerID] = customerport.AudienceRegistrationFact{CustomerID: customerID, Known: true, Registered: true, Source: "people.mobile", UpdatedAt: at}
	}
	reader := &batchedRegistrationFacts{facts: facts}
	source := LegacyTemplateSource{Contacts: legacyFacts{contacts: contacts}, RegistrationFacts: reader}
	definition := legacyDefinition(t, segmentdsl.WeComContactRegistration, `{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"registered"}`)
	result, err := source.Evaluate(context.Background(), definition, at)
	if err != nil || len(result.CustomerIDs) != customerport.MaxAudienceRegistrationCustomerIDs+1 || reader.calls != 2 || reader.sizes[0] != customerport.MaxAudienceRegistrationCustomerIDs || reader.sizes[1] != 1 {
		t.Fatalf("result=%d calls=%d sizes=%v err=%v", len(result.CustomerIDs), reader.calls, reader.sizes, err)
	}
	reader = &batchedRegistrationFacts{facts: facts, failAt: 2}
	source.RegistrationFacts = reader
	if partial, err := source.Evaluate(context.Background(), definition, at); err == nil || len(partial.CustomerIDs) != 0 || reader.calls != 2 {
		t.Fatalf("partial=%+v calls=%d err=%v", partial, reader.calls, err)
	}
}
