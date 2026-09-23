package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// This is a PostgreSQL (not mocked) CRUD/CAS check for the three 0079 tables.
// It uses an isolated schema and skips only when no test database is supplied.
func TestMemberGridPostgreSQLCRUDAndCAS(t *testing.T) {
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
	schema := "aicrm_member_grid_test_" + hex.EncodeToString(raw)
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
	for _, name := range []string{"0003_access.sql", "0010_product.sql", "0079_service_period_member_grid.sql"} {
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
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	viewConfig := json.RawMessage(`{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"remaining_days","operator":"gte","value":7}]},"sorts":[{"field":"remaining_days","direction":"asc"}],"groups":[]}`)
	var view productport.MemberGridView
	err = unit.Within(ctx, func(tx context.Context) error {
		var e error
		view, e = repo.CreateMemberGridView(tx, productport.MemberGridView{ProductID: productport.ID(productID), Name: "保存视图", Config: viewConfig, CreatedBy: 1, UpdatedBy: 1, CreatedAt: now, UpdatedAt: now})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		var e error
		view, e = repo.UpdateMemberGridView(tx, productport.MemberGridView{ID: view.ID, ProductID: view.ProductID, Name: "保存视图二", Config: viewConfig, Version: view.Version, UpdatedBy: 1, UpdatedAt: now.Add(time.Second)})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.CreateMemberGridView(tx, productport.MemberGridView{ProductID: productport.ID(productID), Name: "保存视图二", Config: viewConfig, CreatedBy: 1, UpdatedBy: 1, CreatedAt: now, UpdatedAt: now})
		return e
	})
	if err == nil {
		t.Fatal("case-insensitive view name uniqueness unexpectedly accepted")
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.UpdateMemberGridView(tx, productport.MemberGridView{ID: view.ID, ProductID: view.ProductID, Name: "过期CAS", Config: viewConfig, Version: view.Version - 1, UpdatedBy: 1, UpdatedAt: now})
		return e
	})
	if err == nil {
		t.Fatal("stale view CAS unexpectedly succeeded")
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.DeleteMemberGridView(tx, view.ProductID, view.ID, view.Version)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	var collaborator productport.MemberGridCollaborator
	err = unit.Within(ctx, func(tx context.Context) error {
		var e error
		collaborator, e = repo.CreateMemberGridCollaborator(tx, productport.MemberGridCollaborator{ProductID: productport.ID(productID), AdminUserID: 1, Permission: "edit", CreatedBy: 1, UpdatedBy: 1, CreatedAt: now, UpdatedAt: now})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		var e error
		collaborator, e = repo.UpdateMemberGridCollaborator(tx, productport.MemberGridCollaborator{ID: collaborator.ID, ProductID: collaborator.ProductID, Permission: "read", Version: collaborator.Version, UpdatedBy: 1, UpdatedAt: now.Add(time.Second)})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.UpdateMemberGridCollaborator(tx, productport.MemberGridCollaborator{ID: collaborator.ID, ProductID: collaborator.ProductID, Permission: "edit", Version: collaborator.Version - 1, UpdatedBy: 1, UpdatedAt: now})
		return e
	})
	if err == nil {
		t.Fatal("stale collaborator CAS unexpectedly succeeded")
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.DeleteMemberGridCollaborator(tx, collaborator.ProductID, collaborator.ID, collaborator.Version)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.SetMemberGridShare(tx, productport.MemberGridShare{ProductID: productport.ID(productID), Enabled: true, PublicID: "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Generation: 1, CreatedBy: 1, UpdatedBy: 1, CreatedAt: now, UpdatedAt: now}, 0)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.SetMemberGridShare(tx, productport.MemberGridShare{ProductID: productport.ID(productID), Enabled: false, Generation: 1, CreatedBy: 1, UpdatedBy: 1, CreatedAt: now, UpdatedAt: now.Add(time.Second)}, 0)
		return e
	})
	if err == nil {
		t.Fatal("stale share CAS unexpectedly succeeded")
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		s, e := repo.GetMemberGridShareByToken(tx, "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		if e != nil {
			return e
		}
		if !s.Enabled || s.Version != 1 {
			return os.ErrInvalid
		}
		_, e = repo.SetMemberGridShare(tx, productport.MemberGridShare{ProductID: productport.ID(productID), Enabled: false, Generation: s.Generation, UpdatedBy: 1, CreatedBy: 1, CreatedAt: now, UpdatedAt: now.Add(time.Second)}, s.Version)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(tx context.Context) error {
		_, e := repo.GetMemberGridShareByToken(tx, "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		return e
	})
	if err == nil {
		t.Fatal("revoked share token remained readable")
	}
}

func memberGridMigration(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name)
}
