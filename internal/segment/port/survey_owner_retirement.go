package port

import "encoding/json"

// SurveyOwnerSelectionRetired distinguishes a real retired filter from the
// historical all/empty no-op. Never erase a specified filter into all users.
func SurveyOwnerSelectionRetired(template string, parameters map[string]json.RawMessage) bool {
	if template != "questionnaire_submissions" && template != "questionnaire_choice_answers" {
		return false
	}
	scopeRaw, scopePresent := parameters["owner_scope"]
	staffRaw, staffPresent := parameters["owner_staff_ids"]
	userRaw, userPresent := parameters["owner_userids"]
	if !scopePresent && !staffPresent && !userPresent {
		return false
	}
	var scope string
	if !scopePresent || json.Unmarshal(scopeRaw, &scope) != nil || scope != "all" || (!staffPresent && !userPresent) {
		return true
	}
	for _, raw := range []json.RawMessage{staffRaw, userRaw} {
		if raw == nil {
			continue
		}
		var ids []string
		if json.Unmarshal(raw, &ids) != nil || len(ids) != 0 {
			return true
		}
	}
	return false
}

// SurveyDefinitionOwnerSelectionRetired also covers immutable stored versions.
func SurveyDefinitionOwnerSelectionRetired(raw json.RawMessage) bool {
	var definition struct {
		TemplateKey string                     `json:"template_key"`
		Parameters  map[string]json.RawMessage `json:"parameters"`
	}
	return json.Unmarshal(raw, &definition) == nil && SurveyOwnerSelectionRetired(definition.TemplateKey, definition.Parameters)
}
