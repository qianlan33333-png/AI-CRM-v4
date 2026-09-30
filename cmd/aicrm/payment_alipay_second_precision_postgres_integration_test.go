package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	alipaysdk "github.com/smartwalle/alipay/v3"
	"github.com/smartwalle/nsign"
)

type alipaySecondSecurity struct{}

func (alipaySecondSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}
func (alipaySecondSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}

// The signed HTTP request, Payment/Order transaction, paid consumers and SQL
// readbacks are real; keys, products and Provider query results are synthetic.
func TestPostgreSQLAlipaySignedSecondPrecision(t *testing.T) {
	var queryMu sync.Mutex
	queryResponses := map[string]map[string]string{}
	responseFor := func(orderNo string) map[string]string {
		queryMu.Lock()
		defer queryMu.Unlock()
		return queryResponses[orderNo]
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true, alipayQueryResponse: responseFor})
	s := f.application.paymentDistribution.(*paymentapp.Service)
	keyPath, publicKey := virtualAlipayFixtureCredentials(t)
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	const appID = "virtual-alipay-test-app"
	verifier, err := paymentprovider.NewAlipay(paymentprovider.AlipayConfig{Enabled: true, Production: true, AppID: appID, PrivateKey: string(key), AlipayPublicKey: publicKey, NotifyURL: "https://example.test/callback", ReturnURL: "https://example.test/return"})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := alipaysdk.New(appID, string(key), true)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := paymenthttp.NewHandler(s, nil, alipaySecondSecurity{}, true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetAlipayCallbackVerifier(verifier); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	uow, err := platformpostgres.NewUnitOfWork(f.application.pool)
	if err != nil {
		t.Fatal(err)
	}
	orderRepo, err := orderstore.NewPostgreSQL(f.application.pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 20, 1, 2, 3, 0, time.UTC)
	for i, tc := range []struct{ first, field, kind string }{{"callback", "notify_time", "standard"}, {"callback", "gmt_payment", "service_period"}, {"query", "gmt_payment", "standard"}} {
		t.Run(tc.first+"_"+tc.field+"_"+tc.kind, func(t *testing.T) {
			session := issuePublicCommerceTrustedH5SessionWithKey(t, f, fmt.Sprintf("alipay-second-session-%d", i))
			productID := f.productID
			if tc.first == "query" {
				productID = seedAlipayPageCheckoutProduct(t, f)
			}
			if tc.kind == "service_period" {
				productID = f.serviceProductID
			}
			binding := publicAlipayCheckoutBinding(t, f, session.token)
			body := fmt.Sprintf(`{"product_id":%d,"product_kind":%q,"provider":"alipay","channel":"alipay_wap","beneficiary_selection":"payer_self","checkout_session_binding":%q}`, productID, tc.kind, binding)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", fmt.Sprintf("alipay-second-create-%d", i))
			request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
			createdResponse := httptest.NewRecorder()
			f.application.handler.ServeHTTP(createdResponse, request)
			var created alipayCheckoutCreateResult
			if createdResponse.Code != http.StatusAccepted || json.Unmarshal(createdResponse.Body.Bytes(), &created) != nil {
				t.Fatalf("create status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
			}
			paymentUpdated, orderUpdated := at.Add(900001*time.Microsecond), at.Add(456789*time.Microsecond)
			if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE payments SET created_at=$2,updated_at=$3 WHERE id=$1`, created.PaymentID, at.Add(123456*time.Microsecond), paymentUpdated); err != nil {
				t.Fatal(err)
			}
			if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE orders SET created_at=$2,updated_at=$2 WHERE id=$1`, created.OrderID, orderUpdated); err != nil {
				t.Fatal(err)
			}
			var amount int64
			if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT amount_minor FROM payments WHERE id=$1`, created.PaymentID).Scan(&amount); err != nil {
				t.Fatal(err)
			}
			trade := fmt.Sprintf("alipay-second-trade-%d", i)
			values := url.Values{"app_id": {appID}, "notify_id": {fmt.Sprintf("alipay-second-notify-%d", i)}, "out_trade_no": {created.MerchantOrder}, "trade_no": {trade}, "trade_status": {"TRADE_SUCCESS"}, "total_amount": {fmt.Sprintf("%d.%02d", amount/100, amount%100)}, "sign_type": {"RSA2"}, tc.field: {at.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05")}}
			post := func(v url.Values, tamper bool) int {
				t.Helper()
				sig, e := signer.SignValues(v, nsign.WithIgnore("sign", "sign_type", "alipay_cert_sn"))
				if e != nil {
					t.Fatal(e)
				}
				v.Set("sign", base64.StdEncoding.EncodeToString(sig))
				if tamper {
					v.Set("sign", "tampered")
				}
				response, e := http.Post(server.URL+"/api/public/alipay/callback", "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
				if e != nil {
					t.Fatal(e)
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				return response.StatusCode
			}
			unpaid := func() {
				t.Helper()
				var status, orderStatus string
				var receipts int
				if e := f.application.pool.Native().QueryRow(f.ctx, `SELECT p.status,o.status,(SELECT count(*) FROM payment_callback_receipts WHERE payment_id=p.id) FROM payments p JOIN orders o ON o.id=p.order_id WHERE p.id=$1`, created.PaymentID).Scan(&status, &orderStatus, &receipts); e != nil {
					t.Fatal(e)
				}
				if status == "paid" || orderStatus != "pending_payment" || receipts != 0 {
					t.Fatalf("partial/rejected settlement persisted %s %s %d", status, orderStatus, receipts)
				}
			}
			for _, bad := range []string{"signature", "amount", "app", "order", "older-second"} {
				v := url.Values{}
				for k, vs := range values {
					v[k] = append([]string(nil), vs...)
				}
				want := http.StatusConflict
				switch bad {
				case "signature":
					want = http.StatusUnauthorized
				case "app":
					v.Set("app_id", "wrong-app")
					want = http.StatusUnauthorized
				case "order":
					v.Set("out_trade_no", "unknown-second-order")
					want = http.StatusNotFound
				case "amount":
					v.Set("total_amount", "999999.00")
				case "older-second":
					v.Set(tc.field, at.Add(-time.Second).In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05"))
				}
				if got := post(v, bad == "signature"); got != want {
					t.Fatalf("%s status=%d want=%d", bad, got, want)
				}
				unpaid()
			}
			// A same-second Payment cannot bypass a later Order timestamp.
			if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE orders SET updated_at=$2 WHERE id=$1`, created.OrderID, at.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if got := post(values, false); got != http.StatusConflict {
				t.Fatalf("later Order accepted: %d", got)
			}
			unpaid()
			if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE orders SET updated_at=$2 WHERE id=$1`, created.OrderID, orderUpdated); err != nil {
				t.Fatal(err)
			}
			queryMu.Lock()
			queryResponses[created.MerchantOrder] = map[string]string{"code": "10000", "msg": "Success", "out_trade_no": created.MerchantOrder, "trade_no": trade, "trade_status": "TRADE_SUCCESS", "total_amount": values.Get("total_amount"), "send_pay_date": at.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05")}
			queryMu.Unlock()
			if tc.first == "query" {
				if _, err = s.ReconcileAlipayPayment(f.ctx, created.PaymentID); err != nil {
					t.Fatalf("same-second query: %v", err)
				}
			} else {
				// A downstream failure must roll back even the callback receipt.
				if _, err = f.application.pool.Native().Exec(f.ctx, `CREATE FUNCTION reject_second_paid() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic paid-event failure'; END $$; CREATE TRIGGER reject_second_paid BEFORE INSERT ON order_paid_events FOR EACH ROW EXECUTE FUNCTION reject_second_paid()`); err != nil {
					t.Fatal(err)
				}
				if got := post(values, false); got == http.StatusOK {
					t.Fatal("failed consumer accepted")
				}
				unpaid()
				if _, err = f.application.pool.Native().Exec(f.ctx, `DROP TRIGGER reject_second_paid ON order_paid_events; DROP FUNCTION reject_second_paid()`); err != nil {
					t.Fatal(err)
				}
			}
			var callbacks sync.WaitGroup
			statuses := make(chan int, 4)
			for n := 0; n < 4; n++ {
				v := url.Values{}
				for k, vs := range values {
					v[k] = append([]string(nil), vs...)
				}
				callbacks.Add(1)
				go func() { defer callbacks.Done(); statuses <- post(v, false) }()
			}
			callbacks.Wait()
			close(statuses)
			for got := range statuses {
				if got != http.StatusOK {
					t.Fatalf("signed same-second callback status=%d want=200", got)
				}
			}

			if _, err = s.ReconcileAlipayPayment(f.ctx, created.PaymentID); err != nil {
				t.Fatal(err)
			}
			if got := post(values, false); got != http.StatusOK {
				t.Fatalf("replay status=%d", got)
			}
			values.Set("notify_id", fmt.Sprintf("alipay-second-replay-%d", i))
			if got := post(values, false); got != http.StatusOK {
				t.Fatalf("new notification replay status=%d", got)
			}
			conflict := url.Values{}
			for k, vs := range values {
				conflict[k] = append([]string(nil), vs...)
			}
			conflict.Set("notify_id", fmt.Sprintf("alipay-second-conflict-%d", i))
			conflict.Set("trade_no", "another-provider-transaction")
			if got := post(conflict, false); got != http.StatusConflict {
				t.Fatalf("different paid transaction accepted: %d", got)
			}
			var confirmed, pupdated, oupdated, eventAt, historyAt, auditAt time.Time
			var pv, ov, events, receipts int
			err = f.application.pool.Native().QueryRow(f.ctx, `SELECT p.paid_confirmed_at,p.updated_at,o.updated_at,pe.occurred_at,h.occurred_at,a.occurred_at,p.version,o.version,(SELECT count(*) FROM order_paid_events WHERE order_id=o.id),(SELECT count(*) FROM payment_callback_receipts WHERE payment_id=p.id) FROM payments p JOIN orders o ON o.id=p.order_id JOIN order_paid_events pe ON pe.order_id=o.id JOIN order_status_history h ON h.order_id=o.id AND h.order_version=pe.order_version JOIN payment_audit_events a ON a.aggregate_id=p.id AND a.event_type='payment.settled' WHERE p.id=$1`, created.PaymentID).Scan(&confirmed, &pupdated, &oupdated, &eventAt, &historyAt, &auditAt, &pv, &ov, &events, &receipts)
			if err != nil {
				t.Fatal(err)
			}
			if !confirmed.Equal(at) || !eventAt.Equal(at) || !historyAt.Equal(at) || !auditAt.Equal(at) || !pupdated.Equal(paymentUpdated) || !oupdated.Equal(orderUpdated) || pv != 3 || ov != 2 || events != 1 || receipts != 2 {
				t.Fatalf("time/once oracle: confirmed=%s event=%s history=%s audit=%s payment_updated=%s order_updated=%s versions=%d/%d events=%d receipts=%d", confirmed, eventAt, historyAt, auditAt, pupdated, oupdated, pv, ov, events, receipts)
			}
			var paymentOutboxAt, orderPaidOutboxAt, orderStatusOutboxAt time.Time
			var settlementReceipts int
			if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT po.occurred_at,paid.occurred_at,changed.occurred_at,(SELECT count(*) FROM payment_operation_receipts WHERE result_kind='payment' AND result_id=$1 AND actor_scope='provider') FROM payment_outbox po JOIN order_outbox paid ON paid.aggregate_id=$2 AND paid.event_type='order.paid.v1' JOIN order_outbox changed ON changed.aggregate_id=$2 AND changed.event_type='order.status_changed' WHERE po.aggregate_id=$1 AND po.event_type='payment.settled'`, created.PaymentID, created.OrderID).Scan(&paymentOutboxAt, &orderPaidOutboxAt, &orderStatusOutboxAt, &settlementReceipts); err != nil {
				t.Fatal(err)
			}
			if !paymentOutboxAt.Equal(at) || !orderPaidOutboxAt.Equal(at) || !orderStatusOutboxAt.Equal(at) || settlementReceipts != 1 {
				t.Fatalf("outbox/receipt mismatch: %s %s %s count=%d", paymentOutboxAt, orderPaidOutboxAt, orderStatusOutboxAt, settlementReceipts)
			}
			if err = uow.Within(f.ctx, func(ctx context.Context) error {
				fact, e := orderRepo.ReadPaidSaleBackfillFactWithin(ctx, created.OrderID)
				if e != nil {
					return e
				}
				if !fact.Paid.OccurredAt.Equal(at) {
					return fmt.Errorf("backfill changed provider time")
				}
				_, e = orderdomain.Restore(fact.Paid.Order)
				return e
			}); err != nil {
				t.Fatalf("backfill invalid: %v", err)
			}
			if tc.kind == "service_period" {
				var start time.Time
				if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT start_at FROM order_service_entitlements WHERE last_order_id=$1`, created.OrderID).Scan(&start); err != nil {
					t.Fatal(err)
				}
				if !start.Equal(at) {
					t.Fatalf("entitlement start=%s want=%s", start, at)
				}
			}
		})
	}
}
