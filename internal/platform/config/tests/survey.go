package testconfig

import "os"

// InstalledSurveyInputs selects immutable package bytes for synthetic recovery
// journeys. Empty Binary keeps ordinary source-owner tests. Runtime roles do
// not import this package; the journey validates all nonempty inputs.
type InstalledSurveyInputs struct {
	Binary, Root, SHA, BinarySHA256               string
	HostSHA, HostManifestSHA256, SourceRepository string
}

func InstalledSurvey() InstalledSurveyInputs {
	return InstalledSurveyInputs{
		Binary:             os.Getenv("AICRM_INSTALLED_SURVEY_BINARY"),
		Root:               os.Getenv("AICRM_INSTALLED_SURVEY_ROOT"),
		SHA:                os.Getenv("AICRM_INSTALLED_SURVEY_SHA"),
		BinarySHA256:       os.Getenv("AICRM_INSTALLED_SURVEY_BINARY_SHA256"),
		HostSHA:            os.Getenv("AICRM_INSTALLED_SURVEY_HOST_SOURCE_SHA"),
		HostManifestSHA256: os.Getenv("AICRM_INSTALLED_SURVEY_HOST_MANIFEST_SHA256"),
		SourceRepository:   os.Getenv("AICRM_INSTALLED_SURVEY_SOURCE_REPOSITORY"),
	}
}
