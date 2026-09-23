package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestPostgreSQLPaymentConfirmationReconciliationRestoresOnlyVerifiedOrderFacts
// proves the recovery path against the real Payment and Order PostgreSQL
// stores. The sole substitute is the already-verified Provider query leaf.
func TestPostgreSQLPaymentConfirmationReconciliationRestoresOnlyVerifiedOrderFacts(t *testing.T) {
	fixture := newPaymentConfirmationFixture(t, time.Time{})
	defer fixture.close()

	query := fixture.query()
	result, err := fixture.payments.ReconcileWeChatPayPayment(context.Background(), fixture.paymentID)
	if err != nil {
		t.Fatal(err)
	}
	if result.PaidConfirmedAt == nil || !result.PaidConfirmedAt.UTC().Equal(fixture.persistedPaidAt()) {
		t.Fatalf("paid confirmation=%v; want %s", result.PaidConfirmedAt, fixture.persistedPaidAt())
	}
	if result.ProviderTransactionReference != query.TransactionReference || result.ProviderTransactionDigest != string(query.TransactionDigest) {
		t.Fatalf("verified transaction facts=%q/%q; want %q/%q", result.ProviderTransactionReference, result.ProviderTransactionDigest, query.TransactionReference, query.TransactionDigest)
	}
	fixture.assertPayment(t, fixture.persistedPaidAt(), query.TransactionReference, query.TransactionDigest)
	fixture.assertRecoveryFacts(t, 1, 1, 1)
	state, err := fixture.payments.DistributionPaymentState(context.Background(), fixture.orderID)
	if err != nil || !state.ConfirmedPaid || state.SplitCapable || state.OriginalPaymentRef == "" {
		t.Fatalf("restored distribution payment state=%+v err=%v; confirmation may qualify a purchase but must not retroactively make its payment split-capable", state, err)
	}

	// The deterministic missing-callback recovery: reconciliation has committed
	// the exact Provider SUCCESS fact first, then the original verified callback
	// arrives. It gets an immutable replay receipt and must not produce another
	// Order paid event or rerun paid consumers.
	callbackDigest := sha256.Sum256([]byte("payment-confirmation-late-callback:" + fixture.merchant))
	if err = fixture.payments.ApplyVerifiedCallback(context.Background(), paymentprovider.CallbackResult{
		EventDigest: callbackDigest, BodyDigest: callbackDigest, Kind: "payment", MerchantOrderNo: fixture.merchant, AppID: "app",
		ProviderTransactionReference: query.TransactionReference, ProviderTransactionDigest: string(query.TransactionDigest), AmountMinor: 1000, Currency: "CNY", OccurredAt: fixture.paidAt,
	}); err != nil {
		t.Fatalf("late original callback after reconciliation: %v", err)
	}
	fixture.assertPayment(t, fixture.persistedPaidAt(), query.TransactionReference, query.TransactionDigest)
	fixture.assertLateCallback(t, 1, 1)

	// Replaying the identical verified Provider fact must retain the original
	// confirmation and all durable recovery facts exactly once.
	result, err = fixture.payments.ReconcileWeChatPayPayment(context.Background(), fixture.paymentID)
	if err != nil {
		t.Fatal(err)
	}
	if result.PaidConfirmedAt == nil || !result.PaidConfirmedAt.UTC().Equal(fixture.persistedPaidAt()) {
		t.Fatalf("replayed paid confirmation=%v; want %s", result.PaidConfirmedAt, fixture.persistedPaidAt())
	}
	fixture.assertPayment(t, fixture.persistedPaidAt(), query.TransactionReference, query.TransactionDigest)
	fixture.assertRecoveryFacts(t, 1, 1, 1)
}

