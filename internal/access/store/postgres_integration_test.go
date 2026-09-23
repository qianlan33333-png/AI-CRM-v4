package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLAccessIntegration(t *testing.T) {
	databaseURL := environmentValue("AICRM_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping access PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "access_test_" + hex.EncodeToString(random)
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET search_path TO `+pgx.Identifier{schema}.Sanitize())
		return err
	}
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	}()

	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, source, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "migrations", "0003_access.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	for _, migrationName := range []string{
		"0027_admin_access_login_compat.sql",
		"0151_access_role_governance.sql",
		"0152_access_login_grants.sql",
	} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "migrations", migrationName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", migrationName, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := accessstore.NewPostgreSQL()
	passwordHash, err := (credential.PasswordHasher{}).Hash("integration-password")
	if err != nil {
		t.Fatal(err)
	}
	input := domain.User{Username: "bootstrap", PasswordHash: passwordHash, DisplayName: "Bootstrap",
		Active: true, Roles: []domain.Role{domain.RoleSuperAdmin}}
	if _, err = repository.CreateUser(ctx, input); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("write without transaction error = %v", err)
	}
	var first domain.User
	if err = unit.Within(ctx, func(txContext context.Context) error {
		var createErr error
		var created bool
		first, created, createErr = repository.BootstrapUser(txContext, input)
		if createErr == nil && !created {
			t.Error("first bootstrap was not created")
		}
		if createErr == nil && created {
			createErr = repository.InitializeSuperAdminControl(txContext, first.ID, time.Now().UTC())
		}
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(txContext context.Context) error {
		again, created, bootstrapErr := repository.BootstrapUser(txContext, input)
		if bootstrapErr == nil && (created || again.ID != first.ID) {
			t.Errorf("idempotent bootstrap created=%v id=%d first=%d", created, again.ID, first.ID)
		}
		return bootstrapErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(txContext context.Context) error {
		if bindErr := repository.SetWeComUserID(txContext, first.ID, "Bootstrap_01", time.Now().UTC()); bindErr != nil {
			return bindErr
		}
		byWeCom, lookupErr := repository.UserByWeComUserID(txContext, "Bootstrap_01", true)
		if lookupErr != nil {
			return lookupErr
		}
		if byWeCom.ID != first.ID || byWeCom.SessionVersion != first.SessionVersion+1 {
			t.Errorf("WeCom lookup=%#v first=%#v", byWeCom, first)
		}
		users, listErr := repository.ListUsers(txContext)
		if listErr == nil && (len(users) != 1 || users[0].PasswordHash == "" || len(users[0].Roles) != 1) {
			t.Errorf("listed users=%#v", users)
		}
		return listErr
	}); err != nil {
		t.Fatal(err)
	}
	digest := [32]byte{1}
	if err = unit.Within(ctx, func(txContext context.Context) error {
		owned, reserveErr := repository.ReserveLoginAccessRequest(txContext, first.ID, "receipt-1", digest, time.Now().UTC())
		if reserveErr != nil || !owned {
			t.Errorf("first receipt owned=%v err=%v", owned, reserveErr)
		}
		return reserveErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(txContext context.Context) error {
		owned, reserveErr := repository.ReserveLoginAccessRequest(txContext, first.ID, "receipt-1", digest, time.Now().UTC())
		if reserveErr != nil || owned {
			t.Errorf("exact replay owned=%v err=%v", owned, reserveErr)
		}
		_, reserveErr = repository.ReserveLoginAccessRequest(txContext, first.ID, "receipt-1", [32]byte{2}, time.Now().UTC())
		if !errors.Is(reserveErr, domain.ErrConflict) {
			t.Errorf("payload drift error=%v", reserveErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback compatibility receipt")
	err = unit.Within(ctx, func(txContext context.Context) error {
		owned, reserveErr := repository.ReserveLoginAccessRequest(txContext, first.ID, "receipt-rollback", digest, time.Now().UTC())
		if reserveErr != nil || !owned {
			return errors.New("failed to reserve rollback receipt")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback err=%v", err)
	}
	if err = unit.Within(ctx, func(txContext context.Context) error {
		owned, reserveErr := repository.ReserveLoginAccessRequest(txContext, first.ID, "receipt-rollback", digest, time.Now().UTC())
		if reserveErr != nil || !owned {
			t.Errorf("rolled back receipt owned=%v err=%v", owned, reserveErr)
		}
		return reserveErr
	}); err != nil {
		t.Fatal(err)
	}
}

func environmentValue(key string) string {
	prefix := key + "="
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}
