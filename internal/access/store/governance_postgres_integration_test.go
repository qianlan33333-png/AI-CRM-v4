package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLAccessGovernanceMigrationAndControl(t *testing.T) {
	databaseURL := environmentValue("AICRM_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping governance PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("zero role historical account fails closed", func(t *testing.T) {
		native, cleanup := governanceSchema(t, ctx, "0003_access.sql", "0027_admin_access_login_compat.sql")
		defer cleanup()
		if _, err := native.Exec(ctx, `INSERT INTO admin_users (username,password_hash,display_name,is_active) VALUES ('legacy-zero-role','$argon2id$fixture','Legacy',TRUE)`); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, governanceMigration(t)); err == nil {
			t.Fatal("0151 accepted initialized account without a role")
		}
	})

	t.Run("empty schema bootstraps one controlled super administrator", func(t *testing.T) {
		native, cleanup := governanceSchema(t, ctx, "0003_access.sql", "0027_admin_access_login_compat.sql", "0151_access_role_governance.sql", "0152_access_login_grants.sql")
		defer cleanup()
		pool, err := platformpostgres.Wrap(native, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		unit, err := platformpostgres.NewUnitOfWork(pool)
		if err != nil {
			t.Fatal(err)
		}
		repository := accessstore.NewPostgreSQL()
		management, err := accessapp.NewManagement(repository, unit, credential.PasswordHasher{}, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		if err = management.SetGovernanceSigningKey([]byte("0123456789abcdef0123456789abcdef")); err != nil {
			t.Fatal(err)
		}
		owner, created, err := management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "fixture-owner", Password: "fixture-owner-password", DisplayName: "Fixture Owner"})
		if err != nil || !created || !owner.Active || len(owner.Roles) != 1 || owner.Roles[0] != domain.RoleSuperAdmin {
			t.Fatalf("bootstrap owner=%+v created=%t err=%v", owner, created, err)
		}
		if err = unit.Within(ctx, func(tx context.Context) error {
			control, readErr := repository.SuperAdminControl(tx, true)
			if readErr != nil {
				return readErr
			}
			if control.AdminUserID != owner.ID {
				t.Fatalf("control owner=%d want=%d", control.AdminUserID, owner.ID)
			}
			_, createErr := repository.CreateUser(tx, domain.User{Username: "zero-role", PasswordHash: owner.PasswordHash, DisplayName: "Zero Role", Active: true})
			return createErr
		}); err == nil {
			t.Fatal("transaction committed a zero-role account after governance initialization")
		}
	})

	t.Run("concurrent direct role writes retain one super administrator", func(t *testing.T) {
		native, cleanup := governanceSchema(t, ctx, "0003_access.sql", "0027_admin_access_login_compat.sql", "0151_access_role_governance.sql", "0152_access_login_grants.sql")
		defer cleanup()
		pool, err := platformpostgres.Wrap(native, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		unit, err := platformpostgres.NewUnitOfWork(pool)
		if err != nil {
			t.Fatal(err)
		}
		repository := accessstore.NewPostgreSQL()
		management, err := accessapp.NewManagement(repository, unit, credential.PasswordHasher{}, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		if err = management.SetGovernanceSigningKey([]byte("0123456789abcdef0123456789abcdef")); err != nil {
			t.Fatal(err)
		}
		owner, _, err := management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "concurrent-owner", Password: "fixture-owner-password", DisplayName: "Concurrent Owner"})
		if err != nil {
			t.Fatal(err)
		}
		var first, second domain.User
		if err = unit.Within(ctx, func(tx context.Context) error {
			var createErr error
			first, createErr = repository.CreateUser(tx, domain.User{Username: "candidate-one", PasswordHash: owner.PasswordHash, DisplayName: "Candidate One", Active: true, Roles: []domain.Role{domain.RoleAdmin}})
			if createErr != nil {
				return createErr
			}
			second, createErr = repository.CreateUser(tx, domain.User{Username: "candidate-two", PasswordHash: owner.PasswordHash, DisplayName: "Candidate Two", Active: true, Roles: []domain.Role{domain.RoleAdmin}})
			return createErr
		}); err != nil {
			t.Fatal(err)
		}

		firstTx, err := native.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer firstTx.Rollback(context.Background())
		if _, err = firstTx.Exec(ctx, `DELETE FROM admin_user_roles WHERE admin_user_id IN ($1,$2)`, owner.ID, first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = firstTx.Exec(ctx, `INSERT INTO admin_user_roles (admin_user_id,role_code) VALUES ($1,'admin'),($2,'super_admin')`, owner.ID, first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = firstTx.Exec(ctx, `UPDATE access_super_admin_control SET admin_user_id=$1,version=version+1 WHERE singleton=TRUE`, first.ID); err != nil {
			t.Fatal(err)
		}

		secondDone := make(chan error, 1)
		go func() {
			tx, beginErr := native.Begin(context.Background())
			if beginErr != nil {
				secondDone <- beginErr
				return
			}
			defer tx.Rollback(context.Background())
			if _, beginErr = tx.Exec(context.Background(), `DELETE FROM admin_user_roles WHERE admin_user_id=$1`, second.ID); beginErr != nil {
				secondDone <- beginErr
				return
			}
			_, execErr := tx.Exec(context.Background(), `INSERT INTO admin_user_roles (admin_user_id,role_code) VALUES ($1,'super_admin')`, second.ID)
			secondDone <- execErr
		}()
		select {
		case err := <-secondDone:
			t.Fatalf("second super insert did not wait for concurrent unique check: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		if err = firstTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-secondDone; err == nil {
			t.Fatal("concurrent direct role write created a second super administrator")
		}
		var superCount int
		if err = native.QueryRow(ctx, `SELECT COUNT(*) FROM admin_user_roles WHERE role_code='super_admin'`).Scan(&superCount); err != nil || superCount != 1 {
			t.Fatalf("super count=%d err=%v", superCount, err)
		}
	})

	t.Run("0152 preserves and safely reactivates a historical disabled login", func(t *testing.T) {
		native, cleanup := governanceSchema(t, ctx, "0003_access.sql", "0027_admin_access_login_compat.sql", "0151_access_role_governance.sql")
		defer cleanup()
		passwords := credential.PasswordHasher{}
		ownerHash, err := passwords.Hash("fixture-owner-password")
		if err != nil {
			t.Fatal(err)
		}
		legacyHash, err := passwords.Hash("legacy-login-password")
		if err != nil {
			t.Fatal(err)
		}
		seed, err := native.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer seed.Rollback(ctx)
		var ownerID, legacyID int64
		if err = seed.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active)
			VALUES('migration-owner',$1,'Migration Owner',TRUE) RETURNING id`, ownerHash).Scan(&ownerID); err != nil {
			t.Fatal(err)
		}
		if err = seed.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active)
			VALUES('historical-disabled',$1,'Historical Disabled',FALSE) RETURNING id`, legacyHash).Scan(&legacyID); err != nil {
			t.Fatal(err)
		}
		if _, err = seed.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_code)
			VALUES($1,'super_admin'),($2,'viewer')`, ownerID, legacyID); err != nil {
			t.Fatal(err)
		}
		if _, err = seed.Exec(ctx, `INSERT INTO access_super_admin_control(singleton,admin_user_id,version,updated_at)
			VALUES(TRUE,$1,1,clock_timestamp())`, ownerID); err != nil {
			t.Fatal(err)
		}
		if err = seed.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, migrationSQL(t, "0152_access_login_grants.sql")); err != nil {
			t.Fatalf("apply 0152: %v", err)
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
		management, err := accessapp.NewManagement(repository, unit, passwords, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		if err = management.SetGovernanceSigningKey([]byte("0123456789abcdef0123456789abcdef")); err != nil {
			t.Fatal(err)
		}
		var owner, legacy domain.User
		if err = unit.Within(ctx, func(tx context.Context) error {
			var readErr error
			owner, readErr = repository.UserByID(tx, ownerID, false)
			if readErr != nil {
				return readErr
			}
			legacy, readErr = repository.UserByID(tx, legacyID, false)
			return readErr
		}); err != nil {
			t.Fatal(err)
		}
		if legacy.Active || legacy.LoginEnabled || legacy.AccessGrantedAt == nil || !legacy.LegacyLoginReactivationPending {
			t.Fatalf("unexpected migrated legacy state: %+v", legacy)
		}
		actor := domain.Principal{Kind: domain.KindAdmin, InternalID: owner.ID, Roles: owner.Roles, SessionVersion: owner.SessionVersion}
		if err = management.SetGovernanceLoginEnabled(ctx, actor, accessapp.SetLoginEnabledInput{TargetID: legacy.ID, LoginEnabled: true, IdempotencyKey: "legacy-enable-once"}); err != nil {
			t.Fatalf("enable legacy account: %v", err)
		}
		if err = unit.Within(ctx, func(tx context.Context) error {
			var readErr error
			legacy, readErr = repository.UserByID(tx, legacyID, false)
			return readErr
		}); err != nil {
			t.Fatal(err)
		}
		if !legacy.Active || !legacy.LoginEnabled || legacy.AccessGrantedAt == nil || legacy.LegacyLoginReactivationPending {
			t.Fatalf("legacy enable did not restore a usable account: %+v", legacy)
		}
		authentication, err := accessapp.NewAuthentication(repository, unit, passwords, accessapp.AuthenticationConfig{
			SessionTTL: time.Hour, Window: time.Minute, MaxFailures: 5, BlockFor: time.Minute, DummyPHCHash: ownerHash,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = authentication.Login(ctx, accessapp.LoginCommand{Username: "historical-disabled", Password: "legacy-login-password", Remote: "127.0.0.1"}); err != nil {
			t.Fatalf("reactivated historical account could not authenticate: %v", err)
		}
		if err = management.SetGovernanceLoginEnabled(ctx, actor, accessapp.SetLoginEnabledInput{TargetID: legacy.ID, LoginEnabled: false, IdempotencyKey: "legacy-disable-after-bridge"}); err != nil {
			t.Fatal(err)
		}
		if err = management.SetGovernanceLoginEnabled(ctx, actor, accessapp.SetLoginEnabledInput{TargetID: legacy.ID, LoginEnabled: true, IdempotencyKey: "legacy-enable-after-bridge"}); err != nil {
			t.Fatal(err)
		}
		if err = unit.Within(ctx, func(tx context.Context) error {
			var readErr error
			legacy, readErr = repository.UserByID(tx, legacyID, false)
			return readErr
		}); err != nil {
			t.Fatal(err)
		}
		if !legacy.Active || !legacy.LoginEnabled || legacy.LegacyLoginReactivationPending {
			t.Fatalf("post-bridge toggles changed employee availability: %+v", legacy)
		}
	})

	t.Run("0152 rejects a bare post-migration account insert without a grant", func(t *testing.T) {
		native, cleanup := governanceSchema(t, ctx, "0003_access.sql", "0027_admin_access_login_compat.sql", "0151_access_role_governance.sql")
		defer cleanup()
		if _, err := native.Exec(ctx, migrationSQL(t, "0152_access_login_grants.sql")); err != nil {
			t.Fatalf("apply 0152: %v", err)
		}
		if _, err := native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active)
			VALUES('bare-post-0152','$argon2id$fixture','Bare',TRUE)`); err == nil {
			t.Fatal("0152 accepted a bare account without explicit grant fields")
		}
	})
}

func governanceSchema(t *testing.T, ctx context.Context, names ...string) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL := environmentValue("AICRM_DATABASE_URL")
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "access_governance_" + hex.EncodeToString(raw)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET search_path TO `+pgx.Identifier{schema}.Sanitize())
		return err
	}
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err = native.Exec(ctx, migrationSQL(t, name)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return native, func() {
		native.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		admin.Close(cleanupCtx)
	}
}

func governanceMigration(t *testing.T) string {
	t.Helper()
	return migrationSQL(t, "0151_access_role_governance.sql")
}
func migrationSQL(t *testing.T, name string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("migration source path unavailable")
	}
	bytes, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}
