package outbound

import (
	"encoding/json"
	"testing"
)

func TestSurveyEndpointMetadataOverridesEditableFields(t *testing.T) {
	target := SurveyCompletionTarget{PushType: "template", Remark: "template", CustomParams: map[string]string{"old": "value"}}
	raw := json.RawMessage(`{"type":"subscription","expires_at_ts":1893456000,"day":365,"frequency":1,"remark":"问卷激活","custom_params":{"source":"survey"}}`)
	if !validSurveyEndpointMetadata(raw) {
		t.Fatal("expected legacy questionnaire metadata to be valid")
	}
	applySurveyEndpointMetadata(&target, raw)
	if target.PushType != "subscription" || target.ExpiresAtTS == nil || *target.ExpiresAtTS != 1893456000 || target.Day == nil || *target.Day != 365 || target.Frequency == nil || *target.Frequency != 1 || target.Remark != "问卷激活" || target.CustomParams["source"] != "survey" {
		t.Fatalf("metadata not applied: %#v", target)
	}
	if _, retained := target.CustomParams["old"]; retained {
		t.Fatal("template custom parameters must not leak into the administrator configuration")
	}
	if validSurveyEndpointMetadata(json.RawMessage(`{"day":-1}`)) {
		t.Fatal("negative service period must be rejected")
	}
}

func TestEditableSurveyEndpointRejectsNonPublicDestinations(t *testing.T) {
	for _, endpoint := range []string{
		"https://receiver.example.test/complete",
		"https://receiver.example.test:8443/complete",
	} {
		if !editableSurveyEndpoint(endpoint) {
			t.Fatalf("public endpoint rejected: %q", endpoint)
		}
	}
	for _, endpoint := range []string{
		"http://receiver.example.test/complete",
		"https://localhost/complete",
		"https://receiver.local/complete",
		"https://127.0.0.1/complete",
		"https://10.0.0.1/complete",
		"https://169.254.169.254/complete",
		"https://224.0.0.1/complete",
		"https://0.0.0.0/complete",
		"https://[::1]/complete",
		"https://[fe80::1]/complete",
		"https://user:secret@receiver.example.test/complete",
		"https://receiver.example.test/complete#fragment",
	} {
		if editableSurveyEndpoint(endpoint) {
			t.Fatalf("unsafe endpoint accepted: %q", endpoint)
		}
	}
}
