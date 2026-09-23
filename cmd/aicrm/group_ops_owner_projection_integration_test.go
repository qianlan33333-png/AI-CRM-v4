package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// OneID decision: not involved. This test joins only Group Ops-owned plan,
// staff-reference, directory, and execution projection tables. Persistence
// decision: repository reads are performed through the normal PostgreSQL UoW;
// no provider call, outbound intent, or external effect execution is made.
func TestGroupOpsPostgreSQLPlanOwnerProjection(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	staffID := func(username, sender string) int64 {
		t.Helper()
		var id int64
		if err = native.QueryRow(ctx, `WITH account AS (
			INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled)
			VALUES($1,'$argon2id$owner-projection',$2,$3,true,false) RETURNING id
		), role AS (
			INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account
		) SELECT id FROM account`, username, username, sender).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	readyStaff := staffID("owner-projection-ready", "owner-ready")
	secondaryStaff := staffID("owner-projection-secondary", "owner-secondary")
	pendingStaff := staffID("owner-projection-pending", "owner-pending")
	unavailableStaff := staffID("owner-projection-unavailable", "owner-unavailable")

	insertDirectory := func(staff int64, sender, name, state, code string) {
		t.Helper()
		if _, err = native.Exec(ctx, `
			INSERT INTO group_ops_operation_member_directory(
				staff_id,sender_userid,display_name,name_source,profile_read_state,
				profile_read_error_code,source_digest,active,profile_refreshed_at,refreshed_at
			) VALUES($1,$2,$3,'wecom_profile',$4,$5,$6,true,$7,$7)`,
			staff, sender, name, state, code, effectport.Hash("groupops-owner-projection", sender), now); err != nil {
			t.Fatal(err)
		}
	}
	insertDirectory(readyStaff, "owner-ready", "真实负责人", "ready", "")
	insertDirectory(secondaryStaff, "owner-secondary", "不应被选中", "ready", "")
	insertDirectory(unavailableStaff, "owner-unavailable", "保留的历史姓名", "unavailable", "provider_unavailable")

	insertPlan := func(name string) int64 {
		t.Helper()
		var id int64
		if err = native.QueryRow(ctx, `
			INSERT INTO group_ops_plans(name,status,revision,created_by,updated_by,created_at,updated_at,plan_type)
			VALUES($1,'draft',1,$2,$2,$3,$3,'standard') RETURNING id`, name, readyStaff, now).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	readyPlan := insertPlan("负责人已同步计划")
	pendingPlan := insertPlan("负责人待同步计划")
	unavailablePlan := insertPlan("负责人目录不可用计划")
	unconfiguredPlan := insertPlan("未配置负责人计划")

	// Insert in the opposite order so List and Get must both use their explicit
	// staff_id ordering, rather than relying on insertion order.
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2),($1,$3)`, readyPlan, secondaryStaff, readyStaff); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2)`, pendingPlan, pendingStaff); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2)`, unavailablePlan, unavailableStaff); err != nil {
		t.Fatal(err)
	}

	var nodeID, runID int64
	if err = native.QueryRow(ctx, `INSERT INTO group_ops_plan_nodes(plan_id,position,kind,message_text) VALUES($1,1,'message','队列计数') RETURNING id`, readyPlan).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO group_ops_runs(plan_id,trigger_kind,source_key_digest,plan_revision,scheduled_for,accepted_at,accepted_by) VALUES($1,'broadcast',$2,1,$3,$3,$4) RETURNING id`, readyPlan, make([]byte, 32), now, "admin:"+strconv.FormatInt(readyStaff, 10)).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	for index, target := range []string{"queue-target-one", "queue-target-two"} {
		var effectID int64
		seed := "owner-projection-queue-" + target
		if err = native.QueryRow(ctx, `
			INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state)
			VALUES('outbound','group_message',$1,$2,$3,$4,$5,'accepted') RETURNING id`,
			effectport.Hash(seed, "source"), effectport.Hash(seed, "target"), effectport.Hash(seed, "payload"), effectport.Hash(seed, "policy"), effectport.Hash(seed, "envelope")).Scan(&effectID); err != nil {
			t.Fatal(err)
		}
		executionKey := make([]byte, 32)
		executionKey[len(executionKey)-1] = byte(index + 1)
		if _, err = native.Exec(ctx, `
			INSERT INTO group_ops_executions(
				run_id,plan_id,node_id,plan_revision,node_position,target_reference,sender_userid_snapshot,
				target_digest,content_snapshot,content_digest,material_snapshot,material_digest,
				execution_key_digest,external_effect_id,state,scheduled_for,created_at,updated_at
			) VALUES($1,$2,$3,1,1,$4,'owner-ready',$5,'{}'::jsonb,$6,'{}'::jsonb,$7,$8,$9,'accepted',$10,$10,$10)`,
			runID, readyPlan, nodeID, target, effectport.Hash(seed, "target"), effectport.Hash(seed, "content"), effectport.Hash(seed, "material"), executionKey, effectID, now); err != nil {
			t.Fatal(err)
		}
	}

	var listed []groupopsport.PlanListItem
	if err = uow.Within(ctx, func(tx context.Context) error {
		listed, err = repository.List(tx, 20, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	byPlanID := make(map[int64]groupopsport.PlanListItem, len(listed))
	for _, item := range listed {
		byPlanID[item.ID] = item
	}
	expectations := map[int64]groupopsport.PlanOwner{
		readyPlan:        {StaffID: readyStaff, SenderUserID: "owner-ready", DisplayName: "真实负责人", NameSource: "wecom_profile", ProfileReadState: "ready"},
		pendingPlan:      {StaffID: pendingStaff},
		unavailablePlan:  {StaffID: unavailableStaff, SenderUserID: "owner-unavailable", DisplayName: "保留的历史姓名", NameSource: "wecom_profile", ProfileReadState: "unavailable", ProfileReadErrorCode: "provider_unavailable"},
		unconfiguredPlan: {},
	}
	for planID, want := range expectations {
		item, ok := byPlanID[planID]
		if !ok || item.Owner != want {
			t.Fatalf("list plan=%d present=%v owner=%+v want=%+v", planID, ok, item.Owner, want)
		}
		var detail groupopsport.Detail
		if err = uow.Within(ctx, func(tx context.Context) error {
			detail, err = repository.Get(tx, planID)
			return err
		}); err != nil || detail.Plan.Owner != want {
			t.Fatalf("detail plan=%d owner=%+v want=%+v err=%v", planID, detail.Plan.Owner, want, err)
		}
	}
	if got := byPlanID[readyPlan].QueueCount; got != 2 {
		t.Fatalf("ready queue_count=%d want=2; owner join must not multiply executions", got)
	}
}
