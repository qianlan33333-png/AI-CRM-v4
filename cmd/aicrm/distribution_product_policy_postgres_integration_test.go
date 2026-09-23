package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// This is composition-root coverage: the Product application writes Product's
// row and reaches Distribution only through its stable PolicyService Port in
// one PostgreSQL UoW. Keeping it here prevents Product Store tests from
// importing Distribution app/store packages.
func TestPostgreSQLDistributionProductPolicyCompositionUoW(t *testing.T) {
	native, cleanup := distributionProductPolicyPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	productRepo, err := productstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	distributionRepo, err := distributionstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	policies := distributionapp.NewPolicyService(distributionRepo)
	products := productapp.NewService(uow, productRepo, distributionProductPolicyEvents{})
	if err = products.SetDistributionPolicyWriter(policies); err != nil {
		t.Fatal(err)
	}
	projection := []byte(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`)
	base := productport.CreateCommand{ProductCode: "atomic-policy", Name: "Atomic policy", Currency: "CNY", StockQuantity: 1, PriceMinor: 100, Images: []string{}, LegacyAdminProjection: projection, Actor: 1, IdempotencyKey: "distribution-policy-create-0001", DistributionPolicy: &productport.DistributionPolicy{Enabled: true, CommissionRateBasisPoints: 1234, WaitDays: 8, ExpectedVersion: 0}}
	created, err := products.Create(ctx, base)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	policy, err := policies.ReadProductPolicy(ctx, int64(created.ID), distributiondomain.ProductTypeStandard)
	if err != nil || policy.Version != 1 || !policy.Enabled || policy.CommissionRateBasisPoints != 1234 || policy.WaitDays != 8 {
		t.Fatalf("created policy=%+v err=%v", policy, err)
	}

	oldUpdate := productport.UpdateCommand{ID: created.ID, ExpectedVersion: created.Version, Name: "Atomic policy v2", Currency: "CNY", StockQuantity: 1, PriceMinor: 100, LegacyAdminProjection: projection, Actor: 1, IdempotencyKey: "distribution-policy-old-update-0001"}
	updated, err := products.Update(ctx, oldUpdate)
	if err != nil {
		t.Fatalf("old update: %v", err)
	}
	policy, err = policies.ReadProductPolicy(ctx, int64(created.ID), distributiondomain.ProductTypeStandard)
	if err != nil || policy.Version != 1 || !policy.Enabled {
		t.Fatalf("old update rewrote policy=%+v err=%v", policy, err)
	}

	bad := oldUpdate
	bad.ExpectedVersion, bad.Name, bad.IdempotencyKey = updated.Version, "must roll back", "distribution-policy-conflict-0001"
	bad.DistributionPolicy = &productport.DistributionPolicy{Enabled: false, CommissionRateBasisPoints: 0, WaitDays: 7, ExpectedVersion: 99}
	if _, err = products.Update(ctx, bad); !errors.Is(err, productapp.ErrConflict) {
		t.Fatalf("policy conflict=%v", err)
	}
	var version int64
	var name string
	if err = native.QueryRow(ctx, `SELECT version,name FROM products WHERE id=$1`, created.ID).Scan(&version, &name); err != nil || version != updated.Version || name != updated.Name {
		t.Fatalf("rollback version=%d name=%q err=%v", version, name, err)
	}

	rollback := base
	rollback.ProductCode, rollback.IdempotencyKey = "rollback-policy", "distribution-policy-rollback-0001"
	rollback.DistributionPolicy = &productport.DistributionPolicy{Enabled: true, CommissionRateBasisPoints: 1, WaitDays: 7, ExpectedVersion: 0}
	rollbackProducts := productapp.NewService(uow, productRepo, distributionProductPolicyEvents{})
	if err = rollbackProducts.SetDistributionPolicyWriter(distributionProductPolicyWriteConflict{}); err != nil {
		t.Fatal(err)
	}
	if _, err = rollbackProducts.Create(ctx, rollback); !errors.Is(err, productapp.ErrConflict) {
		t.Fatalf("create conflict=%v", err)
	}
	var rows int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM products WHERE product_code='rollback-policy'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rolled-back product rows=%d err=%v", rows, err)
	}

	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			outcomes <- uow.Within(ctx, func(tx context.Context) error {
				_, writeErr := policies.SaveProductPolicyWithin(tx, distributionport.PolicyCommand{ProductID: int64(created.ID), ProductType: distributiondomain.ProductTypeStandard, Enabled: i == 0, CommissionRateBasisPoints: int32(i * 100), WaitDays: 7, ExpectedVersion: 1, ActorScope: "admin:1", IdempotencyKey: fmt.Sprintf("distribution-policy-race-%d", i)})
				return writeErr
			})
		}(i)
	}
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch outcome := <-outcomes; {
		case outcome == nil:
			successes++
		case errors.Is(outcome, distributionport.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent policy outcome=%v", outcome)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("race successes=%d conflicts=%d", successes, conflicts)
	}
	policy, err = policies.ReadProductPolicy(ctx, int64(created.ID), distributiondomain.ProductTypeStandard)
	if err != nil || policy.Version != 2 {
		t.Fatalf("race policy=%+v err=%v", policy, err)
	}
}

type distributionProductPolicyEvents struct{}

func (distributionProductPolicyEvents) Append(context.Context, productport.Event) (productport.EventID, error) {
	return 1, nil
}

type distributionProductPolicyWriteConflict struct{}

func (distributionProductPolicyWriteConflict) SaveProductPolicyWithin(context.Context, distributionport.PolicyCommand) (distributiondomain.Policy, error) {
	return distributiondomain.Policy{}, distributionport.ErrConflict
}

func (distributionProductPolicyWriteConflict) ReadProductPolicy(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error) {
	return distributiondomain.Policy{}, distributionport.ErrUnavailable
}

func (distributionProductPolicyWriteConflict) ReadProductPolicyWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error) {
	return distributiondomain.Policy{}, distributionport.ErrUnavailable
}

func distributionProductPolicyPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping distribution policy PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_distribution_policy_test_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		native.Close()
		admin.Close()
		t.Fatal("locate migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, name := range []string{"0003_access.sql", "0010_product.sql", "0157_distribution_core.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(body)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('distribution-policy-admin','$argon2id$test','Distribution Policy Admin')`); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
