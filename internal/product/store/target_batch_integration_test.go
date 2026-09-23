package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// This is a real PostgreSQL check of the Product-owned typed batch query used
// by Coupon presentation. It runs in an isolated schema and never touches a
// shared Product table. Archived products remain a historical target fact;
// availability for a new target is enforced by ProductTargetReader instead.
func TestProductTargetBatchPostgreSQLRetainsArchivedHistoryAndTypeFacts(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Product target batch PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err = admin.Ping(ctx); err != nil {
		t.Fatalf("postgres readiness: %v", err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_product_target_batch_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	for _, name := range []string{"0003_access.sql", "0010_product.sql"} {
		migration, readErr := os.ReadFile(memberGridMigration(t, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('target-admin','$argon2id$test','Target Admin')`); err != nil {
		t.Fatal(err)
	}
	var standardID, periodID int64
	if err = native.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('target-standard','已归档普通商品',100,'CNY',0,1,'{"schema_version":1,"status":"archived","enabled":false}') RETURNING id`).Scan(&standardID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('target-period','已归档周期商品',100,'CNY',0,1,'{"schema_version":1,"status":"service_period_archived","enabled":false}') RETURNING id`).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := productapp.NewTargetBatchReader(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	lookups, err := reader.ReadProductTargets(ctx, []productport.ProductTargetReference{
		{ProductType: productport.ProductOptionStandard, ID: productport.ID(standardID)},
		{ProductType: productport.ProductOptionServicePeriod, ID: productport.ID(periodID)},
		{ProductType: productport.ProductOptionStandard, ID: 999999},
		{ProductType: productport.ProductOptionServicePeriod, ID: productport.ID(standardID)},
	})
	if err != nil || len(lookups) != 4 {
		t.Fatalf("lookups=%+v err=%v", lookups, err)
	}
	if !lookups[0].Found || lookups[0].Name != "已归档普通商品" || !lookups[1].Found || lookups[1].Name != "已归档周期商品" || lookups[2].Found || lookups[3].Found {
		t.Fatalf("typed Product projection=%+v", lookups)
	}
}
