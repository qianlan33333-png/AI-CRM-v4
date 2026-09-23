package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func archiveRetentionFixture(t *testing.T) (*pgxpool.Pool, *platformpostgres.UnitOfWork) {
	t.Helper()
	pool, cleanup := archiveIntegrationPool(t)
	t.Cleanup(cleanup)
	_, file, _, _ := runtime.Caller(0)
	for _, name := range []string{"0013_automation_agents.sql", "0015_config_adminops.sql", "0043_automation_runtime.sql", "0094_runtime_config_releases.sql", "0189_owner_process_retention.sql"} {
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
	if _, err = pool.Exec(context.Background(), `INSERT INTO message_archive_sync_state(corp_scope) VALUES('wecom-corp:retention')`); err != nil {
		t.Fatal(err)
	}
	return pool, uow
}

func insertArchiveRun(t *testing.T, pool *pgxpool.Pool, status string, finished any, started time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO message_archive_sync_runs(corp_scope,trigger_type,start_seq,end_seq,status,started_at,finished_at)
		VALUES('wecom-corp:retention','manual',0,0,$1,$2,$3) RETURNING id`, status, started, finished).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgreSQLArchiveProcessRetentionBoundariesRunningCursorAndMsgID(t *testing.T) {
	pool, uow := archiveRetentionFixture(t)
	ctx := context.Background()
	repo := NewPostgreSQL()
	var now time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	before := now.Add(-720 * time.Hour)
	for i, days := range []int{29, 30, 31} {
		status := "succeeded"
		if i == 1 {
			status = "failed"
		}
		at := now.Add(-time.Duration(days) * 24 * time.Hour)
		insertArchiveRun(t, pool, status, at, at.Add(-time.Hour))
	}
	runningID := insertArchiveRun(t, pool, "running", now.Add(-31*24*time.Hour), now.Add(-32*24*time.Hour))
	insertArchiveRun(t, pool, "failed", nil, now.Add(-31*24*time.Hour))
	message := domain.Message{CorpScope: "wecom-corp:retention", Seq: 1, MsgID: "retention-fixture-msgid", MessageType: "text", Conversation: "private", OccurredAt: now, ContentText: "original", Normalized: json.RawMessage(`{}`)}
	if err := uow.Within(ctx, func(c context.Context) error {
		id, err := repo.StartRun(c, app.SyncRun{CorpScope: message.CorpScope, Trigger: "manual", StartedAt: now.Add(-31 * 24 * time.Hour)})
		if err != nil {
			return err
		}
		if _, err = repo.CommitBatch(c, app.Batch{CorpScope: message.CorpScope, RunID: id, EndSeq: 1, Messages: []domain.Message{message}, NotifyReceivedAt: now}); err != nil {
			return err
		}
		return repo.FinishRun(c, id, app.SyncRunFinish{EndSeq: 1, Status: "succeeded", FinishedAt: now.Add(-31 * 24 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	if err := uow.Within(ctx, func(c context.Context) error {
		preview, err := repo.CleanupProcessDetailWithin(c, archiveport.ProcessRetentionCommand{Before: before, Limit: 100})
		if err != nil {
			return err
		}
		if preview.Candidates != 2 || preview.Deleted != 0 || preview.Bytes <= 0 || preview.Remaining {
			t.Fatalf("preview: %+v", preview)
		}
		report, err := repo.CleanupProcessDetailWithin(c, archiveport.ProcessRetentionCommand{Before: before, Limit: 100, Apply: true})
		if err != nil {
			return err
		}
		if report.Candidates != 2 || report.Deleted != 2 || report.Bytes <= 0 || report.Remaining {
			t.Fatalf("apply: %+v", report)
		}
		cursor, err := repo.CommittedCursor(c, message.CorpScope)
		if err != nil {
			return err
		}
		if cursor != 1 {
			t.Fatalf("cursor lost after cleanup: %d", cursor)
		}
		// A later provider page can repeat a msgid at a new sequence. Actual
		// CommitBatch must still deduplicate after its old run detail is removed.
		message.Seq, message.ContentText = 2, "must not overwrite"
		result, err := repo.CommitBatch(c, app.Batch{CorpScope: message.CorpScope, ExpectedCursor: 1, EndSeq: 2, Messages: []domain.Message{message}, NotifyReceivedAt: now})
		if err != nil {
			return err
		}
		if result.Inserted != 0 || result.Duplicates != 1 || result.CommittedCursor != 2 {
			t.Fatalf("msgid replay changed: %+v", result)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_archive_sync_runs`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("recent/boundary/running/incomplete rows lost: %d %v", count, err)
	}
	var text string
	if err := pool.QueryRow(ctx, `SELECT content_text FROM message_archive_messages`).Scan(&text); err != nil || text != "original" {
		t.Fatalf("message business fact changed: %s %v", text, err)
	}
	if err := uow.Within(ctx, func(c context.Context) error {
		return repo.FinishRun(c, runningID, app.SyncRunFinish{EndSeq: 2, Status: "succeeded", FinishedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM message_archive_sync_runs WHERE id=$1`, runningID).Scan(&text); err != nil || text != "succeeded" {
		t.Fatalf("running run could not finish: %s %v", text, err)
	}
}

func TestPostgreSQLArchiveProcessRetentionRollbackAndSkipLocked(t *testing.T) {
	pool, uow := archiveRetentionFixture(t)
	ctx := context.Background()
	repo := NewPostgreSQL()
	before := time.Now().Add(-720 * time.Hour)
	lockedID := insertArchiveRun(t, pool, "failed", before.Add(-2*time.Hour), before.Add(-3*time.Hour))
	insertArchiveRun(t, pool, "succeeded", before.Add(-time.Hour), before.Add(-2*time.Hour))
	rollback := errors.New("caller rollback")
	if err := uow.Within(ctx, func(c context.Context) error {
		preview, err := repo.CleanupProcessDetailWithin(c, archiveport.ProcessRetentionCommand{Before: before, Limit: 1})
		if err != nil {
			return err
		}
		if preview.Candidates != 1 || !preview.Remaining || preview.Deleted != 0 {
			t.Fatalf("preview not bounded: %+v", preview)
		}
		report, err := repo.CleanupProcessDetailWithin(c, archiveport.ProcessRetentionCommand{Before: before, Limit: 1, Apply: true})
		if err != nil {
			return err
		}
		if report.Deleted != 1 || !report.Remaining {
			t.Fatalf("delete not bounded: %+v", report)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_archive_sync_runs`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback lost process detail: %d %v", count, err)
	}
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if _, err = locked.Exec(ctx, `SELECT id FROM message_archive_sync_runs WHERE id=$1 FOR UPDATE`, lockedID); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(c context.Context) error {
		report, e := repo.CleanupProcessDetailWithin(c, archiveport.ProcessRetentionCommand{Before: before, Limit: 1, Apply: true})
		if e == nil && (report.Deleted != 1 || !report.Remaining) {
			t.Fatalf("locked row hidden or removed: %+v", report)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM message_archive_sync_runs WHERE id=$1`, lockedID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("locked row deleted: %d %v", count, err)
	}
}

func TestPostgreSQLArchiveProcessRetentionRejectsInvalidAndClampsCutoff(t *testing.T) {
	pool, uow := archiveRetentionFixture(t)
	ctx := context.Background()
	repo := NewPostgreSQL()
	insertArchiveRun(t, pool, "succeeded", time.Now().Add(-29*24*time.Hour), time.Now().Add(-30*24*time.Hour))
	if err := uow.Within(ctx, func(c context.Context) error {
		report, err := repo.CleanupProcessDetailWithin(c, archiveport.ProcessRetentionCommand{Before: time.Now().Add(24 * time.Hour), Limit: 1000, Apply: true})
		if err == nil && (report.Candidates != 0 || report.Deleted != 0 || report.Before.After(time.Now().Add(-720*time.Hour))) {
			t.Fatalf("future cutoff shortened retention: %+v", report)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CleanupProcessDetailWithin(ctx, archiveport.ProcessRetentionCommand{Before: time.Now(), Limit: 1}); err == nil {
		t.Fatal("missing caller UoW accepted")
	}
	for _, command := range []archiveport.ProcessRetentionCommand{{Limit: 1}, {Before: time.Now(), Limit: 0}, {Before: time.Now(), Limit: 1001}} {
		if _, err := repo.CleanupProcessDetailWithin(ctx, command); !errors.Is(err, archiveport.ErrProcessRetentionInvalid) {
			t.Fatalf("invalid command accepted: %+v %v", command, err)
		}
	}
}
