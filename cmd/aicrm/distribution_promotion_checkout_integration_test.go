package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// TestPostgreSQLDistributionPromotionCheckoutCreatesCommissionFromARealOrder
// keeps the referral chain intact: a qualified and receiver-ready distributor
// obtains a real opaque credential, Order freezes the attribution in its
// checkout transaction, and the first paid event creates the commission.
func TestPostgreSQLDistributionPromotionCheckoutCreatesCommissionFromARealOrder(t *testing.T) {
	fixture := newPromotionCheckoutFixture(t)
	defer fixture.close()
	link := fixture.issue(t, fixture.promoter, "promotion-checkout-issue-key")
	token := promotionCheckoutToken(t, link)
	created := fixture.createOrder(t, fixture.buyer, fixture.buyer, token, "promotion-checkout-order-key", "promotion-checkout-order")
	fixture.assertOrderAttribution(t, created.ID, 1, true)
	fixture.assertSettlementBeforeCreatedAtRejected(t, created, "promotion-checkout-before-created-key")
	fixture.settle(t, created, "promotion-checkout-paid-key")
	fixture.assertCommission(t, created.ID, 1)
}

func TestPostgreSQLDistributionPromotionCheckoutRejectsPromoterAsEitherBuyerParty(t *testing.T) {
	fixture := newPromotionCheckoutFixture(t)
	defer fixture.close()
	link := fixture.issue(t, fixture.promoter, "promotion-self-issue-key")
	token := promotionCheckoutToken(t, link)

	payerSelf := fixture.createOrder(t, fixture.promoter.CustomerID, fixture.buyer, token, "promotion-self-payer-key", "promotion-self-payer")
	fixture.settle(t, payerSelf, "promotion-self-payer-paid-key")
	fixture.assertOrderAttribution(t, payerSelf.ID, 0, false)
	fixture.assertCommission(t, payerSelf.ID, 0)

	beneficiarySelf := fixture.createOrder(t, fixture.buyer, fixture.promoter.CustomerID, token, "promotion-self-beneficiary-key", "promotion-self-beneficiary")
	fixture.settle(t, beneficiarySelf, "promotion-self-beneficiary-paid-key")
	fixture.assertOrderAttribution(t, beneficiarySelf.ID, 0, false)
	fixture.assertCommission(t, beneficiarySelf.ID, 0)
}

func TestPostgreSQLDistributionPromotionCredentialReceiptsSerializeAndScopeKeys(t *testing.T) {
	fixture := newPromotionCheckoutFixture(t)
	defer fixture.close()
	const key = "promotion-concurrent-receipt-key"
	start := make(chan struct{})
	links := make(chan distributionport.PromotionLink, 2)
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			link, err := fixture.promotion.IssuePromotionLink(context.Background(), distributionport.IssuePromotionCommand{Actor: fixture.promoter, ProductID: fixture.productID, ProductType: distributiondomain.ProductTypeStandard, IdempotencyKey: key})
			links <- link
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(links)
	close(errs)
	var urls []string
	for err := range errs {
		if err != nil {
			t.Fatalf("same-key concurrent issue: %v", err)
		}
	}
	for link := range links {
		urls = append(urls, link.URL)
	}
	if len(urls) != 2 || urls[0] == "" || urls[0] != urls[1] {
		t.Fatalf("same-key issue did not replay exactly: %v", urls)
	}
	fixture.assertCredentialFacts(t, fixture.promoter.CustomerID, key, 1)

	other := fixture.issue(t, fixture.otherPromoter, key)
	if other.URL == urls[0] {
		t.Fatal("different qualified customer reused another customer's credential")
	}
	fixture.assertCredentialFacts(t, fixture.otherPromoter.CustomerID, key, 1)

	_, err := fixture.promotion.IssuePromotionLink(context.Background(), distributionport.IssuePromotionCommand{Actor: fixture.promoter, ProductID: fixture.productID, ProductType: distributiondomain.ProductTypeServicePeriod, IdempotencyKey: key})
	if !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("credential payload drift error=%v", err)
	}
	fixture.assertCredentialFacts(t, fixture.promoter.CustomerID, key, 1)
}

type promotionCheckoutFixture struct {
	pool          *pgxpool.Pool
	cleanup       func()
	uow           platformport.UnitOfWork
	qualification *distributionapp.QualificationService
	distributions *distributionstore.Repository
	targets       *productapp.TargetReader
	promotion     *distributionapp.PromotionService
	orders        *orderapp.Service
	productID     int64
	promoter      distributionport.TrustedSessionActor
	otherPromoter distributionport.TrustedSessionActor
	buyer         int64
	now           time.Time
}

