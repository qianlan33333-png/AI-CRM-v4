package http

import (
	"context"
	"encoding/json"
)

func (h *Handler) normalizeSubmissionReferences(ctx context.Context, raw json.RawMessage, p map[string]json.RawMessage) (json.RawMessage, error) {
	if h.surveys == nil {
		return nil, errReferenceUnavailable
	}
	var refs []string
	if json.Unmarshal(p["questionnaire_ids"], &refs) != nil || len(refs) == 0 || len(refs) > 100 {
		return nil, errReferenceInvalid
	}
	out := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !validReferenceValue(ref) {
			return nil, errReferenceInvalid
		}
		id, found, err := h.surveys.ResolveAudienceQuestionnaire(ctx, ref)
		if err != nil {
			return nil, errReferenceUnavailable
		}
		if !found || id == "" {
			return nil, errReferenceUnknown
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	p["questionnaire_ids"], _ = json.Marshal(out)
	return marshalNormalizedOwnerDefinition(raw, p)
}
