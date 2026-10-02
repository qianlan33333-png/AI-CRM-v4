package http

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestActivationPaidValidatesQuestionnaireReferenceBeforeNegativeMatch(t *testing.T) {
	h := &Handler{}
	h.BindAudienceSurveyReferences(surveyReferenceStub{questionnaires: map[string]string{"16": "16"}})
	for _, id := range []string{"16", "999"} {
		raw := json.RawMessage(`{"schema_version":1,"template_key":"paid_order","parameters":{"activation":{"questionnaire_status":"unsubmitted","questionnaire_ids":["` + id + `"]}}}`)
		_, err := h.normalizeSurveyReferences(context.Background(), raw)
		if id == "16" && err != nil {
			t.Fatal(err)
		}
		if id == "999" && !errors.Is(err, errReferenceUnknown) {
			t.Fatalf("unknown questionnaire accepted: %v", err)
		}
	}
}
