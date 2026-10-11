package wechatshop

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, *productstore.Repository) {
	t.Helper()
	url := os.Getenv("AICRM_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	admin, e := pgx.Connect(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	schema := "shop_test_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(ctx)
	})
	migrator, e := rivermigrate.New(riverpgxv5.New(pool), nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); e != nil {
		t.Fatal(e)
	}
	files, e := filepath.Glob("../../migrations/*.sql")
	if e != nil {
		t.Fatal(e)
	}
	sort.Strings(files)
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(raw)); e != nil {
			t.Fatalf("%s: %v", file, e)
		}
	}
	wrap, _ := postgres.Wrap(pool, time.Second)
	uow, _ := postgres.NewUnitOfWork(wrap)
	repo, _ := productstore.NewPostgreSQL(pool, uow)
	auditService, _ := audit.NewService(audit.NewPostgreSQLStore())
	events, _ := productstore.NewTransactionalEventAppender(auditService, outbox.NewPostgreSQL())
	workers := river.NewWorkers()
	_ = river.AddWorkerSafely[ProductArgs](workers, &ProductWorker{})
	_ = river.AddWorkerSafely[OrderArgs](workers, &OrderWorker{})
	_ = river.AddWorkerSafely[DailyOrderArgs](workers, &DailyOrderWorker{})
	client, e := jobqueue.NewInsertClient(pool, workers)
	if e != nil {
		t.Fatal(e)
	}
	service := &Service{Pool: pool, UOW: uow, Client: client, AppID: "test-shop", Enabled: true, Products: productapp.ShopCatalog{Store: repo, Events: events}}
	return service, repo
}

type productReads struct {
	skus []productport.ShopSKU
	fail bool
}

func (r *productReads) ListShopProducts(_ context.Context, key string) (shopport.ProductPage, error) {
	if key == "" {
		return shopport.ProductPage{IDs: []string{"parent"}, Next: "last"}, nil
	}
	if r.fail {
		return shopport.ProductPage{}, errors.New("page failed")
	}
	return shopport.ProductPage{}, nil
}
func (r *productReads) ReadShopProduct(context.Context, string) (shopport.Product, error) {
	return shopport.Product{ID: "parent", Online: true, SKUs: r.skus}, nil
}
func TestPostgreSQLShopProductRefreshReplayAndNewSKU(t *testing.T) {
	s, repo := fixture(t)
	ctx := context.Background()
	reader := &productReads{skus: []productport.ShopSKU{{AppID: s.AppID, ProductID: "parent", SKUID: "90071992547409931234", Name: "课程 / 月度", PriceMinor: 100}}, fail: true}
	s.Reader = reader
	var group sync.WaitGroup
	ids := make(chan int64, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			v, e := s.SubmitProductRefresh(ctx)
			if e != nil {
				errs <- e
			} else {
				ids <- v.ID
			}
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	var id int64
	for got := range ids {
		if id != 0 && got != id {
			t.Fatalf("concurrent refresh IDs differ")
		}
		id = got
	}
	if e := s.refreshProducts(ctx, id); e == nil {
		t.Fatal("page failure accepted")
	}
	v, _ := s.ProductRefreshStatus(ctx)
	if v.State != "retrying" || v.Added != 1 || v.Failed != 1 {
		t.Fatalf("partial receipt: %+v", v)
	}
	var productID productport.ID
	if e := s.UOW.Within(ctx, func(ctx context.Context) error {
		var e error
		productID, _, e = repo.ResolveShopSKUWithin(ctx, s.AppID, "parent", reader.skus[0].SKUID)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Pool.Exec(ctx, `UPDATE products SET name='人工名称',price_minor=777 WHERE id=$1`, productID); e != nil {
		t.Fatal(e)
	}
	reader.fail = false
	if e := s.refreshProducts(ctx, id); e != nil {
		t.Fatal(e)
	}
	v, _ = s.ProductRefreshStatus(ctx)
	if v.Added != 1 || v.Existing != 0 || v.State != "succeeded" {
		t.Fatalf("retry counts: %+v", v)
	}
	reader.skus = append(reader.skus, productport.ShopSKU{AppID: s.AppID, ProductID: "parent", SKUID: "second", Name: "课程 / 年度", PriceMinor: 900})
	v, e := s.SubmitProductRefresh(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.refreshProducts(ctx, v.ID); e != nil {
		t.Fatal(e)
	}
	v, _ = s.ProductRefreshStatus(ctx)
	if v.Added != 1 || v.Existing != 1 {
		t.Fatalf("new SKU: %+v", v)
	}
	reader.skus = nil
	v, e = s.SubmitProductRefresh(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.refreshProducts(ctx, v.ID); e != nil {
		t.Fatal(e)
	}
	var name string
	var price, count int64
	if e = s.Pool.QueryRow(ctx, `SELECT name,price_minor,(SELECT count(*) FROM products) FROM products WHERE id=$1`, productID).Scan(&name, &price, &count); e != nil || name != "人工名称" || price != 777 || count != 2 {
		t.Fatalf("overwritten/delisted %s %d %d %v", name, price, count, e)
	}
}
func TestShopDailyScheduleBeijing(t *testing.T) {
	now := time.Date(2026, 10, 11, 18, 29, 0, 0, time.UTC)
	if got := (DailyAt{2, 30}).Next(now); !got.Equal(time.Date(2026, 10, 11, 18, 30, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
	at := (DailyAt{2, 30}).Next(now)
	if !(DailyAt{2, 30}).Next(at).Equal(at.Add(24 * time.Hour)) {
		t.Fatal("boundary repeated")
	}
}
