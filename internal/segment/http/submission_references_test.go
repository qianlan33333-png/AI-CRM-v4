package http

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSubmissionReferencesResolveEveryQuestionnaireOrFail(t *testing.T) {
	h := &Handler{}
	h.BindAudienceSurveyReferences(surveyReferenceStub{questionnaires: map[string]string{"Survey A": "5", "5": "5", "Survey B": "7"}})
	for _, c := range []struct {
		refs string
		fail bool
	}{{`["Survey A","5","Survey B"]`, false}, {`["Survey A","missing"]`, true}, {`[]`, true}} {
		raw := json.RawMessage(`{"schema_version":1,"template_key":"questionnaire_submissions","parameters":{"questionnaire_ids":` + c.refs + `,"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":true}}`)
		out, err := h.normalizeSurveyReferences(context.Background(), raw)
		if c.fail {
			if err == nil {
				t.Fatal("unknown or empty accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Parameters struct {
				IDs []string `json:"questionnaire_ids"`
			} `json:"parameters"`
		}
		if json.Unmarshal(out, &d) != nil || len(d.Parameters.IDs) != 2 || d.Parameters.IDs[0] != "5" || d.Parameters.IDs[1] != "7" {
			t.Fatal("canonical references not retained")
		}
	}
	h.surveys = nil
	_, err := h.normalizeSurveyReferences(context.Background(), json.RawMessage(`{"template_key":"questionnaire_submissions","parameters":{}}`))
	if !errors.Is(err, errReferenceUnavailable) {
		t.Fatal("must fail closed")
	}
}
