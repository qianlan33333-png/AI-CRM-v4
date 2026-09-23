package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestPostgreSQLRefundWorkerRepricesOnceAndRevokesOnlyAfterLastQualification
// exercises the real Distribution worker against real Identity, Order, and
// Payment ports. Provider I/O is deliberately absent: payment/refund terminal
// facts already exist before the durable worker starts.
func TestPostgreSQLRefundWorkerRepricesOnceAndRevokesOnlyAfterLastQualification(t *testing.T) {
	pool, cleanup := refundWorkerPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, nil)
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}
	due := &refundWorkerDue{}
	refunds := &refundWorkerRechecks{}
	worker, err := distributionapp.NewRefundService(uow, distributionRepository, due, refunds, qualification, orders)
	if err != nil {
		t.Fatal(err)
	}

	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	qualificationA := refundWorkerPaidOrder(t, ctx, pool, promoter, 701, "qualification-a", now.Add(-4*time.Hour))
	qualificationB := refundWorkerPaidOrder(t, ctx, pool, promoter, 701, "qualification-b", now.Add(-3*time.Hour))
	buyerOrder := refundWorkerPaidOrder(t, ctx, pool, buyer, 701, "buyer-order", now.Add(-2*time.Hour))
	refundWorkerCompletedRefund(t, ctx, pool, buyerOrder.paymentID, "buyer-refund", 300, now.Add(-time.Hour))

	commissionID := refundWorkerCommission(t, ctx, pool, promoter, buyerOrder.orderID, 701, now)

	// Two worker deliveries of one immutable refund fact race after a restart.
	// Both read the same cumulative Payment total under locks; exactly one
	// adjustment can be appended.
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- worker.RunRefundRecheck(ctx, distributionapp.RefundRecheckJobArgs{OrderID: buyerOrder.orderID, State: "successful", OccurredAt: now, ReceiptKey: "buyer-refund-terminal-1"})
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if runErr := <-results; runErr != nil {
			t.Fatalf("concurrent refund worker: %v", runErr)
		}
	}
	refundWorkerAssertCommission(t, ctx, pool, commissionID, "pending", 300, 140, "")
	refundWorkerAssertAdjustmentCount(t, ctx, pool, commissionID, "buyer_refund", 1)

	// Refunding the first self-purchase retains the commission because the
	// second exact-product purchase remains payment-confirmed and unrefunded.
	refundWorkerCompletedRefund(t, ctx, pool, qualificationA.paymentID, "qualification-refund-a", 1000, now)
	if err = worker.RunRefundRecheck(ctx, distributionapp.RefundRecheckJobArgs{OrderID: qualificationA.orderID, State: "successful", OccurredAt: now.Add(time.Minute), ReceiptKey: "qualification-refund-a"}); err != nil {
		t.Fatalf("first qualification refund: %v", err)
	}
	refundWorkerAssertCommission(t, ctx, pool, commissionID, "pending", 300, 140, "")

	// The second refund removes the final qualifying proof. The frozen buyer
	// attribution is unchanged, while the current commission is cancelled and
	// the revocation is appended as an auditable adjustment.
	refundWorkerCompletedRefund(t, ctx, pool, qualificationB.paymentID, "qualification-refund-b", 1000, now.Add(2*time.Minute))
	if err = worker.RunRefundRecheck(ctx, distributionapp.RefundRecheckJobArgs{OrderID: qualificationB.orderID, State: "successful", OccurredAt: now.Add(2 * time.Minute), ReceiptKey: "qualification-refund-b"}); err != nil {
		t.Fatalf("last qualification refund: %v", err)
	}
	refundWorkerAssertCommission(t, ctx, pool, commissionID, "cancelled", 300, 0, "qualification_revoked")
	refundWorkerAssertAdjustmentCount(t, ctx, pool, commissionID, "qualification_revoke", 1)
}

type refundWorkerDue struct {
	mu  sync.Mutex
	ids []int64
}

func (q *refundWorkerDue) EnqueueCommissionDueWithin(_ context.Context, id int64, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
	return nil
}

type refundWorkerRechecks struct{}

func (*refundWorkerRechecks) EnqueueRefundRecheckWithin(context.Context, distributionapp.RefundRecheckJobArgs) error {
	return nil
}

type refundWorkerOrder struct{ orderID, paymentID int64 }

