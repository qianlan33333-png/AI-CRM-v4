package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestInvoiceExplicitTrustAndPrivateKeyFile(t *testing.T) {
	c := Invoice{ServiceMerchantID: "service", MerchantSerial: "serial", PrivateKeyPath: "/private/key.pem", APIV3Key: strings.Repeat("k", 32), SellerName: "seller", SellerTaxID: "tax", BillingPersonID: "person", TaxConfigPath: "/private/taxes.json", AuthMode: "public_key", PublicKeyID: "PUB_KEY_ID_fixture", PublicKeyPath: "/private/public.pem"}
	if e := ValidateInvoice(c); e != nil {
		t.Fatal(e)
	}
	bad := c
	bad.PlatformCertPath = "/private/cert.pem"
	if ValidateInvoice(bad) == nil {
		t.Fatal("ambiguous trust accepted")
	}
	bad = c
	bad.AuthMode = ""
	if ValidateInvoice(bad) == nil {
		t.Fatal("trust inferred")
	}
	bad = c
	bad.PublicKeyID = "certificate-serial"
	if ValidateInvoice(bad) == nil {
		t.Fatal("certificate serial accepted as public-key ID")
	}
	bad = c
	bad.AuthMode = "certificate"
	if ValidateInvoice(bad) == nil {
		t.Fatal("public-key fields ignored in certificate mode")
	}
	cert := c
	cert.AuthMode = "certificate"
	cert.PublicKeyID = ""
	cert.PublicKeyPath = ""
	cert.PlatformCertPath = "/private/platform.pem"
	if e := ValidateInvoice(cert); e != nil {
		t.Fatal("legacy certificate mode failed", e)
	}
	path := filepath.Join(t.TempDir(), "api-v3.key")
	for _, newline := range []string{"", "\n", "\r\n"} {
		if os.WriteFile(path, []byte(c.APIV3Key+newline), 0600) != nil {
			t.Fatal("fixture write")
		}
		key, e := readInvoiceKey("", path)
		if e != nil || key != c.APIV3Key {
			t.Fatal("private file load", e)
		}
	}
	if _, e := readInvoiceKey(c.APIV3Key, path); e == nil {
		t.Fatal("two key sources accepted")
	}
	if os.Chmod(path, 0644) != nil {
		t.Fatal("fixture chmod")
	}
	if _, e := readInvoiceKey("", path); e == nil {
		t.Fatal("world-readable secret accepted")
	}
	os.Chmod(path, 0600)
	for _, raw := range []string{strings.Repeat("k", 31), strings.Repeat("k", 32) + "\n\n", " " + strings.Repeat("k", 31), strings.Repeat("k", 100)} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, e := readInvoiceKey("", path); e == nil || strings.Contains(e.Error(), raw) {
			t.Fatal("invalid or exposed secret")
		}
	}
}