func TestPostgreSQLPaymentConfirmationReconciliationPreviewDoesNotWrite(t *testing.T) {
	fixture := newPaymentConfirmationFixture(t, time.Time{})
	defer fixture.close()

	preview, err := fixture.payments.PreviewReconcileWeChatPayPayment(context.Background(), fixture.paymentID)
	if err != nil || !preview.WouldRestorePaidConfirmation || preview.Reason != "payment_confirmation_missing" || preview.PaymentID != fixture.paymentID {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	fixture.assertPayment(t, time.Time{}, "", "")
	fixture.assertRecoveryFacts(t, 0, 0, 0)
}

func TestPostgreSQLPaymentConfirmationReconciliationRejectsExistingTransactionFactMismatch(t *testing.T) {
	fixture := newPaymentConfirmationFixture(t, time.Time{})
	defer fixture.close()
	other := "4200000000000099"
	otherDigest := effectport.Hash("wechatpay.transaction", other)
	if _, err := fixture.pool.Exec(context.Background(), `UPDATE payments SET provider_transaction_reference=$2,provider_transaction_digest=$3 WHERE id=$1`, fixture.paymentID, other, otherDigest); err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.payments.PreviewReconcileWeChatPayPayment(context.Background(), fixture.paymentID)
	if err != nil || preview.WouldRestorePaidConfirmation || preview.Reason != "payment_evidence_mismatch" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if _, err = fixture.payments.ReconcileWeChatPayPayment(context.Background(), fixture.paymentID); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("reconcile mismatch err=%v", err)
	}
	fixture.assertPayment(t, time.Time{}, other, otherDigest)
	fixture.assertRecoveryFacts(t, 0, 0, 0)
}

func TestPostgreSQLPaymentConfirmationReconciliationNeverOverwritesExistingConfirmation(t *testing.T) {
	original := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	fixture := newPaymentConfirmationFixture(t, original)
	defer fixture.close()

	if _, err := fixture.payments.ReconcileWeChatPayPayment(context.Background(), fixture.paymentID); err != nil {
		t.Fatal(err)
	}
	fixture.assertPayment(t, original, "", "")
	// A provider read can be recorded for reconciliation, but an already
	// confirmed payment cannot mint another restore receipt or audit fact.
	fixture.assertRecoveryFacts(t, 1, 0, 0)
}

func TestPostgreSQLPaymentConfirmationReconciliationRejectsUnprovenFacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paymentConfirmationFixture, *paymentport.WeChatPayPaymentQuery)
		query  error
	}{
		{
			name: "merchant order mismatch",
			mutate: func(_ *paymentConfirmationFixture, query *paymentport.WeChatPayPaymentQuery) {
				query.MerchantOrderNo = "M-other-order"
			},
		},
		{
			name: "amount mismatch",
			mutate: func(_ *paymentConfirmationFixture, query *paymentport.WeChatPayPaymentQuery) {
				query.AmountMinor++
			},
		},
		{
			name: "currency mismatch",
			mutate: func(_ *paymentConfirmationFixture, query *paymentport.WeChatPayPaymentQuery) {
				query.Currency = "USD"
			},
		},
		{
			name: "transaction mismatch",
			mutate: func(_ *paymentConfirmationFixture, query *paymentport.WeChatPayPaymentQuery) {
				query.TransactionReference = "4200000000000002"
				query.TransactionDigest = effectport.Hash("wechatpay.transaction", query.TransactionReference)
			},
		},
		{
			name: "paid event time mismatch",
			mutate: func(_ *paymentConfirmationFixture, query *paymentport.WeChatPayPaymentQuery) {
				query.OccurredAt = query.OccurredAt.Add(time.Second)
			},
		},
		{
			name: "provider unknown",
			mutate: func(_ *paymentConfirmationFixture, query *paymentport.WeChatPayPaymentQuery) {
				query.Status = "NOT_FOUND"
			},
		},
		{name: "provider query error", query: errors.New("provider unavailable")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPaymentConfirmationFixture(t, time.Time{})
			defer fixture.close()
			query := fixture.query()
			if test.mutate != nil {
				test.mutate(fixture, &query)
			}
			fixture.reconciler.query, fixture.reconciler.err = query, test.query

			_, err := fixture.payments.ReconcileWeChatPayPayment(context.Background(), fixture.paymentID)
			if test.query != nil {
				if err == nil {
					t.Fatal("provider query error was accepted")
				}
			} else if query.Status == "SUCCESS" {
				if err == nil {
					t.Fatal("unproven provider fact was accepted")
				}
			} else if err != nil {
				t.Fatalf("non-terminal provider result: %v", err)
			}

			fixture.assertPayment(t, time.Time{}, "", "")
			// Unknown Provider state may be recorded for later reconciliation, but
			// it must never produce a paid-confirmation receipt or audit fact.
			fixture.assertRecoveryFacts(t, -1, 0, 0)
		})
	}
}

type paymentConfirmationProvider struct {
	query paymentport.WeChatPayPaymentQuery
	err   error
}

