package dsl

import "encoding/json"

type Template string

const (
	CoreAIProduct              Template = "core_ai_product"
	ActiveContacts             Template = "active_contacts"
	StageAny                   Template = "stage_any"
	TagAny                     Template = "tag_any"
	OwnerAny                   Template = "owner_any"
	ChannelAny                 Template = "channel_any"
	HXCRegistration            Template = "hxc_registration"
	WeComContactRegistration   Template = "wecom_contact_registration"
	QuestionnaireSubmissions   Template = "questionnaire_submissions"
	QuestionnaireChoiceAnswers Template = "questionnaire_choice_answers"
	PaidOrder                  Template = "paid_order"
	ChannelEntry               Template = "channel_entry"
	RadarFirstClickElapsed     Template = "radar_first_click_elapsed"
	MemberExcludingGroupPaid   Template = "member_excluding_group_paid"
	MemberUsageStatus          Template = "member_usage_status"
)

type Predicate struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Values []string `json:"values"`
}

type AST struct {
	SchemaVersion int                        `json:"schema_version"`
	Template      Template                   `json:"template"`
	Predicate     Predicate                  `json:"predicate,omitempty"`
	Parameters    map[string]json.RawMessage `json:"parameters"`
}
