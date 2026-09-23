package dsl

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrInvalid = errors.New("invalid closed audience definition")

type wireDefinition struct {
	SchemaVersion int                        `json:"schema_version"`
	TemplateKey   Template                   `json:"template_key"`
	Parameters    map[string]json.RawMessage `json:"parameters"`
}

func Parse(raw json.RawMessage) (AST, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire wireDefinition
	if decoder.Decode(&wire) != nil {
		return AST{}, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || wire.SchemaVersion != 1 {
		return AST{}, ErrInvalid
	}
	field, op := "", "in"
	switch wire.TemplateKey {
	case CoreAIProduct:
		field = "segment.core_product"
	case ActiveContacts:
		field, op = "customer.active_within_days", "lte"
	case StageAny:
		field = "customer.stage"
	case TagAny:
		field = "tag.code"
	case OwnerAny:
		field = "owner.staff_id"
	case ChannelAny:
		field = "channel.code"
	case HXCRegistration:
		field = "hxc.registration_coverage"
	case WeComContactRegistration:
		field = "wecom.contact_registration"
	case QuestionnaireSubmissions:
		field = "survey.submissions"
	case QuestionnaireChoiceAnswers:
		field = "survey.first_complete_choice_answers"
	case PaidOrder:
		field = "order.paid"
	case ChannelEntry:
		field = "channel.entry"
	case RadarFirstClickElapsed:
		field = "radar.first_click_elapsed"
	case MemberExcludingGroupPaid:
		field = "hxc.member_excluding_group_paid"
	case MemberUsageStatus:
		field = "hxc.member_usage_status"
	default:
		return AST{}, ErrInvalid
	}
	if len(wire.Parameters) == 0 {
		return AST{}, ErrInvalid
	}
	if wire.TemplateKey == ActiveContacts {
		var days string
		if len(wire.Parameters) != 1 || json.Unmarshal(wire.Parameters["within_days"], &days) != nil || days == "" {
			return AST{}, ErrInvalid
		}
		return AST{SchemaVersion: 1, Template: wire.TemplateKey, Predicate: Predicate{Field: field, Op: op, Values: []string{days}}, Parameters: wire.Parameters}, nil
	}
	if wire.TemplateKey == StageAny || wire.TemplateKey == TagAny || wire.TemplateKey == OwnerAny || wire.TemplateKey == ChannelAny {
		parameter := map[Template]string{StageAny: "stages", TagAny: "tag_codes", OwnerAny: "staff_ids", ChannelAny: "channels"}[wire.TemplateKey]
		var values []string
		if len(wire.Parameters) != 1 || json.Unmarshal(wire.Parameters[parameter], &values) != nil || len(values) == 0 {
			return AST{}, ErrInvalid
		}
		return AST{SchemaVersion: 1, Template: wire.TemplateKey, Predicate: Predicate{Field: field, Op: op, Values: values}, Parameters: wire.Parameters}, nil
	}
	return AST{SchemaVersion: 1, Template: wire.TemplateKey, Predicate: Predicate{Field: field, Op: op}, Parameters: wire.Parameters}, nil
}
