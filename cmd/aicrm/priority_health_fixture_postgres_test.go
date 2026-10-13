package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLPriorityHealthSenderFixtureHonorsAccessGovernance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	superAdmin := surveyJourneyGovernedActor(t, ctx, native, "priority-health-governed-admin", "Synthetic governed administrator")
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	id := newPriorityHealthSenderFixture(t, ctx, uow)
	var roles int
	var role string
	var active, login bool
	if err = native.QueryRow(ctx, `SELECT u.is_active,u.login_enabled,count(r.role_code),min(r.role_code) FROM admin_users u JOIN admin_user_roles r ON r.admin_user_id=u.id WHERE u.id=$1 GROUP BY u.id`, id).Scan(&active, &login, &roles, &role); err != nil || !active || login || roles != 1 || role != "viewer" {
		t.Fatalf("sender did not commit exactly one governed non-login role: active=%t login=%t roles=%d err=%v", active, login, roles, err)
	}
	var retainedSuper int64
	if err = native.QueryRow(ctx, `SELECT admin_user_id FROM access_super_admin_control WHERE singleton`).Scan(&retainedSuper); err != nil || retainedSuper != superAdmin {
		t.Fatalf("fixture changed existing super-administrator: unchanged=%t err=%v", retainedSuper == superAdmin, err)
	}
	adapter := automationOpsStaffAdapter{uow: uow, users: accessstore.NewPostgreSQL()}
	item, found, err := adapter.ResolveAutomationSender(ctx, "synthetic-installed-health-sender")
	if err != nil || !found || int64(item.StaffID) != id || !item.Active || !item.Eligible || item.EligibilityVersion < 1 || item.RefreshedAt.IsZero() {
		t.Fatalf("governed sender is not available to the real Access projection: found=%t eligible=%t err=%v", found, item.Active && item.Eligible, err)
	}
}
