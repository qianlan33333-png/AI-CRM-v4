package config

import "os"

// CutoverWecomProvisionEnvironment is restricted to the directory read adapter.
func CutoverWecomProvisionEnvironment(name string) string {
	switch name {
	case "AICRM_WECOM_CORP_ID", "AICRM_WECOM_CONTACT_SECRET", "AICRM_DATABASE_URL":
		return os.Getenv(name)
	}
	return ""
}
