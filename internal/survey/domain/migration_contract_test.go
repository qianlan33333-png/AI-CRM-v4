package domain

import (
	"os"
	"strings"
	"testing"
)

func TestSurveyDefinitionMigrationOwnershipAndImmutability(t *testing.T) {
	contents, err := os.ReadFile("../../../migrations/0018_survey.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, required := range []string{
		"Owner: internal/survey",
		"CREATE TABLE survey_questionnaires",
		"CREATE TABLE survey_definition_versions",
		"CREATE TABLE survey_definition_questions",
		"CREATE TABLE survey_definition_options",
		"CREATE TABLE survey_score_rules",
		"CREATE TABLE survey_operation_receipts",
		"survey_definition_versions_immutable",
		"survey_definition_questions_immutable",
		"ON DELETE RESTRICT",
		"Forward-only",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{" unionid ", " openid ", " external_userid ", " phone ", " provider_response", " webhook_url"} {
		if strings.Contains(strings.ToLower(source), forbidden) {
			t.Fatalf("migration contains forbidden identity/provider field %q", forbidden)
		}
	}
}
