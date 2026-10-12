package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"

	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

type installedProviderReadOverride struct {
	host    string
	handler http.Handler
	paths   map[string]bool
}

func TestInstalledDirectoryProviderReadBoundary(t *testing.T) {
	proxyURL, trust, reads, unexpected := installedSurveyProviderBoundary(t, installedProviderReadOverride{
		host: "qyapi.weixin.qq.com", handler: accessGovernanceDirectoryFixture(), paths: map[string]bool{"/cgi-bin/agent/get": true},
	})
	cert, err := os.ReadFile(trust)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid fixture certificate")
	}
	proxy, _ := url.Parse(proxyURL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	request, _ := http.NewRequest(http.MethodGet, "https://qyapi.weixin.qq.com/cgi-bin/agent/get", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || reads.Load() != 1 || unexpected.Load() != 0 {
		t.Fatal("controlled directory read did not reach loopback")
	}
	request, _ = http.NewRequest(http.MethodPost, "https://qyapi.weixin.qq.com/cgi-bin/agent/get", nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("directory boundary permitted a Provider write")
	}
	request, _ = http.NewRequest(http.MethodGet, "https://unrelated.invalid/cgi-bin/agent/get", nil)
	if response, err = client.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("directory boundary allowed another host")
	}
	if reads.Load() != 1 || unexpected.Load() != 2 {
		t.Fatal("directory boundary rejection counters are incomplete")
	}

	// Existing Survey callers still get their exact two read-only OAuth leaves.
	proxyURL, trust, reads, unexpected = installedSurveyProviderBoundary(t)
	cert, err = os.ReadFile(trust)
	if err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	roots.AppendCertsFromPEM(cert)
	proxy, _ = url.Parse(proxyURL)
	client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	for _, endpoint := range []string{"/sns/oauth2/access_token?code=public-survey-browser-code", "/sns/userinfo?access_token=public-survey-browser-token"} {
		response, err = client.Get("https://api.weixin.qq.com" + endpoint)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("existing Survey boundary changed")
		}
	}
	if reads.Load() != 2 || unexpected.Load() != 0 {
		t.Fatal("existing Survey read counters changed")
	}
}

// All browser/API requests execute the supplied binary and that package's Host.
// Source composition only prepares disposable fixtures. The synthetic TLS CA
// is process-scoped, and the existing CONNECT boundary permits directory GETs
// to loopback only. No real credentials, worker or Provider write are enabled.
func useInstalledStaffDirectoryFixture(t *testing.T, server *httptest.Server, databaseURL string, dataKey []byte) {
	t.Helper()
	input := testconfig.InstalledProduct()
	// The common artifact helper verifies binary SHA256, absolute paths and
	// manifest source SHA before this function can be reached.
	db, err := url.Parse(databaseURL)
	if err != nil || (db.Hostname() != "127.0.0.1" && db.Hostname() != "localhost" && db.Hostname() != "::1") || !strings.HasSuffix(db.Path, "_acceptance_test") {
		t.Fatal("installed directory requires an isolated loopback acceptance database")
	}
	providerProxy, trust, calls, unexpected := installedSurveyProviderBoundary(t, installedProviderReadOverride{
		host: "qyapi.weixin.qq.com", handler: accessGovernanceDirectoryFixture(),
		paths: map[string]bool{"/cgi-bin/gettoken": true, "/cgi-bin/agent/get": true, "/cgi-bin/department/simplelist": true, "/cgi-bin/user/simplelist": true, "/cgi-bin/user/get": true},
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	origin := "https://" + server.Listener.Addr().String()
	key := base64.RawStdEncoding.EncodeToString(dataKey)
	process := startPeriodicContactInstalledProcess(t, input.Binary, input.Root, []string{
		"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + input.SHA,
		"AICRM_PUBLIC_ORIGIN=" + origin, "AICRM_H5_PUBLIC_ORIGIN=" + origin,
		"AICRM_SURVEY_DATA_KEY=" + key, "AICRM_IDENTITY_PHONE_DATA_KEY=" + key,
		"AICRM_WECOM_ENABLED=true", "AICRM_WECOM_CORP_ID=access-ui-corp", "AICRM_WECOM_AGENT_ID=access-ui-agent",
		"AICRM_WECOM_SECRET=access-ui-secret", "AICRM_WECOM_CONTACT_SECRET=access-ui-contact", "AICRM_WECOM_CONTEXT_SIGNING_KEY=" + strings.Repeat("x", 32),
		"AICRM_GROUP_OPS_WEBHOOK_SECRET=staff-directory-installed-fixture-webhook-secret",
		"AICRM_EFFECTS_PROVIDER_ENABLED=false", "AICRM_OUTBOUND_PROVIDER_ENABLED=false", "AICRM_GROUP_OPS_PROVIDER_ENABLED=false",
		"HTTPS_PROXY=" + providerProxy, "HTTP_PROXY=" + providerProxy, "NO_PROXY=127.0.0.1,localhost,::1", "SSL_CERT_FILE=" + trust,
	})
	endpoint := "http://" + address
	waitDomesticReleaseReady(t, process, endpoint, input.SHA)
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) { director(r); r.Header.Set("X-Forwarded-Proto", "https") }
	proxy.Transport = &http.Transport{Proxy: nil}
	server.Config.Handler = proxy
	t.Cleanup(func() {
		if unexpected.Load() != 0 || calls.Load() < 4 {
			t.Errorf("installed directory controlled_reads=%d unexpected_provider_calls=%d", calls.Load(), unexpected.Load())
		}
		t.Logf("installed_staff_directory source_sha=%s binary_sha256=%s controlled_reads=%d unexpected_provider_calls=%d", input.SHA, input.BinarySHA256, calls.Load(), unexpected.Load())
	})
}