func newPromotionCheckoutFixture(t *testing.T) *promotionCheckoutFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	distributions, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	ordersRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	productsRepository, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, ordersRepository)
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, nil)
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	productAudit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	productEvents, err := productstore.NewTransactionalEventAppender(productAudit, platformoutbox.NewPostgreSQL())
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	catalog := productapp.NewService(uow, productsRepository, productEvents)
	period := productapp.NewServicePeriodService(uow, productsRepository, productEvents)
	targets, err := productapp.NewTargetReader(catalog, period)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	promotion, err := distributionapp.NewPromotionService(uow, distributions, qualification, catalog, targets, identityquery.NewPostgreSQL(), "https://crm.example.test", base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	promotion.SetSettlementEnabled(true)
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[distributionapp.CommissionDueJobArgs](workers, distributionapp.NewCommissionDueWorker()); err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	due, err := distributionapp.NewRiverCommissionDueEnqueuer(insertClient)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	commission, err := distributionapp.NewCommissionService(distributions, due, qualification)
	if err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	if err = orders.SetCheckoutAttributionCoordinator(promotion); err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}
	if err = orders.SetPaidEventConsumer(commission); err != nil {
		wrapped.Close()
		cleanup()
		t.Fatal(err)
	}

	fixture := &promotionCheckoutFixture{pool: pool, uow: uow, qualification: qualification, distributions: distributions, targets: targets, promotion: promotion, orders: orders, now: time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)}
	fixture.cleanup = func() { wrapped.Close(); cleanup() }
	fixture.seed(t)
	return fixture
}

func (fixture *promotionCheckoutFixture) close() { fixture.cleanup() }

func (fixture *promotionCheckoutFixture) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	var promoterID, promoterIdentity, otherID, otherIdentity, buyer, productID int64
	for _, destination := range []*int64{&promoterID, &otherID, &buyer} {
		if err := fixture.pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []struct {
		customer *int64
		identity *int64
		suffix   string
	}{{&promoterID, &promoterIdentity, "one"}, {&otherID, &otherIdentity, "two"}} {
		if err := fixture.pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'mp_openid','wechat-app:promotion-checkout',$2,'verified','promotion-checkout-fixture',1,$3) RETURNING id`, *value.customer, "promotion-checkout-"+value.suffix, fixture.now).Scan(value.identity); err != nil {
			t.Fatal(err)
		}
	}
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`
	if err := fixture.pool.QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('promotion-checkout-product','分销归因商品','真实分销归因测试',1000,'CNY',100,1,$1::jsonb) RETURNING id`, projection).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,'standard_product',true,1000,7,1,$2,$2)`, productID, fixture.now); err != nil {
		t.Fatal(err)
	}
	for _, values := range []struct {
		customer, identity int64
		public             string
	}{{promoterID, promoterIdentity, "DSTPROMOTERONE"}, {otherID, otherIdentity, "DSTPROMOTERTWO"}} {
		if err := fixture.pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,receiver_reference,receiver_app_id,receiver_ready,receiver_reason,receiver_checked_at,registered_at,version,created_at,updated_at) VALUES($1,$2,'v1',true,'receiver-' || $2,'wx-promotion-checkout',true,'',$3,$3,1,$3,$3) RETURNING id`, values.customer, values.public, fixture.now).Scan(new(int64)); err != nil {
			t.Fatal(err)
		}
		receiverDigest := sha256.Sum256([]byte(fmt.Sprintf("receiver-%d", values.customer)))
		if _, err := fixture.pool.Exec(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES($1,$2,'wx-promotion-checkout','wechat-app:promotion-checkout','mini_program',$3,'ready',1,$4,$4)`, values.customer, values.identity, "sha256:"+hex.EncodeToString(receiverDigest[:]), fixture.now); err != nil {
			t.Fatal(err)
		}
		fixture.seedQualificationPurchase(t, values.customer, values.identity, productID, values.public)
	}
	fixture.productID, fixture.buyer = productID, buyer
	fixture.promoter = distributionport.TrustedSessionActor{CustomerID: promoterID, IdentityID: promoterIdentity, AppID: "wx-promotion-checkout", AppScope: "wechat-app:promotion-checkout", Channel: "mini_program", OccurredAt: fixture.now}
	fixture.otherPromoter = distributionport.TrustedSessionActor{CustomerID: otherID, IdentityID: otherIdentity, AppID: "wx-promotion-checkout", AppScope: "wechat-app:promotion-checkout", Channel: "mini_program", OccurredAt: fixture.now}
}

