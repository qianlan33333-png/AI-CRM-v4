package testconfig

import "os"

// InstalledAudience is test-only. Empty Binary retains the source fixture.
func InstalledAudience() InstalledSurveyInputs {
	return InstalledSurveyInputs{
		Binary:       os.Getenv("AICRM_INSTALLED_AUDIENCE_BINARY"),
		Root:         os.Getenv("AICRM_INSTALLED_AUDIENCE_ROOT"),
		SHA:          os.Getenv("AICRM_INSTALLED_AUDIENCE_SHA"),
		BinarySHA256: os.Getenv("AICRM_INSTALLED_AUDIENCE_BINARY_SHA256"),
	}
}