func refundWorkerCustomer(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers(status,version,lineage_version,created_at,updated_at) VALUES('active',1,1,clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func refundWorkerPaidOrder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, customerID, productID int64, key string, paidAt time.Time) refundWorkerOrder {
	t.Helper()
	var orderID, paymentID int64
	merchant := "M-refund-worker-" + key
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','refund-worker',$1,$2,$3,$3,1000,'CNY','paid','native',true,2,$4,$4) RETURNING id`, key, merchant, customerID, paidAt).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,'refund-worker-product','Refund worker product',1000,1,1000)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',$2,'refund-worker-product','Refund worker product',1,0,1000,0,1000,'CNY',false,'',$3,$3)`, orderID, productID, paidAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, refundWorkerDigest(key+":paid"), paidAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,1,$3,$3,1000,'CNY','paid',false,1,$4,$4,$4) RETURNING id`, orderID, merchant, customerID, paidAt).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	return refundWorkerOrder{orderID: orderID, paymentID: paymentID}
}

func refundWorkerCompletedRefund(t *testing.T, ctx context.Context, pool *pgxpool.Pool, paymentID int64, refundNo string, amount int64, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay',$2,$3,'terminal refund fixture','completed',1,$4,$4)`, paymentID, refundNo, amount, at); err != nil {
		t.Fatal(err)
	}
}

func refundWorkerCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, promoterID, buyerOrderID, productID int64, now time.Time) int64 {
	t.Helper()
	var distributorID, policyID, credentialID, attributionID, commissionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,'DSTREFUNDWORKER','v1',true,$2,1,$2,$2) RETURNING id`, promoterID, now).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,'standard_product',true,2000,7,1,$2,$2) RETURNING id`, productID, now).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,$2,'standard_product',$3,'active',$4,$5) RETURNING id`, distributorID, productID, refundWorkerDigest("credential"), now, now.Add(time.Hour)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'refund-worker-product','Refund worker product',$2,$3,'order:1:item:1','eligible',$4,1,2000,7,$5) RETURNING id`, buyerOrderID, distributorID, credentialID, policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1000,0,200,200,0,2000,$4,$5,'pending','','','',1,$4,$4) RETURNING id`, attributionID, buyerOrderID, distributorID, now, now.Add(7*24*time.Hour)).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	return commissionID
}

func refundWorkerAssertCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commissionID int64, wantStatus string, wantRefund, wantPayable int64, wantCancel string) {
	t.Helper()
	var status, cancel string
	var refunded, payable int64
	if err := pool.QueryRow(ctx, `SELECT status,successful_refund_minor,current_payable_minor,cancel_reason FROM distribution_commissions WHERE id=$1`, commissionID).Scan(&status, &refunded, &payable, &cancel); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || refunded != wantRefund || payable != wantPayable || cancel != wantCancel {
		t.Fatalf("commission status=%q refunded=%d payable=%d cancel=%q; want %q %d %d %q", status, refunded, payable, cancel, wantStatus, wantRefund, wantPayable, wantCancel)
	}
}

func refundWorkerAssertAdjustmentCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commissionID int64, kind string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind=$2`, commissionID, kind).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("commission=%d adjustment kind=%s got=%d want=%d", commissionID, kind, got, want)
	}
}

func refundWorkerDigest(value string) []byte {
	result := make([]byte, 32)
	copy(result, []byte(fmt.Sprintf("%032s", value)))
	return result
}

func refundWorkerPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Distribution refund worker PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var entropy [8]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	schema := "aicrm_distribution_refund_worker_" + hex.EncodeToString(entropy[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close(ctx)
		t.Fatal("locate migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, name := range []string{
		"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0010_product.sql",
		"0020_order.sql", "0021_payment.sql", "0024_order_product_version.sql", "0025_payment_reconciliation.sql",
		"0049_order_history_attribution.sql", "0055_order_service_entitlements.sql", "0061_product_public_purchase.sql",
		"0068_payment_session_beneficiary_selection.sql", "0070_service_period_entitlement_fulfillment.sql",
		"0076_order_checkout_snapshots.sql", "0088_order_service_entitlement_alliance.sql", "0095_product_external_push.sql",
		"0127_payment_historical_refund_states.sql", "0131_payment_historical_unassigned.sql", "0134_payment_history_source_delta.sql",
		"0140_payment_h5_unionid_verified.sql", "0143_payment_checkout_abandonments.sql", "0144_payment_checkout_restart_permissions.sql",
		"0156_distribution_profit_sharing_payment.sql", "0157_distribution_core.sql", "0158_order_distribution_qualification_evidence.sql", "0161_payment_paid_confirmation_time.sql", "0165_payment_profit_sharing_receiver_failure_class.sql", "0166_payment_profit_sharing_instruction_failure_class.sql", "0167_distribution_settlement_not_paid_exception.sql",
	} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(cleanup)
	}
}

var _ = distributiondomain.CommissionPending

