package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	"testing"
	"time"
)

func TestPostgreSQLManualImageLimitKeepsCompletedReceipts(t *testing.T) {
	repo, db := retentionRepository(t)
	ctx := context.Background()
	recorder := &materialAcceptanceRecorder{}
	if err := repo.BindMaterialPreparationAccepter(recorder, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	image := testPNG(t)
	makeInput := func(size int) ImageInput {
		return ImageInput{FileName: "boundary.png", Name: "boundary", MIME: "image/png", Content: append(append([]byte{}, image...), make([]byte, size-len(image))...), Width: 2, Height: 2}
	}
	large := makeInput(mediaport.ManualImageUploadMaxBytes + 1)
	large.Enabled = true
	if _, err := repo.CreateImage(ctx, 7, "new-large-image-0001", large); !errors.Is(err, mediaport.ErrImageUploadTooLarge) {
		t.Fatalf("oversize=%v", err)
	}
	if _, err := repo.CreateOrReuseExcelCover(ctx, mediaport.ExcelCoverUpload{Actor: 7, IdempotencyKey: "new-large-cover-0001", FileName: large.FileName, DeclaredType: large.MIME, Content: large.Content}); !errors.Is(err, mediaport.ErrImageUploadTooLarge) {
		t.Fatalf("cover oversize=%v", err)
	}
	for _, table := range []string{"media_images", "media_blobs", "media_operation_receipts", "media_audit_events", "media_outbox"} {
		var count int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rejection persisted %s: count=%d err=%v", table, count, err)
		}
	}
	if len(recorder.requests) != 0 {
		t.Fatal("rejected upload accepted external preparation")
	}
	for i, size := range []int{mediaport.ManualImageUploadMaxBytes - 1, mediaport.ManualImageUploadMaxBytes} {
		key := []string{"allowed-boundary-low-0001", "allowed-boundary-exact-0001"}[i]
		input := makeInput(size)
		first, err := repo.CreateImage(ctx, 7, key, input)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := repo.CreateImage(ctx, 7, key, input)
		if err != nil || replay["id"].(float64) != float64(first["id"].(int64)) {
			t.Fatalf("replay=%v %v", replay, err)
		}
	}
	// Reproduce a completed pre-policy accept without disabling the new limit.
	large.Enabled = false
	now := time.Now()
	blob := bytesDigest(large.Content)
	if _, err := db.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,$2,$3,$4)`, blob, large.MIME, len(large.Content), large.Content); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.QueryRow(ctx, `INSERT INTO media_images(blob_digest,file_name,name,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5,2,2,false,7,7) RETURNING id`, blob, large.FileName, large.Name, large.MIME, len(large.Content)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(struct {
		F, M, N, D, T, C, H string
		S                   int
		W, X                int32
		E                   bool
	}{large.FileName, large.MIME, large.Name, "", "", "", blob, len(large.Content), 2, 2, false})
	snapshot, _ := json.Marshal(imageMap(id, large.FileName, large.Name, "", "", "", large.MIME, int64(len(large.Content)), 2, 2, false, now, now))
	key := "completed-old-large-image-0001"
	if _, err := db.Exec(ctx, `INSERT INTO media_operation_receipts(operation,actor_admin_user_id,resource_kind,resource_id,idempotency_key_digest,command_digest,result) VALUES('image.create',7,'image',$1,$2,$3,$4)`, id, bytesDigest([]byte(key)), bytesDigest(payload), snapshot); err != nil {
		t.Fatal(err)
	}
	replay, err := repo.CreateImage(ctx, 7, key, large)
	if err != nil || replay["id"].(float64) != float64(id) {
		t.Fatalf("legacy replay=%v %v", replay, err)
	}
	large.Name = "changed"
	if _, err := repo.CreateImage(ctx, 7, key, large); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy drift=%v", err)
	}
	var stored []byte
	if err := db.QueryRow(ctx, `SELECT content FROM media_blobs WHERE digest=$1`, blob).Scan(&stored); err != nil || !bytes.Equal(stored, large.Content) {
		t.Fatal("legacy original changed")
	}
}
