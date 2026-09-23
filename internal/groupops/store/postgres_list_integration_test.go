package store

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
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// This fixture is intentionally limited to List's local projection. It uses
// an isolated PostgreSQL schema and never enables a Provider or dispatch path.
func TestPostgreSQLListProjectsBoundGroupCounts(t *testing.T) {
	pool, uow := groupOpsListIntegrationPool(t)
	ctx := context.Background()
	repository, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	const passwordHash = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$ZGlnaWVzdA"
	var actor int64
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES ('groupops-list-fixture', $1, '列表夹具') RETURNING id`, passwordHash).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	older := groupOpsListPlan(t, ctx, pool, actor, "旧计划", groupopsport.PlanDraft, base)
	empty := groupOpsListPlan(t, ctx, pool, actor, "零群计划", groupopsport.PlanPaused, base.Add(time.Minute))
	archived := groupOpsListPlan(t, ctx, pool, actor, "归档计划", groupopsport.PlanArchived, base.Add(2*time.Minute))
	for _, reference := range []string{"missing-directory-a", "missing-directory-b"} {
		if _, err = pool.Exec(ctx, `INSERT INTO group_ops_plan_group_assets(plan_id,asset_reference) VALUES ($1,$2)`, older, reference); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_ops_plan_group_assets(plan_id,asset_reference) VALUES ($1,'archived-missing-directory')`, archived); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES ($1,$2)`, older, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_ops_operation_member_directory(staff_id,sender_userid,display_name,name_source,profile_read_state,source_digest,profile_refreshed_at,refreshed_at) VALUES ($1,'fixture-owner','列表负责人','wecom_profile','ready','sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',$2,$2)`, actor, base); err != nil {
		t.Fatal(err)
	}

	first := groupOpsListPage(t, ctx, uow, repository, 2, 0)
	if len(first) != 2 || first[0].ID != empty || first[0].BoundGroupCount != 0 || first[1].ID != older || first[1].BoundGroupCount != 2 || first[1].Owner.StaffID != actor || first[1].Owner.DisplayName != "列表负责人" || first[1].QueueCount != 0 {
		t.Fatalf("first page=%+v", first)
	}
	second := groupOpsListPage(t, ctx, uow, repository, 2, 2)
	if len(second) != 0 {
		t.Fatalf("second page=%+v", second)
	}
	if count := groupOpsListCount(t, ctx, uow, repository); count != 2 {
		t.Fatalf("normal list count=%d want=2 (archived plan must not be counted)", count)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM group_ops_plan_group_assets WHERE plan_id=$1 AND asset_reference='missing-directory-b'`, older); err != nil {
		t.Fatal(err)
	}
	afterRemoval := groupOpsListPage(t, ctx, uow, repository, 2, 0)
	if len(afterRemoval) != 2 || afterRemoval[1].ID != older || afterRemoval[1].BoundGroupCount != 1 {
		t.Fatalf("after removal=%+v", afterRemoval)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_ops_plan_group_assets(plan_id,asset_reference) VALUES ($1,'bound-after-read')`, older); err != nil {
		t.Fatal(err)
	}
	afterBinding := groupOpsListPage(t, ctx, uow, repository, 2, 0)
	if len(afterBinding) != 2 || afterBinding[1].ID != older || afterBinding[1].BoundGroupCount != 2 {
		t.Fatalf("after binding=%+v", afterBinding)
	}
}

func groupOpsListPage(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *Repository, limit, offset int32) []groupopsport.PlanListItem {
	t.Helper()
	var result []groupopsport.PlanListItem
	if err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = repository.List(tx, limit, offset)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func groupOpsListCount(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *Repository) int64 {
	t.Helper()
	var count int64
	if err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		count, err = repository.Count(tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func groupOpsListPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, actor int64, name string, status groupopsport.PlanStatus, updated time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO group_ops_plans(name,status,revision,created_by,updated_by,created_at,updated_at,plan_type) VALUES ($1,$2,1,$3,$3,$4,$4,'standard') RETURNING id`, name, status, actor, updated).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func groupOpsListIntegrationPool(t *testing.T) (*pgxpool.Pool, *platformpostgres.UnitOfWork) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Group Ops PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_groupops_list_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	// Register cleanup immediately after creation. Subsequent migration or
	// wrapping failures must not leave this fixture's random schema behind.
	var pool *pgxpool.Pool
	var wrapped *platformpostgres.Pool
	t.Cleanup(func() {
		if wrapped != nil {
			wrapped.Close()
		} else if pool != nil {
			pool.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, dropErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); dropErr != nil {
			t.Errorf("drop isolated Group Ops fixture schema %q: %v", schema, dropErr)
		}
		admin.Close(cleanupCtx)
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Group Ops PostgreSQL fixture")
	}
	for _, name := range []string{"0003_access.sql", "0005_external_effects.sql", "0012_group_ops.sql", "0101_group_ops_ui_metadata.sql", "0116_group_ops_operation_member_directory.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	var currentSchema string
	if err = pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil || currentSchema != schema {
		t.Fatalf("fixture schema=%q want=%q err=%v", currentSchema, schema, err)
	}
	wrapped, err = platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	return pool, uow
}
