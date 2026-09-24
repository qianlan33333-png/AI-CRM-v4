package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	alipaysdk "github.com/smartwalle/alipay/v3"
)

var domesticReleaseSmokeInputFile = flag.String(
	"domestic-release-smoke-input", "", "path to the fixed helper's private smoke input file",
)

type domesticReleaseSmokeInputs struct {
	StageRole             string `json:"stage_role"`
	SourceSHA             string `json:"source_sha"`
	InstalledSHA          string `json:"installed_sha"`
	InstalledBinary       string `json:"installed_binary"`
	InstalledBinarySHA256 string `json:"installed_binary_sha256"`
	DatabaseURL           string `json:"database_url"`
}

// TestDomesticReleaseInstalledAlipayCheckout is a stage-only contract. The
// fixed root helper supplies a private input file after it has verified the
// staging host, isolated database connection, installed manifest and binary.
// Ordinary CI may skip this separately owned stage check; a stage invocation
// with a missing input or pass marker is a hard failure.
func TestDomesticReleaseInstalledAlipayCheckout(t *testing.T) {
	if *domesticReleaseSmokeInputFile == "" {
		t.Skip("fixed staging release helper owns this installed-binary contract")
	}
	inputs, err := readDomesticReleaseSmokeInputs(*domesticReleaseSmokeInputFile)
	if err != nil {
		t.Fatalf("fixed staging smoke input is invalid: %T", err)
	}
	values := map[string]string{
		"AICRM_DOMESTIC_RELEASE_STAGE_ROLE":              inputs.StageRole,
		"AICRM_DOMESTIC_RELEASE_SOURCE_SHA":              inputs.SourceSHA,
		"AICRM_DOMESTIC_RELEASE_INSTALLED_SHA":           inputs.InstalledSHA,
		"AICRM_DOMESTIC_RELEASE_INSTALLED_BINARY":        inputs.InstalledBinary,
		"AICRM_DOMESTIC_RELEASE_INSTALLED_BINARY_SHA256": inputs.InstalledBinarySHA256,
		"AICRM_DATABASE_URL":                             inputs.DatabaseURL,
	}
	for name, value := range values {
		if value == "" {
			t.Fatalf("required staging smoke input is missing: %s", name)
		}
	}
	if values["AICRM_DOMESTIC_RELEASE_STAGE_ROLE"] != "staging" {
		t.Fatal("installed checkout smoke is restricted to the staging role")
	}
	for _, name := range []string{"AICRM_DOMESTIC_RELEASE_SOURCE_SHA", "AICRM_DOMESTIC_RELEASE_INSTALLED_SHA"} {
		if !validDomesticReleaseSHA(values[name]) {
			t.Fatalf("invalid staging smoke commit identity: %s", name)
		}
	}
	if len(values["AICRM_DOMESTIC_RELEASE_INSTALLED_BINARY_SHA256"]) != 64 {
		t.Fatal("invalid installed binary digest")
	}
	if _, err := hex.DecodeString(values["AICRM_DOMESTIC_RELEASE_INSTALLED_BINARY_SHA256"]); err != nil {
		t.Fatal("invalid installed binary digest")
	}
	installedBinary := filepath.Clean(values["AICRM_DOMESTIC_RELEASE_INSTALLED_BINARY"])
	if !filepath.IsAbs(installedBinary) || filepath.Base(installedBinary) != "aicrm" || strings.Contains(installedBinary, "..") {
		t.Fatal("installed binary path is unsafe")
	}
	releaseRoot := filepath.Dir(filepath.Dir(installedBinary))
	if filepath.Base(filepath.Dir(installedBinary)) != "bin" || filepath.Base(releaseRoot) != values["AICRM_DOMESTIC_RELEASE_INSTALLED_SHA"] || filepath.Dir(releaseRoot) != "/opt/aicrm/releases" {
		t.Fatal("installed binary is outside the exact immutable release directory")
	}
	info, err := os.Lstat(installedBinary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Fatal("installed API binary is unavailable")
	}
	content, err := os.Open(installedBinary)
	if err != nil {
		t.Fatal("read installed API binary")
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, content); err != nil {
		_ = content.Close()
		t.Fatal("hash installed API binary")
	}
	if err = content.Close(); err != nil {
		t.Fatal("close installed API binary")
	}
	if hex.EncodeToString(digest.Sum(nil)) != values["AICRM_DOMESTIC_RELEASE_INSTALLED_BINARY_SHA256"] {
		t.Fatal("installed API binary digest mismatch")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	// The helper has already checked the host identity and PostgreSQL 16
	// loopback service. This helper creates a random schema and removes only
	// that schema during cleanup; the configured database is never reset.
	databaseURL, cleanupDatabase := adminAccessCompositionDatabaseForURL(t, ctx, inputs.DatabaseURL)
	t.Cleanup(cleanupDatabase)

	t.Chdir(releaseRoot)
	h5Origin := "https://alipay-h5.example.test"
	dataKey := domesticSmokeDataKey(t)
	wechatPrivateKeyPath, wechatPlatformCertPath := distributionFixturePaymentCredentials(t)
	alipayPrivateKeyPath, alipayPublicKey := virtualAlipayFixtureCredentials(t)
	provider, providerCalls := domesticSmokeAlipayGateway(t, alipayPrivateKeyPath)
	t.Cleanup(provider.Close)
	providerCertificate := filepath.Join(t.TempDir(), "synthetic-alipay-provider.pem")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw})
	if err = initDomesticSmokeCertificate(certificatePEM); err != nil {
		t.Fatal("synthetic provider certificate is invalid")
	}
	if err = os.WriteFile(providerCertificate, certificatePEM, 0600); err != nil {
		t.Fatal("write synthetic provider trust certificate")
	}
	fixtureRuntime := domesticSmokeRuntime(
		databaseURL, values["AICRM_DOMESTIC_RELEASE_SOURCE_SHA"], h5Origin, dataKey,
		wechatPrivateKeyPath, wechatPlatformCertPath, alipayPrivateKeyPath, alipayPublicKey, provider.URL+"/gateway.do",
	)
	gateway, err := validateDomesticSmokeAlipayGateway(fixtureRuntime.Alipay.Gateway)
	if err != nil {
		t.Fatal("synthetic Alipay gateway must be an HTTPS loopback fixture")
	}
	alipayURLVerifier, err := newDomesticSmokeAlipayURLVerifier(alipayPublicKey)
	if err != nil {
		t.Fatal("create synthetic Alipay URL verifier")
	}
	fixtureApplication, err := compose(ctx, fixtureRuntime)
	if err != nil {
		t.Fatalf("compose synthetic checkout fixture: %T", err)
	}
	t.Cleanup(fixtureApplication.Close)
	fixture := &productExternalPushChromiumFixture{ctx: ctx, application: fixtureApplication}
	productID := seedAlipayPageCheckoutProduct(t, fixture)

	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve installed API loopback address")
	}
	listenAddress := listen.Addr().String()
	if err = listen.Close(); err != nil {
		t.Fatal("release installed API loopback address")
	}
	installedSHA := values["AICRM_DOMESTIC_RELEASE_INSTALLED_SHA"]
	apiEnvironment := []string{
		"AICRM_DATABASE_URL=" + databaseURL,
		"AICRM_ROLE=api",
		"AICRM_RELEASE_SHA=" + installedSHA,
		"AICRM_LISTEN_ADDR=" + listenAddress,
		"AICRM_PUBLIC_ORIGIN=https://crm.example.test",
		"AICRM_H5_PUBLIC_ORIGIN=" + h5Origin,
		"AICRM_SURVEY_DATA_KEY=" + dataKey,
		"AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey,
		"AICRM_SURVEY_OAUTH_ENABLED=true",
		"AICRM_SURVEY_OAUTH_APP_ID=" + fixtureRuntime.Survey.OAuthAppID,
		"AICRM_SURVEY_OAUTH_SECRET=" + fixtureRuntime.Survey.OAuthSecret,
		"AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID=" + fixtureRuntime.Survey.OAuthOpenPlatformID,
		"AICRM_SURVEY_OAUTH_SCOPE=" + fixtureRuntime.Survey.OAuthScope,
		"AICRM_WECHAT_PAY_PROVIDER_ENABLED=true",
		"AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true",
		"AICRM_WECHAT_PAY_APP_ID=" + fixtureRuntime.WeChatPay.AppID,
		"AICRM_WECHAT_PAY_APP_SECRET=" + fixtureRuntime.WeChatPay.AppSecret,
		"AICRM_WECHAT_PAY_APP_SCOPE=" + fixtureRuntime.WeChatPay.AppScope,
		"AICRM_WECHAT_PAY_H5_APP_ID=" + fixtureRuntime.WeChatPay.H5AppID,
		"AICRM_WECHAT_PAY_H5_APP_SECRET=" + fixtureRuntime.WeChatPay.H5AppSecret,
		"AICRM_WECHAT_PAY_H5_APP_SCOPE=" + fixtureRuntime.WeChatPay.H5AppScope,
		"AICRM_ORDER_CONTACT_DATA_KEY=" + fixtureRuntime.WeChatPay.OrderContactDataKey,
		"AICRM_WECHAT_PAY_MERCHANT_ID=" + fixtureRuntime.WeChatPay.MerchantID,
		"AICRM_WECHAT_PAY_MERCHANT_SERIAL=" + fixtureRuntime.WeChatPay.MerchantSerial,
		"AICRM_WECHAT_PAY_PRIVATE_KEY_PATH=" + fixtureRuntime.WeChatPay.PrivateKeyPath,
		"AICRM_WECHAT_PAY_PLATFORM_CERT_PATH=" + fixtureRuntime.WeChatPay.PlatformCertPath,
		"AICRM_WECHAT_PAY_API_V3_KEY=" + fixtureRuntime.WeChatPay.APIV3Key,
		"AICRM_ALIPAY_PROVIDER_ENABLED=true",
		"AICRM_ALIPAY_PRODUCTION=false",
		"AICRM_ALIPAY_APP_ID=" + fixtureRuntime.Alipay.AppID,
		"AICRM_ALIPAY_PRIVATE_KEY_PATH=" + fixtureRuntime.Alipay.PrivateKeyPath,
		"AICRM_ALIPAY_PUBLIC_KEY=" + fixtureRuntime.Alipay.AlipayPublicKey,
		"AICRM_ALIPAY_GATEWAY=" + fixtureRuntime.Alipay.Gateway,
		"AICRM_ALIPAY_NOTIFY_URL=https://crm.example.test/api/public/alipay/callback",
		"AICRM_ALIPAY_RETURN_URL=https://crm.example.test/pay/result",
		"SSL_CERT_FILE=" + providerCertificate,
		"AICRM_OUTBOUND_PROVIDER_ENABLED=false",
	}
	api := startDomesticSmokeProcess(t, installedBinary, releaseRoot, apiEnvironment)
	apiURL := "http://" + listenAddress
	waitDomesticReleaseReady(t, api, apiURL, installedSHA)

	type checkoutCase struct {
		name    string
		channel paymentdomain.Channel
	}
	cases := []checkoutCase{
		{name: "wap", channel: paymentdomain.ChannelAlipayWap},
		{name: "page", channel: paymentdomain.ChannelAlipayPage},
	}
	created := make(map[string]alipayCheckoutCreateResult, len(cases))
	tokens := make(map[string]string, len(cases))
	for _, testCase := range cases {
		session := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "domestic-release-smoke-"+testCase.name+"-"+values["AICRM_DOMESTIC_RELEASE_SOURCE_SHA"][:12])
		tokens[testCase.name] = session.token
		binding := domesticSmokeCheckoutBinding(t, apiURL, session.token)
		created[testCase.name] = domesticSmokeCreateAlipayCheckout(t, apiURL, session.token, binding, productID, testCase.channel, "domestic-release-"+testCase.name+"-"+values["AICRM_DOMESTIC_RELEASE_SOURCE_SHA"][:12])
	}

	workerEnvironment := make([]string, 0, len(apiEnvironment))
	for _, value := range apiEnvironment {
		if strings.HasPrefix(value, "AICRM_ROLE=") {
			workerEnvironment = append(workerEnvironment, "AICRM_ROLE=effects-worker")
			continue
		}
		if strings.HasPrefix(value, "AICRM_LISTEN_ADDR=") {
			continue
		}
		workerEnvironment = append(workerEnvironment, value)
	}
	worker := startDomesticSmokeProcess(t, installedBinary, releaseRoot, workerEnvironment)
	for _, testCase := range cases {
		status := waitDomesticSmokeHandoff(t, worker, apiURL, tokens[testCase.name], created[testCase.name].MerchantOrder)
		method := "alipay.trade.wap.pay"
		if testCase.channel == paymentdomain.ChannelAlipayPage {
			method = "alipay.trade.page.pay"
		}
		parsed, parseErr := validateDomesticSmokeAlipayRedirect(status.Handoff.RedirectURL, gateway, method, fixtureRuntime.Alipay.AppID, created[testCase.name].MerchantOrder, "99.00")
		if parseErr != nil {
			t.Fatalf("installed synthetic %s checkout handoff is incomplete", testCase.name)
		}
		if err = verifyDomesticSmokeAlipayURLSignature(alipayURLVerifier, parsed.Query()); err != nil {
			t.Fatalf("installed synthetic %s checkout handoff signature is invalid", testCase.name)
		}
		assertAlipayCheckoutPersistence(t, fixture, created[testCase.name], testCase.channel)
	}
	// WAP/Page creation signs a URL locally. It must not make an API request
	// while producing the checkout handoff; the gateway above is a loopback
	// TLS fixture so this journey cannot contact a live Alipay endpoint.
	if calls := providerCalls.Load(); calls != 0 {
		t.Fatalf("installed WAP/Page handoff unexpectedly made %d HTTP calls to the synthetic Alipay API fixture", calls)
	}
	t.Logf("domestic_release_installed_alipay_checkout: PASS source_sha=%s installed_sha=%s", values["AICRM_DOMESTIC_RELEASE_SOURCE_SHA"], installedSHA)
}

