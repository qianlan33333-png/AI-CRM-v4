package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// Uses PostgreSQL rather than a fake store. A same-total payment whose
// provider transaction digest was legally changed must fail reconciliation.
func TestPostgreSQLHistoricalPaymentVerifierRejectsPerRowDrift(t *testing.T) {
	pool, cleanup := historicalPaymentPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	orderDigest := sha256.Sum256([]byte("order-source-row"))
	paymentDigest := sha256.Sum256([]byte("payment-source-row"))
	refundDigest := sha256.Sum256([]byte("refund-source-row"))
	var orderID, paymentID, refundID int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at) VALUES('wechat_pay','commerce-history','source-order','merchant-history','transaction-history',11,11,100,40,'CNY','partially_refunded','history',false,$1,1,$2,$2) RETURNING id`, orderDigest[:], now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	transactionDigest := string(effectport.Hash("history.transaction", "transaction-history"))
	if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,provider_transaction_digest,version,created_at,updated_at,historical) VALUES($1,'wechat_pay','mini_program','merchant-history',101,11,11,100,'CNY','paid',$2,1,$3,$3,true) RETURNING id`, orderID, transactionDigest, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	paymentKey := sha256.Sum256([]byte("payment-history:run-001:merchant-history"))
	if _, err := pool.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('history_import','run-001',$1,$2,'payment',$3,$4)`, paymentKey[:], paymentDigest[:], paymentID, now); err != nil {
		t.Fatal(err)
	}
	providerRefundDigest := string(effectport.Hash("history.refund", "provider-refund-history"))
	if err := pool.QueryRow(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,provider_refund_digest,version,created_at,updated_at) VALUES($1,'wechat_pay','refund-history',40,'历史退款','completed',$2,1,$3,$3) RETURNING id`, paymentID, providerRefundDigest, now).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	refundKey := sha256.Sum256([]byte("refund-history:run-001:refund-history"))
	if _, err := pool.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('history_import','run-001',$1,$2,'refund',$3,$4)`, refundKey[:], refundDigest[:], refundID, now); err != nil {
		t.Fatal(err)
	}
	insertHistoryFacts(t, ctx, pool, "payment.history_imported", paymentID, "run-001", now)
	insertHistoryFacts(t, ctx, pool, "payment.refund_history_imported", refundID, "run-001", now)
	verifier := PostgreSQLVerifier{Pool: pool}
	payments := []HistoricalPaymentFact{{OrderID: orderID, Provider: paymentdomain.ProviderWeChatPay, MerchantOrderNo: "merchant-history", PayerIdentityID: 101, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 100, Currency: "CNY", Status: paymentdomain.StatusPaid, ProviderTransactionReference: "transaction-history", SourceDigest: paymentDigest, CreatedAt: now, UpdatedAt: now}}
	refunds := []HistoricalRefundFact{{OrderID: orderID, Provider: paymentdomain.ProviderWeChatPay, MerchantOrderNo: "merchant-history", RefundNo: "refund-history", Reason: "历史退款", AmountMinor: 40, ProviderRefundReference: "provider-refund-history", SourceDigest: refundDigest, OccurredAt: now}}
	matched, err := verifier.VerifyHistorical(ctx, "run-001", []int64{orderID}, payments, refunds)
	if err != nil || matched.Payments != 1 || matched.Refunds != 1 || matched.AmountMinor != 100 || matched.RefundMinor != 40 {
		t.Fatalf("matched=%+v err=%v", matched, err)
	}
	// A legacy ledger can reconcile with no claimed source confirmation. Once a
	// source does claim one, it must match the immutable persisted fact exactly.
	confirmed := now
	payments[0].PaidConfirmedAt = &confirmed
	if _, err = verifier.VerifyHistorical(ctx, "run-001", []int64{orderID}, payments, refunds); !errors.Is(err, ErrHistoricalReconciliationMismatch) {
		t.Fatalf("missing persisted confirmation accepted claimed source fact: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE payments SET paid_confirmed_at=$1 WHERE id=$2`, confirmed, paymentID); err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.VerifyHistorical(ctx, "run-001", []int64{orderID}, payments, refunds); err != nil {
		t.Fatalf("exact persisted confirmation rejected: %v", err)
	}
	payments[0].PaidConfirmedAt = nil
	if _, err = verifier.VerifyHistorical(ctx, "run-001", []int64{orderID}, payments, refunds); !errors.Is(err, ErrHistoricalReconciliationMismatch) {
		t.Fatalf("claimed confirmation cannot disappear from reconciliation: %v", err)
	}
	payments[0].PaidConfirmedAt = &confirmed

	for _, status := range []paymentdomain.RefundStatus{paymentdomain.RefundHistoryRequested, paymentdomain.RefundHistoryProcessing, paymentdomain.RefundHistoryFailed, paymentdomain.RefundHistoryClosed} {
		if _, err = pool.Exec(ctx, `UPDATE payment_refunds SET status=$1 WHERE id=$2`, status, refundID); err != nil {
			t.Fatal(err)
		}
		if _, err = verifier.VerifyHistorical(ctx, "run-001", []int64{orderID}, payments, refunds); !errors.Is(err, ErrHistoricalReconciliationMismatch) {
			t.Fatal("refund status drift accepted")
		}
		refunds[0].Status = status
		result, e := verifier.VerifyHistorical(ctx, "run-001", []int64{orderID}, payments, refunds)
		if e != nil || result.Refunds != 1 || result.RefundMinor != 0 {
			t.Fatalf("historical status reconciliation %s: %+v %v", status, result, e)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE payments SET provider_transaction_digest='sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff' WHERE id=$1`, paymentID); err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.VerifyHistorical(ctx, "run-001", []int64{orderID}, payments, refunds); !errors.Is(err, ErrHistoricalReconciliationMismatch) {
		t.Fatalf("same-total per-row payment drift err=%v", err)
	}
	newDigest := sha256.Sum256([]byte("fresh-payment-row"))
	if _, e := pool.Exec(ctx, `INSERT INTO payment_history_source_deltas(result_kind,result_id,run_key,before_digest,after_digest,before_version,after_version,before_status,after_status) VALUES('payment',$1,'run-002',$2,$3,1,2,'paid','paid')`, paymentID, paymentDigest[:], newDigest[:]); e != nil {
		t.Fatal(e)
	}
	if e := verifier.verifyHistoryVersion(ctx, "payment", paymentID, 2, "run-002", newDigest, "payment.history_imported", now); e != nil {
		t.Fatalf("valid chain: %v", e)
	}
	if e := verifier.verifyHistoryVersion(ctx, "payment", paymentID, 3, "run-002", newDigest, "payment.history_imported", now); !errors.Is(e, ErrHistoricalReconciliationMismatch) {
		t.Fatalf("missing version accepted: %v", e)
	}
	if e := verifier.verifyHistoryVersion(ctx, "payment", paymentID, 2, "run-002", paymentDigest, "payment.history_imported", now); !errors.Is(e, ErrHistoricalReconciliationMismatch) {
		t.Fatalf("wrong digest accepted: %v", e)
	}

}

func insertHistoryFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, event string, aggregateID int64, runKey string, occurredAt time.Time) {
	t.Helper()
	payload := fmt.Sprintf(`{"aggregate_id":%d,"occurred_at":"%s"}`, aggregateID, occurredAt.UTC().Format(time.RFC3339Nano))
	digest := sha256.Sum256([]byte(payload))
	idempotency := event + ":" + strconv.FormatInt(aggregateID, 10) + ":" + fmt.Sprintf("%x", digest[:8])
	if _, err := pool.Exec(ctx, `INSERT INTO payment_audit_events(event_type,aggregate_id,actor_scope,payload,occurred_at) VALUES($1,$2,$3,$4::jsonb,$5)`, event, aggregateID, "migration:"+runKey, payload, occurredAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES($1,$2,$3,$4::jsonb,$5)`, event, idempotency, aggregateID, payload, occurredAt); err != nil {
		t.Fatal(err)
	}
}

func historicalPaymentPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping payment migration PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_payment_migration_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate payment migration test")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0020_order.sql", "0021_payment.sql", "0024_order_product_version.sql", "0025_payment_reconciliation.sql", "0061_product_public_purchase.sql", "0068_payment_session_beneficiary_selection.sql", "0127_payment_historical_refund_states.sql", "0131_payment_historical_unassigned.sql", "0134_payment_history_source_delta.sql", "0140_payment_h5_unionid_verified.sql", "0143_payment_checkout_abandonments.sql", "0144_payment_checkout_restart_permissions.sql", "0161_payment_paid_confirmation_time.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
