package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	overviewapp "github.com/qianlan33333-png/AI-CRM-v3/internal/overview/app"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// OneID decision: the fixture inserts one already-verified canonical identity
// strictly to prove the read-only root-provenance counter. It never resolves
// or provisions through the overview. Persistence decision: the overview
// joins no owner tables and writes nothing; this PostgreSQL fixture only
// seeds owner facts needed to exercise the composed read path.
func TestPostgreSQLAdminOverviewCompositionPreflight(t *testing.T) {
	fixture := newAdminOverviewFixture(t, withAdminOverviewTrendFacts())
	response := overviewAuthenticatedGET(t, fixture.application.handler, fixture.session, "/api/admin/overview?period=7d")
	assertAdminOverviewResponse(t, response)
	unauthenticated := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/admin/overview?period=7d", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("overview unauthenticated status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func TestPostgreSQLAdminOverviewPaidRecordsCompositionUsesPaidConfirmationFact(t *testing.T) {
	fixture := newAdminOverviewFixture(t, withAdminOverviewTrendFacts())
	response := overviewAuthenticatedGET(t, fixture.application.handler, fixture.session, "/api/admin/overview/paid-records?period=7d")
	var body struct {
		Range struct {
			Timezone string `json:"timezone"`
		} `json:"range"`
		Items []struct {
			Provider        string    `json:"provider"`
			OrderReference  string    `json:"order_reference"`
			PayerCustomerID *int64    `json:"payer_customer_id"`
			AmountMinor     int64     `json:"amount_minor"`
			Currency        string    `json:"currency"`
			PaidConfirmedAt time.Time `json:"paid_confirmed_at"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, item := range body.Items {
		if item.Provider != "wechat_pay" || item.OrderReference == "" || item.PayerCustomerID == nil || item.AmountMinor < 1 || item.Currency != "CNY" || item.PaidConfirmedAt.IsZero() {
			t.Fatalf("invalid paid record %+v", item)
		}
		total += item.AmountMinor
	}
	if response.Code != http.StatusOK || body.Range.Timezone != "Asia/Shanghai" || len(body.Items) != 3 || total != 2400 || body.NextCursor != "" || strings.Contains(response.Body.String(), "payment_id") || strings.Contains(response.Body.String(), "provider_transaction") {
		t.Fatalf("paid records status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPostgreSQLAdminOverviewChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	fixture := newAdminOverviewFixture(t, withAdminOverviewTrendFacts(), withAdminOverviewLongTrendAmount(), withAdminOverviewOrderReadFacts())
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate overview Chromium journey")
	}
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(source), "overview_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_OVERVIEW_BROWSER_URL="+fixture.server.URL,
		"AICRM_OVERVIEW_BROWSER_USERNAME=overview-browser-admin",
		"AICRM_OVERVIEW_BROWSER_PASSWORD=overview-browser-admin-password",
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "admin_overview_chromium: PASS") {
		t.Fatalf("admin overview Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
}

func TestPostgreSQLAdminOverviewCanonicalPayersFollowCurrentMerge(t *testing.T) {
	fixture := newAdminOverviewFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	payerA := insertAdminOverviewCustomer(t, fixture.ctx, fixture.application, now)
	payerB := insertAdminOverviewCustomer(t, fixture.ctx, fixture.application, now)
	payerC := insertAdminOverviewCustomer(t, fixture.ctx, fixture.application, now)
	for _, item := range []struct {
		key   string
		payer *int64
		minor int64
	}{
		{key: "canonical-a", payer: &payerA, minor: 100},
		{key: "canonical-b", payer: &payerB, minor: 200},
		{key: "canonical-c", payer: &payerC, minor: 300},
		{key: "canonical-missing", payer: nil, minor: 400},
	} {
		insertAdminOverviewPayment(t, fixture.ctx, fixture.application, now, item.key, item.payer, item.minor)
	}
	if _, err := fixture.application.pool.Native().Exec(fixture.ctx, `UPDATE customers
		SET status='merged',merged_into_customer_id=$2,merged_at=$3,updated_at=$3
		WHERE id=$1`, payerA, payerB, now); err != nil {
		t.Fatal(err)
	}
	response := overviewAuthenticatedGET(t, fixture.application.handler, fixture.session, "/api/admin/overview?period=7d")
	var body struct {
		Paid struct {
			Status                  string `json:"status"`
			ReasonCode              string `json:"reason_code"`
			OrderCount              int64  `json:"order_count"`
			DistinctCanonicalPayers *int64 `json:"distinct_canonical_payers"`
			MissingPayerCount       int64  `json:"missing_payer_count"`
		} `json:"paid"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Paid.Status != "data_missing" || body.Paid.ReasonCode != "payer_customer_missing" || body.Paid.OrderCount != 5 || body.Paid.DistinctCanonicalPayers == nil || *body.Paid.DistinctCanonicalPayers != 3 || body.Paid.MissingPayerCount != 1 {
		t.Fatalf("canonical payer overview status=%d body=%s", response.Code, response.Body.String())
	}
	var persistedA, persistedB int64
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT payer_customer_id FROM payments WHERE merchant_order_no='M-OVERVIEW-canonical-a'`).Scan(&persistedA); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT payer_customer_id FROM payments WHERE merchant_order_no='M-OVERVIEW-canonical-b'`).Scan(&persistedB); err != nil {
		t.Fatal(err)
	}
	if persistedA != payerA || persistedB != payerB {
		t.Fatalf("historical payment payer IDs were rewritten: a=%d b=%d want a=%d b=%d", persistedA, persistedB, payerA, payerB)
	}
}

func TestPostgreSQLAdminOverviewCanonicalPayersKeepOneSnapshotAcrossConcurrentMerge(t *testing.T) {
	fixture := newAdminOverviewFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	payerA := insertAdminOverviewCustomer(t, fixture.ctx, fixture.application, now)
	payerB := insertAdminOverviewCustomer(t, fixture.ctx, fixture.application, now)
	insertAdminOverviewPayment(t, fixture.ctx, fixture.application, now, "snapshot-a", &payerA, 100)
	insertAdminOverviewPayment(t, fixture.ctx, fixture.application, now, "snapshot-b", &payerB, 200)
	readUoW, err := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(fixture.application.pool)
	if err != nil {
		t.Fatal(err)
	}
	merged := false
	store := mergeAfterPaidOverviewStore{repository: paymentstore.NewPostgreSQL(), afterFirstPaidRead: func() error {
		if merged {
			return errors.New("fixture merge repeated")
		}
		merged = true
		_, mergeErr := fixture.application.pool.Native().Exec(fixture.ctx, `UPDATE customers
			SET status='merged',merged_into_customer_id=$2,merged_at=$3,updated_at=$3
			WHERE id=$1`, payerA, payerB, now)
		return mergeErr
	}}
	reader, err := paymentapp.NewOverviewReader(readUoW, store, identityquery.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	window := paymentport.OverviewWindow{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	first, err := reader.ReadPaidOverview(fixture.ctx, window)
	if err != nil || !merged || first.OrderCount != 3 || first.DistinctCanonicalPayers != 3 {
		t.Fatalf("repeatable-read snapshot result=%+v merged=%t err=%v", first, merged, err)
	}
	currentReader, err := paymentapp.NewOverviewReader(readUoW, paymentstore.NewPostgreSQL(), identityquery.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	current, err := currentReader.ReadPaidOverview(fixture.ctx, window)
	if err != nil || current.OrderCount != 3 || current.DistinctCanonicalPayers != 2 {
		t.Fatalf("new snapshot result=%+v err=%v", current, err)
	}
	var persistedA, persistedB int64
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT payer_customer_id FROM payments WHERE merchant_order_no='M-OVERVIEW-snapshot-a'`).Scan(&persistedA); err != nil {
		t.Fatal(err)
	}
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT payer_customer_id FROM payments WHERE merchant_order_no='M-OVERVIEW-snapshot-b'`).Scan(&persistedB); err != nil {
		t.Fatal(err)
	}
	if persistedA != payerA || persistedB != payerB {
		t.Fatalf("concurrent merge rewrote historical payer facts: a=%d b=%d", persistedA, persistedB)
	}
}

func TestPostgreSQLAdminOverviewCanonicalPayersScaleWithinSectionBudget(t *testing.T) {
	fixture := newAdminOverviewFixture(t)
	const payerCount = 10001
	seedAdminOverviewPayerScale(t, fixture.ctx, fixture.application, time.Now().UTC().Truncate(time.Microsecond), payerCount)
	started := time.Now()
	response := overviewAuthenticatedGET(t, fixture.application.handler, fixture.session, "/api/admin/overview?period=7d")
	elapsed := time.Since(started)
	var body struct {
		Paid struct {
			Status                  string `json:"status"`
			OrderCount              int64  `json:"order_count"`
			DistinctCanonicalPayers *int64 `json:"distinct_canonical_payers"`
		} `json:"paid"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Paid.Status != "ready" || body.Paid.OrderCount != payerCount+1 || body.Paid.DistinctCanonicalPayers == nil || *body.Paid.DistinctCanonicalPayers != payerCount+1 {
		t.Fatalf("scaled canonical payer overview status=%d body=%s", response.Code, response.Body.String())
	}
	if elapsed > overviewapp.DefaultSectionReadTimeout {
		t.Fatalf("%d canonical payers took %s, exceeding overview section budget %s", payerCount, elapsed, overviewapp.DefaultSectionReadTimeout)
	}
	t.Logf("%d canonical payers read exactly in %s within %s section budget", payerCount, elapsed, overviewapp.DefaultSectionReadTimeout)
}

type adminOverviewFixture struct {
	ctx         context.Context
	application *composedApplication
	server      *httptest.Server
	session     string
}

type adminOverviewFixtureOptions struct {
	includeTrendFacts      bool
	includeLongTrendAmount bool
	includeOrderReadFacts  bool
}
type adminOverviewFixtureOption func(*adminOverviewFixtureOptions)

// withAdminOverviewTrendFacts supplies the three-date visual fixture only to
// response and Chromium coverage. Canonical-root and scale tests retain their
// minimal one-payment baseline so their exact denominator is self-contained.
func withAdminOverviewTrendFacts() adminOverviewFixtureOption {
	return func(options *adminOverviewFixtureOptions) { options.includeTrendFacts = true }
}

// withAdminOverviewLongTrendAmount keeps the long-money visual proof scoped to
// Chromium. Production aggregation semantics and the narrower composition
// fixtures retain their concise, independently asserted amounts.
func withAdminOverviewLongTrendAmount() adminOverviewFixtureOption {
	return func(options *adminOverviewFixtureOptions) { options.includeLongTrendAmount = true }
}

// withAdminOverviewOrderReadFacts enables the synthetic payment configuration
// only for the browser route that crosses into the existing Order detail host.
// Provider effects remain disabled and the fixture never starts a worker or
// invokes a Provider; this merely opens the already-scoped Distribution read
// composition that the Order detail uses.
func withAdminOverviewOrderReadFacts() adminOverviewFixtureOption {
	return func(options *adminOverviewFixtureOptions) { options.includeOrderReadFacts = true }
}

func newAdminOverviewFixture(t *testing.T, configure ...adminOverviewFixtureOption) *adminOverviewFixture {
	t.Helper()
	// compose resolves the release manifest as web/dist relative to the running
	// service. Go package tests otherwise start in cmd/aicrm and silently take
	// the generic no-assets fallback, which cannot validate the V3 page Host.
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate overview composition fixture")
	}
	t.Chdir(filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..")))
	options := adminOverviewFixtureOptions{}
	for _, option := range configure {
		option(&options)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	runtimeConfig := platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://" + server.Listener.Addr().String(),
		ReleaseSHA:   "1111111111111111111111111111111111111111",
		WorkerOwner:  "admin-overview-chromium",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "admin-overview-chromium-webhook"},
		OpenPlatform: platformconfig.OpenPlatform{JWTSigningKey: "01234567890123456789012345678901"},
		Survey: platformconfig.Survey{
			DataKey: dataKey, IdentityPhoneDataKey: dataKey,
		},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "overview-browser-admin", Password: "overview-browser-admin-password", DisplayName: "Overview Browser Admin"},
	}
	if options.includeOrderReadFacts {
		key, cert := distributionFixturePaymentCredentials(t)
		runtimeConfig.WeChatPay = platformconfig.WeChatPay{
			Enabled: true, AppID: "wx-overview-browser", AppSecret: "fixture-secret", AppScope: "wechat-app:overview-browser",
			OrderContactDataKey: dataKey, MerchantID: "overview-fixture-mch", MerchantSerial: "overview-fixture-merchant",
			PrivateKeyPath: key, PlatformCertPath: cert, APIV3Key: "0123456789abcdef0123456789abcdef",
		}
	}
	application, err := compose(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "overview-browser-admin", Password: "overview-browser-admin-password", DisplayName: "Overview Browser Admin"}); err != nil {
		t.Fatal(err)
	}
	seedAdminOverviewFacts(t, ctx, application, options.includeTrendFacts, options.includeLongTrendAmount, options.includeOrderReadFacts)
	server.Config.Handler = application.handler
	server.StartTLS()
	session, _ := adminAccessLogin(t, application.handler, "overview-browser-admin", "overview-browser-admin-password")
	return &adminOverviewFixture{ctx: ctx, application: application, server: server, session: session}
}

func seedAdminOverviewFacts(t *testing.T, ctx context.Context, application *composedApplication, includeTrendFacts, includeLongTrendAmount, includeOrderReadFacts bool) {
	t.Helper()
	pool := application.pool.Native()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var customerID, identityID, orderID, paymentID int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers(created_at,updated_at) VALUES($1,$1) RETURNING id`, now).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at,created_at,updated_at) VALUES($1,'mp_openid','wechat-app:overview-browser','overview-browser-payer','verified','wechat_miniprogram',1,$2,$2,$2) RETURNING id`, customerID, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','overview-browser','overview-browser-order','M-OVERVIEW-BROWSER',$1,$1,1200,'CNY','paid','native',true,2,$2,$2) RETURNING id`, customerID, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,paid_confirmed_at,created_at,updated_at,historical) VALUES($1,'wechat_pay','mini_program','M-OVERVIEW-BROWSER',$2,$3,$3,1200,'CNY','paid',1,$4,$4,$4,false) RETURNING id`, orderID, identityID, customerID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	if includeOrderReadFacts {
		if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
VALUES($1,1,99001,1,'overview-browser-product','Overview Browser Product',1200,1,1200)`, orderID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(
order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,
gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,
reserved_at,created_at
) VALUES($1,'standard_product',99001,'overview-browser-product','Overview Browser Product',1,0,1200,0,1200,'CNY',false,'',$2,$2)`, orderID, now); err != nil {
			t.Fatal(err)
		}
		paidDigest := sha256.Sum256([]byte("overview-browser-paid"))
		if _, err := pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, paidDigest[:], now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,'pending_payment','paid',0,2,'overview-browser',$2)`, orderID, now); err != nil {
			t.Fatal(err)
		}
	}
	if includeTrendFacts {
		// Seed three trustworthy confirmation dates only for the visual fixture.
		// The deliberately absent dates exercise ready-only client-side zero-day
		// completion without fabricating data when the Owner reports data_missing.
		longTrendAmount := int64(800)
		if includeLongTrendAmount {
			longTrendAmount = 1234567890
		}
		for _, extra := range []struct {
			sourceKey, merchantOrder string
			amount                   int64
			confirmedAt              time.Time
		}{
			{sourceKey: "overview-browser-order-six-days", merchantOrder: "M-OVERVIEW-BROWSER-6", amount: 400, confirmedAt: now.AddDate(0, 0, -6)},
			{sourceKey: "overview-browser-order-three-days", merchantOrder: "M-OVERVIEW-BROWSER-3", amount: longTrendAmount, confirmedAt: now.AddDate(0, 0, -3)},
		} {
			var extraOrderID int64
			if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','overview-browser',$1,$2,$3,$3,$4,'CNY','paid','native',true,1,$5,$5) RETURNING id`, extra.sourceKey, extra.merchantOrder, customerID, extra.amount, extra.confirmedAt).Scan(&extraOrderID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,paid_confirmed_at,created_at,updated_at,historical) VALUES($1,'wechat_pay','mini_program',$2,$3,$4,$4,$5,'CNY','paid',1,$6,$6,$6,false)`, extraOrderID, extra.merchantOrder, identityID, customerID, extra.amount, extra.confirmedAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	var refundID int64
	if err := pool.QueryRow(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','R-OVERVIEW-BROWSER',200,'overview fixture','completed',1,$2,$2) RETURNING id`, paymentID, now).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_audit_events(event_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('payment.refund_settled',$1,'overview-browser','{}'::jsonb,$2)`, refundID, now); err != nil {
		t.Fatal(err)
	}
	var distributorID, policyID, credentialID, attributionID, commissionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,'DSTOVERVIEWBROWSER','v1',true,$2,1,$2,$2) RETURNING id`, customerID, now).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(99001,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,99001,'standard_product',$2,'active',$3,$4) RETURNING id`, distributorID, make([]byte, 32), now, now.Add(24*time.Hour)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'overview-browser-product','Overview Browser Product',$2,$3,'payment:overview-browser','eligible',$4,1,1000,7,$5) RETURNING id`, orderID, distributorID, credentialID, policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1200,0,120,120,0,1000,$4,$5,'pending','','','',1,$4,$4) RETURNING id`, attributionID, orderID, distributorID, now, now.AddDate(0, 0, 7)).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'settlement_unknown','open',120,0,120,'provider_result_pending','payment:overview-browser','overview-browser',1,$2,$2)`, commissionID, now); err != nil {
		t.Fatal(err)
	}
}

func insertAdminOverviewCustomer(t *testing.T, ctx context.Context, application *composedApplication, now time.Time) int64 {
	t.Helper()
	var customerID int64
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customers(created_at,updated_at) VALUES($1,$1) RETURNING id`, now).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	return customerID
}

