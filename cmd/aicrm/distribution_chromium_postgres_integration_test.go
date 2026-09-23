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
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestPostgreSQLDistributionChromiumJourney is the Distribution acceptance
// fixture: real composition, PostgreSQL-owned Distribution facts, the actual
// release documents and a headless Chrome session.  The WeChat Pay channel is
// configured solely to open the scoped Distribution composition gate; no
// provider request or profit-sharing effect is enabled by this fixture.
func TestPostgreSQLDistributionChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Distribution Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key, cert := distributionFixturePaymentCredentials(t)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	disabledServer := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer disabledServer.Close()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	runtimeFor := func(origin string, profitSharingEnabled bool) platformconfig.Runtime {
		return platformconfig.Runtime{
			Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "1111111111111111111111111111111111111111", WorkerOwner: "distribution-chromium", WorkerLimit: 1,
			GroupOps:  platformconfig.GroupOps{WebhookSecret: "distribution-chromium-webhook"},
			Survey:    platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey, OAuthEnabled: true, OAuthAppID: "wx-distribution-h5", OAuthSecret: "fixture-h5-secret", OAuthOpenPlatformID: "distribution-open-platform", OAuthScope: "snsapi_userinfo"},
			Effects:   platformconfig.Effects{ProviderEnabled: true},
			WeChatPay: platformconfig.WeChatPay{Enabled: true, AppID: "wx-distribution-browser", AppSecret: "fixture-secret", AppScope: "wechat-app:distribution-browser", H5OAuthEnabled: true, H5AppID: "wx-distribution-h5", H5AppSecret: "fixture-h5-secret", H5AppScope: "wechat-app:wx-distribution-h5", OrderContactDataKey: dataKey, MerchantID: "fixture-mch", MerchantSerial: "fixture-merchant", PrivateKeyPath: key, PlatformCertPath: cert, APIV3Key: "0123456789abcdef0123456789abcdef", ProfitSharingEnabled: profitSharingEnabled, ProfitSharingAuthMode: "certificate"},
			Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "distribution-admin", Password: "distribution-admin-password", DisplayName: "Distribution Admin"},
		}
	}
	application, err := compose(ctx, runtimeFor("https://"+server.Listener.Addr().String(), true))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	disabledApplication, err := compose(ctx, runtimeFor("https://"+disabledServer.Listener.Addr().String(), false))
	if err != nil {
		t.Fatal(err)
	}
	defer disabledApplication.Close()
	assertDistributionH5OAuthStart(t, application.handler)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "distribution-admin", Password: "distribution-admin-password", DisplayName: "Distribution Admin"}); err != nil {
		t.Fatal(err)
	}
	seed := seedDistributionChromiumFacts(t, ctx, application)
	assertDistributionOrderProviderCollisionReadModel(t, ctx, application, seed)
	assertDistributionAdminDetailFacts(t, ctx, application, seed)
	assertDistributionAdminDeadlineWarningReadModel(t, ctx, application, seed.commissionID)
	var overviewFailures atomic.Int32
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/admin/overview" && request.URL.Query().Get("period") == "30d" && overviewFailures.Add(1) == 1 {
			http.Error(writer, "overview fixture unavailable", http.StatusServiceUnavailable)
			return
		}
		application.handler.ServeHTTP(writer, request)
	})
	disabledServer.Config.Handler = disabledApplication.handler
	server.StartTLS()
	disabledServer.StartTLS()
	journey := filepath.Join(repository, "cmd", "aicrm", "distribution_chromium_journey.mjs")
	runJourney := func(phase, want string) {
		t.Helper()
		command := exec.CommandContext(ctx, "node", journey)
		command.Env = append(os.Environ(), "AICRM_DISTRIBUTION_BROWSER_PHASE="+phase, "AICRM_DISTRIBUTION_BROWSER_URL="+server.URL, "AICRM_DISTRIBUTION_BROWSER_DISABLED_URL="+disabledServer.URL, "AICRM_DISTRIBUTION_BROWSER_SESSION="+seed.session, "AICRM_DISTRIBUTION_BROWSER_PROMOTION="+seed.promotion, "AICRM_DISTRIBUTION_BROWSER_PRODUCT="+seed.productCode, "AICRM_DISTRIBUTION_BROWSER_PRODUCT_ID="+strconv.FormatInt(seed.productID, 10), "AICRM_DISTRIBUTION_BROWSER_APPLICATION_TARGET_ID="+strconv.FormatInt(seed.applicationTargetID, 10), "AICRM_DISTRIBUTION_BROWSER_CSRF="+seed.csrf, "AICRM_DISTRIBUTION_BROWSER_DETAIL_ATTRIBUTION="+strconv.FormatInt(seed.detailAttributionID, 10), "AICRM_DISTRIBUTION_BROWSER_DETAIL_EXCEPTION="+strconv.FormatInt(seed.detailExceptionID, 10), "AICRM_DISTRIBUTION_BROWSER_CONFIRMATION_EXCEPTION="+strconv.FormatInt(seed.confirmationExceptionID, 10), "AICRM_DISTRIBUTION_BROWSER_CONFIRMATION_FAILURE_EXCEPTION="+strconv.FormatInt(seed.confirmationFailureExceptionID, 10), "AICRM_DISTRIBUTION_BROWSER_DETAIL_CREATED_AT="+seed.detailCreatedAt.Format(time.RFC3339Nano), "AICRM_DISTRIBUTION_BROWSER_SETTLEMENT_CONFIRMED_AT="+seed.detailSettlementConfirmedAt.Format(time.RFC3339Nano), "AICRM_DISTRIBUTION_BROWSER_ORDER_COLLISION_REFERENCE="+seed.orderCollisionReference, "AICRM_DISTRIBUTION_BROWSER_ORDER_SETTLED_AT="+seed.orderSettledAt.Format(time.RFC3339Nano), "AICRM_DISTRIBUTION_BROWSER_EARNINGS_PRODUCT="+seed.earningsProduct, "AICRM_DISTRIBUTION_BROWSER_EARNINGS_ORDER="+seed.earningsOrderReference, "AICRM_DISTRIBUTION_BROWSER_EARNINGS_GROSS_MINOR="+strconv.FormatInt(seed.earningsGrossMinor, 10), "AICRM_DISTRIBUTION_BROWSER_EARNINGS_COMMISSION_MINOR="+strconv.FormatInt(seed.earningsCommissionMinor, 10), "AICRM_DISTRIBUTION_BROWSER_ADMIN_DISPLAY_NAME="+seed.adminDisplayName, "AICRM_DISTRIBUTION_BROWSER_ADMIN=distribution-admin", "AICRM_DISTRIBUTION_BROWSER_PASSWORD=distribution-admin-password")
		output, runErr := command.CombinedOutput()
		if runErr != nil || !strings.Contains(string(output), want) {
			t.Fatalf("Distribution Chromium %s phase err=%v output=%s", phase, runErr, strings.TrimSpace(string(output)))
		}
	}
	runJourney("registration", "distribution_chromium: REGISTERED")
	assertDistributionReceiverWorkerProjection(t, ctx, application, seed)
	seed = seedDistributionChromiumRegisteredEarnings(t, ctx, application, seed)
	assertDistributionRegisteredEarningsReadModel(t, ctx, application, seed)
	runJourney("ready", "distribution_chromium: PASS")
	assertDistributionRegistrationAndCredentialFacts(t, ctx, application, seed)
	assertDistributionPromotionProductsStrictlyFilter(t, ctx, application, seed)
	assertDistributionAdminConfirmationFacts(t, ctx, application, seed)
}

