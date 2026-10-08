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
	t.Setenv("AICRM_INVOICE_CALLBACK_ENABLED", "")
	cfg, e := Load()
	if e != nil || cfg.Invoice.Enabled || cfg.Invoice.CallbackEnabled {
		t.Fatal("invoice defaults", e)
	}
	t.Setenv("AICRM_INVOICE_PROVIDER_ENABLED", "true")
	if _, e = Load(); e == nil {
		t.Fatal("enabled without service-provider permissions/credentials")
	}
}

func TestInvoiceCallbackBootstrapLoadsOnlyNotificationCredentials(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://fixture:test@localhost/aicrm_test_invoice")
	t.Setenv("AICRM_INVOICE_PROVIDER_ENABLED", "false")
	t.Setenv("AICRM_INVOICE_CALLBACK_ENABLED", "false")
	t.Setenv("AICRM_INVOICE_API_V3_KEY_PATH", "/missing")
	if _, e := Load(); e != nil {
		t.Fatal("disabled callback read credentials", e)
	}
	t.Setenv("AICRM_INVOICE_CALLBACK_ENABLED", "true")
	if _, e := Load(); e == nil {
		t.Fatal("incomplete callback trust accepted")
	}
	path := filepath.Join(t.TempDir(), "api-v3.key")
	key := strings.Repeat("k", 32)
	if e := os.WriteFile(path, []byte(key), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("AICRM_INVOICE_API_V3_KEY", "")
	t.Setenv("AICRM_INVOICE_API_V3_KEY_PATH", path)
	t.Setenv("AICRM_INVOICE_SERVICE_MERCHANT_ID", "service")
	t.Setenv("AICRM_INVOICE_AUTH_MODE", "public_key")
	t.Setenv("AICRM_INVOICE_PUBLIC_KEY_ID", "PUB_KEY_ID_fixture")
	t.Setenv("AICRM_INVOICE_PUBLIC_KEY_PATH", "/private/public.pem")
	cfg, e := Load()
	if e != nil || cfg.Invoice.Enabled || !cfg.Invoice.CallbackEnabled || cfg.Invoice.APIV3Key != key {
		t.Fatal("notification-only config failed", e)
	}
	if cfg.Effects.ProviderEnabled || cfg.WeChatPay.Enabled || cfg.WeChatPay.H5OAuthEnabled {
		t.Fatal("notification configuration enabled other providers")
	}
	if e := os.WriteFile(path, []byte("invalid-key"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(); e == nil || strings.Contains(e.Error(), "invalid-key") {
		t.Fatal("invalid decryption key accepted or exposed")
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