func (fixture *promotionCheckoutFixture) seedQualificationPurchase(t *testing.T, customerID, identityID, productID int64, suffix string) {
	t.Helper()
	ctx := context.Background()
	var orderID int64
	merchant := "M-promotion-qualification-" + suffix
	if err := fixture.pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','promotion-checkout',$1,$2,$3,$3,1000,'CNY','paid','native',true,2,$4,$4) RETURNING id`, suffix, merchant, customerID, fixture.now.Add(-time.Hour)).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,'promotion-checkout-product','分销归因商品',1000,1,1000)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',$2,'promotion-checkout-product','分销归因商品',1,0,1000,0,1000,'CNY',false,'',$3,$3)`, orderID, productID, fixture.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("promotion-qualification-paid:" + suffix))
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, digest[:], fixture.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,$3,$4,$4,1000,'CNY','paid',false,1,$5,$5,$5)`, orderID, merchant, identityID, customerID, fixture.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func (fixture *promotionCheckoutFixture) issue(t *testing.T, actor distributionport.TrustedSessionActor, key string) distributionport.PromotionLink {
	t.Helper()
	var qualification distributiondomain.Qualification
	if err := fixture.uow.Within(context.Background(), func(tx context.Context) error {
		distributor, readiness, readErr := fixture.distributions.ReadDistributorByCustomerWithin(tx, actor.CustomerID, false)
		if readErr != nil || !distributor.Enabled || !readiness.Ready || readiness.AppID != actor.AppID {
			return fmt.Errorf("distribution readiness distributor=%+v readiness=%+v err=%w", distributor, readiness, readErr)
		}
		policy, policyErr := fixture.distributions.ReadProductPolicyForUpdateWithin(tx, fixture.productID, distributiondomain.ProductTypeStandard)
		if policyErr != nil || !policy.Enabled {
			return fmt.Errorf("distribution policy value=%+v err=%w", policy, policyErr)
		}
		share, shareErr := fixture.targets.ReadSidebarShareProduct(tx, productport.ProductOptionStandard, productport.ID(fixture.productID))
		if shareErr != nil {
			return fmt.Errorf("saleable product value=%+v err=%w", share, shareErr)
		}
		var checkErr error
		qualification, checkErr = fixture.qualification.CheckWithin(tx, actor.CustomerID, fixture.productID, distributiondomain.ProductTypeStandard)
		return checkErr
	}); err != nil || !qualification.AllowsPromotion() {
		t.Fatalf("fixture qualification actor=%d value=%+v err=%v", actor.CustomerID, qualification, err)
	}
	link, err := fixture.promotion.IssuePromotionLink(context.Background(), distributionport.IssuePromotionCommand{Actor: actor, ProductID: fixture.productID, ProductType: distributiondomain.ProductTypeStandard, IdempotencyKey: key})
	if err != nil || link.URL == "" || link.ExpiresAt.IsZero() {
		t.Fatalf("issue credential link=%+v err=%v", link, err)
	}
	return link
}

func promotionCheckoutToken(t *testing.T, link distributionport.PromotionLink) string {
	t.Helper()
	const prefix = "https://crm.example.test/d/"
	if !strings.HasPrefix(link.URL, prefix) {
		t.Fatalf("unexpected credential URL %q", link.URL)
	}
	return strings.TrimPrefix(link.URL, prefix)
}

func (fixture *promotionCheckoutFixture) createOrder(t *testing.T, payer, beneficiary int64, promotion, key, source string) orderdomain.Snapshot {
	t.Helper()
	var created orderdomain.Snapshot
	if err := fixture.uow.Within(context.Background(), func(tx context.Context) error {
		var createErr error
		created, createErr = fixture.orders.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-" + source, PayerCustomerID: payer, BeneficiaryCustomerID: beneficiary, ProductID: fixture.productID, ProductCode: "promotion-checkout-product", ProductName: "分销归因商品", ProductVersion: 1, ProductType: "standard_product", UnitAmountMinor: 1000, Currency: "CNY", PromotionContext: promotion, ActorScope: "checkout:" + source, IdempotencyKey: key})
		return createErr
	}); err != nil {
		t.Fatalf("checkout create: %v", err)
	}
	return created
}

