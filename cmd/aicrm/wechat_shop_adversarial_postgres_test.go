package main

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// This sink observes the Order contract only. The separate ShopOrders replay
// journey uses real Outbound/EER and a signed loopback HTTP receiver.
type shopSnapshotSink struct {
	event orderport.PaidEvent
	calls int
	fail  bool
}

func (s *shopSnapshotSink) ConsumeShopPaidEventWithin(ctx context.Context, e orderport.PaidEvent) (bool, error) {
	if _, err := postgres.RequireTransaction(ctx); err != nil {
		return false, err
	}
	if s.fail {
		return false, errors.New("fixture consumer failed")
	}
	s.calls++
	s.event = e
	return true, nil
}

func TestPostgreSQLShopStateMoneyIdentityAndImmutableSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	wrapped, e := postgres.Wrap(pool, 3*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := postgres.NewUnitOfWork(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	products, e := productstore.NewPostgreSQL(pool, uow)
	if e != nil {
		t.Fatal(e)
	}
	orders, e := orderstore.NewPostgreSQL(pool, uow)
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	queries := identityquery.NewPostgreSQL()
	fact, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:shop-app", Value: "fixture-shop-buyer", Source: "wechat-shop.order-api"})
	if e != nil {
		t.Fatal(e)
	}
	var customer identityport.ProvisionResult
	if e = uow.Within(ctx, func(tx context.Context) error {
		var err error
		customer, err = oneID.ProvisionCustomerFromVerifiedIdentity(tx, fact)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	sink := &shopSnapshotSink{}
	s := orderapp.ShopOrders{Store: orders, Products: products, Consumer: sink, Cipher: cipher, Identity: oneID, Subjects: queries}
	t0 := time.Now().UTC().Truncate(time.Second)
	original := orderport.ShopOrder{AppID: "shop-app", ID: "90071992547409939876", TransactionID: "fixture-tx", CreatedAt: t0.Add(-90 * 24 * time.Hour), PaidAt: t0, UpdatedAt: t0, PaidMinor: 1050, Phone: "13800138000", Buyer: fact, Lines: []orderport.ShopOrderLine{{ProductID: "p", SKUID: "s1", Name: "课程", Quantity: 2, UnitMinor: 500, PaidMinor: 900}, {ProductID: "p", SKUID: "s2", Name: "资料", Quantity: 1, UnitMinor: 100, PaidMinor: 100}}}
	importOrder := func(v orderport.ShopOrder) error {
		return uow.Within(ctx, func(tx context.Context) error { return s.ImportShopOrderWithin(tx, v, t0) })
	}
	if e = importOrder(original); e != nil {
		t.Fatal("discount/freight/T0 exact boundary", e)
	}
	var paidSnapshot string
	var payer int64
	if e = pool.QueryRow(ctx, `SELECT f.paid_snapshot::text,o.payer_customer_id FROM order_wechat_shop_facts f JOIN orders o ON o.id=f.order_id WHERE o.merchant_order_no=$1`, original.ID).Scan(&paidSnapshot, &payer); e != nil || payer != int64(customer.CustomerID) {
		t.Fatal("OneID verified payer", e)
	}
	assertState := func(state string, refund int64) {
		t.Helper()
		var got string
		var amount, total int64
		var snapshot string
		if e := pool.QueryRow(ctx, `SELECT o.status,o.refunded_minor,o.amount_minor,f.paid_snapshot::text FROM orders o JOIN order_wechat_shop_facts f ON f.order_id=o.id WHERE o.merchant_order_no=$1`, original.ID).Scan(&got, &amount, &total, &snapshot); e != nil || got != state || amount != refund || total != 1050 || snapshot != paidSnapshot {
			t.Fatal("refund state or frozen payment changed", state, refund, e)
		}
	}
	partial := original
	partial.Lines = []orderport.ShopOrderLine{original.Lines[1], original.Lines[0]}
	partial.RefundedMinor = 500
	partial.UpdatedAt = t0.Add(time.Hour)
	partial.Phone = "13900139000"
	if e = importOrder(partial); e != nil {
		t.Fatal("reordered detail replay", e)
	}
	assertState("partially_refunded", 500)
	full := partial
	full.RefundedMinor = 1050
	full.UpdatedAt = t0.Add(2 * time.Hour)
	if e = importOrder(full); e != nil {
		t.Fatal(e)
	}
	assertState("refunded", 1050)
	stale := original
	stale.UpdatedAt = t0.Add(3 * time.Hour)
	if e = importOrder(stale); e != nil {
		t.Fatal(e)
	}
	assertState("refunded", 1050)
	if sink.event.ShopPhone != original.Phone || sink.event.Order.Amount.AmountMinor != 1050 || len(sink.event.Order.Items) != 2 {
		t.Fatal("accepted payment contact or amount rewritten")
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if e := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if count("orders") != 1 || count("order_paid_events") != 1 || count("order_status_history") != 3 {
		t.Fatal("duplicate payment/status history")
	}
	customersBefore := count("customers")
	unknownFact, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:shop-app", Value: "fixture-unknown-buyer", Source: "wechat-shop.order-api"})
	if e != nil {
		t.Fatal(e)
	}
	unknown := original
	unknown.ID = "fixture-unknown"
	unknown.TransactionID = "fixture-unknown-tx"
	unknown.Buyer = unknownFact
	if e = importOrder(unknown); e != nil {
		t.Fatal(e)
	}
	var hasPayer bool
	if e = pool.QueryRow(ctx, `SELECT payer_customer_id IS NOT NULL FROM orders WHERE merchant_order_no=$1`, unknown.ID).Scan(&hasPayer); e != nil || hasPayer || count("customers") != customersBefore {
		t.Fatal("unresolved buyer guessed or provisioned", e)
	}
	before := original
	before.ID = "before-t0"
	before.TransactionID = "before-t0-tx"
	before.PaidAt = t0.Add(-time.Second)
	calls := sink.calls
	if e = importOrder(before); e != nil || sink.calls != calls {
		t.Fatal("T0-1s activated", e)
	}
	zero := original
	zero.ID = "zero-paid"
	zero.TransactionID = "zero-paid-tx"
	zero.PaidMinor = 0
	zero.Lines = []orderport.ShopOrderLine{{ProductID: "zero", SKUID: "zero", Name: "零元", Quantity: 2, UnitMinor: 0, PaidMinor: 0}}
	if e = importOrder(zero); e != nil {
		t.Fatal("valid zero paid fact", e)
	}
	wrongScope, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:other-app", Value: "fixture-shop-buyer", Source: "wechat-shop.order-api"})
	if e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name   string
		mutate func(*orderport.ShopOrder)
	}{
		{"negative-paid", func(v *orderport.ShopOrder) { v.PaidMinor = -1 }},
		{"negative-refund", func(v *orderport.ShopOrder) { v.RefundedMinor = -1 }},
		{"excess-refund", func(v *orderport.ShopOrder) { v.RefundedMinor = v.PaidMinor + 1 }},
		{"negative-line", func(v *orderport.ShopOrder) { v.Lines[0].PaidMinor = -1 }},
		{"zero-quantity", func(v *orderport.ShopOrder) { v.Lines[0].Quantity = 0 }},
		{"sum-overflow", func(v *orderport.ShopOrder) { v.Lines[0].PaidMinor = math.MaxInt64; v.Lines[1].PaidMinor = 1 }},
		{"blank-name", func(v *orderport.ShopOrder) { v.Lines[0].Name = " " }},
		{"wrong-scope", func(v *orderport.ShopOrder) { v.Buyer = wrongScope }},
	}
	ordersBefore, eventsBefore := count("orders"), count("order_paid_events")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := original
			v.ID = tc.name
			v.TransactionID = tc.name
			v.Lines = append([]orderport.ShopOrderLine(nil), original.Lines...)
			tc.mutate(&v)
			if e := importOrder(v); e == nil || count("orders") != ordersBefore || count("order_paid_events") != eventsBefore {
				t.Fatal("invalid source committed", e)
			}
		})
	}
	other := original
	other.AppID = "other-app"
	other.Buyer = identitydomain.VerifiedFact{}
	if e = importOrder(other); e == nil || count("orders") != ordersBefore {
		t.Fatal("same order ID reassigned to another App")
	}
	sink.fail = true
	abort := original
	abort.ID = "consumer-abort"
	abort.TransactionID = "consumer-abort-tx"
	if e = importOrder(abort); e == nil || count("orders") != ordersBefore || count("order_paid_events") != eventsBefore {
		t.Fatal("consumer failure split transaction", e)
	}
}
