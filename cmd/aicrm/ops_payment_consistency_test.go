package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type opsConsistencyFixture struct {
	pool     *platformpostgres.Pool
	native   *pgxpool.Pool
	snapshot *platformpostgres.UnitOfWork
	customer int64
	sequence int
}

func newOpsConsistencyFixture(t *testing.T) *opsConsistencyFixture {
	t.Helper()
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	pool, e := platformpostgres.Open(ctx, platformpostgres.Config{URL: dsn})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	snapshot, e := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	f := &opsConsistencyFixture{pool: pool, native: pool.Native(), snapshot: snapshot}
	if e = f.native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&f.customer); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *opsConsistencyFixture) seed(t *testing.T, kind, paymentState, orderState string, checkout bool) (int64, int64) {
	t.Helper()
	f.sequence++
	key := fmt.Sprintf("ops-consistency-%d", f.sequence)
	now := time.Now().UTC()
	var order, payment int64
	e := f.native.QueryRow(context.Background(), `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','v3-checkout',$1,$1,$2,$2,1000,'CNY',$3,'native',true,CASE WHEN $3='paid' THEN 2 ELSE 1 END,$4,$4) RETURNING id`, key, f.customer, orderState, now).Scan(&order)
	if e != nil {
		t.Fatal(e)
	}
	e = f.native.QueryRow(context.Background(), `INSERT INTO payments(order_id,provider,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at,paid_confirmed_at) VALUES($1,'wechat_pay',$2,1,$3,$3,1000,'CNY',$4,1,$5,$5,CASE WHEN $4='paid' THEN $5::timestamptz ELSE NULL END) RETURNING id`, order, key, f.customer, paymentState, now).Scan(&payment)
	if e != nil {
		t.Fatal(e)
	}
	if checkout {
		days := 0
		if kind == "service_period" {
			days = 31
		}
		_, e = f.native.Exec(context.Background(), `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,reserved_at,created_at) VALUES($1,$2,1,'fixture','fixture',1,$3,1000,0,1000,'CNY',false,$4,$4)`, order, kind, days, now)
		if e != nil {
			t.Fatal(e)
		}
	}
	if paymentState == "paid" {
		digest := sha256.Sum256([]byte(key))
		_, e = f.native.Exec(context.Background(), `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, order, digest[:], now)
		if e != nil {
			t.Fatal(e)
		}
	}
	return order, payment
}
func TestPostgreSQLOpsPaymentOrderConsistencySnapshotAndLegalPending(t *testing.T) {
	f := newOpsConsistencyFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	payments, orders := paymentstore.NewOpsConsistencyReader(), orderstore.NewOpsConsistencyReader()
	collector := opsPaymentOrderConsistencyCollector(f.snapshot, payments, orders)
	first, e := collector.Collect(ctx, now)
	if e != nil || first.Status != "ok" || first.Metrics["observed_paid_payments"] != 0 {
		t.Fatalf("empty=%+v %v", first, e)
	}
	order, payment := f.seed(t, "standard_product", "paid", "paid", true)
	f.seed(t, "standard_product", "awaiting_payment", "pending_payment", true)
	if _, e = f.native.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','ops-future-refund',100,'fixture','requested',1,$2,$2)`, payment, now.Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	good, e := collector.Collect(ctx, now)
	if e != nil || good.Status != "ok" || good.Metrics["observed_paid_payments"] != 1 || good.Metrics["refund_total_mismatch"] != 0 || good.Metrics["external_delivery_outside_scope"] != 1 {
		t.Fatalf("legal pending=%+v %v", good, e)
	}
	// Paid -> closed is a legal Order transition and does not undo Payment.
	if _, e = f.native.Exec(ctx, `UPDATE orders SET status='closed' WHERE id=$1`, order); e != nil {
		t.Fatal(e)
	}
	closed, e := collector.Collect(ctx, now)
	if e != nil || closed.Status != "ok" {
		t.Fatalf("legally closed paid order=%+v %v", closed, e)
	}
	if _, e = f.native.Exec(ctx, `UPDATE orders SET status='pending_payment' WHERE id=$1`, order); e != nil {
		t.Fatal(e)
	}
	wrongState, e := collector.Collect(ctx, now)
	if e != nil || wrongState.Status != "critical" || wrongState.Metrics["paid_state_mismatch"] != 1 {
		t.Fatalf("paid payment with pending order=%+v %v", wrongState, e)
	}
	if _, e = f.native.Exec(ctx, `UPDATE orders SET status='paid' WHERE id=$1`, order); e != nil {
		t.Fatal(e)
	}
	historicalDigest := sha256.Sum256([]byte("ops-history"))
	var historicalOrder int64
	if e = f.native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','fixture-history','ops-history','ops-history',1000,'CNY','paid','history',false,$1,$2,$2) RETURNING id`, historicalDigest[:], now).Scan(&historicalOrder); e != nil {
		t.Fatal(e)
	}
	if _, e = f.native.Exec(ctx, `INSERT INTO payments(order_id,provider,merchant_order_no,amount_minor,currency,status,version,created_at,updated_at,historical) VALUES($1,'wechat_pay','ops-history',1000,'CNY','paid',1,$2,$2,true)`, historicalOrder, now); e != nil {
		t.Fatal(e)
	}
	good, e = collector.Collect(ctx, now)
	if e != nil || good.Status != "ok" || good.Metrics["observed_paid_payments"] != 1 {
		t.Fatalf("history is not live settlement failure=%+v %v", good, e)
	}
	if _, e = payments.ReadOpsPaidOrderPageWithin(ctx, 0, 500); e == nil {
		t.Fatal("missing snapshot accepted")
	}
	writable, e := platformpostgres.NewUnitOfWork(f.pool)
	if e != nil {
		t.Fatal(e)
	}
	if e = writable.Within(ctx, func(txctx context.Context) error {
		_, err := orders.ReadOpsPaymentOrderFactsWithin(txctx, []int64{order})
		return err
	}); e == nil {
		t.Fatal("writable/read-committed snapshot accepted")
	}
	// The first Owner read pins the shared snapshot. A concurrent committed
	// Order mutation must not be mixed into the second Owner's earlier view.
	hooked := opsPaymentReaderFunc(func(txctx context.Context, after int64, limit int32) (paymentport.OpsPaidOrderPage, error) {
		page, err := payments.ReadOpsPaidOrderPageWithin(txctx, after, limit)
		if err != nil {
			return page, err
		}
		_, err = f.native.Exec(ctx, `UPDATE orders SET amount_minor=1100 WHERE id=$1`, order)
		return page, err
	})
	frozen, e := opsPaymentOrderConsistencyCollector(f.snapshot, hooked, orders).Collect(ctx, now)
	if e != nil || frozen.Status != "ok" {
		t.Fatalf("mixed owner snapshots=%+v %v", frozen, e)
	}
	changed, e := collector.Collect(ctx, now)
	if e != nil || changed.Status != "critical" || changed.Metrics["amount_currency_mismatch"] != 1 {
		t.Fatalf("next snapshot must see committed drift=%+v %v", changed, e)
	}
	if _, e = f.native.Exec(ctx, `ALTER TABLE payment_refunds RENAME TO fixture_missing_refunds`); e != nil {
		t.Fatal(e)
	}
	if broken, err := collector.Collect(ctx, now); err == nil || broken.Status == "ok" {
		t.Fatalf("source loss became healthy=%+v %v", broken, err)
	}
}
func TestPostgreSQLOpsPaymentOrderServicePeriodFulfillmentEvidence(t *testing.T) {
	f := newOpsConsistencyFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	order, payment := f.seed(t, "service_period", "paid", "paid", true)
	collector := opsPaymentOrderConsistencyCollector(f.snapshot, paymentstore.NewOpsConsistencyReader(), orderstore.NewOpsConsistencyReader())
	missing, e := collector.Collect(ctx, now)
	if e != nil || missing.Status != "critical" || missing.Metrics["missing_service_grant"] != 1 {
		t.Fatalf("missing grant=%+v %v", missing, e)
	}
	var entitlement int64
	digest := sha256.Sum256([]byte("ops-entitlement"))
	e = f.native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,version,source_digest,created_at,updated_at) VALUES('fixture','ops-entitlement',$1,1,'fixture',$2,'active',$3,$3::timestamptz+interval '31 days',1,$4,$3,$3) RETURNING id`, f.customer, order, now, digest[:]).Scan(&entitlement)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.native.Exec(ctx, `INSERT INTO order_entitlement_fulfillment_receipts(operation,source_order_id,payload_digest,entitlement_id,result_snapshot,duration_days,created_at) VALUES('grant',$1,$2,$3,'{}',31,$4)`, order, digest[:], entitlement, now); e != nil {
		t.Fatal(e)
	}
	good, e := collector.Collect(ctx, now)
	if e != nil || good.Status != "ok" {
		t.Fatalf("valid service grant=%+v %v", good, e)
	}
	if _, e = f.native.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','ops-completed-refund',100,'fixture','completed',1,$2,$2)`, payment, now); e != nil {
		t.Fatal(e)
	}
	if _, e = f.native.Exec(ctx, `UPDATE orders SET status='partially_refunded',refunded_minor=100 WHERE id=$1`, order); e != nil {
		t.Fatal(e)
	}
	missing, e = collector.Collect(ctx, now)
	if e != nil || missing.Status != "critical" || missing.Metrics["missing_service_refund"] != 1 {
		t.Fatalf("missing revocation=%+v %v", missing, e)
	}
	if _, e = f.native.Exec(ctx, `INSERT INTO order_entitlement_fulfillment_receipts(operation,source_order_id,payload_digest,entitlement_id,result_snapshot,duration_days,refund_amount_minor,created_at) VALUES('refund',$1,$2,$3,'{}',31,100,$4)`, order, digest[:], entitlement, now); e != nil {
		t.Fatal(e)
	}
	good, e = collector.Collect(ctx, now)
	if e != nil || good.Status != "ok" || good.Metrics["refund_total_mismatch"] != 0 {
		t.Fatalf("refund receipts=%+v %v", good, e)
	}
	f.seed(t, "standard_product", "paid", "paid", false)
	unknown, e := collector.Collect(ctx, now)
	if e != nil || unknown.Status != "unknown" || unknown.Metrics["checkout_unknown"] != 1 {
		t.Fatalf("legacy checkout gap must remain unknown=%+v %v", unknown, e)
	}
}

