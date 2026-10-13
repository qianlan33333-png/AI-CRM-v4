package testconfig

import "os"

// InstalledProduct is test-only; runtime roles never consume these inputs.
func InstalledProduct() InstalledSurveyInputs {
	return InstalledSurveyInputs{
		Binary:             os.Getenv("AICRM_INSTALLED_PRODUCT_BINARY"),
		Root:               os.Getenv("AICRM_INSTALLED_PRODUCT_ROOT"),
		SHA:                os.Getenv("AICRM_INSTALLED_PRODUCT_SHA"),
		BinarySHA256:       os.Getenv("AICRM_INSTALLED_PRODUCT_BINARY_SHA256"),
		HostSHA:            os.Getenv("AICRM_INSTALLED_PRODUCT_HOST_SOURCE_SHA"),
		HostManifestSHA256: os.Getenv("AICRM_INSTALLED_PRODUCT_HOST_MANIFEST_SHA256"),
		SourceRepository:   os.Getenv("AICRM_INSTALLED_PRODUCT_SOURCE_REPOSITORY"),
	}
}

// An ordinary source/PG run cannot prove an installed package. Explicit
// required mode or any package input opts into the separate installed layer;
// that layer must reject incomplete inputs rather than silently skip.
func InstalledProductContractsRequested() bool {
	i := InstalledProduct()
	return os.Getenv("AICRM_REQUIRE_INSTALLED_PRODUCT_CONTRACTS") == "1" ||
		i.Binary != "" || i.Root != "" || i.SHA != "" || i.BinarySHA256 != "" ||
		i.HostSHA != "" || i.HostManifestSHA256 != "" || i.SourceRepository != ""
}
