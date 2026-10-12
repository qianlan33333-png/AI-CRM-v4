package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLArchiveRepairEvidenceCursorAtomicAndReplay(t *testing.T) {
	pool, cleanup := archiveIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPostgreSQL()
	now := time.Now().UTC()
	const scope = "wecom-corp:repair-fixture"
	var run int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		run, e = repo.StartRun(tx, app.SyncRun{CorpScope: scope, Trigger: "notify", StartedAt: now})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"text\":\"a\nb\"}")
	protected, digest := domain.NewIssuePayload(original)
	batch := app.Batch{CorpScope: scope, ExpectedCursor: 0, EndSeq: 11, RunID: run, NotifyReceivedAt: now,
		Messages: []domain.Message{{CorpScope: scope, Seq: 11, MsgID: "repair-fixture", Action: "send", MessageType: "text", Conversation: "private", OccurredAt: now, ContentText: "a\nb", Normalized: json.RawMessage(`{"msgtype":"text","content":"a\nb"}`)}},
		Issues:   []app.IngestIssue{{Seq: 11, MsgID: "repair-fixture", Stage: "sdk_payload", Reason: "json_control_character_escaped", Payload: protected, Digest: digest}}}
	abort := errors.New("fixture abort")
	if err = uow.Within(ctx, func(tx context.Context) error {
		if _, e := repo.CommitBatch(tx, batch); e != nil {
			return e
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	var messages, issues, cursor int64
	readCounts := func() {
		t.Helper()
		if e := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM message_archive_messages),(SELECT count(*) FROM message_archive_ingest_issues),(SELECT last_seq FROM message_archive_sync_state WHERE corp_scope=$1)`, scope).Scan(&messages, &issues, &cursor); e != nil {
			t.Fatal(e)
		}
	}
	readCounts()
	if messages != 0 || issues != 0 || cursor != 0 {
		t.Fatal("repair evidence committed apart from cursor")
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := repo.CommitBatch(tx, batch); return e }); err != nil {
		t.Fatal(err)
	}
	readCounts()
	if messages != 1 || issues != 1 || cursor != 11 {
		t.Fatal("repair facts missing")
	}
	var retained []byte
	var content string
	var fetched, issueCount int64
	if err = pool.QueryRow(ctx, `SELECT (SELECT protected_payload FROM message_archive_ingest_issues),(SELECT content_text FROM message_archive_messages),fetched_count,issue_count FROM message_archive_sync_runs WHERE id=$1`, run).Scan(&retained, &content, &fetched, &issueCount); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, retained) || content != "a\nb" || fetched != 1 || issueCount != 1 {
		t.Fatal("payload changed or fetched count duplicated")
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := repo.CommitBatch(tx, batch); return e }); !errors.Is(err, app.ErrCursorAdvanced) {
		t.Fatal("replay did not preserve CAS")
	}
	readCounts()
	if messages != 1 || issues != 1 || cursor != 11 {
		t.Fatal("replay duplicated repair evidence")
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repo.FinishRun(tx, run, app.SyncRunFinish{EndSeq: 11, Status: "succeeded", FinishedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	reader := NewOpsDiagnosticReader(pool)
	counts, err := reader.ReadOpsDiagnosticCounts(ctx, now)
	if err != nil || counts["ingestion_blocked"] != 0 || counts["imported_last_hour"] != 1 {
		t.Fatalf("successful health=%v err=%v", counts, err)
	}
	var failed int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		failed, e = repo.StartRun(tx, app.SyncRun{CorpScope: scope, Trigger: "notify", StartSeq: 11, StartedAt: now})
		if e != nil {
			return e
		}
		return repo.FinishRun(tx, failed, app.SyncRunFinish{EndSeq: 11, Status: "failed", ErrorCode: "archive_processing_failed", FinishedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	counts, err = reader.ReadOpsDiagnosticCounts(ctx, now)
	if err != nil || counts["ingestion_blocked"] != 1 || counts["failures_last_hour"] != 1 {
		t.Fatalf("blocked health=%v err=%v", counts, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		failed, e = repo.StartRun(tx, app.SyncRun{CorpScope: scope, Trigger: "notify", StartSeq: 11, StartedAt: now})
		if e != nil {
			return e
		}
		return repo.FinishRun(tx, failed, app.SyncRunFinish{EndSeq: 11, Status: "failed", ErrorCode: "work_budget_exhausted", FinishedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	counts, err = reader.ReadOpsDiagnosticCounts(ctx, now)
	if err != nil || counts["ingestion_blocked"] != 0 || counts["failures_last_hour"] != 1 {
		t.Fatalf("progress incorrectly blocks health=%v err=%v", counts, err)
	}
}
