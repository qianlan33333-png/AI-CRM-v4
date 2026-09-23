package app

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidImageDetail     = errors.New("invalid image detail")
	ErrImageDetailNotFound    = errors.New("image detail not found")
	ErrImageDetailUnavailable = errors.New("image detail unavailable")
)

// ImageDetailRow is the one statement Media-owned metadata-plus-blob read.
// It deliberately remains app-local so this legacy projection does not widen
// the shared Media port.
type ImageDetailRow struct {
	ID                          int64
	Name, FileName, MimeType    string
	FileSize                    int32
	Enabled                     bool
	Description, Tags, Category string
	Width, Height               int32
	CreatedAt, UpdatedAt        time.Time
	ImageChecksum, BlobChecksum []byte
	Content                     []byte
}

type ImageDetailStore interface {
	ReadImageDetail(context.Context, int64) (ImageDetailRow, error)
}

// ImageDetail is fully validated in the app layer before the HTTP layer
// chooses whether legacy compatibility exposes its original bytes.
type ImageDetail struct {
	ID                       int64
	Name, FileName, MimeType string
	FileSize                 int32
	Enabled                  bool
	Description, Category    string
	Tags                     []string
	Width, Height            int32
	CreatedAt, UpdatedAt     time.Time
	Content                  []byte
}

func (service *Service) GetImageDetail(ctx context.Context, imageID int64) (ImageDetail, error) {
	if imageID < 1 {
		return ImageDetail{}, ErrInvalidImageDetail
	}
	if service == nil || ctx == nil || service.uow == nil || service.store == nil {
		return ImageDetail{}, ErrImageDetailUnavailable
	}
	store, ok := service.store.(ImageDetailStore)
	if !ok {
		return ImageDetail{}, ErrImageDetailUnavailable
	}

	var row ImageDetailRow
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		value, err := store.ReadImageDetail(tx, imageID)
		if err != nil {
			return err
		}
		row = value
		return nil
	}); err != nil {
		if errors.Is(err, ErrImageDetailNotFound) {
			return ImageDetail{}, ErrImageDetailNotFound
		}
		return ImageDetail{}, ErrImageDetailUnavailable
	}
	if !validImageDetailMetadata(row) {
		return ImageDetail{}, ErrImageDetailUnavailable
	}
	if _, err := inspectImageVariantRow(ImageVariantRow{
		ID: row.ID, FileName: row.FileName, MimeType: row.MimeType, FileSize: row.FileSize,
		Width: row.Width, Height: row.Height, ImageChecksum: row.ImageChecksum,
		BlobChecksum: row.BlobChecksum, Content: row.Content,
	}, imageID); err != nil {
		return ImageDetail{}, ErrImageDetailUnavailable
	}
	return ImageDetail{
		ID: row.ID, Name: row.Name, FileName: row.FileName, MimeType: row.MimeType, FileSize: row.FileSize, Enabled: row.Enabled,
		Description: row.Description, Category: row.Category, Tags: normalizeImageListTags(row.Tags), Width: row.Width,
		Height: row.Height, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), Content: append([]byte(nil), row.Content...),
	}, nil
}

func validImageDetailMetadata(row ImageDetailRow) bool {
	return row.ID > 0 && !row.CreatedAt.IsZero() && !row.UpdatedAt.IsZero() &&
		utf8.ValidString(row.Name) && utf8.ValidString(row.FileName) && utf8.ValidString(row.MimeType) &&
		utf8.ValidString(row.Description) && utf8.ValidString(row.Tags) && utf8.ValidString(row.Category) &&
		utf8.RuneCountInString(row.Name) <= 200 && utf8.RuneCountInString(row.Description) <= 10_000 &&
		utf8.RuneCountInString(row.Tags) <= 10_000 && utf8.RuneCountInString(row.Category) <= 200
}
