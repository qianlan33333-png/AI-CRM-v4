package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributionhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/http"
	invoiceport "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Exercise the customer-facing authorization boundary through the exact
// installed binary. The API-only role starts no query/effects workers; all
// remote traffic is blocked and the only accepted HTTP requests are loopback.
func TestInvoiceDistributionInstalledAuthorizationBoundaries(t *testing.T) {
	installed := testconfig.InstalledProduct()
	if installed.Binary == "" || os.Getenv("AICRM_DATABASE_URL") == "" {
		t.Fatal("installed authorization contract requires the exact package and an isolated PostgreSQL database")
	}
	validateOrdersOAuthInstalledProduct(t, installed)
	f := newInvoiceFixture(t)
	prepareInvoiceInstalledDirectory(t, f)

	var otherIdentity int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at) VALUES($1,'oa_openid','wechat-app:wx-fixture','openid-installed-other','verified','invoice.installed.fixture',1,'active',now()) RETURNING id`, f.other).Scan(&otherIdentity); err != nil {
		t.Fatal("seed second synthetic OA identity", err)
	}
	otherToken := "invoice-installed-other-session-0001"
	if err := f.s.UoW.Within(f.ctx, func(tx context.Context) error {
		_, err := paymentsession.NewPostgreSQL().Insert(tx, paymentsession.Record{
			TokenDigest: sha256Sum(otherToken), PayerIdentityID: otherIdentity, PayerCustomerID: customerdomain.CustomerID(f.other),
			BeneficiarySelection: "unresolved", AppScopeDigest: sha256Sum("wechat-app:wx-fixture"), Channel: "h5_official_account",
			UnionIDVerified: true, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(),
		})
		return err
	}); err != nil {
		t.Fatal("seed second synthetic payment session", err)
	}

	ownOrder := f.order(f.customer, f.customer, "paid", "installed own order", "wechat_pay", time.Now().UTC())
	otherOrder := f.order(f.other, f.other, "paid", "installed other order", "wechat_pay", time.Now().UTC())
	ownReceipt, err := f.s.Create(f.ctx, f.token, ownOrder, "installed-own-invoice-0001")
	if err != nil {
		t.Fatal("seed own invoice application", err)
	}
	otherReceipt, err := f.s.Create(f.ctx, otherToken, otherOrder, "installed-other-invoice-0001")
	if err != nil {
		t.Fatal("seed other invoice application", err)
	}
	// Mark only the other customer's synthetic invoice as issued so a foreign
	// PDF request reaches the otherwise eligible file branch if authorization
	// were ever skipped. No Provider or issue effect is executed by this setup.
	if _, err = f.pool.Exec(f.ctx, `UPDATE invoice_applications SET state='issued',blue_number='BLUE-installed-fixture',updated_at=now() WHERE id=$1`, otherReceipt.ID); err != nil {
		t.Fatal("prepare issued foreign invoice fixture", err)
	}
	var before [5]int64
	if err = f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM invoice_applications),
		(SELECT count(*) FROM invoice_audit_events),
		(SELECT count(*) FROM external_effects WHERE owner='invoice'),
		(SELECT count(*) FROM external_effect_attempts),
		(SELECT count(*) FROM river_job WHERE kind='invoice.provider-query.v1')`).Scan(&before[0], &before[1], &before[2], &before[3], &before[4]); err != nil {
		t.Fatal("read baseline invoice effect counts", err)
	}

	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&platformKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	publicPath, keyPath := filepath.Join(dir, "platform-public.pem"), filepath.Join(dir, "api-v3.key")
	apiKey := "0123456789abcdef0123456789abcdef"
	if err = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, []byte(apiKey), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	endpoint := "http://" + address
	snapshot := invoiceport.Snapshot{MerchantID: f.s.Config.MerchantID, AppID: f.s.Config.AppID, AppScope: f.s.Config.AppScope}
	environment := invoiceCallbackInstalledEnvironment(f.pool.Config().ConnString(), address, installed.SHA, snapshot, publicPath, keyPath, base64.RawStdEncoding.EncodeToString([]byte(apiKey)))
	environment = invoiceBillInstalledEnvironment(t, environment, f.s.Config, snapshot, platformKey, apiKey, dir)

	process := startPeriodicContactInstalledProcess(t, installed.Binary, installed.Root, environment)
	waitInstalledSurveyReady(t, process, endpoint, installed.SHA)
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	call := func(method, path, token string, body any) *http.Response {
		t.Helper()
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			raw, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			reader = strings.NewReader(string(raw))
		}
		request, requestErr := http.NewRequest(method, endpoint+path, reader)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
		}
		if method == http.MethodPost {
			request.Header.Set("Origin", "https://invoice.fixture")
			request.Header.Set("Content-Type", "application/json")
			// The API is reached over loopback HTTP, while the installed runtime
			// retains its production-shaped HTTPS allowlisted origin.
			request.Host = "invoice.fixture"
		}
		response, doErr := client.Do(request)
		if doErr != nil {
			t.Fatal(doErr)
		}
		return response
	}
	readBody := func(response *http.Response) []byte {
		t.Helper()
		defer response.Body.Close()
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if readErr != nil {
			t.Fatal(readErr)
		}
		return raw
	}

	ownOrdersResponse := call(http.MethodGet, "/api/h5/me/orders?limit=20", f.token, nil)
	var ownOrders invoiceport.OrdersPage
	if raw := readBody(ownOrdersResponse); ownOrdersResponse.StatusCode != http.StatusOK || json.Unmarshal(raw, &ownOrders) != nil || len(ownOrders.Items) != 1 || ownOrders.Items[0].ID != ownOrder {
		t.Fatalf("installed own-order authorization status=%d body=%s", ownOrdersResponse.StatusCode, raw)
	}
	otherOrdersResponse := call(http.MethodGet, "/api/h5/me/orders?limit=20", otherToken, nil)
	var otherOrders invoiceport.OrdersPage
	if raw := readBody(otherOrdersResponse); otherOrdersResponse.StatusCode != http.StatusOK || json.Unmarshal(raw, &otherOrders) != nil || len(otherOrders.Items) != 1 || otherOrders.Items[0].ID != otherOrder {
		t.Fatalf("installed other-order isolation status=%d body=%s", otherOrdersResponse.StatusCode, raw)
	}

	ownHistoryResponse := call(http.MethodGet, "/api/h5/me/invoices?limit=20", f.token, nil)
	var ownHistory invoiceport.HistoryPage
	ownHistoryBody := readBody(ownHistoryResponse)
	if ownHistoryResponse.StatusCode != http.StatusOK || json.Unmarshal(ownHistoryBody, &ownHistory) != nil || len(ownHistory.Items) != 1 || ownHistory.Items[0].ID != ownReceipt.ID {
		t.Fatalf("installed own-invoice authorization status=%d body=%s", ownHistoryResponse.StatusCode, ownHistoryBody)
	}
	if strings.Contains(string(ownHistoryBody), "installed other invoice") || strings.Contains(string(ownHistoryBody), "BLUE-installed-fixture") {
		t.Fatal("own invoice history exposed another customer's private order or invoice")
	}
	ownDetailResponse := call(http.MethodGet, "/api/h5/me/invoices/"+ownReceipt.ID, f.token, nil)
	var ownDetail invoiceport.Receipt
	if raw := readBody(ownDetailResponse); ownDetailResponse.StatusCode != http.StatusOK || json.Unmarshal(raw, &ownDetail) != nil || ownDetail.ID != ownReceipt.ID {
		t.Fatalf("installed own-invoice detail status=%d body=%s", ownDetailResponse.StatusCode, raw)
	}

	foreignDetail := call(http.MethodGet, "/api/h5/me/invoices/"+otherReceipt.ID, f.token, nil)
	if raw := readBody(foreignDetail); foreignDetail.StatusCode != http.StatusNotFound || strings.Contains(string(raw), "BLUE-installed-fixture") {
		t.Fatalf("cross-customer invoice detail was not hidden: status=%d body=%s", foreignDetail.StatusCode, raw)
	}
	foreignPDF := call(http.MethodGet, "/api/h5/me/invoices/"+otherReceipt.ID+"/file", f.token, nil)
	if raw := readBody(foreignPDF); foreignPDF.StatusCode != http.StatusNotFound || strings.Contains(string(raw), "%PDF") || strings.Contains(string(raw), "BLUE-installed-fixture") {
		t.Fatalf("cross-customer issued PDF was not denied before release: status=%d body=%s", foreignPDF.StatusCode, raw)
	}

	personalTitle := map[string]any{
		"order_id": ownOrder, "idempotency_key": "installed-personal-title-rejected-0001",
		"buyer":                 map[string]string{"type": "PERSONAL", "name": "synthetic-personal-title", "taxpayer_id": "synthetic-tax"},
		"expected_amount_minor": int64(9900),
	}
	invalidTitleResponse := call(http.MethodPost, "/api/h5/me/invoices", f.token, personalTitle)
	invalidTitleBody := readBody(invalidTitleResponse)
	if invalidTitleResponse.StatusCode != http.StatusBadRequest || strings.Contains(string(invalidTitleBody), "synthetic-personal-title") || strings.Contains(string(invalidTitleBody), "synthetic-tax") {
		t.Fatalf("installed enterprise-only title rejection status=%d body=%s", invalidTitleResponse.StatusCode, invalidTitleBody)
	}

	// The distributor session is created through the installed Payment ->
	// Distribution bridge, then the same token must fail after its verified
	// handle is retired. No registration, payment, settlement, or worker runs.
	bridgeRequest, err := http.NewRequest(http.MethodPost, endpoint+"/api/v1/distribution/session/bridge", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	bridgeRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: f.token})
	bridgeRequest.Header.Set("Origin", "https://invoice.fixture")
	bridgeRequest.Header.Set("Idempotency-Key", "installed-distribution-session-bridge-0001")
	bridgeRequest.Host = "invoice.fixture"
	bridgeResponse, err := client.Do(bridgeRequest)
	if err != nil {
		t.Fatal(err)
	}
	var distributionCookie *http.Cookie
	for _, cookie := range bridgeResponse.Cookies() {
		if cookie.Name == distributionhttp.DistributionSessionCookieName {
			distributionCookie = cookie
		}
	}
	bridgeBody := readBody(bridgeResponse)
	if bridgeResponse.StatusCode != http.StatusCreated || distributionCookie == nil {
		t.Fatalf("installed distribution login bridge failed: status=%d body=%s", bridgeResponse.StatusCode, bridgeBody)
	}
	meRequest, err := http.NewRequest(http.MethodGet, endpoint+"/api/v1/distribution/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	meRequest.AddCookie(distributionCookie)
	meResponse, err := client.Do(meRequest)
	if err != nil {
		t.Fatal(err)
	}
	meBody := readBody(meResponse)
	if meResponse.StatusCode != http.StatusOK {
		t.Fatalf("installed distribution profile after login status=%d body=%s", meResponse.StatusCode, meBody)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE customer_identities SET status='retired',version=version+1,updated_at=now() WHERE id=$1`, f.identity); err != nil {
		t.Fatal("retire synthetic identity", err)
	}
	retiredRequest, err := http.NewRequest(http.MethodGet, endpoint+"/api/v1/distribution/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	retiredRequest.AddCookie(distributionCookie)
	retiredResponse, err := client.Do(retiredRequest)
	if err != nil {
		t.Fatal(err)
	}
	retiredBody := readBody(retiredResponse)
	if retiredResponse.StatusCode != http.StatusUnauthorized || strings.Contains(string(retiredBody), "distributor") {
		t.Fatalf("retired identity retained distribution read authority: status=%d body=%s", retiredResponse.StatusCode, retiredBody)
	}

	var after [5]int64
	if err = f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM invoice_applications),
		(SELECT count(*) FROM invoice_audit_events),
		(SELECT count(*) FROM external_effects WHERE owner='invoice'),
		(SELECT count(*) FROM external_effect_attempts),
		(SELECT count(*) FROM river_job WHERE kind='invoice.provider-query.v1')`).Scan(&after[0], &after[1], &after[2], &after[3], &after[4]); err != nil {
		t.Fatal("read post-request side-effect counts", err)
	}
	if after != before {
		t.Fatalf("read/denied requests changed invoice effect state: before=%v after=%v", before, after)
	}
	t.Logf("invoice-review-installed PASS source_sha=%s binary_sha256=%s own_order=true own_invoice=true enterprise_title_rejected=true cross_customer_pdf_denied=true api_only=true provider_writes=0", installed.SHA, installed.BinarySHA256)
	t.Logf("distribution-review-installed PASS source_sha=%s binary_sha256=%s payment_bridge_login=true retired_handle_denied=true api_only=true provider_writes=0", installed.SHA, installed.BinarySHA256)
}

func sha256Sum(value string) [32]byte { return sha256.Sum256([]byte(value)) }
