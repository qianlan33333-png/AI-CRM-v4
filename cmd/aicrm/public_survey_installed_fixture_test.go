package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Optional installed mode runs the same all/one recovery assertions against
// immutable app bytes and that package's Host. Only fixture seeding/readback
// uses source stores; every browser/API operation is reverse-proxied to the
// supplied process. No production environment or credentials are inherited.
func useInstalledPublicSurveyFixture(t *testing.T, fixture *publicSurveyPresentationChromiumFixture, databaseURL string) {
	t.Helper()
	inputs := testconfig.InstalledSurvey()
	binary := inputs.Binary
	if binary == "" {
		return
	}
	root, head, digest := inputs.Root, inputs.SHA, inputs.BinarySHA256
	if !filepath.IsAbs(binary) || !filepath.IsAbs(root) || !validDomesticReleaseSHA(head) || len(digest) != 64 {
		t.Fatal("invalid installed Survey inputs")
	}
	provenance, err := validateInstalledSurveyPackage(inputs, installedSurveyWebTree)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("installed_survey_provenance app_sha=%s binary_sha256=%s host_sha=%s host_manifest_sha256=%s host_asset_sha256=%s app_web_tree=%s host_web_tree=%s", head, digest, provenance.HostSHA, provenance.ManifestSHA256, provenance.AssetSHA256, provenance.AppWebTree, provenance.HostWebTree)
	db, err := url.Parse(databaseURL)
	if err != nil || db.Hostname() != "localhost" && db.Hostname() != "127.0.0.1" && db.Hostname() != "::1" {
		t.Fatal("installed Survey requires isolated loopback PostgreSQL")
	}
	providerProxy, trust, calls, unexpected := installedSurveyProviderBoundary(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve installed Survey listener")
	}
	address := listener.Addr().String()
	_ = listener.Close()
	dataKey := base64.RawStdEncoding.EncodeToString(make([]byte, 32)) // synthetic fixture cipher only
	process := startDomesticSmokeProcess(t, binary, root, []string{
		"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + head,
		"AICRM_PUBLIC_ORIGIN=" + fixture.browserURL, "AICRM_H5_PUBLIC_ORIGIN=" + fixture.browserURL,
		"AICRM_SURVEY_DATA_KEY=" + dataKey, "AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey,
		"AICRM_SURVEY_OAUTH_ENABLED=true", "AICRM_SURVEY_OAUTH_APP_ID=public-survey-browser-app",
		"AICRM_SURVEY_OAUTH_SECRET=public-survey-browser-secret", "AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID=public-survey-browser-platform", "AICRM_SURVEY_OAUTH_SCOPE=snsapi_userinfo",
		"AICRM_OPEN_PLATFORM_JWT_SIGNING_KEY=01234567890123456789012345678901", "AICRM_OPEN_PLATFORM_TRUSTED_PROXY_CIDRS=127.0.0.1/32", "AICRM_WECOM_CORP_ID=installed-health-corp",
		"AICRM_OUTBOUND_PROVIDER_ENABLED=false", "AICRM_SURVEY_COMPLETION_PROVIDER_ENABLED=false",
		"HTTPS_PROXY=" + providerProxy, "NO_PROXY=127.0.0.1,localhost,::1", "SSL_CERT_FILE=" + trust,
	})
	endpoint := "http://" + address
	waitInstalledSurveyReady(t, process, endpoint, head)
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) { director(r); r.Header.Set("X-Forwarded-Proto", "https") }
	proxy.Transport = &http.Transport{Proxy: nil} // only loopback, never a real Provider
	fixture.server.Config.Handler = proxy
	expectedOAuthReads := int32(0)
	if strings.Contains(t.Name(), "ReauthorizationChromiumJourney") {
		expectedOAuthReads = 2 // denial does no exchange; acceptance reads token and userinfo
	}
	t.Cleanup(func() {
		if unexpected.Load() != 0 {
			t.Errorf("installed Survey reached %d forbidden Provider requests", unexpected.Load())
		}
		if calls.Load() != expectedOAuthReads {
			t.Errorf("installed Survey controlled OAuth reads=%d want=%d", calls.Load(), expectedOAuthReads)
		}
		t.Logf("installed_survey binary_sha256=%s source_sha=%s controlled_oauth_reads=%d unexpected_provider_calls=%d", digest, head, calls.Load(), unexpected.Load())
	})
	installedSurveyCopyJourney(t, fixture)
}