func validateDomesticSmokeAlipayGateway(raw string) (*url.URL, error) {
	gateway, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse synthetic Alipay gateway: %w", err)
	}
	address := net.ParseIP(gateway.Hostname())
	if gateway.Scheme != "https" || gateway.User != nil || address == nil || !address.IsLoopback() || gateway.Port() == "" || gateway.Path != "/gateway.do" || gateway.RawQuery != "" || gateway.Fragment != "" {
		return nil, fmt.Errorf("synthetic Alipay gateway must be an HTTPS loopback /gateway.do endpoint")
	}
	return gateway, nil
}

func newDomesticSmokeAlipayURLVerifier(publicKeyPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("decode synthetic Alipay public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse synthetic Alipay public key: %w", err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("synthetic Alipay public key is not RSA")
	}
	return publicKey, nil
}

func verifyDomesticSmokeAlipayURLSignature(publicKey *rsa.PublicKey, values url.Values) error {
	signature, err := base64.StdEncoding.DecodeString(values.Get("sign"))
	if err != nil {
		return fmt.Errorf("decode Alipay URL signature: %w", err)
	}
	// Match the pinned SDK Encoder: sort full key=value pairs and omit only
	// sign. URLValues includes sign_type in its request signature, whereas the
	// SDK's Client.VerifySign is for provider responses and ignores sign_type.
	pairs := make([]string, 0, len(values))
	for key, entries := range values {
		if key == "sign" {
			continue
		}
		for _, value := range entries {
			pairs = append(pairs, key+"="+value)
		}
	}
	sort.Strings(pairs)
	digest := sha256.Sum256([]byte(strings.Join(pairs, "&")))
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature)
}

