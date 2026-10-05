package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	paymentmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/payment"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
)

// A callback receipt survives task exhaustion. Replaying the same signed event
// must therefore recreate the original query task after River discards it.
func TestPostgreSQLPaymentQueryDiscardRecoveryOnCallbackReplay(t *testing.T) {
	var queryReady atomic.Bool
	var queryAmount atomic.Int64
	paidAt := time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)
	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{
		enablePublicH5:     true,
		enableAlipay:       true,
		deferEffectsWorker: true,
		alipayQueryResponse: func(order string) map[string]string {
			if queryReady.Load() {
				n := queryAmount.Load()
				return map[string]string{"code": "10000", "msg": "Success", "out_trade_no": order, "trade_no": "query-discard-recovery-trade", "trade_status": "TRADE_SUCCESS", "total_amount": fmt.Sprintf("%d.%02d", n/100, n%100), "send_pay_date": pay02CSTString(paidAt)}
			}
			return map[string]string{"code": "20000", "msg": "System Error"}
		},
	})
	checkout := pay02CreateAlipayCheckout(t, fixture, fixture.serviceProductID, "service_period", "query-discard-recovery-checkout")
	pool := fixture.application.pool.Native()
	var amount int64
	if err := pool.QueryRow(fixture.ctx, `SELECT amount_minor FROM payments WHERE id=$1`, checkout.PaymentID).Scan(&amount); err != nil {
		t.Fatal(err)
	}

	callbackServer, signer := pay02AlipayCallbackServer(t, fixture.application.paymentReconciliation)
	t.Cleanup(callbackServer.Close)
	queryAmount.Store(amount)
	form := pay02SignedPaymentForm(t, signer, "query-discard-recovery-notify", checkout.MerchantOrder, "query-discard-recovery-trade", amount, paidAt)
	form.Del("gmt_payment")
	pay02SignValues(t, signer, form)
	postCallback := func() {
		t.Helper()
		if status, body := pay02PostAlipayForm(t, callbackServer, form); status != http.StatusOK || body != "success" {
			t.Fatalf("signed callback status=%d body=%q", status, body)
		}
	}
	postCallback()
	// Simulate an existing pre-upgrade task without a River unique key.
	if _, err := pool.Exec(fixture.ctx, `UPDATE river_job SET unique_key=NULL,unique_states=NULL WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::bigint::text`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	postCallback() // The legacy active task must still be deduplicated.

	var originalJobID int64
	if err := pool.QueryRow(fixture.ctx, `SELECT id FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::bigint::text`, checkout.PaymentID).Scan(&originalJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(fixture.ctx, `UPDATE river_job SET max_attempts=1 WHERE id=$1`, originalJobID); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	worker := paymentmodule.NewReconciliationWorker()
	if err := worker.BindService(fixture.application.paymentReconciliation); err != nil {
		t.Fatal(err)
	}
	if err := river.AddWorkerSafely[paymentmodule.ReconciliationJobArgs](workers, worker); err != nil {
		t.Fatal(err)
	}
	runtime, err := platformjobqueue.NewRuntime(pool, workers, paymentmodule.ReconciliationQueue)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(fixture.ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- runtime.Run(workerCtx) }()
	waitForRiverState(t, fixture.ctx, pool, originalJobID, "discarded")
	cancelWorker()
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatalf("stop River runtime: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("River runtime did not stop")
	}

	var beforeReplayJobs, beforeReplayDiscarded int
	if err := pool.QueryRow(fixture.ctx, `SELECT count(*),count(*) FILTER (WHERE state='discarded') FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::bigint::text`, checkout.PaymentID).Scan(&beforeReplayJobs, &beforeReplayDiscarded); err != nil {
		t.Fatal(err)
	}
	if beforeReplayJobs != 1 || beforeReplayDiscarded != 1 {
		t.Fatalf("pre-replay queue evidence jobs=%d discarded=%d; want only the final discarded job", beforeReplayJobs, beforeReplayDiscarded)
	}

	postCallback() // Exact signed replay must make the original target runnable again.
	postCallback() // Repeated recovery does not create another runnable task.
	var totalJobs, discardedJobs, runnableJobs, receipts int
	if err := pool.QueryRow(fixture.ctx, `SELECT count(*),count(*) FILTER (WHERE state='discarded'),count(*) FILTER (WHERE state IN ('available','retryable','scheduled','pending','running')) FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::bigint::text`, checkout.PaymentID).Scan(&totalJobs, &discardedJobs, &runnableJobs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(fixture.ctx, `SELECT count(*) FROM payment_callback_receipts WHERE payment_id=$1 AND outcome='query_required'`, checkout.PaymentID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if totalJobs != 2 || discardedJobs != 1 || runnableJobs != 1 || receipts != 1 {
		t.Fatalf("callback replay did not restore target: jobs=%d discarded=%d runnable=%d receipts=%d", totalJobs, discardedJobs, runnableJobs, receipts)
	}
	var paymentStatus string
	var confirmedAt *time.Time
	var grants, paidEvents int
	if err := pool.QueryRow(fixture.ctx, `SELECT status,paid_confirmed_at FROM payments WHERE id=$1`, checkout.PaymentID).Scan(&paymentStatus, &confirmedAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM order_paid_events WHERE order_id=$2)`, checkout.OrderID, checkout.OrderID).Scan(&grants, &paidEvents); err != nil {
		t.Fatal(err)
	}
	if paymentStatus == "paid" || confirmedAt != nil || grants != 0 || paidEvents != 0 {
		t.Fatalf("replay settled without Provider query: status=%s confirmed_at=%v grants=%d paid_events=%d", paymentStatus, confirmedAt, grants, paidEvents)
	}
	queryReady.Store(true)
	var recoveryJobID int64
	if err := pool.QueryRow(fixture.ctx, `SELECT id FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::bigint::text AND state<>'discarded'`, checkout.PaymentID).Scan(&recoveryJobID); err != nil {
		t.Fatal(err)
	}
	runtime, err = platformjobqueue.NewRuntime(pool, workers, paymentmodule.ReconciliationQueue)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker = context.WithCancel(fixture.ctx)
	go func() { workerDone <- runtime.Run(workerCtx) }()
	waitForRiverState(t, fixture.ctx, pool, recoveryJobID, "completed")
	cancelWorker()
	if err := <-workerDone; err != nil {
		t.Fatal(err)
	}
	postCallback() // Paid replay cannot create another task or entitlement.
	if err := pool.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM order_paid_events WHERE order_id=$1),(SELECT count(*) FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$2::bigint::text)`, checkout.OrderID, checkout.PaymentID).Scan(&grants, &paidEvents, &totalJobs); err != nil || grants != 1 || paidEvents != 1 || totalJobs != 2 {
		t.Fatalf("recovered grants=%d events=%d jobs=%d err=%v", grants, paidEvents, totalJobs, err)
	}
	t.Logf("discard recovery: exhausted query leaves one discarded job; exact signed callback replay inserts one runnable task, preserving pending payment and single receipt")
}

func waitForRiverState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, jobID int64, want string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		var state string
		if err := pool.QueryRow(ctx, `SELECT state FROM river_job WHERE id=$1`, jobID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("River job %d did not reach %s", jobID, want)
}