func waitInstalledSurveyReady(t *testing.T, process *domesticSmokeProcess, endpoint, head string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	deadline := time.Now().Add(30 * time.Second)
	lastStatus := 0
	lastBodyStatus := ""
	lastMatches := false
	lastDecode := false
	lastError := ""
	for time.Now().Before(deadline) {
		select {
		case <-process.done:
			process.finished = true
			t.Fatal("installed Survey exited before readiness")
		default:
		}
		response, err := client.Get(endpoint + "/readyz")
		if err != nil {
			lastError = fmt.Sprintf("%T", err)
		}
		if err == nil {
			lastStatus = response.StatusCode
			var body struct {
				Status string `json:"status"`
				SHA    string `json:"release_sha"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body)
			_ = response.Body.Close()
			lastBodyStatus = body.Status
			lastMatches = body.SHA == head
			lastDecode = decodeErr == nil
			if decodeErr == nil && response.StatusCode == 200 && body.Status == "ready" && body.SHA == head {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("installed Survey readiness: http=%d status=%q sha_matches=%t json_valid=%t error_type=%s", lastStatus, lastBodyStatus, lastMatches, lastDecode, lastError)
}

// The installed provider still uses HTTPS, ordinary certificate validation and
// its unmodified api.weixin.qq.com URL. Only this isolated process trusts the
// short-lived synthetic CA. CONNECT has a strict host allowlist and tunnels
// solely to a local TLS fixture, so no real token or external network is used.
func installedSurveyProviderBoundary(t *testing.T, overrides ...installedProviderReadOverride) (string, string, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	host := "api.weixin.qq.com"
	if len(overrides) > 1 {
		t.Fatal("one isolated Provider boundary is required")
	}
	if len(overrides) == 1 {
		if overrides[0].host != "qyapi.weixin.qq.com" || overrides[0].handler == nil {
			t.Fatal("invalid isolated directory Provider")
		}
		host = overrides[0].host
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Isolated Provider fixture"}, DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pair, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(t.TempDir(), "survey-fixture-ca.pem")
	if err = os.WriteFile(trust, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	calls, unexpected := &atomic.Int32{}, &atomic.Int32{}
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.Host != host {
			unexpected.Add(1)
			http.Error(w, "forbidden fixture request", 403)
			return
		}
		if len(overrides) == 1 {
			if !overrides[0].paths[r.URL.Path] {
				unexpected.Add(1)
				http.Error(w, "forbidden directory fixture path", 403)
				return
			}
			calls.Add(1)
			overrides[0].handler.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/sns/oauth2/access_token":
			if r.URL.Query().Get("code") != "public-survey-browser-code" {
				unexpected.Add(1)
				http.Error(w, "invalid fixture code", 400)
				return
			}
			calls.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"public-survey-browser-token","openid":"public-survey-browser-openid","scope":"snsapi_userinfo"}`))
		case "/sns/userinfo":
			if r.URL.Query().Get("access_token") != "public-survey-browser-token" {
				unexpected.Add(1)
				http.Error(w, "invalid fixture token", 400)
				return
			}
			calls.Add(1)
			_, _ = w.Write([]byte(`{"openid":"public-survey-browser-openid","unionid":"public-survey-browser-unionid"}`))
		default:
			unexpected.Add(1)
			http.Error(w, "forbidden fixture path", 403)
		}
	}))
	provider.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	provider.StartTLS()
	t.Cleanup(provider.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != host+":443" {
			unexpected.Add(1)
			http.Error(w, "forbidden fixture destination", 403)
			return
		}
		upstream, err := net.DialTimeout("tcp", provider.Listener.Addr().String(), 2*time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", 503)
			return
		}
		connection, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close() }()
		_, _ = io.Copy(connection, upstream)
		_ = connection.Close()
		_ = upstream.Close()
	}))
	t.Cleanup(proxy.Close)
	return proxy.URL, trust, calls, unexpected
}

// Authentication and copying go through the installed Access/Survey HTTP
// owners. The only setup write is a password for the disposable seeded admin.
func installedSurveyCopyJourney(t *testing.T, fixture *publicSurveyPresentationChromiumFixture) {
	t.Helper()
	password := "isolated-survey-admin-password"
	hash, err := (credential.PasswordHasher{}).Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.native.Exec(fixture.ctx, `UPDATE admin_users SET password_hash=$1 WHERE username='public-survey-browser'`, hash); err != nil {
		t.Fatal(err)
	}
	client := fixture.server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(fixture.server.URL + "/login")
	if err != nil {
		t.Fatal("installed admin login page")
	}
	_ = response.Body.Close()
	var loginCSRF *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == "aicrm_login_csrf" {
			loginCSRF = cookie
		}
	}
	if response.StatusCode != 200 || loginCSRF == nil {
		t.Fatalf("installed login page status=%d has_csrf=%t", response.StatusCode, loginCSRF != nil)
	}
	payload, _ := json.Marshal(map[string]string{"username": "public-survey-browser", "password": password, "login_csrf_token": loginCSRF.Value})
	request, _ := http.NewRequest(http.MethodPost, fixture.server.URL+"/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(loginCSRF)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal("installed admin login")
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("installed admin login status=%d", response.StatusCode)
	}
	cookies := response.Cookies()
	fixture.adminCookies = cookies
	csrf := ""
	for _, cookie := range cookies {
		if cookie.Name == "aicrm_admin_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("installed admin login omitted CSRF")
	}
	originalSlug := fixture.success.Slug
	sourceID := int64(fixture.success.ID)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		request, _ = http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/admin/questionnaires/%d/duplicate", fixture.server.URL, sourceID), nil)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		request.Header.Set("Idempotency-Key", fmt.Sprintf("installed-survey-copy-%d", i))
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response, err = client.Do(request)
		if err != nil {
			t.Fatal("installed questionnaire copy")
		}
		var body struct {
			Data struct {
				ID   int64  `json:"id"`
				Slug string `json:"slug"`
			} `json:"questionnaire"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&body)
		_ = response.Body.Close()
		if response.StatusCode != 200 || decodeErr != nil || body.Data.ID < 1 || len(body.Data.Slug) != 34 || !strings.HasPrefix(body.Data.Slug, "q-") || seen[body.Data.Slug] {
			t.Fatalf("installed copy status=%d valid_body=%t slug_length=%d", response.StatusCode, decodeErr == nil, len(body.Data.Slug))
		}
		seen[body.Data.Slug] = true
		sourceID = body.Data.ID
	}
	var retained string
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT slug FROM survey_questionnaires WHERE id=$1`, fixture.success.ID).Scan(&retained); err != nil || retained != originalSlug {
		t.Fatal("installed copying altered original link")
	}
	t.Log("installed_survey_copy: PASS recursive_fixed_length old_link_retained")
}