type distributionChromiumSeed struct {
	session, promotion, productCode, csrf                   string
	adminDisplayName                                        string
	registrationCustomerID                                  int64
	productID, applicationTargetID                          int64
	commissionID, detailAttributionID                       int64
	detailExceptionID                                       int64
	confirmationExceptionID, confirmationFailureExceptionID int64
	receiverEffectID                                        int64
	detailCreatedAt, detailSettlementConfirmedAt            time.Time
	orderCollisionReference                                 string
	orderSettledAt                                          time.Time
	earningsProduct                                         string
	earningsOrderReference                                  string
	earningsGrossMinor, earningsCommissionMinor             int64
	receiverCount, effectCount, intentCount                 int64
}

func assertDistributionH5OAuthStart(t *testing.T, handler http.Handler) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Fdistribution%3Fproduct_id%3D1%26product_type%3Dstandard_product", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 MicroMessenger")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusFound || !strings.HasPrefix(response.Header().Get("Location"), "https://open.weixin.qq.com/connect/oauth2/authorize?") {
		t.Fatalf("payment H5 OAuth start status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
}

func seedDistributionChromiumFacts(t *testing.T, ctx context.Context, application *composedApplication) distributionChromiumSeed {
	t.Helper()
	pool := application.pool.Native()
	now := time.Now().UTC().Truncate(time.Microsecond)
	const code = "distribution-browser-product"
	const token = "dpc_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	session := "dist_" + "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	var registrationCustomer, detailCustomer, identityID, product, applicationTarget, distributor, policy, credential, attribution, receiverEffectID int64
	if err := pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&registrationCustomer); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&detailCustomer); err != nil {
		t.Fatal(err)
	}
	const adminDisplayName = "浏览器分销员昵称"
	if _, err := pool.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,source,updated_at) VALUES($1,'active','注册分销员昵称','distribution-chromium',$2),($3,'active',$4,'distribution-chromium',$2)`, registrationCustomer, now, detailCustomer, adminDisplayName); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'mp_openid','wechat-app:distribution-browser','distribution-browser-openid','verified','distribution-browser-fixture',1,$2) RETURNING id`, registrationCustomer, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('payment',$1,$2,$3,$4,$5,$6,'queued') RETURNING id`, effectport.KindWeChatPayReceiverAdd, effectport.Hash("distribution-chromium-receiver-source"), effectport.Hash("distribution-chromium-receiver-target"), effectport.Hash("distribution-chromium-receiver-payload"), effectport.Hash("distribution-chromium-receiver-policy"), effectport.Hash("distribution-chromium-receiver-envelope")).Scan(&receiverEffectID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,external_effect_id,version,created_at,updated_at) VALUES($1,$2,'wx-distribution-browser','wechat-app:distribution-browser','mini_program',$3,'accepted',$4,1,$5,$5)`, registrationCustomer, identityID, string(effectport.Hash("payment.profit-sharing.receiver.account.v1", "wx-distribution-browser", "wechat-app:distribution-browser", "distribution-browser-openid")), receiverEffectID, now); err != nil {
		t.Fatal(err)
	}
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`
	if err := pool.QueryRow(ctx, "INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES($1,'分销浏览器商品','真实分销浏览器夹具',9900,'CNY',10,1,$2::jsonb) RETURNING id", code, projection).Scan(&product); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('distribution-browser-application-target','申请未购商品','仅用于申请上下文夹具',9900,'CNY',10,1,$1::jsonb) RETURNING id", projection).Scan(&applicationTarget); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,receiver_reference,receiver_app_id,receiver_ready,receiver_reason,receiver_checked_at,registered_at,version,created_at,updated_at) VALUES($1,'DISTBROWSER01','v1',TRUE,'receiver-browser','wx-distribution-browser',TRUE,'',$2,$2,1,$2,$2) RETURNING id", detailCustomer, now).Scan(&distributor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,'standard_product',TRUE,1000,7,1,$2,$2) RETURNING id", product, now).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,'standard_product',TRUE,1000,7,1,$2,$2)", applicationTarget, now); err != nil {
		t.Fatal(err)
	}
	var qualificationOrder int64
	if err := pool.QueryRow(ctx, "INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','distribution-browser','qualification','M-distribution-qualification',$1,$1,9900,'CNY','paid','native',true,2,$2,$2) RETURNING id", registrationCustomer, now).Scan(&qualificationOrder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,$3,'分销浏览器商品',9900,1,9900)", qualificationOrder, product, code); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',$2,$3,'分销浏览器商品',1,0,9900,0,9900,'CNY',false,'',$4,$4)", qualificationOrder, product, code, now); err != nil {
		t.Fatal(err)
	}
	paidDigest := sha256.Sum256([]byte("distribution-browser-qualified-paid"))
	if _, err := pool.Exec(ctx, "INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)", qualificationOrder, paidDigest[:], now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-distribution-qualification',$2,$3,$3,9900,'CNY','paid',false,1,$4,$4,$4)", qualificationOrder, identityID, registrationCustomer, now); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(token))
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,$2,'standard_product',$3,'active',$4,$5) RETURNING id", distributor, product, digest[:], now, now.Add(24*time.Hour)).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	var commission, detailAttribution, detailCommission, detailException int64
	detailCreatedAt := now.Add(2 * time.Minute)
	detailSettlementConfirmedAt := detailCreatedAt.Add(time.Minute)
	for offset := int64(0); offset < 11; offset++ {
		attributedAt := now.Add(time.Duration(offset) * time.Second)
		orderID := int64(9001) + offset
		initial, current, successfulRefund, status := int64(990), int64(990), int64(0), "pending"
		if offset == 9 {
			initial, current, successfulRefund, status = 990, 495, 4950, "exception"
		}
		if offset == 10 {
			initial, current, status = 0, 0, "zero_commission"
		}
		if err := pool.QueryRow(ctx, "INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,$2,'分销浏览器商品',$3,$4,$5,'eligible',$6,1,1000,7,$7) RETURNING id", orderID, code, distributor, credential, "order:"+strconv.FormatInt(orderID, 10)+":line:1", policy, attributedAt).Scan(&attribution); err != nil {
			t.Fatal(err)
		}
		var insertedCommission int64
		if err := pool.QueryRow(ctx, "INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,9900,$4,$5,$6,0,1000,$7,$8,$9,'','','',1,$7,$7) RETURNING id", attribution, orderID, distributor, successfulRefund, initial, current, attributedAt, attributedAt.Add(7*24*time.Hour), status).Scan(&insertedCommission); err != nil {
			t.Fatal(err)
		}
		if offset == 0 {
			commission = insertedCommission
		}
		if offset != 9 {
			continue
		}
		detailAttribution, detailCommission = attribution, insertedCommission
		if _, err := pool.Exec(ctx, "INSERT INTO distribution_commission_adjustments(commission_id,kind,delta_minor,resulting_payable_minor,reason,source_reference,occurred_at) VALUES($1,'buyer_refund',-495,495,'buyer_refund','payment:browser:9010',$2)", detailCommission, detailCreatedAt); err != nil {
			t.Fatal(err)
		}
		var settlementID int64
		if err := pool.QueryRow(ctx, "INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,payment_instruction_reference,payment_effect_reference,state,provider_deadline_at,version,created_at,updated_at) VALUES($1,'dstl_browser_partial',495,'CNY','payment:browser:9010','psinst_1','','outcome_unknown',$2,1,$3,$3) RETURNING id", detailCommission, detailCreatedAt.Add(24*time.Hour), detailCreatedAt).Scan(&settlementID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,payment_instruction_reference,payment_effect_reference,state,provider_deadline_at,version,created_at,updated_at) VALUES($1,'dstl_browser_confirmed',100,'CNY','payment:browser:9010','psinst_confirmed','','receiver_succeeded',$2,1,$3,$4)", detailCommission, detailCreatedAt.Add(24*time.Hour), detailCreatedAt, detailCreatedAt.Add(8*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('distribution.settlement_paid.v1','commission',$1,'worker:distribution-due',jsonb_build_object('settlement_reference','dstl_browser_confirmed'),$2)", detailCommission, detailSettlementConfirmedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,payment_instruction_reference,payment_effect_reference,state,provider_deadline_at,version,created_at,updated_at) VALUES($1,'dstl_browser_unrecorded',200,'CNY','payment:browser:9010','psinst_unrecorded','','receiver_succeeded',$2,1,$3,$4)", detailCommission, detailCreatedAt.Add(24*time.Hour), detailCreatedAt, detailCreatedAt.Add(9*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "INSERT INTO distribution_exceptions(commission_id,settlement_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,$2,'settlement_unknown','open',495,0,495,'settlement_outcome_unknown','reconcile:browser-partial','worker:distribution-due',1,$3,$3) RETURNING id", detailCommission, settlementID, detailCreatedAt).Scan(&detailException); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('distribution.exception_opened.v1','exception',$1,'worker:distribution-due',$2::jsonb,$3)", detailException, `{"reason":"settlement_outcome_unknown","amount_minor":495,"evidence_reference":"reconcile:browser-partial"}`, detailCreatedAt); err != nil {
			t.Fatal(err)
		}
	}
	confirmationException, confirmationFailureException := seedDistributionChromiumAdminConfirmationFacts(t, ctx, pool, distributor, credential, policy, code, now)
	orderCollisionReference, orderSettledAt := seedDistributionChromiumProviderCollision(t, ctx, pool, product, code, distributor, credential, policy, detailCustomer, now)
	sessionDigest := sha256.Sum256([]byte(session))
	if _, err := pool.Exec(ctx, "INSERT INTO distribution_browser_sessions(token_digest,customer_id,identity_id,channel,app_id,app_scope,expires_at,created_at) VALUES($1,$2,$3,'mini_program','wx-distribution-browser','wechat-app:distribution-browser',$4,$5)", sessionDigest[:], registrationCustomer, identityID, now.Add(8*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	var receiverCount, effectCount, intentCount int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_profit_sharing_receivers`).Scan(&receiverCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_profit_sharing_provider_intents`).Scan(&intentCount); err != nil {
		t.Fatal(err)
	}
	return distributionChromiumSeed{session: session, promotion: token, productCode: code, csrf: "distribution-browser-csrf", adminDisplayName: adminDisplayName, registrationCustomerID: registrationCustomer, productID: product, applicationTargetID: applicationTarget, commissionID: commission, detailAttributionID: detailAttribution, detailExceptionID: detailException, confirmationExceptionID: confirmationException, confirmationFailureExceptionID: confirmationFailureException, receiverEffectID: receiverEffectID, detailCreatedAt: detailCreatedAt, detailSettlementConfirmedAt: detailSettlementConfirmedAt, orderCollisionReference: orderCollisionReference, orderSettledAt: orderSettledAt, earningsProduct: "分销浏览器商品", earningsGrossMinor: 9900000, earningsCommissionMinor: 990000, receiverCount: receiverCount, effectCount: effectCount, intentCount: intentCount}
}

// seedDistributionChromiumAdminConfirmationFacts adds two local, already-paid
// after-sales cases. They exercise only Distribution's own append-only admin
// ledger: the browser never calls a provider.
func seedDistributionChromiumAdminConfirmationFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributorID, credentialID, policyID int64, productCode string, now time.Time) (int64, int64) {
	t.Helper()
	ids := make([]int64, 0, 2)
	for index, orderID := range []int64{9401, 9402} {
		// The admin service writes at its current clock instant, so fixture facts
		// must not be timestamped into the future or PostgreSQL will correctly
		// reject an updated_at-before-created_at mutation.
		at := now.Add(-time.Duration(2-index) * time.Minute)
		var attributionID, commissionID, exceptionID int64
		if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at)
			VALUES($1,1,$2,'分销确认夹具商品',$3,$4,$5,'eligible',$6,1,1000,7,$7) RETURNING id`, orderID, productCode, distributorID, credentialID, "order:"+strconv.FormatInt(orderID, 10)+":line:1", policyID, at).Scan(&attributionID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at)
			VALUES($1,$2,1,$3,9900,9900,990,0,495,1000,$4,$5,'exception','','','buyer_refund_after_paid',1,$4,$4) RETURNING id`, attributionID, orderID, distributorID, at, at.Add(7*24*time.Hour)).Scan(&commissionID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at)
			VALUES($1,'buyer_refund_after_paid','open',0,495,495,'buyer_refund_after_paid',$2,'distribution-chromium',1,$3,$3) RETURNING id`, commissionID, "refund:confirmation:"+strconv.FormatInt(orderID, 10), at).Scan(&exceptionID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, exceptionID)
	}
	return ids[0], ids[1]
}

