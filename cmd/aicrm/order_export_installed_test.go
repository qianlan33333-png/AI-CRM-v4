package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	postgresurltestutil "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres/testutil"
	"github.com/xuri/excelize/v2"
)

// This contract has no source-handler fallback or successful skip. The
// package SHA256 and Host are verified before any acceptance request.
func TestPostgreSQLOrderExportSharingInstalled(t *testing.T) {
	if testconfig.InstalledProduct().Binary == "" {
		t.Fatal("order-export-sharing requires exact installed binary/Host inputs")
	}
	validateOrdersOAuthInstalledProduct(t, testconfig.InstalledProduct())
	testUnifiedOrderExportHTTPAndCLI(t, true)
}

func useInstalledOrderExportFixture(t *testing.T, server *httptest.Server, databaseURL, key string, overrides ...string) *domesticSmokeProcess {
	t.Helper()
	if !postgresurltestutil.IsLocalTestDatabaseURL(databaseURL) {
		t.Fatal("installed export requires the existing isolated loopback test database")
	}
	input := testconfig.InstalledProduct()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	origin := "https://" + server.Listener.Addr().String()
	environment := []string{
		"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + input.SHA,
		"AICRM_PUBLIC_ORIGIN=" + origin, "AICRM_H5_PUBLIC_ORIGIN=" + origin,
		"AICRM_SURVEY_DATA_KEY=" + key, "AICRM_IDENTITY_PHONE_DATA_KEY=" + key, "AICRM_ORDER_CONTACT_DATA_KEY=" + key,
		"AICRM_GROUP_OPS_WEBHOOK_SECRET=order-export-installed-fixture-webhook-secret",
		"AICRM_OUTBOUND_PROVIDER_ENABLED=false", "AICRM_COMMERCE_PUSH_PROVIDER_ENABLED=false", "AICRM_GROUP_OPS_PROVIDER_ENABLED=false",
		"AICRM_WECHAT_PAY_PROVIDER_ENABLED=false", "AICRM_ALIPAY_PROVIDER_ENABLED=false", "AICRM_WECHAT_SHOP_PROVIDER_ENABLED=false", "AICRM_INVOICE_PROVIDER_ENABLED=false",
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost,::1",
	}
	for _, override := range overrides {
		name, _, ok := strings.Cut(override, "=")
		if !ok {
			t.Fatal("invalid installed fixture environment override")
		}
		filtered := environment[:0]
		for _, entry := range environment {
			if !strings.HasPrefix(entry, name+"=") {
				filtered = append(filtered, entry)
			}
		}
		environment = append(filtered, override)
	}
	process := startPeriodicContactInstalledProcess(t, input.Binary, input.Root, environment)
	endpoint := "http://" + address
	waitInstalledSurveyReady(t, process, endpoint, input.SHA)
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil}
	t.Cleanup(proxy.Transport.(*http.Transport).CloseIdleConnections)
	server.Config.Handler = proxy
	return process
}

// Keep the existing request/login fixture helpers while making every request
// cross HTTPS. In installed mode the TLS server has only a reverse proxy.
func orderExportHTTPBridge(t *testing.T, server *httptest.Server) http.Handler {
	t.Helper()
	client := server.Client()
	client.Timeout = 10 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), r.Method, server.URL+r.URL.RequestURI(), r.Body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header = r.Header.Clone()
		if req.Header.Get("Origin") == "" && r.Method != http.MethodGet {
			req.Header.Set("Origin", server.URL)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("export HTTPS request failed", err)
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		if _, err = io.Copy(w, response.Body); err != nil {
			t.Fatal(err)
		}
	})
}

func assertOrderExportWorkbookContract(t *testing.T, f *excelize.File, headers []string) {
	t.Helper()
	want := []string{"线上订单号", "下单时间", "买家帐号", "买家昵称", "支付单号", "付款方式", "付款时间", "应付金额", "币种", "退款金额", "收货人姓名", "手机", "省份", "城市", "区县", "详细地址", "商品编码", "商品名称", "数量", "商品单价", "状态", "订单来源", "分销员编号", "分销员名称", "原平台状态", "原平台售后状态", "导入时间", "订单应收金额", "买家实付金额", "应分账金额", "已分账金额", "待分账金额", "分账追回金额", "商家应收净额（未扣手续费）", "商家实收净额（未扣手续费）", "分账状态"}
	if !reflect.DeepEqual(headers, want) {
		t.Fatal("export header order/36-column contract changed")
	}
	if note, _ := f.GetCellValue("金额口径", "B2"); !strings.Contains(note, "只在首条商品明细") {
		t.Fatal("export workbook amount definitions missing")
	}
}

func assertInstalledOrderExportHost(t *testing.T, handler http.Handler, session string) {
	t.Helper()
	input := testconfig.InstalledProduct()
	ready := automationFixedContentRequest(t, handler, "GET", "/readyz", nil, "", "", "")
	var state struct {
		Status string `json:"status"`
		SHA    string `json:"release_sha"`
	}
	if ready.Code != 200 || json.Unmarshal(ready.Body.Bytes(), &state) != nil || state.Status != "ready" || state.SHA != input.SHA {
		t.Fatal("export HTTPS route is not the supplied installed runtime")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(input.Root, "web", "dist", "asset-manifest.json"))
	var manifest struct {
		Entries map[string]string `json:"entries"`
	}
	if err != nil || json.Unmarshal(manifestBytes, &manifest) != nil {
		t.Fatal("installed export manifest unavailable")
	}
	entry := manifest.Entries["orderHost"]
	if !strings.HasPrefix(entry, "assets/") || strings.Contains(entry, "..") {
		t.Fatal("installed export Host missing")
	}
	assetPath := "/order-assets/" + strings.TrimPrefix(entry, "assets/")
	page := automationFixedContentRequest(t, handler, "GET", "/admin/orders", nil, session, "", "")
	if page.Code != 200 || !bytes.Contains(page.Body.Bytes(), []byte(assetPath)) {
		t.Fatal("installed export page does not mount its package Host")
	}
	asset := automationFixedContentRequest(t, handler, "GET", assetPath, nil, session, "", "")
	local, err := os.ReadFile(filepath.Join(input.Root, "web", "dist", entry))
	if err != nil || asset.Code != 200 || !bytes.Equal(asset.Body.Bytes(), local) {
		t.Fatal("installed export Host bytes differ from supplied package")
	}
	if !bytes.Contains(local, []byte("order-export-money-note")) {
		t.Fatal("installed package lacks new financial export guidance")
	}
	digest := sha256.Sum256(local)
	t.Logf("order-export-sharing installed Host source_sha=%s binary_sha256=%s host_sha256=%s", input.SHA, input.BinarySHA256, hex.EncodeToString(digest[:]))
}

func logInstalledOrderExportReceipt(t *testing.T, orders, rows int) {
	t.Helper()
	input := testconfig.InstalledProduct()
	t.Logf("order-export-sharing PASS source_sha=%s binary_sha256=%s orders=%d item_rows=%d columns=36 original_columns=preserved protected_http_cli=true empty_contacts=blank replay=byte_identical permissions=401_403 provider_attempts=0", input.SHA, input.BinarySHA256, orders, rows)
}
