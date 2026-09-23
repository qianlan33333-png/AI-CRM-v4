package http

import (
	"encoding/json"
	"testing"
)

func TestDecodeDonorGridConfigPreservesOrderOwnedMultiFactContract(t *testing.T) {
	config, err := decodeDonorGridConfig(json.RawMessage(`{
  "schema_version":1,
  "filter":{"logic":"and","conditions":[
    {"field":"renewal_count","operator":"between","value":[4.5,2.25]},
    {"field":"remark","operator":"not_contains","value":"已退款"},
    {"field":"remark","operator":"is_not_empty"}
  ]},
  "sorts":[{"field":"renewal_count","direction":"desc"},{"field":"remark","direction":"asc"}],
  "groups":[{"field":"remaining_days","direction":"asc"}]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Filter.Conditions) != 3 || len(config.Sorts) != 2 || len(config.Groups) != 1 {
		t.Fatalf("decoded config=%+v", config)
	}
	values, ok := config.Filter.Conditions[0].Value.([]any)
	if !ok || values[0] != 2.25 || values[1] != 4.5 {
		t.Fatalf("donor between normalization=%#v", config.Filter.Conditions[0].Value)
	}
	query, err := donorGridOrderQuery(7, config, "", 200)
	if err != nil || len(query.GridFilters) != 3 || len(query.GridSorts) != 2 || len(query.GridGroups) != 1 || query.GridFilters[0].Numbers[0] != 2.25 || query.GridFilters[0].Numbers[1] != 4.5 {
		t.Fatalf("order query=%+v err=%v", query, err)
	}
}

func TestDecodeDonorGridConfigRejectsOverlappingAndUnavailableFields(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":1,"filter":{"logic":"and","conditions":[]},"sorts":[{"field":"remark","direction":"asc"}],"groups":[{"field":"remark","direction":"asc"}]}`,
		`{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"formally_logged_in","operator":"in","value":["unavailable"]}]},"sorts":[],"groups":[]}`,
		`{"schema_version":1,"filter":{"logic":"and","conditions":[]},"sorts":[{"field":"remaining_days","direction":"asc"},{"field":"remaining_days","direction":"desc"}],"groups":[]}`,
	} {
		if _, err := decodeDonorGridConfig(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted unsupported config %s", raw)
		}
	}
}
