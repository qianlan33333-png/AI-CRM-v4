package wechatshop

import (
	"context"
	"crypto/sha256"
	"errors"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"sync"
	"testing"
	"time"
)

type windowReader struct {
	windows [][2]time.Time
	fail    bool
	paid    time.Time
}

func (r *windowReader) ListShopOrders(_ context.Context, a, b time.Time, _ string) (shopport.OrderPage, error) {
	r.windows = append(r.windows, [2]time.Time{a, b})
	return shopport.OrderPage{IDs: []string{"old-created-today-paid"}}, nil
}
func (r *windowReader) ReadShopOrder(_ context.Context, id string) (orderport.ShopOrder, error) {
	if r.fail {
		return orderport.ShopOrder{}, errors.New("failed detail")
	}
	return orderport.ShopOrder{ID: id, AppID: "test-shop", CreatedAt: r.paid.Add(-90 * 24 * time.Hour), PaidAt: r.paid, UpdatedAt: r.paid}, nil
}

type windowImporter struct {
	calls  int
	cutoff time.Time
}

func (i *windowImporter) ImportShopOrderWithin(ctx context.Context, o orderport.ShopOrder, cutoff time.Time) error {
	if _, e := postgres.RequireTransaction(ctx); e != nil {
		return e
	}
	i.calls++
	i.cutoff = cutoff
	return nil
}
func TestPostgreSQLShopDailyWindowFailureAndOutageRecovery(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	reader := &windowReader{paid: now, fail: true}
	importer := &windowImporter{}
	s.OrderReader = reader
	s.Orders = importer
	if e := s.EnsureOrderProgress(ctx); e != nil {
		t.Fatal(e)
	}
	var initial, time0 time.Time
	if e := s.Pool.QueryRow(ctx, `SELECT completed_until,activated_at FROM wechat_shop_order_progress`).Scan(&initial, &time0); e != nil {
		t.Fatal(e)
	}
	until := initial.Add(3 * 24 * time.Hour)
	if e := s.syncOrders(ctx, until); e == nil {
		t.Fatal("detail failure passed")
	}
	if importer.calls != 0 {
		t.Fatal("failed detail reached order import/dispatch")
	}
	var after time.Time
	_ = s.Pool.QueryRow(ctx, `SELECT completed_until FROM wechat_shop_order_progress`).Scan(&after)
	if !after.Equal(initial) {
		t.Fatal("failed window advanced")
	}
	reader.fail = false
	if e := s.syncOrders(ctx, until); e != nil {
		t.Fatal(e)
	}
	if importer.calls != 3 || !importer.cutoff.Equal(time0) {
		t.Fatal("lost day or reset T0")
	}
	for _, w := range reader.windows {
		if w[1].Sub(w[0]) != 24*time.Hour {
			t.Fatal("daily scan expanded")
		}
	}
	_ = s.Pool.QueryRow(ctx, `SELECT completed_until FROM wechat_shop_order_progress`).Scan(&after)
	if !after.Equal(until) {
		t.Fatal("outage not recovered")
	}
	s.Now = func() time.Time { return now.Add(10 * 24 * time.Hour) }
	if e := s.EnsureOrderProgress(ctx); e != nil {
		t.Fatal(e)
	}
	_ = s.Pool.QueryRow(ctx, `SELECT activated_at FROM wechat_shop_order_progress`).Scan(&after)
	if !after.Equal(time0) {
		t.Fatal("activation cutoff reset")
	}
	before := importer.calls
	if e := s.syncOrders(ctx, until); e != nil || importer.calls != before {
		t.Fatal("completed window reread")
	}
	reader.windows = nil
	if e := s.ReadWindow(ctx, initial, initial.Add(15*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if len(reader.windows) != 3 {
		t.Fatal("backfill chunks")
	}
	for _, w := range reader.windows {
		if w[1].Sub(w[0]) > 7*24*time.Hour {
			t.Fatal("API window limit")
		}
	}
}
func TestPostgreSQLShopCallbackAtomicReceiptAndReplay(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	cb := shopport.Callback{Relevant: true, OrderID: "90071992547409931234", EventDigest: sha256.Sum256([]byte("event")), BodyDigest: sha256.Sum256([]byte("body"))}
	if _, e := s.Pool.Exec(ctx, `ALTER TABLE river_job ADD CONSTRAINT fail_shop_job CHECK(kind<>'wechat_shop.order_read')`); e != nil {
		t.Fatal(e)
	}
	if e := s.AcceptCallback(ctx, cb); e == nil {
		t.Fatal("River insert failure ignored")
	}
	if _, e := s.Pool.Exec(ctx, `ALTER TABLE river_job DROP CONSTRAINT fail_shop_job`); e != nil {
		t.Fatal(e)
	}
	var receipts, jobs int
	read := func() {
		t.Helper()
		if e := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM wechat_shop_callback_receipts),(SELECT count(*) FROM river_job WHERE kind='wechat_shop.order_read')`).Scan(&receipts, &jobs); e != nil {
			t.Fatal(e)
		}
	}
	read()
	if receipts != 0 || jobs != 0 {
		t.Fatal("receipt and job not atomic")
	}
	var group sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		group.Add(1)
		go func() { defer group.Done(); errs <- s.AcceptCallback(ctx, cb) }()
	}
	group.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	read()
	if receipts != 1 || jobs != 1 {
		t.Fatal("callback replay duplicated durable work")
	}
}