func (fixture *promotionCheckoutFixture) assertSettlementBeforeCreatedAtRejected(t *testing.T, created orderdomain.Snapshot, key string) {
	t.Helper()
	err := fixture.uow.Within(context.Background(), func(tx context.Context) error {
		_, settleErr := fixture.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: created.ID, ProviderTransactionNo: "tx-" + key, OccurredAt: created.CreatedAt.Add(-time.Second), ReceiptKey: key})
		return settleErr
	})
	if !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("settlement before immutable order snapshot err=%v", err)
	}
	var status string
	if err := fixture.pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id=$1`, created.ID).Scan(&status); err != nil || status != string(orderdomain.StatusPendingPayment) {
		t.Fatalf("early settlement changed order=%d status=%q err=%v", created.ID, status, err)
	}
	fixture.assertCommission(t, created.ID, 0)
}

func (fixture *promotionCheckoutFixture) settle(t *testing.T, created orderdomain.Snapshot, key string) {
	t.Helper()
	if err := fixture.uow.Within(context.Background(), func(tx context.Context) error {
		_, settleErr := fixture.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: created.ID, ProviderTransactionNo: "tx-" + key, OccurredAt: created.CreatedAt.Add(time.Minute), ReceiptKey: key})
		return settleErr
	}); err != nil {
		t.Fatalf("checkout settlement: %v", err)
	}
}

func (fixture *promotionCheckoutFixture) assertOrderAttribution(t *testing.T, orderID int64, want int, profitSharing bool) {
	t.Helper()
	var count int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT count(*) FROM distribution_order_attributions WHERE order_id=$1`, orderID).Scan(&count); err != nil || count != want {
		t.Fatalf("attribution count order=%d got=%d want=%d err=%v", orderID, count, want, err)
	}
	var frozen bool
	if err := fixture.pool.QueryRow(context.Background(), `SELECT profit_sharing_required FROM order_checkout_snapshots WHERE order_id=$1`, orderID).Scan(&frozen); err != nil || frozen != profitSharing {
		t.Fatalf("checkout frozen profit-sharing order=%d got=%t want=%t err=%v", orderID, frozen, profitSharing, err)
	}
}

func (fixture *promotionCheckoutFixture) assertCommission(t *testing.T, orderID int64, want int) {
	t.Helper()
	var count int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT count(*) FROM distribution_commissions WHERE order_id=$1`, orderID).Scan(&count); err != nil || count != want {
		t.Fatalf("commission count order=%d got=%d want=%d err=%v", orderID, count, want, err)
	}
}

func (fixture *promotionCheckoutFixture) assertCredentialFacts(t *testing.T, customerID int64, key string, want int) {
	t.Helper()
	actor := "customer:" + fmt.Sprint(customerID)
	digest := sha256.Sum256([]byte(key))
	assertPromotionCheckoutCount(t, fixture.pool, `SELECT count(*) FROM distribution_promotion_credentials c JOIN distribution_distributors d ON d.id=c.distributor_id WHERE d.customer_id=$1`, want, customerID)
	assertPromotionCheckoutCount(t, fixture.pool, `SELECT count(*) FROM distribution_operation_receipts WHERE operation='credential' AND actor_scope=$1 AND key_digest=$2 AND result_kind='credential'`, want, actor, digest[:])
	assertPromotionCheckoutCount(t, fixture.pool, `SELECT count(*) FROM distribution_audit_events a JOIN distribution_promotion_credentials c ON c.id=a.aggregate_id JOIN distribution_distributors d ON d.id=c.distributor_id WHERE d.customer_id=$1 AND a.event_type='distribution.credential_issued.v1' AND a.aggregate_type='credential'`, want, customerID)
	var tokenDigest []byte
	if err := fixture.pool.QueryRow(context.Background(), `SELECT c.token_digest FROM distribution_promotion_credentials c JOIN distribution_distributors d ON d.id=c.distributor_id WHERE d.customer_id=$1`, customerID).Scan(&tokenDigest); err != nil || len(tokenDigest) != sha256.Size {
		t.Fatalf("credential token digest customer=%d len=%d err=%v", customerID, len(tokenDigest), err)
	}
	outboxKey := "distribution.credential:" + base64.RawURLEncoding.EncodeToString(tokenDigest)
	assertPromotionCheckoutCount(t, fixture.pool, `SELECT count(*) FROM distribution_outbox WHERE event_type='distribution.credential_issued.v1' AND idempotency_key=$1`, want, outboxKey)
}

func assertPromotionCheckoutCount(t *testing.T, pool *pgxpool.Pool, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("promotion checkout count query=%q got=%d want=%d err=%v", query, got, want, err)
	}
}