func (provider *paymentConfirmationProvider) QueryPayment(_ context.Context, _ string) (paymentport.WeChatPayPaymentQuery, error) {
	if provider.err != nil {
		return paymentport.WeChatPayPaymentQuery{}, provider.err
	}
	return provider.query, nil
}

func (*paymentConfirmationProvider) QueryRefund(context.Context, string) (paymentport.WeChatPayRefundQuery, error) {
	return paymentport.WeChatPayRefundQuery{}, errors.New("unused Provider query")
}

// These two ready-boundary collaborators are intentionally inert: the
// deterministic late-callback branch only reads its already-settled Payment
// and writes its receipt. The real PostgreSQL Order and Payment stores remain
// under test; neither checkout-session nor new effect acceptance is reachable.
type paymentConfirmationSessions struct{}

func (paymentConfirmationSessions) ConsumeWithin(context.Context, string, time.Time) (paymentport.SessionActor, error) {
	return paymentport.SessionActor{}, paymentport.ErrInvalid
}
func (paymentConfirmationSessions) LookupWithin(context.Context, string, time.Time) (paymentport.SessionActor, error) {
	return paymentport.SessionActor{}, paymentport.ErrInvalid
}
func (paymentConfirmationSessions) SelectPayerSelfWithin(context.Context, string, time.Time) (paymentport.SessionActor, error) {
	return paymentport.SessionActor{}, paymentport.ErrInvalid
}

type paymentConfirmationEffects struct{}

func (paymentConfirmationEffects) AcceptAndQueueWithin(context.Context, effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	return effectport.Projection{}, effectport.Receipt{}, errors.New("late callback must not accept an effect")
}

type paymentConfirmationFixture struct {
	pool       *pgxpool.Pool
	close      func()
	orderID    int64
	paymentID  int64
	merchant   string
	paidAt     time.Time
	payments   *paymentapp.Service
	reconciler *paymentConfirmationProvider
}

