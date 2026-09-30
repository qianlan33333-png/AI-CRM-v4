package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/payment"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// pay05UnknownAdapter is a deterministic, loopback-free simulation of a
// request whose response was lost after the attempted boundary. It cannot
// issue network requests and explicitly records that no real external call
// executed; only the EER/Payment unknown-state path is exercised.
type pay05UnknownAdapter struct{}

func (pay05UnknownAdapter) Execute(_ context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Owner != effectport.OwnerPayment || envelope.Kind != effectport.KindWeChatPayRefund || attempt.Number < 1 {
		return effectport.AdapterResult{}, fmt.Errorf("unexpected PAY-05 effect envelope")
	}
	return effectport.AdapterResult{
		Completion:               effectport.StateUnknown,
		ReceiptDigest:            effectport.Hash("pay05.synthetic.accepted-response-lost", string(envelope.PayloadDigest), strconv.Itoa(int(attempt.Number))),
		CallAttempted:            true,
		RealExternalCallExecuted: false,
	}, nil
}

type pay05RefundQuery struct {
	amount      int64
	providerRef string
	statuses    []string
}

// pay05RefundReconciler returns only synthetic Provider-read facts. It has no
// transport and no network access; each returned identity/amount is matched
// by the production Payment reconciler before any state transition.
type pay05RefundReconciler struct {
	mu      sync.Mutex
	queries map[string]*pay05RefundQuery
	calls   map[string]int
}

type pay05EffectsRequestSecurity struct {
	principal accessdomain.Principal
	csrfErr   error
}

func (s pay05EffectsRequestSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}

func (s pay05EffectsRequestSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.csrfErr
}

func (r *pay05RefundReconciler) QueryPayment(context.Context, string) (paymentport.WeChatPayPaymentQuery, error) {
	return paymentport.WeChatPayPaymentQuery{}, fmt.Errorf("unused PAY-05 payment query")
}

func (r *pay05RefundReconciler) QueryRefund(_ context.Context, refundNo string) (paymentport.WeChatPayRefundQuery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	query := r.queries[refundNo]
	if query == nil || len(query.statuses) == 0 {
		return paymentport.WeChatPayRefundQuery{}, fmt.Errorf("no synthetic PAY-05 query result for refund")
	}
	status := query.statuses[0]
	query.statuses = query.statuses[1:]
	r.calls[refundNo]++
	call := r.calls[refundNo]
	result := paymentport.WeChatPayRefundQuery{
		RefundNo:       refundNo,
		Currency:       "CNY",
		Status:         status,
		AmountMinor:    query.amount,
		TotalMinor:     1000,
		OccurredAt:     time.Now().UTC().Truncate(time.Microsecond),
		EvidenceDigest: effectport.Hash("pay05.synthetic.refund-query", refundNo, status, strconv.Itoa(call)),
	}
	if status == "SUCCESS" {
		result.RefundDigest = effectport.Hash("wechatpay.refund", query.providerRef)
	}
	return result, nil
}

