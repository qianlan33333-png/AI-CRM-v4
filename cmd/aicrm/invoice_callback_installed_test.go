package main

import (
	"bytes"
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
