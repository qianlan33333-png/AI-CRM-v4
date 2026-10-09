package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func invoiceReviewDurableCounts(t *testing.T, f *invoiceFixture) [6]int {
	t.Helper()
	var c [6]int
	if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_applications),(SELECT count(*) FROM invoice_operation_receipts),(SELECT count(*) FROM invoice_callback_receipts),(SELECT count(*) FROM invoice_audit_events),(SELECT count(*) FROM external_effects WHERE owner='invoice'),(SELECT count(*) FROM river_job)`).Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5]); e != nil {
		t.Fatal(e)
	}
	return c
}

type invoiceReviewAfterJobFault struct {
	inner p.QueryJobEnqueuer
}

func (j invoiceReviewAfterJobFault) EnqueueWithin(ctx context.Context, id string, at time.Time) error {
	if e := j.inner.EnqueueWithin(ctx, id, at); e != nil {
		return e
	}
	return errors.New("isolated failure after durable invoice query insertion")
}

func TestInvoiceReview20261009TitleAtomicConcurrentAndBound(t *testing.T) {
	f := newInvoiceReviewFixture(t)
	id := f.order(f.customer, f.customer, "paid", "隔离事务并发课程", "wechat_pay", time.Now())
	title := titleFixture()
	before := invoiceReviewDurableCounts(t, f)
	jobs := f.s.Jobs
	f.s.Jobs = invoiceReviewAfterJobFault{inner: jobs}
	if _, e := f.s.SubmitTitle(f.ctx, f.token, id, "review-title-afterjob", title); e == nil {
		t.Fatal("post-insertion job fault was ignored")
	}
	if invoiceReviewDurableCounts(t, f) != before {
		t.Fatal("job failure split application, receipt, audit, EER or River commit")
	}
	f.s.Jobs = jobs
	accept := f.s.Effects
	f.s.Effects = &invoiceFaultEffects{inner: accept, fail: true}
	if _, e := f.s.SubmitTitle(f.ctx, f.token, id, "review-title-accept-fault", title); e == nil {
		t.Fatal("post-insertion effect fault was ignored")
	}
	if invoiceReviewDurableCounts(t, f) != before {
		t.Fatal("effect failure split application, receipt, audit, EER or River commit")
	}
	f.s.Effects = accept
	var wg sync.WaitGroup
	type answer struct {
		receipt p.Receipt
		err     error
	}
	answers := make(chan answer, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := f.s.SubmitTitle(f.ctx, f.token, id, fmt.Sprintf("review-title-key-%04d", i), title)
			answers <- answer{r, e}
		}(i)
	}
	wg.Wait()
	close(answers)
	var applicationID string
	for got := range answers {
		if got.err != nil {
			t.Fatal("independent idempotency keys did not converge", got.err)
		}
		if applicationID != "" && got.receipt.ID != applicationID {
			t.Fatal("same transaction produced multiple applications")
		}
		applicationID = got.receipt.ID
	}
	a := f.read(applicationID)
	if a.ID != a.Snapshot.TransactionID || a.State != "queued" || a.Buyer.Amount != a.Snapshot.Amount || a.Buyer.OrderNo != a.Snapshot.OrderNo || a.Snapshot.Amount != 9900 {
		t.Fatal("application, buyer or amount no longer bound to original transaction")
	}
	after := invoiceReviewDurableCounts(t, f)
	if after[0] != 1 || after[1] != 8 || after[4] != 1 || after[5] != 2 {
		t.Fatalf("duplicate durable issue facts: counts=%v", after)
	}
	for _, changed := range []string{"same", "buyer", "amount", "order"} {
		currentTitle := title
		currentOrder := id
		if changed == "buyer" {
			currentTitle.Buyer.Name = "另一个隔离单位"
		}
		if changed == "amount" {
			currentTitle.ExpectedAmount++
		}
		if changed == "order" {
			currentOrder = f.order(f.customer, f.customer, "paid", "隔离第二订单", "wechat_pay", time.Now())
		}
		r, e := f.s.SubmitTitle(f.ctx, f.token, currentOrder, "review-title-key-0000", currentTitle)
		if changed == "same" && (e != nil || r.ID != a.ID) || changed != "same" && !errors.Is(e, p.ErrConflict) {
			t.Fatalf("idempotency payload binding failed: change=%s err=%v", changed, e)
		}
		if invoiceReviewDurableCounts(t, f) != after {
			t.Fatal("replay or conflict changed durable invoice facts")
		}
	}
	// The authenticated native bill notification and manual confirmation share
	// one Order lock, one application and one accepted issue effect.
	second := f.order(f.customer, f.customer, "paid", "隔离原生账单竞争", "wechat_pay", time.Now())
	transaction := f.billPayment(second) // Synthetic paid fact; no payment Provider action.
	notice := billNotice(transaction)
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- f.s.Notification(f.ctx, notice) }()
	go func() {
		defer wg.Done()
		_, e := f.s.SubmitTitle(f.ctx, f.token, second, "review-native-manual-key", title)
		errs <- e
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("native/manual acceptance did not converge", e)
		}
	}
	if got := f.read(transaction); got.State != "queued" || got.IssueEffect == "" {
		t.Fatal("native/manual race lost confirmed title")
	}
	if c := invoiceReviewDurableCounts(t, f); c[0] != 2 || c[2] != 1 || c[4] != 2 || c[5] != 4 {
		t.Fatalf("native/manual race duplicated application/effects/jobs: counts=%v", c)
	}
}

func TestInvoiceReview20261009NativeTitleAcceptsExactFieldBoundaries(t *testing.T) {
	f := newInvoiceReviewFixture(t)
	id := f.order(f.customer, f.customer, "paid", "有效字段边界课程", "wechat_pay", time.Now())
	r := f.create(id, "review-valid-boundaries")
	a := f.read(r.ID)
	buyer := p.Buyer{Type: "ORGANIZATION", Name: strings.Repeat("a", 256), TaxpayerID: strings.Repeat("b", 32), Address: strings.Repeat("a", 128), Telephone: strings.Repeat("a", 32), BankName: strings.Repeat("a", 128), BankAccount: strings.Repeat("a", 32), Phone: strings.Repeat("a", 32), Email: strings.Repeat("a", 190), UserApplyMessage: strings.Repeat("a", 100), Amount: a.Snapshot.Amount, OrderNo: a.Snapshot.OrderNo, BillType: "COMM_FAPIAO"}
	f.provider.officialBuyer = &buyer
	if e := f.s.Reconcile(f.ctx, a.ID); e != nil {
		t.Fatal("valid official title boundary rejected", e)
	}
	accepted := f.read(a.ID)
	if accepted.Buyer != buyer || accepted.State != "queued" {
		t.Fatal("full official title was changed or not queued")
	}
	before := invoiceReviewDurableCounts(t, f)
	f.provider.optionalError = p.ErrUnavailable
	title := p.TitleSubmission{Buyer: p.TitleInput{Type: buyer.Type, Name: buyer.Name, TaxpayerID: buyer.TaxpayerID}, ExpectedAmount: a.Snapshot.Amount}
	if replay, e := f.s.SubmitTitle(f.ctx, f.token, id, "review-valid-manual-replay", title); e != nil || replay.ID != a.ID {
		t.Fatal("accepted native title replay depended on Provider availability", e)
	}
	after := invoiceReviewDurableCounts(t, f)
	if after[1] != before[1]+1 || after[0] != before[0] || after[3] != before[3] || after[4] != before[4] || after[5] != before[5] || f.read(a.ID).Buyer != buyer {
		t.Fatal("native title replay changed material, effect, audit or query job")
	}
}

func TestInvoiceReview20261009TrustedHTTPAndBuyerAuthority(t *testing.T) {
	f := newInvoiceReviewFixture(t)
	id := f.order(f.customer, f.customer, "paid", "隔离HTTP边界课程", "wechat_pay", time.Now())
	h := &invoice.Handler{Service: f.s, Origins: []string{"https://crm.fixture"}}
	post := func(token, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://crm.fixture/api/h5/me/invoices", strings.NewReader(body))
		if token != "" {
			r.AddCookie(&http.Cookie{Name: pp.TrustedSessionCookieName, Value: token})
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	valid := fmt.Sprintf(`{"order_id":%d,"idempotency_key":"review-http-title-key","buyer":{"type":"ORGANIZATION","name":"隔离测试单位","taxpayer_id":"FIXTURE-TAX"},"expected_amount_minor":9900}`, id)
	cases := []struct {
		name, token, origin, body string
		status                    int
	}{
		{"anonymous", "", "https://crm.fixture", valid, 401},
		{"untrusted-session", "unknown-review-session", "https://crm.fixture", valid, 401},
		{"cross-origin", f.token, "https://evil.fixture", valid, 403},
		{"no-origin", f.token, "", valid, 403},
		{"individual", f.token, "https://crm.fixture", strings.Replace(valid, "ORGANIZATION", "INDIVIDUAL", 1), 400},
		{"blank-taxpayer", f.token, "https://crm.fixture", strings.Replace(valid, "FIXTURE-TAX", "  ", 1), 400},
		{"wrong-amount", f.token, "https://crm.fixture", strings.Replace(valid, "9900", "9901", 1), 409},
		{"self-reported-identity", f.token, "https://crm.fixture", strings.Replace(valid, `"order_id":`, `"customer_id":1,"verified":true,"order_id":`, 1), 400},
	}
	before := invoiceReviewDurableCounts(t, f)
	for _, tc := range cases {
		w := post(tc.token, tc.origin, tc.body)
		if w.Code != tc.status || invoiceReviewDurableCounts(t, f) != before {
			t.Fatalf("HTTP authority rejection failed: scenario=%s status=%d want=%d", tc.name, w.Code, tc.status)
		}
	}
	w := post(f.token, "https://crm.fixture", valid)
	if w.Code != 200 || strings.Contains(w.Body.String(), "FIXTURE-TAX") || strings.Contains(w.Body.String(), "隔离测试单位") {
		t.Fatal("valid receipt rejected or exposed buyer material", w.Code)
	}
	var applicationID string
	if e := f.pool.QueryRow(f.ctx, `SELECT id FROM invoice_applications WHERE order_id=$1`, id).Scan(&applicationID); e != nil {
		t.Fatal(e)
	}
	other := invoiceReviewOtherSession(t, f)
	for _, suffix := range []string{"", "/resume", "/file", "/card"} {
		method, body := http.MethodGet, ""
		if suffix == "/resume" {
			method, body = http.MethodPost, "{}"
		}
		r := httptest.NewRequest(method, "https://crm.fixture/api/h5/me/invoices/"+applicationID+suffix, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: pp.TrustedSessionCookieName, Value: other})
		r.Header.Set("Origin", "https://crm.fixture")
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != 404 {
			t.Fatal("other valid customer accessed invoice", suffix, out.Code)
		}
	}
	if f.provider.calls != 0 {
		t.Fatal("HTTP authority checks dispatched a Provider write")
	}
	// A verified fact in another App scope cannot authenticate this OA session.
	if _, e := f.pool.Exec(f.ctx, `UPDATE customer_identities SET scope_key='wechat-app:other-fixture' WHERE id=$1`, f.identity); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Read(f.ctx, f.token, applicationID); !errors.Is(e, pp.ErrSessionRequired) {
		t.Fatal("wrong App scope authenticated invoice owner", e)
	}
}

func TestInvoiceReview20261009HistoricalUnknownPayerRemainsIneligible(t *testing.T) {
	f := newInvoiceReviewFixture(t)
	id := f.order(f.customer, f.customer, "paid", "隔离历史未知付款人", "wechat_pay", time.Now())
	if _, e := f.pool.Exec(f.ctx, `UPDATE orders SET record_origin='history',effect_eligible=false,source_row_digest=$2,payer_customer_id=NULL WHERE id=$1`, id, make([]byte, 32)); e != nil {
		t.Fatal(e)
	}
	before := invoiceReviewDurableCounts(t, f)
	for _, manual := range []bool{false, true} {
		var e error
		if manual {
			_, e = f.s.SubmitTitle(f.ctx, f.token, id, "review-history-title-key", titleFixture())
		} else {
			_, e = f.s.Create(f.ctx, f.token, id, "review-history-create-key")
		}
		if !errors.Is(e, p.ErrIneligible) || invoiceReviewDurableCounts(t, f) != before {
			t.Fatal("history/unknown payer accepted or committed invoice", e)
		}
	}
	page, e := f.s.List(f.ctx, f.token, p.OrdersQuery{})
	if e != nil || len(page.Items) != 1 || page.Items[0].InvoiceEligible {
		t.Fatal("historical beneficiary read lost or promoted to eligible", e)
	}
}
