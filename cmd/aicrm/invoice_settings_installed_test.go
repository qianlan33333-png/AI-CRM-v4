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
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	configcatalog "github.com/qianlan33333-png/AI-CRM-v3/internal/config"
	cp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Fixture setup/readback is isolated PostgreSQL. All business/configuration HTTP
// executes the supplied installed bytes, with no source Host or worker process.
func TestInvoicePostgreSQLSettingsInstalled(t *testing.T) {
	if !testconfig.InstalledProductContractsRequested() {
		t.Skip("installed-only invoice contract: unverified in source/PG layer; require exact package inputs")
	}
	installed := testconfig.InstalledProduct()
	if installed.Binary == "" || os.Getenv("AICRM_DATABASE_URL") == "" {
		t.Fatal("installed settings contract requires exact package and isolated PostgreSQL")
	}
	validateOrdersOAuthInstalledProduct(t, installed)
	f := newInvoiceFixture(t)
	prepareInvoiceInstalledDirectory(t, f)
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&platformKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	publicPath, keyPath := filepath.Join(dir, "public.pem"), filepath.Join(dir, "old-api-v3.key")
	oldKey, newKey := "0123456789abcdef0123456789abcdef", strings.Repeat("Z", 32)
	if err = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, []byte(oldKey), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	endpoint := "http://" + address
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil}
	// TLS transport preserves the normal Secure-cookie/Origin contract. This
	// proxy forwards requests only and never constructs an application handler.
	server := httptest.NewTLSServer(proxy)
	defer server.Close()
	defer proxy.Transport.(*http.Transport).CloseIdleConnections()
	snapshot := p.Snapshot{MerchantID: f.s.Config.MerchantID, AppID: f.s.Config.AppID, AppScope: f.s.Config.AppScope}
	finance := f.s.Config
	finance.Taxes = map[string]p.Tax{"*": {Code: "3040203000000000000", Rate: 100, GoodsName: "服务费"}, "course": f.s.Config.Taxes["course"]}
	environment := invoiceCallbackInstalledEnvironment(f.pool.Config().ConnString(), address, installed.SHA, snapshot, publicPath, keyPath, base64.RawStdEncoding.EncodeToString([]byte(oldKey)))
	environment = invoiceBillInstalledEnvironment(t, environment, finance, snapshot, platformKey, oldKey, dir)
	for i, value := range environment {
		if strings.HasPrefix(value, "AICRM_PUBLIC_ORIGIN=") {
			environment[i] = "AICRM_PUBLIC_ORIGIN=" + server.URL
		}
		if strings.HasPrefix(value, "AICRM_H5_PUBLIC_ORIGIN=") {
			environment[i] = "AICRM_H5_PUBLIC_ORIGIN=" + server.URL
		}
	}
	materialDir := filepath.Join(dir, "private-materials")
	environment = append(environment, "AICRM_INVOICE_MATERIAL_DIR="+materialDir, "AICRM_INVOICE_LOGO_URL=https://mmbiz.qpic.cn/mmbiz_png/fixture/0", "AICRM_BOOTSTRAP_USERNAME=invoice-settings-owner", "AICRM_BOOTSTRAP_PASSWORD=invoice-settings-owner-password", "AICRM_BOOTSTRAP_DISPLAY_NAME=Invoice Settings Owner")
	process := startPeriodicContactInstalledProcess(t, installed.Binary, installed.Root, environment)
	waitInstalledSurveyReady(t, process, endpoint, installed.SHA)
	client := server.Client()
	client.Timeout = 10 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	login, _ := io.ReadAll(response.Body)
	response.Body.Close()
	match := regexp.MustCompile(`name="login_csrf_token" value="([^"]+)"`).FindSubmatch(login)
	if response.StatusCode != 200 || len(match) != 2 {
		t.Fatal("installed login CSRF missing")
	}
	form := url.Values{"username": {"invoice-settings-owner"}, "password": {"invoice-settings-owner-password"}, "login_csrf_token": {string(match[1])}}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", server.URL)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatal("installed administrator login failed", response.StatusCode)
	}

	call := func(method, path string, payload any, key string, want int) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(payload)
		req, e := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", server.URL)
		for _, cookie := range client.Jar.Cookies(req.URL) {
			if cookie.Name == "aicrm_admin_csrf" {
				req.Header.Set("X-CSRF-Token", cookie.Value)
			}
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, e := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if e != nil || res.StatusCode != want || !json.Valid(body) {
			t.Fatalf("installed settings %s %s status=%d want=%d", method, path, res.StatusCode, want)
		}
		if bytes.Contains(body, []byte(newKey)) || bytes.Contains(body, []byte(oldKey)) || bytes.Contains(body, []byte(dir)) {
			t.Fatal("private credential or filesystem path exposed")
		}
		return body
	}
	var staged struct {
		ID     string `json:"key_file_id"`
		Staged bool   `json:"staged"`
	}
	if json.Unmarshal(call("POST", "/api/admin/config/invoice-key", map[string]string{"key": newKey}, "", 201), &staged) != nil || len(staged.ID) != 32 || !staged.Staged {
		t.Fatal("installed upload did not return opaque reference")
	}
	stagedPath := filepath.Join(materialDir, staged.ID+".key")
	info, err := os.Stat(stagedPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("uploaded key not protected")
	}
	keyBytes, err := os.ReadFile(stagedPath)
	if err != nil || string(keyBytes) != newKey {
		t.Fatal("uploaded key content not saved")
	}

	var listing struct {
		Action    string               `json:"admin_action_token"`
		Effective cp.EffectiveSnapshot `json:"effective"`
	}
	if json.Unmarshal(call("GET", "/api/admin/config/runtime-releases", nil, "", 200), &listing) != nil || listing.Action == "" {
		t.Fatal("create action token unavailable")
	}
	settings := []cp.RuntimeSetting{runtimeApplySetting(t, cp.InvoiceKeyFileID, staged.ID), runtimeApplySetting(t, cp.InvoiceDefaultGoodsName, strings.Repeat("服", 128)), runtimeApplySetting(t, cp.InvoiceCardConfirmed, true)}
	var saved struct {
		Release cp.RuntimeRelease `json:"runtime_release"`
	}
	if json.Unmarshal(call("POST", "/api/admin/config/runtime-releases", map[string]any{"expected_base_revision": listing.Effective.Revision, "settings": settings, "admin_action_token": listing.Action}, "invoice-installed-save", 201), &saved) != nil || saved.Release.ID < 1 {
		t.Fatal("installed draft missing")
	}
	path := fmt.Sprintf("/api/admin/config/runtime-releases/%d", saved.Release.ID)
	var detail struct {
		Actions map[string]string `json:"actions"`
	}
	json.Unmarshal(call("GET", path, nil, "", 200), &detail)
	var validated struct {
		Release   cp.RuntimeRelease `json:"runtime_release"`
		Validated bool              `json:"validated"`
	}
	if json.Unmarshal(call("POST", path+"/validate", map[string]string{"admin_action_token": detail.Actions["validate"]}, "invoice-installed-validate", 200), &validated) != nil || !validated.Validated {
		t.Fatal("valid boundary failed installed validation")
	}
	json.Unmarshal(call("GET", path, nil, "", 200), &detail)
	call("POST", path+"/publish", map[string]any{"expected_base_revision": listing.Effective.Revision, "expected_checksum": validated.Release.Checksum, "admin_action_token": detail.Actions["publish"]}, "invoice-installed-publish", 200)
	var card p.CardSetup
	json.Unmarshal(call("GET", "/api/admin/config/invoice-card", nil, "", 200), &card)
	if card.ConfigurationApplied {
		t.Fatal("publication incorrectly reported applied before restart")
	}
	stopInvoiceSettingsInstalledProcess(t, process)
	process = startPeriodicContactInstalledProcess(t, installed.Binary, installed.Root, environment)
	waitInstalledSurveyReady(t, process, endpoint, installed.SHA)
	var catalog struct {
		Categories   []configcatalog.RuntimeCategory `json:"categories"`
		Effective    cp.EffectiveSnapshot            `json:"effective"`
		Applications []cp.RuntimeApplication         `json:"applications"`
	}
	if json.Unmarshal(call("GET", "/api/admin/config/runtime-catalog", nil, "", 200), &catalog) != nil || catalog.Effective.Revision != saved.Release.ID || catalog.Effective.Checksum != validated.Release.Checksum {
		t.Fatal("installed independent published readback mismatch")
	}
	fields, presence := 0, map[string]bool{}
	for _, category := range catalog.Categories {
		if category.Key != "wechat_pay" {
			continue
		}
		for _, field := range category.Fields {
			if strings.HasPrefix(string(field.Key), "wechat_invoice.") {
				fields++
			}
			if strings.HasPrefix(field.SecretReference, "environment://AICRM_INVOICE_") {
				if field.Configured == nil || !*field.Configured || field.Input != "secret-reference" {
					t.Fatal("credential readback not presence-only")
				}
				presence[field.SecretReference] = true
			}
		}
	}
	if fields != 12 || len(presence) != 3 {
		t.Fatal("installed invoice catalog incomplete", fields, len(presence))
	}
	applicationFound := false
	for _, fact := range catalog.Applications {
		if fact.Role == "api" && fact.ReleaseSHA == installed.SHA && fact.Revision == saved.Release.ID && fact.SnapshotChecksum == catalog.Effective.Checksum {
			applicationFound = true
		}
	}
	if !applicationFound {
		t.Fatal("new installed API application fact missing")
	}
	json.Unmarshal(call("GET", "/api/admin/config/invoice-card", nil, "", 200), &card)
	if !card.ConfigurationApplied || card.State != "not_submitted" || card.EffectID != "" {
		t.Fatal("installed card state changed without explicit submission")
	}
	for _, setting := range catalog.Effective.Settings {
		if setting.Key == cp.InvoiceDefaultGoodsName && string(setting.Value) != fmt.Sprintf("%q", strings.Repeat("服", 128)) {
			t.Fatal("published unicode boundary not retained")
		}
	}
	// Two synthetic signed native notices prove the installed consumer retained
	// the product override and uses the published default for future products.
	// Title-query jobs remain queued: no query/effects worker is started.
	for _, product := range []string{"course", "future-installed"} {
		order := f.orderWithProduct(f.customer, f.customer, "paid", "隔离开票商品", product, "wechat_pay", time.Now())
		transaction := f.billPayment(order)
		invoiceInstalledSettingsKeyReadback(t, client, server.URL, platformKey, newKey, f.s.Config.MerchantID, transaction, 200)
		var raw []byte
		var frozen p.Snapshot
		if err = f.pool.QueryRow(f.ctx, `SELECT snapshot FROM invoice_applications WHERE id=$1`, transaction).Scan(&raw); err != nil || json.Unmarshal(raw, &frozen) != nil || len(frozen.Lines) != 1 {
			t.Fatal("installed invoice snapshot missing", err)
		}
		want := finance.Taxes["course"]
		if product != "course" {
			want = p.Tax{Code: "3040203000000000000", Rate: 100, GoodsName: strings.Repeat("服", 128)}
		}
		if frozen.Lines[0].TaxCode != want.Code || frozen.Lines[0].TaxRate != want.Rate || frozen.Lines[0].GoodsName != want.GoodsName {
			t.Fatal("installed default or product override changed")
		}
	}
	// A synthetic signed/decrypted notice for another merchant proves the new
	// key was actually read. It creates no invoice or Provider effect.
	invoiceInstalledSettingsKeyReadback(t, client, server.URL, platformKey, newKey, "other-fixture", "45000000000000000000000000000000", 200)
	invoiceInstalledSettingsKeyReadback(t, client, server.URL, platformKey, oldKey, "other-fixture", "45000000000000000000000000000000", 400)
	var attempts, cardIntents int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM external_effect_attempts),(SELECT count(*) FROM invoice_card_setup_intents)`).Scan(&attempts, &cardIntents); err != nil || attempts != 0 || cardIntents != 0 {
		t.Fatal("installed fixture made a Provider write", err)
	}
	stopInvoiceSettingsInstalledProcess(t, process)
	t.Logf("invoice-settings-installed PASS source_sha=%s binary_sha256=%s admin_login=normal csrf=verified save_validate_publish=true restarted_application=true key_readback=decrypted credentials=presence_only tax_override=retained zero_provider_writes=true", installed.SHA, installed.BinarySHA256)
}

func stopInvoiceSettingsInstalledProcess(t *testing.T, process *domesticSmokeProcess) {
	t.Helper()
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal("stop installed settings API", err)
	}
	select {
	case err := <-process.done:
		process.finished = true
		if err != nil {
			t.Fatal("installed settings API stopped unexpectedly", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("installed settings API shutdown deadline")
	}
}

func invoiceInstalledSettingsKeyReadback(t *testing.T, client *http.Client, origin string, key *rsa.PrivateKey, apiKey, subMerchant, application string, want int) {
	t.Helper()
	plain, _ := json.Marshal(map[string]string{"mchid": "service-fixture", "sub_mchid": subMerchant, "fapiao_apply_id": application})
	block, _ := aes.NewCipher([]byte(apiKey))
	gcm, _ := cipher.NewGCM(block)
	encrypted := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte("123456789012"), plain, []byte("invoice")))
	body, _ := json.Marshal(map[string]any{"id": "settings-key-fixture-" + application, "event_type": "FAPIAO.USER_APPLIED", "resource_type": "encrypt-resource", "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": encrypted, "nonce": "123456789012", "associated_data": "invoice"}})
	req, _ := http.NewRequest("POST", origin+"/api/callbacks/wechat-invoice", bytes.NewReader(body))
	ts, nonce := fmt.Sprint(time.Now().Unix()), "fixture-nonce"
	digest := sha256.Sum256([]byte(ts + "\n" + nonce + "\n" + string(body) + "\n"))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Wechatpay-Timestamp", ts)
	req.Header.Set("Wechatpay-Nonce", nonce)
	req.Header.Set("Wechatpay-Serial", "PUB_KEY_ID_fixture")
	req.Header.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(sig))
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != want {
		t.Fatal("published key actual callback readback mismatch", res.StatusCode, want)
	}
}