// seedDistributionChromiumProviderCollision creates two real Order rows with
// the same merchant reference. Payment identity is the provider/reference
// pair, so the fixture proves the V3 list and details never use the merchant
// string as a cache key or a provider inference hint.
func seedDistributionChromiumProviderCollision(t *testing.T, ctx context.Context, pool *pgxpool.Pool, productID int64, productCode string, distributorID, credentialID, policyID, customerID int64, now time.Time) (string, time.Time) {
	t.Helper()
	const reference = "M-distribution-browser-provider-collision"
	const grossMinor int64 = 1000
	const rateBasisPoints int32 = 1000
	const waitDays int32 = 7
	paidAt := now.Add(-8 * 24 * time.Hour)
	settledAt := paidAt.Add(time.Duration(waitDays)*24*time.Hour + time.Hour)
	expectedCommission, err := distributiondomain.CalculateCommission(grossMinor, rateBasisPoints)
	if err != nil {
		t.Fatal(err)
	}
	var wechatOrderID, alipayOrderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at)
		VALUES('wechat_pay','distribution-browser-collision','provider-wechat',$1,$2,$2,$3,'CNY','paid','native',true,2,$4,$4) RETURNING id`, reference, customerID, grossMinor, paidAt).Scan(&wechatOrderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at)
		VALUES('alipay','distribution-browser-collision','provider-alipay',$1,$2,'CNY','paid','history',false,1,$3,$3) RETURNING id`, reference, grossMinor, paidAt.Add(time.Second)).Scan(&alipayOrderID); err != nil {
		t.Fatal(err)
	}
	for _, order := range []struct {
		id, productLine int64
		name            string
	}{{wechatOrderID, 1, "分账成功商品"}, {alipayOrderID, 1, "分账待核验商品"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
			VALUES($1,$2,$3,1,$4,$5,$6,1,$6)`, order.id, order.productLine, productID, productCode, order.name, grossMinor); err != nil {
			t.Fatal(err)
		}
	}
	var wechatAttribution, alipayAttribution, wechatCommission, alipayCommission int64
	for _, fact := range []struct {
		orderID      int64
		productName  string
		commissionID *int64
		attribution  *int64
		attributedAt time.Time
		settlementAt time.Time
		succeeded    bool
		reference    string
	}{
		{wechatOrderID, "分账成功商品", &wechatCommission, &wechatAttribution, paidAt, settledAt, true, "dstl_browser_succeeded"},
		{alipayOrderID, "分账待核验商品", &alipayCommission, &alipayAttribution, paidAt.Add(time.Second), settledAt.Add(time.Minute), false, "dstl_browser_outcome_unknown"},
	} {
		if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at)
			VALUES($1,1,$2,$3,$4,$5,$6,'eligible',$7,1,$8,$9,$10) RETURNING id`, fact.orderID, productCode, fact.productName, distributorID, credentialID, "order:"+strconv.FormatInt(fact.orderID, 10)+":line:1", policyID, rateBasisPoints, waitDays, fact.attributedAt).Scan(fact.attribution); err != nil {
			t.Fatal(err)
		}
		attribution := distributiondomain.Attribution{ID: *fact.attribution, OrderID: fact.orderID, OrderItemLine: 1, ProductCode: productCode, ProductName: fact.productName, DistributorID: distributorID, PromotionCredentialID: credentialID, QualificationEvidenceRef: "order:" + strconv.FormatInt(fact.orderID, 10) + ":line:1", QualificationState: distributiondomain.QualificationEligible, PolicyVersion: 1, CommissionRateBasisPoints: rateBasisPoints, WaitDays: waitDays, AttributedAt: fact.attributedAt}
		commission, err := distributiondomain.NewCommission(attribution, grossMinor, fact.attributedAt)
		if err != nil || commission.InitialMinor != expectedCommission || commission.CurrentPayableMinor != expectedCommission {
			t.Fatalf("construct consistent collision commission err=%v commission=%+v expected=%d", err, commission, expectedCommission)
		}
		settling, err := commission.BeginSettlement(commission.Version, fact.settlementAt)
		if err != nil {
			t.Fatalf("begin collision settlement: %v", err)
		}
		if fact.succeeded {
			commission, err = settling.ConfirmReceiverPaid(settling.Version, expectedCommission, fact.settlementAt.Add(time.Minute))
		} else {
			commission, err = settling.MarkException(settling.Version, "settlement_outcome_unknown", fact.settlementAt)
		}
		if err != nil {
			t.Fatalf("transition collision settlement: %v", err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at)
			VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING id`, *fact.attribution, fact.orderID, distributorID, commission.OriginalItemPaidMinor, commission.SuccessfulRefundMinor, commission.InitialMinor, commission.CurrentPayableMinor, commission.PaidMinor, commission.CommissionRateBasisPoints, commission.PaidConfirmedAt, commission.DueAt, string(commission.Status), commission.HoldReason, commission.CancelReason, commission.ExceptionReason, commission.Version, commission.CreatedAt, commission.UpdatedAt).Scan(fact.commissionID); err != nil {
			t.Fatal(err)
		}
		settlementState := "outcome_unknown"
		if fact.succeeded {
			settlementState = "receiver_succeeded"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,state,provider_deadline_at,version,created_at,updated_at)
			VALUES($1,$2,$3,'CNY',$4,$5,$6,1,$7,$7)`, *fact.commissionID, fact.reference, expectedCommission, "payment:browser:"+fact.reference, settlementState, fact.settlementAt.Add(24*time.Hour), commission.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	settledAt = settledAt.Add(time.Minute)
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at)
		VALUES('distribution.settlement_paid.v1','commission',$1,'fixture:distribution-browser',$2::jsonb,$3)`, wechatCommission, `{"settlement_reference":"dstl_browser_succeeded"}`, settledAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at)
		VALUES($1,'settlement_unknown','open',$2,0,$2,'settlement_outcome_unknown','reconcile:browser-provider-alipay','fixture:distribution-browser',1,$3,$3)`, alipayCommission, expectedCommission, settledAt); err != nil {
		t.Fatal(err)
	}
	return reference, settledAt
}

