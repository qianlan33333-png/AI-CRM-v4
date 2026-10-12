package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ mediaport.PrivateImageStorage = (*Repository)(nil)

func (r *Repository) InspectPrivateImage(input mediaport.PrivateImageUpload) (mediaport.PrivateImageInfo, error) {
	inspection, e := domain.Inspect(input.FileName, input.MediaType, input.Content)
	if e != nil {
		return mediaport.PrivateImageInfo{}, mediaport.ErrPrivateImageInvalid
	}
	return mediaport.PrivateImageInfo{FileName: input.FileName, MediaType: inspection.MediaType, SizeBytes: int64(len(input.Content)), Width: inspection.Width, Height: inspection.Height, Digest: sha256.Sum256(input.Content)}, nil
}
func (r *Repository) StorePrivateImageWithin(ctx context.Context, input mediaport.PrivateImageUpload, expected mediaport.PrivateImageInfo) (mediaport.PrivateImageInfo, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return mediaport.PrivateImageInfo{}, e
	}
	// Check the prepared immutable bytes, without decoding again under the lock.
	if expected.Ref != "" || expected.FileName != input.FileName || expected.SizeBytes != int64(len(input.Content)) || expected.SizeBytes < 1 || expected.SizeBytes > domain.MaxImageBytes ||
		expected.Digest != sha256.Sum256(input.Content) || expected.Width < 1 || expected.Height < 1 ||
		expected.Width > domain.MaxImageSide || expected.Height > domain.MaxImageSide || int64(expected.Width)*int64(expected.Height) > domain.MaxImagePixels ||
		(expected.MediaType != "image/png" && expected.MediaType != "image/jpeg" && expected.MediaType != "image/gif") {
		return mediaport.PrivateImageInfo{}, mediaport.ErrPrivateImageInvalid
	}
	var random [32]byte
	if _, e = rand.Read(random[:]); e != nil {
		return mediaport.PrivateImageInfo{}, e
	}
	expected.Ref = base64.RawURLEncoding.EncodeToString(random[:])
	digest := bytesDigest(input.Content)
	if _, e = tx.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,$2,$3,$4) ON CONFLICT(digest) DO NOTHING`, digest, expected.MediaType, expected.SizeBytes, input.Content); e != nil {
		return mediaport.PrivateImageInfo{}, e
	}
	// No media_images row: private answers cannot enter public library routes,
	// provider scans, admin material selection or material preparation.
	_, e = tx.Exec(ctx, `INSERT INTO media_private_images(ref,blob_digest,file_name,mime_type,byte_size,width,height) VALUES($1,$2,$3,$4,$5,$6,$7)`, expected.Ref, digest, expected.FileName, expected.MediaType, expected.SizeBytes, expected.Width, expected.Height)
	return expected, e
}
func (r *Repository) ReadPrivateImageWithin(ctx context.Context, ref string) (mediaport.PrivateImageContent, error) {
	var result mediaport.PrivateImageContent
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return result, e
	}
	if decoded, e := base64.RawURLEncoding.DecodeString(ref); e != nil || len(decoded) != 32 {
		return result, mediaport.ErrPrivateImageNotFound
	}
	var digest string
	e = tx.QueryRow(ctx, `SELECT image.ref,image.file_name,image.mime_type,image.byte_size,image.width,image.height,blob.digest,blob.content FROM media_private_images image JOIN media_blobs blob ON blob.digest=image.blob_digest WHERE image.ref=$1`, ref).Scan(&result.Info.Ref, &result.Info.FileName, &result.Info.MediaType, &result.Info.SizeBytes, &result.Info.Width, &result.Info.Height, &digest, &result.Content)
	if errors.Is(e, pgx.ErrNoRows) {
		return result, mediaport.ErrPrivateImageNotFound
	}
	if e != nil {
		return result, e
	}
	result.Info.Digest = sha256.Sum256(result.Content)
	if int64(len(result.Content)) != result.Info.SizeBytes || bytesDigest(result.Content) != digest {
		return mediaport.PrivateImageContent{}, mediaport.ErrPrivateImageInvalid
	}
	return result, nil
}
