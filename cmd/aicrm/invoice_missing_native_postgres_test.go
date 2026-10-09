package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type missingNativeProvider struct {
	p.Provider
	proof      p.UnsubmittedVerifier
	afterTitle func()
}

func (m missingNativeProvider) ReadTitle(ctx context.Context, a p.Application) (p.Buyer, error) {
	if _, e := pg.RequireTransaction(ctx); e == nil {
		return p.Buyer{}, errors.New("title read held transaction")
	}
	b, e := m.Provider.ReadTitle(ctx, a)
	if m.afterTitle != nil {
		m.afterTitle()
	}
	return b, e
}
func (m missingNativeProvider) VerifyUnsubmitted(ctx context.Context, a p.Application) error {
	if _, e := pg.RequireTransaction(ctx); e == nil {
		return errors.New("application query held transaction")
	}
	return m.proof.VerifyUnsubmitted(ctx, a)
}

func missingNativeFixture(t *testing.T) (*invoiceFixture, int64, string) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "漏通知账单", "wechat_pay", time.Now())
	transaction := f.billPayment(id)
	var no string
	if e := f.pool.QueryRow(f.ctx, `SELECT merchant_order_no FROM orders WHERE id=$1`, id).Scan(&no); e != nil {
		t.Fatal(e)
	}
	f.provider.officialBuyer = &p.Buyer{Type: "ORGANIZATION", Name: "官方单位抬头", TaxpayerID: "FIXTURE-TAX", Amount: 9900, OrderNo: no, Address: "official address", BankName: "official bank", BankAccount: "original account"}
	f.s.Provider = missingNativeProvider{Provider: f.provider, proof: f.provider}
	return f, id, transaction
}

func TestInvoicePostgreSQLMissingNativeRecoveryAtomicConcurrent(t *testing.T) {
	f, _, transaction := missingNativeFixture(t)
	fault := &invoiceFaultEffects{inner: f.s.Effects, fail: true}
	f.s.Effects = fault
	if _, e := f.s.RecoverFailed(f.ctx, transaction, "", true); e == nil {
		t.Fatal("effect acceptance failure ignored")
	}
	if got := invoiceBillCounts(t, f); got != [4]int{} {
		t.Fatal("partial missing application committed", got)
	}
	var jobs int
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM river_job WHERE kind='invoice.provider-query.v1'`).Scan(&jobs); e != nil || jobs != 0 {
		t.Fatal("partial query job committed", jobs, e)
	}
	fault.fail = false
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.s.RecoverFailed(f.ctx, transaction, "", true); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	a := f.read(transaction)
	counts := invoiceBillCounts(t, f)
	if counts[0] != 1 || counts[1] != 0 || counts[3] != 1 || a.State != "queued" || a.Buyer.BankAccount != f.provider.officialBuyer.BankAccount {
		t.Fatal("missing native recovery split or forged callback receipt", counts, a.State)
	}
	if f.provider.calls != 0 {
		t.Fatal("recovery directly invoked provider write")
	}
	// A delayed genuine callback must reuse the recovered application/effect.
	if e := f.s.Notification(f.ctx, billNotice(transaction)); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Reconcile(f.ctx, transaction); !errors.Is(e, p.ErrPending) {
		t.Fatal(e)
	}
	if got := invoiceBillCounts(t, f); got[0] != 1 || got[1] != 1 || got[3] != 1 {
		t.Fatal("delayed callback duplicated invoice", got)
	}
	f.run(a.IssueEffect)
	f.provider.status, f.provider.card = "ISSUED", "INSERTED"
	if e := f.s.Reconcile(f.ctx, transaction); e != nil {
		t.Fatal(e)
	}
	if a := f.read(transaction); a.State != "issued" || a.CardState != "INSERTED" {
		t.Fatal("recovered invoice did not use original result path")
	}
}

func TestInvoicePostgreSQLMissingNativeRecoveryAuthority(t *testing.T) {
	for _, scenario := range []string{"non-operator", "unknown-proof", "accepted-proof", "individual", "amount", "order-no", "unverified-payer", "tax-not-configured", "refund-during-read"} {
		t.Run(scenario, func(t *testing.T) {
			f, id, transaction := missingNativeFixture(t)
			canonical := true
			switch scenario {
			case "non-operator":
				canonical = false
			case "unknown-proof":
				f.provider.unsubmittedError = p.ErrUnavailable
			case "accepted-proof":
				f.provider.unsubmittedError = p.ErrConflict
			case "individual":
				f.provider.officialBuyer.Type = "INDIVIDUAL"
			case "amount":
				f.provider.officialBuyer.Amount = 0
			case "order-no":
				f.provider.officialBuyer.OrderNo = "another-order"
			case "unverified-payer":
				if _, e := f.pool.Exec(f.ctx, `UPDATE customer_identities SET assurance='declared',verified_at=NULL WHERE id=$1`, f.identity); e != nil {
					t.Fatal(e)
				}
			case "tax-not-configured":
				delete(f.s.Config.Taxes, "course")
			case "refund-during-read":
				f.s.Provider = missingNativeProvider{Provider: f.provider, proof: f.provider, afterTitle: func() {
					if e := f.refund(id, 100); e != nil {
						t.Fatal(e)
					}
				}}
			}
			if _, e := f.s.RecoverFailed(f.ctx, transaction, "", canonical); e == nil {
				t.Fatal("invalid native recovery accepted")
			}
			if got := invoiceBillCounts(t, f); got != [4]int{} {
				t.Fatal("rejection persisted application or effect", got)
			}
		})
	}
}