// TestPostgreSQLSettlementWorkerAcceptsOneInstructionAndReplaysAfterRestart
// joins the real Distribution and Payment applications in one PostgreSQL UoW.
// The reconciler is intentionally a deterministic Provider read: it proves
// durable EER acceptance, immutable instruction reuse, reserve release, and
// unfreeze replay without calling WeChat during a repository test.
func TestPostgreSQLSettlementWorkerAcceptsOneInstructionAndReplaysAfterRestart(t *testing.T) {
	pool, cleanup := refundWorkerPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	provider := &settlementWorkerProvider{now: now}
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, settlementWorkerEffects{})
	if err = payments.SetPaymentChannelAppIDs("wx-settlement-worker", ""); err != nil {
		t.Fatal(err)
	}
	if err = payments.SetProfitSharingReconciler(provider); err != nil {
		t.Fatal(err)
	}
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}

	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	_ = refundWorkerPaidOrder(t, ctx, pool, promoter, 702, "settlement-qualification", now.Add(-2*time.Hour))
	buyerOrder := refundWorkerPaidOrder(t, ctx, pool, buyer, 702, "settlement-buyer", now.Add(-time.Hour))
	if _, err = pool.Exec(ctx, `UPDATE payments SET profit_sharing_marked=true,provider_transaction_reference='4200000000000001',provider_transaction_digest=$2 WHERE id=$1`, buyerOrder.paymentID, effectport.Hash("settlement-worker-transaction")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES($1,1,'wx-settlement-worker','wechat-app:wx-settlement-worker','mini_program',$2,'ready',1,$3,$3)`, promoter, effectport.Hash("settlement-worker-receiver"), now); err != nil {
		t.Fatal(err)
	}
	commissionID := refundWorkerCommission(t, ctx, pool, promoter, buyerOrder.orderID, 702, now)
	if _, err = pool.Exec(ctx, `UPDATE distribution_distributors SET receiver_reference='psrecv_1',receiver_app_id='wx-settlement-worker',receiver_ready=true,receiver_reason='',receiver_checked_at=$2 WHERE customer_id=$1`, promoter, now); err != nil {
		t.Fatal(err)
	}
	// A commission cannot become due before the immutable payment-confirmed
	// fact. Seed a completed waiting period rather than manufacturing that
	// invalid state just to make the worker runnable.
	paidAt := now.Add(-8 * 24 * time.Hour)
	dueAt := paidAt.Add(7 * 24 * time.Hour)
	if _, err = pool.Exec(ctx, `UPDATE distribution_commissions SET paid_confirmed_at=$2,due_at=$3,created_at=$2,updated_at=$2 WHERE id=$1`, commissionID, paidAt, dueAt); err != nil {
		t.Fatal(err)
	}

	first, err := distributionapp.NewSettlementService(uow, distributionRepository, qualification, payments)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.RunCommissionDueCheck(ctx, commissionID); err == nil {
		t.Fatal("accepted split must retain a durable reconciliation job")
	} else {
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) {
			t.Fatalf("first due execution=%v, want durable snooze", err)
		}
	}

	// Reconstructing the application models a worker process restart. It must
	// query and reuse the exact accepted instruction rather than minting a
	// second EER effect or payment reserve.
	restarted, err := distributionapp.NewSettlementService(uow, distributionRepository, qualification, payments)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("receiver success plus unfreeze reconciliation=%v", err)
	}
	if err = restarted.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("post-restart replay=%v", err)
	}

	var status string
	var paidMinor int64
	if err = pool.QueryRow(ctx, `SELECT status,paid_minor FROM distribution_commissions WHERE id=$1`, commissionID).Scan(&status, &paidMinor); err != nil || status != string(distributiondomain.CommissionPaid) || paidMinor != 200 {
		t.Fatalf("commission status=%q paid=%d err=%v", status, paidMinor, err)
	}
	var instructionCount, reserveCount, releasedReserveCount, unfreezeCount, effectCount int
	if err = pool.QueryRow(ctx, `SELECT
	(SELECT count(*) FROM payment_profit_sharing_instructions),
	(SELECT count(*) FROM payment_profit_sharing_reserves),
	(SELECT count(*) FROM payment_profit_sharing_reserves WHERE state='released'),
	(SELECT count(*) FROM payment_profit_sharing_unfreezes),
	(SELECT count(*) FROM external_effects WHERE owner='payment' AND kind IN ('wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'))`).Scan(&instructionCount, &reserveCount, &releasedReserveCount, &unfreezeCount, &effectCount); err != nil {
		t.Fatal(err)
	}
	if instructionCount != 1 || reserveCount != 1 || releasedReserveCount != 1 || unfreezeCount != 1 || effectCount != 2 {
		t.Fatalf("instruction=%d reserve=%d released=%d unfreeze=%d effects=%d", instructionCount, reserveCount, releasedReserveCount, unfreezeCount, effectCount)
	}
	if splits, unfreezes := provider.calls(); splits != 1 || unfreezes != 1 {
		t.Fatalf("provider query replayed terminal effects: splits=%d unfreezes=%d", splits, unfreezes)
	}
}

func TestPostgreSQLSettlementWorkerClosedInstructionPreservesOrCancelsOnlyWithBusinessFacts(t *testing.T) {
	pool, cleanup := refundWorkerPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	provider := &settlementWorkerProvider{now: now, splitResult: paymentport.ProfitSharingProviderResult{State: "FINISHED", ReceiverConfirmedFailure: true, FailureClass: "receiver_receipt_limit", OutcomeKnown: true, EvidenceDigest: effectport.Hash("settlement-worker-closed"), OccurredAt: now.Add(time.Minute)}}
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, settlementWorkerEffects{})
	if err = payments.SetPaymentChannelAppIDs("wx-settlement-worker", ""); err != nil {
		t.Fatal(err)
	}
	if err = payments.SetProfitSharingReconciler(provider); err != nil {
		t.Fatal(err)
	}
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}

	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	_ = refundWorkerPaidOrder(t, ctx, pool, promoter, 703, "closed-settlement-qualification", now.Add(-2*time.Hour))
	buyerOrder := refundWorkerPaidOrder(t, ctx, pool, buyer, 703, "closed-settlement-buyer", now.Add(-time.Hour))
	if _, err = pool.Exec(ctx, `UPDATE payments SET profit_sharing_marked=true,provider_transaction_reference='4200000000000002',provider_transaction_digest=$2 WHERE id=$1`, buyerOrder.paymentID, effectport.Hash("closed-settlement-transaction")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES($1,1,'wx-settlement-worker','wechat-app:wx-settlement-worker','mini_program',$2,'ready',1,$3,$3)`, promoter, effectport.Hash("closed-settlement-receiver"), now); err != nil {
		t.Fatal(err)
	}
	commissionID := refundWorkerCommission(t, ctx, pool, promoter, buyerOrder.orderID, 703, now)
	if _, err = pool.Exec(ctx, `UPDATE distribution_distributors SET receiver_reference='psrecv_1',receiver_app_id='wx-settlement-worker',receiver_ready=true,receiver_reason='',receiver_checked_at=$2 WHERE customer_id=$1`, promoter, now); err != nil {
		t.Fatal(err)
	}
	paidAt := now.Add(-8 * 24 * time.Hour)
	if _, err = pool.Exec(ctx, `UPDATE distribution_commissions SET paid_confirmed_at=$2,due_at=$3,created_at=$2,updated_at=$2 WHERE id=$1`, commissionID, paidAt, paidAt.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	worker, err := distributionapp.NewSettlementService(uow, distributionRepository, qualification, payments)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err == nil {
		t.Fatal("accepted split must retain a durable reconciliation job")
	} else {
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) {
			t.Fatalf("first due execution=%v, want durable snooze", err)
		}
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("closed instruction plus unfreeze=%v", err)
	}
	// Model an administrator completing the operational exception before a
	// delayed due-check delivery repeats the same immutable provider result.
	if _, err = pool.Exec(ctx, `UPDATE distribution_exceptions SET status='resolved',version=version+1,updated_at=clock_timestamp() WHERE commission_id=$1 AND kind='settlement_not_paid'`, commissionID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("closed instruction replay after resolution=%v", err)
	}

	var status, exceptionStatus, exceptionReason, settlementState, failureClass string
	var payable, paid, unpaid, exceptionAmount, instructions, exceptions, negativeAdjustments, unfreezes, effects int64
	if err = pool.QueryRow(ctx, `SELECT c.status,c.current_payable_minor,c.paid_minor,s.state,e.status,e.reason,e.unpaid_due_minor,e.amount_minor,(SELECT failure_class FROM payment_profit_sharing_instructions LIMIT 1),(SELECT count(*) FROM payment_profit_sharing_instructions),(SELECT count(*) FROM distribution_exceptions WHERE commission_id=c.id AND kind='settlement_not_paid'),(SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=c.id AND kind='settlement_not_paid'),(SELECT count(*) FROM payment_profit_sharing_unfreezes),(SELECT count(*) FROM external_effects WHERE owner='payment' AND kind IN ('wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1')) FROM distribution_commissions c JOIN distribution_settlements s ON s.commission_id=c.id JOIN distribution_exceptions e ON e.commission_id=c.id AND e.kind='settlement_not_paid' WHERE c.id=$1`, commissionID).Scan(&status, &payable, &paid, &settlementState, &exceptionStatus, &exceptionReason, &unpaid, &exceptionAmount, &failureClass, &instructions, &exceptions, &negativeAdjustments, &unfreezes, &effects); err != nil {
		t.Fatal(err)
	}
	if status != "exception" || payable != 200 || paid != 0 || settlementState != "exception" || exceptionStatus != "resolved" || exceptionReason != "payment_receiver_receipt_limit" || unpaid != 200 || exceptionAmount != 200 || failureClass != "receiver_receipt_limit" || instructions != 1 || exceptions != 1 || negativeAdjustments != 0 || unfreezes != 1 || effects != 2 {
		t.Fatalf("closed commission status=%q payable=%d paid=%d settlement=%q exception_status=%q reason=%q unpaid=%d amount=%d failure=%q instructions=%d exceptions=%d negative_adjustments=%d unfreezes=%d effects=%d", status, payable, paid, settlementState, exceptionStatus, exceptionReason, unpaid, exceptionAmount, failureClass, instructions, exceptions, negativeAdjustments, unfreezes, effects)
	}
	// A manual replay reads the immutable provider instruction again, but it
	// must not accept another split, exception, reserve, or unfreeze intent.
	// The durable counts above prove that no money-changing effect was replayed.
	if splits, unfreezeCalls := provider.calls(); splits != 2 || unfreezeCalls != 1 {
		t.Fatalf("terminal closed reconciliation reads=%d unfreeze_reads=%d", splits, unfreezeCalls)
	}

	// A partial authoritative refund still leaves an unpaid balance. The same
	// immutable CLOSED instruction can be queried again, but it must not erase
	// the outstanding commission or create another settlement exception.
	if _, err = pool.Exec(ctx, `UPDATE distribution_commissions SET successful_refund_minor=500,current_payable_minor=100,status='exception',exception_reason='buyer_refund_after_paid',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, commissionID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("partial-refund closed replay=%v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT status,current_payable_minor,paid_minor FROM distribution_commissions WHERE id=$1`, commissionID).Scan(&status, &payable, &paid); err != nil || status != "exception" || payable != 100 || paid != 0 {
		t.Fatalf("partial refund must retain unpaid obligation status=%q payable=%d paid=%d err=%v", status, payable, paid, err)
	}

	// Only a durable full buyer-refund fact can cancel an already-submitted
	// instruction. The matching business exception remains history but is
	// resolved in the same Distribution UoW, so it cannot advertise a recovery
	// or merchant-liability action after cancellation.
	if _, err = pool.Exec(ctx, `UPDATE distribution_commissions SET successful_refund_minor=1000,current_payable_minor=0,status='exception',exception_reason='buyer_refund_after_paid',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, commissionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,settlement_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) SELECT $1,id,'buyer_refund_after_paid','open',0,0,0,'buyer_refund_after_paid','refund:closed-worker','order-refund:fixture',1,clock_timestamp(),clock_timestamp() FROM distribution_settlements WHERE commission_id=$1`, commissionID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("full-refund closed replay=%v", err)
	}
	var buyerExceptionStatus string
	if err = pool.QueryRow(ctx, `SELECT c.status,c.current_payable_minor,c.paid_minor,c.exception_reason,e.status FROM distribution_commissions c JOIN distribution_exceptions e ON e.commission_id=c.id AND e.kind='buyer_refund_after_paid' WHERE c.id=$1`, commissionID).Scan(&status, &payable, &paid, &exceptionReason, &buyerExceptionStatus); err != nil || status != "cancelled" || payable != 0 || paid != 0 || exceptionReason != "" || buyerExceptionStatus != "resolved" {
		t.Fatalf("full refund must cancel and close its business exception status=%q payable=%d paid=%d commission_reason=%q exception_status=%q err=%v", status, payable, paid, exceptionReason, buyerExceptionStatus, err)
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("cancelled closed replay=%v", err)
	}
	var buyerExceptionCount int64
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM distribution_exceptions WHERE commission_id=$1 AND kind='buyer_refund_after_paid'`, commissionID).Scan(&buyerExceptionCount); err != nil || buyerExceptionCount != 1 {
		t.Fatalf("cancelled replay recreated business exception count=%d err=%v", buyerExceptionCount, err)
	}
}

// TestPostgreSQLSettlementAdminObservationPreservesQualificationSourceForDueReplay
// proves that an administrator's Payment query is an observation, not a rewrite
// of the durable qualification-revocation source fact. The subsequent due job
// must still cancel an unpaid CLOSED instruction exactly once.
func TestPostgreSQLSettlementAdminObservationPreservesQualificationSourceForDueReplay(t *testing.T) {
	pool, cleanup := refundWorkerPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	provider := &settlementWorkerProvider{now: now, splitResult: paymentport.ProfitSharingProviderResult{State: "CLOSED", ReceiverConfirmedFailure: true, FailureClass: "merchant_permission_revoked", OutcomeKnown: true, EvidenceDigest: effectport.Hash("admin-source-preservation"), OccurredAt: now.Add(time.Minute)}}
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, settlementWorkerEffects{})
	if err = payments.SetPaymentChannelAppIDs("wx-admin-source", ""); err != nil {
		t.Fatal(err)
	}
	if err = payments.SetProfitSharingReconciler(provider); err != nil {
		t.Fatal(err)
	}
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}
	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	_ = refundWorkerPaidOrder(t, ctx, pool, promoter, 704, "admin-source-qualification", now.Add(-2*time.Hour))
	buyerOrder := refundWorkerPaidOrder(t, ctx, pool, buyer, 704, "admin-source-buyer", now.Add(-time.Hour))
	if _, err = pool.Exec(ctx, `UPDATE payments SET profit_sharing_marked=true,provider_transaction_reference='4200000000000004',provider_transaction_digest=$2 WHERE id=$1`, buyerOrder.paymentID, effectport.Hash("admin-source-payment")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES($1,1,'wx-admin-source','wechat-app:wx-admin-source','mini_program',$2,'ready',1,$3,$3)`, promoter, effectport.Hash("admin-source-receiver"), now); err != nil {
		t.Fatal(err)
	}
	commissionID := refundWorkerCommission(t, ctx, pool, promoter, buyerOrder.orderID, 704, now)
	if _, err = pool.Exec(ctx, `UPDATE distribution_distributors SET receiver_reference='psrecv_2',receiver_app_id='wx-admin-source',receiver_ready=true,receiver_reason='',receiver_checked_at=$2 WHERE customer_id=$1`, promoter, now); err != nil {
		t.Fatal(err)
	}
	paidAt := now.Add(-8 * 24 * time.Hour)
	if _, err = pool.Exec(ctx, `UPDATE distribution_commissions SET paid_confirmed_at=$2,due_at=$3,created_at=$2,updated_at=$2 WHERE id=$1`, commissionID, paidAt, paidAt.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	worker, err := distributionapp.NewSettlementService(uow, distributionRepository, qualification, payments)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err == nil {
		t.Fatal("accepted split must retain a durable reconciliation job")
	} else {
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) {
			t.Fatalf("first due execution=%v, want durable snooze", err)
		}
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("closed instruction=%v", err)
	}

	var qualificationExceptionID, qualificationVersion int64
	if err = pool.QueryRow(ctx, `INSERT INTO distribution_exceptions(commission_id,settlement_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at)
		SELECT $1,id,'qualification_revoked_after_paid','open',200,0,0,'qualification_revoked_after_paid','qualification:admin-source','order-refund:fixture',1,$2,$2
		FROM distribution_settlements WHERE commission_id=$1
		RETURNING id,version`, commissionID, now).Scan(&qualificationExceptionID, &qualificationVersion); err != nil {
		t.Fatal(err)
	}
	admin, err := distributionapp.NewAdminService(uow, distributionRepository, payments)
	if err != nil {
		t.Fatal(err)
	}
	adminCommand := distributionport.AdminExceptionCommand{ExceptionID: qualificationExceptionID, ExpectedVersion: qualificationVersion, ActorScope: "access:42", IdempotencyKey: "admin-source-query-key"}
	if err = admin.ReconcileException(ctx, adminCommand); err != nil {
		t.Fatalf("admin payment observation=%v", err)
	}
	var status, sourceReason, sourceEvidence string
	var sourceAmount, sourceVersion, observations int64
	if err = pool.QueryRow(ctx, `SELECT e.status,e.reason,e.evidence_reference,e.amount_minor,e.version,(SELECT count(*) FROM distribution_audit_events a WHERE a.aggregate_type='exception' AND a.aggregate_id=e.id AND a.event_type='distribution.exception_reconciled.v1') FROM distribution_exceptions e WHERE e.id=$1`, qualificationExceptionID).Scan(&status, &sourceReason, &sourceEvidence, &sourceAmount, &sourceVersion, &observations); err != nil {
		t.Fatal(err)
	}
	if status != "open" || sourceReason != "qualification_revoked_after_paid" || sourceEvidence != "qualification:admin-source" || sourceAmount != 0 || sourceVersion != qualificationVersion+1 || observations != 1 {
		t.Fatalf("admin observation rewrote source status=%q reason=%q evidence=%q amount=%d version=%d observations=%d", status, sourceReason, sourceEvidence, sourceAmount, sourceVersion, observations)
	}

	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("qualification cancellation after admin observation=%v", err)
	}
	var commissionStatus, commissionReason, qualificationStatus string
	var payable, paid, qualificationCount, qualificationAdjustments int64
	if err = pool.QueryRow(ctx, `SELECT c.status,c.cancel_reason,c.current_payable_minor,c.paid_minor,e.status,e.reason,e.evidence_reference,e.amount_minor,(SELECT count(*) FROM distribution_exceptions x WHERE x.commission_id=c.id AND x.kind='qualification_revoked_after_paid'),(SELECT count(*) FROM distribution_commission_adjustments a WHERE a.commission_id=c.id AND a.kind='qualification_revoke') FROM distribution_commissions c JOIN distribution_exceptions e ON e.id=$2 WHERE c.id=$1`, commissionID, qualificationExceptionID).Scan(&commissionStatus, &commissionReason, &payable, &paid, &qualificationStatus, &sourceReason, &sourceEvidence, &sourceAmount, &qualificationCount, &qualificationAdjustments); err != nil {
		t.Fatal(err)
	}
	if commissionStatus != "cancelled" || commissionReason != "qualification_revoked" || payable != 0 || paid != 0 || qualificationStatus != "resolved" || sourceReason != "qualification_revoked_after_paid" || sourceEvidence != "qualification:admin-source" || sourceAmount != 0 || qualificationCount != 1 || qualificationAdjustments != 1 {
		t.Fatalf("due replay lost qualification source commission=%q/%q payable=%d paid=%d exception=%q/%q/%q amount=%d count=%d adjustments=%d", commissionStatus, commissionReason, payable, paid, qualificationStatus, sourceReason, sourceEvidence, sourceAmount, qualificationCount, qualificationAdjustments)
	}
	if err = worker.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("cancelled due replay=%v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind='qualification_revoke'`, commissionID).Scan(&qualificationAdjustments); err != nil || qualificationAdjustments != 1 {
		t.Fatalf("cancelled replay duplicated qualification adjustment=%d err=%v", qualificationAdjustments, err)
	}
}

