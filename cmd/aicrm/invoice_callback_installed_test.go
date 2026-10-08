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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Only database setup/readback runs in this test process. Every callback uses
// the exact installed binary, synthetic RSA/APIv3 keys and an isolated DB.
func TestInvoicePostgreSQLCallbackBootstrapInstalled(t *testing.T) {
	installed := testconfig.InstalledProduct()
	if installed.Binary == "" || os.Getenv("AICRM_DATABASE_URL") == "" {
		t.Fatal("installed callback contract requires exact package inputs and isolated PostgreSQL")
	}
	validateOrdersOAuthInstalledProduct(t, installed)
	f := newInvoiceFixture(t)
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
	id := f.order(f.customer, f.customer, "paid", "fixture invoice", "wechat_pay", time.Now())
	r := f.create(id, "callback-installed-0001")
	a := f.read(r.ID)
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
	environment := []string{
		"AICRM_DATABASE_URL=" + f.pool.Config().ConnString(), "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + installed.SHA,
		"AICRM_PUBLIC_ORIGIN=https://invoice.fixture", "AICRM_H5_PUBLIC_ORIGIN=https://invoice.fixture",
		"AICRM_SURVEY_DATA_KEY=" + dataKey, "AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey, "AICRM_ORDER_CONTACT_DATA_KEY=" + dataKey, "AICRM_REFERRAL_TOKEN_DATA_KEY=" + dataKey,
		"AICRM_GROUP_OPS_WEBHOOK_SECRET=invoice-callback-installed-fixture-webhook-secret",
		"AICRM_INVOICE_CALLBACK_ENABLED=true", "AICRM_INVOICE_PROVIDER_ENABLED=false",
		"AICRM_INVOICE_SERVICE_MERCHANT_ID=service-fixture", "AICRM_INVOICE_AUTH_MODE=public_key",
		"AICRM_INVOICE_PUBLIC_KEY_ID=PUB_KEY_ID_fixture", "AICRM_INVOICE_PUBLIC_KEY_PATH=" + publicPath, "AICRM_INVOICE_API_V3_KEY_PATH=" + keyPath,
		"AICRM_WECHAT_PAY_MERCHANT_ID=" + a.Snapshot.MerchantID,
		"AICRM_EFFECTS_PROVIDER_ENABLED=false", "AICRM_WECHAT_PAY_PROVIDER_ENABLED=false", "AICRM_ALIPAY_PROVIDER_ENABLED=false", "AICRM_OUTBOUND_PROVIDER_ENABLED=false",
	}
	process := startPeriodicContactInstalledProcess(t, installed.Binary, installed.Root, environment)
	waitInstalledSurveyReady(t, process, endpoint, installed.SHA)
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	counts := func() [3]int {
		var counts [3]int
		if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_callback_receipts), (SELECT count(*) FROM river_job WHERE kind='invoice.provider-query.v1'), (SELECT count(*) FROM external_effects WHERE owner='invoice')`).Scan(&counts[0], &counts[1], &counts[2]); e != nil {
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
	if post(unknown, a.Snapshot.MerchantID, "service-fixture", "valid") != 200 || post(unknown, "other-service-child", "service-fixture", "valid") != 200 {
		t.Fatal("authenticated unknown application was not safely ignored")
	}
	if post(a.ID, "foreign-sub", "service-fixture", "valid") != 409 || post(a.ID, a.Snapshot.MerchantID, "service-fixture", "valid") != 503 {
		t.Fatal("known application scope or disabled business boundary changed")
	}
	if counts() != before || f.read(a.ID).State != a.State {
		t.Fatal("callback-only process created a receipt, task or effect, or changed application")
	}
	t.Logf("invoice-callback-bootstrap-installed PASS source_sha=%s binary_sha256=%s callback_enabled=true invoice_provider=false zero_business_writes=true", installed.SHA, installed.BinarySHA256)
}
