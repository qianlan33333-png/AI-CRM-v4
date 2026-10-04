package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestPostgreSQLAlipayUnknownPaymentTimeQueuesOriginalQuery(t *testing.T) {
	var mu sync.Mutex
	queryReady := false
	var queryAmount int64
	paidAt := time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)
	const trade = "unknown-paid-time-trade"
	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true, alipayQueryResponse: func(order string) map[string]string {
		mu.Lock()
		defer mu.Unlock()
		if !queryReady {
			return map[string]string{"code": "20000", "msg": "System Error"}
		}
		return map[string]string{"code": "10000", "msg": "Success", "out_trade_no": order, "trade_no": trade, "trade_status": "TRADE_SUCCESS", "total_amount": fmt.Sprintf("%d.%02d", queryAmount/100, queryAmount%100), "send_pay_date": pay02CSTString(paidAt)}
	}})
	session := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "unknown-paid-time-session-0001")
	binding := publicAlipayCheckoutBinding(t, fixture, session.token)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(fmt.Sprintf(`{"product_id":%d,"product_kind":"service_period","provider":"alipay","channel":%q,"beneficiary_selection":"payer_self","checkout_session_binding":%q}`, fixture.serviceProductID, paymentdomain.ChannelAlipayWap, binding)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "unknown-paid-time-checkout-0001")
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
	response := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(response, request)
	var created alipayCheckoutCreateResult
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create period checkout status=%d body=%s", response.Code, response.Body.String())
	}
	notifyAt := paidAt.Add(3 * time.Second)
	var amount int64
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT amount_minor FROM payments WHERE id=$1`, created.PaymentID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	queryAmount = amount
	mu.Unlock()
	callbackServer, signer := pay02AlipayCallbackServer(t, fixture.application.paymentReconciliation)
	t.Cleanup(callbackServer.Close)
	form := pay02SignedPaymentForm(t, signer, "unknown-paid-time-notify", created.MerchantOrder, trade, amount, notifyAt)
	form.Del("gmt_payment")
	pay02SignValues(t, signer, form)
	post := func() {
		t.Helper()
		if status, body := pay02PostAlipayForm(t, callbackServer, form); status != http.StatusOK || body != "success" {
			t.Fatalf("signed notification status=%d body=%s", status, body)
		}
	}
	service := fixture.application.paymentReconciliation
	// A durable-queue failure must roll back the callback receipt too, so the
	// exact signed notification remains retryable after recovery.
	if _, err := fixture.application.pool.Native().Exec(fixture.ctx, `CREATE FUNCTION reject_time_query_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='payment.provider-reconciliation.v1' THEN RAISE EXCEPTION 'synthetic queue failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_time_query_job BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION reject_time_query_job()`); err != nil {
		t.Fatal(err)
	}
	if status, _ := pay02PostAlipayForm(t, callbackServer, form); status != http.StatusServiceUnavailable {
		t.Fatalf("queue failure acked: %d", status)
	}
	var rollbackReceipts int
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM payment_callback_receipts WHERE payment_id=$1`, created.PaymentID).Scan(&rollbackReceipts); err != nil || rollbackReceipts != 0 {
		t.Fatalf("partial callback receipt=%d err=%v", rollbackReceipts, err)
	}
	if _, err := fixture.application.pool.Native().Exec(fixture.ctx, `DROP TRIGGER reject_time_query_job ON river_job`); err != nil {
		t.Fatal(err)
	}
	post()
	post()
	var status string
	var confirmed *time.Time
	var receipts, jobs, paidEvents int
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT status,paid_confirmed_at FROM payments WHERE id=$1`, created.PaymentID).Scan(&status, &confirmed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM payment_callback_receipts WHERE payment_id=$1 AND outcome='query_required'),(SELECT count(*) FROM river_job WHERE kind='payment.provider-reconciliation.v1' AND args->>'payment_id'=$1::text),(SELECT count(*) FROM order_paid_events WHERE order_id=$2)`, created.PaymentID, created.OrderID).Scan(&receipts, &jobs, &paidEvents); err != nil {
		t.Fatal(err)
	}
	if status == "paid" || confirmed != nil || receipts != 1 || jobs != 1 || paidEvents != 0 {
		t.Fatalf("notification became payment-time evidence: status=%s confirmed=%v receipts=%d jobs=%d paidEvents=%d", status, confirmed, receipts, jobs, paidEvents)
	}
	var grants int
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM order_service_entitlements WHERE last_order_id=$1`, created.OrderID).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("notification granted premature entitlement=%d err=%v", grants, err)
	}
	if _, err := service.ReconcileAlipayPayment(fixture.ctx, created.PaymentID); err == nil {
		t.Fatal("query failure settled payment")
	}
	mu.Lock()
	queryReady = true
	mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, err := service.ReconcileAlipayPayment(fixture.ctx, created.PaymentID); err != nil {
			t.Fatal(err)
		}
	}
	post()
	var orderPaidAt time.Time
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT paid_confirmed_at FROM payments WHERE id=$1`, created.PaymentID).Scan(&confirmed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT occurred_at FROM order_status_history WHERE order_id=$1 AND to_status='paid'`, created.OrderID).Scan(&orderPaidAt); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM order_paid_events WHERE order_id=$1`, created.OrderID).Scan(&paidEvents); err != nil {
		t.Fatal(err)
	}
	followedAt := paidAt.Add(2 * time.Second)
	if confirmed == nil || !confirmed.Equal(paidAt) || !orderPaidAt.Equal(paidAt) || paidEvents != 1 || followedAt.Unix() < orderPaidAt.Unix() {
		t.Fatalf("actual paidAt=%v confirmed=%v order=%v paidEvents=%d", paidAt, confirmed, orderPaidAt, paidEvents)
	}
	var start time.Time
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT start_at FROM order_service_entitlements WHERE last_order_id=$1`, created.OrderID).Scan(&start); err != nil || !start.Equal(paidAt) {
		t.Fatalf("entitlement start=%v want actual payment=%v err=%v", start, paidAt, err)
	}
}
