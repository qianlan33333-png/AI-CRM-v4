package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// Seeds disposable facts through owner services; all assertions exercise the
// exact installed binary. No Worker or real WeChat/activation endpoint runs.
func TestPostgreSQLShopInstalledAPI(t *testing.T) {
	input := testconfig.InstalledProduct()
	if input.Binary == "" {
		t.Skip("explicit installed package inputs required")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{deferEffectsWorker: true})
	session, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	wrapped, _ := postgres.Wrap(f.application.pool.Native(), time.Second)
	uow, _ := postgres.NewUnitOfWork(wrapped)
	catalog, _ := productstore.NewPostgreSQL(f.application.pool.Native(), uow)
	var id productport.ID
	now := time.Now().UTC().Truncate(time.Second)
	if e := uow.Within(f.ctx, func(ctx context.Context) error {
		var e error
		id, _, e = catalog.ImportShopSKUWithin(ctx, productport.ShopSKU{AppID: "installed-shop", ProductID: "parent", SKUID: "sku", Name: "Installed Shop", PriceMinor: 100}, "wxshop-installed", now)
		if e != nil {
			return e
		}
		return f.application.shopSync.Orders.ImportShopOrderWithin(ctx, orderport.ShopOrder{AppID: "installed-shop", ID: "90071992547409931234", TransactionID: "installed-shop-tx", CreatedAt: now.Add(-time.Hour), PaidAt: now, UpdatedAt: now, PaidMinor: 85, RefundedMinor: 85, Lines: []orderport.ShopOrderLine{{ProductID: "parent", SKUID: "sku", Name: "Installed Shop", Quantity: 2, UnitMinor: 100, PaidMinor: 80}}}, now.Add(time.Second))
	}); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	key := []byte("abcdefghijklmnopqrstuvwxyz123456")
	process := startPeriodicContactInstalledProcess(t, input.Binary, input.Root, []string{
		"AICRM_DATABASE_URL=" + f.application.pool.Native().Config().ConnConfig.ConnString(), "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + input.SHA,
		"AICRM_PUBLIC_ORIGIN=" + f.server.URL, "AICRM_GROUP_OPS_WEBHOOK_SECRET=installed-shop-fixture-secret",
		"AICRM_SURVEY_DATA_KEY=" + base64.RawStdEncoding.EncodeToString(f.dataKey), "AICRM_IDENTITY_PHONE_DATA_KEY=" + base64.RawStdEncoding.EncodeToString(f.dataKey),
		"AICRM_COMMERCE_PUSH_PAYLOAD_DATA_KEY=" + base64.RawStdEncoding.EncodeToString(f.dataKey),
		"AICRM_WECHAT_SHOP_PROVIDER_ENABLED=true", "AICRM_WECHAT_SHOP_APP_ID=installed-shop", "AICRM_WECHAT_SHOP_APP_SECRET=installed-fixture-secret",
		"AICRM_WECHAT_SHOP_CALLBACK_TOKEN=installed-fixture-token", "AICRM_WECHAT_SHOP_CALLBACK_AES_KEY=" + base64.RawStdEncoding.EncodeToString(key),
		"AICRM_EFFECTS_PROVIDER_ENABLED=false", "AICRM_OUTBOUND_PROVIDER_ENABLED=false", "AICRM_GROUP_OPS_PROVIDER_ENABLED=false", "AICRM_COMMERCE_PUSH_PROVIDER_ENABLED=false", "AICRM_WECHAT_PAY_PROVIDER_ENABLED=false", "AICRM_ALIPAY_PROVIDER_ENABLED=false",
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost,::1",
	})
	waitInstalledSurveyReady(t, process, "http://"+address, input.SHA)
	target, _ := url.Parse("http://" + address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := &http.Transport{Proxy: nil}
	proxy.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	f.server.Config.Handler = proxy
	call := func(method, path, body string, auth bool) (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", f.server.URL)
		if auth {
			r.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: session})
			r.AddCookie(&http.Cookie{Name: "aicrm_csrf", Value: csrf})
			r.Header.Set("X-CSRF-Token", csrf)
		}
		response, e := f.server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		return response.StatusCode, v
	}
	if code, _ := call("POST", "/api/admin/products/wechat-shop/refresh", "", false); code != 401 {
		t.Fatalf("refresh anonymous=%d", code)
	}
	code, first := call("POST", "/api/admin/products/wechat-shop/refresh", "", true)
	if code != 202 {
		t.Fatalf("refresh=%d", code)
	}
	code, second := call("POST", "/api/admin/products/wechat-shop/refresh", "", true)
	if code != 202 || first["id"] != second["id"] {
		t.Fatal("refresh did not reuse active task")
	}
	if code, status := call("GET", "/api/admin/products/wechat-shop/refresh-status", "", true); code != 200 || status["state"] != "queued" {
		t.Fatal("refresh status unreadable")
	}
	if code, detail := call("GET", "/api/admin/orders/90071992547409931234?provider=wechat_shop", "", true); code != 200 || detail["record_origin"] != "external" || detail["refunded_minor"] != float64(85) {
		t.Fatal("external refund fact unreadable")
	}
	if code, product := call("GET", "/api/admin/products/"+strconv.FormatInt(int64(id), 10), "", true); code != 200 || product["source_kind"] != "wechat_shop" {
		t.Fatal("Shop source not exposed")
	}
	query, body := installedShopCallback(key, now)
	if code, _ := call("POST", "/api/public/wechat-shop/callback?"+query, body, false); code != 200 {
		t.Fatalf("callback=%d", code)
	}
	if code, _ := call("POST", "/api/public/wechat-shop/callback?"+query, body, false); code != 200 {
		t.Fatal("callback replay failed")
	}
	if code, _ := call("POST", "/api/public/wechat-shop/callback?"+query+"0", body, false); code != 400 {
		t.Fatal("wrong signature accepted")
	}
	var receipts, jobs int
	if e := f.application.pool.Native().QueryRow(f.ctx, `SELECT count(*) FROM wechat_shop_callback_receipts`).Scan(&receipts); e != nil {
		t.Fatal(e)
	}
	if e := f.application.pool.Native().QueryRow(f.ctx, `SELECT count(*) FROM river_job WHERE kind='wechat_shop.order_read'`).Scan(&jobs); e != nil {
		t.Fatal(e)
	}
	if receipts != 1 || jobs != 1 {
		t.Fatalf("callback receipt/job replay=%d/%d", receipts, jobs)
	}
	t.Log("installed Shop API: active refresh reuse, source/refund readback, authenticated callback atomic replay; no Provider call")
}
func installedShopCallback(key []byte, now time.Time) (string, string) {
	plain := []byte(`{"MsgType":"event","Event":"channels_ec_order_pay","order_info":{"order_id":"90071992547409931234"}}`)
	data := append(make([]byte, 16), make([]byte, 4)...)
	binary.BigEndian.PutUint32(data[16:20], uint32(len(plain)))
	data = append(data, plain...)
	data = append(data, []byte("installed-shop")...)
	pad := 32 - len(data)%32
	data = append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	encrypted := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(encrypted, data)
	value := base64.StdEncoding.EncodeToString(encrypted)
	ts := strconv.FormatInt(now.Unix(), 10)
	parts := []string{"installed-fixture-token", ts, "fixture-nonce", value}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	q := url.Values{"timestamp": {ts}, "nonce": {"fixture-nonce"}, "msg_signature": {hex.EncodeToString(sum[:])}}
	body, _ := json.Marshal(map[string]string{"ToUserName": "gh_fixture", "Encrypt": value})
	return q.Encode(), string(body)
}
