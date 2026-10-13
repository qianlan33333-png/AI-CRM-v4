package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	invoiceport "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Only database setup/readback runs in this test process. Every callback uses
// the exact installed binary, synthetic RSA/APIv3 keys and an isolated DB.
func TestInvoicePostgreSQLCallbackBootstrapInstalled(t *testing.T) {
	invoiceCallbackInstalled(t, false)
}
func TestInvoicePostgreSQLBillEntryInstalled(t *testing.T) { invoiceCallbackInstalled(t, true) }
func invoiceCallbackInstalled(t *testing.T, bill bool) {
	if !testconfig.InstalledProductContractsRequested() {
		t.Skip("installed-only invoice contract: unverified in source/PG layer; require exact package inputs")
	}
	installed := testconfig.InstalledProduct()
	if installed.Binary == "" || os.Getenv("AICRM_DATABASE_URL") == "" {
		t.Fatal("installed callback contract requires exact package inputs and isolated PostgreSQL")
	}
	validateOrdersOAuthInstalledProduct(t, installed)
	f := newInvoiceFixture(t)
	prepareInvoiceInstalledDirectory(t, f)
	id := f.order(f.customer, f.customer, "paid", "fixture invoice", "wechat_pay", time.Now())
	var a invoiceport.Application
	if bill {
		a.ID = f.billPayment(id)
		a.Snapshot = invoiceport.Snapshot{MerchantID: f.s.Config.MerchantID, AppID: f.s.Config.AppID, AppScope: f.s.Config.AppScope}
	} else {
		r := f.create(id, "callback-installed-0001")
		a = f.read(r.ID)
	}
	platformKey, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	public, e := x509.MarshalPKIXPublicKey(&platformKey.PublicKey)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	publicPath, keyPath := filepath.Join(dir, "public.pem"), filepath.Join(dir, "api-v3.key")
	apiKey := "0123456789abcdef0123456789abcdef"
	if e = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(keyPath, []byte(apiKey), 0600); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	endpoint := "http://" + address
	dataKey := base64.RawStdEncoding.EncodeToString([]byte(apiKey))
	// A fresh environment excludes all production credentials and proxies.
	environment := invoiceCallbackInstalledEnvironment(f.pool.Config().ConnString(), address, installed.SHA, a.Snapshot, publicPath, keyPath, dataKey)
	if bill {
		environment = invoiceBillInstalledEnvironment(t, environment, f.s.Config, a.Snapshot, platformKey, apiKey, dir)
	}

	process := startPeriodicContactInstalledProcess(t, installed.Binary, installed.Root, environment)
	waitInstalledSurveyReady(t, process, endpoint, installed.SHA)
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	exerciseInvoiceCallbackHTTP(t, f, bill, id, a, platformKey, apiKey, endpoint, client)
	if bill {
		exerciseInvoiceCardPaymentHTTP(t, f, a.ID, endpoint, client)
		t.Logf("invoice-card-payment-installed PASS source_sha=%s binary_sha256=%s actual_installed=true synthetic_database=true", installed.SHA, installed.BinarySHA256)
		t.Logf("invoice-bill-entry-installed PASS source_sha=%s binary_sha256=%s native_callback=verified transaction_id=shared zero_provider_writes=true", installed.SHA, installed.BinarySHA256)
	} else {
		t.Logf("invoice-callback-bootstrap-installed PASS source_sha=%s binary_sha256=%s callback_enabled=true invoice_provider=false zero_business_writes=true", installed.SHA, installed.BinarySHA256)
	}
}

