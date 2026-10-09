package main

import (
	"errors"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"strings"
	"sync"
	"testing"
	"time"
)

func failedInvoiceFixture(t *testing.T) (*invoiceFixture, p.Application) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "失败开票恢复", "wechat_pay", time.Now())
	f.billPayment(id)
	r, e := f.s.SubmitTitle(f.ctx, f.token, id, "recovery-confirmed-key", titleFixture())
	if e != nil {
		t.Fatal(e)
	}
	a := f.read(r.ID)
	f.run(a.IssueEffect)
	f.provider.status = "ISSUE_FAILED"
	f.provider.card = "INSERT_ACCEPTED"
	if e = f.s.Reconcile(f.ctx, a.ID); e != nil {
		t.Fatal(e)
	}
	a = f.read(a.ID)
	if a.State != "failed" || a.CardState != "pending" {
		t.Fatal("failed ticket/card projection", a.State, a.CardState)
	}
	f.s.Config.Taxes["course"] = p.Tax{Code: "3040203000000000000", Rate: 100, GoodsName: "服务费"}
	return f, a
}
func TestInvoicePostgreSQLFailedRecoveryCurrentFinanceAndAtomic(t *testing.T) {
	f, prior := failedInvoiceFixture(t)
	before := invoiceBillCounts(t, f)
	fault := &invoiceFaultEffects{inner: f.s.Effects, fail: true}
	f.s.Effects = fault
	if _, e := f.s.RecoverFailed(f.ctx, prior.ID, prior.InvoiceID, false); e == nil {
		t.Fatal("injected recovery rollback absent")
	}
	a := f.read(prior.ID)
	if invoiceBillCounts(t, f) != before || a.InvoiceID != prior.InvoiceID || a.Snapshot.Lines[0].TaxCode != prior.Snapshot.Lines[0].TaxCode {
		t.Fatal("partial recovery committed")
	}
	fault.fail = false
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.s.RecoverFailed(f.ctx, prior.ID, prior.InvoiceID, false); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	a = f.read(prior.ID)
	if a.InvoiceID == prior.InvoiceID || a.IssueAttempt != 1 || a.Snapshot.Lines[0].TaxCode != "3040203000000000000" || a.Snapshot.Lines[0].TaxRate != 100 || a.Snapshot.Lines[0].TotalAmount != 9900 || a.Buyer.Name != prior.Buyer.Name {
		t.Fatal("recovery did not preserve title/order and apply finance")
	}
	after := invoiceBillCounts(t, f)
	if after[3] != before[3]+1 {
		t.Fatal("concurrent recovery duplicated effect")
	}
	var oldCode, newCode string
	if e := f.pool.QueryRow(f.ctx, `SELECT material->'before'->'lines'->0->>'tax_code',material->'after'->'lines'->0->>'tax_code' FROM invoice_audit_events WHERE application_id=$1 AND event_type='failed_recovery'`, a.ID).Scan(&oldCode, &newCode); e != nil || oldCode != prior.Snapshot.Lines[0].TaxCode || newCode != "3040203000000000000" {
		t.Fatal("prior attempt material not preserved", e)
	}
	// A stale native recovery job cannot mint another invoice after this acceptance.
	if e := f.s.RecoverNativeTitle(f.ctx, a.ID, prior.InvoiceID); e != nil && !errors.Is(e, p.ErrPending) {
		t.Fatal(e)
	}
	if f.read(a.ID).InvoiceID != a.InvoiceID || invoiceBillCounts(t, f)[3] != after[3] {
		t.Fatal("stale job repeated recovery")
	}
}
func TestInvoicePostgreSQLFailedRecoveryRejectsUnsafeProof(t *testing.T) {
	for _, kind := range []string{"pending", "issued", "refund", "seller", "amount", "unknown", "exhausted", "individual"} {
		t.Run(kind, func(t *testing.T) {
			f, a := failedInvoiceFixture(t)
			before := invoiceBillCounts(t, f)
			switch kind {
			case "pending":
				f.provider.status = "ISSUE_ACCEPTED"
			case "issued":
				f.provider.status = "ISSUED"
			case "refund":
				f.pendingRefund(a.OrderID, "requested")
			case "seller":
				f.s.Config.SellerTaxID = "changed"
			case "amount":
				_, e := f.pool.Exec(f.ctx, `UPDATE orders SET amount_minor=10000 WHERE id=$1`, a.OrderID)
				if e != nil {
					t.Fatal(e)
				}
			case "unknown":
				_, e := f.pool.Exec(f.ctx, `UPDATE external_effects SET state='outcome_unknown' WHERE id=$1`, strings.TrimPrefix(a.IssueEffect, "eer_"))
				if e != nil {
					t.Fatal(e)
				}
			case "exhausted":
				_, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET issue_attempt=4 WHERE id=$1`, a.ID)
				if e != nil {
					t.Fatal(e)
				}
			case "individual":
				_, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET buyer=jsonb_set(buyer,'{type}','"INDIVIDUAL"') WHERE id=$1`, a.ID)
				if e != nil {
					t.Fatal(e)
				}
			}
			if _, e := f.s.RecoverFailed(f.ctx, a.ID, a.InvoiceID, false); e == nil {
				t.Fatal("unsafe recovery accepted")
			}
			if invoiceBillCounts(t, f) != before || f.read(a.ID).InvoiceID != a.InvoiceID {
				t.Fatal("rejected proof changed application")
			}
		})
	}
}
func TestInvoicePostgreSQLFailedTestCanonicalRecoveryPreservesFacts(t *testing.T) {
	f, a := failedInvoiceFixture(t)
	notice := billNotice(a.ID)
	notice.EventType = "FAPIAO.ISSUED"
	if e := f.s.Notification(f.ctx, notice); e != nil {
		t.Fatal(e)
	}
	oldID := "inv_" + strings.Repeat("a", 28)
	if _, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET id=$2 WHERE id=$1`, a.ID, oldID); e != nil {
		t.Fatal(e)
	}
	before := invoiceBillCounts(t, f)
	f.provider.unsubmittedError = p.ErrPending
	if _, e := f.s.RecoverFailed(f.ctx, oldID, a.InvoiceID, true); !errors.Is(e, p.ErrPending) {
		t.Fatal("unknown canonical outcome accepted", e)
	}
	if invoiceBillCounts(t, f) != before {
		t.Fatal("unknown canonical mutated history")
	}
	f.provider.unsubmittedError = nil
	fault := &invoiceFaultJobs{inner: f.s.Jobs, fail: true}
	f.s.Jobs = fault
	if _, e := f.s.RecoverFailed(f.ctx, oldID, a.InvoiceID, true); e == nil {
		t.Fatal("canonical recovery rollback absent")
	}
	if f.read(oldID).InvoiceID != a.InvoiceID || invoiceBillCounts(t, f) != before {
		t.Fatal("canonical rollback lost receipts")
	}
	fault.fail = false
	r, e := f.s.RecoverFailed(f.ctx, oldID, a.InvoiceID, true)
	if e != nil {
		t.Fatal(e)
	}
	if r.ID != a.ID || r.State != "queued" {
		t.Fatal("not unified by transaction")
	}
	if _, e = f.s.Read(f.ctx, f.token, oldID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("old lookup compatibility introduced")
	}
	var receipts, callbacks, audit int
	if e = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_operation_receipts WHERE application_id=$1),(SELECT count(*) FROM invoice_callback_receipts WHERE application_id=$1),(SELECT count(*) FROM invoice_audit_events WHERE application_id=$1)`, a.ID).Scan(&receipts, &callbacks, &audit); e != nil || receipts != 1 || callbacks != 1 || audit <= before[2] {
		t.Fatal("canonical history lost", e, receipts, callbacks, audit)
	}
	f.s.Jobs = fault.inner
	if e = f.s.Notification(f.ctx, billNotice(a.ID)); e != nil {
		t.Fatal("bill callback still conflicted", e)
	}
	if invoiceBillCounts(t, f)[3] != before[3]+1 {
		t.Fatal("bill duplicated invoice")
	}
}
func TestInvoicePostgreSQLNativeRecoveryNoticeReplay(t *testing.T) {
	f, a := failedInvoiceFixture(t)
	n := billNotice(a.ID)
	if e := f.s.Notification(f.ctx, n); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Notification(f.ctx, n); e != nil {
		t.Fatal(e)
	}
	var jobs int
	var failedID string
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*),min(args->>'failed_invoice_id') FROM river_job WHERE kind='invoice.provider-query.v1' AND args->>'failed_invoice_id' IS NOT NULL`).Scan(&jobs, &failedID); e != nil || jobs != 1 || failedID != a.InvoiceID {
		t.Fatal("native recovery not attempt bound", e, jobs)
	}
	if e := f.s.RecoverNativeTitle(f.ctx, a.ID, failedID); e != nil && !errors.Is(e, p.ErrPending) {
		t.Fatal(e)
	}
	current := f.read(a.ID)
	if current.State != "failed" || current.IssueAttempt != 1 {
		t.Fatal("test provider failure not recorded")
	}
	if e := f.s.Notification(f.ctx, n); e != nil {
		t.Fatal(e)
	}
	if e := f.s.RecoverNativeTitle(f.ctx, a.ID, failedID); e != nil && !errors.Is(e, p.ErrPending) {
		t.Fatal(e)
	}
	if f.read(a.ID).InvoiceID != current.InvoiceID || f.read(a.ID).IssueAttempt != 1 {
		t.Fatal("notice/job replay minted second ticket")
	}
	// No effect executes from this synthetic notification test.
	var count int
	f.pool.QueryRow(f.ctx, `SELECT count(*) FROM external_effects WHERE owner=$1 AND kind=$2`, effects.OwnerInvoice, effects.KindInvoiceIssue).Scan(&count)
	if count != 2 {
		t.Fatal("native recovery effect count", count)
	}
}
