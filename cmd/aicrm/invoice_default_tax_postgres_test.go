package main

import (
	"testing"
	"time"

	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
)

func TestInvoicePostgreSQLDefaultTaxSupportsFutureProductBothEntries(t *testing.T) {
	f := newInvoiceFixture(t)
	f.s.Config.Taxes["*"] = p.Tax{Code: "3040203000000000000", Rate: 100, GoodsName: "服务费"}
	for _, entry := range []string{"native", "order-page"} {
		id := f.orderWithProduct(f.customer, f.customer, "paid", "未来新增商品", "future-"+entry, "wechat_pay", time.Now())
		transaction := f.billPayment(id)
		if entry == "native" {
			if e := f.s.Notification(f.ctx, billNotice(transaction)); e != nil {
				t.Fatal("new product native eligibility rejected", e)
			}
			if e := f.s.Reconcile(f.ctx, transaction); e != nil {
				t.Fatal(e)
			}
		} else {
			if _, e := f.s.SubmitTitle(f.ctx, f.token, id, "future-product-confirm-key", titleFixture()); e != nil {
				t.Fatal("new product order-page eligibility rejected", e)
			}
		}
		a := f.read(transaction)
		if a.State != "queued" || a.Snapshot.Lines[0].TaxCode != "3040203000000000000" || a.Snapshot.Lines[0].TaxRate != 100 || a.Snapshot.Lines[0].GoodsName != "服务费" {
			t.Fatal("default tax did not reach accepted effect", entry, a.State)
		}
	}
	if got := invoiceBillCounts(t, f); got[0] != 2 || got[3] != 2 {
		t.Fatal("default tax split invoice flow", got)
	}
}
