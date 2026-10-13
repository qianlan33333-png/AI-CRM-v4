package config

import "testing"

func TestMaintenanceOperatorReadsOnlyHostEnvironment(t *testing.T) {
	t.Setenv("SUDO_USER", "fixture-operator")
	t.Setenv("AICRM_CLI_OPERATOR", "unrelated")
	if MaintenanceOperator() != "fixture-operator" {
		t.Fatal("maintenance operator source changed")
	}
	t.Setenv("SUDO_USER", "")
	if MaintenanceOperator() != "" {
		t.Fatal("configuration manufactured an operator")
	}
}
