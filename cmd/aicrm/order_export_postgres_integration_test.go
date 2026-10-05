package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/crmcli"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	postgresurltestutil "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres/testutil"
	"github.com/xuri/excelize/v2"
	"github.com/zalando/go-keyring"
)

// CLI and authenticated HTTP use the real composition and owner transactions.
// All customers, credentials and contacts below are synthetic local fixtures.
func TestPostgreSQLUnifiedOrderExportHTTPAndCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	raw, err := platformconfig.DatabaseURL()
	if err != nil || !postgresurltestutil.IsLocalTestDatabaseURL(raw) {
		t.Fatal("requires isolated loopback AICRM_DATABASE_URL")
	}
	db, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	application, err := compose(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: db, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "order-export-fixture", WorkerOwner: "order-export-fixture", WorkerLimit: 1, GroupOps: platformconfig.GroupOps{WebhookSecret: "order-export-fixture"}, Survey: platformconfig.Survey{DataKey: key, IdentityPhoneDataKey: key}, WeChatPay: platformconfig.WeChatPay{OrderContactDataKey: key}})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if _, _, err = application.management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "export-owner", Password: "export-owner-fixture-password", DisplayName: "导出测试管理员"}); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := orderstore.NewPostgreSQL(application.pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)
	cipher, _ := ordersecure.NewContactCipher(key)
	service.SetContactCipher(cipher)
	service.SetCollectedPhoneRecorder(collectedPhoneTestRecorder(t))
	var buyer, emptyBuyer int64
	for _, target := range []*int64{&buyer, &emptyBuyer} {
		if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,source) VALUES($1,'active','真实付款人测试昵称','test'),($2,'active','无联系方式测试买家','test')`, buyer, emptyBuyer); err != nil {
		t.Fatal(err)
	}
	create := func(no, level, mobile string, payer int64, provider orderdomain.Provider) orderdomain.Snapshot {
		t.Helper()
		var snapshot orderdomain.Snapshot
		command := orderport.PaymentOrderCommand{Provider: provider, MerchantOrderNo: no, PayerCustomerID: payer, BeneficiaryCustomerID: emptyBuyer, ProductID: 9, ProductCode: "0000123", ProductName: "测试商品", ProductVersion: 1, ProductType: "standard_product", UnitAmountMinor: 3093, Currency: "CNY", MobileE164: mobile, ContactCollectionLevel: level, ActorScope: "order-export-fixture", IdempotencyKey: "create-" + no}
		if level == "shipping_address" {
			command.ShippingAddress = orderport.ShippingAddress{RecipientName: "测试小区A座", ProvinceCode: "11", ProvinceName: "北京市", CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区", DetailAddress: "202"}
		}
		err = uow.Within(ctx, func(tx context.Context) error {
			var e error
			snapshot, e = service.CreatePaymentOrderWithin(tx, command)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	full := create("000123456789012345678901", "shipping_address", "+8613800000088", buyer, orderdomain.ProviderWeChatPay)
	fallback := create("export-fallback", "none", "", buyer, orderdomain.ProviderAlipay)
	empty := create("export-empty", "none", "", emptyBuyer, orderdomain.ProviderAlipay)
	// Preserve a partial refund and true payment confirmation independently from UpdatedAt.
	paid := time.Date(2026, 10, 5, 0, 3, 4, 0, time.UTC)
	if _, err = application.pool.Native().Exec(ctx, `UPDATE orders SET status='partially_refunded',refunded_minor=100 WHERE id=$1`, full.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO payments(order_id,provider,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at,paid_confirmed_at,provider_transaction_reference) VALUES($1,'wechat_pay',$2,1,$3,$4,3093,'CNY','paid',1,$5,$5,$5,'000987654321012345678901')`, full.ID, full.MerchantOrderNo, buyer, emptyBuyer, paid); err != nil {
		t.Fatal(err)
	}
	var distributor, policy, credential int64
	pool := application.pool.Native()
	if err = pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,registered_at,version,created_at,updated_at) VALUES($1,'D000001','v1',$2,1,$2,$2) RETURNING id`, buyer, paid).Scan(&distributor); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(9,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, paid).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,9,'standard_product',$2,'active',$3,$4) RETURNING id`, distributor, make([]byte, 32), paid, paid.Add(time.Hour)).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'0000123','测试商品',$2,$3,'order:fixture:item:1','eligible',$4,1,1000,7,$5)`, full.ID, distributor, credential, policy, paid); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "export-owner", "export-owner-fixture-password")
	request := func(path, body, key string) *httptest.ResponseRecorder {
		return automationFixedContentRequest(t, application.handler, http.MethodPost, path, []byte(body), session, csrf, key)
	}
	preview := request("/api/admin/exports/preview", `{"resource":"orders","filter":{"offset":50,"limit":1}}`, "")
	var counts struct {
		Orders int `json:"order_count"`
		Rows   int `json:"row_count"`
	}
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &counts) != nil || counts.Orders != 3 || counts.Rows != 3 {
		t.Fatal("preview did not count all pages")
	}
	exported := request("/api/admin/exports", `{"resource":"orders","filter":{"offset":50,"limit":1}}`, "export-http-replay-0001")
	if exported.Code != 200 || exported.Header().Get("Cache-Control") != "no-store" || exported.Header().Get("X-AICRM-Export-Receipt") == "" {
		t.Fatalf("export status %d: %s", exported.Code, exported.Body.String())
	}
	assertWorkbook := func(content []byte) {
		t.Helper()
		f, e := excelize.OpenReader(bytes.NewReader(content))
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		rows, e := f.GetRows("订单信息")
		if e != nil || len(rows) != 4 || len(rows[0]) != 27 {
			t.Fatal("invalid workbook schema")
		}
		byNo := map[string][]string{}
		for _, row := range rows[1:] {
			for len(row) < 27 {
				row = append(row, "")
			}
			byNo[row[0]] = row
		}
		a, b, c := byNo[full.MerchantOrderNo], byNo[fallback.MerchantOrderNo], byNo[empty.MerchantOrderNo]
		if a[3] != "真实付款人测试昵称" || a[4] != "000987654321012345678901" || a[6] != "2026-10-05 08:03:04" || a[7] != "30.93" || a[9] != "1.00" || a[10] != "测试小区A座" || a[11] != "13800000088" || a[15] != "202" || a[16] != "0000123" || a[22] != "D000001" || a[23] != "真实付款人测试昵称" || b[22] != "" {
			t.Fatal("saved payment/contact facts changed")
		}
		var publicNo string
		if e = application.pool.Native().QueryRow(ctx, `SELECT public_number::text FROM customers WHERE id=$1`, buyer).Scan(&publicNo); e != nil {
			t.Fatal(e)
		}
		if a[2] != publicNo || b[2] != publicNo || b[5] != "支付宝" || b[6] != "" || b[10] != "" || b[11] != "13800000088" || c[11] != "" {
			t.Fatal("payer/fallback/missing facts mismatch")
		}
	}
	assertWorkbook(exported.Body.Bytes())
	replay := request("/api/admin/exports", `{"resource":"orders"}`, "export-http-replay-0001")
	if replay.Code != 200 || !bytes.Equal(exported.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("receipt replay changed bytes")
	}
	// A phone filter is resolved by the same OneID path as the list.
	filtered := request("/api/admin/exports", `{"resource":"orders","format":"csv","filter":{"phone":"13800000088"}}`, "export-filter-0001")
	if filtered.Code != 200 {
		t.Fatalf("identity filter status=%d body=%s", filtered.Code, filtered.Body.String())
	}
	csvRows, e := csv.NewReader(strings.NewReader(strings.TrimPrefix(filtered.Body.String(), "\ufeff"))).ReadAll()
	if e != nil || len(csvRows) != 3 || len(csvRows[0]) != 27 {
		t.Fatal("identity filter exported wrong customers")
	}
	// Real CLI catalog request -> Access/CSRF -> XLSX -> private file -> reader.
	server.Config.Handler = application.handler
	server.StartTLS()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = originalTransport }()
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	for _, name := range []string{"AICRM_CLI_TOKEN", "AICRM_CLI_PASSWORD", "AICRM_CLI_CLIENT_SECRET"} {
		t.Setenv(name, "")
	}
	var stdout, stderr bytes.Buffer
	run := func(input string, args ...string) {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if code := crmcli.Run(args, strings.NewReader(input), &stdout, &stderr); code != 0 {
			t.Fatalf("CLI exit=%d output=%s error=%s", code, stdout.String(), stderr.String())
		}
	}
	run("", "profile", "add", "order-export", server.URL)
	run("export-owner-fixture-password", "auth", "login", "--username", "export-owner", "--secret-stdin")
	output := filepath.Join(t.TempDir(), "orders.xlsx")
	run(`{"resource":"orders"}`, "invoke", "admin.createLegacyOrderExport", "--input", "-", "--idempotency-key", "order-export-cli-0001", "--output", output)
	file, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	assertWorkbook(file)
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 || strings.Contains(stdout.String(), "13800000088") {
		t.Fatal("CLI exposed contact outside protected download")
	}
	// An unreadable existing phone is an error, never an absent-contact success.
	if _, err = application.pool.Native().Exec(ctx, `UPDATE identity_phone_secrets SET ciphertext=decode(repeat('00',40),'hex') WHERE identity_id IN (SELECT id FROM customer_identities WHERE customer_id=$1 AND kind='phone')`, buyer); err != nil {
		t.Fatal(err)
	}
	failed := request("/api/admin/exports", `{"resource":"orders"}`, "export-bad-identity-0001")
	if failed.Code != 503 {
		t.Fatalf("unreadable contact was not rejected: %d", failed.Code)
	}
	var receipts int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM order_export_receipts`).Scan(&receipts); err != nil || receipts != 3 {
		t.Fatalf("failed/replayed exports recorded extra receipts: %d %v", receipts, err)
	}
	t.Log(fmt.Sprintf("order_export_postgres: PASS orders=%d rows=%d HTTP/CLI/CSV/replay/decrypt-failure", counts.Orders, counts.Rows))
}
