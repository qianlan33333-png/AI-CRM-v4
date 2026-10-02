package config

import (
	"os"
)

// CollectedPhoneSourceKeys are consumed only by the source-owner repair
// composition. They are never exposed in flags, reports or error messages.
type CollectedPhoneSourceKeys struct{ OrderContact, Survey string }

func CollectedPhoneRepairKeys() CollectedPhoneSourceKeys {
	return CollectedPhoneSourceKeys{OrderContact: os.Getenv("AICRM_ORDER_CONTACT_DATA_KEY"), Survey: os.Getenv("AICRM_SURVEY_DATA_KEY")}
}

// CollectedPhoneAcceptanceBinary selects installed bytes for the isolated
// maintenance journey. Empty uses a local build and is not install evidence.
func CollectedPhoneAcceptanceBinary() string {
	return os.Getenv("AICRM_INSTALLED_PHONE_RECOVERY_BINARY")
}
