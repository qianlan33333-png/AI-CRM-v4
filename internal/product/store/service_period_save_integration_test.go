package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// Reproduces an enabled product with no images against PostgreSQL constraints.
// Both consecutive saves must commit without changing its lifecycle or duration.
func TestServicePeriodEmptyImagesPostgreSQLSave(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping member-grid PostgreSQL integration test")
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
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_period_save_test_" + hex.EncodeToString(raw)
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
	cfg := adminConfig.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0003_access.sql", "0010_product.sql"} {
		sql, readErr := os.ReadFile(memberGridMigration(t, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('grid-admin','$argon2id$test','Grid Admin')`); err != nil {
		t.Fatal(err)
	}
	projection := json.RawMessage(`{"schema_version":1,"status":"service_period_enabled","enabled":true}`)
	var productID int64
	if err = native.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('grid-product','Grid',0,'CNY',0,1,$1) RETURNING id`, projection).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		t.Fatal(err)
	}
	defer wrapped.Close()
	unit, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := productstore.NewPostgreSQL(native, unit)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = native.Exec(ctx, `CREATE TABLE product_imported_service_period_definitions(product_id BIGINT PRIMARY KEY REFERENCES products(id),duration_days INTEGER NOT NULL CHECK(duration_days>0)); INSERT INTO product_imported_service_period_definitions VALUES(1,90)`); err != nil {
		t.Fatal(err)
	}
	service := productapp.NewServicePeriodService(unit, repo, periodSaveEvents{})
	for i := int64(1); i <= 2; i++ {
		saved, err := service.UpdateServicePeriodProduct(ctx, productport.UpdateServicePeriodProductCommand{ID: productport.ID(productID), ExpectedVersion: i, Name: "Grid", PriceMinor: 99900, Currency: "CNY", DurationDays: 90, Images: []string{}, Actor: 1, IdempotencyKey: fmt.Sprintf("period-save-empty-%d", i)})
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		if saved.Version != i+1 || saved.DurationDays != 90 || !saved.Enabled || len(saved.Images) != 0 {
			t.Fatalf("save result: %+v", saved)
		}
	}
	var shape string
	var version, receipts int64
	if err = native.QueryRow(ctx, `SELECT jsonb_typeof(images),version FROM products WHERE id=$1`, productID).Scan(&shape, &version); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM product_operation_receipts WHERE state='completed'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if shape != "array" || version != 3 || receipts != 2 {
		t.Fatalf("shape=%s version=%d receipts=%d", shape, version, receipts)
	}
}

type periodSaveEvents struct{}

func (periodSaveEvents) Append(context.Context, productport.Event) (productport.EventID, error) {
	return productport.EventID(1), nil
}
