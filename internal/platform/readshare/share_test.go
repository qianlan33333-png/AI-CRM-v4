package readshare

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCapabilityStableRetryAndSeparation(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	token := Token(key, "product", 1, "create-1")
	if len(token) != 43 || token != Token(append([]byte{}, key...), "product", 1, "create-1") {
		t.Fatal("restart retry must preserve capability")
	}
	for _, other := range []string{Token(key, "hxc", 1, "create-1"), Token(key, "product", 2, "create-1"), Token(key, "product", 1, "create-2")} {
		if token == other {
			t.Fatal("capability scope collision")
		}
	}
}
func TestMetricProjectionEmptyAndSelected(t *testing.T) {
	metrics := map[string]any{"total": 100, "active": 40, "tiers": []string{"pro"}}
	if len(ProjectMetrics(metrics, json.RawMessage(`{"metrics":[]}`))) != 0 {
		t.Fatal("empty metric selection must remain empty")
	}
	got := ProjectMetrics(metrics, json.RawMessage(`{"metrics":["active","private"]}`))
	if len(got) != 1 || got["active"] != 40 {
		t.Fatal("only selected known metric may be returned")
	}
	if len(ProjectMetrics(metrics, nil)) != 3 {
		t.Fatal("old configuration must use defaults")
	}
}