func insertAdminOverviewPayment(t *testing.T, ctx context.Context, application *composedApplication, now time.Time, key string, payerCustomerID *int64, amountMinor int64) {
	t.Helper()
	var orderID int64
	merchantOrderNo := "M-OVERVIEW-" + key
	if payerCustomerID == nil {
		if err := application.pool.Native().QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)
			VALUES('wechat_pay','overview-canonical',$1,$2,NULL,NULL,$3,'CNY','paid','history',false,$4,1,$5,$5) RETURNING id`, key, merchantOrderNo, amountMinor, make([]byte, 32), now).Scan(&orderID); err != nil {
			t.Fatal(err)
		}
	} else if err := application.pool.Native().QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at)
		VALUES('wechat_pay','overview-canonical',$1,$2,$3,$3,$4,'CNY','paid','native',true,1,$5,$5) RETURNING id`, key, merchantOrderNo, *payerCustomerID, amountMinor, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if payerCustomerID == nil {
		if _, err := application.pool.Native().Exec(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,paid_confirmed_at,created_at,updated_at,historical)
			VALUES($1,'wechat_pay','mini_program',$2,NULL,NULL,NULL,$3,'CNY','paid',1,$4,$4,$4,true)`, orderID, merchantOrderNo, amountMinor, now); err != nil {
			t.Fatal(err)
		}
		return
	}
	var identityID int64
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at,created_at,updated_at)
		VALUES($1,'mp_openid','wechat-app:overview-canonical',$2,'verified','wechat_miniprogram',1,$3,$3,$3) RETURNING id`, *payerCustomerID, key, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,paid_confirmed_at,created_at,updated_at,historical)
		VALUES($1,'wechat_pay','mini_program',$2,$3,$4,$4,$5,'CNY','paid',1,$6,$6,$6,false)`, orderID, merchantOrderNo, identityID, *payerCustomerID, amountMinor, now); err != nil {
		t.Fatal(err)
	}
}

func seedAdminOverviewPayerScale(t *testing.T, ctx context.Context, application *composedApplication, now time.Time, count int) {
	t.Helper()
	if count < 1 {
		t.Fatal("canonical payer scale count must be positive")
	}
	_, err := application.pool.Native().Exec(ctx, `WITH inserted_customers AS (
		INSERT INTO customers(created_at,updated_at)
		SELECT $1,$1 FROM generate_series(1,$2)
		RETURNING id
	), inserted_identities AS (
		INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at,created_at,updated_at)
		SELECT id,'mp_openid','wechat-app:overview-canonical-scale','payer-'||id,'verified','wechat_miniprogram',1,$1,$1,$1
		FROM inserted_customers
		RETURNING id,customer_id
	), inserted_orders AS (
		INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at)
		SELECT 'wechat_pay','overview-canonical-scale','payer-'||customers.id,'M-OVERVIEW-SCALE-'||customers.id,customers.id,customers.id,1,'CNY','paid','native',true,1,$1,$1
		FROM inserted_customers customers
		JOIN inserted_identities identities ON identities.customer_id=customers.id
		RETURNING id,payer_customer_id,merchant_order_no
	)
	INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,paid_confirmed_at,created_at,updated_at,historical)
	SELECT orders.id,'wechat_pay','mini_program',orders.merchant_order_no,identities.id,orders.payer_customer_id,orders.payer_customer_id,1,'CNY','paid',1,$1,$1,$1,false
	FROM inserted_orders orders
	JOIN inserted_identities identities ON identities.customer_id=orders.payer_customer_id`, now, count)
	if err != nil {
		t.Fatal(err)
	}
}

type mergeAfterPaidOverviewStore struct {
	repository         *paymentstore.Repository
	afterFirstPaidRead func() error
}

func (store mergeAfterPaidOverviewStore) ReadPaidOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.PaidOverview, error) {
	facts, err := store.repository.ReadPaidOverview(ctx, window)
	if err != nil || store.afterFirstPaidRead == nil {
		return facts, err
	}
	return facts, store.afterFirstPaidRead()
}

func (store mergeAfterPaidOverviewStore) ReadPaidOverviewPayerPage(ctx context.Context, window paymentport.OverviewWindow, after customerdomain.CustomerID, limit int) (paymentport.PaidOverviewPayerPage, error) {
	return store.repository.ReadPaidOverviewPayerPage(ctx, window, after, limit)
}

func (store mergeAfterPaidOverviewStore) ReadPaidOverviewRecords(ctx context.Context, window paymentport.OverviewWindow, after *paymentport.PaidOverviewRecordCursor, limit int) (paymentport.PaidOverviewRecordPage, error) {
	return store.repository.ReadPaidOverviewRecords(ctx, window, after, limit)
}

func (store mergeAfterPaidOverviewStore) ReadRefundOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.RefundOverview, error) {
	return store.repository.ReadRefundOverview(ctx, window)
}

func overviewAuthenticatedGET(t *testing.T, handler http.Handler, session, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertAdminOverviewResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Range struct {
			Timezone string `json:"timezone"`
		} `json:"range"`
		Paid struct {
			Status                  string `json:"status"`
			OrderCount              int64  `json:"order_count"`
			DistinctCanonicalPayers *int64 `json:"distinct_canonical_payers"`
			Gross                   []struct {
				AmountMinor int64  `json:"amount_minor"`
				Currency    string `json:"currency"`
			} `json:"gross"`
		} `json:"paid"`
		Customers struct {
			Status string `json:"status"`
			Count  int64  `json:"new_canonical_customers"`
		} `json:"customers"`
		Refunds struct {
			Status    string `json:"status"`
			Completed int64  `json:"completed_count"`
		} `json:"refunds"`
		Distribution struct {
			Status          string `json:"status"`
			Unsettled       int64  `json:"current_unsettled_minor"`
			ExceptionOrders int64  `json:"current_exception_order_count"`
		} `json:"distribution"`
		Todos struct {
			Status string `json:"status"`
			Items  []struct {
				Code  string `json:"code"`
				Count int64  `json:"count"`
				Href  string `json:"href"`
			} `json:"items"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Range.Timezone != "Asia/Shanghai" || body.Paid.Status != "ready" || body.Paid.OrderCount != 3 || body.Paid.DistinctCanonicalPayers == nil || *body.Paid.DistinctCanonicalPayers != 1 || len(body.Paid.Gross) != 1 || body.Paid.Gross[0].AmountMinor != 2400 || body.Paid.Gross[0].Currency != "CNY" || body.Customers.Status != "ready" || body.Customers.Count != 1 || body.Refunds.Status != "ready" || body.Refunds.Completed != 1 || body.Distribution.Status != "ready" || body.Distribution.Unsettled != 120 || body.Distribution.ExceptionOrders != 1 || body.Todos.Status != "ready" || len(body.Todos.Items) != 1 || body.Todos.Items[0].Code != "distribution_exceptions" || body.Todos.Items[0].Count != 1 || body.Todos.Items[0].Href != "/admin/distribution" {
		t.Fatalf("overview response status=%d body=%s", response.Code, response.Body.String())
	}
}
