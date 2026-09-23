package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ mediaport.SourceScanner = (*Repository)(nil)
var _ outboundport.MaterialSourceReader = (*Repository)(nil)

const (
	defaultSourcePageLimit = 100
	maximumSourcePageLimit = 500
)

type sourceCursor struct {
	Rank int
	ID   int64
}

func (r *Repository) ListEnabledSourceSnapshots(ctx context.Context, request mediaport.SnapshotPageRequest) (mediaport.SnapshotPage, error) {
	if r == nil || ctx == nil {
		return mediaport.SnapshotPage{}, mediaport.ErrSourceUnavailable
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultSourcePageLimit
	}
	if limit < 1 || limit > maximumSourcePageLimit {
		return mediaport.SnapshotPage{}, mediaport.ErrSourceUnavailable
	}
	cursor, err := decodeSourceCursor(request.Cursor)
	if err != nil {
		return mediaport.SnapshotPage{}, mediaport.ErrSourceUnavailable
	}
	page := mediaport.SnapshotPage{Items: []mediaport.SourceSnapshot{}, Failures: []outboundport.MaterialSourceFailure{}}
	err = r.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(txctx, `WITH sources AS (
 SELECT 1 AS source_rank,id,'image:' || id::text AS source_ref,'image' AS source_type,blob_digest,file_name,mime_type,byte_size,version
 FROM media_images WHERE enabled
 UNION ALL
 SELECT 2 AS source_rank,id,'attachment:' || id::text AS source_ref,'file' AS source_type,blob_digest,file_name,mime_type,byte_size,version
 FROM media_attachments WHERE enabled
)
SELECT sources.source_rank,sources.id,sources.source_ref,sources.source_type,sources.blob_digest,sources.file_name,sources.mime_type,sources.byte_size,sources.version,blobs.digest IS NOT NULL
FROM sources LEFT JOIN media_blobs blobs ON blobs.digest=sources.blob_digest
WHERE sources.source_rank > $1 OR (sources.source_rank = $1 AND sources.id > $2)
ORDER BY sources.source_rank,sources.id LIMIT $3`, cursor.Rank, cursor.ID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		type row struct {
			rank        int
			id          int64
			snapshot    mediaport.SourceSnapshot
			digest      string
			blobPresent bool
		}
		// PostgreSQL permits only one active query on a transaction connection.
		// Material snapshot inserts therefore happen after the page rows are closed.
		rowsRead := 0
		delivered := make([]row, 0, limit)
		for rows.Next() {
			var value row
			if err = rows.Scan(&value.rank, &value.id, &value.snapshot.SourceRef, &value.snapshot.SourceType, &value.digest, &value.snapshot.FileName, &value.snapshot.MediaType, &value.snapshot.SizeBytes, &value.snapshot.SnapshotVersion, &value.blobPresent); err != nil {
				return err
			}
			rowsRead++
			if rowsRead > limit {
				break
			}
			delivered = append(delivered, value)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for _, value := range delivered {
			if !value.blobPresent {
				page.Failures = append(page.Failures, outboundport.MaterialSourceFailure{SourceRef: value.snapshot.SourceRef, FailureCode: "source_blob_missing"})
				continue
			}
			if value.snapshot.ContentDigest, err = sourceDigest(value.digest); err != nil || !mediaport.ValidSourceSnapshot(value.snapshot) {
				// A legacy-corrupt row is terminal for this round, but its cursor
				// position is still delivered so later sources are never blocked.
				page.Failures = append(page.Failures, outboundport.MaterialSourceFailure{SourceRef: value.snapshot.SourceRef, FailureCode: "invalid_source_metadata"})
				continue
			}
			if _, err = tx.Exec(txctx, `INSERT INTO media_material_source_snapshots(source_ref,snapshot_version,source_type,content_digest,blob_digest,file_name,media_type,byte_size)
VALUES($1,$2,$3,$4,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, value.snapshot.SourceRef, value.snapshot.SnapshotVersion, value.snapshot.SourceType, value.digest, value.snapshot.FileName, value.snapshot.MediaType, value.snapshot.SizeBytes); err != nil {
				return err
			}
			page.Items = append(page.Items, value.snapshot)
		}
		if rowsRead > limit {
			lastDelivered := delivered[len(delivered)-1]
			page.NextCursor = encodeSourceCursor(sourceCursor{Rank: lastDelivered.rank, ID: lastDelivered.id})
		}
		page.Done = rowsRead <= limit
		return nil
	})
	if err != nil {
		return mediaport.SnapshotPage{}, mediaport.ErrSourceUnavailable
	}
	return page, nil
}
func (r *Repository) GetSourceSnapshot(ctx context.Context, sourceRef string) (mediaport.SourceSnapshot, error) {
	if r == nil || ctx == nil {
		return mediaport.SourceSnapshot{}, mediaport.ErrSourceUnavailable
	}
	var result mediaport.SourceSnapshot
	err := r.Within(ctx, func(txctx context.Context) error {
		var snapshotErr error
		result, snapshotErr = r.sourceSnapshotWithin(txctx, sourceRef)
		return snapshotErr
	})
	if errors.Is(err, mediaport.ErrSourceNotFound) {
		return mediaport.SourceSnapshot{}, mediaport.ErrSourceNotFound
	}
	if err != nil {
		return mediaport.SourceSnapshot{}, mediaport.ErrSourceUnavailable
	}
	return result, nil
}

// sourceSnapshotWithin records the immutable Media source evidence in the
// caller's existing transaction. It intentionally has no Provider dependency.
func (r *Repository) sourceSnapshotWithin(ctx context.Context, sourceRef string) (mediaport.SourceSnapshot, error) {
	kind, id, err := parseSourceRef(sourceRef)
	if err != nil {
		return mediaport.SourceSnapshot{}, mediaport.ErrSourceUnavailable
	}
	table, sourceType := "media_images", "image"
	if kind == "attachment" {
		table, sourceType = "media_attachments", "file"
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.SourceSnapshot{}, err
	}
	var result mediaport.SourceSnapshot
	var digest string
	query := fmt.Sprintf(`SELECT blob_digest,file_name,mime_type,byte_size,version FROM %s WHERE id=$1 AND enabled`, table)
	if err = tx.QueryRow(ctx, query, id).Scan(&digest, &result.FileName, &result.MediaType, &result.SizeBytes, &result.SnapshotVersion); errors.Is(err, pgx.ErrNoRows) {
		return mediaport.SourceSnapshot{}, mediaport.ErrSourceNotFound
	} else if err != nil {
		return mediaport.SourceSnapshot{}, err
	}
	result.SourceRef, result.SourceType = sourceRef, sourceType
	if result.ContentDigest, err = sourceDigest(digest); err != nil || !mediaport.ValidSourceSnapshot(result) {
		return mediaport.SourceSnapshot{}, mediaport.ErrSourceUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_material_source_snapshots(source_ref,snapshot_version,source_type,content_digest,blob_digest,file_name,media_type,byte_size)
VALUES($1,$2,$3,$4,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, result.SourceRef, result.SnapshotVersion, result.SourceType, digest, result.FileName, result.MediaType, result.SizeBytes); err != nil {
		return mediaport.SourceSnapshot{}, err
	}
	return result, nil
}

func (r *Repository) ReadSourceBytes(ctx context.Context, request mediaport.SourceReadRequest) (mediaport.SourceContent, error) {
	if r == nil || ctx == nil || !sourceRefValid(request.SourceRef) || request.SnapshotVersion < 1 || request.ContentDigest == [sha256.Size]byte{} {
		return mediaport.SourceContent{}, outboundport.ErrMaterialSourceChanged
	}
	contentDigest := "sha256:" + hex.EncodeToString(request.ContentDigest[:])
	var result mediaport.SourceContent
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		err = tx.QueryRow(txctx, `SELECT snapshot.file_name,snapshot.media_type,blob.content
FROM media_material_source_snapshots snapshot
JOIN media_blobs blob ON blob.digest=snapshot.blob_digest
WHERE snapshot.source_ref=$1 AND snapshot.snapshot_version=$2 AND snapshot.content_digest=$3`, request.SourceRef, request.SnapshotVersion, contentDigest).Scan(&result.FileName, &result.MediaType, &result.Bytes)
		if errors.Is(err, pgx.ErrNoRows) {
			return outboundport.ErrMaterialSourceChanged
		}
		if err != nil || len(result.Bytes) == 0 || result.FileName == "" || result.MediaType == "" {
			if err != nil {
				return err
			}
			return outboundport.ErrMaterialSourceChanged
		}
		got := sha256.Sum256(result.Bytes)
		if got != request.ContentDigest {
			return outboundport.ErrMaterialSourceChanged
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, outboundport.ErrMaterialSourceChanged) {
			return mediaport.SourceContent{}, outboundport.ErrMaterialSourceChanged
		}
		return mediaport.SourceContent{}, mediaport.ErrSourceUnavailable
	}
	return result, nil
}

func sourceDigest(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return result, mediaport.ErrSourceUnavailable
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return result, mediaport.ErrSourceUnavailable
	}
	copy(result[:], decoded)
	return result, nil
}

func sourceRefValid(value string) bool {
	return mediaport.ValidSourceRef(value)
}

func parseSourceRef(value string) (string, int64, error) {
	if !mediaport.ValidSourceRef(value) {
		return "", 0, mediaport.ErrSourceUnavailable
	}
	for _, prefix := range []string{"image:", "attachment:"} {
		if strings.HasPrefix(value, prefix) {
			id, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
			if err != nil || id < 1 {
				return "", 0, mediaport.ErrSourceUnavailable
			}
			return strings.TrimSuffix(prefix, ":"), id, nil
		}
	}
	return "", 0, mediaport.ErrSourceUnavailable
}

func encodeSourceCursor(cursor sourceCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", cursor.Rank, cursor.ID)))
}

func decodeSourceCursor(value string) (sourceCursor, error) {
	if value == "" {
		return sourceCursor{Rank: 1}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return sourceCursor{}, err
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 {
		return sourceCursor{}, mediaport.ErrSourceUnavailable
	}
	rank, err := strconv.Atoi(parts[0])
	if err != nil || rank < 1 || rank > 2 {
		return sourceCursor{}, mediaport.ErrSourceUnavailable
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 0 {
		return sourceCursor{}, mediaport.ErrSourceUnavailable
	}
	return sourceCursor{Rank: rank, ID: id}, nil
}
