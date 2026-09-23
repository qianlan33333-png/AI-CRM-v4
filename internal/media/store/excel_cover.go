package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ mediaport.ExcelCoverLibrary = (*Repository)(nil)
var _ mediaport.ExcelCoverReader = (*Repository)(nil)

// CreateOrReuseExcelCover intentionally reuses only an enabled image. A
// disabled image is evidence of an operator decision and is never revived by
// an Excel upload; a new stable Media image is created instead.
func (r *Repository) CreateOrReuseExcelCover(ctx context.Context, command mediaport.ExcelCoverUpload) (mediaport.ExcelCover, error) {
	if r == nil || command.Actor < 1 || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 || strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey {
		return mediaport.ExcelCover{}, ErrInvalid
	}
	inspection, err := domain.Inspect(command.FileName, command.DeclaredType, command.Content)
	if err != nil {
		return mediaport.ExcelCover{}, ErrInvalid
	}
	digest := bytesDigest(command.Content)
	var cover mediaport.ExcelCover
	err = r.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		commandJSON, marshalErr := json.Marshal(struct {
			Digest, FileName, MediaType string
			Width, Height               int32
		}{digest, command.FileName, inspection.MediaType, inspection.Width, inspection.Height})
		if marshalErr != nil {
			return marshalErr
		}
		replay, owned, reserveErr := r.reserve(txctx, "excel_cover.upload", "image", command.Actor, command.IdempotencyKey, string(commandJSON))
		if reserveErr != nil {
			return reserveErr
		}
		if !owned {
			var stored struct {
				ImageID int64  `json:"image_id"`
				Digest  string `json:"content_digest"`
			}
			if json.Unmarshal(replay, &stored) != nil || stored.ImageID < 1 || stored.Digest != digest {
				return ErrConflict
			}
			decoded, decodeErr := sourceDigest(stored.Digest)
			if decodeErr != nil {
				return ErrConflict
			}
			cover = mediaport.ExcelCover{ImageID: stored.ImageID, ContentDigest: decoded}
			return nil
		}
		if _, reserveErr = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "excel-cover:"+digest); reserveErr != nil {
			return reserveErr
		}
		rows, queryErr := tx.Query(txctx, `SELECT id,enabled FROM media_images WHERE blob_digest=$1 ORDER BY id FOR UPDATE`, digest)
		if queryErr != nil {
			return queryErr
		}
		var imageID int64
		for rows.Next() {
			var candidate int64
			var enabled bool
			if queryErr = rows.Scan(&candidate, &enabled); queryErr != nil {
				rows.Close()
				return queryErr
			}
			if enabled && imageID == 0 {
				imageID = candidate
			}
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return queryErr
		}
		rows.Close()
		created := imageID == 0
		if created {
			if _, queryErr = tx.Exec(txctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,$2,$3,$4) ON CONFLICT(digest) DO NOTHING`, digest, inspection.MediaType, len(command.Content), command.Content); queryErr != nil {
				return queryErr
			}
			queryErr = tx.QueryRow(txctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by)
VALUES($1,$2,'Excel 批次封面','','','excel_batch_cover',$3,$4,$5,$6,true,$7,$7) RETURNING id`, digest, command.FileName, inspection.MediaType, len(command.Content), inspection.Width, inspection.Height, command.Actor).Scan(&imageID)
			if queryErr != nil {
				return queryErr
			}
		}
		decoded, decodeErr := sourceDigest(digest)
		if decodeErr != nil {
			return decodeErr
		}
		cover = mediaport.ExcelCover{ImageID: imageID, ContentDigest: decoded}
		if err = r.recordExcelCoverSnapshotWithin(txctx, cover); err != nil {
			return err
		}
		if err = r.acceptMaterialPreparationWithin(txctx, "image:"+strconv.FormatInt(imageID, 10)); err != nil {
			return err
		}
		snapshot := map[string]any{"image_id": cover.ImageID, "content_digest": digest}
		event := "media.excel_cover_reused"
		if created {
			event = "media.excel_cover_uploaded"
		}
		return r.complete(txctx, "excel_cover.upload", "image", command.Actor, command.IdempotencyKey, imageID, snapshot, event)
	})
	if err != nil {
		return mediaport.ExcelCover{}, err
	}
	return cover, nil
}