func validateDomesticSmokeAlipayRedirect(raw string, gateway *url.URL, method, appID, merchantOrder, totalAmount string) (*url.URL, error) {
	redirect, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse handoff URL: %w", err)
	}
	query := redirect.Query()
	if redirect.Scheme != gateway.Scheme || redirect.User != nil || redirect.Host != gateway.Host || redirect.Path != gateway.Path || redirect.Fragment != "" ||
		query.Get("method") != method || query.Get("app_id") != appID || query.Get("sign_type") != "RSA2" || query.Get("sign") == "" {
		return nil, fmt.Errorf("handoff origin or signed Alipay method is invalid")
	}
	var content struct {
		OutTradeNo  string `json:"out_trade_no"`
		TotalAmount string `json:"total_amount"`
	}
	if err = json.Unmarshal([]byte(query.Get("biz_content")), &content); err != nil || content.OutTradeNo != merchantOrder || content.TotalAmount != totalAmount {
		return nil, fmt.Errorf("handoff order or amount does not match")
	}
	return redirect, nil
}

func TestValidateDomesticSmokeAlipayGatewayRequiresLoopbackTLS(t *testing.T) {
	if _, err := validateDomesticSmokeAlipayGateway("https://127.0.0.1:12345/gateway.do"); err != nil {
		t.Fatalf("valid loopback gateway rejected: %v", err)
	}
	for _, raw := range []string{
		"http://127.0.0.1:12345/gateway.do",
		"https://203.0.113.10:12345/gateway.do",
		"https://alipay.example:12345/gateway.do",
		"https://127.0.0.1:12345/other",
	} {
		if _, err := validateDomesticSmokeAlipayGateway(raw); err == nil {
			t.Errorf("unsafe synthetic gateway accepted: %s", raw)
		}
	}
}

