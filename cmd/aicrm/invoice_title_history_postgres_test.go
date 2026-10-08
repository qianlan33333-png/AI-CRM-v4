package main

import (
	"context"
	"encoding/json"
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

func titleFixture() p.TitleSubmission {
	return p.TitleSubmission{Buyer: p.TitleInput{Type: "ORGANIZATION", Name: "隔离测试单位", TaxpayerID: "FIXTURE-TAX"}, ExpectedAmount: 9900}
}

func TestInvoicePostgreSQLSelfTitleAtomicAndReplay(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "自填抬头课程", "wechat_pay", time.Now())
	title := titleFixture()
	// Effect acceptance has already inserted its rows before fault injection.
	fault := &invoiceFaultEffects{inner: f.s.Effects, fail: true}
	f.s.Effects = fault
	if _, e := f.s.SubmitTitle(f.ctx, f.token, id, "manual-title-key-0001", title); e == nil {
		t.Fatal("missing rollback failure")
	}
	var apps, receipts, audit, effects int
	if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_applications),(SELECT count(*) FROM invoice_operation_receipts),(SELECT count(*) FROM invoice_audit_events),(SELECT count(*) FROM external_effects WHERE owner='invoice')`).Scan(&apps, &receipts, &audit, &effects); e != nil {
		t.Fatal(e)
	}
	if apps+receipts+audit+effects != 0 {
		t.Fatalf("partial commit %d %d %d %d", apps, receipts, audit, effects)
	}
	fault.fail = false
	bad := title
	bad.ExpectedAmount++
	if _, e := f.s.SubmitTitle(f.ctx, f.token, id, "manual-amount-key-01", bad); !errors.Is(e, p.ErrConflict) {
		t.Fatal("client amount trusted", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, e := f.s.SubmitTitle(f.ctx, f.token, id, fmt.Sprintf("manual-concurrent-%04d", i), title)
			errs <- e
			ids <- a.ID
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate application")
		}
	}
	a, e := f.s.Read(f.ctx, f.token, first)
	if e != nil || a.State != "queued" || a.Buyer.Name != title.Buyer.Name || a.Buyer.Amount != 9900 || a.IssueEffect == "" {
		t.Fatal("missing frozen title/effect", e)
	}
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM external_effects WHERE owner='invoice'`).Scan(&effects); e != nil || effects != 1 {
		t.Fatal("duplicate effect", effects, e)
	}
	changed := title
	changed.Buyer.Name = "另一抬头"
	if _, e = f.s.SubmitTitle(f.ctx, f.token, id, "manual-concurrent-0000", changed); !errors.Is(e, p.ErrConflict) {
		t.Fatal("accepted title overwritten", e)
	}
	if e = f.s.Reconcile(f.ctx, a.ID); !errors.Is(e, p.ErrPending) {
		t.Fatal("queued query", e)
	}
	current, _ := f.s.Read(f.ctx, f.token, a.ID)
	if current.InvoiceID != a.InvoiceID {
		t.Fatal("unknown result changed invoice ID")
	}
	// An accepted title remains frozen when dispatch stops before any provider
	// call. Recovery reuses it instead of reading an unrelated official title.
	f.provider.stoppedCode = "invoice_payment_check_unavailable"
	f.run(a.IssueEffect)
	stopped := f.read(a.ID)
	if stopped.State != "awaiting_title" {
		t.Fatal("missing stopped recovery", stopped.State)
	}
	if _, e = f.s.SubmitTitle(f.ctx, f.token, id, "manual-concurrent-0000", changed); !errors.Is(e, p.ErrConflict) {
		t.Fatal("stopped title overwritten", e)
	}
	f.provider.stoppedCode = ""
	if e = f.s.Reconcile(f.ctx, a.ID); e != nil {
		t.Fatal(e)
	}
	recovered := f.read(a.ID)
	if recovered.Buyer.Name != title.Buyer.Name || recovered.InvoiceID != a.InvoiceID || recovered.IssueEffect == a.IssueEffect {
		t.Fatal("stopped recovery lost original confirmed title/ID")
	}

	// The self-hosted HTTP contract refuses partial, personal and cross-site
	// writes; a complete confirmed payload returns no official launch link.
	handler := &invoice.Handler{Service: f.s, Origins: []string{"https://crm.fixture"}}
	post := func(body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "https://crm.fixture/api/h5/me/invoices", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: pp.TrustedSessionCookieName, Value: f.token})
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	payload := fmt.Sprintf(`{"order_id":%d,"idempotency_key":"http-manual-title-01","buyer":{"type":"ORGANIZATION","name":"隔离测试单位","taxpayer_id":"FIXTURE-TAX"},"expected_amount_minor":9900}`, id)
	if w := post(payload, "https://other.fixture"); w.Code != 403 {
		t.Fatal("cross-site write", w.Code)
	}
	if w := post(strings.Replace(payload, `,"expected_amount_minor":9900`, "", 1), "https://crm.fixture"); w.Code != 400 {
		t.Fatal("amount confirmation missing", w.Code)
	}
	if w := post(strings.Replace(payload, `"ORGANIZATION"`, `"INDIVIDUAL"`, 1), "https://crm.fixture"); w.Code != 400 {
		t.Fatal("personal title permitted", w.Code)
	}
	if w := post(strings.Replace(payload, `"taxpayer_id":"FIXTURE-TAX"`, `"taxpayer_id":"FIXTURE-TAX","phone":"untrusted"`, 1), "https://crm.fixture"); w.Code != 400 {
		t.Fatal("unknown buyer field", w.Code)
	}
	if w := post(payload, "https://crm.fixture"); w.Code != 200 || strings.Contains(w.Body.String(), "pages/title") {
		t.Fatal("manual HTTP result", w.Code)
	}

	gift := f.order(f.other, f.customer, "paid", "受益人课程", "wechat_pay", time.Now())
	if _, e = f.s.SubmitTitle(f.ctx, f.token, gift, "manual-gift-key-0001", title); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("beneficiary could issue", e)
	}
	refund := f.order(f.customer, f.customer, "paid", "退款课程", "wechat_pay", time.Now())
	f.pendingRefund(refund, "requested")
	if _, e = f.s.SubmitTitle(f.ctx, f.token, refund, "manual-refund-key-01", title); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("refund exposure allowed", e)
	}
	for _, bad := range []p.TitleSubmission{{Buyer: p.TitleInput{Type: "ORGANIZATION", Name: "缺税号"}, ExpectedAmount: 9900}, {Buyer: p.TitleInput{Type: "ORGANIZATION", Name: strings.Repeat("x", 257), TaxpayerID: "TAX"}, ExpectedAmount: 9900}, {Buyer: p.TitleInput{Type: "INDIVIDUAL", Name: "个人"}, ExpectedAmount: 9900}} {
		if _, e = f.s.SubmitTitle(f.ctx, f.token, id, "manual-invalid-0001", bad); !errors.Is(e, p.ErrInvalid) {
			t.Fatal("invalid title", e)
		}
	}
}

