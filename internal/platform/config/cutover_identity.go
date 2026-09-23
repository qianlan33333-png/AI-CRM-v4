package config

import "os"

// CutoverIdentityEnvironment exposes only the explicit cutover proof inputs.
// Missing or unrecognized names remain empty; scope validation belongs to the
// migration proof boundary and never derives an OpenPlatform scope by default.
func CutoverIdentityEnvironment(name string) string {
	switch name {
	case "AICRM_WECOM_CORP_ID", "AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID", "AICRM_SOURCE_DATABASE_URL", "AICRM_DATABASE_URL":
		return os.Getenv(name)
	default:
		return ""
	}
}
