package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

func imageMutationServices(t *testing.T) (context.Context, *pgxpool.Pool, *productapp.Service, *productapp.ServicePeriodService, *productapp.LocalProductLifecycleService) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is required for image mutation PostgreSQL test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_product_images_" + hex.EncodeToString(nonce[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"0001_platform.sql", "0010_product.sql"} {
		sql, err := os.ReadFile(memberGridMigration(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE outbox_events (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, aggregate_type TEXT NOT NULL,
 aggregate_id TEXT NOT NULL,event_type TEXT NOT NULL,event_version SMALLINT NOT NULL,
 idempotency_key TEXT NOT NULL UNIQUE,payload_json JSONB NOT NULL,occurred_at TIMESTAMPTZ NOT NULL,processed_at TIMESTAMPTZ);
 CREATE TABLE product_imported_service_period_definitions(product_id BIGINT PRIMARY KEY REFERENCES products(id),duration_days INTEGER NOT NULL CHECK(duration_days>0))`); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := productstore.NewPostgreSQL(pool, unit)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	events, err := productstore.NewTransactionalEventAppender(audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	return ctx, pool, productapp.NewService(unit, repo, events), productapp.NewServicePeriodService(unit, repo, events), productapp.NewLocalProductLifecycleService(unit, repo, events)
}

func TestServicePeriodImagesPostgreSQLClearPreserveReplay(t *testing.T) {
	ctx, pool, _, period, _ := imageMutationServices(t)
	p, err := period.CreateServicePeriodProduct(ctx, productport.CreateServicePeriodProductCommand{ProductCode: "period-images", Name: "图片清空", Currency: "CNY", DurationDays: 30, Images: []string{"/cover.png"}, Actor: 1, IdempotencyKey: "period-images-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	update := productport.UpdateServicePeriodProductCommand{ID: p.ServiceProductID, ExpectedVersion: p.Version, Name: p.Name, Currency: p.Currency, DurationDays: p.DurationDays, Actor: 1, IdempotencyKey: "period-images-omit-0001"}
	// An old caller omitting images must preserve the current cover.
	kept, err := period.UpdateServicePeriodProduct(ctx, update)
	if err != nil || len(kept.Images) != 1 {
		t.Fatalf("omitted images=%v err=%v", kept.Images, err)
	}
	update.ExpectedVersion = kept.Version
	update.IdempotencyKey = "period-images-clear-0001"
	update.Images = []string{}
	cleared, err := period.UpdateServicePeriodProduct(ctx, update)
	if err != nil || len(cleared.Images) != 0 {
		t.Fatalf("explicit [] did not clear cover: images=%v err=%v", cleared.Images, err)
	}
	replay, err := period.UpdateServicePeriodProduct(ctx, update)
	if err != nil || replay.Version != cleared.Version || len(replay.Images) != 0 {
		t.Fatalf("clear replay=%+v err=%v", replay, err)
	}
	// The same key with omitted images expresses a different command.
	update.Images = nil
	if _, err = period.UpdateServicePeriodProduct(ctx, update); err != productapp.ErrConflict {
		t.Fatalf("clear/omit key drift: %v", err)
	}
	var images json.RawMessage
	var version, receipts, outbox int64
	if err = pool.QueryRow(ctx, `SELECT images,version FROM products WHERE id=$1`, p.ServiceProductID).Scan(&images, &version); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_operation_receipts),(SELECT count(*) FROM outbox_events)`).Scan(&receipts, &outbox); err != nil {
		t.Fatal(err)
	}
	if string(images) != "[]" || version != 3 || receipts != 3 || outbox != 3 {
		t.Fatalf("images=%s version=%d receipts=%d outbox=%d", images, version, receipts, outbox)
	}
}

func TestProductImagesPostgreSQLCopyWithoutCover(t *testing.T) {
	for _, kind := range []string{"standard", "service_period"} {
		t.Run(kind, func(t *testing.T) {
			ctx, pool, standard, period, lifecycle := imageMutationServices(t)
			var copiedID productport.ID
			if kind == "standard" {
				p, err := standard.Create(ctx, productport.CreateCommand{ProductCode: "empty-standard", Name: "无图商品", Currency: "CNY", Images: []string{}, LegacyAdminProjection: productapp.DefaultLegacyAdminProjection(), Actor: 1, IdempotencyKey: "empty-standard-create-0001"})
				if err != nil {
					t.Fatal(err)
				}
				cmd := productport.CopyLocalProductCommand{ID: p.ID, ExpectedVersion: p.Version, Actor: 1, IdempotencyKey: "empty-standard-copy-0001"}
				copied, err := lifecycle.CopyLocalProduct(ctx, cmd)
				if err != nil {
					t.Fatalf("copy zero-image standard: %v", err)
				}
				copiedID = copied.ID
				replay, err := lifecycle.CopyLocalProduct(ctx, cmd)
				if err != nil || replay.ID != copiedID {
					t.Fatalf("copy replay=%+v err=%v", replay, err)
				}
			} else {
				p, err := period.CreateServicePeriodProduct(ctx, productport.CreateServicePeriodProductCommand{ProductCode: "empty-period", Name: "无图服务", Currency: "CNY", DurationDays: 30, Images: []string{}, Actor: 1, IdempotencyKey: "empty-period-create-0001"})
				if err != nil {
					t.Fatal(err)
				}
				cmd := productport.CopyServicePeriodProductCommand{ID: p.ServiceProductID, ExpectedVersion: p.Version, Actor: 1, IdempotencyKey: "empty-period-copy-0001"}
				copied, err := period.CopyServicePeriodProduct(ctx, cmd)
				if err != nil {
					t.Fatalf("copy zero-image period: %v", err)
				}
				copiedID = copied.ServiceProductID
				replay, err := period.CopyServicePeriodProduct(ctx, cmd)
				if err != nil || replay.ServiceProductID != copiedID {
					t.Fatalf("copy replay=%+v err=%v", replay, err)
				}
			}
			var images, status string
			var count, receipts, outbox int64
			if err := pool.QueryRow(ctx, `SELECT images::text,legacy_admin_projection->>'status' FROM products WHERE id=$1`, copiedID).Scan(&images, &status); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM products),(SELECT count(*) FROM product_operation_receipts),(SELECT count(*) FROM outbox_events)`).Scan(&count, &receipts, &outbox); err != nil {
				t.Fatal(err)
			}
			if images != "[]" || count != 2 || receipts != 2 || outbox != 2 || (status != "draft" && status != "service_period_draft") {
				t.Fatalf("images=%s status=%s count=%d receipts=%d outbox=%d", images, status, count, receipts, outbox)
			}
		})
	}
}
