package main

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestConvergenceApplyReplayConcurrentAndRollbackPostgreSQL(t *testing.T) {
	pool, cleanup := convergencePool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	t.Run("apply then replay-check supports 0153 before 0151", func(t *testing.T) {
		seedConvergence(t, ctx, pool)
		result, err := apply(ctx, pool)
		if err != nil || result.ChangedAccounts != 2 {
			t.Fatalf("apply result=%+v err=%v", result, err)
		}
		assertRole(t, ctx, pool, "qianlan", "super_admin")
		assertRole(t, ctx, pool, "admin", "admin")
		assertRole(t, ctx, pool, "legacy", "admin")
		var revoked, audits int
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM admin_sessions WHERE revoked_at IS NOT NULL").Scan(&revoked); err != nil || revoked != 2 {
			t.Fatalf("revoked=%d err=%v", revoked, err)
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM admin_access_audit WHERE details @> '{\"convergence_id\":\"access-role-convergence-v1\",\"record_kind\":\"completion\"}'::jsonb").Scan(&audits); err != nil || audits != 1 {
			t.Fatalf("completion audits=%d err=%v", audits, err)
		}
		applyMigration(t, ctx, pool, "0151_access_role_governance.sql", "0151")
		applyMigration(t, ctx, pool, "0152_access_login_grants.sql", "0152")
		if result, err = replayCheck(ctx, readOnlyTx(t, ctx, pool)); err != nil || result.Status != "verified" {
			t.Fatalf("replay result=%+v err=%v", result, err)
		}
	})
}

func TestConvergenceConcurrentAndAuditFailureRollbackPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Run("concurrent apply commits once", func(t *testing.T) {
		pool, cleanup := convergencePool(t)
		defer cleanup()
		seedConvergence(t, ctx, pool)
		results := make(chan error, 2)
		go func() { _, err := apply(ctx, pool); results <- err }()
		go func() { _, err := apply(ctx, pool); results <- err }()
		first, second := <-results, <-results
		if (first == nil) == (second == nil) {
			t.Fatalf("concurrent results first=%v second=%v; want exactly one commit", first, second)
		}
		var markers int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_access_audit WHERE details @> '{\"convergence_id\":\"access-role-convergence-v1\",\"record_kind\":\"completion\"}'::jsonb").Scan(&markers); err != nil || markers != 1 {
			t.Fatalf("markers=%d err=%v", markers, err)
		}
	})
	t.Run("audit failure rolls role and session mutations back", func(t *testing.T) {
		pool, cleanup := convergencePool(t)
		defer cleanup()
		seedConvergence(t, ctx, pool)
		if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_convergence_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit blocked'; END $$; CREATE TRIGGER fail_convergence_audit BEFORE INSERT ON admin_access_audit FOR EACH ROW EXECUTE FUNCTION fail_convergence_audit()`); err != nil {
			t.Fatal(err)
		}
		if _, err := apply(ctx, pool); err == nil {
			t.Fatal("apply unexpectedly succeeded")
		}
		assertRole(t, ctx, pool, "admin", "super_admin")
		var revoked, marker int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_sessions WHERE revoked_at IS NOT NULL").Scan(&revoked); err != nil || revoked != 0 {
			t.Fatalf("revoked=%d err=%v", revoked, err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_access_audit").Scan(&marker); err != nil || marker != 0 {
			t.Fatalf("audits=%d err=%v", marker, err)
		}
	})
}

func TestConvergenceDryRunAndPreconditionsPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Run("dry-run validates without writes", func(t *testing.T) {
		pool, cleanup := convergencePool(t)
		defer cleanup()
		seedConvergence(t, ctx, pool)
		var rolesBefore, sessionsBefore, auditsBefore int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_user_roles").Scan(&rolesBefore); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_sessions WHERE revoked_at IS NOT NULL").Scan(&sessionsBefore); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_access_audit").Scan(&auditsBefore); err != nil {
			t.Fatal(err)
		}
		accounts, err := validateReadOnly(ctx, pool)
		if err != nil || changedCount(accounts) != 2 {
			t.Fatalf("dry-run accounts=%+v err=%v", accounts, err)
		}
		var rolesAfter, sessionsAfter, auditsAfter int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_user_roles").Scan(&rolesAfter); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_sessions WHERE revoked_at IS NOT NULL").Scan(&sessionsAfter); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_access_audit").Scan(&auditsAfter); err != nil {
			t.Fatal(err)
		}
		if rolesBefore != rolesAfter || sessionsBefore != sessionsAfter || auditsBefore != auditsAfter {
			t.Fatalf("dry-run wrote roles %d/%d sessions %d/%d audits %d/%d", rolesBefore, rolesAfter, sessionsBefore, sessionsAfter, auditsBefore, auditsAfter)
		}
	})
	t.Run("rejects an unexpected legacy super set without writes", func(t *testing.T) {
		pool, cleanup := convergencePool(t)
		defer cleanup()
		seedConvergence(t, ctx, pool)
		if _, err := pool.Exec(ctx, `UPDATE admin_user_roles SET role_code='admin' WHERE admin_user_id=(SELECT id FROM admin_users WHERE username='admin') AND role_code='super_admin'`); err != nil {
			t.Fatal(err)
		}
		if _, err := apply(ctx, pool); err == nil {
			t.Fatal("apply accepted an unexpected legacy super set")
		}
		assertRole(t, ctx, pool, "admin", "admin")
		var audits int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_access_audit").Scan(&audits); err != nil || audits != 0 {
			t.Fatalf("audits=%d err=%v", audits, err)
		}
	})
	t.Run("rejects qianlan binding with noncanonical case", func(t *testing.T) {
		pool, cleanup := convergencePool(t)
		defer cleanup()
		seedConvergence(t, ctx, pool)
		if _, err := pool.Exec(ctx, `UPDATE admin_users SET wecom_userid='qianlan' WHERE username='qianlan'`); err != nil {
			t.Fatal(err)
		}
		if _, err := validateReadOnly(ctx, pool); err == nil {
			t.Fatal("dry-run accepted a noncanonical qianlan binding")
		}
		var audits int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_access_audit").Scan(&audits); err != nil || audits != 0 {
			t.Fatalf("audits=%d err=%v", audits, err)
		}
	})
}