type blockedLegacyTitle struct {
	*invoiceFixtureProvider
	entered, release chan struct{}
}

func (b *blockedLegacyTitle) ReadTitle(ctx context.Context, a p.Application) (p.Buyer, error) {
	close(b.entered)
	<-b.release
	return b.invoiceFixtureProvider.ReadTitle(ctx, a)
}

func TestInvoicePostgreSQLLegacyTitleRaceAndHistory(t *testing.T) {
	f := newInvoiceFixture(t)
	orderID := f.order(f.customer, f.customer, "paid", "旧申请课程", "wechat_pay", time.Now())
	legacy, e := f.s.Create(f.ctx, f.token, orderID, "legacy-title-key-001")
	if e != nil {
		t.Fatal(e)
	}
	f.provider.titleType = "INDIVIDUAL"
	if e = f.s.Reconcile(f.ctx, legacy.ID); !errors.Is(e, p.ErrInvalid) {
		t.Fatal("legacy personal title accepted", e)
	}
	if pending := f.read(legacy.ID); pending.State != "awaiting_title" || pending.IssueEffect != "" {
		t.Fatal("personal title accepted an effect")
	}
	f.provider.titleType = ""

	block := &blockedLegacyTitle{f.provider, make(chan struct{}), make(chan struct{})}
	f.s.Provider = block
	done := make(chan error, 1)
	go func() { done <- f.s.Reconcile(f.ctx, legacy.ID) }()
	select {
	case <-block.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy read did not start")
	}
	a, e := f.s.SubmitTitle(f.ctx, f.token, orderID, "manual-old-app-00001", titleFixture())
	close(block.release)
	if e != nil || a.ID != legacy.ID || a.State != "queued" {
		t.Fatal("legacy ID not reused", e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	current, _ := f.s.Read(f.ctx, f.token, a.ID)
	if current.Buyer.Name != "隔离测试单位" {
		t.Fatal("late official title overwrote confirmed title")
	}
	own2 := f.order(f.customer, f.customer, "paid", "第二课程", "wechat_pay", time.Now())
	second, e := f.s.SubmitTitle(f.ctx, f.token, own2, "manual-company-0001", titleFixture())
	if e != nil {
		t.Fatal(e)
	}
	hiddenOrder := f.order(f.other, f.other, "paid", "他人课程", "wechat_pay", time.Now())
	_, e = f.pool.Exec(f.ctx, `INSERT INTO invoice_applications(id,order_id,customer_id,identity_id,invoice_id,state,snapshot) SELECT 'inv_bbbbbbbbbbbbbbbbbbbbbbbbbbbb',$1,$2,identity_id,'inv_bbbbbbbbbbbbbbbbbbbbbbbbbbbb','awaiting_title',snapshot FROM invoice_applications WHERE id=$3`, hiddenOrder, f.other, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.pool.Exec(f.ctx, `UPDATE invoice_applications SET created_at='2026-10-08T00:00:00Z'`)
	if e != nil {
		t.Fatal(e)
	}
	page, e := f.s.History(f.ctx, f.token, p.HistoryQuery{Limit: 1})
	if e != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("history page", e)
	}
	next, e := f.s.History(f.ctx, f.token, p.HistoryQuery{Limit: 1, Cursor: page.NextCursor})
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.NextCursor != "" {
		t.Fatal("cursor tie/order isolation", e)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "FIXTURE-TAX") || strings.Contains(string(raw), "隔离测试单位") {
		t.Fatal("history leaks buyer")
	}
	if _, e = f.s.Read(f.ctx, f.token, "inv_bbbbbbbbbbbbbbbbbbbbbbbbbbbb"); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("other invoice readable", e)
	}
	handler := &invoice.Handler{Service: f.s, Origins: []string{"https://crm.fixture"}}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://crm.fixture"+path, nil)
		req.AddCookie(&http.Cookie{Name: pp.TrustedSessionCookieName, Value: f.token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	if w := get("/api/h5/me/invoices?limit=1"); w.Code != 200 {
		t.Fatal("history HTTP", w.Code)
	}
	if w := get("/api/h5/me/invoices/" + second.ID + "/file?disposition=inline"); w.Code != 409 {
		t.Fatal("pending PDF", w.Code)
	}
	f.provider.status = "ISSUED"
	f.provider.card = "INSERTED"
	if e = f.s.Reconcile(f.ctx, second.ID); e != nil {
		t.Fatal(e)
	}
	if w := get("/api/h5/me/invoices/" + second.ID + "/file?disposition=inline"); w.Code != 200 || w.Header().Get("Content-Disposition") != `inline; filename="invoice.pdf"` || w.Header().Get("Content-Type") != "application/pdf" {
		t.Fatal("PDF preview", w.Code, w.Header())
	}
	if w := get("/api/h5/me/invoices?limit=0"); w.Code != 400 {
		t.Fatal("invalid limit", w.Code)
	}
	if w := get("/api/h5/me/invoices?customer_id=1"); w.Code != 400 {
		t.Fatal("HTTP identity override", w.Code)
	}
	// Only canonical identity merge expands history ownership.
	_, e = f.pool.Exec(f.ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.customer, f.other)
	if e != nil {
		t.Fatal(e)
	}
	merged, e := f.s.History(f.ctx, f.token, p.HistoryQuery{})
	if e != nil || len(merged.Items) != 3 {
		t.Fatal("merged history", e, len(merged.Items))
	}
	f.pool.Exec(f.ctx, `UPDATE payment_sessions SET expires_at=now() WHERE payer_identity_id=$1`, f.identity)
	if w := get("/api/h5/me/invoices"); w.Code != 401 {
		t.Fatal("expired history session", w.Code)
	}
}
