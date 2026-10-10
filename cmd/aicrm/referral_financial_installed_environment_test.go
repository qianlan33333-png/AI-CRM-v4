package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// Financial composition is enabled with disposable credentials. Only the API
// role runs: no effects worker or due worker may send these accepted intents.
// Static certificate authentication avoids the SDK certificate downloader.
func paidReferralFinancialInstalledEnvironment(t *testing.T, databaseURL, releaseSHA, address string) []string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{SerialNumber: big.NewInt(12345), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	privatePath, certificatePath := filepath.Join(dir, "signing.pem"), filepath.Join(dir, "platform.pem")
	for path, block := range map[string]*pem.Block{
		privatePath: {Type: "PRIVATE KEY", Bytes: private}, certificatePath: {Type: "CERTIFICATE", Bytes: der},
	} {
		if err = os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	environment := paidReferralInstalledEnvironment(databaseURL, releaseSHA, address)
	for i, value := range environment {
		if value == "AICRM_WECHAT_PAY_PROVIDER_ENABLED=false" || value == "AICRM_OUTBOUND_PROVIDER_ENABLED=false" {
			environment[i] = strings.TrimSuffix(value, "false") + "true"
		}
	}
	return append(environment,
		"AICRM_WECHAT_PAY_APP_SECRET=synthetic-referral-app-secret",
		"AICRM_WECHAT_PAY_MERCHANT_ID=synthetic-referral-merchant",
		"AICRM_WECHAT_PAY_MERCHANT_SERIAL=synthetic-referral-serial",
		"AICRM_WECHAT_PAY_PRIVATE_KEY_PATH="+privatePath,
		"AICRM_WECHAT_PAY_PLATFORM_CERT_PATH="+certificatePath,
		"AICRM_WECHAT_PAY_API_V3_KEY=0123456789abcdef0123456789abcdef",
		"AICRM_WECHAT_PAY_H5_APP_ID=wx-paid-referral-h5",
		"AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx-paid-referral-h5",
		"AICRM_WECHAT_PAY_H5_APP_SECRET=synthetic-paid-referral-h5-secret",
		"AICRM_ORDER_CONTACT_DATA_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"AICRM_WECHAT_PAY_PROFIT_SHARING_ENABLED=true",
		"AICRM_WECHAT_PAY_PROFIT_SHARING_AUTH_MODE=certificate",
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost",
	)
}

func TestPaidReferralFinancialInstalledEnvironmentEnablesActualComposition(t *testing.T) {
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(name, "AICRM_") {
			t.Setenv(name, "")
		}
	}
	for _, value := range paidReferralFinancialInstalledEnvironment(t, "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_referral_financial_acceptance_test", strings.Repeat("a", 40), "127.0.0.1:18081") {
		name, value, _ := strings.Cut(value, "=")
		t.Setenv(name, value)
	}
	cfg, err := platformconfig.Load()
	if err != nil {
		t.Fatal("financial installed fixture rejected by real runtime parser", err)
	}
	if cfg.Role != platformconfig.RoleAPI || !cfg.WeChatPay.Enabled || !cfg.WeChatPay.ProfitSharingEnabled || !cfg.Effects.ProviderEnabled || cfg.WeChatPay.ProfitSharingAuthMode != "certificate" || cfg.Invoice.Enabled || cfg.Alipay.Enabled {
		t.Fatal("fixture must compose financial attribution in API role only")
	}
	if os.Getenv("HTTPS_PROXY") != "http://127.0.0.1:9" || os.Getenv("NO_PROXY") != "127.0.0.1,localhost" {
		t.Fatal("synthetic installed API must not reach remote Providers")
	}
}
