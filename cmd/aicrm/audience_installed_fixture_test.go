package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

func validateInstalledAudienceArtifacts(t *testing.T, input testconfig.InstalledSurveyInputs) {
	t.Helper()
	content, err := os.ReadFile(input.Binary)
	digest := sha256.Sum256(content)
	manifestBytes, manifestErr := os.ReadFile(filepath.Join(input.Root, "web", "dist", "asset-manifest.json"))
	var manifest struct {
		SourceSHA string            `json:"source_sha"`
		Entries   map[string]string `json:"entries"`
	}
	if err != nil || !filepath.IsAbs(input.Binary) || !filepath.IsAbs(input.Root) || !validDomesticReleaseSHA(input.SHA) || hex.EncodeToString(digest[:]) != input.BinarySHA256 || manifestErr != nil || json.Unmarshal(manifestBytes, &manifest) != nil || manifest.SourceSHA != input.SHA {
		t.Fatal("installed Audience binary/Host binding mismatch")
	}
	for _, entry := range []string{"groupopsHost", "groupopsStyles", "confirmationDialogHost", "confirmationDialogStyles"} {
		if manifest.Entries[entry] == "" {
			t.Fatalf("installed Audience Host lacks %s", entry)
		}
	}
	t.Log("reusing installed Audience Host; source Host/npm preparation omitted")
}

// Source composition only seeds the isolated database and supplies readback.
// Every browser page/API/asset uses this package process through the TLS proxy;
// it never falls back to the source handler. Only the API role is started.
func useInstalledAudienceFixture(t *testing.T, f *audienceConfirmationChromiumFixture, input testconfig.InstalledSurveyInputs) {
	t.Helper()
	if input.Binary == "" {
		return
	}
	databaseURL := f.application.pool.Native().Config().ConnString()
	db, err := url.Parse(databaseURL)
	if err != nil || (db.Hostname() != "localhost" && db.Hostname() != "127.0.0.1" && db.Hostname() != "::1") || !strings.HasPrefix(strings.TrimPrefix(db.Path, "/"), "aicrm_test_") {
		t.Fatal("installed Audience requires isolated loopback aicrm_test_ PostgreSQL")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve installed Audience listener")
	}
	address := listener.Addr().String()
	_ = listener.Close()
	dataKey := base64.RawStdEncoding.EncodeToString(f.dataKey)
	// Do not inherit production configuration, credentials, proxies or workers.
	environment := []string{
		"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + input.SHA,
		"AICRM_PUBLIC_ORIGIN=" + f.server.URL, "AICRM_H5_PUBLIC_ORIGIN=" + f.server.URL,
		"AICRM_SURVEY_DATA_KEY=" + dataKey, "AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey,
		"AICRM_GROUP_OPS_WEBHOOK_SECRET=installed-audience-browser-webhook-secret", "AICRM_AI_ASSISTANT_UI_ENABLED=true",
		"AICRM_OUTBOUND_PROVIDER_ENABLED=false", "AICRM_GROUP_OPS_PROVIDER_ENABLED=false", "AICRM_AI_ASSISTANT_DISPATCH_ENABLED=false",
		"AICRM_WECHAT_PAY_PROVIDER_ENABLED=false", "AICRM_ALIPAY_PROVIDER_ENABLED=false", "AICRM_SURVEY_COMPLETION_PROVIDER_ENABLED=false", "AICRM_COMMERCE_PUSH_PROVIDER_ENABLED=false",
	}
	process := startPeriodicContactInstalledProcess(t, input.Binary, input.Root, environment)
	endpoint := "http://" + address
	waitInstalledSurveyReady(t, process, endpoint, input.SHA)
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := &http.Transport{Proxy: nil}
	proxy.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	f.server.Config.Handler = proxy
	t.Logf("installed_audience source_sha=%s binary_sha256=%s HTTPS=true provider_enabled=false effects_worker=false", input.SHA, input.BinarySHA256)
}