func TestPAY05PostgreSQLRefundUnknownAndDerivedReversal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PAY05 isolated PG clone host=%s port=%d database=%s", config.ConnConfig.Host, config.ConnConfig.Port, config.ConnConfig.Database)
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}

	ordersRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	couponsRepository, err := couponstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	couponCheckout, err := couponapp.NewCheckoutService(uow, couponsRepository)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, ordersRepository)
	if err = orders.SetCheckoutCouponCoordinator(couponCheckout); err != nil {
		t.Fatal(err)
	}
	entitlementFulfillment, err := orderapp.NewEntitlementFulfillmentApplication(ordersRepository)
	if err != nil {
		t.Fatal(err)
	}
	if err = orders.SetServicePeriodEntitlementCoordinator(entitlementFulfillment); err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	if err = effects.NewModuleRegistration().RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	reconciliationWorker := paymentmodule.NewReconciliationWorker()
	if err = river.AddWorkerSafely[paymentmodule.ReconciliationJobArgs](workers, reconciliationWorker); err != nil {
		t.Fatal(err)
	}
	refundRecheckWorker := distributionapp.NewRefundRecheckWorker()
	if err = river.AddWorkerSafely[distributionapp.RefundRecheckJobArgs](workers, refundRecheckWorker); err != nil {
		t.Fatal(err)
	}
	dueWorker := distributionapp.NewCommissionDueWorker()
	if err = river.AddWorkerSafely[distributionapp.CommissionDueJobArgs](workers, dueWorker); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectRepository, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	paymentRepository := paymentstore.NewPostgreSQL()
	reconciliationEnqueuer, err := paymentmodule.NewRiverReconciliationEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	completionSink, err := paymentmodule.NewCompletionSink(paymentRepository, reconciliationEnqueuer, orders)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectRepository.SetCompletionSink(completionSink); err != nil {
		t.Fatal(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	dueEnqueuer, err := distributionapp.NewRiverCommissionDueEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	refundEnqueuer, err := distributionapp.NewRiverRefundRecheckEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}

	var buyerID, promoterID int64
	for _, target := range []*int64{&buyerID, &promoterID} {
		if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(target); err != nil {
			t.Fatal(err)
		}
	}

	days := int32(7)
	couponRules := couponapp.NewService(uow, couponsRepository, commerceCheckoutProductFacts{
		17: {ID: 17, ProductType: productport.ProductOptionServicePeriod, Currency: "CNY", PriceMinor: 1200},
	}, couponsRepository)
	rule, err := couponRules.Create(ctx, couponport.UpsertCommand{
		Coupon: couponport.Coupon{
			Name: "PAY-05 合成券", DiscountAmountTotal: 200, TotalIssueLimit: 1, PerUserIssueLimit: 1,
			ClaimStartsAt: time.Now().UTC().Add(-time.Hour), ClaimEndsAt: time.Now().UTC().Add(time.Hour),
			ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"service_period:17"},
		},
		Actor: 1, IdempotencyKey: "pay05-coupon-create-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = couponRules.Publish(ctx, rule.ID, 1, "pay05-coupon-publish-0001"); err != nil {
		t.Fatal(err)
	}
	claim, err := couponCheckout.Claim(ctx, couponport.ClaimCommand{
		CouponID: rule.ID, HolderCustomerID: buyerID, ActorScope: "pay05:buyer", IdempotencyKey: "pay05-coupon-claim-0001", ClaimedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	merchant := "M-PAY05-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	var orderID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		created, createErr := orders.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{
			Provider:        orderdomain.ProviderWeChatPay,
			MerchantOrderNo: merchant, PayerCustomerID: buyerID, BeneficiaryCustomerID: buyerID,
			ProductID: 17, CouponClaimID: int64(claim.ClaimID), ProductCode: "period-17", ProductName: "PAY-05 service period",
			ProductVersion: 1, UnitAmountMinor: 1200, ProductType: "service_period", ServicePeriodDurationDays: 30,
			Currency: "CNY", ActorScope: "payment:pay05:checkout", IdempotencyKey: "pay05-checkout-0001",
		})
		if createErr == nil {
			orderID = created.ID
		}
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	if orderID < 1 {
		t.Fatal("checkout did not create a real Order row")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	paidAt := now
	var paymentID int64
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,901,$3,$3,1000,'CNY','awaiting_prepay',1,$4,$4) RETURNING id`, orderID, merchant, buyerID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	sessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 901, customerID: customerdomain.CustomerID(buyerID)}, customerstore.NewPostgreSQL(), paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	payments := paymentapp.NewService(uow, paymentRepository, orders, sessions, effectRepository, effectRepository)
	if err = payments.SetPaymentChannelAppIDs("pay05-app", ""); err != nil {
		t.Fatal(err)
	}
	if err = payments.SetReconciliationEnqueuer(reconciliationEnqueuer); err != nil {
		t.Fatal(err)
	}
	reconciler := &pay05RefundReconciler{
		queries: map[string]*pay05RefundQuery{}, calls: map[string]int{},
	}
	if err = payments.SetWeChatPayReconciler(reconciler); err != nil {
		t.Fatal(err)
	}

	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}
	refundService, err := distributionapp.NewRefundService(uow, distributionRepository, dueEnqueuer, refundEnqueuer, qualification, orders)
	if err != nil {
		t.Fatal(err)
	}
	if err = orders.SetRefundSettlementConsumer(refundService); err != nil {
		t.Fatal(err)
	}
	if err = payments.SetRefundExposureConsumer(refundService); err != nil {
		t.Fatal(err)
	}
	if err = refundRecheckWorker.BindService(refundService); err != nil {
		t.Fatal(err)
	}
	if err = reconciliationWorker.BindService(payments); err != nil {
		t.Fatal(err)
	}

	apiKey := []byte("0123456789abcdef0123456789abcdef")
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := paymentprovider.NewCallbackVerifier(map[string]*rsa.PublicKey{"local-platform": &platformKey.PublicKey}, apiKey, "pay05-app", "pay05-mch")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := paymenthttp.NewHandler(payments, verifier, commerceFundsSecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	callback := func(eventID, eventType, path string, payload map[string]any) int {
		t.Helper()
		body, headers := commerceFundsSignedCallback(t, platformKey, apiKey, eventID, eventType, payload)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, commerceFundsCallbackRequest(path, body, headers))
		return response.Code
	}
	if code := callback("pay05-payment", "TRANSACTION.SUCCESS", "/api/public/wechat-pay/callbacks/payment", map[string]any{
		"appid": "pay05-app", "mchid": "pay05-mch", "out_trade_no": merchant, "transaction_id": "pay05-provider-payment",
		"trade_state": "SUCCESS", "success_time": paidAt.Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"},
	}); code != http.StatusOK {
		t.Fatalf("signed payment callback status=%d", code)
	}

	commissionID := pay05SeedCommission(t, ctx, pool, promoterID, buyerID, orderID, paidAt, merchant)
	t.Logf("PAY05 synthetic IDs merchant=%s order_id=%d payment_id=%d coupon_rule_id=%d coupon_claim_id=%d commission_id=%d gross_minor=1200 discount_minor=200 payable_minor=1000", merchant, orderID, paymentID, rule.ID, claim.ClaimID, commissionID)
	pay05AssertCommerce(t, ctx, pool, orderID, paymentID, claim.ClaimID, merchant, "paid", 0, "active", "consumed", "redeemed", "pending", 0, 200, commissionID)

	unknownEffectWorker := effects.NewWorker(effectRepository, pay05UnknownAdapter{})
	failedRefund := pay05RequestRefund(t, ctx, payments, paymentID, 300, "pay05-refund-failed", "pay05-refund-key-failed")
	t.Logf("PAY05 refund case=closed refund_id=%d refund_no=%s requested_minor=300", failedRefund.ID, failedRefund.RefundNo)
	reconciler.queries[failedRefund.RefundNo] = &pay05RefundQuery{amount: 300, providerRef: "pay05-provider-failed", statuses: []string{"CLOSED"}}
	if err = pay05RunUnknownEffect(t, ctx, pool, unknownEffectWorker, failedRefund.ID); err != nil {
		t.Fatal(err)
	}
	pay05AssertEffectAndRefund(t, ctx, pool, failedRefund.ID, "outcome_unknown", "outcome_unknown")
	if err = pay05RunDistributionJob(t, ctx, pool, refundRecheckWorker, orderID, failedRefund.ID, "opened"); err != nil {
		t.Fatal(err)
	}
	if err = pay05RunPaymentReconciliationJob(t, ctx, pool, reconciliationWorker, failedRefund.ID); err != nil {
		t.Fatal(err)
	}
	if err = pay05RunDistributionJob(t, ctx, pool, refundRecheckWorker, orderID, failedRefund.ID, "final_failed"); err != nil {
		t.Fatal(err)
	}
	pay05AssertEffectAndRefund(t, ctx, pool, failedRefund.ID, "outcome_unknown", "final_failed")
	pay05AssertCommerce(t, ctx, pool, orderID, paymentID, claim.ClaimID, merchant, "paid", 0, "active", "consumed", "redeemed", "pending", 0, 200, commissionID)

	refundA := pay05RequestRefund(t, ctx, payments, paymentID, 300, "pay05-refund-a", "pay05-refund-key-a")
	t.Logf("PAY05 refund case=query_success_then_late_callback refund_id=%d refund_no=%s requested_minor=300", refundA.ID, refundA.RefundNo)
	reconciler.queries[refundA.RefundNo] = &pay05RefundQuery{amount: 300, providerRef: "pay05-provider-refund-a", statuses: []string{"PROCESSING", "SUCCESS"}}
	if err = pay05RunUnknownEffect(t, ctx, pool, unknownEffectWorker, refundA.ID); err != nil {
		t.Fatal(err)
	}
	pay05AssertEffectAndRefund(t, ctx, pool, refundA.ID, "outcome_unknown", "outcome_unknown")
	if err = pay05RunDistributionJob(t, ctx, pool, refundRecheckWorker, orderID, refundA.ID, "opened"); err != nil {
		t.Fatal(err)
	}
	if err = pay05RunPaymentReconciliationJob(t, ctx, pool, reconciliationWorker, refundA.ID); err == nil {
		t.Fatal("PROCESSING refund query unexpectedly completed the reconciliation worker")
	}
	pay05AssertCommerce(t, ctx, pool, orderID, paymentID, claim.ClaimID, merchant, "paid", 0, "active", "consumed", "redeemed", "held", 0, 200, commissionID)
	var refundAEffectID int64
	if err = pool.QueryRow(ctx, `SELECT external_effect_id FROM payment_refunds WHERE id=$1`, refundA.ID).Scan(&refundAEffectID); err != nil {
		t.Fatal(err)
	}
	pay05AssertGenericPaymentReconcileRejected(t, ctx, pool, effectRepository, refundA.ID, "eer_"+strconv.FormatInt(refundAEffectID, 10))
	if err = pay05RunPaymentReconciliationJob(t, ctx, pool, reconciliationWorker, refundA.ID); err != nil {
		t.Fatal(err)
	}
	pay05AssertEffectAndRefund(t, ctx, pool, refundA.ID, "outcome_unknown", "completed")
	if err = pay05RunDistributionJob(t, ctx, pool, refundRecheckWorker, orderID, refundA.ID, "successful"); err != nil {
		t.Fatal(err)
	}
	firstEnd, firstUpdated := pay05AssertPartialRefund(t, ctx, pool, orderID, paymentID, claim.ClaimID, merchant, commissionID)
	lateRefundA := httptest.NewRecorder()
	refundABody, refundAHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "pay05-refund-a-callback", "REFUND.SUCCESS", map[string]any{
		"appid": "pay05-app", "mchid": "pay05-mch", "out_refund_no": refundA.RefundNo, "refund_id": "pay05-provider-refund-a",
		"refund_status": "SUCCESS", "success_time": time.Now().UTC().Format(time.RFC3339Nano),
		"amount": map[string]any{"refund": 300, "total": 1000, "currency": "CNY"},
	})
	handler.ServeHTTP(lateRefundA, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", refundABody, refundAHeaders))
	if lateRefundA.Code != http.StatusOK {
		t.Fatalf("late signed refund callback after query success status=%d", lateRefundA.Code)
	}
	pay05AssertPartialRefund(t, ctx, pool, orderID, paymentID, claim.ClaimID, merchant, commissionID)

	refundB := pay05RequestRefund(t, ctx, payments, paymentID, 700, "pay05-refund-b", "pay05-refund-key-b")
	t.Logf("PAY05 refund case=processing_then_signed_callback refund_id=%d refund_no=%s requested_minor=700", refundB.ID, refundB.RefundNo)
	reconciler.queries[refundB.RefundNo] = &pay05RefundQuery{amount: 700, providerRef: "pay05-provider-refund-b", statuses: []string{"PROCESSING"}}
	if err = pay05RunUnknownEffect(t, ctx, pool, unknownEffectWorker, refundB.ID); err != nil {
		t.Fatal(err)
	}
	if err = pay05RunDistributionJob(t, ctx, pool, refundRecheckWorker, orderID, refundB.ID, "opened"); err != nil {
		t.Fatal(err)
	}
	if err = pay05RunPaymentReconciliationJob(t, ctx, pool, reconciliationWorker, refundB.ID); err == nil {
		t.Fatal("second PROCESSING refund query unexpectedly completed the reconciliation worker")
	}
	pay05AssertCommerce(t, ctx, pool, orderID, paymentID, claim.ClaimID, merchant, "partially_refunded", 300, "refunded", "consumed", "redeemed", "held", 300, 140, commissionID)
	refundBBody, refundBHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "pay05-refund-b-callback", "REFUND.SUCCESS", map[string]any{
		"appid": "pay05-app", "mchid": "pay05-mch", "out_refund_no": refundB.RefundNo, "refund_id": "pay05-provider-refund-b",
		"refund_status": "SUCCESS", "success_time": time.Now().UTC().Format(time.RFC3339Nano),
		"amount": map[string]any{"refund": 700, "total": 1000, "currency": "CNY"},
	})
	responseB := httptest.NewRecorder()
	handler.ServeHTTP(responseB, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", refundBBody, refundBHeaders))
	if responseB.Code != http.StatusOK {
		t.Fatalf("signed final refund callback status=%d", responseB.Code)
	}
	pay05AssertEffectAndRefund(t, ctx, pool, refundB.ID, "outcome_unknown", "completed")
	if err = pay05RunDistributionJob(t, ctx, pool, refundRecheckWorker, orderID, refundB.ID, "successful"); err != nil {
		t.Fatal(err)
	}
	replayB := httptest.NewRecorder()
	handler.ServeHTTP(replayB, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", refundBBody, refundBHeaders))
	if replayB.Code != http.StatusOK {
		t.Fatalf("duplicate signed final refund callback status=%d", replayB.Code)
	}
	pay05AssertFinalRefund(t, ctx, pool, orderID, paymentID, claim.ClaimID, merchant, commissionID, firstEnd, firstUpdated)
}

func pay05RequestRefund(t *testing.T, ctx context.Context, payments *paymentapp.Service, paymentID, amount int64, refundNo, idempotency string) paymentdomain.Refund {
	t.Helper()
	refund, err := payments.RequestRefund(ctx, paymentport.RefundCommand{
		PaymentID: paymentID, AmountMinor: amount, RefundNo: refundNo, Reason: "synthetic PAY-05 refund", ActorScope: "admin:pay05", IdempotencyKey: idempotency,
	})
	if err != nil {
		t.Fatalf("request refund %s: %v", refundNo, err)
	}
	return refund
}

func pay05RunUnknownEffect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, worker *effects.Worker, refundID int64) error {
	t.Helper()
	var effectID, generation, riverJobID int64
	if err := pool.QueryRow(ctx, `SELECT effect.id,effect.generation,effect_job.river_job_id FROM payment_refunds refund JOIN external_effects effect ON effect.id=refund.external_effect_id JOIN external_effect_jobs effect_job ON effect_job.effect_id=effect.id AND effect_job.generation=effect.generation WHERE refund.id=$1`, refundID).Scan(&effectID, &generation, &riverJobID); err != nil {
		return err
	}
	job := &river.Job[effects.EffectJobArgs]{JobRow: &rivertype.JobRow{ID: riverJobID}, Args: effects.EffectJobArgs{EffectID: effectID, Generation: generation}}
	return worker.Work(ctx, job)
}

func pay05RunPaymentReconciliationJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, worker *paymentmodule.ReconciliationWorker, refundID int64) error {
	t.Helper()
	var jobID int64
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT id,args FROM river_job WHERE kind=$1 AND args->>'refund_id'=$2 ORDER BY id DESC LIMIT 1`, (paymentmodule.ReconciliationJobArgs{}).Kind(), strconv.FormatInt(refundID, 10)).Scan(&jobID, &raw); err != nil {
		return err
	}
	var args paymentmodule.ReconciliationJobArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return err
	}
	return worker.Work(ctx, &river.Job[paymentmodule.ReconciliationJobArgs]{JobRow: &rivertype.JobRow{ID: jobID}, Args: args})
}