// TestPostgreSQLAdminAfterSalesLedgerUsesLivePaidDelta proves administrative
// handling uses current confirmed commission facts, not an old exception
// snapshot. Recovery and merchant liability share one append-only cap.
func TestPostgreSQLAdminAfterSalesLedgerUsesLivePaidDelta(t *testing.T) {
	pool, cleanup := refundWorkerPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	payment := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, nil)
	admin, err := distributionapp.NewAdminService(uow, repository, payment)
	if err != nil {
		t.Fatal(err)
	}
	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	order := refundWorkerPaidOrder(t, ctx, pool, buyer, 705, "admin-after-sales", now)
	commissionID := refundWorkerCommission(t, ctx, pool, promoter, order.orderID, 705, now)
	if _, err = pool.Exec(ctx, `UPDATE distribution_commissions SET current_payable_minor=60,paid_minor=100,status='exception',exception_reason='buyer_refund_after_paid',version=version+1,updated_at=$2 WHERE id=$1`, commissionID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO distribution_commission_adjustments(commission_id,kind,delta_minor,resulting_payable_minor,reason,source_reference,occurred_at) VALUES($1,'manual_recovery',10,60,'manual_recovery','receipt:previous',$2)`, commissionID, now); err != nil {
		t.Fatal(err)
	}
	var buyerExceptionID, qualificationExceptionID int64
	if err = pool.QueryRow(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'buyer_refund_after_paid','open',0,100,0,'buyer_refund_after_paid','refund:source','order-refund:fixture',1,$2,$2) RETURNING id`, commissionID, now).Scan(&buyerExceptionID); err != nil {
		t.Fatal(err)
	}
	tooLarge := distributionport.AdminExceptionCommand{ExceptionID: buyerExceptionID, ExpectedVersion: 1, AmountMinor: 31, ActorScope: "access:9", Reason: "manual_recovery", EvidenceReference: "receipt:new", IdempotencyKey: "admin-after-sales-too-large"}
	if err = admin.RecordRecovery(ctx, tooLarge); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("combined cap recovery=%v", err)
	}
	recovery := tooLarge
	recovery.AmountMinor, recovery.IdempotencyKey = 30, "admin-after-sales-recovery"
	if err = admin.RecordRecovery(ctx, recovery); err != nil {
		t.Fatalf("recovery from current paid delta=%v", err)
	}
	if err = admin.RecordRecovery(ctx, recovery); err != nil {
		t.Fatalf("recovery exact replay=%v", err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'qualification_revoked_after_paid','open',0,100,0,'qualification_revoked_after_paid','qualification:source','order-refund:fixture',1,$2,$2) RETURNING id`, commissionID, now).Scan(&qualificationExceptionID); err != nil {
		t.Fatal(err)
	}
	tooMuchLiability := distributionport.AdminExceptionCommand{ExceptionID: qualificationExceptionID, ExpectedVersion: 1, AmountMinor: 61, ActorScope: "access:9", Reason: "merchant accepts reversal", IdempotencyKey: "admin-after-sales-liability-large"}
	if err = admin.RecordMerchantLiability(ctx, tooMuchLiability); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("combined cap liability=%v", err)
	}
	liability := tooMuchLiability
	liability.AmountMinor, liability.IdempotencyKey = 60, "admin-after-sales-liability"
	if err = admin.RecordMerchantLiability(ctx, liability); err != nil {
		t.Fatalf("liability from current paid amount=%v", err)
	}
	var buyerReason, buyerEvidence, qualificationReason, qualificationEvidence, buyerStatus, qualificationStatus string
	var buyerAmount, qualificationAmount, handled, adjustmentCount, recoveryReceipts, liabilityReceipts int64
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT reason FROM distribution_exceptions WHERE id=$2),
		(SELECT evidence_reference FROM distribution_exceptions WHERE id=$2),
		(SELECT amount_minor FROM distribution_exceptions WHERE id=$2),
		(SELECT status FROM distribution_exceptions WHERE id=$2),
		(SELECT reason FROM distribution_exceptions WHERE id=$3),
		(SELECT evidence_reference FROM distribution_exceptions WHERE id=$3),
		(SELECT amount_minor FROM distribution_exceptions WHERE id=$3),
		(SELECT status FROM distribution_exceptions WHERE id=$3),
		(SELECT COALESCE(SUM(delta_minor),0) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind IN ('manual_recovery','merchant_liability')),
		(SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind IN ('manual_recovery','merchant_liability')),
		(SELECT count(*) FROM distribution_operation_receipts WHERE operation='recovery'),
		(SELECT count(*) FROM distribution_operation_receipts WHERE operation='merchant_liability')`, commissionID, buyerExceptionID, qualificationExceptionID).Scan(&buyerReason, &buyerEvidence, &buyerAmount, &buyerStatus, &qualificationReason, &qualificationEvidence, &qualificationAmount, &qualificationStatus, &handled, &adjustmentCount, &recoveryReceipts, &liabilityReceipts); err != nil {
		t.Fatal(err)
	}
	if buyerReason != "buyer_refund_after_paid" || buyerEvidence != "refund:source" || buyerAmount != 0 || buyerStatus != "recovery_recorded" || qualificationReason != "qualification_revoked_after_paid" || qualificationEvidence != "qualification:source" || qualificationAmount != 0 || qualificationStatus != "merchant_liability_recorded" || handled != 100 || adjustmentCount != 3 || recoveryReceipts != 1 || liabilityReceipts != 1 {
		t.Fatalf("immutable sources/ledger reason=%q evidence=%q amount=%d status=%q qualification=%q/%q/%d/%q handled=%d adjustments=%d receipts=%d/%d", buyerReason, buyerEvidence, buyerAmount, buyerStatus, qualificationReason, qualificationEvidence, qualificationAmount, qualificationStatus, handled, adjustmentCount, recoveryReceipts, liabilityReceipts)
	}
}

