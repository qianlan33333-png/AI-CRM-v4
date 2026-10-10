package config

import "testing"

func TestSidebarWarehouseDefaultAndProtectedConfiguration(t *testing.T) {
	for _, key := range []string{"AICRM_SIDEBAR_GENERATION_ENABLED", "AICRM_SIDEBAR_WAREHOUSE_ENABLED", "AICRM_SIDEBAR_WAREHOUSE_ORIGIN", "AICRM_SIDEBAR_WAREHOUSE_BASIC_USER", "AICRM_SIDEBAR_WAREHOUSE_BASIC_PASSWORD"} {
		t.Setenv(key, "")
	}
	value, err := loadSidebarWarehouse()
	if err != nil || value.Enabled || value.GenerationEnabled {
		t.Fatalf("default must stay disabled: %v", err)
	}
	t.Setenv("AICRM_SIDEBAR_GENERATION_ENABLED", "true")
	if _, err := loadSidebarWarehouse(); err == nil {
		t.Fatal("generation accepted without the reader")
	}
	t.Setenv("AICRM_SIDEBAR_GENERATION_ENABLED", "false")
	t.Setenv("AICRM_SIDEBAR_WAREHOUSE_ENABLED", "true")
	if _, err := loadSidebarWarehouse(); err == nil {
		t.Fatal("enabled reader accepted absent authentication")
	}
	t.Setenv("AICRM_SIDEBAR_WAREHOUSE_ORIGIN", "https://agent.lhbl.com.cn")
	t.Setenv("AICRM_SIDEBAR_WAREHOUSE_BASIC_USER", "fixture-operator")
	t.Setenv("AICRM_SIDEBAR_WAREHOUSE_BASIC_PASSWORD", "fixture-only")
	if value, err = loadSidebarWarehouse(); err != nil || !value.Enabled {
		t.Fatalf("protected configuration rejected: %v", err)
	}
	t.Setenv("AICRM_SIDEBAR_GENERATION_ENABLED", "true")
	if value, err = loadSidebarWarehouse(); err != nil || !value.GenerationEnabled {
		t.Fatal("manual generation not independently enabled")
	}
	for _, origin := range []string{"http://agent.lhbl.com.cn", "https://user:pass@agent.lhbl.com.cn", "https://agent.lhbl.com.cn/?token=fixture", "https://agent.lhbl.com.cn/#fragment", "https://agent.lhbl.com.cn/api/local/v3/user-source-records"} {
		t.Setenv("AICRM_SIDEBAR_WAREHOUSE_ORIGIN", origin)
		if _, err = loadSidebarWarehouse(); err == nil {
			t.Fatal("unsafe or customer-wide endpoint accepted")
		}
	}
}