func TestConvergenceApplyRequiresExplicitApproval(t *testing.T) {
	t.Setenv("AICRM_ACCESS_CONVERGENCE_APPROVED", "")
	if err := requireApplyApproval("apply"); err == nil {
		t.Fatal("apply accepted a missing approval")
	}
	if err := requireApplyApproval("dry-run"); err != nil {
		t.Fatalf("dry-run approval check: %v", err)
	}
	t.Setenv("AICRM_ACCESS_CONVERGENCE_APPROVED", "1")
	if err := requireApplyApproval("apply"); err != nil {
		t.Fatalf("approved apply rejected: %v", err)
	}
}

func convergencePool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is required for PostgreSQL convergence integration")
	}
	base, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	var random [5]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "access_convergence_" + fmtHex(random[:])
	if _, err = base.Exec(context.Background(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		base.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize()
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	for _, file := range []string{"0003_access.sql", "0027_admin_access_login_compat.sql"} {
		contents, readErr := os.ReadFile(filepath.Join("..", "..", "migrations", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(context.Background(), string(contents)); err != nil {
			t.Fatalf("apply %s: %v", file, err)
		}
	}
	if _, err = pool.Exec(context.Background(), `CREATE TABLE wecom_customer_owner_observations(id bigint generated always as identity primary key)`); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0153_wecom_customer_detail_projection.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(contents)); err != nil {
		t.Fatalf("apply 0153: %v", err)
	}
	if _, err = pool.Exec(context.Background(), `CREATE TABLE platform_schema_migrations(version text PRIMARY KEY,name text NOT NULL UNIQUE,checksum bytea NOT NULL,applied_at timestamptz NOT NULL DEFAULT clock_timestamp()); INSERT INTO platform_schema_migrations(version,name,checksum) VALUES('0153','0153_wecom_customer_detail_projection.sql',decode(repeat('00',32),'hex'))`); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		base.Close()
	}
}

func seedConvergence(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var qianlan, admin, legacy int64
	for _, seed := range []struct {
		username, wecom string
		id              *int64
	}{{"qianlan", "QianLan", &qianlan}, {"admin", "", &admin}, {"legacy", "", &legacy}} {
		if err := pool.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES($1,'$argon2id$fixture','fixture',NULLIF($2,''),TRUE) RETURNING id`, seed.username, seed.wecom).Scan(seed.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		id   int64
		role string
	}{{qianlan, "super_admin"}, {admin, "super_admin"}, {legacy, "admin"}, {legacy, "viewer"}} {
		if _, err := pool.Exec(ctx, "INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES($1,$2)", item.id, item.role); err != nil {
			t.Fatal(err)
		}
	}
	// Distinct 32-byte digests keep the fixture constrained like production.
	for i, id := range []int64{admin, legacy} {
		if _, err := pool.Exec(ctx, `INSERT INTO admin_sessions(token_digest,csrf_token_digest,admin_user_id,session_version,expires_at) VALUES(decode(lpad(to_hex($1::int),64,'0'),'hex'),decode(lpad(to_hex($1::int+20),64,'0'),'hex'),$2,1,clock_timestamp()+interval '1 day')`, i+1, id); err != nil {
			t.Fatal(err)
		}
	}
}

func assertRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `SELECT r.role_code FROM admin_users u JOIN admin_user_roles r ON r.admin_user_id=u.id WHERE u.username=$1`, username).Scan(&got); err != nil || got != want {
		t.Fatalf("%s role=%q err=%v want=%q", username, got, err, want)
	}
}
func applyMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, file, version string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "migrations", file))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(contents)); err != nil {
		t.Fatalf("apply %s: %v", file, err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO platform_schema_migrations(version,name,checksum) VALUES($1,$2,decode(repeat('00',32),'hex'))", version, file); err != nil {
		t.Fatal(err)
	}
}
func readOnlyTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func validateReadOnly(ctx context.Context, pool *pgxpool.Pool) ([]account, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	return loadAndValidate(ctx, tx)
}
func fmtHex(value []byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for i, b := range value {
		result[i*2] = digits[b>>4]
		result[i*2+1] = digits[b&15]
	}
	return string(result)
}
