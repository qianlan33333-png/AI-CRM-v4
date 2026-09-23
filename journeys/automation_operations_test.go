package journeys_test

import (
	"os"
	"strings"
	"testing"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

func TestAutomationOperationsContractIsClosedAndVersioned(t *testing.T) {
	if segmentport.EventAudienceMemberEnteredV1 != string(automationport.TriggerAudienceMemberEnteredV1) {
		t.Fatal("audience event and automation trigger contract drifted")
	}
	if automationport.TriggerCustomerTagAppliedV1 != "customer.tag_applied.v1" {
		t.Fatal("tag trigger compatibility version drifted")
	}
	if automationport.ActionOutboundMessage != "outbound_message" {
		t.Fatal("outbound action contract drifted")
	}
	if outboundport.CompletionOutcomeUnknown != "outcome_unknown" || automationport.RecipientOutcomeUnknown != "outcome_unknown" {
		t.Fatal("unknown outcome must remain explicit across owners")
	}
	if identityport.AudienceConflict != "conflict" || segmentport.IdentityConflict != "conflict" {
		t.Fatal("identity conflict must never collapse into membership")
	}
}

func TestAutomationOperationsOpenAPIClosesUnsafeInputs(t *testing.T) {
	document, err := os.ReadFile("../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(document)
	for _, required := range []string{
		"/api/admin/ai-audience/packages/{package_id}/configuration-preview:",
		"/api/admin/automation-runs/{run_id}/effects/{effect_id}/reconcile:",
		"AutomationOpsWebhookFact: {type: object, additionalProperties: false",
		"provider_member_references: {type: array, minItems: 1, maxItems: 5",
		"member_count: {type: integer, format: int64, minimum: 0, maximum: 100000}",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("missing closed OpenAPI contract %q", required)
		}
	}

	webhookStart := strings.Index(source, "    AutomationOpsWebhookFact:")
	if webhookStart < 0 {
		t.Fatal("webhook schema start missing")
	}
	webhookEnd := strings.Index(source[webhookStart:], "\n    AutomationOpsWebhookReceipt:")
	if webhookEnd < 0 {
		t.Fatal("webhook schema end missing")
	}
	webhookSchema := source[webhookStart : webhookStart+webhookEnd]
	for _, forbidden := range []string{"assurance", "verified", "provider_body", "token"} {
		if strings.Contains(strings.ToLower(webhookSchema), forbidden) {
			t.Fatalf("webhook body may not self-declare %q", forbidden)
		}
	}
}