func exerciseInvoiceCallbackHTTP(t *testing.T, f *invoiceFixture, bill bool, id int64, a invoiceport.Application, platformKey *rsa.PrivateKey, apiKey, endpoint string, client *http.Client) {
	t.Helper()
	// Read the authenticated history through the composed/installed API,
	// including an awaiting-title application in the callback-only bootstrap case.
	historyRequest, e := http.NewRequest(http.MethodGet, endpoint+"/api/h5/me/invoices?limit=1", nil)
	if e != nil {
		t.Fatal(e)
	}
	historyRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: f.token})
	historyResponse, e := client.Do(historyRequest)
	if e != nil {
		t.Fatal(e)
	}
	var history invoiceport.HistoryPage
	decodeError := json.NewDecoder(historyResponse.Body).Decode(&history)
	historyResponse.Body.Close()
	if historyResponse.StatusCode != 200 || decodeError != nil {
		t.Fatal("installed history unavailable", historyResponse.StatusCode, decodeError)
	}
	if bill {
		if len(history.Items) != 0 {
			t.Fatal("premature bill application")
		}
	} else if len(history.Items) != 1 || history.Items[0].ID != a.ID || history.Items[0].State != "awaiting_title" {
		t.Fatal("installed owner history")
	}
	counts := func() [4]int {
		var counts [4]int
		if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_callback_receipts), (SELECT count(*) FROM river_job WHERE kind='invoice.provider-query.v1'), (SELECT count(*) FROM external_effects WHERE owner='invoice'), (SELECT count(*) FROM invoice_applications)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3]); e != nil {
			t.Fatal(e)
		}
		return counts
	}
	before := counts()
	post := func(application, sub, merchant, signatureMode string) int {
		plain, _ := json.Marshal(map[string]string{"mchid": merchant, "sub_mchid": sub, "fapiao_apply_id": application})
		block, _ := aes.NewCipher([]byte(apiKey))
		gcm, _ := cipher.NewGCM(block)
		encrypted := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte("123456789012"), plain, []byte("invoice")))
		body, _ := json.Marshal(map[string]any{"id": "fixture-notification", "event_type": "FAPIAO.USER_APPLIED", "resource_type": "encrypt-resource", "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": encrypted, "nonce": "123456789012", "associated_data": "invoice"}})
		req, e := http.NewRequestWithContext(f.ctx, http.MethodPost, endpoint+"/api/callbacks/wechat-invoice", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		ts, nonce := fmt.Sprint(time.Now().Unix()), "fixture-nonce"
		digest := sha256.Sum256([]byte(ts + "\n" + nonce + "\n" + string(body) + "\n"))
		sig, e := rsa.SignPKCS1v15(rand.Reader, platformKey, crypto.SHA256, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Wechatpay-Timestamp", ts)
		req.Header.Set("Wechatpay-Nonce", nonce)
		req.Header.Set("Wechatpay-Serial", "PUB_KEY_ID_fixture")
		signature := base64.StdEncoding.EncodeToString(sig)
		switch signatureMode {
		case "forged":
			signature = "invalid-signature"
		case "sigtest":
			signature = "WECHATPAY/SIGNTEST/" + signature
		}
		req.Header.Set("Wechatpay-Signature", signature)
		response, e := client.Do(req)
		if e != nil {
			t.Fatal("installed callback request failed")
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return response.StatusCode
	}
	unknown := "inv_" + strings.Repeat("0", 28)
	for _, mode := range []string{"forged", "sigtest"} {
		if post(unknown, a.Snapshot.MerchantID, "service-fixture", mode) < 400 {
			t.Fatal("untrusted callback acknowledged", mode)
		}
	}
	if post(unknown, a.Snapshot.MerchantID, "foreign-service", "valid") < 400 || post(unknown, "", "service-fixture", "valid") < 400 {
		t.Fatal("wrong service or missing sub merchant acknowledged")
	}
	wantUnknown := 200
	if bill {
		wantUnknown = http.StatusConflict
	}
	ownStatus := post(unknown, a.Snapshot.MerchantID, "service-fixture", "valid")
	otherStatus := post(unknown, "other-service-child", "service-fixture", "valid")
	if ownStatus != wantUnknown || otherStatus != 200 {
		t.Fatalf("unknown application HTTP boundary own_status=%d want=%d other_status=%d want=200", ownStatus, wantUnknown, otherStatus)
	}
	t.Logf("unknown application HTTP boundary own_status=%d other_status=%d", ownStatus, otherStatus)
	if counts() != before {
		t.Fatal("unknown or untrusted notice created application/receipt/job/effect")
	}
	if bill {
		if post(a.ID, "foreign-sub", "service-fixture", "valid") != 200 {
			t.Fatal("unknown other merchant mutated local transaction")
		}
		if post(a.ID, a.Snapshot.MerchantID, "service-fixture", "valid") != 200 || post(a.ID, a.Snapshot.MerchantID, "service-fixture", "valid") != 200 {
			t.Fatal("native callback acceptance/replay")
		}
		after := counts()
		if after != [4]int{before[0] + 1, before[1] + 1, before[2], before[3] + 1} {
			t.Fatal("native callback duplicated receipt/job or issued prematurely", before, after)
		}
		if got := f.read(a.ID); got.ID != a.ID || got.OrderID != id || got.State != "awaiting_title" {
			t.Fatal("wrong native application")
		}
		request, _ := http.NewRequest(http.MethodGet, endpoint+"/api/h5/me/invoices/"+a.ID, nil)
		request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: f.token})
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var receipt invoiceport.Receipt
		err = json.NewDecoder(response.Body).Decode(&receipt)
		response.Body.Close()
		if response.StatusCode != 200 || err != nil || receipt.ID != a.ID {
			t.Fatal("installed transaction detail route", response.StatusCode, err)
		}
		if post(a.ID, "foreign-sub", "service-fixture", "valid") != 409 {
			t.Fatal("known wrong merchant accepted")
		}
		return
	}
	if post(a.ID, "foreign-sub", "service-fixture", "valid") != 409 || post(a.ID, a.Snapshot.MerchantID, "service-fixture", "valid") != 503 {
		t.Fatal("known application scope or disabled business boundary changed")
	}
	if counts() != before || f.read(a.ID).State != a.State {
		t.Fatal("callback-only process created business writes")
	}
}

