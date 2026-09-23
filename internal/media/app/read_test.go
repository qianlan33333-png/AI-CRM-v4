package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type readTestUOW struct{ calls int }

func (uow *readTestUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	uow.calls++
	return fn(ctx)
}

type readTestStore struct {
	row     ImageListRow
	content []byte
	enabled bool
}

func (store *readTestStore) ListImageRows(context.Context, ImageListFilter, int64, int64) (ImageListRead, error) {
	return ImageListRead{Total: 1, Rows: []ImageListRow{store.row}}, nil
}

func (store *readTestStore) ListFacetRows(context.Context) ([]FacetRow, error) {
	return []FacetRow{{Category: " cover ", Tags: " hero,hero, course "}}, nil
}

func (store *readTestStore) ImageExists(context.Context, int64) (bool, error) { return true, nil }

func (store *readTestStore) ReadImageVariant(context.Context, int64) (ImageVariantRow, error) {
	digest := sha256.Sum256(store.content)
	return ImageVariantRow{
		ID: 7, FileName: "cover.png", MimeType: "image/png", FileSize: int32(len(store.content)), Width: 2, Height: 1, Enabled: store.enabled,
		ImageChecksum: digest[:], BlobChecksum: digest[:], Content: append([]byte(nil), store.content...),
	}, nil
}

func TestReadServiceProjectsLibraryReadsWithoutProviderEffects(t *testing.T) {
	content := testPNG(t)
	now := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	store := &readTestStore{content: content, enabled: true, row: ImageListRow{
		ID: 7, Name: " Cover ", FileName: "cover.png", MimeType: "image/png", FileSize: int32(len(content)), Enabled: true,
		Tags: " hero,hero ", Category: "cover", Width: 2, Height: 1, CreatedAt: now, UpdatedAt: now,
	}}
	uow := &readTestUOW{}
	service := NewReadService(uow, store)

	page, err := service.ListImages(context.Background(), mediaport.ImageListQuery{EnabledOnly: true})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Thumb320URL == "" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	facets, err := service.Facets(context.Background())
	if err != nil || len(facets.Categories) != 1 || len(facets.Tags) != 2 {
		t.Fatalf("facets=%#v err=%v", facets, err)
	}
	exists, err := service.LocalImageExists(context.Background(), 7)
	if err != nil || !exists || uow.calls != 3 {
		t.Fatalf("exists=%v err=%v uow_calls=%d", exists, err, uow.calls)
	}

	variant, err := service.GetImageVariant(context.Background(), 7, "original")
	if err != nil || variant.MediaType != "image/png" || !bytes.Equal(variant.Content, content) || !ValidImageVariantETag(variant.ETag) {
		t.Fatalf("variant=%#v err=%v", variant, err)
	}
	if !bytes.Equal(variant.Content, content) {
		t.Fatal("variant content changed")
	}
	enabledVariant, err := service.GetEnabledImageVariant(context.Background(), 7, "original")
	if err != nil || !bytes.Equal(enabledVariant.Content, content) {
		t.Fatalf("enabled variant=%#v err=%v", enabledVariant, err)
	}
	store.enabled = false
	if _, err = service.GetEnabledImageVariant(context.Background(), 7, "original"); err != ErrImageVariantNotFound {
		t.Fatalf("disabled viewer variant err=%v", err)
	}
	if _, err = service.GetImageVariant(context.Background(), 7, "original"); err != nil {
		t.Fatalf("administrator variant must preserve disabled-image inspection: %v", err)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 1))
	value.Set(0, 0, color.RGBA{R: 1, A: 255})
	value.Set(1, 0, color.RGBA{G: 2, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
