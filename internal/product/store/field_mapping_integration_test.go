package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// Reproduces an enabled product with no images against PostgreSQL constraints.
// Both consecutive saves must commit without changing its lifecycle or duration.
func TestFieldMappingPostgreSQLSaveReadRollback(t *testing.T) {
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
	schema := "aicrm_mapping_test_" + hex.EncodeToString(raw)
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
	sql, err := os.ReadFile(memberGridMigration(t, "0095_product_external_push.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, strings.Split(string(sql), "-- Old 0010")[0]); err != nil {
		t.Fatal(err)
	}
	sql, err = os.ReadFile(memberGridMigration(t, "0146_product_external_push_field_mapping.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = native.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('mapping','Mapping',990,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
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
	mapping, err := productport.DecodeFieldMapping(json.RawMessage(`{"version":1,"fields":[{"key":"precise","source":"fixed","value_type":"number","value":9007199254740993}]}`))
	if err != nil {
		t.Fatal(err)
	}
	value := productport.ExternalPushConfiguration{ProductID: productport.ID(id), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "mapping-config", FieldMapping: mapping}
	err = unit.Within(ctx, func(tx context.Context) error {
		var e error
		value, e = repo.SaveCommerceExternalPushConfiguration(tx, value, time.Now().UTC())
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("forced rollback")
	err = unit.Within(ctx, func(tx context.Context) error {
		cleared := value
		cleared.FieldMapping = nil
		if _, e := repo.SaveCommerceExternalPushConfiguration(tx, cleared, time.Now().UTC()); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		for _, read := range []func(context.Context) (productport.ExternalPushConfiguration, error){func(c context.Context) (productport.ExternalPushConfiguration, error) {
			return repo.ReadCommerceExternalPushConfiguration(c, productport.ID(id), productport.ExternalPushWeChatPay)
		}, func(c context.Context) (productport.ExternalPushConfiguration, error) {
			return repo.ReadCommerceExternalPushConfigurationForOrder(c, productport.ID(id))
		}} {
			got, e := read(tx)
			if e != nil {
				return e
			}
			body, e := productport.CompileFieldMapping(got.FieldMapping, nil)
			if e != nil {
				return e
			}
			if string(body) != `{"precise":9007199254740993}` || got.Revision != 1 {
				t.Fatalf("stored mapping %s revision %d", body, got.Revision)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
