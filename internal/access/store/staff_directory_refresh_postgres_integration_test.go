package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

// Real HTTP Provider responses flow through the WeCom adapter and the actual
// Access HTTP, PostgreSQL, platform audit, staff projection and grant boundaries.
func TestPostgreSQLStaffDirectoryRefreshDiscoveryVisibilityAndSafety(t *testing.T) {
	if environmentValue("AICRM_DATABASE_URL") == "" {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	f := newGovernanceBehaviorFixture(t)
	if _, err := f.native.Exec(f.ctx, migrationSQL(t, "0238_staff_name_refresh_audit.sql")); err != nil {
		t.Fatal(err)
	}
	var pending domain.User
	if err := f.unit.Within(f.ctx, func(ctx context.Context) error {
		if err := f.repository.SetWeComUserID(ctx, f.admin.ID, "bound-admin", time.Now()); err != nil {
			return err
		}
		if err := f.repository.SetWeComUserID(ctx, f.viewer.ID, "bound-viewer", time.Now()); err != nil {
			return err
		}
		var err error
		pending, err = f.repository.CreateStaffProjection(ctx, domain.User{Username: "pending", PasswordHash: f.owner.PasswordHash, DisplayName: "Old pending", WeComUserID: "pending-existing", Active: true, Roles: []domain.Role{domain.RoleViewer}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.management.SetGovernanceLoginEnabled(f.ctx, f.principal(f.owner), accessapp.SetLoginEnabledInput{TargetID: f.admin.ID, LoginEnabled: false, IdempotencyKey: "retain-disabled"}); err != nil {
		t.Fatal(err)
	}

	var mode atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		m := mode.Load()
		members := []map[string]string{{"userid": "bound-admin", "name": "Current admin"}, {"userid": "bound-viewer", "name": "Current viewer"}, {"userid": "pending-existing", "name": "Current pending"}, {"userid": "new-staff", "name": "New employee"}}
		if m == 1 {
			members = append(members, map[string]string{"userid": "race-new", "name": "Concurrent new"})
		}
		if m >= 2 {
			members[0]["name"] = "Changed admin"
			members[1]["name"] = "Changed viewer"
			members = append(members, map[string]string{"userid": "failure-new", "name": "Must roll back"})
		}
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "fixture", "expires_in": 7200})
		case "/cgi-bin/agent/get":
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "allow_userinfos": map[string]any{"user": []any{}}, "allow_partys": map[string]any{"partyid": []int{1, 2}}})
		case "/cgi-bin/department/simplelist":
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "department_id": []map[string]int{{"id": 1, "parentid": 0}, {"id": 2, "parentid": 0}}})
		case "/cgi-bin/user/simplelist":
			if m == 2 && r.URL.Query().Get("department_id") == "2" {
				json.NewEncoder(w).Encode(map[string]any{"errcode": 48002})
				return
			}
			if m == 4 || r.URL.Query().Get("department_id") == "2" {
				members = []map[string]string{}
			}
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userlist": members})
		case "/cgi-bin/user/get":
			for _, item := range members {
				if item["userid"] == r.URL.Query().Get("userid") {
					json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": item["userid"], "name": item["name"]})
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"errcode": 60111})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	client := func(corp string) *wecomadapter.Client {
		c, err := wecomadapter.New(wecomadapter.Config{Enabled: true, CorpID: corp, AgentID: "agent", Secret: "fixture-secret", AdminCallbackURI: "https://fixture.invalid/callback", SidebarCallbackURI: "https://fixture.invalid/sidebar", APIBase: provider.URL, HTTPClient: provider.Client()})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if err := f.management.SetEnterpriseEmployeeDirectory(client("fixture-corp"), []byte("abcdefghijklmnopqrstuvwxyz012345"), "fixture-corp"); err != nil {
		t.Fatal(err)
	}
	if err := f.management.SetEnterpriseEmployeeDirectory(client("another-corp"), []byte("abcdefghijklmnopqrstuvwxyz012345"), "fixture-corp"); !errors.Is(err, accessapp.ErrEnterpriseDirectoryUnavailable) {
		t.Fatalf("cross-enterprise directory accepted: %v", err)
	}
	owner := f.principal(f.owner)
	session := f.login(t, "owner")
	routes := mustGovernanceHTTPRoutes(t, f)
	refresh := func(path string, want int) map[string]any {
		t.Helper()
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, f.httpRequest(t, http.MethodPost, path, session, "refresh-directory", map[string]any{}))
		if response.Code != want {
			t.Fatalf("refresh status=%d want=%d body=%s", response.Code, want, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := staffRefreshUsers(t, f)
	result := refresh("/api/admin/access/users/refresh", http.StatusOK)
	if result["directory_refreshed"] != true || result["discovered"] != float64(4) || result["created"] != float64(1) || result["names_updated"] != float64(3) {
		t.Fatalf("refresh receipt=%v", result)
	}
	after := staffRefreshUsers(t, f)
	if len(after) != len(before)+1 {
		t.Fatal("new staff did not persist exactly once")
	}
	for i, user := range after[:len(before)] {
		user.DisplayName, user.UpdatedAt = before[i].DisplayName, before[i].UpdatedAt
		if !reflect.DeepEqual(user, before[i]) {
			t.Fatal("directory refresh changed existing access, binding, role, session or active state")
		}
	}
	newUser := after[len(after)-1]
	if newUser.WeComUserID != "new-staff" || newUser.LoginEnabled || newUser.AccessGrantedAt != nil || newUser.Roles[0] != domain.RoleViewer {
		t.Fatal("new staff received a backend grant")
	}
	listing, err := f.management.ListGovernance(f.ctx, owner)
	if err != nil || len(listing.Users) != len(after) {
		t.Fatalf("full governance listing count=%d err=%v", len(listing.Users), err)
	}
	for _, user := range listing.Users {
		if user.ID == pending.ID || user.ID == newUser.ID {
			if user.AuthorizedAccount || !user.Actions.ProvisionAccess || user.Actions.SetLoginEnabled || user.Actions.ChangeRole || user.Actions.ResetPassword || user.Actions.BindWeComUserID {
				t.Fatal("ungranted staff was hidden or exposed authorized-account mutations")
			}
		}
	}
	f.assertOneSuper(t, f.owner.ID)
	auditCount := staffRefreshAuditCount(t, f)
	result = refresh("/api/admin/access/users/refresh-names", http.StatusOK)
	if result["created"] != float64(0) || result["names_updated"] != float64(0) || !reflect.DeepEqual(after, staffRefreshUsers(t, f)) || staffRefreshAuditCount(t, f) != auditCount {
		t.Fatal("legacy/repeated refresh duplicated staff or audit")
	}

	// Manual and existing durable discovery share the same Access lock/kernel.
	mode.Store(1)
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	projector, err := accessapp.NewWeComStaffProjector(f.repository, credential.PasswordHasher{}, audit)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		created int64
		err     error
	}
	outcomes := make(chan outcome, 2)
	go func() { r, e := f.management.RefreshStaffDirectory(f.ctx, owner); outcomes <- outcome{r.Created, e} }()
	go func() {
		var r accessport.WeComStaffProjectionResult
		e := f.unit.Within(f.ctx, func(ctx context.Context) error {
			var e error
			r, e = projector.ProjectWeComStaffWithin(ctx, "periodic-concurrent", []accessport.WeComStaffProjection{{WeComUserID: "race-new", DisplayName: "Concurrent new"}}, time.Now())
			return e
		})
		outcomes <- outcome{r.Created, e}
	}()
	var created int64
	for i := 0; i < 2; i++ {
		o := <-outcomes
		if o.err != nil {
			t.Fatal(o.err)
		}
		created += o.created
	}
	if created != 1 || len(staffRefreshUsers(t, f)) != len(after)+1 {
		t.Fatal("concurrent discovery duplicated or lost the employee")
	}
	stable := staffRefreshUsers(t, f)
	stableAudits := staffRefreshAuditCount(t, f)
	mode.Store(2)
	refresh("/api/admin/access/users/refresh", http.StatusServiceUnavailable)
	if !reflect.DeepEqual(stable, staffRefreshUsers(t, f)) || staffRefreshAuditCount(t, f) != stableAudits {
		t.Fatal("partial Provider failure changed local employees")
	}
	mode.Store(3)
	if _, err := f.native.Exec(f.ctx, fmt.Sprintf(`CREATE FUNCTION fail_directory_audit() RETURNS trigger AS $$ BEGIN IF NEW.action='refresh_staff_name' AND NEW.target_admin_user_id=%d THEN RAISE EXCEPTION 'forced directory audit failure'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql; CREATE TRIGGER fail_directory_audit BEFORE INSERT ON admin_access_audit FOR EACH ROW EXECUTE FUNCTION fail_directory_audit();`, f.viewer.ID)); err != nil {
		t.Fatal(err)
	}
	var platformBefore, platformAfter int
	if err := f.native.QueryRow(f.ctx, `SELECT count(*) FROM audit_events`).Scan(&platformBefore); err != nil {
		t.Fatal(err)
	}
	refresh("/api/admin/access/users/refresh", http.StatusInternalServerError)
	if err := f.native.QueryRow(f.ctx, `SELECT count(*) FROM audit_events`).Scan(&platformAfter); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stable, staffRefreshUsers(t, f)) || staffRefreshAuditCount(t, f) != stableAudits || platformBefore != platformAfter {
		t.Fatal("audit failure committed a new projection, a partial name update or platform audit")
	}
	mode.Store(4)
	refresh("/api/admin/access/users/refresh", http.StatusOK)
	if !reflect.DeepEqual(stable, staffRefreshUsers(t, f)) {
		t.Fatal("absent/empty directory deleted or disabled existing staff")
	}
	mode.Store(0)
	granted, err := f.management.ProvisionEnterpriseEmployee(f.ctx, owner, accessapp.ProvisionEnterpriseEmployeeInput{WeComUserID: "new-staff", Role: domain.RoleViewer, IdempotencyKey: "explicit-first-grant"})
	if err != nil || granted.ID != newUser.ID || !granted.LoginEnabled || granted.AccessGrantedAt == nil {
		t.Fatalf("explicit grant did not reuse the discovered ID: id=%d err=%v", granted.ID, err)
	}
	f.assertOneSuper(t, f.owner.ID)
}
