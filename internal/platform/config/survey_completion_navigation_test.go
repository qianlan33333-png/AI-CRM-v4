package config

import "testing"

func TestParseSurveyCompletionNavigationTargetsFailsClosed(t *testing.T) {
	valid := `{"survey.done":"https://survey.example.test/complete?source=crm"}`
	targets, err := ParseSurveyCompletionNavigationTargets(valid)
	if err != nil || targets["survey.done"] != "https://survey.example.test/complete?source=crm" {
		t.Fatalf("targets=%v err=%v", targets, err)
	}

	for _, raw := range []string{
		`{}`,
		` {"survey.done":"https://survey.example.test/complete"}`,
		`{"survey done":"https://survey.example.test/complete"}`,
		`{"survey.done":"http://survey.example.test/complete"}`,
		`{"survey.done":"https://user@survey.example.test/complete"}`,
		`{"survey.done":"https://survey.example.test/complete#fragment"}`,
		`{"survey.done":"https://localhost/complete"}`,
		`{"survey.done":"https://127.0.0.1/complete"}`,
		`{"survey.done":"https://survey.example.test/one","survey.done":"https://survey.example.test/two"}`,
		`{"survey.done":{"url":"https://survey.example.test/complete"}}`,
		`{"survey.done":"https://survey.example.test/complete"} trailing`,
	} {
		if _, err := ParseSurveyCompletionNavigationTargets(raw); err == nil {
			t.Fatalf("accepted unsafe configuration %q", raw)
		}
	}
}

func TestLoadRejectsUnsafeSurveyCompletionNavigationTargets(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_SURVEY_COMPLETION_NAVIGATION_TARGETS_JSON", `{"survey.done":"http://example.test/complete"}`)
	if _, err := Load(); err == nil {
		t.Fatal("unsafe navigation target accepted")
	}
	t.Setenv("AICRM_SURVEY_COMPLETION_NAVIGATION_TARGETS_JSON", `{"survey.done":"https://example.test/complete"}`)
	cfg, err := Load()
	if err != nil || cfg.Survey.CompletionNavigationTargetsJSON == "" {
		t.Fatalf("config=%+v err=%v", cfg.Survey, err)
	}
}