func prepareInvoiceInstalledDirectory(t *testing.T, f *invoiceFixture) {
	t.Helper()
	missingDirectory := func() int {
		var count int
		if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM customers customer
			LEFT JOIN customer_directory_projection directory ON directory.customer_id=customer.id
			WHERE customer.status='active' AND directory.customer_id IS NULL`).Scan(&count); e != nil {
			t.Fatal("count callback fixture minimum directory gaps", e)
		}
		return count
	}
	if count := missingDirectory(); count != 2 {
		t.Fatalf("callback fixture minimum directory precondition: missing=%d want=2", count)
	}
	// The source-owner invoice fixture intentionally seeds only core customers.
	// A real API also requires each active customer's minimum directory record.
	// Prepare those presentation-only records through the Customer owner before
	// launching installed bytes; keep the installed readiness gate intact.
	var directory customerport.ProjectionWriter = customerstore.NewPostgreSQL()
	if e := f.s.UoW.Within(f.ctx, func(tx context.Context) error {
		now := time.Now().UTC()
		for _, id := range []int64{f.customer, f.other} {
			if e := directory.UpsertDirectoryProjection(tx, customerport.DirectoryProjection{
				CustomerID: customerdomain.CustomerID(id), CustomerStatus: customerdomain.StatusActive,
				DisplayName: "隔离回调测试用户", OneIDLabel: fmt.Sprintf("CID-%d", id), ActivationState: "active",
				Source: "invoice.callback.installed.fixture", SourceVersion: 1, LastSyncedAt: now, UpdatedAt: now,
			}); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal("prepare callback fixture minimum customer directory", e)
	}
	if count := missingDirectory(); count != 0 {
		t.Fatalf("callback fixture minimum directory incomplete: missing=%d want=0", count)
	}
	t.Log("callback fixture minimum directory: missing_before=2 missing_after=0")
	if e := checkCurrentReleaseSchema(f.ctx, f.pool, platformconfig.Runtime{}); e != nil {
		t.Fatal("callback fixture does not satisfy installed schema readiness", e)
	}
}

func invoiceBillInstalledEnvironment(t *testing.T, environment []string, finance invoiceport.Config, snapshot invoiceport.Snapshot, platformKey *rsa.PrivateKey, apiKey, dir string) []string {
	t.Helper()
	private, err := x509.MarshalPKCS8PrivateKey(platformKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePath, taxPath, certificatePath := filepath.Join(dir, "signing.pem"), filepath.Join(dir, "taxes.json"), filepath.Join(dir, "payment-platform.pem")
	if err = os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	taxes, _ := json.Marshal(finance.Taxes)
	if err = os.WriteFile(taxPath, taxes, 0600); err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(12345), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &platformKey.PublicKey, platformKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	for i, v := range environment {
		switch v {
		case "AICRM_INVOICE_PROVIDER_ENABLED=false", "AICRM_OUTBOUND_PROVIDER_ENABLED=false", "AICRM_WECHAT_PAY_PROVIDER_ENABLED=false":
			environment[i] = strings.TrimSuffix(v, "false") + "true"
		}
	}
	environment = append(environment, "AICRM_INVOICE_MERCHANT_SERIAL=fixture-serial", "AICRM_INVOICE_PRIVATE_KEY_PATH="+privatePath, "AICRM_INVOICE_SELLER_NAME="+finance.SellerName, "AICRM_INVOICE_SELLER_TAX_ID="+finance.SellerTaxID, "AICRM_INVOICE_BILLING_PERSON_ID="+finance.BillingPersonID, "AICRM_INVOICE_TAX_CONFIG_PATH="+taxPath)
	// Match the real enabled-Invoice startup contract. Only an API role runs:
	// no effects/query worker and no production credentials. Block any
	// accidental remote HTTP attempt while keeping loopback API reachable.
	environment = append(environment,
		"AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true", "AICRM_WECHAT_PAY_H5_APP_SECRET=fixture-h5-secret",
		"AICRM_SURVEY_OAUTH_APP_ID="+snapshot.AppID, "AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID=invoice-fixture-platform",
		"AICRM_WECHAT_PAY_APP_ID="+snapshot.AppID, "AICRM_WECHAT_PAY_APP_SCOPE="+snapshot.AppScope, "AICRM_WECHAT_PAY_APP_SECRET=fixture-app-secret",
		"AICRM_WECHAT_PAY_MERCHANT_SERIAL=fixture-payment-serial", "AICRM_WECHAT_PAY_PRIVATE_KEY_PATH="+privatePath, "AICRM_WECHAT_PAY_PLATFORM_CERT_PATH="+certificatePath, "AICRM_WECHAT_PAY_API_V3_KEY="+apiKey,
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost",
	)
	return environment
}

func invoiceCallbackInstalledEnvironment(databaseURL, address, sha string, snapshot invoiceport.Snapshot, publicPath, keyPath, dataKey string) []string {
	return []string{
		"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + sha,
		"AICRM_PUBLIC_ORIGIN=https://invoice.fixture", "AICRM_H5_PUBLIC_ORIGIN=https://invoice.fixture",
		"AICRM_SURVEY_DATA_KEY=" + dataKey, "AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey, "AICRM_ORDER_CONTACT_DATA_KEY=" + dataKey, "AICRM_REFERRAL_TOKEN_DATA_KEY=" + dataKey,
		"AICRM_GROUP_OPS_WEBHOOK_SECRET=invoice-callback-installed-fixture-webhook-secret",
		"AICRM_INVOICE_CALLBACK_ENABLED=true", "AICRM_INVOICE_PROVIDER_ENABLED=false",
		"AICRM_INVOICE_SERVICE_MERCHANT_ID=service-fixture", "AICRM_INVOICE_AUTH_MODE=public_key",
		"AICRM_INVOICE_PUBLIC_KEY_ID=PUB_KEY_ID_fixture", "AICRM_INVOICE_PUBLIC_KEY_PATH=" + publicPath, "AICRM_INVOICE_API_V3_KEY_PATH=" + keyPath,
		"AICRM_WECHAT_PAY_MERCHANT_ID=" + snapshot.MerchantID,
		"AICRM_WECHAT_PAY_H5_APP_ID=" + snapshot.AppID, "AICRM_WECHAT_PAY_H5_APP_SCOPE=" + snapshot.AppScope,
		"AICRM_EFFECTS_PROVIDER_ENABLED=false", "AICRM_WECHAT_PAY_PROVIDER_ENABLED=false", "AICRM_ALIPAY_PROVIDER_ENABLED=false", "AICRM_OUTBOUND_PROVIDER_ENABLED=false",
	}
}

// Load the exact fixture environment through the production parser before an
// installed run, including the deliberately different bootstrap/bill flags.
func TestInvoiceInstalledFixtureConfigContract(t *testing.T) {
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(name, "AICRM_") {
			t.Setenv(name, "")
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	publicPath, keyPath := filepath.Join(dir, "public.pem"), filepath.Join(dir, "api-v3.key")
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); err != nil {
		t.Fatal(err)
	}
	apiKey := "0123456789abcdef0123456789abcdef"
	if err = os.WriteFile(keyPath, []byte(apiKey), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := invoiceport.Snapshot{MerchantID: "merchant-fixture", AppID: "wx-fixture", AppScope: "wechat-app:wx-fixture"}
	finance := invoiceport.Config{SellerName: "测试商户", SellerTaxID: "seller-tax-fixture", BillingPersonID: "finance-fixture", Taxes: map[string]invoiceport.Tax{"course": {Code: "3010101020203000000", Rate: 600, GoodsName: "*培训服务*课程"}}}
	for _, bill := range []bool{false, true} {
		t.Run(fmt.Sprintf("bill_%t", bill), func(t *testing.T) {
			environment := invoiceCallbackInstalledEnvironment("postgres://fixture:test@localhost/aicrm_test_invoice", "127.0.0.1:0", strings.Repeat("a", 40), snapshot, publicPath, keyPath, base64.RawStdEncoding.EncodeToString([]byte(apiKey)))
			if bill {
				environment = invoiceBillInstalledEnvironment(t, environment, finance, snapshot, key, apiKey, dir)
			}
			for _, value := range environment {
				name, v, _ := strings.Cut(value, "=")
				t.Setenv(name, v)
			}
			cfg, err := platformconfig.Load()
			if err != nil {
				t.Fatal("fixture rejected by real startup configuration", err)
			}
			if cfg.Role != platformconfig.RoleAPI || cfg.WeCom.Enabled || !cfg.Invoice.CallbackEnabled || cfg.Invoice.Enabled != bill || cfg.Effects.ProviderEnabled != bill || cfg.WeChatPay.Enabled != bill || cfg.WeChatPay.H5OAuthEnabled != bill {
				t.Fatal("fixture startup capability boundary")
			}
			defaults, err := runtimeConfigDefaults(cfg)
			if err != nil {
				t.Fatal("fixture runtime defaults", err)
			}
			if err = validateAppliedRuntimeConfig(cfg); err != nil {
				t.Fatal("fixture rejected by composed runtime activation", err)
			}
			if bill {
				for _, setting := range defaults {
					if setting.Key == configport.InvoiceDefaultTaxCode && string(setting.Value) != `""` {
						t.Fatal("product-only fixture invented a fallback")
					}
				}
			}
			if bill {
				t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "false")
				t.Setenv("AICRM_EFFECTS_PROVIDER_ENABLED", "true")
				if _, err = platformconfig.Load(); err == nil {
					t.Fatal("unused environment name bypassed enabled invoice dependency check")
				}
			}
		})
	}
}

// This is source-composition evidence, separate from installed-binary evidence.
// Use the same exact environment as the enabled bill fixture and run the full
// API startup chain, including readiness and an authenticated mounted read.
func TestInvoicePostgreSQLInstalledFixtureStartup(t *testing.T) {
	f := newInvoiceFixture(t)
	prepareInvoiceInstalledDirectory(t, f)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(name, "AICRM_") {
			t.Setenv(name, "")
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	publicPath, keyPath := filepath.Join(dir, "public.pem"), filepath.Join(dir, "api-v3.key")
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	apiKey := "0123456789abcdef0123456789abcdef"
	if err = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, []byte(apiKey), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := invoiceport.Snapshot{MerchantID: f.s.Config.MerchantID, AppID: f.s.Config.AppID, AppScope: f.s.Config.AppScope}
	environment := invoiceCallbackInstalledEnvironment(f.pool.Config().ConnString(), "127.0.0.1:0", strings.Repeat("a", 40), snapshot, publicPath, keyPath, base64.RawStdEncoding.EncodeToString([]byte(apiKey)))
	environment = invoiceBillInstalledEnvironment(t, environment, f.s.Config, snapshot, key, apiKey, dir)
	for _, value := range environment {
		name, v, _ := strings.Cut(value, "=")
		t.Setenv(name, v)
	}
	cfg, err := platformconfig.Load()
	if err != nil {
		t.Fatal("fixture config", err)
	}
	application, err := compose(f.ctx, cfg)
	if err != nil {
		t.Fatal("full fixture API composition", err)
	}
	defer application.Close()
	if err = application.bootstrap(f.ctx, cfg.Bootstrap); err != nil {
		t.Fatal("fixture bootstrap", err)
	}
	for _, path := range []string{"/readyz", "/api/h5/me/invoices?limit=1"} {
		request := httptest.NewRequest(http.MethodGet, "https://invoice.fixture"+path, nil)
		request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: f.token})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("mounted fixture API %s status=%d", path, response.Code)
		}
		if strings.HasPrefix(path, "/api/") {
			var page invoiceport.HistoryPage
			if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 0 {
				t.Fatal("authenticated fixture history", err)
			}
		}
	}
	// Exercise the same signed native callback boundaries and transactional
	// readback used by the installed test, through the fully composed API.
	id := f.order(f.customer, f.customer, "paid", "fixture invoice", "wechat_pay", time.Now())
	a := invoiceport.Application{ID: f.billPayment(id), Snapshot: snapshot}
	server := httptest.NewServer(application.handler)
	defer server.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	exerciseInvoiceCallbackHTTP(t, f, true, id, a, key, apiKey, server.URL, &http.Client{Transport: transport, Timeout: 5 * time.Second})
	exerciseInvoiceCardPaymentHTTP(t, f, a.ID, server.URL, &http.Client{Transport: transport, Timeout: 5 * time.Second})
	t.Log("invoice fixture full source startup/callback PASS: Config.Load, composition, bootstrap, readiness, authenticated history, signed native acceptance/replay and detail; no workers or Provider writes")
}