func assertDistributionOrderProviderCollisionReadModel(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) {
	t.Helper()
	session, _ := adminAccessLogin(t, application.handler, "distribution-admin", "distribution-admin-password")
	read := func(provider, responseProvider, productName, settlementReference string, confirmed bool) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/orders/"+seed.orderCollisionReference+"?provider="+provider, nil)
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, `"merchant_order_no":"`+seed.orderCollisionReference+`"`) || !strings.Contains(body, `"provider":"`+responseProvider+`"`) || !strings.Contains(body, `"product_name":"`+productName+`"`) || !strings.Contains(body, `"reference":"`+settlementReference+`"`) {
			t.Fatalf("provider-scoped order read provider=%s status=%d body=%s", provider, response.Code, body)
		}
		if confirmed && !strings.Contains(body, `"settlement_confirmed_at":"`) {
			t.Fatalf("successful split read lost audited confirmation: %s", body)
		}
		if !confirmed && strings.Contains(body, `"settlement_confirmed_at":"`) {
			t.Fatalf("outcome-unknown split borrowed a confirmation time: %s", body)
		}
		if !strings.Contains(body, `"initial_minor":100`) || !strings.Contains(body, `"current_payable_minor":100`) {
			t.Fatalf("provider-scoped fixture must retain the calculated ten-percent commission: %s", body)
		}
		if confirmed && !strings.Contains(body, `"paid_minor":100`) {
			t.Fatalf("successful split must retain its confirmed split amount: %s", body)
		}
		if !confirmed && !strings.Contains(body, `"paid_minor":0`) {
			t.Fatalf("outcome-unknown split must retain its unpaid commission amount: %s", body)
		}
	}
	read("wechat_pay", "wechat", "分账成功商品", "dstl_browser_succeeded", true)
	read("alipay", "alipay", "分账待核验商品", "dstl_browser_outcome_unknown", false)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/orders?limit=50", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `"merchant_order_no":"`+seed.orderCollisionReference+`"`) < 2 || !strings.Contains(body, `"detail_url":"/admin/orderDetail.html?id=`+seed.orderCollisionReference+`\u0026provider=wechat"`) || !strings.Contains(body, `"detail_url":"/admin/orderDetail.html?id=`+seed.orderCollisionReference+`\u0026provider=alipay"`) {
		t.Fatalf("provider collision list did not serialize both server-owned detail URLs status=%d body=%s", response.Code, body)
	}
}

