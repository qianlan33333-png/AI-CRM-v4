package store_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
)

// Exercise the installed refresh route against the real Access transaction and
// audit constraint, including upgrading an existing database without data loss.
func TestPostgreSQLStaffNameRefreshAuditUpgradeAndAtomicity(t *testing.T) {
	if environmentValue("AICRM_DATABASE_URL") == "" {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	f := newGovernanceBehaviorFixture(t)
	if err := f.unit.Within(f.ctx, func(ctx context.Context) error {
		if err := f.repository.SetWeComUserID(ctx, f.admin.ID, "bound-admin", time.Now()); err != nil {
			return err
		}
		return f.repository.SetWeComUserID(ctx, f.viewer.ID, "bound-viewer", time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	f.directory.put("bound-admin", "管理员姓名")
	f.directory.put("bound-viewer", "员工姓名")
	f.directory.put("not-provisioned", "未开通员工")
	if err := f.management.SetGovernanceLoginEnabled(f.ctx, f.principal(f.owner), accessapp.SetLoginEnabledInput{
		TargetID: f.admin.ID, LoginEnabled: false, IdempotencyKey: "disable-refresh-target",
	}); err != nil {
		t.Fatal(err)
	}
	ownerSession := f.login(t, "owner")
	viewerSession := f.login(t, "viewer")
	before := staffRefreshUsers(t, f)
	beforeAudit := staffRefreshAuditCount(t, f)

	// This is the exact production defect: Provider read and name UPDATE work,
	// but the missing audit action causes the entire command to roll back.
	err := f.management.RefreshStaffNames(f.ctx, f.principal(f.owner))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "ck_admin_access_audit_action" {
		t.Fatalf("expected original audit constraint failure, got %v", err)
	}
	if !reflect.DeepEqual(before, staffRefreshUsers(t, f)) || beforeAudit != staffRefreshAuditCount(t, f) {
		t.Fatal("original constraint failure did not roll back all employee and audit changes")
	}
	t.Log("reproduced production audit constraint failure with complete rollback")

	if _, err := f.native.Exec(f.ctx, migrationSQL(t, "0238_staff_name_refresh_audit.sql")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, staffRefreshUsers(t, f)) || beforeAudit != staffRefreshAuditCount(t, f) {
		t.Fatal("migration changed existing employee or audit data")
	}
	handler, err := accesshttp.NewHandler(accesshttp.Config{
		Renderer: governanceTestRenderer{}, Auth: f.authentication, Management: f.management, CookieSecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()
	refresh := func(request *http.Request, want int) {
		t.Helper()
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("refresh status=%d want=%d body=%s", response.Code, want, response.Body.String())
		}
	}
	request := func() *http.Request {
		return f.httpRequest(t, http.MethodPost, "/api/admin/access/users/refresh-names", ownerSession, "refresh-staff-names", map[string]any{})
	}
	refresh(request(), http.StatusOK)
	after := staffRefreshUsers(t, f)
	if len(after) != len(before)+4 || staffRefreshAuditCount(t, f) != beforeAudit+2 {
		t.Fatal("refresh omitted discovered staff or per-employee audit")
	}
	for index, user := range after {
		if index >= len(before) {
			if user.AccessGrantedAt != nil || user.LoginEnabled || len(user.Roles) != 1 || user.Roles[0] != domain.RoleViewer {
				t.Fatal("discovery granted new staff backend access")
			}
			continue
		}
		switch user.WeComUserID {
		case "bound-admin":
			if user.DisplayName != "管理员姓名" {
				t.Fatal("administrator name was not refreshed")
			}
		case "bound-viewer":
			if user.DisplayName != "员工姓名" {
				t.Fatal("viewer name was not refreshed")
			}
		}
		user.DisplayName, user.UpdatedAt = before[index].DisplayName, before[index].UpdatedAt
		if !reflect.DeepEqual(before[index], user) {
			t.Fatal("refresh changed a role, login flag, session, binding, or another employee field")
		}
	}
	refresh(request(), http.StatusOK)
	if !reflect.DeepEqual(after, staffRefreshUsers(t, f)) || staffRefreshAuditCount(t, f) != beforeAudit+2 {
		t.Fatal("unchanged repeat refresh changed data or duplicated audit")
	}

	viewerRequest := f.httpRequest(t, http.MethodPost, "/api/admin/access/users/refresh-names", viewerSession, "viewer-refresh", map[string]any{})
	refresh(viewerRequest, http.StatusForbidden)
	missingCSRF := request()
	missingCSRF.Header.Del("X-CSRF-Token")
	refresh(missingCSRF, http.StatusForbidden)
	refresh(httptest.NewRequest(http.MethodPost, "/api/admin/access/users/refresh-names", nil), http.StatusForbidden)
	invalidSession := request()
	invalidSession.Header.Set("Cookie", accesshttp.SessionCookieName+"=not-issued; "+accesshttp.CSRFCookieName+"="+ownerSession.CSRFToken)
	refresh(invalidSession, http.StatusUnauthorized)
	if !reflect.DeepEqual(after, staffRefreshUsers(t, f)) || staffRefreshAuditCount(t, f) != beforeAudit+2 {
		t.Fatal("rejected refresh changed employee data or audit")
	}

	// Fail the later employee's audit so the first employee's already-written
	// name and audit must also roll back, rather than leaving a partial refresh.
	f.directory.put("bound-admin", "管理员新姓名")
	f.directory.put("bound-viewer", "员工新姓名")
	if _, err := f.native.Exec(f.ctx, fmt.Sprintf(`CREATE FUNCTION fail_refresh_audit() RETURNS trigger AS $$
		BEGIN
			IF NEW.action='refresh_staff_name' AND NEW.target_admin_user_id=%d THEN
				RAISE EXCEPTION 'forced refresh audit failure';
			END IF;
			RETURN NEW;
		END; $$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_refresh_audit BEFORE INSERT ON admin_access_audit
		FOR EACH ROW EXECUTE FUNCTION fail_refresh_audit();`, f.viewer.ID)); err != nil {
		t.Fatal(err)
	}
	refresh(request(), http.StatusInternalServerError)
	if !reflect.DeepEqual(after, staffRefreshUsers(t, f)) || staffRefreshAuditCount(t, f) != beforeAudit+2 {
		t.Fatal("audit failure committed a partial name refresh")
	}
}

func staffRefreshUsers(t *testing.T, f *governanceBehaviorFixture) []domain.User {
	t.Helper()
	var users []domain.User
	if err := f.unit.Within(f.ctx, func(ctx context.Context) error {
		var err error
		users, err = f.repository.ListUsers(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return users
}

func staffRefreshAuditCount(t *testing.T, f *governanceBehaviorFixture) int {
	t.Helper()
	var count int
	if err := f.native.QueryRow(f.ctx, `SELECT COUNT(*) FROM admin_access_audit`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
