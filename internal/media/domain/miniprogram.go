package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

var ErrInvalidMiniProgram = errors.New("invalid miniprogram material")

const (
	miniProgramNameLimit           = 200
	miniProgramAppIDLimit          = 120
	miniProgramPagePathLimit       = 500
	miniProgramTitleLimit          = 200
	miniProgramThumbnailMediaLimit = 255
)

func NewMiniProgram(command mediaport.MiniProgramCreateCommand, now time.Time) (mediaport.MiniProgram, error) {
	if command.Actor < 1 || now.IsZero() {
		return mediaport.MiniProgram{}, ErrInvalidMiniProgram
	}
	name, title := normalizeMiniProgramCreateNameTitle(command.Name, command.Title)
	item := mediaport.MiniProgram{
		Name:             name,
		AppID:            normalizeMiniProgramText(command.AppID, miniProgramAppIDLimit),
		PagePath:         normalizeMiniProgramText(command.PagePath, miniProgramPagePathLimit),
		Title:            title,
		ThumbnailImageID: cloneMiniProgramID(command.ThumbnailImageID),
		Enabled:          boolOrDefault(command.Enabled, true),
		CreatedAt:        now.UTC(),
		UpdatedAt:        now.UTC(),
		CreatedBy:        command.Actor,
		UpdatedBy:        command.Actor,
		Version:          1,
	}
	if !ValidMiniProgram(item, false) {
		return mediaport.MiniProgram{}, ErrInvalidMiniProgram
	}
	return item, nil
}

func ApplyMiniProgramPatch(current mediaport.MiniProgram, patch mediaport.MiniProgramPatch, actor int64, now time.Time) (mediaport.MiniProgram, bool, error) {
	if !ValidMiniProgram(current, true) || actor < 1 || now.IsZero() {
		return mediaport.MiniProgram{}, false, ErrInvalidMiniProgram
	}
	updated := current
	changed := false
	if patch.Name != nil {
		changed = changeMiniProgramString(&updated.Name, normalizeMiniProgramText(*patch.Name, miniProgramNameLimit)) || changed
	}
	if patch.AppID != nil {
		changed = changeMiniProgramString(&updated.AppID, normalizeMiniProgramText(*patch.AppID, miniProgramAppIDLimit)) || changed
	}
	if patch.PagePath != nil {
		changed = changeMiniProgramString(&updated.PagePath, normalizeMiniProgramText(*patch.PagePath, miniProgramPagePathLimit)) || changed
	}
	if patch.Title != nil {
		changed = changeMiniProgramString(&updated.Title, normalizeMiniProgramText(*patch.Title, miniProgramTitleLimit)) || changed
	}
	if patch.ThumbnailImageID.Present && !sameMiniProgramID(updated.ThumbnailImageID, patch.ThumbnailImageID.Value) {
		updated.ThumbnailImageID = cloneMiniProgramID(patch.ThumbnailImageID.Value)
		updated.ThumbnailMediaID = ""
		updated.ThumbnailMediaExpiresAt = nil
		changed = true
	}
	if patch.Enabled != nil && updated.Enabled != *patch.Enabled {
		updated.Enabled = *patch.Enabled
		changed = true
	}
	if !changed {
		return current, false, nil
	}
	updated.UpdatedAt, updated.UpdatedBy, updated.Version = now.UTC(), actor, current.Version+1
	if !ValidMiniProgram(updated, true) {
		return mediaport.MiniProgram{}, false, ErrInvalidMiniProgram
	}
	return updated, true, nil
}

func ApplyThumbnailCacheResolution(current mediaport.MiniProgram, resolution mediaport.ThumbnailCacheResolution, actor int64, now time.Time) (mediaport.MiniProgram, bool, error) {
	if !ValidMiniProgram(current, true) || actor < 1 || now.IsZero() || resolution.Status != mediaport.ThumbnailResolved ||
		resolution.CacheOwner != mediaport.ThumbnailCacheOwner || strings.TrimSpace(resolution.CacheReceipt) == "" ||
		strings.TrimSpace(resolution.MediaID) == "" || resolution.SideEffectExecuted || resolution.RealExternalCallExecuted {
		return mediaport.MiniProgram{}, false, ErrInvalidMiniProgram
	}
	updated := current
	mediaID := normalizeMiniProgramText(resolution.MediaID, miniProgramThumbnailMediaLimit)
	if updated.ThumbnailMediaID == mediaID && sameMiniProgramTime(updated.ThumbnailMediaExpiresAt, resolution.ExpiresAt) {
		return current, false, nil
	}
	updated.ThumbnailMediaID = mediaID
	updated.ThumbnailMediaExpiresAt = cloneMiniProgramTime(resolution.ExpiresAt)
	updated.UpdatedAt, updated.UpdatedBy, updated.Version = now.UTC(), actor, current.Version+1
	if !ValidMiniProgram(updated, true) {
		return mediaport.MiniProgram{}, false, ErrInvalidMiniProgram
	}
	return updated, true, nil
}

func ValidMiniProgram(item mediaport.MiniProgram, persisted bool) bool {
	if item.AppID == "" || item.PagePath == "" || item.Title == "" || item.CreatedBy < 1 || item.UpdatedBy < 1 ||
		!validMiniProgramText(item.Name, miniProgramNameLimit) || !validMiniProgramText(item.AppID, miniProgramAppIDLimit) || !validMiniProgramText(item.PagePath, miniProgramPagePathLimit) || !validMiniProgramText(item.Title, miniProgramTitleLimit) ||
		!utf8.ValidString(item.ThumbnailImageURL) || !utf8.ValidString(item.ThumbnailImageBase64) || !validMiniProgramText(item.ThumbnailMediaID, miniProgramThumbnailMediaLimit) {
		return false
	}
	if item.ThumbnailImageID != nil && *item.ThumbnailImageID < 1 {
		return false
	}
	if persisted && (item.ID < 1 || item.Version < 1 || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero()) {
		return false
	}
	return true
}

func validMiniProgramText(value string, limit int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= limit
}

// normalizeMiniProgramCreateNameTitle mirrors the legacy create fallback.
// Update is deliberately different: an empty name is valid and an explicitly
// empty title is rejected by ValidMiniProgram rather than being filled in.
func normalizeMiniProgramCreateNameTitle(name, title string) (string, string) {
	name = normalizeMiniProgramText(name, miniProgramNameLimit)
	title = normalizeMiniProgramText(title, miniProgramTitleLimit)
	if name == "" {
		name = title
	}
	if title == "" {
		title = name
	}
	return name, title
}

func normalizeMiniProgramText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || limit < 1 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func changeMiniProgramString(target *string, next string) bool {
	if *target == next {
		return false
	}
	*target = next
	return true
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func cloneMiniProgramID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func sameMiniProgramID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneMiniProgramTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func sameMiniProgramTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
