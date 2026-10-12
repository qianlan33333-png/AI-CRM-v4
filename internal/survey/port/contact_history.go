package port

// LegacyContactMapping identifies the precise immutable definition and question.
// It grants no identity binding or permission to rewrite the definition.
type LegacyContactMapping struct {
	DefinitionVersionID ID     `json:"definition_version_id"`
	QuestionID          ID     `json:"question_id"`
	Target              string `json:"target"`
}