func newPaymentConfirmationFixture(t *testing.T, existingConfirmation time.Time) *paymentConfirmationFixture {
	t.Helper()
	pool, closePool := refundWorkerPool(t)
	ctx := context.Background()
	// The Provider fact deliberately has sub-microsecond precision. PostgreSQL
	// persists timestamptz values at microsecond precision, which exercises the
	// reconciliation-first then late-callback comparison without hiding it by
	// truncating the Provider value.
	paidAt := time.Date(2026, 9, 13, 10, 30, 0, 123456789, time.UTC)

	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		closePool()
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	reconciler := &paymentConfirmationProvider{}
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orders, paymentConfirmationSessions{}, paymentConfirmationEffects{})
	if err = payments.SetWeChatPayReconciler(reconciler); err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}

	customerID := refundWorkerCustomer(t, ctx, pool)
	var orderID, paymentID int64
	merchant := fmt.Sprintf("M-payment-confirmation-%d", customerID)
	transaction := "4200000000000001"
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,provider_transaction_no,version,created_at,updated_at) VALUES('wechat_pay','payment-confirmation',$1,$2,$3,$3,1000,'CNY','paid','native',true,$4,2,$5,$5) RETURNING id`, merchant, merchant, customerID, transaction, paidAt).Scan(&orderID); err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,1001,1,'payment-confirmation','Payment confirmation product',1000,1,1000)`, orderID); err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',1001,'payment-confirmation','Payment confirmation product',1,0,1000,0,1000,'CNY',false,'',$2,$2)`, orderID, paidAt); err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}
	paidDigest := sha256.Sum256([]byte("payment-confirmation.order-paid:" + merchant))
	if _, err = pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, paidDigest[:], paidAt); err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}
	var confirmedAt any
	if !existingConfirmation.IsZero() {
		confirmedAt = existingConfirmation
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,1,$3,$3,1000,'CNY','paid',false,1,$4,$5,$5) RETURNING id`, orderID, merchant, customerID, confirmedAt, paidAt).Scan(&paymentID); err != nil {
		wrapped.Close()
		closePool()
		t.Fatal(err)
	}

	fixture := &paymentConfirmationFixture{pool: pool, orderID: orderID, paymentID: paymentID, merchant: merchant, paidAt: paidAt, payments: payments, reconciler: reconciler}
	fixture.close = func() {
		wrapped.Close()
		closePool()
	}
	fixture.reconciler.query = fixture.query()
	return fixture
}

func (fixture *paymentConfirmationFixture) query() paymentport.WeChatPayPaymentQuery {
	transaction := "4200000000000001"
	return paymentport.WeChatPayPaymentQuery{
		MerchantOrderNo:      fixture.merchant,
		AmountMinor:          1000,
		Currency:             "CNY",
		Status:               "SUCCESS",
		TransactionReference: transaction,
		TransactionDigest:    effectport.Hash("wechatpay.transaction", transaction),
		OccurredAt:           fixture.paidAt,
		EvidenceDigest:       effectport.Hash("payment-confirmation.provider-query", fixture.merchant),
	}
}

func (fixture *paymentConfirmationFixture) persistedPaidAt() time.Time {
	return fixture.paidAt.Truncate(time.Microsecond)
}

func (fixture *paymentConfirmationFixture) assertPayment(t *testing.T, wantConfirmed time.Time, wantReference string, wantDigest effectport.Digest) {
	t.Helper()
	var confirmedAt *time.Time
	var reference, digest, status string
	var amount int64
	var splitMarked bool
	if err := fixture.pool.QueryRow(context.Background(), `SELECT paid_confirmed_at,COALESCE(provider_transaction_reference,''),COALESCE(provider_transaction_digest,''),status,amount_minor,profit_sharing_marked FROM payments WHERE id=$1`, fixture.paymentID).Scan(&confirmedAt, &reference, &digest, &status, &amount, &splitMarked); err != nil {
		t.Fatal(err)
	}
	if wantConfirmed.IsZero() {
		if confirmedAt != nil {
			t.Fatalf("paid confirmation=%s; want NULL", confirmedAt.UTC())
		}
	} else if confirmedAt == nil || !confirmedAt.UTC().Equal(wantConfirmed.UTC()) {
		t.Fatalf("paid confirmation=%v; want %s", confirmedAt, wantConfirmed.UTC())
	}
	if reference != wantReference || digest != string(wantDigest) {
		t.Fatalf("provider reference/digest=%q/%q; want %q/%q", reference, digest, wantReference, wantDigest)
	}
	if status != "paid" || amount != 1000 || splitMarked {
		t.Fatalf("reconciliation changed non-confirmation payment facts status=%q amount=%d profit_sharing_marked=%v", status, amount, splitMarked)
	}
}

// reconciliationCount=-1 deliberately permits a non-terminal Provider query
// receipt while continuing to require no confirmation recovery facts.
func (fixture *paymentConfirmationFixture) assertRecoveryFacts(t *testing.T, reconciliationCount, receiptCount, auditCount int) {
	t.Helper()
	ctx := context.Background()
	if reconciliationCount >= 0 {
		var actual int
		if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM payment_reconciliations WHERE payment_id=$1`, fixture.paymentID).Scan(&actual); err != nil || actual != reconciliationCount {
			t.Fatalf("payment reconciliations=%d err=%v; want %d", actual, err, reconciliationCount)
		}
	}
	var receipts, audits int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM payment_operation_receipts WHERE operation='reconcile' AND result_kind='payment' AND result_id=$1`, fixture.paymentID).Scan(&receipts); err != nil || receipts != receiptCount {
		t.Fatalf("confirmation receipts=%d err=%v; want %d", receipts, err, receiptCount)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM payment_audit_events WHERE event_type='payment.confirmation_reconciled' AND aggregate_id=$1`, fixture.paymentID).Scan(&audits); err != nil || audits != auditCount {
		t.Fatalf("confirmation audits=%d err=%v; want %d", audits, err, auditCount)
	}
}

func (fixture *paymentConfirmationFixture) assertLateCallback(t *testing.T, callbackReceipts, paidEvents int) {
	t.Helper()
	var actualReceipts, actualEvents int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT
  (SELECT count(*) FROM payment_callback_receipts WHERE payment_id=$1 AND outcome='replayed'),
  (SELECT count(*) FROM order_paid_events WHERE order_id=$2)`, fixture.paymentID, fixture.orderID).Scan(&actualReceipts, &actualEvents); err != nil || actualReceipts != callbackReceipts || actualEvents != paidEvents {
		t.Fatalf("late callback receipts=%d paid events=%d err=%v; want %d/%d", actualReceipts, actualEvents, err, callbackReceipts, paidEvents)
	}
}
