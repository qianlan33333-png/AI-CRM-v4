package config

import "testing"

func TestInvoiceProviderDisabledByDefaultAndIncompleteFailsClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://fixture:test@localhost/aicrm_test_invoice")
	t.Setenv("AICRM_INVOICE_PROVIDER_ENABLED", "")
	cfg, e := Load()
	if e != nil || cfg.Invoice.Enabled {
		t.Fatal("invoice defaults", e)
	}
	t.Setenv("AICRM_INVOICE_PROVIDER_ENABLED", "true")
	if _, e = Load(); e == nil {
		t.Fatal("enabled without service-provider permissions/credentials")
	}
}