func TestValidateDomesticSmokeAlipayRedirectContract(t *testing.T) {
	gateway, err := validateDomesticSmokeAlipayGateway("https://127.0.0.1:12345/gateway.do")
	if err != nil {
		t.Fatal("valid loopback gateway rejected")
	}
	for _, method := range []string{"alipay.trade.wap.pay", "alipay.trade.page.pay"} {
		content, marshalErr := json.Marshal(map[string]string{"out_trade_no": "merchant-test-1", "total_amount": "99.00"})
		if marshalErr != nil {
			t.Fatal("marshal synthetic Alipay order")
		}
		query := url.Values{
			"method": {method}, "app_id": {"virtual-alipay-test-app"}, "sign_type": {"RSA2"}, "sign": {"synthetic-signature"}, "biz_content": {string(content)},
		}
		redirect := (&url.URL{Scheme: gateway.Scheme, Host: gateway.Host, Path: gateway.Path, RawQuery: query.Encode()}).String()
		if _, err = validateDomesticSmokeAlipayRedirect(redirect, gateway, method, "virtual-alipay-test-app", "merchant-test-1", "99.00"); err != nil {
			t.Errorf("valid %s handoff rejected: %v", method, err)
		}
		query.Set("sign", "")
		unsigned := (&url.URL{Scheme: gateway.Scheme, Host: gateway.Host, Path: gateway.Path, RawQuery: query.Encode()}).String()
		if _, err = validateDomesticSmokeAlipayRedirect(unsigned, gateway, method, "virtual-alipay-test-app", "merchant-test-1", "99.00"); err == nil {
			t.Errorf("unsigned %s handoff accepted", method)
		}
	}
}

