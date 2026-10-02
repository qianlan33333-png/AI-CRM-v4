package testconfig

import "os"

// InstalledProduct is test-only; runtime roles never consume these inputs.
func InstalledProduct() InstalledSurveyInputs {
	return InstalledSurveyInputs{
		Binary:       os.Getenv("AICRM_INSTALLED_PRODUCT_BINARY"),
		Root:         os.Getenv("AICRM_INSTALLED_PRODUCT_ROOT"),
		SHA:          os.Getenv("AICRM_INSTALLED_PRODUCT_SHA"),
		BinarySHA256: os.Getenv("AICRM_INSTALLED_PRODUCT_BINARY_SHA256"),
	}
}
