package store

import (
	"testing"
	"time"
)

func TestMiniMapMarksLocalCoverReadyWhenImageIsAttached(t *testing.T) {
	coverID := int64(1071)
	when := time.Unix(0, 0).UTC()
	withCover := miniMap(2001, "日课", "wx-test", "pages/article/article?lesson_id=uuid&from=learn", "标题", &coverID, true, 1, 2, 2, when, when)
	if got := withCover["thumbnail_status"]; got != "ready" {
		t.Fatalf("thumbnail_status with local cover = %v, want ready", got)
	}
	if got := withCover["thumb_image_url"]; got != "/api/admin/image-library/1071/variants/thumb_320" {
		t.Fatalf("thumb_image_url = %v", got)
	}

	withoutCover := miniMap(2002, "日课", "wx-test", "pages/article/article?lesson_id=uuid-2&from=learn", "标题 2", nil, true, 1, 2, 2, when, when)
	if got := withoutCover["thumbnail_status"]; got != "not_available" {
		t.Fatalf("thumbnail_status without local cover = %v, want not_available", got)
	}
}
