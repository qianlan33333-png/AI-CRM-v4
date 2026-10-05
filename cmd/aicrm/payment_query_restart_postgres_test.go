package main

import (
	"context"
	"encoding/json"
	"fmt"
	paymentmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/payment"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Run actual River claim/retry/restart and Payment/Order UoW. The signed Alipay
// adapter talks only to the fixture's loopback Provider; no effect queue runs.
func TestPostgreSQLPaymentQueryWorkerRestartCompetitionAndGrantRollback(t *testing.T) {
	var phase, amount, queries atomic.Int64
	paidAt := time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true, alipayQueryResponse: func(order string) map[string]string {
		queries.Add(1)
		if phase.Load() == 0 {
			return map[string]string{"code": "20000", "msg": "System Error"}
		}
		n := amount.Load()
		return map[string]string{"code": "10000", "msg": "Success", "out_trade_no": order, "trade_no": "query-restart-loopback-trade", "trade_status": "TRADE_SUCCESS", "total_amount": fmt.Sprintf("%d.%02d", n/100, n%100), "send_pay_date": pay02CSTString(paidAt)}
	}})
	session := issuePublicCommerceTrustedH5SessionWithKey(t, f, "query-restart-trusted-session-0001")
	binding := publicAlipayCheckoutBinding(t, f, session.token)
	r := httptest.NewRequest("POST", "/api/v1/alipay/checkouts", strings.NewReader(fmt.Sprintf(`{"product_id":%d,"product_kind":"service_period","provider":"alipay","channel":%q,"beneficiary_selection":"payer_self","checkout_session_binding":%q}`, f.serviceProductID, paymentdomain.ChannelAlipayWap, binding)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "query-restart-checkout-0001")
	r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
	out := httptest.NewRecorder()
	f.application.handler.ServeHTTP(out, r)
	var created alipayCheckoutCreateResult
	if out.Code != 202 || json.Unmarshal(out.Body.Bytes(), &created) != nil {
		t.Fatalf("checkout=%d %s", out.Code, out.Body.String())
	}
	pool := f.application.pool.Native()
	var n int64
	if err := pool.QueryRow(f.ctx, `SELECT amount_minor FROM payments WHERE id=$1`, created.PaymentID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	amount.Store(n)
	callback, signer := pay02AlipayCallbackServer(t, f.application.paymentReconciliation)
	defer callback.Close()
	form := pay02SignedPaymentForm(t, signer, "query-restart-notify", created.MerchantOrder, "query-restart-loopback-trade", n, paidAt)
	form.Del("gmt_payment")
	pay02SignValues(t, signer, form)
	if status, body := pay02PostAlipayForm(t, callback, form); status != 200 || body != "success" {
		t.Fatalf("callback=%d", status)
	}
	var jobID int64
	if err := pool.QueryRow(f.ctx, `SELECT id FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::bigint::text`, created.PaymentID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	start := func() func() {
		t.Helper()
		workers := river.NewWorkers()
		w := paymentmodule.NewReconciliationWorker()
		if err := w.BindService(f.application.paymentReconciliation); err != nil {
			t.Fatal(err)
		}
		if err := river.AddWorkerSafely[paymentmodule.ReconciliationJobArgs](workers, w); err != nil {
			t.Fatal(err)
		}
		runtime, err := platformjobqueue.NewRuntime(pool, workers, paymentmodule.ReconciliationQueue)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(f.ctx)
		done := make(chan error, 1)
		go func() { done <- runtime.Run(ctx) }()
		return func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("runtime stop=%v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("runtime stop timed out")
			}
		}
	}
	waitState := func(state string) {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
			var got string
			if err := pool.QueryRow(f.ctx, `SELECT state FROM river_job WHERE id=$1`, jobID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got == state {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("job did not reach %s", state)
	}
	assertUnpaid := func() {
		t.Helper()
		var status string
		var grants, paidEvents, paidHistory int
		if err := pool.QueryRow(f.ctx, `SELECT status,(SELECT count(*) FROM order_service_entitlements WHERE last_order_id=$2),(SELECT count(*) FROM order_paid_events WHERE order_id=$2),(SELECT count(*) FROM order_status_history WHERE order_id=$2 AND to_status='paid') FROM payments WHERE id=$1`, created.PaymentID, created.OrderID).Scan(&status, &grants, &paidEvents, &paidHistory); err != nil || status == "paid" || grants != 0 || paidEvents != 0 || paidHistory != 0 {
			t.Fatalf("partial settlement status=%s grants=%d events=%d history=%d err=%v", status, grants, paidEvents, paidHistory, err)
		}
	}
	stop := start()
	waitState("retryable")
	stop()
	assertUnpaid()
	firstReads := queries.Load()
	if firstReads < 1 {
		t.Fatalf("initial provider read retries=%d", queries.Load())
	}
	// A successful query whose entitlement grant fails must not leave a paid
	// aggregate or partially emitted paid event; the same durable job recovers.
	if _, err := pool.Exec(f.ctx, `CREATE FUNCTION reject_query_grant() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic entitlement failure'; END $$; CREATE TRIGGER reject_query_grant BEFORE INSERT OR UPDATE ON order_service_entitlements FOR EACH ROW EXECUTE FUNCTION reject_query_grant()`); err != nil {
		t.Fatal(err)
	}
	phase.Store(1)
	if _, err := pool.Exec(f.ctx, `UPDATE river_job SET state='available',scheduled_at=now() WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	stop = start()
	waitState("retryable")
	stop()
	assertUnpaid()
	if queries.Load() <= firstReads {
		t.Fatalf("rollback read attempts=%d", queries.Load())
	}
	if _, err := pool.Exec(f.ctx, `DROP TRIGGER reject_query_grant ON order_service_entitlements`); err != nil {
		t.Fatal(err)
	}
	// Two actual River runtimes compete, including a duplicate delivery of this
	// original payment target. The aggregate lock and receipts must settle once.
	if _, err := pool.Exec(f.ctx, `UPDATE river_job SET state='available',scheduled_at=now() WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(f.ctx, `INSERT INTO river_job(state,args,kind,queue,max_attempts,scheduled_at,priority) SELECT 'available',args,kind,queue,max_attempts,now(),priority FROM river_job WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	phase.Store(2)
	stopA, stopB := start(), start()
	waitState("completed")
	for deadline := time.Now().Add(15 * time.Second); ; {
		var remaining int
		if err := pool.QueryRow(f.ctx, `SELECT count(*) FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::bigint::text AND state<>'completed'`, created.PaymentID).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("duplicate target not completed")
		}
		time.Sleep(25 * time.Millisecond)
	}
	stopA()
	stopB()
	var status string
	var grants, events, history, checks int
	var startAt, endAt time.Time
	if err := pool.QueryRow(f.ctx, `SELECT status,(SELECT count(*) FROM order_service_entitlements WHERE last_order_id=$2),(SELECT count(*) FROM order_paid_events WHERE order_id=$2),(SELECT count(*) FROM order_status_history WHERE order_id=$2 AND to_status='paid'),(SELECT count(*) FROM payment_reconciliations WHERE payment_id=$1) FROM payments WHERE id=$1`, created.PaymentID, created.OrderID).Scan(&status, &grants, &events, &history, &checks); err != nil || status != "paid" || grants != 1 || events != 1 || history != 1 || checks != 1 {
		t.Fatalf("status=%s grants=%d events=%d history=%d checks=%d err=%v", status, grants, events, history, checks, err)
	}
	if err := pool.QueryRow(f.ctx, `SELECT start_at,end_at FROM order_service_entitlements WHERE last_order_id=$1`, created.OrderID).Scan(&startAt, &endAt); err != nil || !startAt.Equal(paidAt) || !endAt.Equal(paidAt.Add(30*24*time.Hour)) {
		t.Fatalf("grant timing start=%s end=%s err=%v", startAt, endAt, err)
	}
	t.Logf("actual River runtimes: failed provider read -> restart; failed grant -> UoW rollback -> restart + competing duplicate target; payment/event/history/entitlement/reconciliation each one; provider reads=%d", queries.Load())
}
