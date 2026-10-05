package dsl

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestParseClosedTemplatesAndRejectSQLShape(t *testing.T) {
	ast, err := Parse(json.RawMessage(`{"schema_version":1,"template_key":"tag_any","parameters":{"tag_codes":["vip"]}}`))
	if err != nil || ast.Predicate.Field != "tag.code" {
		t.Fatalf("ast=%+v err=%v", ast, err)
	}
	invalid := []string{
		`{"schema_version":1,"template_key":"sql","parameters":{"query":"select * from customers"}}`,
		`{"schema_version":1,"template_key":"tag_any","parameters":{"tag_codes":["vip"],"where":"1=1"}}`,
		`{"schema_version":1,"template_key":"owner_any","parameters":{"staff_ids":[]}}`,
	}
	for _, raw := range invalid {
		if _, err := Parse(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func FuzzParseNeverAcceptsArbitraryInput(f *testing.F) {
	f.Add(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	f.Add(`select * from customers`)
	f.Fuzz(func(t *testing.T, raw string) {
		ast, err := Parse(json.RawMessage(raw))
		if err == nil && ast.SchemaVersion != 1 {
			t.Fatalf("accepted invalid schema: %+v", ast)
		}
	})
}

func TestQuestionnaireCompilerRejectsRetiredEmployeeSelection(t *testing.T) {
	base := `{"schema_version":1,"template_key":"questionnaire_choice_answers","parameters":{"questionnaire_id":"9","conditions":[{"question_id":"11","option_ids":["101"]}]%s}}`
	for _, owners := range []string{"", `,"owner_scope":"all","owner_staff_ids":[]`} {
		if _, err := Parse(json.RawMessage(fmt.Sprintf(base, owners))); err != nil {
			t.Fatal(err)
		}
	}
	for _, owners := range []string{`,"owner_scope":"specified","owner_staff_ids":["19"]`, `,"owner_scope":"all","owner_staff_ids":["19"]`, `,"owner_scope":"specified","owner_userids":["bob"]`} {
		if _, err := Parse(json.RawMessage(fmt.Sprintf(base, owners))); err == nil {
			t.Fatalf("retired employee filter still compiles: %s", owners)
		}
	}
}
