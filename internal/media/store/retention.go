package store

import (
	"context"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ mediaport.UploadPartRetention = (*Repository)(nil)

const expiredUploadParts = `
FROM media_attachment_upload_parts part
JOIN media_attachment_uploads upload ON upload.id=part.upload_id
WHERE part.created_at < $1 AND upload.expires_at < $1
AND (upload.completed_attachment_id IS NULL OR EXISTS (
 SELECT 1 FROM media_blobs blob
 WHERE blob.digest=upload.expected_digest AND blob.byte_size=upload.expected_size
))`

// CleanupExpiredUploadParts uses only Media-owned data. A completed upload's
// durable original must still exist before its duplicate staging bytes can go.
// Upload headers/keys, receipts, source snapshots, and material blobs survive.
func (r *Repository) CleanupExpiredUploadParts(ctx context.Context, command mediaport.UploadPartRetentionCommand) (mediaport.UploadPartRetentionReport, error) {
	report := mediaport.UploadPartRetentionReport{Before: command.Before.UTC()}
	if r == nil || ctx == nil || command.Before.IsZero() || command.Limit < 1 || command.Limit > 1000 {
		return report, mediaport.ErrRetentionRequest
	}
	err := r.Within(ctx, func(txctx context.Context) error {
		var err error
		report, err = r.CleanupExpiredUploadPartsWithin(txctx, command)
		return err
	})
	return report, err
}

// CleanupExpiredUploadPartsWithin joins an explicit caller transaction so
// deletion and the maintenance receipt cannot commit independently.
func (r *Repository) CleanupExpiredUploadPartsWithin(ctx context.Context, command mediaport.UploadPartRetentionCommand) (mediaport.UploadPartRetentionReport, error) {
	report := mediaport.UploadPartRetentionReport{Before: command.Before.UTC()}
	if r == nil || ctx == nil || command.Before.IsZero() || command.Limit < 1 || command.Limit > 1000 {
		return report, mediaport.ErrRetentionRequest
	}
	execute := func(txctx context.Context) error {

		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		var now time.Time
		if err = tx.QueryRow(txctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if command.Before.After(now.Add(-30 * 24 * time.Hour)) {
			return mediaport.ErrRetentionRequest
		}
		if !command.Apply {
			return tx.QueryRow(txctx, `SELECT LEAST(count(*),$2),COALESCE(sum(size) FILTER(WHERE position<=$2),0),count(*)>$2 FROM (
 SELECT octet_length(part.content) AS size,row_number() OVER(ORDER BY part.upload_id,part.part_number) AS position `+expiredUploadParts+`
 ORDER BY part.upload_id,part.part_number LIMIT $2+1) candidates`, command.Before, command.Limit).Scan(&report.Candidates, &report.Bytes, &report.Remaining)
		}
		// Serialize owner cleanup independently of scheduler retries; this lock
		// never competes with unrelated business work.
		var locked bool
		if err = tx.QueryRow(txctx, `SELECT pg_try_advisory_xact_lock(hashtext('media.upload_part_retention'))`).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return ErrConflict
		}
		if err = tx.QueryRow(txctx, `WITH candidates AS (
 SELECT part.upload_id,part.part_number `+expiredUploadParts+`
 ORDER BY part.upload_id,part.part_number LIMIT $2 FOR UPDATE OF part,upload SKIP LOCKED
), deleted AS (
 DELETE FROM media_attachment_upload_parts part USING candidates
 WHERE part.upload_id=candidates.upload_id AND part.part_number=candidates.part_number
 RETURNING octet_length(part.content) AS size
) SELECT count(*),COALESCE(sum(size),0) FROM deleted`, command.Before, command.Limit).Scan(&report.Deleted, &report.Bytes); err != nil {
			return err
		}
		report.Candidates = report.Deleted
		return tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 `+expiredUploadParts+` LIMIT 1)`, command.Before).Scan(&report.Remaining)

	}
	err := execute(ctx)
	return report, err
}