type settlementWorkerEffects struct{}

func (settlementWorkerEffects) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	if !command.Valid() {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("invalid fixture effect")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES($1,$2,$3,$4,$5,$6,$7,'queued') RETURNING id`, command.Envelope.Owner, command.Envelope.Kind, command.Envelope.SourceRefDigest, command.Envelope.TargetRefDigest, command.Envelope.PayloadDigest, command.Envelope.PolicyVersionHash, command.Envelope.Fingerprint()).Scan(&id); err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", id), Owner: command.Envelope.Owner, Kind: command.Envelope.Kind, State: effectport.StateQueued, Generation: 1, UpdatedAt: time.Now().UTC()}, effectport.Receipt{}, nil
}

type settlementWorkerProvider struct {
	mu                        sync.Mutex
	now                       time.Time
	splitResult               paymentport.ProfitSharingProviderResult
	splitCalls, unfreezeCalls int
}

func (p *settlementWorkerProvider) QueryProfitSharing(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.splitCalls++
	if p.splitResult.State != "" {
		return p.splitResult, nil
	}
	return paymentport.ProfitSharingProviderResult{State: "FINISHED", ReceiverConfirmedSuccess: true, OutcomeKnown: true, EvidenceDigest: effectport.Hash("settlement-worker-split", fmt.Sprint(p.splitCalls)), OccurredAt: p.now.Add(time.Minute)}, nil
}

func (p *settlementWorkerProvider) QueryProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unfreezeCalls++
	return paymentport.ProfitSharingProviderResult{State: "FINISHED", OutcomeKnown: true, EvidenceDigest: effectport.Hash("settlement-worker-unfreeze", fmt.Sprint(p.unfreezeCalls)), OccurredAt: p.now.Add(2 * time.Minute)}, nil
}

func (p *settlementWorkerProvider) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.splitCalls, p.unfreezeCalls
}
