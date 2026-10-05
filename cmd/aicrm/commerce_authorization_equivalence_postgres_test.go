package main

import (
	"crypto/sha256"
	"fmt"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Actual Access sessions/CSRF and domain handlers. Admin is enterprise-wide;
// Viewer needs a product collaborator grant for private membership reads, and
// even an 'edit' grant cannot elevate its global role. No provider worker runs.
func TestPostgreSQLCommerceAuthorizationObjectRoleEquivalence(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 60*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableReferral: true, deferEffectsWorker: true})
	owner := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	viewer, err := f.application.management.AddUser(f.ctx, owner, accessapp.AddUserInput{Username: "commerce-equivalence-viewer", Password: "commerce-equivalence-viewer-password", DisplayName: "Viewer", Roles: []accessdomain.Role{accessdomain.RoleViewer}})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := f.application.management.AddUser(f.ctx, owner, accessapp.AddUserInput{Username: "commerce-equivalence-admin", Password: "commerce-equivalence-admin-password", DisplayName: "Admin", Roles: []accessdomain.Role{accessdomain.RoleAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	_ = admin
	vs, vc := adminAccessLogin(t, f.application.handler, "commerce-equivalence-viewer", "commerce-equivalence-viewer-password")
	as, ac := adminAccessLogin(t, f.application.handler, "commerce-equivalence-admin", "commerce-equivalence-admin-password")
	trusted := issuePublicCommerceTrustedH5SessionWithKey(t, f, "commerce-equivalence-customer-session-0001")
	_, otherProduct := seedProductWebKitPhase2Products(t, f, "equivalence-standard", "equivalence-other-period")
	pool := f.application.pool.Native()
	now := time.Now().UTC()
	digest := sha256.Sum256([]byte("equivalence-private-member"))
	var memberID int64
	err = pool.QueryRow(f.ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('role-equivalence','private-member',$1,$2,'private-member','active',$3,$4,'PRIVATE-ROLE-REMARK',$5,$3,$3) RETURNING id`, trusted.payerCustomerID, f.serviceProductID, now, now.Add(30*24*time.Hour), digest[:]).Scan(&memberID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(f.ctx, `INSERT INTO product_service_period_member_collaborators(product_id,admin_user_id,permission,created_by,updated_by,created_at,updated_at) VALUES($1,$2,'edit',1,1,$3,$3)`, f.serviceProductID, viewer.ID, now); err != nil {
		t.Fatal(err)
	}
	read := func(path, session string) int {
		t.Helper()
		r := productExternalPushAdminMutation(t, f.application.handler, "GET", path, "", session, "", "")
		return r.Code
	}
	for _, id := range []int64{f.serviceProductID, otherProduct} {
		path := fmt.Sprintf("/api/admin/service-period-products/%d/members", id)
		if got := read(path, as); got != 200 {
			t.Fatalf("enterprise admin object read=%d", got)
		}
		want := 403
		if id == f.serviceProductID {
			want = 200
		}
		if got := read(path, vs); got != want {
			t.Fatalf("viewer grant object=%d status=%d want=%d", id, got, want)
		}
		if got := read(path, ""); got != 401 {
			t.Fatalf("anonymous private read=%d", got)
		}
		if got := read(path, trusted.token); got != 401 {
			t.Fatalf("customer cookie cannot act as admin=%d", got)
		}
	}
	// Distinct administrative authorization implementations and dangerous writes;
	// substitute existent and absent IDs to prove auth precedes domain lookup.
	paths := []struct{ method, path string }{{"PUT", "/api/admin/wechat-pay/products/%d"}, {"POST", "/api/admin/wechat-pay/products/%d/disable"}, {"POST", "/api/admin/service-period-products/%d/copy"}, {"POST", "/api/admin/service-period-products/%d/member-views"}, {"PUT", "/api/admin/service-period-products/%d/member-grid/external-share"}, {"PUT", "/api/admin/wechat-pay/products/%d/external-push"}, {"POST", "/api/admin/referral/campaigns/%d/disable"}, {"POST", "/api/admin/distribution/distributors/%d/disable"}, {"POST", "/api/admin/distribution/exceptions/%d/recoveries"}, {"POST", "/api/admin/payments/%d/refunds"}}
	for _, c := range paths {
		for _, id := range []int64{f.serviceProductID, 999999} {
			path := fmt.Sprintf(c.path, id)
			for _, actor := range []struct {
				session, csrf string
				want          int
			}{{vs, vc, 403}, {as, "", 403}, {"", "", 401}, {trusted.token, "", 401}} {
				out := productExternalPushAdminMutation(t, f.application.handler, c.method, path, "{}", actor.session, actor.csrf, "role-equivalence-write-0001")
				want := actor.want
				if strings.HasPrefix(path, "/api/admin/payments/") && want == 401 {
					want = 403
				} // Payment preserves its existing generic forbidden contract.
				if out.Code != want {
					t.Fatalf("%s %s got=%d want=%d body=%s", c.method, path, out.Code, want, out.Body.String())
				}
			}
		}
	}
	members := commerceFundsObject(t, productExternalPushAdminMutation(t, f.application.handler, "GET", fmt.Sprintf("/api/admin/service-period-products/%d/members", f.serviceProductID), "", as, "", ""), 200)["items"].([]any)
	if len(members) != 1 {
		t.Fatalf("members=%d", len(members))
	}
	memberRef := members[0].(map[string]any)["member_ref"].(string)
	// Scoped member write additionally checks the (product, member, customer)
	// relation after the real Admin session passes the global gate.
	for _, c := range []struct{ product, customer int64 }{{otherProduct, trusted.payerCustomerID}, {f.serviceProductID, trusted.payerCustomerID + 9999}} {
		path := fmt.Sprintf("/api/admin/service-period-products/%d/members/%s/remark", c.product, memberRef)
		body := fmt.Sprintf(`{"customer_id":%d,"remark":"TAMPERED","version":1}`, c.customer)
		out := productExternalPushAdminMutation(t, f.application.handler, http.MethodPut, path, body, as, ac, "role-equivalence-member-0001")
		if out.Code != http.StatusNotFound {
			t.Fatalf("member relation swap=%d %s", out.Code, out.Body.String())
		}
	}
	var remark string
	var version int64
	var receipts, effects int
	if err = pool.QueryRow(f.ctx, `SELECT remark,version,(SELECT count(*) FROM payment_refunds),(SELECT count(*) FROM external_effects) FROM order_service_entitlements WHERE id=$1`, memberID).Scan(&remark, &version, &receipts, &effects); err != nil || remark != "PRIVATE-ROLE-REMARK" || version != 1 || receipts != 0 || effects != 0 {
		t.Fatalf("denied writes changed data remark=%s version=%d refunds=%d effects=%d err=%v", remark, version, receipts, effects, err)
	}
	// Same valid relation permits the expected ordinary Admin write.
	path := fmt.Sprintf("/api/admin/service-period-products/%d/members/%s/remark", f.serviceProductID, memberRef)
	body := fmt.Sprintf(`{"customer_id":%d,"remark":"authorized update","version":1}`, trusted.payerCustomerID)
	out := productExternalPushAdminMutation(t, f.application.handler, http.MethodPut, path, body, as, ac, "role-equivalence-member-valid-0001")
	if out.Code != 200 || !strings.Contains(out.Body.String(), "authorized update") {
		t.Fatalf("valid Admin write=%d %s", out.Code, out.Body.String())
	}
	stale := productExternalPushAdminMutation(t, f.application.handler, http.MethodPut, path, body, as, ac, "role-equivalence-member-stale-0001")
	if stale.Code != 409 {
		t.Fatalf("stale member version=%d %s", stale.Code, stale.Body.String())
	}
}
