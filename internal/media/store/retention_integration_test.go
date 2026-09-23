package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func retentionRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	value := make([]byte, 6)
	if _, err = rand.Read(value); err != nil {
		t.Fatal(err)
	}
	schema := "media_retention_" + hex.EncodeToString(value)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.Close)
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0007_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	return repo, native
}

func seedRetentionUpload(t *testing.T, db *pgxpool.Pool, before time.Time, expired bool, completed bool, original bool) int64 {
	t.Helper()
	ctx := context.Background()
	content := []byte("durable-original")
	digestValue := bytesDigest(content)
	var attachment any
	if completed {
		if _, err := db.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'application/pdf',$2,$3) ON CONFLICT DO NOTHING`, digestValue, len(content), content); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := db.QueryRow(ctx, `INSERT INTO media_attachments(blob_digest,file_name,name,mime_type,byte_size,created_by,updated_by) VALUES($1,'original.pdf','original','application/pdf',$2,7,7) RETURNING id`, digestValue, len(content)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		attachment = id
		if !original {
			digestValue = digest("lost-original")
		}
	}
	expires := before.Add(-time.Hour)
	if !expired {
		expires = time.Now().Add(time.Hour)
	}
	var id int64
	key := digest(fmt.Sprintf("retention-fixture-%d", time.Now().UnixNano()))
	if err := db.QueryRow(ctx, `INSERT INTO media_attachment_uploads(actor_admin_user_id,idempotency_key_digest,file_name,name,expected_size,expected_digest,expires_at,completed_attachment_id,created_at) VALUES(7,$1,'upload.pdf','fixture',$2,$3,$4,$5,$6) RETURNING id`, key, len(content), digestValue, expires, attachment, before.Add(-time.Hour)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO media_attachment_upload_parts(upload_id,part_number,digest,content,created_at) VALUES($1,1,$2,$3,$4)`, id, bytesDigest(content), content, before.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgreSQLRetentionKeepsOriginalsReceiptsAndReplays(t *testing.T) {
	repo, db := retentionRepository(t)
	ctx := context.Background()
	before := time.Now().UTC().Add(-31 * 24 * time.Hour).Truncate(time.Second)
	completed := seedRetentionUpload(t, db, before, true, true, true)
	expired := seedRetentionUpload(t, db, before, true, false, false)
	active := seedRetentionUpload(t, db, before, false, false, false)
	missingOriginal := seedRetentionUpload(t, db, before, true, true, false)
	boundary := seedRetentionUpload(t, db, before, true, false, false)
	if _, err := db.Exec(ctx, `UPDATE media_attachment_upload_parts SET created_at=$2 WHERE upload_id=$1`, boundary, before); err != nil {
		t.Fatal(err)
	}
	var attachmentID int64
	if err := db.QueryRow(ctx, `SELECT completed_attachment_id FROM media_attachment_uploads WHERE id=$1`, completed).Scan(&attachmentID); err != nil {
		t.Fatal(err)
	}
	const replayKey = "retention-completion-replay-0001"
	if _, err := db.Exec(ctx, `INSERT INTO media_operation_receipts(operation,actor_admin_user_id,resource_kind,resource_id,idempotency_key_digest,command_digest,result) VALUES('attachment.upload.complete',7,'attachment',$1,$2,$3,jsonb_build_object('attachment_id',$1::bigint))`, attachmentID, digest(replayKey), digest(fmt.Sprintf("upload:%d", completed))); err != nil {
		t.Fatal(err)
	}
	var beforeFacts string
	facts := `SELECT jsonb_build_object('blobs',(SELECT jsonb_agg(to_jsonb(b) ORDER BY digest) FROM media_blobs b),'uploads',(SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM media_attachment_uploads u),'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM media_operation_receipts r),'attachments',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM media_attachments a))::text`
	if err := db.QueryRow(ctx, facts).Scan(&beforeFacts); err != nil {
		t.Fatal(err)
	}
	command := mediaport.UploadPartRetentionCommand{Before: before, Limit: 1}
	preview, err := repo.CleanupExpiredUploadParts(ctx, command)
	if err != nil || preview.Candidates != 1 || preview.Deleted != 0 || !preview.Remaining {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	command.Apply = true
	first, err := repo.CleanupExpiredUploadParts(ctx, command)
	if err != nil || first.Deleted != 1 || !first.Remaining {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := repo.CleanupExpiredUploadParts(ctx, command)
	if err != nil || second.Deleted != 1 || second.Remaining {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	replayed, err := repo.CleanupExpiredUploadParts(ctx, command)
	if err != nil || replayed.Deleted != 0 || replayed.Remaining {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	for _, id := range []int64{active, missingOriginal, boundary} {
		var count int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM media_attachment_upload_parts WHERE upload_id=$1`, id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("protected upload %d count=%d err=%v", id, count, err)
		}
	}
	for _, id := range []int64{completed, expired} {
		var count int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM media_attachment_upload_parts WHERE upload_id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("expired upload %d count=%d err=%v", id, count, err)
		}
	}
	id, err := repo.CompleteAttachmentUpload(ctx, completed, 7, replayKey)
	if err != nil || id != attachmentID {
		t.Fatalf("business replay id=%d err=%v", id, err)
	}
	var afterFacts string
	if err := db.QueryRow(ctx, facts).Scan(&afterFacts); err != nil || afterFacts != beforeFacts {
		t.Fatalf("business facts changed: %v", err)
	}
}

func TestPostgreSQLRetentionRejectsEarlyCutoffAndSkipsActiveLocks(t *testing.T) {
	repo, db := retentionRepository(t)
	ctx := context.Background()
	before := time.Now().UTC().Add(-31 * 24 * time.Hour)
	upload := seedRetentionUpload(t, db, before, true, false, false)
	for _, command := range []mediaport.UploadPartRetentionCommand{{Before: time.Now().Add(-29 * 24 * time.Hour), Limit: 1000, Apply: true}, {Before: before, Limit: 1001, Apply: true}, {Before: before, Limit: 0, Apply: true}} {
		if _, err := repo.CleanupExpiredUploadParts(ctx, command); !errors.Is(err, mediaport.ErrRetentionRequest) {
			t.Fatalf("invalid request accepted: %v", err)
		}
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM media_attachment_uploads WHERE id=$1 FOR UPDATE`, upload); err != nil {
		t.Fatal(err)
	}
	report, err := repo.CleanupExpiredUploadParts(ctx, mediaport.UploadPartRetentionCommand{Before: before, Limit: 1000, Apply: true})
	if err != nil || report.Deleted != 0 || !report.Remaining {
		t.Fatalf("locked report=%+v err=%v", report, err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('media.upload_part_retention'))`); err != nil {
		t.Fatal(err)
	}
	_, err = repo.CleanupExpiredUploadParts(ctx, mediaport.UploadPartRetentionCommand{Before: before, Limit: 1000, Apply: true})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent cleanup allowed: %v", err)
	}
	if err = tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
}

func TestPostgreSQLRetentionBatchNeverDeletesMoreThan1000Parts(t *testing.T) {
	repo, db := retentionRepository(t)
	ctx := context.Background()
	before := time.Now().UTC().Add(-31 * 24 * time.Hour)
	upload := seedRetentionUpload(t, db, before, true, false, false)
	if _, err := db.Exec(ctx, `INSERT INTO media_attachment_upload_parts(upload_id,part_number,digest,content,created_at)
SELECT $1,n,$2,$3,$4 FROM generate_series(2,1002) n`, upload, bytesDigest([]byte("x")), []byte("x"), before.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	report, err := repo.CleanupExpiredUploadParts(ctx, mediaport.UploadPartRetentionCommand{Before: before, Limit: 1000, Apply: true})
	if err != nil || report.Deleted != 1000 || !report.Remaining {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	var remaining int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM media_attachment_upload_parts`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
}
