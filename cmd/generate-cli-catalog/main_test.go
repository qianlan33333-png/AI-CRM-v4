package main

import "testing"

func TestHumanBindingRequiresItsActualAuthenticationProtocol(t *testing.T) {
	for _, test := range []struct {
		name     string
		security []any
		allowed  bool
	}{
		{"session", []any{map[string]any{"adminSession": []any{}}}, true},
		{"session and CSRF", []any{map[string]any{"adminSession": []any{}, "csrfHeader": []any{}}}, true},
		{"independent signature", []any{map[string]any{"aiAssistantIntegration": []any{}}}, false},
		{"signature AND session", []any{map[string]any{"adminSession": []any{}, "aiAssistantIntegration": []any{}}}, false},
		{"signature OR session", []any{map[string]any{"aiAssistantIntegration": []any{}}, map[string]any{"adminSession": []any{}}}, true},
		{"public override", []any{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := supportsHumanSession(map[string]any{"security": test.security}, map[string]any{"security": []any{map[string]any{"adminSession": []any{}}}}); got != test.allowed {
				t.Fatalf("binding=%v want=%v", got, test.allowed)
			}
		})
	}
	if !supportsHumanSession(map[string]any{}, map[string]any{"security": []any{map[string]any{"adminSession": []any{}}}}) {
		t.Fatal("omission must inherit the declared root security")
	}
	if supportsHumanSession(map[string]any{}, map[string]any{}) {
		t.Fatal("missing authentication evidence must not bind a human route")
	}
}