func TestDomesticSmokeGeneratedAlipayURLSignatures(t *testing.T) {
	privateKeyPath, publicKeyPEM := virtualAlipayFixtureCredentials(t)
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatal("read ephemeral synthetic Alipay private key")
	}
	const gatewayURL = "https://127.0.0.1:12345/gateway.do"
	provider, err := paymentprovider.NewAlipay(paymentprovider.AlipayConfig{
		Enabled: true, Production: false, AppID: "virtual-alipay-test-app",
		PrivateKey: string(privateKey), AlipayPublicKey: publicKeyPEM, Gateway: gatewayURL,
		NotifyURL: "https://crm.example.test/api/public/alipay/callback",
		ReturnURL: "https://crm.example.test/pay/result",
	})
	if err != nil {
		t.Fatal("create synthetic Alipay checkout provider")
	}
	verifier, err := newDomesticSmokeAlipayURLVerifier(publicKeyPEM)
	if err != nil {
		t.Fatal("create synthetic Alipay URL verifier")
	}
	gateway, err := validateDomesticSmokeAlipayGateway(gatewayURL)
	if err != nil {
		t.Fatal("validate synthetic Alipay loopback gateway")
	}

	request := paymentprovider.WebPayRequest{
		MerchantOrderNo: "merchant-generated-url-test",
		Subject:         "Synthetic checkout",
		TotalAmount:     "99.00",
	}
	for _, testCase := range []struct {
		name   string
		method string
		build  func(context.Context, paymentprovider.WebPayRequest) (string, error)
	}{
		{name: "wap", method: "alipay.trade.wap.pay", build: provider.BuildWapPay},
		{name: "page", method: "alipay.trade.page.pay", build: provider.BuildPagePay},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rawURL, buildErr := testCase.build(context.Background(), request)
			if buildErr != nil {
				t.Fatal("build synthetic Alipay checkout URL")
			}
			redirect, validateErr := validateDomesticSmokeAlipayRedirect(rawURL, gateway, testCase.method, "virtual-alipay-test-app", request.MerchantOrderNo, request.TotalAmount)
			if validateErr != nil {
				t.Fatalf("generated %s URL violates the loopback checkout contract: %v", testCase.name, validateErr)
			}
			query := redirect.Query()
			if verifyErr := verifyDomesticSmokeAlipayURLSignature(verifier, query); verifyErr != nil {
				t.Fatalf("generated %s URL signature did not verify: %v", testCase.name, verifyErr)
			}

			tampered := make(url.Values, len(query))
			for key, entries := range query {
				tampered[key] = append([]string(nil), entries...)
			}
			tampered.Set("sign_type", "RSA")
			if verifyErr := verifyDomesticSmokeAlipayURLSignature(verifier, tampered); verifyErr == nil {
				t.Fatalf("tampered generated %s URL signature was accepted", testCase.name)
			}
		})
	}
}

