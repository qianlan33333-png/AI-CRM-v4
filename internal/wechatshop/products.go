// Package wechatshop coordinates Provider reads through existing domain Ports.
// River owns execution, attempts and retry. No Provider write occurs here.
package wechatshop

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	jobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"time"
)

var ErrUnavailable = errors.New("wechat shop synchronization unavailable")

type Service struct {
	Orders      orderport.ShopOrderImporter
	Pending     orderport.ShopPendingReader
	OrderReader shopport.OrderReader
	Verifier    shopport.CallbackVerifier
	Pool        *pgxpool.Pool
	UOW         platformport.UnitOfWork
	Client      *river.Client[pgx.Tx]
	AppID       string
	Enabled     bool
	Products    productport.ShopCatalog
	Reader      shopport.ProductReader
	Wake        productport.ShopProductWakeup
	Now         func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) SubmitProductRefresh(ctx context.Context) (shopport.RefreshStatus, error) {
	if s == nil || !s.Enabled || s.AppID == "" {
		return shopport.RefreshStatus{}, ErrUnavailable
	}
	err := s.UOW.Within(ctx, func(ctx context.Context) error {
		tx, err := postgres.RequireTransaction(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "shop-products:"+s.AppID); err != nil {
			return err
		}
		var id int64
		var state string
		err = tx.QueryRow(ctx, `SELECT id,state FROM wechat_shop_product_refreshes WHERE app_id=$1 AND state<>'succeeded'`, s.AppID).Scan(&id, &state)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO wechat_shop_product_refreshes(app_id,state) VALUES($1,'queued') RETURNING id`, s.AppID).Scan(&id)
		}
		if err != nil {
			return err
		}
		args := ProductArgs{RunID: id}
		job, err := jobqueue.InsertTxWithOptions(ctx, s.Client, tx, args, river.InsertOpts{Queue: jobqueue.OutboundQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}}})
		if err == nil && state == "retrying" {
			return jobqueue.WakePendingJobTx(ctx, s.Client, tx, job.Job.ID, args, jobqueue.OutboundQueue)
		}
		return err
	})
	if err != nil {
		return shopport.RefreshStatus{}, err
	}
	return s.ProductRefreshStatus(ctx)
}
func (s *Service) ProductRefreshStatus(ctx context.Context) (shopport.RefreshStatus, error) {
	if s == nil || s.Pool == nil {
		return shopport.RefreshStatus{}, ErrUnavailable
	}
	result := shopport.RefreshStatus{State: "idle"}
	err := s.Pool.QueryRow(ctx, `SELECT r.id,r.state,r.created_at,r.failed_count,(SELECT count(*) FROM wechat_shop_product_refresh_items WHERE run_id=r.id AND added),(SELECT count(*) FROM wechat_shop_product_refresh_items WHERE run_id=r.id AND NOT added),(SELECT max(completed_at) FROM wechat_shop_product_refreshes WHERE app_id=r.app_id AND state='succeeded') FROM wechat_shop_product_refreshes r WHERE app_id=$1 ORDER BY id DESC LIMIT 1`, s.AppID).Scan(&result.ID, &result.State, &result.CreatedAt, &result.Failed, &result.Added, &result.Existing, &result.LastSuccessAt)
	if err == pgx.ErrNoRows {
		return result, nil
	}
	return result, err
}
func (s *Service) refreshProducts(ctx context.Context, run int64) (err error) {
	if !s.Enabled {
		return nil
	}
	if run == 0 {
		status, e := s.ProductRefreshStatus(ctx)
		if e != nil {
			return e
		}
		if status.LastSuccessAt != nil && !status.LastSuccessAt.Before(DailyAt{2, 30}.Previous(s.now())) {
			return nil
		}
		_, e = s.SubmitProductRefresh(ctx)
		return e
	}
	tag, e := s.Pool.Exec(ctx, `UPDATE wechat_shop_product_refreshes SET state='running',failed_count=0 WHERE id=$1 AND app_id=$2 AND state<>'succeeded'`, run, s.AppID)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	defer func() {
		if err != nil {
			_, _ = s.Pool.Exec(context.WithoutCancel(ctx), `UPDATE wechat_shop_product_refreshes SET state='retrying',failed_count=1 WHERE id=$1 AND state<>'succeeded'`, run)
		}
	}()
	seen := map[string]bool{}
	next := ""
	for {
		page, e := s.Reader.ListShopProducts(ctx, next)
		if e != nil {
			return e
		}
		for _, id := range page.IDs {
			product, e := s.Reader.ReadShopProduct(ctx, id)
			if e != nil {
				return e
			}
			if !product.Online {
				continue
			}
			for _, sku := range product.SKUs {
				e = s.UOW.Within(ctx, func(ctx context.Context) error {
					id, added, e := s.Products.ImportShopSKUWithin(ctx, sku, fmt.Sprint(run), s.now())
					if e != nil {
						return e
					}
					tx, e := postgres.RequireTransaction(ctx)
					if e != nil {
						return e
					}
					if _, e = tx.Exec(ctx, `INSERT INTO wechat_shop_product_refresh_items VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, run, id, added); e != nil {
						return e
					}
					if s.Wake != nil {
						return s.Wake.WakeShopProductWithin(ctx, id)
					}
					return nil
				})
				if e != nil {
					return e
				}
			}
		}
		if page.Next == "" {
			break
		}
		if page.Next == next || seen[page.Next] {
			return ErrUnavailable
		}
		seen[page.Next] = true
		next = page.Next
	}
	_, err = s.Pool.Exec(ctx, `UPDATE wechat_shop_product_refreshes SET state='succeeded',completed_at=$2,failed_count=0 WHERE id=$1`, run, s.now())
	return err
}

type ProductArgs struct {
	RunID int64 `json:"run_id" river:"unique"`
}

func (ProductArgs) Kind() string { return "wechat_shop.product_refresh" }

type ProductWorker struct {
	river.WorkerDefaults[ProductArgs]
	Service *Service
}

func (*ProductWorker) Timeout(*river.Job[ProductArgs]) time.Duration { return time.Hour }
func (w *ProductWorker) Work(ctx context.Context, j *river.Job[ProductArgs]) error {
	if w.Service == nil {
		return ErrUnavailable
	}
	return w.Service.refreshProducts(ctx, j.Args.RunID)
}

// Beijing has no daylight-saving transition; fixed UTC+8 is intentional.
type DailyAt struct{ Hour, Minute int }

func (d DailyAt) Next(now time.Time) time.Time {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	t := now.In(loc)
	next := time.Date(t.Year(), t.Month(), t.Day(), d.Hour, d.Minute, 0, 0, loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}
func (d DailyAt) Previous(now time.Time) time.Time { return d.Next(now).Add(-24 * time.Hour) }
func ProductPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(DailyAt{2, 30}, func() (river.JobArgs, *river.InsertOpts) {
		return ProductArgs{}, &river.InsertOpts{Queue: jobqueue.OutboundQueue}
	}, &river.PeriodicJobOpts{ID: "wechat-shop-products-daily", RunOnStart: true})
}