func (r *Repository) SelectEnabledExcelCover(ctx context.Context, imageID int64) (mediaport.ExcelCover, error) {
	if r == nil || imageID < 1 {
		return mediaport.ExcelCover{}, ErrInvalid
	}
	var digest, mediaType string
	var size int64
	var cover mediaport.ExcelCover
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		err = tx.QueryRow(txctx, `SELECT blob_digest,mime_type,byte_size FROM media_images WHERE id=$1 AND enabled FOR UPDATE`, imageID).Scan(&digest, &mediaType, &size)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if (mediaType != "image/png" && mediaType != "image/jpeg") || size < 1 || size > 2<<20 {
			return ErrInvalid
		}
		value, decodeErr := sourceDigest(digest)
		if decodeErr != nil {
			return ErrConflict
		}
		cover = mediaport.ExcelCover{ImageID: imageID, ContentDigest: value}
		if err = r.recordExcelCoverSnapshotWithin(txctx, cover); err != nil {
			return err
		}
		return r.acceptMaterialPreparationWithin(txctx, "image:"+strconv.FormatInt(imageID, 10))
	})
	if err != nil {
		return mediaport.ExcelCover{}, err
	}
	return cover, nil
}

func (r *Repository) recordExcelCoverSnapshot(ctx context.Context, cover mediaport.ExcelCover) error {
	return r.Within(ctx, func(txctx context.Context) error {
		return r.recordExcelCoverSnapshotWithin(txctx, cover)
	})
}

func (r *Repository) recordExcelCoverSnapshotWithin(ctx context.Context, cover mediaport.ExcelCover) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var sourceType, digest, fileName, mediaType string
	var size, version int64
	err = tx.QueryRow(ctx, `SELECT 'image',blob_digest,file_name,mime_type,byte_size,version FROM media_images WHERE id=$1`, cover.ImageID).Scan(&sourceType, &digest, &fileName, &mediaType, &size, &version)
	if err != nil {
		return err
	}
	if digest != "sha256:"+hex.EncodeToString(cover.ContentDigest[:]) {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_material_source_snapshots(source_ref,snapshot_version,source_type,content_digest,blob_digest,file_name,media_type,byte_size)
VALUES($1,$2,$3,$4,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, "image:"+strconv.FormatInt(cover.ImageID, 10), version, sourceType, digest, fileName, mediaType, size)
	return err
}

func (r *Repository) ReadExcelCover(ctx context.Context, imageID int64, digest [32]byte) (mediaport.SourceContent, error) {
	if r == nil || imageID < 1 || digest == [32]byte{} {
		return mediaport.SourceContent{}, ErrInvalid
	}
	request := mediaport.SourceReadRequest{SourceRef: "image:" + strconv.FormatInt(imageID, 10), ContentDigest: digest}
	var result mediaport.SourceContent
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		digestText := "sha256:" + hex.EncodeToString(digest[:])
		err = tx.QueryRow(txctx, `SELECT snapshot.file_name,snapshot.media_type,blob.content
FROM media_material_source_snapshots snapshot JOIN media_blobs blob ON blob.digest=snapshot.blob_digest
WHERE snapshot.source_ref=$1 AND snapshot.content_digest=$2
ORDER BY snapshot.created_at DESC LIMIT 1`, request.SourceRef, digestText).Scan(&result.FileName, &result.MediaType, &result.Bytes)
		return err
	})
	if err != nil || sha256.Sum256(result.Bytes) != digest {
		return mediaport.SourceContent{}, ErrNotFound
	}
	return result, nil
}
