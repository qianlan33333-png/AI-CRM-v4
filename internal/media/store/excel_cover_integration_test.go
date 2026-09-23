package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLExcelCoverReuseDoesNotReviveDisabledOrLoseFrozenBytes(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_excel_cover_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	for _, migration := range []string{"0007_media.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migration))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("%s: %v", migration, execErr)
		}
	}
	// 0126 adds forward-only fields to AI Assistant's owned history tables.
	// This Media-only integration fixture supplies their pre-migration shape;
	// it does not read or write them.
	if _, err = native.Exec(ctx, `CREATE TABLE ai_assistant_excel_batch_versions (plan_id BIGINT,content_revision INTEGER,file_digest TEXT,cover_digest TEXT,created_by BIGINT,created_at TIMESTAMPTZ);
CREATE TABLE ai_assistant_excel_batch_version_covers (plan_id BIGINT,content_revision INTEGER,cover_digest TEXT,created_by BIGINT,created_at TIMESTAMPTZ);`); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0126_media_material_source_snapshots.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	content := testPNG(t)
	results := make([]mediaport.ExcelCover, 12)
	var group sync.WaitGroup
	errs := make(chan error, len(results))
	for index := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			result, callErr := repo.CreateOrReuseExcelCover(ctx, mediaport.ExcelCoverUpload{Actor: 9, IdempotencyKey: "excel-cover-concurrent-key-" + hex.EncodeToString([]byte{byte(index)}) + "-0000000000000000", FileName: "cover.png", DeclaredType: "image/png", Content: content})
			results[index] = result
			if callErr != nil {
				errs <- callErr
			}
		}(index)
	}
	group.Wait()
	close(errs)
	for err = range errs {
		t.Fatal(err)
	}
	first := results[0]
	if first.ImageID < 1 {
		t.Fatalf("cover=%+v", first)
	}
	for _, result := range results[1:] {
		if result.ImageID != first.ImageID || result.ContentDigest != first.ContentDigest {
			t.Fatalf("concurrent dedup mismatch: first=%+v got=%+v", first, result)
		}
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_images WHERE enabled`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("enabled images=%d err=%v", count, err)
	}
	if _, err = native.Exec(ctx, `UPDATE media_images SET enabled=false,version=version+1 WHERE id=$1`, first.ImageID); err != nil {
		t.Fatal(err)
	}
	replayed, err := repo.CreateOrReuseExcelCover(ctx, mediaport.ExcelCoverUpload{Actor: 9, IdempotencyKey: "excel-cover-concurrent-key-00-0000000000000000", FileName: "cover.png", DeclaredType: "image/png", Content: content})
	if err != nil || replayed.ImageID != first.ImageID {
		t.Fatalf("disabled original did not replay stable receipt: cover=%+v err=%v", replayed, err)
	}
	changed := append(append([]byte(nil), content...), 0)
	if _, err = repo.CreateOrReuseExcelCover(ctx, mediaport.ExcelCoverUpload{Actor: 9, IdempotencyKey: "excel-cover-concurrent-key-00-0000000000000000", FileName: "cover.png", DeclaredType: "image/png", Content: changed}); err == nil {
		t.Fatal("same idempotency key with changed image was accepted")
	}
	frozen, err := repo.ReadExcelCover(ctx, first.ImageID, first.ContentDigest)
	if err != nil || string(frozen.Bytes) != string(content) {
		t.Fatalf("frozen cover lost after disable: bytes=%d err=%v", len(frozen.Bytes), err)
	}
	replacement, err := repo.CreateOrReuseExcelCover(ctx, mediaport.ExcelCoverUpload{Actor: 9, IdempotencyKey: "excel-cover-after-disable-0000000000000000", FileName: "cover.png", DeclaredType: "image/png", Content: content})
	if err != nil || replacement.ImageID == first.ImageID {
		t.Fatalf("disabled image was revived or replacement failed: replacement=%+v err=%v", replacement, err)
	}
	unsupported, err := repo.CreateImage(ctx, 9, "excel-cover-unsupported-format-000000000000", ImageInput{FileName: "cover.gif", MIME: "image/gif", Name: "gif", Content: content, Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SelectEnabledExcelCover(ctx, unsupported["id"].(int64)); err == nil {
		t.Fatal("GIF was accepted as an Excel cover")
	}
	if _, err = native.Exec(ctx, `ALTER TABLE media_images DROP CONSTRAINT media_images_file_name_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE media_images SET file_name='' WHERE id=$1`, replacement.ImageID); err != nil {
		t.Fatal(err)
	}
	page, scanErr := repo.ListEnabledSourceSnapshots(ctx, mediaport.SnapshotPageRequest{Limit: 100})
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	foundFailure := false
	for _, failure := range page.Failures {
		if failure.SourceRef == "image:"+strconv.FormatInt(replacement.ImageID, 10) && failure.FailureCode == "invalid_source_metadata" {
			foundFailure = true
		}
	}
	if !foundFailure || !page.Done {
		t.Fatalf("corrupt source was not reported while scan continued: page=%+v", page)
	}
}

type materialAcceptanceRecorder struct {
	requests           []outboundport.MaterialRequest
	missingTransaction bool
	returnError        error
}

func (r *materialAcceptanceRecorder) AcceptMaterialPreparationWithin(ctx context.Context, request outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		r.missingTransaction = true
	}
	r.requests = append(r.requests, request)
	return outboundport.MaterialResult{}, r.returnError
}

func TestPostgreSQLMediaMaterialAcceptanceSharesMutationTransaction(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_acceptance_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	for _, migration := range []string{"0007_media.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migration))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("%s: %v", migration, execErr)
		}
	}
	if _, err = native.Exec(ctx, `CREATE TABLE ai_assistant_excel_batch_versions (plan_id BIGINT,content_revision INTEGER,file_digest TEXT,cover_digest TEXT,created_by BIGINT,created_at TIMESTAMPTZ);
CREATE TABLE ai_assistant_excel_batch_version_covers (plan_id BIGINT,content_revision INTEGER,cover_digest TEXT,created_by BIGINT,created_at TIMESTAMPTZ);`); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0126_media_material_source_snapshots.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &materialAcceptanceRecorder{}
	if err = repo.BindMaterialPreparationAccepter(recorder, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}

	disabled, err := repo.CreateImage(ctx, 9, "material-accept-disabled-image-0001", ImageInput{FileName: "disabled.png", MIME: "image/png", Name: "disabled", Content: testPNG(t), Width: 2, Height: 2, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.requests) != 0 {
		t.Fatalf("disabled create accepted preparation: %+v", recorder.requests)
	}
	if _, err = repo.UpdateImage(ctx, disabled["id"].(int64), 9, "material-accept-enable-image-0001", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	attachment, err := repo.CreateAttachment(ctx, 9, "material-accept-attachment-0001", AttachmentInput{FileName: "guide.pdf", Name: "guide", Content: []byte("%PDF-1.4\nbody"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseExcelCover(ctx, mediaport.ExcelCoverUpload{Actor: 9, IdempotencyKey: "material-accept-excel-cover-0001", FileName: "cover.png", DeclaredType: "image/png", Content: testPNG(t)}); err != nil {
		t.Fatal(err)
	}
	if recorder.missingTransaction {
		t.Fatal("material acceptance did not receive the existing PostgreSQL transaction")
	}
	if len(recorder.requests) != 3 {
		t.Fatalf("accept calls=%d requests=%+v", len(recorder.requests), recorder.requests)
	}
	if recorder.requests[0].SourceRef != "image:"+strconv.FormatInt(disabled["id"].(int64), 10) || recorder.requests[1].SourceRef != "attachment:"+strconv.FormatInt(attachment["id"].(int64), 10) || recorder.requests[2].SourceType != "image" {
		t.Fatalf("unexpected accepted source sequence: %+v", recorder.requests)
	}
	for _, request := range recorder.requests {
		if request.CorpScopeDigest == "" || request.SnapshotVersion < 1 || request.ContentDigest == [32]byte{} {
			t.Fatalf("incomplete accepted request: %+v", request)
		}
	}

	recorder.returnError = errors.New("durable acceptance rejected")
	if _, err = repo.CreateImage(ctx, 9, "material-accept-rollback-image-0001", ImageInput{FileName: "rollback.png", MIME: "image/png", Name: "rollback", Content: testPNG(t), Width: 2, Height: 2, Enabled: true}); err == nil {
		t.Fatal("image write succeeded after durable acceptance failed")
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_images WHERE name='rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("acceptance failure did not roll back image: count=%d err=%v", count, err)
	}
}
