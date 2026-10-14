package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	p "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Setup uses an isolated database; every business operation reaches the
// supplied installed binary via normal authenticated HTTP and CSRF.
func TestGroupOpsOwnerBindingInstalled(t *testing.T) {
	if testconfig.InstalledSurvey().Binary == "" || os.Getenv("AICRM_DATABASE_URL") == "" {
		t.Fatal("exact installed package and isolated PostgreSQL required")
	}
	f := newPublicSurveyPresentationChromiumFixture(t)
	owner := groupOpsBindingFixtureStaff(t, f.ctx, f.native, "binding-owner", "运营者")
	other := groupOpsBindingFixtureStaff(t, f.ctx, f.native, "binding-other", "其他群主")
	var planID int64
	var err error
	err = f.native.QueryRow(f.ctx, `INSERT INTO group_ops_plans(name,status,revision,created_by,updated_by,created_at,updated_at,plan_type) VALUES('绑定群安装合同','paused',1,$1,$1,now(),now(),'webhook') RETURNING id`, owner).Scan(&planID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.native.Exec(f.ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2)`, planID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = f.native.Exec(f.ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,external_member_count,source_digest,refreshed_at) VALUES('binding-owned',$1,'真实群名',12,8,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',now()),('binding-other',$2,'他人群',10,6,'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',now())`, owner, other); err != nil {
		t.Fatal(err)
	}
	if _, err = f.native.Exec(f.ctx, `INSERT INTO group_ops_operation_member_directory(staff_id,sender_userid,display_name,name_source,profile_read_state,source_digest,active,refreshed_at) VALUES($1,'binding-owner','运营者','local_fallback','unavailable','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',true,now())`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = f.native.Exec(f.ctx, `UPDATE group_ops_directory_groups SET owner_staff_id=NULL,owner_user_id='binding-owner' WHERE chat_reference='binding-owned'`); err != nil {
		t.Fatal(err)
	}

	path := "/api/admin/automation-conversion/group-ops/plans/" + strconv.FormatInt(planID, 10)
	call := func(method, path string, body any, key string, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		if body == nil {
			raw = nil
		}
		req, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Idempotency-Key", key)
		for _, cookie := range f.adminCookies {
			req.AddCookie(cookie)
			if cookie.Name == "aicrm_admin_csrf" {
				req.Header.Set("X-CSRF-Token", cookie.Value)
			}
		}
		res, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d body=%s err=%v", method, path, res.StatusCode, want, out, err)
		}
		return out
	}
	command := map[string]any{"expected_revision": 1, "asset_reference": "binding-owned"}
	first := call("POST", path+"/group-assets", command, "installed-binding-add", 200)
	var d p.Detail
	if err = json.Unmarshal(first, &d); err != nil || len(d.GroupAssets) != 1 || d.Plan.Status != p.PlanPaused || d.Plan.Revision != 2 {
		t.Fatalf("first=%s err=%v", first, err)
	}
	readback := call("GET", path, nil, "", 200)
	if err = json.Unmarshal(readback, &d); err != nil || len(d.GroupAssets) != 1 || d.GroupAssets[0].DisplayName != "真实群名" || d.GroupAssets[0].OwnerStaffID != owner || d.GroupAssets[0].MemberCount == nil || *d.GroupAssets[0].MemberCount != 12 || d.Plan.Status != p.PlanPaused {
		t.Fatalf("decorated readback=%s err=%v", readback, err)
	}
	if replay := call("POST", path+"/group-assets", command, "installed-binding-add", 200); !bytes.Equal(first, replay) {
		t.Fatal("replay changed result")
	}
	call("POST", path+"/group-assets", command, "installed-binding-stale", 409)
	call("POST", path+"/group-assets", map[string]any{"expected_revision": 2, "asset_reference": "binding-other"}, "installed-binding-other", 400)
	if raw := call("GET", "/api/admin/automation-conversion/group-ops/groups?owner_userid="+strconv.FormatInt(owner, 10), nil, "", 200); !bytes.Contains(raw, []byte("真实群名")) || bytes.Contains(raw, []byte("他人群")) {
		t.Fatalf("owner directory=%s", raw)
	}
	if _, err = f.native.Exec(f.ctx, `DELETE FROM group_ops_directory_groups WHERE chat_reference='binding-owned'`); err != nil {
		t.Fatal(err)
	}
	if raw := call("GET", path, nil, "", 200); !bytes.Contains(raw, []byte("binding-owned")) {
		t.Fatal("missing directory erased binding")
	}
	call("DELETE", path+"/group-assets/binding-owned", map[string]any{"expected_revision": 2}, "installed-binding-remove", 200)
	raw := call("GET", path, nil, "", 200)
	if err = json.Unmarshal(raw, &d); err != nil || len(d.GroupAssets) != 0 || d.Plan.Revision != 3 || d.Plan.Status != p.PlanPaused {
		t.Fatalf("readback=%s err=%v", raw, err)
	}
	if _, err = f.native.Exec(f.ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,external_member_count,source_digest,refreshed_at) VALUES('binding-owned',$1,'真实群名',12,8,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',now())`, owner); err != nil {
		t.Fatal(err)
	}
	commandBrowser := exec.CommandContext(f.ctx, "node", filepath.Join("cmd", "aicrm", "group_ops_owner_binding_chromium_journey.mjs"))
	commandBrowser.Env = append(os.Environ(), "AICRM_GROUPOPS_TEST_URL="+f.server.URL, "AICRM_GROUPOPS_TEST_PLAN_ID="+strconv.FormatInt(planID, 10), "AICRM_GROUPOPS_SCREENSHOT_DIR="+f.screenshots)
	if out, e := commandBrowser.CombinedOutput(); e != nil {
		t.Fatalf("installed binding Chromium: %v\n%s", e, out)
	} else {
		t.Log(string(out))
	}
	raw = call("GET", path, nil, "", 200)
	if err = json.Unmarshal(raw, &d); err != nil || len(d.GroupAssets) != 0 || d.Plan.Revision != 5 || d.Plan.Status != p.PlanPaused {
		t.Fatalf("browser readback=%s err=%v", raw, err)
	}

	for _, table := range []string{"external_effects", "river_job"} {
		var count int
		if err = f.native.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s=%d err=%v", table, count, err)
		}
	}
	t.Log("installed group binding: names, owner scope/rejection, paused save, CAS, replay, removal, readback; zero outbound writes")
}

