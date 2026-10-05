package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFrozenTemplatesAndCanonicalDefinition(t *testing.T) {
	if got := Templates(); len(got) != 9 {
		t.Fatalf("templates=%d", len(got))
	} else {
		for _, item := range got {
			if item.TemplateVersion != 1 || item.Label == "" || item.Description == "" || len(item.Fields) == 0 {
				t.Fatalf("missing frozen form metadata: %+v", item)
			}
		}
	}
	left, err := CanonicalDefinition(json.RawMessage(`{"parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"},"template_key":"wecom_contact_registration","schema_version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalDefinition(json.RawMessage(`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"}}`))
	if err != nil || string(left) != string(right) {
		t.Fatalf("canonical left=%s right=%s err=%v", left, right, err)
	}
}

func TestDefinitionRejectsOpenEndedCapabilities(t *testing.T) {
	invalid := []string{
		`{"schema_version":1,"template_key":"sql","parameters":{"query":"select * from customers"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"sql":"select 1"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":[] ,"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"0"}}`,
		`{"schema_version":1,"template_key":"channel_entry","parameters":{"channel_codes":["wecom"],"token":"secret"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["bob"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["09"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["0"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["-1"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["9223372036854775808"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["9","bob"],"contact_statuses":["active"],"registration_status":"any"}}`,
	}
	for _, raw := range invalid {
		if _, err := CanonicalDefinition(json.RawMessage(raw)); !errors.Is(err, ErrUnsupportedDefinition) {
			t.Fatalf("definition accepted: %s err=%v", raw, err)
		}
	}
}

func TestPaidWindowRequiresRFC3339AfterHostDatetimeConversion(t *testing.T) {
	valid := json.RawMessage(`{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["course-v3"],"paid_at_from":"2026-09-05T00:00:00Z","paid_at_to":"2026-09-05T01:00:00Z","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true}}`)
	if _, err := CanonicalDefinition(valid); err != nil {
		t.Fatalf("RFC3339 window rejected: %v", err)
	}
	localControlValue := json.RawMessage(`{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["course-v3"],"paid_at_from":"2026-09-05T08:00","paid_at_to":"2026-09-05T09:00","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true}}`)
	if _, err := CanonicalDefinition(localControlValue); !errors.Is(err, ErrUnsupportedDefinition) {
		t.Fatalf("datetime-local value bypassed Go AST validation: %v", err)
	}
	friendAtPayment := json.RawMessage(`{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["334465678"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true,"require_wecom_friend_at_paid_time":true,"friend_owner_staff_ids":["10"]}}`)
	if _, err := CanonicalDefinition(friendAtPayment); err != nil {
		t.Fatalf("friend-at-first-payment parameters rejected: %v", err)
	}
	friendGateWithoutOwner := json.RawMessage(`{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["334465678"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true,"require_wecom_friend_at_paid_time":true,"friend_owner_staff_ids":[]}}`)
	if _, err := CanonicalDefinition(friendGateWithoutOwner); !errors.Is(err, ErrUnsupportedDefinition) {
		t.Fatalf("friend gate without its exact employee was accepted: %v", err)
	}
}

func TestActivationDefinitionExtendsOnlyExistingPaidAndSubmissionTemplates(t *testing.T) {
	paid := `{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["334465678"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":false,"activation":%s}}`
	for _, v := range []string{`{"elapsed_hours_min":12,"elapsed_hours_max":36,"questionnaire_status":"unsubmitted","questionnaire_ids":["16"]}`, `{"enrollment_override":{"at":"2026-10-02T12:00:00+08:00","order_ids":[11,12]}}`} {
		if _, e := CanonicalDefinition(json.RawMessage(fmt.Sprintf(paid, v))); e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []string{`null`, `{"elapsed_hours_min":36,"elapsed_hours_max":12}`, `{"elapsed_hours_min":-1}`, `{"elapsed_hours_min":12.5}`, `{"elapsed_hours_min":9223372036854775807}`, `{"questionnaire_status":"unsubmitted"}`, `{"token_usage_status":"unused_7d"}`, `{"registration_status":"unknown"}`, `{"sql":"select 1"}`, `{"enrollment_override":{"at":"2026-10-02T12:00:00+08:00","order_ids":[11,11]}}`} {
		if _, e := CanonicalDefinition(json.RawMessage(fmt.Sprintf(paid, v))); !errors.Is(e, ErrUnsupportedDefinition) {
			t.Fatalf("invalid activation accepted: %s err=%v", v, e)
		}
	}
	submitted := json.RawMessage(`{"schema_version":1,"template_key":"questionnaire_submissions","parameters":{"questionnaire_ids":["16"],"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":false,"activation":{"elapsed_hours_min":12,"token_usage_status":"unused_7d"}}}`)
	if _, e := CanonicalDefinition(submitted); e != nil {
		t.Fatal(e)
	}
}

func TestQuestionnaireTemplatesRetireEmployeeAudienceWithoutBroadeningHistory(t *testing.T) {
	for _, item := range Templates() {
		if item.Key != "questionnaire_submissions" && item.Key != "questionnaire_choice_answers" {
			continue
		}
		for _, field := range item.Fields {
			if strings.HasPrefix(field.Name, "owner_") {
				t.Fatalf("retired employee field remains: %s/%s", item.Key, field.Name)
			}
		}
		raw, err := DefaultDefinition(item.Key)
		if err != nil || strings.Contains(string(raw), "owner_") {
			t.Fatalf("new default retains employee input: %s, %v", raw, err)
		}
	}
	base := `{"schema_version":1,"template_key":"questionnaire_submissions","parameters":{"questionnaire_ids":["16"],"require_wecom_identity":false%s}}`
	for _, owners := range []string{"", `,"owner_scope":"all","owner_staff_ids":[]`} {
		if _, err := CanonicalDefinition(json.RawMessage(fmt.Sprintf(base, owners))); err != nil {
			t.Fatalf("same unrestricted respondent semantics rejected: %s %v", owners, err)
		}
	}
	for _, owners := range []string{`,"owner_scope":"specified","owner_staff_ids":["9"]`, `,"owner_scope":"all","owner_staff_ids":["9"]`} {
		if _, err := CanonicalDefinition(json.RawMessage(fmt.Sprintf(base, owners))); !errors.Is(err, ErrUnsupportedDefinition) {
			t.Fatalf("retired selection silently accepted: %s %v", owners, err)
		}
	}
}