func pay05RunDistributionJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, worker *distributionapp.RefundRecheckWorker, orderID, refundID int64, state string) error {
	t.Helper()
	var jobID int64
	var raw []byte
	var err error
	if state == "successful" {
		err = pool.QueryRow(ctx, `SELECT id,args FROM river_job WHERE kind=$1 AND args->>'order_id'=$2 AND args->>'state'=$3 ORDER BY id DESC LIMIT 1`, (distributionapp.RefundRecheckJobArgs{}).Kind(), strconv.FormatInt(orderID, 10), state).Scan(&jobID, &raw)
	} else {
		err = pool.QueryRow(ctx, `SELECT id,args FROM river_job WHERE kind=$1 AND args->>'order_id'=$2 AND args->>'refund_id'=$3 AND args->>'state'=$4 ORDER BY id DESC LIMIT 1`, (distributionapp.RefundRecheckJobArgs{}).Kind(), strconv.FormatInt(orderID, 10), strconv.FormatInt(refundID, 10), state).Scan(&jobID, &raw)
	}
	if err != nil {
		return err
	}
	var args distributionapp.RefundRecheckJobArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return err
	}
	return worker.Work(ctx, &river.Job[distributionapp.RefundRecheckJobArgs]{JobRow: &rivertype.JobRow{ID: jobID}, Args: args})
}

