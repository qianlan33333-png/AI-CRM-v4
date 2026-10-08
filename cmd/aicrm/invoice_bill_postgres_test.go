package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func (f *invoiceFixture) billPayment(orderID int64) string {
	f.t.Helper()
	var transaction string
	e := f.pool.QueryRow(f.ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at,paid_confirmed_at,provider_transaction_reference) SELECT id,'wechat_pay','h5_official_account',merchant_order_no,$2,payer_customer_id,beneficiary_customer_id,amount_minor,currency,'paid',1,created_at,updated_at,created_at,provider_transaction_no FROM orders WHERE id=$1 RETURNING provider_transaction_reference`, orderID, f.identity).Scan(&transaction)
	if e != nil {
		f.t.Fatal(e)
	}
	return transaction
}
func billNotice(transaction string) p.Notice {
	return p.Notice{ID: "bill-event-" + transaction, EventType: "FAPIAO.USER_APPLIED", ApplicationID: transaction, SubMerchantID: "merchant-fixture", BodyDigest: string(effects.Hash("fixture.bill", transaction))}
}
func invoiceBillCounts(t *testing.T, f *invoiceFixture) [4]int {
	t.Helper()
	var c [4]int
	if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_applications),(SELECT count(*) FROM invoice_callback_receipts),(SELECT count(*) FROM invoice_audit_events),(SELECT count(*) FROM external_effects WHERE owner='invoice')`).Scan(&c[0], &c[1], &c[2], &c[3]); e != nil {
		t.Fatal(e)
	}
	return c
}

