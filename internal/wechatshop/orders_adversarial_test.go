package wechatshop

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
)

type paginatedOrderReader struct {
	mu                       sync.Mutex
	windows                  [][2]time.Time
	keys                     []string
	failSecond, cycle, empty bool
	firsts                   atomic.Int32
	barrier                  chan struct{}
}

func (r *paginatedOrderReader) ListShopOrders(ctx context.Context, a, b time.Time, key string) (shopport.OrderPage, error) {
	r.mu.Lock()
	r.windows = append(r.windows, [2]time.Time{a, b})
	r.keys = append(r.keys, key)
	r.mu.Unlock()
	if r.barrier != nil && key == "" {
		if r.firsts.Add(1) == 2 {
			close(r.barrier)
		}
		select {
		case <-r.barrier:
		case <-ctx.Done():
			return shopport.OrderPage{}, ctx.Err()
		}
	}
	if r.empty {
		return shopport.OrderPage{}, nil
	}
	if key == "" {
		return shopport.OrderPage{IDs: []string{"first", "boundary"}, Next: "second", HasMore: true}, nil
	}
	if r.failSecond {
		return shopport.OrderPage{}, errors.New("fixture page two failed")
	}
	if r.cycle {
		return shopport.OrderPage{Next: "second", HasMore: true}, nil
	}
	return shopport.OrderPage{IDs: []string{"boundary", "last"}}, nil
}
func (r *paginatedOrderReader) ReadShopOrder(_ context.Context, id string) (orderport.ShopOrder, error) {
	return orderport.ShopOrder{AppID: "test-shop", ID: id, PaidAt: time.Unix(1700000000, 0)}, nil
}

type concurrentWindowImporter struct {
	mu    sync.Mutex
	calls map[string]int
}

func (i *concurrentWindowImporter) ImportShopOrderWithin(ctx context.Context, o orderport.ShopOrder, _ time.Time) error {
	if _, e := postgres.RequireTransaction(ctx); e != nil {
		return e
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls[o.ID]++
	return nil
}

func TestPostgreSQLShopDailyPaginationFailureCycleAndConcurrentRecovery(t *testing.T) {
	s, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Now = func() time.Time { return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) }
	if e := s.EnsureOrderProgress(ctx); e != nil {
		t.Fatal(e)
	}
	var from time.Time
	if e := s.Pool.QueryRow(ctx, `SELECT completed_until FROM wechat_shop_order_progress WHERE app_id=$1`, s.AppID).Scan(&from); e != nil {
		t.Fatal(e)
	}
	until := from.Add(24 * time.Hour)
	r := &paginatedOrderReader{failSecond: true}
	i := &concurrentWindowImporter{calls: map[string]int{}}
	s.OrderReader = r
	s.Orders = i
	assertProgress := func(expected time.Time) {
		t.Helper()
		var got time.Time
		if e := s.Pool.QueryRow(ctx, `SELECT completed_until FROM wechat_shop_order_progress WHERE app_id=$1`, s.AppID).Scan(&got); e != nil || !got.Equal(expected) {
			t.Fatal("window advanced past uncompleted page", e)
		}
	}
	if e := s.syncOrders(ctx, until); e == nil {
		t.Fatal("second-page failure accepted")
	}
	assertProgress(from)
	r.failSecond = false
	r.cycle = true
	if e := s.syncOrders(ctx, until); e == nil {
		t.Fatal("repeating cursor accepted")
	}
	assertProgress(from)
	r.cycle = false
	r.barrier = make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Go(func() { errs <- s.syncOrders(ctx, until) })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent daily recovery", e)
		}
	}
	assertProgress(until)
	for _, id := range []string{"first", "boundary", "last"} {
		if i.calls[id] < 2 {
			t.Fatal("lost cross-page or concurrent detail", id)
		}
	}
	for _, window := range r.windows {
		if !window[0].Equal(from) || !window[1].Equal(until) {
			t.Fatal("page cursor changed time window")
		}
	}
	// Final closed window is not fetched again on the next worker restart.
	before := len(r.windows)
	if e := s.syncOrders(ctx, until); e != nil || len(r.windows) != before {
		t.Fatal("completed window repeated", e)
	}
}

func TestShopBackfillSegmentsPreserveExactSevenDayBoundaries(t *testing.T) {
	r := &paginatedOrderReader{empty: true}
	s := &Service{Enabled: true, OrderReader: r}
	from := time.Date(2026, 9, 30, 19, 0, 0, 0, time.UTC)
	until := from.Add(15 * 24 * time.Hour)
	if e := s.ReadWindow(context.Background(), from, until); e != nil {
		t.Fatal(e)
	}
	if len(r.windows) != 3 {
		t.Fatal("seven-day segmentation", len(r.windows))
	}
	expected := [][2]time.Time{{from, from.Add(7 * 24 * time.Hour)}, {from.Add(7 * 24 * time.Hour), from.Add(14 * 24 * time.Hour)}, {from.Add(14 * 24 * time.Hour), until}}
	for n, w := range r.windows {
		if w != expected[n] || r.keys[n] != "" {
			t.Fatal("boundary gap, overlap expansion or cursor carried into next segment")
		}
	}
}
