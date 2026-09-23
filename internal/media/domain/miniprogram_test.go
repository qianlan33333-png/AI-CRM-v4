package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

func TestNewMiniProgramPreservesRequiredLegacyFields(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	enabled := false
	thumbnailID := int64(9)
	item, err := NewMiniProgram(mediaport.MiniProgramCreateCommand{
		Name: " 卡片 ", AppID: " wx-demo ", PagePath: " pages/home ", Title: " 标题 ", ThumbnailImageID: &thumbnailID, Enabled: &enabled, Actor: 7,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "卡片" || item.AppID != "wx-demo" || item.PagePath != "pages/home" || item.Title != "标题" || item.Enabled || item.Version != 1 || item.ThumbnailImageID == nil || *item.ThumbnailImageID != 9 {
		t.Fatalf("item=%#v", item)
	}
}

func TestMiniProgramPatchNoOpAndThumbnailChangeClearsCache(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	imageID := int64(3)
	current := mediaport.MiniProgram{ID: 2, Name: "卡片", AppID: "wx-demo", PagePath: "pages/home", Title: "标题", ThumbnailImageID: &imageID, ThumbnailMediaID: "cached", CreatedBy: 1, UpdatedBy: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	name := "卡片"
	noOp, changed, err := ApplyMiniProgramPatch(current, mediaport.MiniProgramPatch{Name: &name}, 7, now.Add(time.Minute))
	if err != nil || changed || noOp.Version != 1 || noOp.UpdatedBy != 1 {
		t.Fatalf("item=%#v changed=%v err=%v", noOp, changed, err)
	}
	nextImageID := int64(4)
	updated, changed, err := ApplyMiniProgramPatch(current, mediaport.MiniProgramPatch{ThumbnailImageID: mediaport.OptionalInt64{Present: true, Value: &nextImageID}}, 7, now.Add(time.Minute))
	if err != nil || !changed || updated.ThumbnailMediaID != "" || updated.ThumbnailMediaExpiresAt != nil || updated.Version != 2 || updated.UpdatedBy != 7 {
		t.Fatalf("item=%#v changed=%v err=%v", updated, changed, err)
	}
}

func TestMiniProgramPatchDistinguishesOmittedAndExplicitNullThumbnail(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	imageID := int64(3)
	current := mediaport.MiniProgram{ID: 2, Name: "卡片", AppID: "wx-demo", PagePath: "pages/home", Title: "标题", ThumbnailImageID: &imageID, ThumbnailMediaID: "cached", CreatedBy: 1, UpdatedBy: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	omitted, changed, err := ApplyMiniProgramPatch(current, mediaport.MiniProgramPatch{}, 7, now.Add(time.Minute))
	if err != nil || changed || omitted.ThumbnailImageID == nil || *omitted.ThumbnailImageID != imageID || omitted.ThumbnailMediaID != "cached" {
		t.Fatalf("omitted=%#v changed=%v err=%v", omitted, changed, err)
	}
	cleared, changed, err := ApplyMiniProgramPatch(current, mediaport.MiniProgramPatch{ThumbnailImageID: mediaport.OptionalInt64{Present: true}}, 7, now.Add(time.Minute))
	if err != nil || !changed || cleared.ThumbnailImageID != nil || cleared.ThumbnailMediaID != "" || cleared.ThumbnailMediaExpiresAt != nil {
		t.Fatalf("cleared=%#v changed=%v err=%v", cleared, changed, err)
	}
}

func TestMiniProgramLegacyTextFallbackAndRuneTruncation(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	item, err := NewMiniProgram(mediaport.MiniProgramCreateCommand{
		Name: strings.Repeat("名", 201), AppID: strings.Repeat("a", 121), PagePath: strings.Repeat("p", 501), Title: "", Actor: 7,
	}, now)
	if err != nil || utf8.RuneCountInString(item.Name) != 200 || item.Title != item.Name || utf8.RuneCountInString(item.AppID) != 120 || utf8.RuneCountInString(item.PagePath) != 500 {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	resolved, changed, err := ApplyThumbnailCacheResolution(itemWithID(item), mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailResolved, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "cache-1", MediaID: strings.Repeat("m", 256)}, 7, now.Add(time.Minute))
	if err != nil || !changed || utf8.RuneCountInString(resolved.ThumbnailMediaID) != 255 {
		t.Fatalf("resolved=%#v changed=%v err=%v", resolved, changed, err)
	}
}

func TestMiniProgramCreateFallbackDiffersFromUpdateNameTitleRules(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	fromTitle, err := NewMiniProgram(mediaport.MiniProgramCreateCommand{Name: "", AppID: "wx-demo", PagePath: "pages/home", Title: "标题", Actor: 7}, now)
	if err != nil || fromTitle.Name != "标题" || fromTitle.Title != "标题" {
		t.Fatalf("create name fallback item=%#v err=%v", fromTitle, err)
	}
	fromName, err := NewMiniProgram(mediaport.MiniProgramCreateCommand{Name: "名称", AppID: "wx-demo", PagePath: "pages/home", Title: "", Actor: 7}, now)
	if err != nil || fromName.Name != "名称" || fromName.Title != "名称" {
		t.Fatalf("create title fallback item=%#v err=%v", fromName, err)
	}
	current := itemWithID(fromTitle)
	emptyName := ""
	updated, changed, err := ApplyMiniProgramPatch(current, mediaport.MiniProgramPatch{Name: &emptyName}, 7, now.Add(time.Minute))
	if err != nil || !changed || updated.Name != "" || updated.Title != "标题" {
		t.Fatalf("empty-name mutation item=%#v changed=%v err=%v", updated, changed, err)
	}
	emptyTitle := ""
	if _, _, err = ApplyMiniProgramPatch(updated, mediaport.MiniProgramPatch{Title: &emptyTitle}, 7, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidMiniProgram) {
		t.Fatalf("empty-title mutation err=%v", err)
	}
}

func itemWithID(item mediaport.MiniProgram) mediaport.MiniProgram {
	item.ID, item.CreatedBy, item.UpdatedBy, item.Version = 1, 7, 7, 1
	return item
}

func TestThumbnailResolutionRequiresLocalOwnerAndReceipt(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	item := mediaport.MiniProgram{ID: 2, Name: "卡片", AppID: "wx-demo", PagePath: "pages/home", Title: "标题", Enabled: true, CreatedBy: 1, UpdatedBy: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	_, _, err := ApplyThumbnailCacheResolution(item, mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailResolved, CacheOwner: mediaport.ThumbnailCacheOwner, MediaID: "media"}, 7, now)
	if !errors.Is(err, ErrInvalidMiniProgram) {
		t.Fatalf("missing receipt err=%v", err)
	}
	resolved, changed, err := ApplyThumbnailCacheResolution(item, mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailResolved, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "cache-1", MediaID: "media"}, 7, now.Add(time.Minute))
	if err != nil || !changed || resolved.ThumbnailMediaID != "media" || resolved.Version != 2 {
		t.Fatalf("resolved=%#v changed=%v err=%v", resolved, changed, err)
	}
}
