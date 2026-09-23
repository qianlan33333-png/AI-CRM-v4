package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// OneID decision: not involved. These are local administrator accounts, not
// customer or channel identities. Persistence decision: local Access UOW; the
// assertions below prove role, session fencing, receipt, and audit consistency
// against a temporary PostgreSQL schema.
func TestPostgreSQLGovernanceRoleMatrixAndProvisioning(t *testing.T) {
	if environmentValue("AICRM_DATABASE_URL") == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping governance PostgreSQL integration test")
	}

	t.Run("super admin and administrator provisioning limits", func(t *testing.T) {
		fixture := newGovernanceBehaviorFixture(t)
		admin := fixture.principal(fixture.admin)
		viewer := fixture.principal(fixture.viewer)

		if _, err := fixture.management.ProvisionEnterpriseEmployee(fixture.ctx, admin, accessapp.ProvisionEnterpriseEmployeeInput{
			WeComUserID: "new-viewer", Role: domain.RoleViewer, IdempotencyKey: "admin-provision-viewer",
		}); err != nil {
			t.Fatalf("administrator could not provision viewer: %v", err)
		}
		if _, err := fixture.management.ProvisionEnterpriseEmployee(fixture.ctx, admin, accessapp.ProvisionEnterpriseEmployeeInput{
			WeComUserID: "new-admin", Role: domain.RoleAdmin, IdempotencyKey: "admin-provision-admin",
		}); !errors.Is(err, domain.ErrPermissionDenied) {
			t.Fatalf("administrator appointed admin: %v", err)
		}
		if _, err := fixture.management.ProvisionEnterpriseEmployee(fixture.ctx, viewer, accessapp.ProvisionEnterpriseEmployeeInput{
			WeComUserID: "new-viewer-2", Role: domain.RoleViewer, IdempotencyKey: "viewer-provision-viewer",
		}); !errors.Is(err, domain.ErrPermissionDenied) {
			t.Fatalf("viewer provisioned account: %v", err)
		}
		if _, err := fixture.management.ProvisionEnterpriseEmployee(fixture.ctx, fixture.principal(fixture.owner), accessapp.ProvisionEnterpriseEmployeeInput{
			WeComUserID: "new-admin", Role: domain.RoleAdmin, IdempotencyKey: "super-provision-admin",
		}); err != nil {
			t.Fatalf("super admin could not provision administrator: %v", err)
		}
	})

	for _, actorRole := range []domain.Role{domain.RoleSuperAdmin, domain.RoleAdmin, domain.RoleViewer} {
		for _, targetRole := range []domain.Role{domain.RoleSuperAdmin, domain.RoleAdmin, domain.RoleViewer} {
			actorRole, targetRole := actorRole, targetRole
			t.Run(fmt.Sprintf("actor=%s/target=%s", actorRole, targetRole), func(t *testing.T) {
				fixture := newGovernanceBehaviorFixture(t)
				actor := fixture.userForRole(actorRole)
				target := fixture.owner
				if targetRole != domain.RoleSuperAdmin {
					target = fixture.createUser(t, "matrix-"+string(actorRole)+"-target-"+string(targetRole), targetRole)
				}
				principal := fixture.principal(actor)
				allowsManage := actorRole == domain.RoleSuperAdmin && targetRole != domain.RoleSuperAdmin
				allowsLogin := allowsManage || (actorRole == domain.RoleAdmin && targetRole == domain.RoleViewer)

				before := fixture.user(t, target.ID)
				err := fixture.management.SetGovernanceLoginEnabled(fixture.ctx, principal, accessapp.SetLoginEnabledInput{
					TargetID: target.ID, LoginEnabled: !before.LoginEnabled, IdempotencyKey: "matrix-login-" + string(actorRole) + "-" + string(targetRole),
				})
				fixture.assertMutation(t, "login", err, allowsLogin, before, target.ID, func(after domain.User) bool {
					return after.Active == before.Active && after.LoginEnabled == !before.LoginEnabled && after.SessionVersion == before.SessionVersion+1
				})

				before = fixture.user(t, target.ID)
				err = fixture.management.ResetGovernancePassword(fixture.ctx, principal, accessapp.ResetPasswordInput{
					TargetID: target.ID, Password: "matrix-password-123", IdempotencyKey: "matrix-password-" + string(actorRole) + "-" + string(targetRole),
				})
				fixture.assertMutation(t, "password", err, allowsManage, before, target.ID, func(after domain.User) bool {
					return after.PasswordHash != before.PasswordHash && after.SessionVersion == before.SessionVersion+1
				})

				before = fixture.user(t, target.ID)
				wecomID := "binding-" + string(actorRole) + "-" + string(targetRole)
				fixture.directory.put(wecomID, "Binding "+string(actorRole)+" "+string(targetRole))
				err = fixture.management.BindGovernanceWeComUserID(fixture.ctx, principal, accessapp.BindEnterpriseEmployeeInput{
					TargetID: target.ID, WeComUserID: wecomID, IdempotencyKey: "matrix-binding-" + string(actorRole) + "-" + string(targetRole),
				})
				fixture.assertMutation(t, "binding", err, allowsManage, before, target.ID, func(after domain.User) bool {
					return after.WeComUserID == wecomID && after.SessionVersion == before.SessionVersion+1
				})

				before = fixture.user(t, target.ID)
				wanted := domain.RoleViewer
				if targetRole == domain.RoleViewer {
					wanted = domain.RoleAdmin
				}
				err = fixture.management.SetGovernanceRole(fixture.ctx, principal, accessapp.SetRoleInput{
					TargetID: target.ID, Role: wanted, IdempotencyKey: "matrix-role-" + string(actorRole) + "-" + string(targetRole),
				})
				fixture.assertMutation(t, "role", err, allowsManage, before, target.ID, func(after domain.User) bool {
					role, roleErr := domain.SingleRole(after.Roles)
					return roleErr == nil && role == wanted && after.SessionVersion == before.SessionVersion+1
				})
			})
		}
	}
}

