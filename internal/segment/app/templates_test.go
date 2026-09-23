package app

import (
	"encoding/json"
	"errors"
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
}
