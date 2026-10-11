package wechatshop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	jobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"io"
	"net/http"
	"time"
)

func (s *Service) EnsureOrderProgress(ctx context.Context) error {
	if !s.Enabled {
		return nil
	}
	return s.UOW.Within(ctx, func(ctx context.Context) error {
		tx, e := postgres.RequireTransaction(ctx)
		if e != nil {
			return e
		}
		now := s.now().Truncate(time.Second)
		_, e = tx.Exec(ctx, `INSERT INTO wechat_shop_order_progress(app_id,activated_at,completed_until) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, s.AppID, now, (DailyAt{3, 0}).Previous(now).Add(-24*time.Hour))
		return e
	})
}
func (s *Service) readOrder(ctx context.Context, id string, requirePaid bool) error {
	if !s.Enabled {
		return nil
	}
	if s.OrderReader == nil || s.Orders == nil {
		return ErrUnavailable
	}
	order, e := s.OrderReader.ReadShopOrder(ctx, id)
	if e != nil {
		return e
	}
	if order.AppID != s.AppID || order.ID != id {
		return ErrUnavailable
	}
	if order.PaidAt.IsZero() {
		if requirePaid {
			return ErrUnavailable
		}
		return nil
	}
	return s.UOW.Within(ctx, func(ctx context.Context) error {
		tx, e := postgres.RequireTransaction(ctx)
		if e != nil {
			return e
		}
		var cutoff time.Time
		if e = tx.QueryRow(ctx, `SELECT activated_at FROM wechat_shop_order_progress WHERE app_id=$1`, s.AppID).Scan(&cutoff); e != nil {
			return e
		}
		return s.Orders.ImportShopOrderWithin(ctx, order, cutoff)
	})
}

// ReadWindow is also the explicit initial/exception backfill entry point.
// Each chunk respects the official seven-day limit; normal daily calls use 24h.
func (s *Service) ReadWindow(ctx context.Context, from, until time.Time) error {
	if !s.Enabled || !until.After(from) {
		return ErrUnavailable
	}
	for start := from; start.Before(until); {
		end := start.Add(7 * 24 * time.Hour)
		if end.After(until) {
			end = until
		}
		next := ""
		seen := map[string]bool{}
		for {
			page, e := s.OrderReader.ListShopOrders(ctx, start, end, next)
			if e != nil {
				return e
			}
			for _, id := range page.IDs {
				if e = s.readOrder(ctx, id, false); e != nil {
					return e
				}
			}
			if !page.HasMore {
				break
			}
			if page.Next == "" || page.Next == next || seen[page.Next] {
				return ErrUnavailable
			}
			seen[page.Next] = true
			next = page.Next
		}
		start = end
	}
	return nil
}
func (s *Service) syncOrders(ctx context.Context, until time.Time) error {
	if !s.Enabled {
		return nil
	}
	if e := s.EnsureOrderProgress(ctx); e != nil {
		return e
	}
	if until.IsZero() {
		until = (DailyAt{3, 0}).Previous(s.now())
	}
	for {
		var from time.Time
		if e := s.Pool.QueryRow(ctx, `SELECT completed_until FROM wechat_shop_order_progress WHERE app_id=$1`, s.AppID).Scan(&from); e != nil {
			return e
		}
		if !from.Before(until) {
			return nil
		}
		end := from.Add(24 * time.Hour)
		if end.After(until) {
			end = until
		}
		if e := s.ReadWindow(ctx, from, end); e != nil {
			return e
		}
		// A concurrent startup/daily task may have advanced this exact window.
		// CAS never skips an incomplete window, and replayed reads share Order dedup.
		if _, e := s.Pool.Exec(ctx, `UPDATE wechat_shop_order_progress SET completed_until=$3 WHERE app_id=$1 AND completed_until=$2`, s.AppID, from, end); e != nil {
			return e
		}
	}
}

type OrderArgs struct {
	OrderID     string `json:"order_id" river:"unique"`
	Receipt     string `json:"receipt" river:"unique"`
	RequirePaid bool   `json:"require_paid"`
}

func (OrderArgs) Kind() string { return "wechat_shop.order_read" }

type OrderWorker struct {
	river.WorkerDefaults[OrderArgs]
	Service *Service
}

func (*OrderWorker) Timeout(*river.Job[OrderArgs]) time.Duration { return 2 * time.Minute }
func (w *OrderWorker) Work(ctx context.Context, j *river.Job[OrderArgs]) error {
	if w.Service == nil {
		return ErrUnavailable
	}
	return w.Service.readOrder(ctx, j.Args.OrderID, j.Args.RequirePaid)
}

type DailyOrderArgs struct {
	Until int64 `json:"until" river:"unique"`
}

func (DailyOrderArgs) Kind() string { return "wechat_shop.orders_daily" }

type DailyOrderWorker struct {
	river.WorkerDefaults[DailyOrderArgs]
	Service *Service
}

func (*DailyOrderWorker) Timeout(*river.Job[DailyOrderArgs]) time.Duration { return 2 * time.Hour }
func (w *DailyOrderWorker) Work(ctx context.Context, j *river.Job[DailyOrderArgs]) error {
	if w.Service == nil {
		return ErrUnavailable
	}
	until := time.Time{}
	if j.Args.Until > 0 {
		until = time.Unix(j.Args.Until, 0).UTC()
	}
	return w.Service.syncOrders(ctx, until)
}
func OrderPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(DailyAt{3, 0}, func() (river.JobArgs, *river.InsertOpts) {
		return DailyOrderArgs{Until: (DailyAt{3, 0}).Previous(time.Now().UTC()).Unix()}, &river.InsertOpts{Queue: jobqueue.OutboundQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true}}
	}, &river.PeriodicJobOpts{ID: "wechat-shop-orders-daily", RunOnStart: true})
}
func (s *Service) enqueueOrder(ctx context.Context, id, receipt string, requirePaid bool) error {
	tx, e := postgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	_, e = jobqueue.InsertTxWithOptions(ctx, s.Client, tx, OrderArgs{OrderID: id, Receipt: receipt, RequirePaid: requirePaid}, river.InsertOpts{Queue: jobqueue.OutboundQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}}})
	return e
}
func (s *Service) WakeShopProductWithin(ctx context.Context, id productport.ID) error {
	if !s.Enabled || s.Pending == nil {
		return nil
	}
	sku, found, e := s.Products.ShopSKUCoordinatesWithin(ctx, id)
	if e != nil || !found {
		return e
	}
	ids, e := s.Pending.PendingShopOrdersWithin(ctx, sku.AppID, sku.ProductID, sku.SKUID)
	if e != nil {
		return e
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return e
	}
	receipt := hex.EncodeToString(random[:])
	for _, id := range ids {
		if e = s.enqueueOrder(ctx, id, receipt, false); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) AcceptCallback(ctx context.Context, callback shopport.Callback) error {
	if !s.Enabled {
		return ErrUnavailable
	}
	return s.UOW.Within(ctx, func(ctx context.Context) error {
		tx, e := postgres.RequireTransaction(ctx)
		if e != nil {
			return e
		}
		inserted, e := tx.Exec(ctx, `INSERT INTO wechat_shop_callback_receipts(app_id,event_digest,body_digest) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, s.AppID, callback.EventDigest[:], callback.BodyDigest[:])
		if e != nil || inserted.RowsAffected() == 0 {
			return e
		}
		if !callback.Relevant {
			return nil
		}
		return s.enqueueOrder(ctx, callback.OrderID, hex.EncodeToString(callback.EventDigest[:]), true)
	})
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.Enabled || s.Verifier == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	query := map[string]string{}
	for _, key := range []string{"signature", "timestamp", "nonce", "echostr", "msg_signature"} {
		query[key] = r.URL.Query().Get(key)
	}
	if r.Method == http.MethodGet {
		echo, e := s.Verifier.VerifyURL(r.Context(), query)
		if e != nil {
			http.Error(w, "invalid signature", 400)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, echo)
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, 128<<10+1))
	if e != nil || len(body) > 128<<10 {
		http.Error(w, "invalid callback", 400)
		return
	}
	callback, e := s.Verifier.VerifyEvent(r.Context(), body, query)
	if e != nil {
		http.Error(w, "invalid signature", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	if e = s.AcceptCallback(ctx, callback); e != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, "success")
}
