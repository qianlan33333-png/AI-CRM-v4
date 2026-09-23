package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLAudienceRegistrationFactsUseDirectoryPhonePresence(t *testing.T) {
	databaseURL, configErr := platformconfig.DatabaseURL()
	if configErr != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping customer registration PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, cleanup := customerRegistrationPool(t, ctx, databaseURL)
	defer cleanup()
	native := pool.Native()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		id    int64
		phone string
	}{
		{1, "138****0001"},
		{2, ""},
	} {
		if _, err := native.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,phone_masked,source,updated_at) VALUES($1,'active',$2,'people.mobile',$3)`, row.id, row.phone, now); err != nil {
			t.Fatal(err)
		}
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var facts map[customerdomain.CustomerID]struct {
		known, registered bool
		source            string
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		out, readErr := NewPostgreSQL().AudienceRegistrationFacts(tx, []customerdomain.CustomerID{1, 2, 3, 1})
		if readErr != nil {
			return readErr
		}
		facts = map[customerdomain.CustomerID]struct {
			known, registered bool
			source            string
		}{}
		for id, fact := range out {
			facts[id] = struct {
				known, registered bool
				source            string
			}{fact.Known, fact.Registered, fact.Source}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := facts[1]; !got.known || !got.registered || got.source != "people.mobile" {
		t.Fatalf("phone directory fact=%+v", got)
	}
	if got := facts[2]; !got.known || got.registered || got.source != "people.mobile" {
		t.Fatalf("empty-phone directory fact=%+v", got)
	}
	if got, found := facts[3]; !found || got.known || got.registered || got.source != "" {
		t.Fatalf("missing directory fact found=%t fact=%+v", found, got)
	}
}

func customerRegistrationPool(t *testing.T, ctx context.Context, databaseURL string) (*platformpostgres.Pool, func()) {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_customer_registration_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0009_customer_activation.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			native.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(raw)); execErr != nil {
			native.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
