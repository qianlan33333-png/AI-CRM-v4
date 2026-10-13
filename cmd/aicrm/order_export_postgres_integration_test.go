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
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/crmcli"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
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
	testUnifiedOrderExportHTTPAndCLI(t, false)
}

// Both modes share the owner fixture and output assertions. Installed mode
// replaces only the HTTPS server with a proxy to the supplied package binary.
func testUnifiedOrderExportHTTPAndCLI(t *testing.T, installedMode bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	raw, err := platformconfig.DatabaseURL()
	if err != nil || !postgresurltestutil.IsLocalTestDatabaseURL(raw) {
		t.Fatal("requires isolated loopback AICRM_DATABASE_URL")
	}
	db, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	application, err := compose(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: db, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "order-export-fixture", WorkerOwner: "order-export-fixture", WorkerLimit: 1, GroupOps: platformconfig.GroupOps{WebhookSecret: "order-export-fixture"}, Survey: platformconfig.Survey{DataKey: key, IdentityPhoneDataKey: key}, WeChatPay: platformconfig.WeChatPay{OrderContactDataKey: key}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	owner, _, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "export-owner", Password: "export-owner-fixture-password", DisplayName: "导出测试管理员"})
	if err != nil {
		t.Fatal(err)
	}
	// Reuse Access's named operator fixture and AddUser path. There is no
	// custom credential/session issuer or runtime authentication bypass.
	if _, err = application.management.AddUser(ctx, accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: owner.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, accessapp.AddUserInput{Username: "operator", Password: "admin-access-operator-password", DisplayName: "Operator", Roles: []accessdomain.Role{accessdomain.RoleViewer}}); err != nil {
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
	if _, err = service.Create(ctx, orderport.CreateCommand{Actor: 1, IdempotencyKey: "create-export-multi-item", Input: orderdomain.NewOrderInput{
		Provider: orderdomain.ProviderAlipay, SourceSystem: "order-export-fixture", SourceKey: "export-multi-item", MerchantOrderNo: "export-multi-item",
		PayerCustomerID: &emptyBuyer, BeneficiaryCustomerID: &emptyBuyer, Amount: orderdomain.Money{AmountMinor: 3093, Currency: "CNY"}, RecordOrigin: orderdomain.RecordOriginNative, CreatedAt: time.Now().UTC(),
		Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "multi-a", ProductName: "多商品A", Quantity: 1, UnitAmountMinor: 2093, LineAmountMinor: 2093}, {LineNo: 2, ProductCode: "multi-b", ProductName: "多商品B", Quantity: 1, UnitAmountMinor: 1000, LineAmountMinor: 1000}},
	}}); err != nil {
		t.Fatal(err)
	}
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
	var commissionID int64
	if err = pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,version,created_at,updated_at)
		SELECT id,$1,1,$2,3093,100,309,299,309,1000,$3,$3,'exception',1,$3,$3 FROM distribution_order_attributions WHERE order_id=$1 RETURNING id`, full.ID, distributor, paid).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO distribution_commission_adjustments(commission_id,kind,delta_minor,resulting_payable_minor,reason,source_reference,occurred_at) VALUES($1,'manual_recovery',3,299,'manual_recovery','export-recovery',$2),($1,'merchant_liability',7,299,'merchant_liability','export-liability',$2)`, commissionID, paid); err != nil {
		t.Fatal(err)
	}
	insertSplit := func(n int, amount int64, state string, known bool) int64 {
		t.Helper()
		digest := effectport.Hash(fmt.Sprintf("export-split-%d", n))
		var receiverID, instructionID int64
		if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES($1,$2,$3,$3,'mini_program',$4,'ready',1,$5,$5) RETURNING id`, buyer, n+10, fmt.Sprintf("export-app-%d", n), digest, paid).Scan(&receiverID); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_instructions(settlement_ref,payment_id,receiver_id,provider_order_no,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,amount_minor,currency,state,deadline_at,receiver_confirmed_success,outcome_known,version,created_at,updated_at)
			SELECT $1,id,$2,$3,$4,$4,$4,$4,$5,'CNY',$6,$7,$8,$9,1,$10,$10 FROM payments WHERE order_id=$11 RETURNING id`, fmt.Sprintf("export-settlement-%d", n), receiverID, "v3ps_"+strings.Repeat(string(rune('A'+n)), 20), digest, amount, state, paid.Add(24*time.Hour), state == "paid", known, paid, full.ID).Scan(&instructionID); err != nil {
			t.Fatal(err)
		}
		return instructionID
	}
	insertSplit(1, 120, "paid", true)
	insertSplit(2, 189, "paid", true)
	insertSplit(3, 73, "exception", true)
	changingSplit := insertSplit(4, 37, "cancelled", true)
	if installedMode {
		process := useInstalledOrderExportFixture(t, server, db, key)
		defer stopInvoiceSettingsInstalledProcess(t, process)
	} else {
		server.Config.Handler = application.handler
	}
	server.StartTLS()
	handler := orderExportHTTPBridge(t, server)
	session, csrf := adminAccessLogin(t, handler, "export-owner", "export-owner-fixture-password")
	viewerSession, viewerCSRF := adminAccessLogin(t, handler, "operator", "admin-access-operator-password")
	for _, denied := range []struct {
		name, session, csrf string
		code                int
	}{
		{"anonymous", "", "", http.StatusUnauthorized},
		{"csrf", session, "", http.StatusForbidden},
		{"viewer", viewerSession, viewerCSRF, http.StatusForbidden},
	} {
		response := automationFixedContentRequest(t, handler, http.MethodPost, "/api/admin/exports", []byte(`{"resource":"orders"}`), denied.session, denied.csrf, "export-denied-"+denied.name)
		if response.Code != denied.code {
			t.Fatalf("export denial %s=%d want=%d", denied.name, response.Code, denied.code)
		}
	}
	if installedMode {
		assertInstalledOrderExportHost(t, handler, session)
	}
	request := func(path, body, key string) *httptest.ResponseRecorder {
		return automationFixedContentRequest(t, handler, http.MethodPost, path, []byte(body), session, csrf, key)
	}
	preview := request("/api/admin/exports/preview", `{"resource":"orders","filter":{"offset":50,"limit":1}}`, "")
	var counts struct {
		Orders int `json:"order_count"`
		Rows   int `json:"row_count"`
	}
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &counts) != nil || counts.Orders != 4 || counts.Rows != 5 {
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
		if e != nil || len(rows) != 6 || len(rows[0]) != 36 {
			t.Fatal("invalid workbook schema")
		}
		byNo := map[string][]string{}
		for _, row := range rows[1:] {
			for len(row) < 36 {
				row = append(row, "")
			}
			if _, seen := byNo[row[0]]; !seen {
				byNo[row[0]] = row
			} else {
				for _, value := range row[27:] {
					if value != "" {
						t.Fatal("installed/source export repeated order financial totals")
					}
				}
			}
		}
		assertOrderExportWorkbookContract(t, f, rows[0])
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
		want := []string{"30.93", "30.93", "2.99", "3.09", "0.00", "0.03", "26.94", "26.87", "异常待核对"}
		for i, value := range want {
			if a[27+i] != value {
				t.Fatalf("financial col %d=%q want=%q", 27+i, a[27+i], value)
			}
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
	if e != nil || len(csvRows) != 3 || len(csvRows[0]) != 36 {
		t.Fatal("identity filter exported wrong customers")
	}
	// Real CLI catalog request -> Access/CSRF -> XLSX -> private file -> reader.
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
	states := []struct {
		state      string
		known      bool
		label, net string
	}{
		{"accepted", false, "分账处理中", ""},
		{"settling", false, "分账处理中", ""},
		{"outcome_unknown", false, "分账结果待确认", ""},
		{"exception", false, "分账结果待确认", ""},
		{"exception", true, "异常待核对", "26.87"},
		{"cancelled", true, "异常待核对", "26.87"},
	}
	for i, tc := range states {
		t.Run(fmt.Sprintf("local_split_%s_known_%t", tc.state, tc.known), func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE payment_profit_sharing_instructions SET state=$2,outcome_known=$3,version=version+1 WHERE id=$1`, changingSplit, tc.state, tc.known); err != nil {
				t.Fatal(err)
			}
			response := request("/api/admin/exports", `{"resource":"orders","format":"csv"}`, fmt.Sprintf("export-state-%04d", i))
			if response.Code != 200 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(response.Body.String(), "\ufeff"))).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows[1:] {
				if row[0] == full.MerchantOrderNo && row[27] == "" {
					for _, value := range row[27:] {
						if value != "" {
							t.Fatal("CSV repeated order financial totals")
						}
					}
					continue
				}
				if row[0] == full.MerchantOrderNo && (row[30] != "3.09" || row[34] != tc.net || row[35] != tc.label) {
					t.Fatalf("incorrect local split money/state: %q", row[27:])
				}
			}
		})
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
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM order_export_receipts`).Scan(&receipts); err != nil || receipts != 3+len(states) {
		t.Fatalf("failed/replayed exports recorded extra receipts: %d %v", receipts, err)
	}
	var providerAttempts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effect_attempts`).Scan(&providerAttempts); err != nil || providerAttempts != 0 {
		t.Fatal("export fixture executed a Provider attempt", err)
	}
	if installedMode {
		logInstalledOrderExportReceipt(t, counts.Orders, counts.Rows)
	}
	t.Log(fmt.Sprintf("order_export_postgres: PASS orders=%d rows=%d HTTP/CLI/CSV/replay/decrypt-failure", counts.Orders, counts.Rows))
}
