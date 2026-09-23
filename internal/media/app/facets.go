package app

import (
	"context"
	"errors"
	"sort"
	"strings"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

const (
	maxImageFacetTagsPerRow = 50
	maxImageFacetTagRunes   = 64
)

var ErrFacetsUnavailable = errors.New("image facets unavailable")

type FacetRow struct {
	Category string
	Tags     string
}

type FacetStore interface {
	ListFacetRows(context.Context) ([]FacetRow, error)
}

func (service *Service) Facets(ctx context.Context) (mediaport.ImageFacets, error) {
	if service == nil || ctx == nil || service.uow == nil || service.store == nil {
		return emptyImageFacets(), ErrFacetsUnavailable
	}
	store, ok := service.store.(FacetStore)
	if !ok {
		return emptyImageFacets(), ErrFacetsUnavailable
	}

	result := emptyImageFacets()
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		rows, err := store.ListFacetRows(tx)
		if err != nil {
			return err
		}
		result = projectImageFacets(rows)
		return nil
	}); err != nil {
		return emptyImageFacets(), ErrFacetsUnavailable
	}
	return result, nil
}

func emptyImageFacets() mediaport.ImageFacets {
	return mediaport.ImageFacets{Categories: []string{}, Tags: []string{}}
}

func projectImageFacets(rows []FacetRow) mediaport.ImageFacets {
	// Keep row iteration deterministic without delegating ordering to PostgreSQL collation.
	ordered := append([]FacetRow(nil), rows...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Category != ordered[right].Category {
			return ordered[left].Category < ordered[right].Category
		}
		return ordered[left].Tags < ordered[right].Tags
	})

	result := emptyImageFacets()
	categorySeen := make(map[string]struct{}, len(ordered))
	tagSeen := make(map[string]struct{})
	for _, row := range ordered {
		category := strings.TrimSpace(row.Category)
		if category != "" {
			if _, exists := categorySeen[category]; !exists {
				categorySeen[category] = struct{}{}
				result.Categories = append(result.Categories, category)
			}
		}

		rowTags := make([]string, 0, maxImageFacetTagsPerRow)
		for _, rawTag := range strings.Split(row.Tags, ",") {
			if len(rowTags) == maxImageFacetTagsPerRow {
				break
			}
			tag := strings.TrimSpace(rawTag)
			// Compare the full value with already-truncated row output before truncating.
			// This intentionally preserves the legacy 50-slot long-tag quirk.
			if tag == "" || containsExact(rowTags, tag) {
				continue
			}
			rowTags = append(rowTags, truncateCodePoints(tag, maxImageFacetTagRunes))
		}
		for _, tag := range rowTags {
			if _, exists := tagSeen[tag]; exists {
				continue
			}
			tagSeen[tag] = struct{}{}
			result.Tags = append(result.Tags, tag)
		}
	}

	sort.Strings(result.Categories)
	sort.Strings(result.Tags)
	return result
}

func containsExact(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func truncateCodePoints(value string, limit int) string {
	codePoints := []rune(value)
	if len(codePoints) <= limit {
		return value
	}
	return string(codePoints[:limit])
}