type opsPaymentReaderFunc func(context.Context, int64, int32) (paymentport.OpsPaidOrderPage, error)

func (f opsPaymentReaderFunc) ReadOpsPaidOrderPageWithin(ctx context.Context, after int64, limit int32) (paymentport.OpsPaidOrderPage, error) {
	return f(ctx, after, limit)
}

type opsOrderReaderFunc func(context.Context, []int64) ([]orderport.OpsPaymentOrderFact, error)

func (f opsOrderReaderFunc) ReadOpsPaymentOrderFactsWithin(ctx context.Context, ids []int64) ([]orderport.OpsPaymentOrderFact, error) {
	return f(ctx, ids)
}

type opsSnapshotFixture struct{}

func (opsSnapshotFixture) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
func TestOpsPaymentConsistencyBoundedCoverageNeverLooksComplete(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	payments := opsPaymentReaderFunc(func(_ context.Context, after int64, _ int32) (paymentport.OpsPaidOrderPage, error) {
		calls++
		return paymentport.OpsPaidOrderPage{More: true, Items: []paymentport.OpsPaidOrderFact{{PaymentID: after + 1, OrderID: after + 1, AmountMinor: 1000, Provider: "wechat_pay", Currency: "CNY", PayerCustomerID: 1, BeneficiaryCustomerID: 1, PaidConfirmedAt: &now}}}, nil
	})
	orders := opsOrderReaderFunc(func(_ context.Context, ids []int64) ([]orderport.OpsPaymentOrderFact, error) {
		return []orderport.OpsPaymentOrderFact{{OrderID: ids[0], AmountMinor: 1000, Provider: "wechat_pay", Currency: "CNY", Status: "paid", RecordOrigin: "native", PayerCustomerID: 1, BeneficiaryCustomerID: 1, CheckoutPresent: true, CheckoutProductType: "standard_product", CheckoutCurrency: "CNY", CheckoutPayableMinor: 1000, PaidEventPresent: true}}, nil
	})
	result, e := opsPaymentOrderConsistencyCollector(opsSnapshotFixture{}, payments, orders).Collect(context.Background(), now)
	if e != nil || result.Status != "unknown" || result.Metrics["scan_truncated"] != 1 || calls != 20 {
		t.Fatalf("partial coverage=%+v calls=%d err=%v", result, calls, e)
	}
}
