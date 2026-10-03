package maintenance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

type fixture struct {
	ctx          context.Context
	native       *pgxpool.Pool
	unit         *platformpostgres.UnitOfWork
	repository   *accessstore.PostgreSQL
	auth         *accessapp.Authentication
	service      *Service
	owner        domain.User
	admin        domain.User
	adminSession string
	oldSession   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("AICRM_DATABASE_URL")
	if dsn == "" {
		t.Skip("AICRM_DATABASE_URL required for recovery PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	seed := make([]byte, 8)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	schema := "recovery_test_" + hex.EncodeToString(seed)
	root, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = root.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = root.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		_ = root.Close(context.Background())
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 4
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, `SET search_path TO `+pgx.Identifier{schema}.Sanitize())
		return err
	}
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.Close)
	_, source, _, _ := runtime.Caller(0)
	for _, name := range []string{"0003_access.sql", "0027_admin_access_login_compat.sql", "0151_access_role_governance.sql", "0152_access_login_grants.sql"} {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
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
	repo := accessstore.NewPostgreSQL()
	passwords := credential.PasswordHasher{}
	management, err := accessapp.NewManagement(repo, unit, passwords, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "recovery-owner", Password: "original-fixture-password", DisplayName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	var admin domain.User
	if err = unit.Within(ctx, func(ctx context.Context) error {
		var e error
		admin, e = repo.CreateUser(ctx, domain.User{Username: "recovery-admin", PasswordHash: owner.PasswordHash, DisplayName: "Admin", Active: true, Roles: []domain.Role{domain.RoleAdmin}})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	auth, err := accessapp.NewAuthentication(repo, unit, passwords, accessapp.AuthenticationConfig{DummyPHCHash: owner.PasswordHash})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := auth.Login(ctx, accessapp.LoginCommand{Username: admin.Username, Password: "original-fixture-password", Remote: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := auth.Login(ctx, accessapp.LoginCommand{Username: owner.Username, Password: "original-fixture-password", Remote: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(repo, unit, passwords, auth)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{ctx, native, unit, repo, auth, s, owner, admin, issued.SessionToken, old.SessionToken}
}
func (f *fixture) input() Input {
	return Input{Target: Target{f.owner.ID, f.owner.Username, f.owner.SessionVersion}, SessionToken: f.adminSession, Password: "replacement-fixture-password", AuthorizationReference: "fixture-owner-request"}
}
func (f *fixture) user(t *testing.T) domain.User {
	t.Helper()
	var user domain.User
	if err := f.unit.Within(f.ctx, func(ctx context.Context) error {
		var err error
		user, err = f.repository.UserByID(ctx, f.owner.ID, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return user
}
func (f *fixture) audits(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.native.QueryRow(f.ctx, `SELECT count(*) FROM admin_access_audit WHERE action='reset_password'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPostgreSQLRecoveryAuthorizationAndLoginJourney(t *testing.T) {
	f := newFixture(t)
	authority := Authority{root: true, operator: "fixture-ssh-operator"}
	before := f.user(t)
	for _, mutate := range []func(*Input, *Authority){
		func(i *Input, a *Authority) { *a = Authority{} },
		func(i *Input, a *Authority) { i.SessionToken = "forged" },
		func(i *Input, a *Authority) { i.Password = "short" },
		func(i *Input, a *Authority) { i.Target.ID = f.admin.ID },
		func(i *Input, a *Authority) { i.Target.Username = "other-owner" },
		func(i *Input, a *Authority) { i.Target.SessionVersion++ },
	} {
		input, a := f.input(), authority
		mutate(&input, &a)
		if _, err := f.service.Recover(f.ctx, a, input); err == nil {
			t.Fatal("invalid recovery accepted")
		}
		after := f.user(t)
		if after.SessionVersion != before.SessionVersion || after.PasswordHash != before.PasswordHash || f.audits(t) != 0 {
			t.Fatal("rejected recovery changed credentials or audit")
		}
	}
	if _, err := f.service.Inspect(f.ctx, Authority{}, f.owner.ID, f.owner.Username); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatal("unprivileged inspect accepted")
	}
	input := f.input()
	result, err := f.service.Recover(f.ctx, authority, input)
	if err != nil {
		t.Fatal(err)
	}
	after := f.user(t)
	if result.SessionVersion != before.SessionVersion+1 || after.SessionVersion != result.SessionVersion || !after.HasRole(domain.RoleSuperAdmin) || after.WeComUserID != before.WeComUserID || after.LoginEnabled != before.LoginEnabled || !(credential.PasswordHasher{}).Verify(input.Password, after.PasswordHash) {
		t.Fatal("recovery changed wrong state")
	}
	if _, err = f.auth.Authenticate(f.ctx, f.oldSession); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatal("old session accepted")
	}
	if _, err = f.auth.Login(f.ctx, accessapp.LoginCommand{Username: f.owner.Username, Password: "original-fixture-password", Remote: "old-check"}); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatal("old password accepted")
	}
	if _, err = f.auth.Login(f.ctx, accessapp.LoginCommand{Username: f.owner.Username, Password: input.Password, Remote: "new-check"}); err != nil {
		t.Fatal("new password login failed")
	}
	var actor int64
	var details []byte
	if err = f.native.QueryRow(f.ctx, `SELECT actor_admin_user_id,details FROM admin_access_audit WHERE action='reset_password'`).Scan(&actor, &details); err != nil {
		t.Fatal(err)
	}
	if actor != f.admin.ID || strings.Contains(string(details), input.Password) || strings.Contains(string(details), input.SessionToken) || strings.Contains(string(details), after.PasswordHash) {
		t.Fatal("audit misattributes identity or leaks secret")
	}
	var evidence map[string]any
	if json.Unmarshal(details, &evidence) != nil || evidence["authority"] != "linux_root" {
		t.Fatal("missing maintenance authority audit")
	}
	if _, err = f.service.Recover(f.ctx, authority, input); !errors.Is(err, domain.ErrConflict) || f.audits(t) != 1 {
		t.Fatal("same version caused repeated reset")
	}
}
func TestPostgreSQLRecoveryAuditFailureRollsBackCredentials(t *testing.T) {
	f := newFixture(t)
	before := f.user(t)
	if _, err := f.native.Exec(f.ctx, `CREATE FUNCTION fail_recovery_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='reset_password' THEN RAISE EXCEPTION 'forced audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_recovery_audit BEFORE INSERT ON admin_access_audit FOR EACH ROW EXECUTE FUNCTION fail_recovery_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Recover(f.ctx, Authority{root: true}, f.input()); err == nil {
		t.Fatal("audit failure committed")
	}
	after := f.user(t)
	if after.PasswordHash != before.PasswordHash || after.SessionVersion != before.SessionVersion || f.audits(t) != 0 {
		t.Fatal("credentials survived transaction rollback")
	}
	if _, err := f.auth.Authenticate(f.ctx, f.oldSession); err != nil {
		t.Fatal("rollback invalidated original session")
	}
}
func TestPostgreSQLRecoveryConcurrentVersionHasOneWinner(t *testing.T) {
	f := newFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := f.service.Recover(f.ctx, Authority{root: true}, f.input()); results <- err }()
	}
	close(start)
	winners := 0
	for range 2 {
		err := <-results
		if err == nil {
			winners++
		} else if !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 || f.audits(t) != 1 || f.user(t).SessionVersion != f.owner.SessionVersion+1 {
		t.Fatal("concurrent recovery repeated mutation")
	}
}

type revokeAfterAuth struct {
	auth   *accessapp.Authentication
	revoke func() error
}

func (a revokeAfterAuth) Authenticate(ctx context.Context, token string) (domain.Principal, error) {
	principal, err := a.auth.Authenticate(ctx, token)
	if err != nil {
		return principal, err
	}
	return principal, a.revoke()
}
func TestPostgreSQLRecoveryRechecksRevokedOperatorBeforeCommit(t *testing.T) {
	f := newFixture(t)
	before := f.user(t)
	auth := revokeAfterAuth{f.auth, func() error {
		return f.unit.Within(f.ctx, func(tx context.Context) error { return f.repository.SetLoginEnabled(tx, f.admin.ID, false, time.Now()) })
	}}
	s, err := New(f.repository, f.unit, credential.PasswordHasher{}, auth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Recover(f.ctx, Authority{root: true}, f.input()); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatal("revoked operator recovery accepted")
	}
	after := f.user(t)
	if before.PasswordHash != after.PasswordHash || before.SessionVersion != after.SessionVersion || f.audits(t) != 0 {
		t.Fatal("revoked operator changed target")
	}
}
