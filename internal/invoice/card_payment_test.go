package invoice

import (
	"context"
	"errors"

	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	od "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	op "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	pd "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"testing"
)

func TestInvoiceCardRejectsUntrustedOrUndeliveredFacts(t *testing.T) {
	a := p.Application{InvoiceID: "fapiao", BlueNumber: "blue", Snapshot: p.Snapshot{Amount: 9900, SellerTaxID: "seller", AppID: "wx-app"}}
	r := p.Result{InvoiceID: a.InvoiceID, Status: "ISSUED", TotalAmount: 9900}
	r.Seller.TaxpayerID = "seller"
	r.Blue.Number = "blue"
	r.Card.AppID = "wx-app"
	r.Card.OpenID = "recipient"
	r.Card.ID = "card"
	r.Card.Code = "code"
	r.Card.Status = "INSERTED"
	entry, e := verifiedCard(a, r, "recipient")
	if e != nil || entry.ID != "card" || entry.Code != "code" {
		t.Fatal(entry, e)
	}
	cases := []struct {
		name   string
		mutate func(*p.Result)
		want   error
	}{
		{"other recipient", func(v *p.Result) { v.Card.OpenID = "other" }, p.ErrConflict},
		{"other app", func(v *p.Result) { v.Card.AppID = "wx-other" }, p.ErrConflict},
		{"other invoice", func(v *p.Result) { v.InvoiceID = "other" }, p.ErrConflict},
		{"other blue", func(v *p.Result) { v.Blue.Number = "other" }, p.ErrConflict},
		{"other seller", func(v *p.Result) { v.Seller.TaxpayerID = "other" }, p.ErrConflict},
		{"other amount", func(v *p.Result) { v.TotalAmount++ }, p.ErrConflict},
		{"reversed", func(v *p.Result) { v.Status = "REVERSED" }, p.ErrConflict},
		{"insert pending", func(v *p.Result) { v.Card.Status = "INSERT_ACCEPTED" }, p.ErrPending},
		{"discarded", func(v *p.Result) { v.Card.Status = "DISCARDED" }, p.ErrIneligible},
		{"missing code", func(v *p.Result) { v.Card.Code = "" }, p.ErrUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := r
			c.mutate(&v)
			if _, e := verifiedCard(a, v, "recipient"); !errors.Is(e, c.want) {
				t.Fatal(e)
			}
		})
	}
}

type selfPaymentFixture pd.Payment

func (v selfPaymentFixture) SelfCheckoutWithin(context.Context, int64) (pd.Payment, error) {
	return pd.Payment(v), nil
}

type selfSnapshotFixture op.CheckoutSnapshot

func (v selfSnapshotFixture) ReadCheckoutSnapshotWithin(context.Context, int64) (op.CheckoutSnapshot, error) {
	return op.CheckoutSnapshot(v), nil
}

type selfRefundFixture bool

func (v selfRefundFixture) RefundRelatedOrderIDsWithin(context.Context, []int64) (map[int64]struct{}, error) {
	m := map[int64]struct{}{}
	if v {
		m[7] = struct{}{}
	}
	return m, nil
}

func TestInvoiceOriginalCheckoutScopesAndFrozenFacts(t *testing.T) {
	payer := int64(11)
	pid := int64(9)
	o := od.Snapshot{ID: 7, Provider: od.ProviderWeChatPay, RecordOrigin: od.RecordOriginNative, EffectEligible: true, Status: od.StatusPendingPayment, PayerCustomerID: &payer, MerchantOrderNo: "original-7", Amount: od.Money{AmountMinor: 8800, Currency: "CNY"}, Items: []od.ItemSnapshot{{ProductID: &pid, ProductCode: "course"}}}
	v := pd.Payment{OrderID: 7, Provider: pd.ProviderWeChatPay, Channel: pd.ChannelH5Official, PayerIdentityID: 21, PayerCustomerID: 11, MerchantOrderNo: o.MerchantOrderNo, Status: pd.StatusAwaitingPayment, AmountMinor: 8800, Currency: "CNY"}
	actor := pp.SessionActor{PayerIdentityID: 21, Channel: pd.ChannelH5Official}
	f := op.CheckoutSnapshot{OrderID: 7, ProductType: "standard_product", ProductID: 9, ProductCode: "course", ProductName: "原商品", PayableAmountMinor: 8800, Currency: "CNY"}
	s := Service{SelfPayments: selfPaymentFixture(v), Checkouts: selfSnapshotFixture(f), Refunds: selfRefundFixture(false)}
	out, e := s.continuationWithin(context.Background(), actor, []int64{12, 11}, op.SelfOrder{Order: o})
	if e != nil || out.Path != "/pay/course?resume_order=7" || out.Amount != 8800 {
		t.Fatal(out, e)
	}
	f.ProductType = "service_period"
	s.Checkouts = selfSnapshotFixture(f)
	out, e = s.continuationWithin(context.Background(), actor, []int64{11}, op.SelfOrder{Order: o})
	if e != nil || out.Path != "/s/course/pay?resume_order=7" {
		t.Fatal(out, e)
	}
	for _, mutate := range []func(*pd.Payment){func(v *pd.Payment) { v.PayerIdentityID++ }, func(v *pd.Payment) { v.PayerCustomerID++ }, func(v *pd.Payment) { v.AmountMinor++ }, func(v *pd.Payment) { v.Historical = true }, func(v *pd.Payment) { v.Status = pd.StatusCancelled }, func(v *pd.Payment) { v.MerchantOrderNo = "other" }, func(v *pd.Payment) { v.Channel = pd.ChannelMiniProgram }} {
		bad := v
		mutate(&bad)
		if continuablePayment(bad, o, actor, []int64{11}) {
			t.Fatal("unsafe continuation", bad.Status)
		}
	}
	if continuablePayment(v, o, actor, []int64{12}) {
		t.Fatal("beneficiary-only ownership")
	}
	s.Refunds = selfRefundFixture(true)
	if _, e = s.continuationWithin(context.Background(), actor, []int64{11}, op.SelfOrder{Order: o}); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("in-flight refund", e)
	}
	v.Provider = pd.ProviderAlipay
	v.Channel = pd.ChannelAlipayWap
	o.Provider = od.ProviderAlipay
	if !continuablePayment(v, o, actor, []int64{11}) {
		t.Fatal("original Alipay checkout unavailable")
	}
}
