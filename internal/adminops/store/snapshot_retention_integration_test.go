package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func snapshotRetentionFixture(t *testing.T) (*ProjectionStore, *pgxpool.Pool, platformport.UnitOfWork) {
	t.Helper()
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	value := make([]byte, 6)
	if _, err = rand.Read(value); err != nil {
		t.Fatal(err)
	}
	schema := "adminops_retention_" + hex.EncodeToString(value)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../migrations/0015_config_adminops.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewProjectionPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	return owner, pool, uow
}

func TestPostgreSQLSnapshotRetentionAtomicBoundaryBatchAndReadTTL(t *testing.T) {
	owner, db, uow := snapshotRetentionFixture(t)
	ctx := context.Background()
	before := time.Now().UTC().Add(-30*24*time.Hour - time.Second)
	_, err := db.Exec(ctx, `INSERT INTO adminops_diagnostic_snapshots(diagnostic_key,status,observed_at)
 SELECT 'expired','ok',$1::timestamptz-interval '1 second' FROM generate_series(1,1002);
 `, before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO adminops_diagnostic_snapshots(diagnostic_key,status,observed_at) VALUES('boundary','ok',$1),('recent','ok',clock_timestamp())`, before); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO adminops_release_projections(release_sha,status,observed_at) VALUES('release-evidence','active',clock_timestamp()-interval '300 days')`); err != nil {
		t.Fatal(err)
	}
	command := adminopsport.SnapshotRetentionCommand{Before: before, Limit: 1000}
	rollback := errors.New("simulate cleanup receipt failure")
	err = uow.Within(ctx, func(txctx context.Context) error {
		visible, e := owner.ListDiagnosticSnapshots(txctx)
		if e != nil || len(visible) != 1 || visible[0].Key != "recent" {
			t.Fatalf("read TTL: count=%d err=%v", len(visible), e)
		}
		preview, e := owner.CleanupDiagnosticSnapshotsWithin(txctx, command)
		if e != nil || preview.Candidates != 1000 || preview.Deleted != 0 || !preview.Remaining {
			t.Fatalf("preview: %+v err=%v", preview, e)
		}
		command.Apply = true
		report, e := owner.CleanupDiagnosticSnapshotsWithin(txctx, command)
		if e != nil || report.Deleted != 1000 || !report.Remaining {
			t.Fatalf("batch: %+v err=%v", report, e)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM adminops_diagnostic_snapshots`).Scan(&count); err != nil || count != 1004 {
		t.Fatalf("must rollback deletion with failed receipt: %d %v", count, err)
	}
	for _, expected := range []int64{1000, 2, 0} {
		err = uow.Within(ctx, func(txctx context.Context) error {
			report, e := owner.CleanupDiagnosticSnapshotsWithin(txctx, command)
			if e != nil || report.Deleted != expected {
				t.Fatalf("replay/batch: %+v err=%v", report, e)
			}
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM adminops_diagnostic_snapshots WHERE diagnostic_key='boundary'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("boundary must survive: %d %v", count, err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM adminops_release_projections WHERE release_sha='release-evidence'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("release evidence must survive: %d %v", count, err)
	}
}

func TestPostgreSQLSnapshotRetentionRejectsUnsafeRequestAndConcurrentOwner(t *testing.T) {
	owner, db, uow := snapshotRetentionFixture(t)
	ctx := context.Background()
	command := adminopsport.SnapshotRetentionCommand{Before: time.Now().Add(-31 * 24 * time.Hour), Limit: 1000, Apply: true}
	if _, err := owner.CleanupDiagnosticSnapshotsWithin(ctx, command); err == nil {
		t.Fatal("must require caller transaction")
	}
	for _, bad := range []adminopsport.SnapshotRetentionCommand{{Before: time.Now().Add(-29 * 24 * time.Hour), Limit: 1000}, {Before: command.Before, Limit: 1001}} {
		err := uow.Within(ctx, func(txctx context.Context) error {
			_, e := owner.CleanupDiagnosticSnapshotsWithin(txctx, bad)
			return e
		})
		if !errors.Is(err, adminopsport.ErrSnapshotRetentionRequest) {
			t.Fatalf("unsafe request: %v", err)
		}
	}
	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err = holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('adminops.diagnostic_snapshot_retention'))`); err != nil {
		t.Fatal(err)
	}
	err = uow.Within(ctx, func(txctx context.Context) error {
		_, e := owner.CleanupDiagnosticSnapshotsWithin(txctx, command)
		return e
	})
	if !errors.Is(err, adminopsport.ErrSnapshotRetentionBusy) {
		t.Fatalf("concurrent owner must not run: %v", err)
	}
}
