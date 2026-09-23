package port

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestFieldMappingPreservesTypesPrecisionAndOnlyMappedKeys(t *testing.T) {
	mapping, err := DecodeFieldMapping(json.RawMessage(`{"version":1,"fields":[{"key":"n","source":"fixed","value_type":"number","value":9007199254740993},{"key":"j","source":"fixed","value_type":"json","value":{"a":[true,null,1.000000000000000001]}},{"key":"phone","source":"variable","value_type":"string","variable":"order.mobile"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CompileFieldMapping(mapping, SyntheticFieldMappingVariables())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"j":{"a":[true,null,1.000000000000000001]},"n":9007199254740993,"phone":"+8613800000000"}` {
		t.Fatalf("precision/shape lost: %s", raw)
	}
	_, err = CompileFieldMapping(mapping, nil)
	if !errors.Is(err, ErrFieldMappingVariableUnavailable) {
		t.Fatalf("missing facts: %v", err)
	}
	raw, err = CompileFieldMapping(mapping, map[string]json.RawMessage{"order.mobile": json.RawMessage(`null`)})
	if err != nil || !strings.Contains(string(raw), `"phone":null`) {
		t.Fatalf("confirmed absent: %s %v", raw, err)
	}
}
func TestFieldMappingRejectsAmbiguousOrUnsafeDefinitions(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"fields":[]}`,
		`{"version":2,"fields":[]}`,
		`{"version":1,"version":1,"fields":[]}`,
		`{"version":1,"fields":[{"key":" x","source":"fixed","value_type":"string","value":"a"}]}`,
		`{"version":1,"fields":[{"key":"","source":"fixed","value_type":"string","value":"a"}]}`,
		`{"version":1,"fields":[{"key":"__proto__","source":"fixed","value_type":"string","value":"a"}]}`,
		`{"version":1,"fields":[{"key":"x","source":"fixed","value_type":"json","value":{"constructor":{}}}]}`,
		`{"version":1,"fields":[{"key":"x","source":"fixed","value_type":"json","value":{"a":1,"a":2}}]}`,
		`{"version":1,"fields":[{"key":"x","source":"fixed","value_type":"number","value":"1"}]}`,
		`{"version":1,"fields":[{"key":"x","source":"fixed","value_type":"json","value":null}]}`,
		`{"version":1,"fields":[{"key":"x","source":"variable","value_type":"number","variable":"order.mobile"}]}`,
		`{"version":1,"fields":[{"key":"x","source":"variable","value_type":"string","variable":"order.mobile","value":null}]}`,
		`{"version":1,"fields":[{"key":"x","source":"fixed","value_type":"null","value":null},{"key":"x","source":"fixed","value_type":"null","value":null}]}`,
	} {
		if _, err := DecodeFieldMapping(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestFieldMappingDecimalEqualityAfterJSONBNormalization(t *testing.T) {
	makeMapping := func(n string) *FieldMapping {
		return &FieldMapping{Version: 1, Fields: []FieldMappingField{{Key: "x", Source: "fixed", ValueType: "number", Value: json.RawMessage(n)}}}
	}
	for _, pair := range [][2]string{{"1e3", "1000"}, {"0.0100", "1e-2"}, {"-0", "0"}, {"1e999999999999999", "10e999999999999998"}} {
		if !EqualFieldMappings(makeMapping(pair[0]), makeMapping(pair[1])) {
			t.Fatalf("not equal %v", pair)
		}
	}
	if EqualFieldMappings(makeMapping("9007199254740992"), makeMapping("9007199254740993")) {
		t.Fatal("precision collapsed")
	}
}