func TestInvoicePostgreSQLBillEntryUnifiedAndAtomic(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "账单开票", "wechat_pay", time.Now())
	transaction := f.billPayment(id)
	notice := billNotice(transaction)
	// The actual shared job adapter inserts before the injected failure. All
	// application/audit/callback/job rows must roll back together.
	jobs := &invoiceFaultJobs{inner: f.s.Jobs, fail: true}
	f.s.Jobs = jobs
	if e := f.s.Notification(f.ctx, notice); e == nil {
		t.Fatal("job fault did not roll back")
	}
	if c := invoiceBillCounts(t, f); c != [4]int{} {
		t.Fatal("partial bill acceptance", c)
	}
	var jobCount int
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM river_job WHERE kind='invoice.provider-query.v1'`).Scan(&jobCount); e != nil || jobCount != 0 {
		t.Fatal("orphan query job", e, jobCount)
	}
	jobs.fail = false
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- f.s.Notification(f.ctx, notice) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	a := f.read(transaction)
	if a.ID != transaction || a.Snapshot.TransactionID != transaction || a.OrderID != id || a.CustomerID != f.customer || a.IdentityID != f.identity || a.IssueEffect != "" {
		t.Fatal("wrong application or premature issue", a.ID, a.State)
	}
	if c := invoiceBillCounts(t, f); c[0] != 1 || c[1] != 1 || c[2] != 1 || c[3] != 0 {
		t.Fatal("duplicate bill records", c)
	}
	// Manual confirmation uses the same application and first title wins.
	result, e := f.s.SubmitTitle(f.ctx, f.token, id, "unified-title-00001", titleFixture())
	if e != nil || result.ID != transaction {
		t.Fatal("manual/native split", e, result.ID)
	}
	if e = f.s.Reconcile(f.ctx, transaction); !errors.Is(e, p.ErrPending) {
		t.Fatal(e)
	}
	a = f.read(transaction)
	if a.Buyer.Name != titleFixture().Buyer.Name || a.State != "queued" {
		t.Fatal("title overwritten")
	}
	if c := invoiceBillCounts(t, f); c[0] != 1 || c[3] != 1 {
		t.Fatal("second application/effect", c)
	}
	// A previous long backoff must wake after acceptance and after dispatch.
	_, e = f.pool.Exec(f.ctx, `UPDATE river_job SET state='retryable',scheduled_at=now()+interval '1 hour' WHERE kind='invoice.provider-query.v1'`)
	if e != nil {
		t.Fatal(e)
	}
	f.run(a.IssueEffect)
	var state string
	var at time.Time
	if e = f.pool.QueryRow(f.ctx, `SELECT state,scheduled_at FROM river_job WHERE kind='invoice.provider-query.v1' ORDER BY id DESC LIMIT 1`).Scan(&state, &at); e != nil || at.After(time.Now().Add(5*time.Second)) {
		t.Fatal("query still in backoff", state, at, e)
	}
	f.provider.status = "ISSUED"
	f.provider.card = "INSERTED"
	if e = f.s.Reconcile(f.ctx, transaction); e != nil {
		t.Fatal(e)
	}
	history, e := f.s.History(f.ctx, f.token, p.HistoryQuery{})
	if e != nil || len(history.Items) != 1 || history.Items[0].ID != transaction || history.Items[0].State != "issued" || history.Items[0].CardState != "INSERTED" {
		t.Fatal("native history", e)
	}
	h := &invoice.Handler{Service: f.s}
	req := httptest.NewRequest(http.MethodGet, "https://crm.fixture/api/h5/me/invoices/"+transaction+"/file?disposition=inline", nil)
	req.AddCookie(&http.Cookie{Name: pp.TrustedSessionCookieName, Value: f.token})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 || out.Header().Get("Content-Type") != "application/pdf" {
		t.Fatal("transaction PDF route", out.Code)
	}
	if e = f.refund(id, 100); e != nil {
		t.Fatal(e)
	}
	a = f.read(transaction)
	if a.State != "reversing" || a.ReverseEffect == "" {
		t.Fatal("native refund did not reverse")
	}
	f.run(a.ReverseEffect)
	f.provider.status = "REVERSED"
	f.provider.card = "DISCARDED"
	if e = f.s.Reconcile(f.ctx, transaction); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Notification(f.ctx, notice); e != nil {
		t.Fatal(e)
	}
	if a = f.read(transaction); a.State != "reversed" || a.CardState != "DISCARDED" {
		t.Fatal("replay downgraded terminal state")
	}
}

func TestInvoicePostgreSQLBillEntryAuthorityAndRecovery(t *testing.T) {
	t.Run("unknown-and-non-title", func(t *testing.T) {
		f := newInvoiceFixture(t)
		n := billNotice("4200000444201910177461284488")
		if e := f.s.Notification(f.ctx, n); !errors.Is(e, p.ErrPending) {
			t.Fatal("unknown payment not retryable", e)
		}
		n.EventType = "FAPIAO.ISSUED"
		if e := f.s.Notification(f.ctx, n); e != nil {
			t.Fatal(e)
		}
		n.EventType = "FAPIAO.USER_APPLIED"
		n.SubMerchantID = "foreign"
		if e := f.s.Notification(f.ctx, n); e != nil {
			t.Fatal(e)
		}
		if c := invoiceBillCounts(t, f); c != [4]int{} {
			t.Fatal("unknown notice created business state", c)
		}
	})
	for _, bad := range []string{"identity", "amount", "unconfirmed", "disabled", "refund", "duplicate-payment"} {
		t.Run(bad, func(t *testing.T) {
			f := newInvoiceFixture(t)
			id := f.order(f.customer, f.customer, "paid", "拒绝路径", "wechat_pay", time.Now())
			ref := f.billPayment(id)
			switch bad {
			case "identity":
				_, e := f.pool.Exec(f.ctx, `UPDATE customer_identities SET assurance='declared',verified_at=NULL WHERE id=$1`, f.identity)
				if e != nil {
					t.Fatal(e)
				}
			case "amount":
				_, e := f.pool.Exec(f.ctx, `UPDATE payments SET amount_minor=amount_minor+1 WHERE order_id=$1`, id)
				if e != nil {
					t.Fatal(e)
				}
			case "unconfirmed":
				_, e := f.pool.Exec(f.ctx, `UPDATE payments SET paid_confirmed_at=NULL WHERE order_id=$1`, id)
				if e != nil {
					t.Fatal(e)
				}
			case "disabled":
				f.s.Config.Enabled = false
				f.s.Provider = nil
			case "refund":
				f.pendingRefund(id, "requested")
			case "duplicate-payment":
				other := f.order(f.customer, f.customer, "paid", "重复交易", "wechat_pay", time.Now())
				f.billPayment(other)
				_, e := f.pool.Exec(f.ctx, `UPDATE payments SET provider_transaction_reference=$2 WHERE order_id=$1`, other, ref)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e := f.s.Notification(f.ctx, billNotice(ref)); e == nil {
				t.Fatal("unsafe bill accepted")
			}
			if c := invoiceBillCounts(t, f); c != [4]int{} {
				t.Fatal("rejection wrote invoice state", c)
			}
		})
	}
	t.Run("organization-then-refund-before-issue", func(t *testing.T) {
		f := newInvoiceFixture(t)
		id := f.order(f.customer, f.customer, "paid", "单位开票", "wechat_pay", time.Now())
		ref := f.billPayment(id)
		if e := f.s.Notification(f.ctx, billNotice(ref)); e != nil {
			t.Fatal(e)
		}
		f.provider.titleType = "INDIVIDUAL"
		if e := f.s.Reconcile(f.ctx, ref); !errors.Is(e, p.ErrInvalid) {
			t.Fatal("personal bill title accepted", e)
		}
		if c := invoiceBillCounts(t, f); c[3] != 0 {
			t.Fatal("personal title issued")
		}
		f.provider.titleType = "ORGANIZATION"
		if e := f.s.Reconcile(f.ctx, ref); e != nil {
			t.Fatal(e)
		}
		a := f.read(ref)
		if a.Buyer.Type != "ORGANIZATION" || a.State != "queued" {
			t.Fatal("official organization title not accepted")
		}
		if e := f.refund(id, 9900); e != nil {
			t.Fatal(e)
		}
		f.run(a.IssueEffect)
		if f.provider.calls != 0 || f.read(ref).State != "cancelled" {
			t.Fatal("refunded bill issued")
		}
	})
	t.Run("merged-payer", func(t *testing.T) {
		f := newInvoiceFixture(t)
		id := f.order(f.customer, f.customer, "paid", "归属合并", "wechat_pay", time.Now())
		ref := f.billPayment(id)
		_, e := f.pool.Exec(f.ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.customer, f.other)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.Notification(f.ctx, billNotice(ref)); e != nil {
			t.Fatal(e)
		}
		history, e := f.s.History(f.ctx, f.token, p.HistoryQuery{})
		if e != nil || len(history.Items) != 1 || history.Items[0].ID != ref {
			t.Fatal("merged bill ownership", e)
		}
	})
}

func TestInvoicePostgreSQLBillAndManualConcurrentFirstTitle(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "两入口竞争", "wechat_pay", time.Now())
	ref := f.billPayment(id)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				errs <- f.s.Notification(f.ctx, billNotice(ref))
			} else {
				_, e := f.s.SubmitTitle(f.ctx, f.token, id, fmt.Sprintf("bill-manual-%06d", i), titleFixture())
				errs <- e
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	a := f.read(ref)
	if a.State != "queued" || a.Buyer.Name != titleFixture().Buyer.Name {
		t.Fatal("missing winning title")
	}
	if c := invoiceBillCounts(t, f); c[0] != 1 || c[1] != 1 || c[3] != 1 {
		t.Fatal("parallel duplicate", c)
	}
	// Authenticated provider title read finishes after manual confirmation.
	read := &blockedOfficialTitle{invoiceFixtureProvider: f.provider, entered: make(chan struct{}), release: make(chan struct{})}
	fresh := f.order(f.customer, f.customer, "paid", "抬头读取竞争", "wechat_pay", time.Now())
	freshRef := f.billPayment(fresh)
	if e := f.s.Notification(f.ctx, billNotice(freshRef)); e != nil {
		t.Fatal(e)
	}
	f.s.Provider = read
	finished := make(chan error, 1)
	go func() { finished <- f.s.Reconcile(context.Background(), freshRef) }()
	<-read.entered
	if _, e := f.s.SubmitTitle(f.ctx, f.token, fresh, "manual-title-race-0001", titleFixture()); e != nil {
		t.Fatal(e)
	}
	close(read.release)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if a = f.read(freshRef); a.Buyer.Name != titleFixture().Buyer.Name {
		t.Fatal("stale official read overwrote title")
	}
}