func pay05AssertEffectAndRefund(t *testing.T, ctx context.Context, pool *pgxpool.Pool, refundID int64, effectState, refundState string) {
	t.Helper()
	var gotEffect, gotRefund string
	var calls, realCalls int
	if err := pool.QueryRow(ctx, `SELECT effect.state,refund.status,(SELECT count(*) FROM external_effect_attempts attempt WHERE attempt.effect_id=effect.id),(SELECT count(*) FROM external_effect_attempts attempt WHERE attempt.effect_id=effect.id AND attempt.real_external_call_executed) FROM payment_refunds refund JOIN external_effects effect ON effect.id=refund.external_effect_id WHERE refund.id=$1`, refundID).Scan(&gotEffect, &gotRefund, &calls, &realCalls); err != nil {
		t.Fatal(err)
	}
	if gotEffect != effectState || gotRefund != refundState || calls != 1 || realCalls != 0 {
		t.Fatalf("synthetic unknown state effect=%q refund=%q attempts=%d real_calls=%d", gotEffect, gotRefund, calls, realCalls)
	}
}

type pay05ReconcileState struct {
	refundStatus       string
	effectOwner        string
	effectKind         string
	reconciliation     string
	paymentEvidence    string
	reconciliationRows int
	effectState        string
	attemptState       string
	attemptEvidence    string
	attemptCount       int
	providerCallCount  int
	reconcileReceipts  int
}

func pay05AssertGenericPaymentReconcileRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *effects.Repository, refundID int64, effectID string) {
	t.Helper()
	readState := func() pay05ReconcileState {
		t.Helper()
		var state pay05ReconcileState
		err := pool.QueryRow(ctx, `SELECT refund.status,effect.owner,effect.kind,reconciliation.outcome,encode(reconciliation.evidence_digest,'hex'),
			(SELECT count(*) FROM payment_reconciliations recorded WHERE recorded.refund_id=refund.id),effect.state,attempt.state,COALESCE(attempt.evidence_digest,''),effect.attempt_count,
			(SELECT count(*) FROM external_effect_attempts counted WHERE counted.effect_id=effect.id AND counted.real_external_call_executed),
			(SELECT count(*) FROM external_effect_operation_receipts receipt WHERE receipt.effect_id=effect.id AND receipt.operation='reconcile')
			FROM payment_refunds refund
			JOIN payment_reconciliations reconciliation ON reconciliation.refund_id=refund.id
			JOIN external_effects effect ON effect.id=refund.external_effect_id
			JOIN external_effect_attempts attempt ON attempt.effect_id=effect.id AND attempt.generation=effect.generation
			WHERE refund.id=$1
			ORDER BY reconciliation.id DESC LIMIT 1`, refundID).Scan(&state.refundStatus, &state.effectOwner, &state.effectKind, &state.reconciliation, &state.paymentEvidence, &state.reconciliationRows, &state.effectState, &state.attemptState, &state.attemptEvidence, &state.attemptCount, &state.providerCallCount, &state.reconcileReceipts)
		if err != nil {
			t.Fatalf("read PAY05 reconcile state: %v", err)
		}
		return state
	}
	before := readState()
	if before.refundStatus != "outcome_unknown" || before.effectOwner != string(effectport.OwnerPayment) || before.effectKind != string(effectport.KindWeChatPayRefund) || before.reconciliation != "pending" || before.reconciliationRows != 1 || before.effectState != "outcome_unknown" || before.attemptState != "outcome_unknown" || before.attemptCount != 1 || before.providerCallCount != 0 || before.reconcileReceipts != 0 {
		t.Fatalf("PAY05 precondition refund=%+v", before)
	}

	admin := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	viewer := accessdomain.Principal{InternalID: 8, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	for _, test := range []struct {
		name     string
		security pay05EffectsRequestSecurity
		wantCode int
		wantBody string
	}{
		{name: "missing csrf", security: pay05EffectsRequestSecurity{principal: admin, csrfErr: fmt.Errorf("missing synthetic csrf")}, wantCode: http.StatusForbidden, wantBody: "csrf_required"},
		{name: "viewer", security: pay05EffectsRequestSecurity{principal: viewer}, wantCode: http.StatusForbidden, wantBody: "permission_denied"},
	} {
		handler, err := effects.NewHTTPHandler(repository, test.security)
		if err != nil {
			t.Fatal(err)
		}
		response := pay05PostGenericReconcile(t, handler, effectID, "pay05-reconcile-"+test.name, effectport.Hash("pay05-admin-pending", test.name))
		if response.Code != test.wantCode || !strings.Contains(response.Body.String(), test.wantBody) {
			t.Fatalf("PAY05 %s reconcile status=%d body=%q", test.name, response.Code, response.Body.String())
		}
		if after := readState(); after != before {
			t.Fatalf("PAY05 %s denial mutated payment/EER state before=%+v after=%+v", test.name, before, after)
		}
	}

	handler, err := effects.NewHTTPHandler(repository, pay05EffectsRequestSecurity{principal: admin})
	if err != nil {
		t.Fatal(err)
	}
	key := "pay05-reconcile-admin-pending"
	firstDigest := effectport.Hash("pay05-admin-pending", "first")
	for _, digest := range []effects.Digest{firstDigest, firstDigest, effectport.Hash("pay05-admin-pending", "conflicting-replay")} {
		response := pay05PostGenericReconcile(t, handler, effectID, key, digest)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "state_conflict") {
			t.Fatalf("PAY05 generic Payment reconcile status=%d body=%q", response.Code, response.Body.String())
		}
		if after := readState(); after != before {
			t.Fatalf("PAY05 generic reconcile mutated payment/EER state before=%+v after=%+v", before, after)
		}
	}
}