// Employees are seeded with exactly one role in the same SQL transaction;
// they remain unable to log in, and the fixture grants no real user access.
func groupOpsBindingFixtureStaff(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username, displayName string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `WITH staff AS (INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES($1,'$argon2id$test',$2,$1,true,false) RETURNING id), role AS (INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM staff) SELECT id FROM staff`, username, displayName).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGroupOpsOwnerBindingFixtureStaffGovernance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate browser fixture")
	}
	if out, err := exec.CommandContext(ctx, "node", "--check", filepath.Join(filepath.Dir(source), "group_ops_owner_binding_chromium_journey.mjs")).CombinedOutput(); err != nil {
		t.Fatalf("browser fixture syntax: %v\n%s", err, out)
	}

	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	superAdmin := surveyJourneyGovernedActor(t, ctx, pool, "binding-fixture-super", "Synthetic super administrator")
	owner := groupOpsBindingFixtureStaff(t, ctx, pool, "binding-governed-fixture", "运营者")
	var roles int
	var role string
	var login, active bool
	err = pool.QueryRow(ctx, `SELECT count(r.*),min(r.role_code),u.login_enabled,u.is_active FROM admin_users u JOIN admin_user_roles r ON r.admin_user_id=u.id WHERE u.id=$1 GROUP BY u.login_enabled,u.is_active`, owner).Scan(&roles, &role, &login, &active)
	if err != nil || roles != 1 || role != "viewer" || login || !active {
		t.Fatalf("fixture roles=%d role=%s login=%t active=%t err=%v", roles, role, login, active, err)
	}
	var preserved int64
	if err = pool.QueryRow(ctx, `SELECT admin_user_id FROM access_super_admin_control WHERE singleton`).Scan(&preserved); err != nil || preserved != superAdmin {
		t.Fatalf("sole fixture super administrator changed: %v", err)
	}

}
