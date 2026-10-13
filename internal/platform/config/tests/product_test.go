package testconfig

import "testing"

func TestInstalledProductLayerSelectionRequiresPackageOrExplicitRequest(t *testing.T) {
	keys := []string{"AICRM_REQUIRE_INSTALLED_PRODUCT_CONTRACTS", "AICRM_INSTALLED_PRODUCT_BINARY", "AICRM_INSTALLED_PRODUCT_ROOT", "AICRM_INSTALLED_PRODUCT_SHA", "AICRM_INSTALLED_PRODUCT_BINARY_SHA256", "AICRM_INSTALLED_PRODUCT_HOST_SOURCE_SHA", "AICRM_INSTALLED_PRODUCT_HOST_MANIFEST_SHA256", "AICRM_INSTALLED_PRODUCT_SOURCE_REPOSITORY"}
	for _, k := range keys {
		t.Setenv(k, "")
	}
	t.Setenv("AICRM_DATABASE_URL", "postgres://synthetic@127.0.0.1/aicrm_test_layer")
	if InstalledProductContractsRequested() {
		t.Fatal("generic PG unexpectedly selects installed layer")
	}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "1")
			if !InstalledProductContractsRequested() {
				t.Fatal("partial package/required input silently ignored")
			}
		})
	}
}