// seedDistributionChromiumRegisteredEarnings appends one complete, non-self
// referral fact after the public journey registers its distributor and Payment
// projects the receiver ready.  The fixture uses a real PostgreSQL UoW and
// durable Order/Payment/Distribution records; it invokes neither a Provider
// nor a split effect.
func seedDistributionChromiumRegisteredEarnings(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) distributionChromiumSeed {
	t.Helper()
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	const quantity int64 = 1000
	const grossMinor int64 = 9900000
	const commissionMinor int64 = 990000
	now := time.Now().UTC().Truncate(time.Microsecond)
	var orderID int64
	err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		var distributorID, policyID, buyerID, buyerIdentityID, credentialID, attributionID int64
		if txErr = tx.QueryRow(txctx, `SELECT id FROM distribution_distributors WHERE customer_id=$1`, seed.registrationCustomerID).Scan(&distributorID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `SELECT id FROM distribution_product_policies WHERE product_id=$1 AND product_type='standard_product' AND enabled`, seed.productID).Scan(&policyID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&buyerID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'mp_openid','wechat-app:distribution-browser','distribution-browser-earned-buyer','verified','distribution-browser-fixture',1,$2) RETURNING id`, buyerID, now).Scan(&buyerIdentityID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','distribution-browser','registered-earned-order','M-distribution-browser-earned',$1,$1,$2,'CNY','paid','native',true,2,$3,$3) RETURNING id`, buyerID, grossMinor, now).Scan(&orderID); txErr != nil {
			return txErr
		}
		if _, txErr = tx.Exec(txctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,$3,$4,9900,$5,$6)`, orderID, seed.productID, seed.productCode, seed.earningsProduct, quantity, grossMinor); txErr != nil {
			return txErr
		}
		if _, txErr = tx.Exec(txctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,profit_sharing_required,reserved_at,created_at) VALUES($1,'standard_product',$2,$3,$4,1,0,$5,0,$5,'CNY',false,'',true,$6,$6)`, orderID, seed.productID, seed.productCode, seed.earningsProduct, grossMinor, now); txErr != nil {
			return txErr
		}
		paidDigest := sha256.Sum256([]byte("distribution-browser-registered-earned-paid"))
		if _, txErr = tx.Exec(txctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, paidDigest[:], now); txErr != nil {
			return txErr
		}
		if _, txErr = tx.Exec(txctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-distribution-browser-earned',$2,$3,$3,$4,'CNY','paid',false,1,$5,$5,$5)`, orderID, buyerIdentityID, buyerID, grossMinor, now); txErr != nil {
			return txErr
		}
		credentialDigest := sha256.Sum256([]byte("distribution-browser-registered-earned-credential"))
		if txErr = tx.QueryRow(txctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,$2,'standard_product',$3,'active',$4,$5) RETURNING id`, distributorID, seed.productID, credentialDigest[:], now, now.Add(24*time.Hour)).Scan(&credentialID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,$2,$3,$4,$5,$6,'eligible',$7,1,1000,7,$8) RETURNING id`, orderID, seed.productCode, seed.earningsProduct, distributorID, credentialID, "order:"+strconv.FormatInt(orderID, 10)+":line:1", policyID, now).Scan(&attributionID); txErr != nil {
			return txErr
		}
		_, txErr = tx.Exec(txctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,$4,0,$5,$5,0,1000,$6,$7,'pending','','','',1,$6,$6)`, attributionID, orderID, distributorID, grossMinor, commissionMinor, now, now.Add(7*24*time.Hour))
		return txErr
	})
	if err != nil {
		t.Fatal(err)
	}
	seed.earningsOrderReference = "order-" + strconv.FormatInt(orderID, 10)
	return seed
}

func assertDistributionRegisteredEarningsReadModel(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/distribution/earnings", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_distribution_session", Value: seed.session})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"gross_paid_sales_minor":9900000`) || !strings.Contains(response.Body.String(), `"initial_commission_minor":990000`) {
		t.Fatalf("registered earnings status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/distribution/commissions?limit=50", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_distribution_session", Value: seed.session})
	response = httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"order_reference":"`+seed.earningsOrderReference+`"`) || !strings.Contains(response.Body.String(), `"product_name":"`+seed.earningsProduct+`"`) || !strings.Contains(response.Body.String(), `"initial_minor":990000`) {
		t.Fatalf("registered commissions status=%d body=%s", response.Code, response.Body.String())
	}
}

func assertDistributionAdminConfirmationFacts(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) {
	t.Helper()
	pool := application.pool.Native()
	var successfulAdjustments, successfulReceipts, successfulAudits, failedAdjustments, failedReceipts int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_commission_adjustments a JOIN distribution_exceptions e ON e.commission_id=a.commission_id WHERE e.id=$1 AND a.kind='manual_recovery'`, seed.confirmationExceptionID).Scan(&successfulAdjustments); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_operation_receipts WHERE operation='recovery' AND result_kind='exception' AND result_id=$1`, seed.confirmationExceptionID).Scan(&successfulReceipts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_audit_events WHERE event_type='distribution.recovery_recorded.v1' AND aggregate_type='exception' AND aggregate_id=$1`, seed.confirmationExceptionID).Scan(&successfulAudits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_commission_adjustments a JOIN distribution_exceptions e ON e.commission_id=a.commission_id WHERE e.id=$1 AND a.kind='manual_recovery'`, seed.confirmationFailureExceptionID).Scan(&failedAdjustments); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_operation_receipts WHERE operation='recovery' AND result_kind='exception' AND result_id=$1`, seed.confirmationFailureExceptionID).Scan(&failedReceipts); err != nil {
		t.Fatal(err)
	}
	if successfulAdjustments != 1 || successfulReceipts != 1 || successfulAudits != 1 || failedAdjustments != 0 || failedReceipts != 0 {
		t.Fatalf("admin confirmation facts success adjustments=%d receipts=%d audits=%d failed adjustments=%d receipts=%d", successfulAdjustments, successfulReceipts, successfulAudits, failedAdjustments, failedReceipts)
	}
}

