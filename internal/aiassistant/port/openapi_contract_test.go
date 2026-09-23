package port_test

import (
	"os"
	"strings"
	"testing"
)

func TestOpenAPIFreezesAIAssistantRoutesAndSecurity(t *testing.T) {
	document, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(document)
	required := []string{
		"/api/admin/ai-assistant/plans:",
		"/api/admin/ai-assistant/plans/{plan_id}/recipients/{recipient_id}/content:",
		"/api/admin/ai-assistant/plans/{plan_id}/approve:",
		"/api/admin/ai-assistant/effects/{effect_id}/reconcile:",
		"/api/integrations/ai-assistant/review-plans:",
		"/api/admin/ai-assist/review-plans:",
		"RequiredIdempotencyKey",
		"expected_version",
		"aiAssistantIntegration",
	}
	for _, value := range required {
		if !strings.Contains(text, value) {
			t.Fatalf("OpenAPI is missing %q", value)
		}
	}
	if strings.Contains(text, "AIAssistantIntegrationIdentity:\n      type: object\n      properties:\n        assurance:") {
		t.Fatal("integration request must not self-declare identity assurance")
	}
}