func pay05PostGenericReconcile(t *testing.T, handler http.Handler, effectID, idempotencyKey string, evidence effects.Digest) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"evidence_digest":%q,"outcome":"pending"}`, evidence)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/external-effects/"+effectID+"/reconcile", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func pay05SeedCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, promoterID, buyerID, orderID int64, paidAt time.Time, merchant string) int64 {
	t.Helper()
	var distributorID, policyID, credentialID, attributionID, commissionID int64
	publicNo := "PAY05" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,$2,'v1',true,$3,1,$3,$3) RETURNING id`, promoterID, publicNo, paidAt).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(17,'service_period',true,2000,7,1,$1,$1) RETURNING id`, paidAt).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	credentialDigest := sha256.Sum256([]byte("pay05-credential:" + merchant))
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,17,'service_period',$2,'active',$3,$4) RETURNING id`, distributorID, credentialDigest[:], paidAt, paidAt.Add(time.Hour)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'period-17','PAY-05 service period',$2,$3,'order:pay05:item:1','eligible',$4,1,2000,7,$5) RETURNING id`, orderID, distributorID, credentialID, policyID, paidAt).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1000,0,200,200,0,2000,$4,$5,'pending','','','',1,$4,$4) RETURNING id`, attributionID, orderID, distributorID, paidAt, paidAt.Add(7*24*time.Hour)).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	_ = buyerID // The attribution is intentionally owned by a distinct synthetic promoter.
	return commissionID
}