func assertDistributionRegistrationAndCredentialFacts(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) {
	t.Helper()
	pool := application.pool.Native()
	var distributors, registerReceipts, registerAudits, registerOutbox, credentials, browserCredentials, receipts, audits, outbox, receivers, effects, intents int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_distributors WHERE customer_id=$1`, seed.registrationCustomerID).Scan(&distributors); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_operation_receipts WHERE operation='register' AND actor_scope=$1 AND result_kind='distributor'`, "customer:"+strconv.FormatInt(seed.registrationCustomerID, 10)).Scan(&registerReceipts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_audit_events a JOIN distribution_distributors d ON d.id=a.aggregate_id WHERE a.event_type='distribution.distributor_registered.v1' AND a.aggregate_type='distributor' AND d.customer_id=$1`, seed.registrationCustomerID).Scan(&registerAudits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_outbox WHERE event_type='distribution.distributor_registered.v1'`).Scan(&registerOutbox); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_promotion_credentials c JOIN distribution_distributors d ON d.id=c.distributor_id WHERE d.customer_id=$1`, seed.registrationCustomerID).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	// The earnings fixture creates one credential solely to establish a real
	// historical referral. The browser journey must contribute exactly one
	// separately receipted credential, rather than treating that fixture fact
	// as a second browser issuance.
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_operation_receipts r JOIN distribution_promotion_credentials c ON c.id=r.result_id JOIN distribution_distributors d ON d.id=c.distributor_id WHERE r.operation='credential' AND r.actor_scope=$1 AND r.result_kind='credential' AND d.customer_id=$2`, "customer:"+strconv.FormatInt(seed.registrationCustomerID, 10), seed.registrationCustomerID).Scan(&browserCredentials); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_operation_receipts WHERE operation='credential' AND actor_scope=$1 AND result_kind='credential'`, "customer:"+strconv.FormatInt(seed.registrationCustomerID, 10)).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_audit_events a JOIN distribution_promotion_credentials c ON c.id=a.aggregate_id JOIN distribution_distributors d ON d.id=c.distributor_id WHERE a.event_type='distribution.credential_issued.v1' AND a.aggregate_type='credential' AND d.customer_id=$1`, seed.registrationCustomerID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_outbox WHERE event_type='distribution.credential_issued.v1'`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_profit_sharing_receivers`).Scan(&receivers); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_profit_sharing_provider_intents`).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if distributors != 1 || registerReceipts != 1 || registerAudits != 1 || registerOutbox != 1 || credentials != 2 || browserCredentials != 1 || receipts != 1 || audits != 1 || outbox != 1 || receivers != seed.receiverCount || effects != seed.effectCount || intents != seed.intentCount {
		t.Fatalf("registration/credential facts distributors=%d registration_receipts=%d registration_audits=%d registration_outbox=%d credentials=%d browser_credentials=%d credential_receipts=%d credential_audits=%d credential_outbox=%d receivers=%d/%d effects=%d/%d intents=%d/%d", distributors, registerReceipts, registerAudits, registerOutbox, credentials, browserCredentials, receipts, audits, outbox, receivers, seed.receiverCount, effects, seed.effectCount, intents, seed.intentCount)
	}
}

// assertDistributionReceiverWorkerProjection completes a seeded Payment
// receiver-add effect through Composition's actual completion sink. The
// terminal Payment fact and the Distribution snapshot share the same UoW;
// public and admin reads therefore become ready without a second H5 prepare
// action or any Provider call.
func assertDistributionReceiverWorkerProjection(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) {
	t.Helper()
	pool := application.pool.Native()
	var distributorID, version, synchronizedAudits, preparedAudits int64
	var ready bool
	if err := pool.QueryRow(ctx, `SELECT id,version,receiver_ready,(SELECT count(*) FROM distribution_audit_events WHERE aggregate_type='distributor' AND aggregate_id=d.id AND event_type='distribution.receiver_status_synchronized.v1'),(SELECT count(*) FROM distribution_audit_events WHERE aggregate_type='distributor' AND aggregate_id=d.id AND event_type='distribution.receiver_prepared.v1') FROM distribution_distributors d WHERE customer_id=$1`, seed.registrationCustomerID).Scan(&distributorID, &version, &ready, &synchronizedAudits, &preparedAudits); err != nil {
		t.Fatal(err)
	}
	if ready || version != 1 || synchronizedAudits != 0 || preparedAudits != 0 {
		t.Fatalf("registered worker-projection baseline distributor=%d ready=%v version=%d synchronized=%d prepared=%d", distributorID, ready, version, synchronizedAudits, preparedAudits)
	}
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	complete := func() {
		t.Helper()
		if err := uow.Within(ctx, func(tx context.Context) error {
			return application.paymentDistribution.CompleteEffect(tx, "eer_"+strconv.FormatInt(seed.receiverEffectID, 10), effectport.Envelope{Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayReceiverAdd}, effectport.Attempt{EffectID: "eer_" + strconv.FormatInt(seed.receiverEffectID, 10), Number: 1, Generation: 1, Fence: 1}, effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("distribution-chromium-receiver-ready")})
		}); err != nil {
			t.Fatal(err)
		}
	}
	complete()
	if err := pool.QueryRow(ctx, `SELECT version,receiver_ready,(SELECT count(*) FROM distribution_audit_events WHERE aggregate_type='distributor' AND aggregate_id=d.id AND event_type='distribution.receiver_status_synchronized.v1'),(SELECT count(*) FROM distribution_audit_events WHERE aggregate_type='distributor' AND aggregate_id=d.id AND event_type='distribution.receiver_prepared.v1') FROM distribution_distributors d WHERE id=$1`, distributorID).Scan(&version, &ready, &synchronizedAudits, &preparedAudits); err != nil {
		t.Fatal(err)
	}
	if !ready || version != 2 || synchronizedAudits != 1 || preparedAudits != 0 {
		t.Fatalf("worker projection readiness=%v version=%d synchronized=%d prepared=%d", ready, version, synchronizedAudits, preparedAudits)
	}
	publicRead := func(path string) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_distribution_session", Value: seed.session})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("public worker projection %s status=%d body=%s", path, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	if body := publicRead("/api/v1/distribution/me"); !strings.Contains(body, `"ready":true`) || strings.Contains(body, `"receiver_not_ready"`) {
		t.Fatalf("public profile did not read worker-ready receiver: %s", body)
	}
	if body := publicRead("/api/v1/distribution/products?limit=20"); !strings.Contains(body, `"product_id":`+strconv.FormatInt(seed.productID, 10)) || !strings.Contains(body, `"promotion_ready":true`) {
		t.Fatalf("promotion products did not read worker-ready receiver: %s", body)
	}
	adminSession, _ := adminAccessLogin(t, application.handler, "distribution-admin", "distribution-admin-password")
	adminRead := func(path string) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: adminSession})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("admin worker projection %s status=%d body=%s", path, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	if body := adminRead("/api/admin/distribution/distributors?limit=50"); !strings.Contains(body, `"display_name":"注册分销员昵称"`) || !strings.Contains(body, `"receiver_ready":true`) {
		t.Fatalf("admin distributor list did not read worker-ready projection: %s", body)
	}
	if body := adminRead("/api/admin/distribution/distributors/" + strconv.FormatInt(distributorID, 10)); !strings.Contains(body, `"public_no"`) || !strings.Contains(body, `"receiver_ready":true`) {
		t.Fatalf("admin distributor detail did not read worker-ready projection: %s", body)
	}
	complete()
	if err := pool.QueryRow(ctx, `SELECT version,(SELECT count(*) FROM distribution_audit_events WHERE aggregate_type='distributor' AND aggregate_id=d.id AND event_type='distribution.receiver_status_synchronized.v1') FROM distribution_distributors d WHERE id=$1`, distributorID).Scan(&version, &synchronizedAudits); err != nil {
		t.Fatal(err)
	}
	if version != 2 || synchronizedAudits != 1 {
		t.Fatalf("replayed worker ready changed distribution projection version=%d synchronized=%d", version, synchronizedAudits)
	}
}

// assertDistributionPromotionProductsStrictlyFilter uses the composed public
// HTTP handler and PostgreSQL facts after the browser has completed actual
// registration.  It proves unpurchased and paid-but-unconfirmed products are
// never serialized as promotion cards; the latter becomes an empty-state fact
// only after the one eligible policy is disabled.
func assertDistributionPromotionProductsStrictlyFilter(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) {
	t.Helper()
	pool := application.pool.Native()
	now := time.Now().UTC().Truncate(time.Microsecond)
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`
	var unpurchasedID, missingConfirmationID int64
	if err := pool.QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('distribution-unpurchased-filter','未购买商品','严格过滤夹具',9900,'CNY',10,1,$1::jsonb) RETURNING id`, projection).Scan(&unpurchasedID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('distribution-confirmation-gap-filter','待核验商品','严格过滤夹具',9900,'CNY',10,1,$1::jsonb) RETURNING id`, projection).Scan(&missingConfirmationID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{unpurchasedID, missingConfirmationID} {
		if _, err := pool.Exec(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,'standard_product',true,1000,7,1,$2,$2)`, id, now); err != nil {
			t.Fatal(err)
		}
	}
	var identityID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM customer_identities WHERE customer_id=$1 AND kind='mp_openid'`, seed.registrationCustomerID).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	var orderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','distribution-confirmation-gap','promotion-filter-gap','M-distribution-confirmation-gap',$1,$1,9900,'CNY','paid','native',true,2,$2,$2) RETURNING id`, seed.registrationCustomerID, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,'distribution-confirmation-gap-filter','待核验商品',9900,1,9900)`, orderID, missingConfirmationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',$2,'distribution-confirmation-gap-filter','待核验商品',1,0,9900,0,9900,'CNY',false,'',$3,$3)`, orderID, missingConfirmationID, now); err != nil {
		t.Fatal(err)
	}
	paidDigest := sha256.Sum256([]byte("distribution-confirmation-gap-paid"))
	if _, err := pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, paidDigest[:], now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-distribution-confirmation-gap',$2,$3,$3,9900,'CNY','paid',false,1,NULL,$4,$4)`, orderID, identityID, seed.registrationCustomerID, now); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/distribution/products?limit=20", nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_distribution_session", Value: seed.session})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("promotion products status=%d body=%s", response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	body := read()
	if !strings.Contains(body, `"product_id":`+strconv.FormatInt(seed.productID, 10)) || strings.Contains(body, "distribution-unpurchased-filter") || strings.Contains(body, "distribution-confirmation-gap-filter") {
		t.Fatalf("strict promotion list leaked unqualified product: %s", body)
	}
	if _, err := pool.Exec(ctx, `UPDATE distribution_product_policies SET enabled=false,version=version+1,updated_at=$2 WHERE product_id=$1 AND product_type='standard_product'`, seed.productID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	body = read()
	if !strings.Contains(body, `"items":[]`) || !strings.Contains(body, `"empty_reason":"qualification_payment_confirmation_missing"`) || strings.Contains(body, "distribution-confirmation-gap-filter") {
		t.Fatalf("strict promotion empty state=%s", body)
	}
}

func assertDistributionAdminDetailFacts(t *testing.T, ctx context.Context, application *composedApplication, seed distributionChromiumSeed) {
	t.Helper()
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := distributionstore.NewPostgreSQL(application.pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	// This is the same commission -> exception lock order used by the admin
	// command path. It proves an optional settlement row does not make the
	// real PostgreSQL `FOR UPDATE OF e` query fail, while accepting Payment's
	// actual psinst_<id> projection.
	if err = uow.Within(ctx, func(tx context.Context) error {
		initial, readErr := repository.ReadAdminExceptionWithin(tx, seed.detailExceptionID, false)
		if readErr != nil {
			return readErr
		}
		if _, readErr = repository.ReadCommissionWithin(tx, initial.CommissionID, true); readErr != nil {
			return readErr
		}
		locked, readErr := repository.ReadAdminExceptionWithin(tx, seed.detailExceptionID, true)
		if readErr != nil {
			return readErr
		}
		if locked.InstructionReference != "psinst_1" || locked.ReconcileTarget != "split" {
			t.Fatalf("admin lock/query instruction=%q target=%q", locked.InstructionReference, locked.ReconcileTarget)
		}
		return nil
	}); err != nil {
		t.Fatalf("admin commission-first lock/read: %v", err)
	}
	session, _ := adminAccessLogin(t, application.handler, "distribution-admin", "distribution-admin-password")
	request := httptest.NewRequest(http.MethodGet, "/api/admin/distribution/orders/"+strconv.FormatInt(seed.detailAttributionID, 10), nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	body := response.Body.String()
	for _, want := range []string{`"delta_minor":-495`, `"resulting_payable_minor":495`, `"reference":"dstl_browser_partial"`, `"reference":"dstl_browser_confirmed"`, `"reference":"dstl_browser_unrecorded"`, `"amount_minor":495`, `"currency":"CNY"`, `"created_at":"` + seed.detailCreatedAt.Format(time.RFC3339Nano) + `"`} {
		if response.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("admin order detail omitted real fact %s status=%d body=%s", want, response.Code, body)
		}
	}
	var detailPayload struct {
		Settlements []struct {
			Reference             string     `json:"reference"`
			SettlementConfirmedAt *time.Time `json:"settlement_confirmed_at"`
		} `json:"settlements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detailPayload); err != nil {
		t.Fatalf("decode admin order detail: %v", err)
	}
	var confirmed, unrecorded *time.Time
	for _, settlement := range detailPayload.Settlements {
		if settlement.Reference == "dstl_browser_confirmed" {
			confirmed = settlement.SettlementConfirmedAt
		}
		if settlement.Reference == "dstl_browser_unrecorded" {
			unrecorded = settlement.SettlementConfirmedAt
		}
	}
	if confirmed == nil || !confirmed.Equal(seed.detailSettlementConfirmedAt) || unrecorded != nil {
		t.Fatalf("admin order settlement confirmation evidence confirmed=%v want=%v unrecorded=%v", confirmed, seed.detailSettlementConfirmedAt, unrecorded)
	}
	if strings.Contains(body, `"DeltaMinor"`) || strings.Contains(body, `"CreatedAt"`) {
		t.Fatalf("admin order detail leaked Go-shaped DTO: %s", body)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/distribution/exceptions/"+strconv.FormatInt(seed.detailExceptionID, 10), nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	response = httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	body = response.Body.String()
	for _, want := range []string{`"event_type":"distribution.exception_opened.v1"`, `"actor_scope":"worker:distribution-due"`, `"amount_minor":495`, `"currency":"CNY"`, `"payment_instruction_reference":"psinst_1"`, `"reconcile_target":"split"`, `"can_reconcile":true`, `"occurred_at":"` + seed.detailCreatedAt.Format(time.RFC3339Nano) + `"`} {
		if response.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("admin exception detail omitted real audit fact %s status=%d body=%s", want, response.Code, body)
		}
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/distribution/exceptions?limit=50", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	response = httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	body = response.Body.String()
	for _, want := range []string{`"exception_id":` + strconv.FormatInt(seed.detailExceptionID, 10), `"currency":"CNY"`, `"payment_instruction_reference":"psinst_1"`, `"reconcile_target":"split"`, `"can_reconcile":true`} {
		if response.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("admin exception list rejected Payment stable instruction projection %s status=%d body=%s", want, response.Code, body)
		}
	}
}

func assertDistributionAdminDeadlineWarningReadModel(t *testing.T, ctx context.Context, application *composedApplication, commissionID int64) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,settlement_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,NULL,'settlement_deadline_imminent','open',0,0,0,'split_deadline_within_24h','deadline:fixture','worker:distribution-due',1,$2,$2)`, commissionID, now); err != nil {
		t.Fatal(err)
	}
	session, _ := adminAccessLogin(t, application.handler, "distribution-admin", "distribution-admin-password")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/distribution/exceptions", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	application.handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"kind":"settlement_deadline_imminent"`) || !strings.Contains(body, `"can_reconcile":false`) || !strings.Contains(body, `"can_record_recovery":false`) || !strings.Contains(body, `"can_record_merchant_liability":false`) {
		t.Fatalf("deadline warning admin read-model status=%d body=%s", response.Code, body)
	}
}

func distributionFixturePaymentCredentials(t *testing.T) (string, string) {
	t.Helper()
	merchant, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial := big.NewInt(42)
	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true}, &x509.Certificate{SerialNumber: serial}, &platform.PublicKey, merchant)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyPath, certPath := filepath.Join(dir, "merchant.pem"), filepath.Join(dir, "platform.pem")
	keyBytes, err := x509.MarshalPKCS8PrivateKey(merchant)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return keyPath, certPath
}
