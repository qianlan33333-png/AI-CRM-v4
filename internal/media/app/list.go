package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

const (
	defaultImageListLimit = int64(100)
	maxImageListLimit     = int64(500)
)

var ErrListUnavailable = errors.New("image list unavailable")

type ImageListFilter struct {
	Search        string
	Category      string
	Tags          []string
	TagGroups     [][]string
	OnlyUnlabeled bool
	EnabledOnly   bool
}

type ImageListRow struct {
	ID          int64
	Name        string
	FileName    string
	MimeType    string
	FileSize    int32
	Enabled     bool
	Description string
	Tags        string
	Category    string
	Width       int32
	Height      int32
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ImageListRead struct {
	Total int64
	Rows  []ImageListRow
}

// ImageListStore deliberately exposes one combined count-and-page read. The
// PostgreSQL implementation executes it as one statement inside one read UoW,
// so total and items cannot come from different snapshots.
type ImageListStore interface {
	ListImageRows(context.Context, ImageListFilter, int64, int64) (ImageListRead, error)
}

type ImageCountStore interface {
	CountEnabledImages(context.Context) (int64, error)
}

func (service *Service) ListImages(ctx context.Context, query mediaport.ImageListQuery) (mediaport.ImageListPage, error) {
	limit, offset := clampImageListPage(query.Limit, query.Offset)
	empty := emptyImageListPage(limit, offset)
	if service == nil || ctx == nil || service.uow == nil || service.store == nil {
		return empty, ErrListUnavailable
	}
	store, ok := service.store.(ImageListStore)
	if !ok {
		return empty, ErrListUnavailable
	}

	filter := ImageListFilter{
		Search:        strings.TrimSpace(query.Search),
		Category:      strings.TrimSpace(query.Category),
		Tags:          normalizeImageListTags(query.Tags),
		TagGroups:     normalizeImageListTagGroups(query.TagGroups),
		OnlyUnlabeled: query.OnlyUnlabeled,
		EnabledOnly:   query.EnabledOnly,
	}

	read := ImageListRead{Rows: []ImageListRow{}}
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		result, err := store.ListImageRows(tx, filter, limit, offset)
		if err != nil || !validImageListRead(result, limit, offset) {
			return ErrListUnavailable
		}
		read = result
		return nil
	}); err != nil {
		return empty, ErrListUnavailable
	}

	items := make([]mediaport.ImageListItem, 0, len(read.Rows))
	for _, row := range read.Rows {
		items = append(items, projectImageListItem(row))
	}
	return mediaport.ImageListPage{Items: items, Total: read.Total, Limit: limit, Offset: offset}, nil
}

func (service *Service) CountEnabledImages(ctx context.Context) (int64, error) {
	if service == nil || ctx == nil || service.uow == nil || service.store == nil {
		return 0, ErrListUnavailable
	}
	store, ok := service.store.(ImageCountStore)
	if !ok {
		return 0, ErrListUnavailable
	}
	var count int64
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		count, readErr = store.CountEnabledImages(tx)
		return readErr
	})
	if err != nil || count < 0 {
		return 0, ErrListUnavailable
	}
	return count, nil
}

func (service *Service) LocalImageExists(ctx context.Context, imageID int64) (bool, error) {
	if service == nil || ctx == nil || service.uow == nil || service.store == nil || imageID < 1 {
		return false, ErrListUnavailable
	}
	reader, ok := service.store.(mediaport.ImageMetadataReader)
	if !ok {
		return false, ErrListUnavailable
	}
	var exists bool
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		exists, readErr = reader.ImageExists(tx, imageID)
		return readErr
	}); err != nil {
		return false, ErrListUnavailable
	}
	return exists, nil
}

var _ mediaport.ImageLibraryReader = (*Service)(nil)

func clampImageListPage(limit, offset int64) (int64, int64) {
	switch {
	case limit == 0:
		limit = defaultImageListLimit
	case limit < 0:
		limit = 1
	case limit > maxImageListLimit:
		limit = maxImageListLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func emptyImageListPage(limit, offset int64) mediaport.ImageListPage {
	return mediaport.ImageListPage{Items: []mediaport.ImageListItem{}, Limit: limit, Offset: offset}
}

func validImageListRead(read ImageListRead, limit, offset int64) bool {
	count := int64(len(read.Rows))
	if read.Total < 0 || count > limit || count > read.Total {
		return false
	}
	if count == 0 && offset < read.Total {
		return false
	}
	if count > 0 && offset > read.Total-count {
		return false
	}
	for _, row := range read.Rows {
		if row.ID < 1 || row.FileName == "" || row.MimeType == "" || row.FileSize < 1 ||
			row.Width < 1 || row.Height < 1 || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
			return false
		}
	}
	return true
}

func projectImageListItem(row ImageListRow) mediaport.ImageListItem {
	base := "/api/admin/image-library/" + strconv.FormatInt(row.ID, 10) + "/variants/"
	thumb160 := base + "thumb_160"
	thumb320 := base + "thumb_320"
	mobile1080 := base + "mobile_1080"
	return mediaport.ImageListItem{
		ID: row.ID, Name: row.Name, FileName: row.FileName, MimeType: row.MimeType, FileSize: row.FileSize,
		Enabled: row.Enabled, Description: row.Description, Tags: normalizeImageListTags(row.Tags), Category: row.Category,
		Width: row.Width, Height: row.Height, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano), Thumb160URL: thumb160, Thumb320URL: thumb320,
		ThumbURL: thumb320, PreviewURL: mobile1080, Mobile1080URL: mobile1080,
		Large1440URL: base + "large_1440", OriginalURL: base + "original",
	}
}

func normalizeImageListTags(value string) []string {
	result := make([]string, 0, maxImageFacetTagsPerRow)
	for _, raw := range strings.Split(value, ",") {
		if len(result) == maxImageFacetTagsPerRow {
			break
		}
		tag := strings.TrimSpace(raw)
		// Compare the complete value with the already-truncated output first.
		// This intentionally preserves the frozen 65+ code-point duplicate quirk.
		if tag == "" || containsExact(result, tag) {
			continue
		}
		result = append(result, truncateCodePoints(tag, maxImageFacetTagRunes))
	}
	return result
}

func normalizeImageListTagGroups(values []string) [][]string {
	groups := make([][]string, 0, len(values))
	for _, value := range values {
		group := normalizeImageListTags(value)
		if len(group) == 0 || containsExactImageListGroup(groups, group) {
			continue
		}
		groups = append(groups, group)
	}
	return groups
}

func containsExactImageListGroup(groups [][]string, candidate []string) bool {
	for _, group := range groups {
		if len(group) != len(candidate) {
			continue
		}
		equal := true
		for index := range group {
			if group[index] != candidate[index] {
				equal = false
				break
			}
		}
		if equal {
			return true
		}
	}
	return false
}