func readDomesticReleaseSmokeInputs(path string) (domesticReleaseSmokeInputs, error) {
	var inputs domesticReleaseSmokeInputs
	if !filepath.IsAbs(path) {
		return inputs, fmt.Errorf("smoke input path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return inputs, err
	}
	if !info.Mode().IsRegular() || info.Mode()&0077 != 0 {
		return inputs, fmt.Errorf("smoke input file is not a private regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return inputs, err
	}
	if err = json.Unmarshal(content, &inputs); err != nil {
		return inputs, err
	}
	if inputs.StageRole == "" || inputs.SourceSHA == "" || inputs.InstalledSHA == "" ||
		inputs.InstalledBinary == "" || inputs.InstalledBinarySHA256 == "" || inputs.DatabaseURL == "" {
		return inputs, fmt.Errorf("smoke input file is missing a required value")
	}
	return inputs, nil
}

func TestReadDomesticReleaseSmokeInputsRejectsMissingDatabaseURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smoke-input.json")
	content := `{"stage_role":"staging","source_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installed_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","installed_binary":"/opt/aicrm/releases/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/bin/aicrm","installed_binary_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal("write smoke parser fixture")
	}
	if _, err := readDomesticReleaseSmokeInputs(path); err == nil {
		t.Fatal("a nonempty smoke input path without the database URL must fail")
	}
}

func validDomesticReleaseSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func domesticSmokeDataKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal("generate synthetic fixture data key")
	}
	return base64.RawStdEncoding.EncodeToString(key)
}

func domesticSmokeAPIV3Key(dataKey string) string {
	// The provider credential is only used by the isolated smoke runtime. Derive
	// a fresh, correctly sized synthetic value from this run's random fixture
	// key rather than keeping a key-shaped literal in source history.
	digest := sha256.Sum256([]byte("domestic-release-smoke/wechat-api-v3\x00" + dataKey))
	return hex.EncodeToString(digest[:16])
}

func TestDomesticSmokeAPIV3KeyIsFreshAndSized(t *testing.T) {
	first := domesticSmokeAPIV3Key("first-per-run-data-key")
	second := domesticSmokeAPIV3Key("second-per-run-data-key")
	if len(first) != 32 || len(second) != 32 || first == second {
		t.Fatal("synthetic API v3 key must be a distinct 32-character value for each fixture key")
	}
}

func domesticSmokeRuntime(
	databaseURL, releaseSHA, h5Origin, dataKey string,
	wechatPrivateKeyPath, wechatPlatformCertPath, alipayPrivateKeyPath, alipayPublicKey, alipayGateway string,
) platformconfig.Runtime {
	const (
		wechatAppID       = "wx-domestic-release-smoke-mini"
		wechatH5AppID     = "wx-domestic-release-smoke-h5"
		openPlatformID    = "domestic-release-smoke-platform"
		alipayAppID       = "virtual-alipay-test-app"
		alipayNotifyURL   = "https://crm.example.test/api/public/alipay/callback"
		alipayReturnURL   = "https://crm.example.test/pay/result"
		wechatH5AppSecret = "domestic-release-synthetic-h5-secret"
	)
	return platformconfig.Runtime{
		ListenAddress:  "127.0.0.1:0",
		ReleaseSHA:     releaseSHA,
		Role:           platformconfig.RoleAPI,
		DatabaseURL:    databaseURL,
		PublicOrigin:   "https://crm.example.test",
		H5PublicOrigin: h5Origin,
		WorkerOwner:    "domestic-release-installed-smoke",
		WorkerLimit:    1,
		Survey: platformconfig.Survey{
			DataKey: dataKey, IdentityPhoneDataKey: dataKey,
			OAuthEnabled: true, OAuthAppID: wechatH5AppID, OAuthSecret: wechatH5AppSecret,
			OAuthOpenPlatformID: openPlatformID, OAuthScope: "snsapi_userinfo",
		},
		WeChatPay: platformconfig.WeChatPay{
			Enabled: true, AppID: wechatAppID, AppSecret: "domestic-release-synthetic-mini-secret",
			AppScope:       "wechat-app:" + wechatAppID,
			H5OAuthEnabled: true, H5AppID: wechatH5AppID, H5AppSecret: wechatH5AppSecret,
			H5AppScope: "wechat-app:" + wechatH5AppID, OrderContactDataKey: dataKey,
			MerchantID: "domestic-release-synthetic-merchant", MerchantSerial: "domestic-release-synthetic-serial",
			PrivateKeyPath: wechatPrivateKeyPath, PlatformCertPath: wechatPlatformCertPath,
			APIV3Key: domesticSmokeAPIV3Key(dataKey),
		},
		Alipay: platformconfig.Alipay{
			Enabled: true, Production: false, AppID: alipayAppID,
			PrivateKeyPath: alipayPrivateKeyPath, AlipayPublicKey: alipayPublicKey,
			Gateway: alipayGateway, NotifyURL: alipayNotifyURL, ReturnURL: alipayReturnURL,
		},
	}
}

func domesticSmokeAlipayGateway(t *testing.T, privateKeyPath string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatal("read synthetic Alipay signing key")
	}
	signer, err := alipaysdk.New("virtual-alipay-test-app", string(privateKey), true)
	if err != nil {
		t.Fatal("create synthetic Alipay query signer")
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if err := request.ParseForm(); err != nil || request.Form.Get("method") != "alipay.trade.query" {
			http.Error(writer, "synthetic checkout fixture accepts query only", http.StatusBadRequest)
			return
		}
		var query struct {
			OutTradeNo string `json:"out_trade_no"`
		}
		if err := json.Unmarshal([]byte(request.Form.Get("biz_content")), &query); err != nil || query.OutTradeNo == "" {
			http.Error(writer, "synthetic checkout query is invalid", http.StatusBadRequest)
			return
		}
		body, err := json.Marshal(map[string]string{
			"code": "10000", "msg": "Success", "out_trade_no": query.OutTradeNo,
			"trade_status": "WAIT_BUYER_PAY", "total_amount": "99.00",
		})
		if err != nil {
			http.Error(writer, "synthetic checkout response is invalid", http.StatusInternalServerError)
			return
		}
		signature, err := signer.SignBytes(body)
		if err != nil {
			http.Error(writer, "synthetic checkout response signature failed", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"alipay_trade_query_response":%s,"sign":%q}`, body, base64.StdEncoding.EncodeToString(signature))
	}))
	return server, &calls
}