func TestPostgreSQLGovernanceHTTPRoutesUseRealManagement(t *testing.T) {
	if environmentValue("AICRM_DATABASE_URL") == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping governance PostgreSQL integration test")
	}
	fixture := newGovernanceBehaviorFixture(t)
	handler, err := accesshttp.NewHandler(accesshttp.Config{
		Renderer: governanceTestRenderer{}, Auth: fixture.authentication, Management: fixture.management, CookieSecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()
	adminSession := fixture.login(t, "admin")

	// The frozen POST provision route cannot let an administrator appoint another
	// administrator, and the canonical role route refuses a super_admin payload.
	for _, test := range []struct {
		name    string
		request *http.Request
		status  int
	}{
		{"old provision cannot appoint admin", fixture.httpRequest(t, http.MethodPost, "/api/admin/access/users", adminSession, "old-provision-admin", map[string]any{"wecom_userid": "new-admin", "role": "admin"}), http.StatusForbidden},
		{"canonical route cannot self elevate", fixture.httpRequest(t, http.MethodPut, fmt.Sprintf("/api/admin/access/users/%d/role", fixture.admin.ID), adminSession, "new-self-elevate", map[string]any{"role": "super_admin"}), http.StatusBadRequest},
		{"old route cannot self elevate", fixture.httpRequest(t, http.MethodPost, fmt.Sprintf("/api/admin/access/users/%d/roles", fixture.admin.ID), adminSession, "old-self-elevate", map[string]any{"roles": []string{"super_admin"}}), http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, test.request)
		if response.Code != test.status {
			t.Fatalf("%s status=%d want=%d body=%s", test.name, response.Code, test.status, response.Body.String())
		}
	}

	viewerA := fixture.viewer
	viewerB := fixture.createUser(t, "http-viewer-b", domain.RoleViewer)
	for _, request := range []*http.Request{
		fixture.httpRequest(t, http.MethodPost, fmt.Sprintf("/api/admin/access/users/%d/disable", viewerA.ID), adminSession, "old-disable-viewer", map[string]any{}),
		fixture.httpRequest(t, http.MethodPut, fmt.Sprintf("/api/admin/access/users/%d/login-access", viewerB.ID), adminSession, "new-disable-viewer", map[string]any{"login_enabled": false}),
	} {
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("real management route status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if !fixture.user(t, viewerA.ID).Active || !fixture.user(t, viewerB.ID).Active || fixture.user(t, viewerA.ID).LoginEnabled || fixture.user(t, viewerB.ID).LoginEnabled {
		t.Fatal("old or canonical route did not persist viewer disable through real Management")
	}
}

func TestPostgreSQLUnprovisionedStaffProjectionCannotUseAnyGovernanceMutation(t *testing.T) {
	if environmentValue("AICRM_DATABASE_URL") == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping governance PostgreSQL integration test")
	}
	fixture := newGovernanceBehaviorFixture(t)
	fixture.directory.put("projected-only", "企业员工甲")
	var projected domain.User
	if err := fixture.unit.Within(fixture.ctx, func(ctx context.Context) error {
		var createErr error
		projected, createErr = fixture.repository.CreateStaffProjection(ctx, domain.User{
			Username: "wecom-staff-projected-only", PasswordHash: fixture.owner.PasswordHash,
			DisplayName: "企微客服 projected-only", WeComUserID: "projected-only", Active: true,
			Roles: []domain.Role{domain.RoleViewer},
		})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	if projected.AccessGrantedAt != nil || projected.LoginEnabled {
		t.Fatalf("projection unexpectedly received a login grant: %+v", projected)
	}

	routes := mustGovernanceHTTPRoutes(t, fixture)
	ownerSession := fixture.login(t, "owner")
	for _, request := range []*http.Request{
		fixture.httpRequest(t, http.MethodPut, fmt.Sprintf("/api/admin/access/users/%d/login-access", projected.ID), ownerSession, "projection-new-login", map[string]any{"login_enabled": true}),
		fixture.httpRequest(t, http.MethodPost, fmt.Sprintf("/api/admin/access/users/%d/disable", projected.ID), ownerSession, "projection-old-disable", map[string]any{}),
		fixture.httpRequest(t, http.MethodPut, fmt.Sprintf("/api/admin/access/users/%d/role", projected.ID), ownerSession, "projection-new-role", map[string]any{"role": "admin"}),
		fixture.httpRequest(t, http.MethodPost, fmt.Sprintf("/api/admin/access/users/%d/roles", projected.ID), ownerSession, "projection-old-role", map[string]any{"roles": []string{"admin"}}),
		fixture.httpRequest(t, http.MethodPut, fmt.Sprintf("/api/admin/access/users/%d/wecom-userid", projected.ID), ownerSession, "projection-new-bind", map[string]any{"wecom_userid": "projected-only"}),
		fixture.httpRequest(t, http.MethodPost, fmt.Sprintf("/api/admin/access/users/%d/wecom-userid", projected.ID), ownerSession, "projection-old-bind", map[string]any{"wecom_userid": "projected-only"}),
		fixture.httpRequest(t, http.MethodPut, fmt.Sprintf("/api/admin/access/users/%d/password", projected.ID), ownerSession, "projection-new-password", map[string]any{"password": "projected-password-123"}),
		fixture.httpRequest(t, http.MethodPost, fmt.Sprintf("/api/admin/access/users/%d/password", projected.ID), ownerSession, "projection-old-password", map[string]any{"password": "projected-password-123"}),
	} {
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unprovisioned route %s %s status=%d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
	beforeGrant := fixture.user(t, projected.ID)
	if beforeGrant.AccessGrantedAt != nil || beforeGrant.LoginEnabled || beforeGrant.DisplayName != "企微客服 projected-only" {
		t.Fatalf("rejected governance mutations changed staff projection: %+v", beforeGrant)
	}
	granted, err := fixture.management.ProvisionEnterpriseEmployee(fixture.ctx, fixture.principal(fixture.owner), accessapp.ProvisionEnterpriseEmployeeInput{
		WeComUserID: "projected-only", Role: domain.RoleViewer, IdempotencyKey: "projection-first-grant",
	})
	if err != nil {
		t.Fatalf("provider-verified first grant failed: %v", err)
	}
	if granted.ID != projected.ID || granted.AccessGrantedAt == nil || !granted.LoginEnabled || granted.DisplayName != "企业员工甲" {
		t.Fatalf("first grant did not preserve ID and refresh verified display name: %+v", granted)
	}
	if role, roleErr := domain.SingleRole(granted.Roles); roleErr != nil || role != domain.RoleViewer {
		t.Fatalf("first grant role=%q err=%v", role, roleErr)
	}
}

func mustGovernanceHTTPRoutes(t *testing.T, fixture *governanceBehaviorFixture) http.Handler {
	t.Helper()
	handler, err := accesshttp.NewHandler(accesshttp.Config{
		Renderer: governanceTestRenderer{}, Auth: fixture.authentication, Management: fixture.management, CookieSecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}

func TestPostgreSQLTransferGovernanceSuperAdminFencesSessionsAndIsIdempotent(t *testing.T) {
	if environmentValue("AICRM_DATABASE_URL") == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping governance PostgreSQL integration test")
	}
	fixture := newGovernanceBehaviorFixture(t)
	target := fixture.admin
	ownerSession := fixture.login(t, "owner")
	targetSession := fixture.login(t, "admin")
	ownerPrincipal, err := fixture.authentication.Authenticate(fixture.ctx, ownerSession.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	beforeOwner, beforeTarget := fixture.user(t, fixture.owner.ID), fixture.user(t, target.ID)

	if err = fixture.management.TransferGovernanceSuperAdmin(fixture.ctx, ownerPrincipal, accessapp.TransferSuperAdminInput{TargetID: target.ID, IdempotencyKey: "transfer-one"}); err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	if _, err = fixture.authentication.Authenticate(fixture.ctx, ownerSession.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("former super admin old session remained valid: %v", err)
	}
	if _, err = fixture.authentication.Authenticate(fixture.ctx, targetSession.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("new super admin old session remained valid: %v", err)
	}
	fixture.assertOneSuper(t, target.ID)
	afterOwner, afterTarget := fixture.user(t, fixture.owner.ID), fixture.user(t, target.ID)
	if role, _ := domain.SingleRole(afterOwner.Roles); role != domain.RoleAdmin || afterOwner.SessionVersion != beforeOwner.SessionVersion+1 {
		t.Fatalf("former super state=%+v", afterOwner)
	}
	if role, _ := domain.SingleRole(afterTarget.Roles); role != domain.RoleSuperAdmin || afterTarget.SessionVersion != beforeTarget.SessionVersion+1 {
		t.Fatalf("new super state=%+v", afterTarget)
	}

	// A transport retry carries the old session version. It must neither repeat
	// the transfer nor mutate the receipt/control state.
	if err = fixture.management.TransferGovernanceSuperAdmin(fixture.ctx, ownerPrincipal, accessapp.TransferSuperAdminInput{TargetID: target.ID, IdempotencyKey: "transfer-one"}); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("replayed transfer error=%v", err)
	}
	fixture.assertOneSuper(t, target.ID)
	if fixture.user(t, fixture.owner.ID).SessionVersion != afterOwner.SessionVersion || fixture.user(t, target.ID).SessionVersion != afterTarget.SessionVersion {
		t.Fatal("replayed transfer changed either account")
	}
	var receipts int
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM admin_access_governance_receipts WHERE action='transfer_super_admin'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("transfer receipts=%d err=%v", receipts, err)
	}
}

func TestPostgreSQLTransferGovernanceSuperAdminConcurrentTargetsAndAuditRollback(t *testing.T) {
	if environmentValue("AICRM_DATABASE_URL") == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping governance PostgreSQL integration test")
	}
	t.Run("two targets allow exactly one winner", func(t *testing.T) {
		fixture := newGovernanceBehaviorFixture(t)
		other := fixture.createUser(t, "second-admin", domain.RoleAdmin)
		actor := fixture.principal(fixture.owner)
		start := make(chan struct{})
		result := make(chan error, 2)
		for index, target := range []domain.User{fixture.admin, other} {
			index, target := index, target
			go func() {
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				result <- fixture.management.TransferGovernanceSuperAdmin(ctx, actor, accessapp.TransferSuperAdminInput{TargetID: target.ID, IdempotencyKey: fmt.Sprintf("concurrent-transfer-%d", index)})
			}()
		}
		close(start)
		var winner int
		for range 2 {
			if err := <-result; err == nil {
				winner++
			} else if !errors.Is(err, domain.ErrAuthentication) {
				t.Fatalf("concurrent transfer error=%v", err)
			}
		}
		if winner != 1 {
			t.Fatalf("concurrent transfer winners=%d", winner)
		}
		var controlID int64
		if err := fixture.native.QueryRow(fixture.ctx, `SELECT admin_user_id FROM access_super_admin_control WHERE singleton=TRUE`).Scan(&controlID); err != nil {
			t.Fatal(err)
		}
		fixture.assertOneSuper(t, controlID)
	})

	t.Run("audit failure rolls back transfer state and receipt", func(t *testing.T) {
		fixture := newGovernanceBehaviorFixture(t)
		if _, err := fixture.native.Exec(fixture.ctx, `CREATE FUNCTION fail_transfer_audit() RETURNS trigger AS $$
			BEGIN
				IF NEW.action = 'transfer_super_admin' THEN RAISE EXCEPTION 'forced audit failure'; END IF;
				RETURN NEW;
			END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_transfer_audit BEFORE INSERT ON admin_access_audit FOR EACH ROW EXECUTE FUNCTION fail_transfer_audit();`); err != nil {
			t.Fatal(err)
		}
		beforeOwner, beforeTarget := fixture.user(t, fixture.owner.ID), fixture.user(t, fixture.admin.ID)
		err := fixture.management.TransferGovernanceSuperAdmin(fixture.ctx, fixture.principal(fixture.owner), accessapp.TransferSuperAdminInput{TargetID: fixture.admin.ID, IdempotencyKey: "audit-failure-transfer"})
		if err == nil {
			t.Fatal("forced audit failure committed transfer")
		}
		fixture.assertOneSuper(t, fixture.owner.ID)
		afterOwner, afterTarget := fixture.user(t, fixture.owner.ID), fixture.user(t, fixture.admin.ID)
		if afterOwner.SessionVersion != beforeOwner.SessionVersion || afterTarget.SessionVersion != beforeTarget.SessionVersion || !afterOwner.HasRole(domain.RoleSuperAdmin) || !afterTarget.HasRole(domain.RoleAdmin) {
			t.Fatalf("audit rollback states owner=%+v target=%+v", afterOwner, afterTarget)
		}
		var receipts int
		if err := fixture.native.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM admin_access_governance_receipts WHERE idempotency_key='audit-failure-transfer'`).Scan(&receipts); err != nil || receipts != 0 {
			t.Fatalf("rolled back receipt count=%d err=%v", receipts, err)
		}
	})
}

type governanceBehaviorFixture struct {
	t              *testing.T
	ctx            context.Context
	native         *pgxpool.Pool
	cleanup        func()
	repository     *accessstore.PostgreSQL
	unit           *platformpostgres.UnitOfWork
	management     *accessapp.Management
	authentication *accessapp.Authentication
	directory      *governanceTestDirectory
	owner          domain.User
	admin          domain.User
	viewer         domain.User
}

func newGovernanceBehaviorFixture(t *testing.T) *governanceBehaviorFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	native, cleanupSchema := governanceSchema(t, ctx, "0003_access.sql", "0027_admin_access_login_compat.sql", "0151_access_role_governance.sql", "0152_access_login_grants.sql")
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		cleanupSchema()
		cancel()
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		cleanupSchema()
		cancel()
		t.Fatal(err)
	}
	repository := accessstore.NewPostgreSQL()
	management, err := accessapp.NewManagement(repository, unit, credential.PasswordHasher{}, time.Now)
	if err != nil {
		cleanupSchema()
		cancel()
		t.Fatal(err)
	}
	if err = management.SetGovernanceSigningKey([]byte("0123456789abcdef0123456789abcdef")); err != nil {
		cleanupSchema()
		cancel()
		t.Fatal(err)
	}
	directory := newGovernanceTestDirectory()
	for _, id := range []string{"new-viewer", "new-viewer-2", "new-admin"} {
		directory.put(id, "Directory "+id)
	}
	if err = management.SetEnterpriseEmployeeDirectory(directory, []byte("abcdefghijklmnopqrstuvwxyz012345"), "fixture-corp"); err != nil {
		cleanupSchema()
		cancel()
		t.Fatal(err)
	}
	owner, created, err := management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "owner", Password: "governance-password", DisplayName: "Owner"})
	if err != nil || !created {
		cleanupSchema()
		cancel()
		t.Fatalf("bootstrap owner=%+v created=%t err=%v", owner, created, err)
	}
	authentication, err := accessapp.NewAuthentication(repository, unit, credential.PasswordHasher{}, accessapp.AuthenticationConfig{
		SessionTTL: time.Hour, Window: time.Minute, MaxFailures: 5, BlockFor: time.Minute, DummyPHCHash: owner.PasswordHash,
	})
	if err != nil {
		cleanupSchema()
		cancel()
		t.Fatal(err)
	}
	fixture := &governanceBehaviorFixture{t: t, ctx: ctx, native: native, cleanup: func() { cleanupSchema(); cancel() }, repository: repository, unit: unit, management: management, authentication: authentication, directory: directory, owner: owner}
	t.Cleanup(fixture.cleanup)
	fixture.admin = fixture.createUser(t, "admin", domain.RoleAdmin)
	fixture.viewer = fixture.createUser(t, "viewer", domain.RoleViewer)
	return fixture
}

func (fixture *governanceBehaviorFixture) createUser(t *testing.T, username string, role domain.Role) domain.User {
	t.Helper()
	var user domain.User
	err := fixture.unit.Within(fixture.ctx, func(ctx context.Context) error {
		var createErr error
		user, createErr = fixture.repository.CreateUser(ctx, domain.User{Username: username, PasswordHash: fixture.owner.PasswordHash, DisplayName: username, Active: true, Roles: []domain.Role{role}})
		return createErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func (fixture *governanceBehaviorFixture) user(t *testing.T, id int64) domain.User {
	t.Helper()
	var user domain.User
	if err := fixture.unit.Within(fixture.ctx, func(ctx context.Context) error {
		var err error
		user, err = fixture.repository.UserByID(ctx, id, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return user
}

func (fixture *governanceBehaviorFixture) userForRole(role domain.Role) domain.User {
	switch role {
	case domain.RoleSuperAdmin:
		return fixture.owner
	case domain.RoleAdmin:
		return fixture.admin
	default:
		return fixture.viewer
	}
}

func (fixture *governanceBehaviorFixture) principal(user domain.User) domain.Principal {
	return domain.Principal{Kind: domain.KindAdmin, InternalID: user.ID, Roles: user.Roles, SessionVersion: user.SessionVersion}
}

func (fixture *governanceBehaviorFixture) assertMutation(t *testing.T, name string, err error, allowed bool, before domain.User, targetID int64, changed func(domain.User) bool) {
	t.Helper()
	after := fixture.user(t, targetID)
	if allowed {
		if err != nil {
			t.Fatalf("%s unexpectedly rejected: %v", name, err)
		}
		if !changed(after) {
			t.Fatalf("%s did not persist expected mutation: %+v", name, after)
		}
		return
	}
	if !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("%s unexpectedly allowed or wrong error: %v", name, err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s rejected but still changed target before=%+v after=%+v", name, before, after)
	}
}

func (fixture *governanceBehaviorFixture) assertOneSuper(t *testing.T, expectedID int64) {
	t.Helper()
	var count, controlID int64
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM admin_user_roles WHERE role_code='super_admin'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT admin_user_id FROM access_super_admin_control WHERE singleton=TRUE`).Scan(&controlID); err != nil {
		t.Fatal(err)
	}
	if count != 1 || controlID != expectedID {
		t.Fatalf("super count=%d control=%d expected=%d", count, controlID, expectedID)
	}
}

func (fixture *governanceBehaviorFixture) login(t *testing.T, username string) accessapp.IssuedSession {
	t.Helper()
	issued, err := fixture.authentication.Login(fixture.ctx, accessapp.LoginCommand{Username: username, Password: "governance-password", Remote: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func (fixture *governanceBehaviorFixture) httpRequest(t *testing.T, method, path string, session accessapp.IssuedSession, key string, payload map[string]any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("X-CSRF-Token", session.CSRFToken)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session.SessionToken})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: session.CSRFToken})
	return request
}

type governanceTestDirectory struct {
	employees map[string]wecomport.EnterpriseEmployee
}

func newGovernanceTestDirectory() *governanceTestDirectory {
	return &governanceTestDirectory{employees: make(map[string]wecomport.EnterpriseEmployee)}
}

func (directory *governanceTestDirectory) put(id, name string) {
	directory.employees[id] = wecomport.EnterpriseEmployee{UserID: id, DisplayName: name}
}

func (*governanceTestDirectory) EnterpriseDirectoryReady() bool { return true }

func (directory *governanceTestDirectory) ListEnterpriseEmployees(context.Context) ([]wecomport.EnterpriseEmployee, error) {
	items := make([]wecomport.EnterpriseEmployee, 0, len(directory.employees))
	for _, employee := range directory.employees {
		items = append(items, employee)
	}
	return items, nil
}

func (directory *governanceTestDirectory) ReadEnterpriseEmployee(_ context.Context, id string) (wecomport.EnterpriseEmployee, error) {
	employee, ok := directory.employees[id]
	if !ok {
		return wecomport.EnterpriseEmployee{}, wecomport.ErrEnterpriseEmployeeNotFound
	}
	return employee, nil
}

type governanceTestRenderer struct{}

func (governanceTestRenderer) Render(context.Context, http.ResponseWriter, int, string, map[string]any) error {
	return nil
}
