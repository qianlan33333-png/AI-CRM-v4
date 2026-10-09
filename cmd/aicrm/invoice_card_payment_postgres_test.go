package main

import (
	"context"
	"crypto/sha256"
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	oa "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	op "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	ps "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type ownCardProvider struct {
	p.Provider
	mutate func(p.Result) p.Result
}

func TestInvoicePostgreSQLOriginalCheckoutOAuthReturnRetention(t *testing.T) {
	f := newInvoiceFixture(t)
	old, e := os.ReadFile(filepath.Join(surveyJourneyRepositoryRoot(t), "migrations/0225_payment_h5_orders_return_path.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(f.ctx, string(old)); e != nil {
		t.Fatal(e)
	}
	paths := []string{"/my/orders", "/pay/course", "/s/term/pay", "/distribution"}
	for _, path := range paths {
		d := sha256.Sum256([]byte(path))
		if e = f.s.UoW.Within(f.ctx, func(tx context.Context) error {
			return (h5oauth.PostgreSQL{}).Create(tx, d, h5oauth.State{ReturnPath: path, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
		}); e != nil {
			t.Fatal(e)
		}
	}
	migration, e := os.ReadFile(filepath.Join(surveyJourneyRepositoryRoot(t), "migrations/0228_payment_h5_original_checkout_return.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(f.ctx, string(migration)); e != nil {
		t.Fatal(e)
	}
	for _, path := range append(paths, "/pay/course?resume_order=7", "/s/term/pay?resume_order=9223372036854775807") {
		d := sha256.Sum256([]byte("new:" + path))
		if e = f.s.UoW.Within(f.ctx, func(tx context.Context) error {
			store := h5oauth.PostgreSQL{}
			if e := store.Create(tx, d, h5oauth.State{ReturnPath: path, ExpiresAt: time.Now().Add(time.Hour)}, time.Now()); e != nil {
				return e
			}
			state, e := store.Consume(tx, d, time.Now())
			if state.ReturnPath != path {
				t.Fatal("return path lost", state)
			}
			return e
		}); e != nil {
			t.Fatal(path, e)
		}
	}
	for _, path := range []string{"/pay/course?resume_order=0", "/pay/course?resume_order=01", "/pay/course?resume_order=9223372036854775808", "/pay/course?resume_order=7&next=/admin", "/p/course?resume_order=7", "/pay/course?resume_order=%37", "/pay/a%2Fb?resume_order=7"} {
		d := sha256.Sum256([]byte(path))
		if e = f.s.UoW.Within(f.ctx, func(tx context.Context) error {
			return (h5oauth.PostgreSQL{}).Create(tx, d, h5oauth.State{ReturnPath: path, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
		}); e == nil {
			t.Fatal("unsafe DB return accepted", path)
		}
	}
	var count int
	if e = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM payment_h5_oauth_states WHERE consumed_at IS NULL").Scan(&count); e != nil || count != len(paths) {
		t.Fatal("preexisting OAuth records changed", count, e)
	}
}

func (v ownCardProvider) Query(ctx context.Context, a p.Application) (p.Result, error) {
	r, e := v.Provider.Query(ctx, a)
	if e == nil {
		return v.mutate(r), nil
	}
	return r, e
}

func TestInvoicePostgreSQLNativeCardOwnershipAndRefundRace(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "卡包课程", "wechat_pay", time.Now())
	r := f.create(id, "native-card-key-000001")
	if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	a := f.read(r.ID)
	f.run(a.IssueEffect)
	f.provider.status = "ISSUED"
	f.provider.card = "INSERTED"
	if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	queries := 0
	f.s.Provider = ownCardProvider{Provider: f.provider, mutate: func(r p.Result) p.Result {
		queries++
		r.Card.AppID = "wx-fixture"
		r.Card.OpenID = "openid-isolated-fixture"
		r.Card.ID = "synthetic-card"
		r.Card.Code = "synthetic-code"
		return r
	}}
	entry, e := f.s.Card(f.ctx, f.token, r.ID)
	if e != nil || entry.ID != "synthetic-card" {
		t.Fatal(entry, e)
	}
	before := queries
	if _, e = f.s.Card(f.ctx, "expired-or-other-session-0001", r.ID); !errors.Is(e, pp.ErrSessionRequired) {
		t.Fatal("invalid session", e)
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE invoice_applications SET customer_id=$2 WHERE id=$1`, r.ID, f.other); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Card(f.ctx, f.token, r.ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("other owner", e)
	}
	if queries != before {
		t.Fatal("provider read preceded ownership validation")
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE invoice_applications SET customer_id=$2 WHERE id=$1`, r.ID, f.customer); e != nil {
		t.Fatal(e)
	}
	f.s.Provider = ownCardProvider{Provider: f.s.Provider, mutate: func(v p.Result) p.Result {
		if _, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET refund_confirmed=true WHERE id=$1`, r.ID); e != nil {
			t.Fatal(e)
		}
		return v
	}}
	if _, e = f.s.Card(f.ctx, f.token, r.ID); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("refund during official query", e)
	}
	if f.provider.calls != 1 {
		t.Fatal("card read caused invoice writes", f.provider.calls)
	}
}

func TestInvoicePostgreSQLOriginalPaymentReadOnlyAndPayerScope(t *testing.T) {
	f := newInvoiceFixture(t)
	repo := f.s.Orders.(*orderstore.Repository)
	f.s.SelfPayments = ps.NewPostgreSQL()
	f.s.Checkouts = oa.NewService(f.s.UoW, repo)
	var id int64
	e := f.s.UoW.Within(f.ctx, func(tx context.Context) error {
		v, e := f.s.Checkouts.(*oa.Service).CreatePaymentOrderWithin(tx, op.PaymentOrderCommand{Provider: "wechat_pay", MerchantOrderNo: "original-resume-7", PayerCustomerID: f.customer, BeneficiaryCustomerID: f.customer, ProductID: 9, ProductType: "standard_product", ProductCode: "course", ProductName: "原商品", ProductVersion: 1, UnitAmountMinor: 9900, Currency: "CNY", ActorScope: "resume-fixture-original", IdempotencyKey: "resume-fixture-order-0001"})
		id = v.ID
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(f.ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at)SELECT id,'wechat_pay','h5_official_account',merchant_order_no,$2,payer_customer_id,beneficiary_customer_id,amount_minor,'CNY','awaiting_payment',1,created_at,updated_at FROM orders WHERE id=$1`, id, f.identity); e != nil {
		t.Fatal(e)
	}
	out, e := f.s.ContinuePayment(f.ctx, f.token, id)
	if e != nil || out.Amount != 9900 || out.ProductName != "原商品" {
		t.Fatal(out, e)
	}
	page, e := f.s.List(f.ctx, f.token, p.OrdersQuery{State: "pending"})
	if e != nil || len(page.Items) != 1 || page.Items[0].PaymentPath != out.Path {
		t.Fatal(page, e)
	}
	other := f.order(f.other, f.customer, "pending_payment", "他人赠送订单", "wechat_pay", time.Now())
	if _, e = f.s.ContinuePayment(f.ctx, f.token, other); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("beneficiary can continue payer payment", e)
	}
	if _, e = f.s.ContinuePayment(f.ctx, "invalid-session-0000000001", id); !errors.Is(e, pp.ErrSessionRequired) {
		t.Fatal("session", e)
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE payments SET amount_minor=9901 WHERE order_id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ContinuePayment(f.ctx, f.token, id); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("inconsistent payment amount was accepted", e)
	}
	var count, version, effects, apps int
	if e = f.pool.QueryRow(f.ctx, `SELECT count(*),max(version),(SELECT count(*) FROM external_effects),(SELECT count(*) FROM invoice_applications) FROM payments`).Scan(&count, &version, &effects, &apps); e != nil {
		t.Fatal(e)
	}
	if count != 1 || version != 1 || effects != 0 || apps != 0 {
		t.Fatal("resume read created a new intent", count, version, effects, apps)
	}
}