func pay05AssertCommerce(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID, claimID int64, merchant, orderStatus string, refunded int64, entitlementStatus, redemptionStatus, claimStatus, commissionStatus string, commissionRefund, commissionPayable int64, commissionID int64) {
	t.Helper()
	var gotOrder, gotPayment, gotEntitlement, gotRedemption, gotClaim, gotCommission string
	var gotRefunded, gotPaymentAmount, gotCommissionRefund, gotCommissionPayable int64
	var grantReceipts, refundReceipts, consumeReceipts, couponReleases int
	err := pool.QueryRow(ctx, `SELECT
		(SELECT status FROM orders WHERE id=$1),
		(SELECT refunded_minor FROM orders WHERE id=$1),
		(SELECT status FROM payments WHERE id=$2 AND order_id=$1),
		(SELECT amount_minor FROM payments WHERE id=$2 AND order_id=$1),
		(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),
		(SELECT status FROM coupon_order_redemptions WHERE order_reference=$4),
		(SELECT status FROM coupon_customer_claims WHERE id=$3),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE source_order_id=$1 AND operation='grant'),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE source_order_id=$1 AND operation='refund'),
		(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$4 AND receipt.operation='consume'),
		(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$4 AND receipt.operation='release'),
		(SELECT successful_refund_minor FROM distribution_commissions WHERE id=$5),
		(SELECT current_payable_minor FROM distribution_commissions WHERE id=$5),
		(SELECT status FROM distribution_commissions WHERE id=$5)`, orderID, paymentID, claimID, merchant, commissionID).Scan(&gotOrder, &gotRefunded, &gotPayment, &gotPaymentAmount, &gotEntitlement, &gotRedemption, &gotClaim, &grantReceipts, &refundReceipts, &consumeReceipts, &couponReleases, &gotCommissionRefund, &gotCommissionPayable, &gotCommission)
	if err != nil || gotOrder != orderStatus || gotRefunded != refunded || gotPayment != "paid" || gotPaymentAmount != 1000 || gotEntitlement != entitlementStatus || gotRedemption != redemptionStatus || gotClaim != claimStatus || grantReceipts != 1 || consumeReceipts != 1 || couponReleases != 0 || gotCommission != commissionStatus || gotCommissionRefund != commissionRefund || gotCommissionPayable != commissionPayable {
		t.Fatalf("PAY05 oracle order=%q/%d payment=%q/%d entitlement=%q coupon=%q/%q grant=%d refund_receipts=%d coupon_consume=%d releases=%d commission=%q/%d/%d err=%v", gotOrder, gotRefunded, gotPayment, gotPaymentAmount, gotEntitlement, gotRedemption, gotClaim, grantReceipts, refundReceipts, consumeReceipts, couponReleases, gotCommission, gotCommissionRefund, gotCommissionPayable, err)
	}
	if gotEntitlement == "active" && refundReceipts != 0 || gotEntitlement == "refunded" && refundReceipts != 1 {
		t.Fatalf("PAY05 entitlement receipt count=%d for entitlement state %q", refundReceipts, gotEntitlement)
	}
}

