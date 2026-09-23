package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

// sidebarMediaLibrary adapts Media's existing local HTTP-read facade into the
// deliberately narrower sidebar read ports. It has no Provider path: list,
// facets, and metadata are all Media-owned PostgreSQL projections, while send
// preparation stays delegated to Media's existing lease-bound reader.
type sidebarMediaLibrary struct {
	library interface {
		ListImagesFiltered(context.Context, mediaapp.ImageQuery) ([]map[string]any, int, error)
		ImageFacets(context.Context) ([]string, []string, error)
		LocalImageExists(context.Context, int64) (bool, error)
	}
	sender mediaport.SidebarImageSendReader
}

func (adapter sidebarMediaLibrary) ListImages(ctx context.Context, query mediaport.ImageListQuery) (mediaport.ImageListPage, error) {
	if adapter.library == nil || ctx == nil {
		return mediaport.ImageListPage{}, errors.New("sidebar media library is unavailable")
	}
	limit, offset := query.Limit, query.Offset
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 || offset < 0 {
		return mediaport.ImageListPage{}, errors.New("invalid sidebar media query")
	}
	rows, total, err := adapter.library.ListImagesFiltered(ctx, mediaapp.ImageQuery{
		Limit:         int(limit),
		Offset:        int(offset),
		EnabledOnly:   query.EnabledOnly,
		Query:         query.Search,
		Category:      query.Category,
		Tags:          sidebarMediaTags(query.Tags),
		TagGroups:     sidebarMediaTagGroups(query.TagGroups),
		OnlyUnlabeled: query.OnlyUnlabeled,
	})
	if err != nil || total < 0 || len(rows) > int(limit) || len(rows) > total {
		return mediaport.ImageListPage{}, errors.New("sidebar media list is unavailable")
	}
	items := make([]mediaport.ImageListItem, 0, len(rows))
	for _, row := range rows {
		item, projectErr := sidebarMediaListItem(row)
		if projectErr != nil {
			return mediaport.ImageListPage{}, errors.New("sidebar media list is unavailable")
		}
		items = append(items, item)
	}
	return mediaport.ImageListPage{Items: items, Total: int64(total), Limit: limit, Offset: offset}, nil
}

func (adapter sidebarMediaLibrary) Facets(ctx context.Context) (mediaport.ImageFacets, error) {
	if adapter.library == nil || ctx == nil {
		return mediaport.ImageFacets{}, errors.New("sidebar media library is unavailable")
	}
	categories, tags, err := adapter.library.ImageFacets(ctx)
	if err != nil {
		return mediaport.ImageFacets{}, err
	}
	return mediaport.ImageFacets{Categories: append([]string(nil), categories...), Tags: append([]string(nil), tags...)}, nil
}

func (adapter sidebarMediaLibrary) LocalImageExists(ctx context.Context, imageID int64) (bool, error) {
	if adapter.library == nil || ctx == nil || imageID < 1 {
		return false, errors.New("sidebar media library is unavailable")
	}
	return adapter.library.LocalImageExists(ctx, imageID)
}

func (adapter sidebarMediaLibrary) ReadSidebarImageForSend(ctx context.Context, imageID int64, requiredThrough time.Time) (mediaport.SidebarImageSendMaterial, error) {
	if adapter.sender == nil {
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
	}
	return adapter.sender.ReadSidebarImageForSend(ctx, imageID, requiredThrough)
}

func sidebarMediaTags(raw string) []string {
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func sidebarMediaTagGroups(raw []string) [][]string {
	result := make([][]string, 0, len(raw))
	for _, group := range raw {
		if tags := sidebarMediaTags(group); len(tags) != 0 {
			result = append(result, tags)
		}
	}
	return result
}

func sidebarMediaListItem(row map[string]any) (mediaport.ImageListItem, error) {
	id, ok := sidebarMediaInt64(row["id"])
	if !ok || id < 1 {
		return mediaport.ImageListItem{}, errors.New("invalid media id")
	}
	fileName, ok := row["file_name"].(string)
	if !ok || fileName == "" {
		return mediaport.ImageListItem{}, errors.New("invalid media file name")
	}
	mimeType, ok := row["mime_type"].(string)
	if !ok || mimeType == "" {
		return mediaport.ImageListItem{}, errors.New("invalid media mime type")
	}
	fileSize, ok := sidebarMediaInt64(row["file_size"])
	if !ok || fileSize < 1 || fileSize > int64(^uint32(0)>>1) {
		return mediaport.ImageListItem{}, errors.New("invalid media file size")
	}
	width, widthOK := sidebarMediaInt64(row["width"])
	height, heightOK := sidebarMediaInt64(row["height"])
	if !widthOK || !heightOK || width < 1 || height < 1 || width > int64(^uint32(0)>>1) || height > int64(^uint32(0)>>1) {
		return mediaport.ImageListItem{}, errors.New("invalid media dimensions")
	}
	createdAt, createdOK := sidebarMediaTimestamp(row["created_at"])
	updatedAt, updatedOK := sidebarMediaTimestamp(row["updated_at"])
	if !createdOK || !updatedOK {
		return mediaport.ImageListItem{}, errors.New("invalid media timestamps")
	}
	enabled, ok := row["enabled"].(bool)
	if !ok {
		return mediaport.ImageListItem{}, errors.New("invalid media enabled state")
	}
	name, _ := row["name"].(string)
	description, _ := row["description"].(string)
	category, _ := row["category"].(string)
	tags, ok := row["tags"].([]string)
	if !ok {
		return mediaport.ImageListItem{}, errors.New("invalid media tags")
	}
	value := mediaport.ImageListItem{
		ID: id, Name: name, FileName: fileName, MimeType: mimeType, FileSize: int32(fileSize), Enabled: enabled,
		Description: description, Tags: append([]string(nil), tags...), Category: category, Width: int32(width), Height: int32(height),
		CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano),
	}
	for field, target := range map[string]*string{
		"thumb_160_url": &value.Thumb160URL, "thumb_320_url": &value.Thumb320URL, "thumb_url": &value.ThumbURL,
		"preview_url": &value.PreviewURL, "mobile_1080_url": &value.Mobile1080URL, "large_1440_url": &value.Large1440URL, "original_url": &value.OriginalURL,
	} {
		url, stringOK := row[field].(string)
		if !stringOK || url == "" {
			return mediaport.ImageListItem{}, fmt.Errorf("invalid media %s", field)
		}
		*target = url
	}
	return value, nil
}

func sidebarMediaInt64(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case uint32:
		return int64(value), true
	case uint64:
		if value <= uint64(^uint64(0)>>1) {
			return int64(value), true
		}
	}
	return 0, false
}

func sidebarMediaTimestamp(value any) (time.Time, bool) {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return parsed, err == nil && !parsed.IsZero()
}

var _ mediaport.ImageLibraryReader = sidebarMediaLibrary{}
var _ mediaport.SidebarImageSendReader = sidebarMediaLibrary{}
