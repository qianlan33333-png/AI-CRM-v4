package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func configRetentionFixture(t *testing.T) (*pgxpool.Pool, *configstore.Repository, *platformpostgres.UnitOfWork) {
	t.Helper()
	pool, cleanup := runtimeConfigIntegrationPool(t)
	t.Cleanup(cleanup)
	_, file, _, _ := runtime.Caller(0)
	for _, name := range []string{"0002_identity.sql", "0003_access.sql", "0071_message_archive_core.sql", "0189_owner_process_retention.sql"} {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(context.Background(), string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	return pool, repo, uow
}

func insertConfigUsage(t *testing.T, pool *pgxpool.Pool, subject int64, at time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO config_runtime_usage(revision,source,consumer,role,operation,subject_kind,subject_id,used_at)
		VALUES(0,'environment_default','automation.operations.max_recipients_per_run','api','preview','automation_preview',$1,$2) RETURNING id`, subject, at).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgreSQLConfigProcessRetentionBoundariesTTLAndBusinessPreservation(t *testing.T) {
	pool, repo, uow := configRetentionFixture(t)
	ctx := context.Background()
	var now time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	for i, days := range []int{29, 30, 31} {
		insertConfigUsage(t, pool, int64(i+1), now.Add(-time.Duration(days)*24*time.Hour))
	}
	service := runtimeConfigService(t, pool, nil)
	release := runtimeConfigCreateValidatePublish(t, ctx, service, 0, 2, "retention-create-01", "retention-validate-01", "retention-publish-01")
	// Actual Automation preview storage, not the observation row, freezes policy.
	if _, err := pool.Exec(ctx, `INSERT INTO automation_run_previews(package_id,package_version,snapshot_id,configuration_version_id,agent_id,agent_published_version,binding_version,sender_set_version,target_count,skipped_count,preview_digest,created_by,created_at,expires_at,runtime_config_observed,runtime_config_revision,max_recipients_per_run)
		VALUES(1,1,1,1,1,1,1,1,2,0,decode(repeat('aa',32),'hex'),1,$1::timestamptz,$1::timestamptz+interval '1 hour',true,$2,2)`, now, release.ID); err != nil {
		t.Fatal(err)
	}
	before := now.Add(-720 * time.Hour)
	if err := uow.Within(ctx, func(c context.Context) error {
		usage, err := repo.ListRuntimeUsage(c, 0, 100)
		if err != nil {
			return err
		}
		if len(usage) != 1 || usage[0].SubjectID != 1 {
			t.Fatalf("expired observations leaked: %+v", usage)
		}
		preview, err := repo.CleanupProcessDetailWithin(c, configport.ProcessRetentionCommand{Before: before, Limit: 100})
		if err != nil {
			return err
		}
		if preview.Candidates != 1 || preview.Deleted != 0 || preview.Bytes <= 0 || preview.Remaining {
			t.Fatalf("preview: %+v", preview)
		}
		report, err := repo.CleanupProcessDetailWithin(c, configport.ProcessRetentionCommand{Before: before, Limit: 100, Apply: true})
		if err != nil {
			return err
		}
		if report.Deleted != 1 || report.Candidates != 1 || report.Bytes <= 0 || report.Remaining {
			t.Fatalf("apply: %+v", report)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("29d and exact supplied 30d cutoff must survive: count=%d err=%v", count, err)
	}
	page, err := service.ListRuntimeReleases(ctx, 10)
	if err != nil || page.ActiveRevision != release.ID || page.Effective.AutomationMaxRecipients != 2 {
		t.Fatalf("release was changed: %+v %v", page, err)
	}
	var revision int64
	var ceiling int
	if err = pool.QueryRow(ctx, `SELECT runtime_config_revision,max_recipients_per_run FROM automation_run_previews`).Scan(&revision, &ceiling); err != nil || revision != release.ID || ceiling != 2 {
		t.Fatalf("frozen preview changed: %d %d %v", revision, ceiling, err)
	}
	for _, table := range []string{"config_runtime_release_values", "config_runtime_release_audits", "config_runtime_release_command_receipts", "config_outbox"} {
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count == 0 {
			t.Fatalf("business evidence %s lost: %d %v", table, count, err)
		}
	}
}

func TestPostgreSQLConfigProcessRetentionRollbackLocksAndBoundedPreview(t *testing.T) {
	pool, repo, uow := configRetentionFixture(t)
	ctx := context.Background()
	before := time.Now().Add(-720 * time.Hour)
	lockedID := insertConfigUsage(t, pool, 1, before.Add(-2*time.Hour))
	insertConfigUsage(t, pool, 2, before.Add(-time.Hour))
	rollback := errors.New("caller rollback")
	if err := uow.Within(ctx, func(c context.Context) error {
		preview, err := repo.CleanupProcessDetailWithin(c, configport.ProcessRetentionCommand{Before: before, Limit: 1})
		if err != nil {
			return err
		}
		if preview.Candidates != 1 || !preview.Remaining || preview.Deleted != 0 {
			t.Fatalf("unbounded preview: %+v", preview)
		}
		report, err := repo.CleanupProcessDetailWithin(c, configport.ProcessRetentionCommand{Before: before, Limit: 1, Apply: true})
		if err != nil {
			return err
		}
		if report.Deleted != 1 || !report.Remaining {
			t.Fatalf("batch not bounded: %+v", report)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("caller rollback was split: %d %v", count, err)
	}
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if _, err = locked.Exec(ctx, `SELECT id FROM config_runtime_usage WHERE id=$1 FOR UPDATE`, lockedID); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(c context.Context) error {
		report, e := repo.CleanupProcessDetailWithin(c, configport.ProcessRetentionCommand{Before: before, Limit: 1, Apply: true})
		if e == nil && (report.Deleted != 1 || !report.Remaining) {
			t.Fatalf("locked row hidden or removed: %+v", report)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE id=$1`, lockedID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("lock protection lost: %d %v", count, err)
	}
}

func TestPostgreSQLConfigProcessRetentionProtectsRecentDeleteUpdateAndTruncate(t *testing.T) {
	pool, repo, uow := configRetentionFixture(t)
	ctx := context.Background()
	insertConfigUsage(t, pool, 1, time.Now().Add(-29*24*time.Hour))
	for _, query := range []string{"UPDATE config_runtime_usage SET subject_id=subject_id", "UPDATE config_runtime_usage SET subject_id=subject_id WHERE false", "TRUNCATE config_runtime_usage", "DELETE FROM config_runtime_usage"} {
		if _, err := pool.Exec(ctx, query); err == nil {
			t.Fatalf("mutation guard bypass: %s", query)
		}
	}
	if err := uow.Within(ctx, func(c context.Context) error {
		report, err := repo.CleanupProcessDetailWithin(c, configport.ProcessRetentionCommand{Before: time.Now().Add(24 * time.Hour), Limit: 1000, Apply: true})
		if err == nil && (report.Deleted != 0 || report.Candidates != 0 || report.Before.After(time.Now().Add(-720*time.Hour))) {
			t.Fatalf("future cutoff bypassed minimum retention: %+v", report)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CleanupProcessDetailWithin(ctx, configport.ProcessRetentionCommand{Before: time.Now(), Limit: 1, Apply: true}); err == nil {
		t.Fatal("independent transaction accepted")
	}
	for _, command := range []configport.ProcessRetentionCommand{{Limit: 1}, {Before: time.Now(), Limit: 0}, {Before: time.Now(), Limit: 1001}} {
		if _, err := repo.CleanupProcessDetailWithin(ctx, command); !errors.Is(err, configport.ErrProcessRetentionInvalid) {
			t.Fatalf("invalid command accepted: %+v %v", command, err)
		}
	}
}
