package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrInvalidImageMetadataUpdate = errors.New("invalid image metadata update")
	ErrImageMetadataNotFound      = errors.New("image metadata not found")
	ErrImageMetadataUnavailable   = errors.New("image metadata unavailable")
)

// ImageMetadataPatch represents the explicitly supplied, mutable image fields.
// Pointers preserve the distinction between an omitted field and a zero value.
type ImageMetadataPatch struct {
	Name        *string
	Description *string
	Tags        *[]string
	Category    *string
	Enabled     *bool
}

type ImageMetadataUpdateCommand struct {
	ImageID int64
	Actor   int64
	Patch   ImageMetadataPatch
}

// ImageMetadata is the metadata projection used by the local image-update path.
type ImageMetadata struct {
	ID          int64
	Name        string
	FileName    string
	MimeType    string
	FileSize    int64
	Enabled     bool
	Description string
	Tags        string
	Category    string
	Width       int
	Height      int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ImageMetadataUpdateStore interface {
	LockImageMetadata(ctx context.Context, imageID int64) (ImageMetadata, error)
	UpdateImageMetadata(ctx context.Context, image ImageMetadata) (ImageMetadata, error)
}

// ImageMetadataService is the write-side application boundary for mutable
// image metadata. It is separate from the read-side Service because the
// latter intentionally has no receipt/event dependencies.
type ImageMetadataService struct {
	uow    platformport.UnitOfWork
	store  ImageMetadataUpdateStore
	events mediaport.EventAppender
	now    func() time.Time
}

func NewImageMetadataService(uow platformport.UnitOfWork, store ImageMetadataUpdateStore, events mediaport.EventAppender) *ImageMetadataService {
	return &ImageMetadataService{uow: uow, store: store, events: events, now: time.Now}
}

// UpdateImageMetadata updates metadata and enabled state in the caller's UoW.
// It deliberately uses a local store capability so that the central Media port
// remains unchanged.
func (s *ImageMetadataService) UpdateImageMetadata(ctx context.Context, command ImageMetadataUpdateCommand) (ImageMetadata, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.now == nil {
		return ImageMetadata{}, ErrImageMetadataUnavailable
	}
	if command.ImageID <= 0 || command.Actor <= 0 {
		return ImageMetadata{}, ErrInvalidImageMetadataUpdate
	}
	patch, err := normalizeImageMetadataPatch(command.Patch)
	if err != nil {
		return ImageMetadata{}, err
	}
	store, ok := s.store.(ImageMetadataUpdateStore)
	if !ok || store == nil {
		return ImageMetadata{}, ErrImageMetadataUnavailable
	}

	var result ImageMetadata
	err = s.uow.Within(ctx, func(txCtx context.Context) error {
		current, err := store.LockImageMetadata(txCtx, command.ImageID)
		if err != nil {
			return classifyImageMetadataError(err)
		}
		if err := validImageMetadata(current); err != nil {
			return err
		}

		candidate, changedFields := applyImageMetadataPatch(current, patch)
		if len(changedFields) == 0 {
			result = current
			return nil
		}
		sort.Strings(changedFields)
		updatedAt := s.now().UTC().Truncate(time.Microsecond)
		if !updatedAt.After(current.UpdatedAt) {
			updatedAt = current.UpdatedAt.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
		}
		candidate.UpdatedAt = updatedAt

		updated, err := store.UpdateImageMetadata(txCtx, candidate)
		if err != nil {
			return classifyImageMetadataError(err)
		}
		if err := validImageMetadata(updated); err != nil {
			return err
		}
		if !equalImageMetadata(candidate, updated) {
			return ErrImageMetadataUnavailable
		}

		payload, err := json.Marshal(struct {
			ImageID       int64    `json:"image_id"`
			Actor         int64    `json:"actor"`
			ChangedFields []string `json:"changed_fields"`
		}{
			ImageID:       updated.ID,
			Actor:         command.Actor,
			ChangedFields: changedFields,
		})
		if err != nil {
			return ErrImageMetadataUnavailable
		}
		key, err := imageMetadataUpdatedIdempotencyKey(command.Actor, updated, changedFields)
		if err != nil {
			return ErrImageMetadataUnavailable
		}
		if _, err := s.events.Append(txCtx, mediaport.Event{
			Type:           "media.image_metadata_updated",
			Payload:        payload,
			OccurredAt:     updated.UpdatedAt.UTC(),
			IdempotencyKey: key,
		}); err != nil {
			return err
		}
		result = updated
		return nil
	})
	if err != nil {
		return ImageMetadata{}, classifyImageMetadataError(err)
	}
	return result, nil
}

func normalizeImageMetadataPatch(patch ImageMetadataPatch) (ImageMetadataPatch, error) {
	result := ImageMetadataPatch{Enabled: patch.Enabled}
	if patch.Name != nil {
		value, err := normalizeImageMetadataText(*patch.Name, 200, true)
		if err != nil {
			return ImageMetadataPatch{}, err
		}
		result.Name = &value
	}
	if patch.Description != nil {
		value, err := normalizeImageMetadataText(*patch.Description, 10_000, true)
		if err != nil {
			return ImageMetadataPatch{}, err
		}
		result.Description = &value
	}
	if patch.Category != nil {
		value, err := normalizeImageMetadataText(*patch.Category, 200, true)
		if err != nil {
			return ImageMetadataPatch{}, err
		}
		result.Category = &value
	}
	if patch.Tags != nil {
		if len(*patch.Tags) > 50 {
			return ImageMetadataPatch{}, ErrInvalidImageMetadataUpdate
		}
		seen := make(map[string]struct{}, len(*patch.Tags))
		values := make([]string, 0, len(*patch.Tags))
		for _, raw := range *patch.Tags {
			value, err := normalizeImageMetadataText(raw, 64, false)
			if err != nil {
				return ImageMetadataPatch{}, err
			}
			if strings.Contains(value, ",") {
				return ImageMetadataPatch{}, ErrInvalidImageMetadataUpdate
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
		result.Tags = &values
	}
	return result, nil
}

func normalizeImageMetadataText(value string, maxRunes int, allowEmpty bool) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalidImageMetadataUpdate
	}
	value = strings.TrimSpace(value)
	if (!allowEmpty && value == "") || utf8.RuneCountInString(value) > maxRunes {
		return "", ErrInvalidImageMetadataUpdate
	}
	return value, nil
}

func applyImageMetadataPatch(current ImageMetadata, patch ImageMetadataPatch) (ImageMetadata, []string) {
	candidate := current
	changedFields := make([]string, 0, 5)
	if patch.Name != nil && candidate.Name != *patch.Name {
		candidate.Name = *patch.Name
		changedFields = append(changedFields, "name")
	}
	if patch.Description != nil && candidate.Description != *patch.Description {
		candidate.Description = *patch.Description
		changedFields = append(changedFields, "description")
	}
	if patch.Tags != nil {
		value := strings.Join(*patch.Tags, ",")
		if candidate.Tags != value {
			candidate.Tags = value
			changedFields = append(changedFields, "tags")
		}
	}
	if patch.Category != nil && candidate.Category != *patch.Category {
		candidate.Category = *patch.Category
		changedFields = append(changedFields, "category")
	}
	if patch.Enabled != nil && candidate.Enabled != *patch.Enabled {
		candidate.Enabled = *patch.Enabled
		changedFields = append(changedFields, "enabled")
	}
	return candidate, changedFields
}

func validImageMetadata(image ImageMetadata) error {
	if image.ID <= 0 || !utf8.ValidString(image.Name) || utf8.RuneCountInString(image.Name) > 200 ||
		!utf8.ValidString(image.FileName) || image.FileName == "" || utf8.RuneCountInString(image.FileName) > 255 ||
		!validImageMetadataMimeType(image.MimeType) || image.FileSize < 1 || image.FileSize > 10<<20 ||
		!utf8.ValidString(image.Description) || utf8.RuneCountInString(image.Description) > 10_000 ||
		!validImageMetadataTags(image.Tags) ||
		!utf8.ValidString(image.Category) || utf8.RuneCountInString(image.Category) > 200 ||
		image.Width < 1 || image.Width > 10_000 || image.Height < 1 || image.Height > 10_000 ||
		int64(image.Width)*int64(image.Height) > 40_000_000 || image.CreatedAt.IsZero() || image.UpdatedAt.IsZero() {
		return ErrImageMetadataUnavailable
	}
	return nil
}

func validImageMetadataTags(value string) bool {
	if value == "" {
		return true
	}
	tags := strings.Split(value, ",")
	if len(tags) > 50 {
		return false
	}
	for _, tag := range tags {
		if !utf8.ValidString(tag) || tag == "" || strings.TrimSpace(tag) != tag ||
			utf8.RuneCountInString(tag) > 64 || strings.Contains(tag, ",") {
			return false
		}
	}
	return true
}

func validImageMetadataMimeType(value string) bool {
	return utf8.ValidString(value) && (value == "image/png" || value == "image/jpeg" || value == "image/gif")
}

func equalImageMetadata(left, right ImageMetadata) bool {
	return left.ID == right.ID && left.Name == right.Name && left.FileName == right.FileName &&
		left.MimeType == right.MimeType && left.FileSize == right.FileSize && left.Enabled == right.Enabled &&
		left.Description == right.Description && left.Tags == right.Tags && left.Category == right.Category &&
		left.Width == right.Width && left.Height == right.Height && left.CreatedAt.Equal(right.CreatedAt) &&
		left.UpdatedAt.Equal(right.UpdatedAt)
}

func imageMetadataUpdatedIdempotencyKey(actor int64, image ImageMetadata, changedFields []string) (string, error) {
	canonical, err := json.Marshal(struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		FileName    string `json:"file_name"`
		MimeType    string `json:"mime_type"`
		FileSize    int64  `json:"file_size"`
		Enabled     bool   `json:"enabled"`
		Description string `json:"description"`
		Tags        string `json:"tags"`
		Category    string `json:"category"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}{
		ID: image.ID, Name: image.Name, FileName: image.FileName, MimeType: image.MimeType, FileSize: image.FileSize,
		Enabled: image.Enabled, Description: image.Description, Tags: image.Tags, Category: image.Category,
		Width: image.Width, Height: image.Height, CreatedAt: image.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: image.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", err
	}
	seed := strings.Join([]string{
		strconv.FormatInt(actor, 10),
		strconv.FormatInt(image.ID, 10),
		image.UpdatedAt.UTC().Format(time.RFC3339Nano),
		strings.Join(changedFields, ","),
		string(canonical),
	}, "\x00")
	digest := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("media.image_metadata_updated:%s", hex.EncodeToString(digest[:])), nil
}

func classifyImageMetadataError(err error) error {
	if errors.Is(err, ErrImageMetadataNotFound) {
		return ErrImageMetadataNotFound
	}
	if errors.Is(err, ErrInvalidImageMetadataUpdate) {
		return ErrInvalidImageMetadataUpdate
	}
	return ErrImageMetadataUnavailable
}