type domesticSmokeProcess struct {
	command  *exec.Cmd
	done     chan error
	finished bool
}

func startDomesticSmokeProcess(t *testing.T, binary, workingDirectory string, environment []string) *domesticSmokeProcess {
	t.Helper()
	command := exec.Command(binary)
	command.Dir = workingDirectory
	command.Env = append([]string(nil), environment...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatalf("start installed %s process: %T", filepath.Base(binary), err)
	}
	process := &domesticSmokeProcess{command: command, done: make(chan error, 1)}
	go func() { process.done <- command.Wait() }()
	t.Cleanup(func() {
		if !process.finished {
			_ = command.Process.Signal(os.Interrupt)
			select {
			case processErr := <-process.done:
				process.finished = true
				if processErr != nil {
					t.Errorf("installed %s process stopped with %T", filepath.Base(binary), processErr)
				}
			case <-time.After(8 * time.Second):
				_ = command.Process.Kill()
				<-process.done
				process.finished = true
				t.Errorf("installed %s process did not stop after interrupt", filepath.Base(binary))
			}
		}
	})
	return process
}

func waitDomesticReleaseReady(t *testing.T, process *domesticSmokeProcess, baseURL, installedSHA string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	lastHTTPStatus := "no response"
	lastStatus := "unavailable"
	lastReleaseSHA := "unavailable"
	for time.Now().Before(deadline) {
		select {
		case err := <-process.done:
			process.finished = true
			t.Fatalf("installed API exited before readiness: %T", err)
		default:
		}
		response, err := client.Get(baseURL + "/readyz")
		if err != nil {
			lastHTTPStatus = "no response"
			lastStatus = "unavailable"
			lastReleaseSHA = "unavailable"
			time.Sleep(200 * time.Millisecond)
			continue
		}
		lastHTTPStatus = fmt.Sprintf("%d", response.StatusCode)
		var body struct {
			Status     string `json:"status"`
			ReleaseSHA string `json:"release_sha"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body)
		_ = response.Body.Close()
		if decodeErr == nil {
			switch body.Status {
			case "ready", "not_ready":
				lastStatus = body.Status
			default:
				lastStatus = "unavailable"
			}
			if validDomesticReleaseSHA(body.ReleaseSHA) {
				lastReleaseSHA = body.ReleaseSHA
			} else {
				lastReleaseSHA = "unavailable"
			}
		} else {
			lastStatus = "unavailable"
			lastReleaseSHA = "unavailable"
		}
		if decodeErr == nil && response.StatusCode == http.StatusOK && body.Status == "ready" && body.ReleaseSHA == installedSHA {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("installed API did not report ready: last_http_status=%q last_status=%q last_release_sha=%q", lastHTTPStatus, lastStatus, lastReleaseSHA)
}

func domesticSmokeCheckoutBinding(t *testing.T, baseURL, token string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/wechat-pay/checkout-session", nil)
	if err != nil {
		t.Fatal("build installed checkout binding request")
	}
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("installed checkout binding request failed: %T", err)
	}
	defer response.Body.Close()
	var body struct {
		Binding string `json:"checkout_session_binding"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&body) != nil || body.Binding == "" {
		t.Fatal("installed checkout binding response is invalid")
	}
	return body.Binding
}

func domesticSmokeCreateAlipayCheckout(t *testing.T, baseURL, token, binding string, productID int64, channel paymentdomain.Channel, idempotencyKey string) alipayCheckoutCreateResult {
	t.Helper()
	body := fmt.Sprintf(`{"product_id":%d,"product_kind":"standard","provider":"alipay","channel":%q,"beneficiary_selection":"payer_self","checkout_session_binding":%q}`, productID, channel, binding)
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/alipay/checkouts", strings.NewReader(body))
	if err != nil {
		t.Fatal("build installed Alipay checkout request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("installed Alipay checkout request failed: %T", err)
	}
	defer response.Body.Close()
	var created alipayCheckoutCreateResult
	if response.StatusCode != http.StatusAccepted || json.NewDecoder(response.Body).Decode(&created) != nil || created.OrderID < 1 || created.PaymentID < 1 || created.MerchantOrder == "" {
		t.Fatalf("installed Alipay %s checkout response is invalid", channel)
	}
	return created
}

func waitDomesticSmokeHandoff(t *testing.T, worker *domesticSmokeProcess, baseURL, token, merchantOrder string) alipayCheckoutStatusResult {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-worker.done:
			worker.finished = true
			t.Fatalf("installed effects worker exited before checkout handoff: %T", err)
		default:
		}
		request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/alipay/checkouts/"+url.PathEscape(merchantOrder), nil)
		if err != nil {
			t.Fatal("build installed Alipay readback request")
		}
		request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
		response, requestErr := client.Do(request)
		if requestErr == nil {
			var status alipayCheckoutStatusResult
			decodeErr := json.NewDecoder(response.Body).Decode(&status)
			_ = response.Body.Close()
			if (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusAccepted) && decodeErr == nil && status.Provider == paymentdomain.ProviderAlipay && status.MerchantOrder == merchantOrder && status.Ready && status.Handoff.RedirectURL != "" {
				return status
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("installed checkout did not produce a synthetic provider handoff")
	return alipayCheckoutStatusResult{}
}

// Keep x509 imported as an explicit check that the local TLS fixture contains
// a parseable certificate before the child process trusts it.
func initDomesticSmokeCertificate(certificatePEM []byte) error {
	block, _ := pem.Decode(certificatePEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("synthetic provider certificate is invalid")
	}
	_, err := x509.ParseCertificate(block.Bytes)
	return err
}
