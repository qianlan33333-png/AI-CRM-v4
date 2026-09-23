package app

import (
	"encoding/json"
	"testing"
	"time"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestPublicProductImageURLsUseSavedImageLibraryBindings(t *testing.T) {
	projection, err := CanonicalLegacyAdminProjection(json.RawMessage(`{"schema_version":1,"status":"active","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	product := productport.Product{
		ID: 8, ProductCode: "course-9", Name: "课程", Description: "说明", PriceMinor: 9900, Currency: "CNY", StockQuantity: 2,
		Images:    []string{"/api/admin/image-library/88/variants/original", "https://assets.example.test/cover.png", "/api/admin/image-library/88/variants/original", "/api/admin/image-library/89/variants/thumb_320"},
		CreatedBy: 3, CreatedAt: now, UpdatedAt: now, Version: 1, LocalLifecycle: productport.LocalProductEnabled, LegacyAdminProjection: projection,
	}
	urls, err := PublicProductImageURLs(product)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(urls), 3; got != want || urls[0] != "/api/h5/product-images/course-9/88/variants/original" || urls[1] != "https://assets.example.test/cover.png" || urls[2] != "/api/h5/product-images/course-9/88/variants/original" {
		t.Fatalf("urls=%q", urls)
	}
	if cover := publicProductCardCover(product); cover != "/api/h5/product-images/course-9/88/variants/original" {
		t.Fatalf("cover=%q", cover)
	}
	ids, err := PublicProductImageIDs(product)
	if err != nil || len(ids) != 1 || ids[0] != 88 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}

	draftProjection, err := CanonicalLegacyAdminProjection(json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	product.LocalLifecycle, product.LegacyAdminProjection = productport.LocalProductDraft, draftProjection
	if _, err = PublicProductImageURLs(product); err == nil {
		t.Fatal("draft product must not expose a public media URL")
	}
}