func pay05AssertPartialRefund(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID, claimID int64, merchant string, commissionID int64) (time.Time, time.Time) {
	t.Helper()
	pay05AssertCommerce(t, ctx, pool, orderID, paymentID, claimID, merchant, "partially_refunded", 300, "refunded", "consumed", "redeemed", "held", 300, 140, commissionID)
	var firstRefund, receiptCount int64
	var endAt, updatedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT entitlement.end_at,entitlement.updated_at,receipt.refund_amount_minor,(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE source_order_id=$1 AND operation='refund') FROM order_service_entitlements entitlement JOIN order_entitlement_fulfillment_receipts receipt ON receipt.source_order_id=$1 AND receipt.operation='refund' WHERE entitlement.last_order_id=$1`, orderID).Scan(&endAt, &updatedAt, &firstRefund, &receiptCount); err != nil || firstRefund != 300 || receiptCount != 1 {
		t.Fatalf("PAY05 partial entitlement receipt amount=%d count=%d err=%v", firstRefund, receiptCount, err)
	}
	var delta, resulting int64
	var adjustmentCount int
	if err := pool.QueryRow(ctx, `SELECT delta_minor,resulting_payable_minor,(SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind='buyer_refund') FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind='buyer_refund' ORDER BY id`, commissionID).Scan(&delta, &resulting, &adjustmentCount); err != nil || delta != -60 || resulting != 140 || adjustmentCount != 1 {
		t.Fatalf("PAY05 partial commission delta=%d resulting=%d count=%d err=%v", delta, resulting, adjustmentCount, err)
	}
	return endAt, updatedAt
}

func pay05AssertFinalRefund(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID, claimID int64, merchant string, commissionID int64, firstEnd, firstUpdated time.Time) {
	t.Helper()
	var paymentStatus, orderStatus, entitlementStatus string
	var orderAmount, orderRefunded int64
	var refunds, completed, reconciliationPending, reconciliationRefunded, callbackReceipts, adjustments, refundReceipts int
	var endAt, updatedAt time.Time
	var commissionStatus, holdReason, cancelReason string
	var commissionRefund, commissionPayable, deltaSum int64
	err := pool.QueryRow(ctx, `SELECT
		(SELECT status FROM payments WHERE id=$2), (SELECT status FROM orders WHERE id=$1),
		(SELECT amount_minor FROM orders WHERE id=$1), (SELECT refunded_minor FROM orders WHERE id=$1),
		(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),
		(SELECT end_at FROM order_service_entitlements WHERE last_order_id=$1),
		(SELECT updated_at FROM order_service_entitlements WHERE last_order_id=$1),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE source_order_id=$1 AND operation='refund'),
		(SELECT count(*) FROM payment_refunds WHERE payment_id=$2),
		(SELECT count(*) FROM payment_refunds WHERE payment_id=$2 AND status='completed'),
		(SELECT count(*) FROM payment_reconciliations WHERE refund_id=(SELECT id FROM payment_refunds WHERE refund_no='pay05-refund-a') AND outcome='pending'),
		(SELECT count(*) FROM payment_reconciliations WHERE refund_id=(SELECT id FROM payment_refunds WHERE refund_no='pay05-refund-a') AND outcome='refunded'),
		(SELECT count(*) FROM payment_callback_receipts),
		(SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=$3 AND kind='buyer_refund'),
		(SELECT COALESCE(sum(delta_minor),0) FROM distribution_commission_adjustments WHERE commission_id=$3 AND kind='buyer_refund'),
		(SELECT successful_refund_minor FROM distribution_commissions WHERE id=$3),
		(SELECT current_payable_minor FROM distribution_commissions WHERE id=$3),
		(SELECT status FROM distribution_commissions WHERE id=$3),
		(SELECT hold_reason FROM distribution_commissions WHERE id=$3),
		(SELECT cancel_reason FROM distribution_commissions WHERE id=$3)`, orderID, paymentID, commissionID).Scan(&paymentStatus, &orderStatus, &orderAmount, &orderRefunded, &entitlementStatus, &endAt, &updatedAt, &refundReceipts, &refunds, &completed, &reconciliationPending, &reconciliationRefunded, &callbackReceipts, &adjustments, &deltaSum, &commissionRefund, &commissionPayable, &commissionStatus, &holdReason, &cancelReason)
	var redemptionStatus, claimStatus string
	if err == nil {
		err = pool.QueryRow(ctx, `SELECT (SELECT status FROM coupon_order_redemptions WHERE order_reference=$1),(SELECT status FROM coupon_customer_claims WHERE id=$2)`, merchant, claimID).Scan(&redemptionStatus, &claimStatus)
	}
	if err != nil || paymentStatus != "paid" || orderStatus != "refunded" || orderAmount != 1000 || orderRefunded != 1000 || entitlementStatus != "refunded" || redemptionStatus != "consumed" || claimStatus != "redeemed" || !endAt.Equal(firstEnd) || !updatedAt.Equal(firstUpdated) || refundReceipts != 1 || refunds != 3 || completed != 2 || reconciliationPending != 1 || reconciliationRefunded != 1 || callbackReceipts != 3 || adjustments != 2 || deltaSum != -200 || commissionRefund != 1000 || commissionPayable != 0 || commissionStatus != "cancelled" || cancelReason != "buyer_refund" {
		t.Fatalf("PAY05 final payment=%q order=%q amount=%d refunded=%d entitlement=%q end_same=%t updated_same=%t coupon=%q/%q refund_receipts=%d refunds=%d completed=%d reconciled_pending=%d reconciled_success=%d callbacks=%d commission=%q/%d/%d hold=%q cancel=%q adjustments=%d delta_sum=%d err=%v", paymentStatus, orderStatus, orderAmount, orderRefunded, entitlementStatus, endAt.Equal(firstEnd), updatedAt.Equal(firstUpdated), redemptionStatus, claimStatus, refundReceipts, refunds, completed, reconciliationPending, reconciliationRefunded, callbackReceipts, commissionStatus, commissionRefund, commissionPayable, holdReason, cancelReason, adjustments, deltaSum, err)
	}
	var finalDelta, finalResult int64
	var finalAdjCount int
	if err = pool.QueryRow(ctx, `SELECT delta_minor,resulting_payable_minor,(SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind='buyer_refund') FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind='buyer_refund' ORDER BY id DESC LIMIT 1`, commissionID).Scan(&finalDelta, &finalResult, &finalAdjCount); err != nil || finalDelta != -140 || finalResult != 0 || finalAdjCount != 2 {
		t.Fatalf("PAY05 final commission delta=%d resulting=%d count=%d err=%v", finalDelta, finalResult, finalAdjCount, err)
	}
}
