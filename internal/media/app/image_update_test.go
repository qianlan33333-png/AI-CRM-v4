package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	eventport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type imageMetadataUpdateTestStore struct {
	row                    ImageMetadata
	lockErr, updateErr     error
	lockCalls, updateCalls int
}

func (store *imageMetadataUpdateTestStore) LockImageMetadata(_ context.Context, id int64) (ImageMetadata, error) {
	store.lockCalls++
	if store.lockErr != nil {
		return ImageMetadata{}, store.lockErr
	}
	if id != store.row.ID {
		return ImageMetadata{}, ErrImageMetadataNotFound
	}
	return store.row, nil
}

func (store *imageMetadataUpdateTestStore) UpdateImageMetadata(_ context.Context, image ImageMetadata) (ImageMetadata, error) {
	store.updateCalls++
	if store.updateErr != nil {
		return ImageMetadata{}, store.updateErr
	}
	store.row = image
	return image, nil
}

func (store *imageMetadataUpdateTestStore) Reserve(context.Context, Reservation) (Receipt, bool, error) {
	return Receipt{}, false, errors.New("unexpected upload reserve")
}

func (store *imageMetadataUpdateTestStore) Create(context.Context, CreateInput) (mediaport.Image, error) {
	return mediaport.Image{}, errors.New("unexpected image create")
}

func (store *imageMetadataUpdateTestStore) Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error) {
	return Receipt{}, errors.New("unexpected upload complete")
}

type imageMetadataUpdateTestEvents struct {
	events []eventport.Event
	err    error
}

func (events *imageMetadataUpdateTestEvents) Append(_ context.Context, event eventport.Event) (eventport.EventID, error) {
	if events.err != nil {
		return 0, events.err
	}
	events.events = append(events.events, event)
	return eventport.EventID(len(events.events)), nil
}

type imageMetadataUpdateTestUOW struct {
	store  *imageMetadataUpdateTestStore
	events *imageMetadataUpdateTestEvents
	calls  int
	err    error
}

func (uow *imageMetadataUpdateTestUOW) Within(ctx context.Context, operation func(context.Context) error) error {
	uow.calls++
	if uow.err != nil {
		return uow.err
	}
	beforeRow := uow.store.row
	beforeEvents := append([]eventport.Event(nil), uow.events.events...)
	err := operation(ctx)
	if err != nil {
		uow.store.row = beforeRow
		uow.events.events = beforeEvents
	}
	return err
}

func TestUpdateImageMetadataNormalizesChangesAndWritesOneRedactedEvent(t *testing.T) {
	store := &imageMetadataUpdateTestStore{row: imageMetadataUpdateFixture()}
	events := &imageMetadataUpdateTestEvents{}
	uow := &imageMetadataUpdateTestUOW{store: store, events: events}
	service := NewImageMetadataService(uow, store, events)
	now := store.row.UpdatedAt.Add(time.Minute)
	service.now = func() time.Time { return now }
	name := "  新名称  "
	description := "  更新说明  "
	tags := []string{" hero ", "hero", "首页", " 首页 ", "新品"}
	category := "  banner "
	enabled := false
	result, err := service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: store.row.ID, Actor: 7,
		Patch: ImageMetadataPatch{Name: &name, Description: &description, Tags: &tags, Category: &category, Enabled: &enabled}})
	if err != nil {
		t.Fatal(err)
	}
	if uow.calls != 1 || store.lockCalls != 1 || store.updateCalls != 1 || len(events.events) != 1 {
		t.Fatalf("uow=%d locks=%d updates=%d events=%d", uow.calls, store.lockCalls, store.updateCalls, len(events.events))
	}
	if result.Name != "新名称" || result.Description != "更新说明" || result.Tags != "hero,首页,新品" || result.Category != "banner" || result.Enabled || !result.UpdatedAt.Equal(now) {
		t.Fatalf("result=%#v", result)
	}
	if result.FileName != "cover.png" || result.MimeType != "image/png" || result.FileSize != 42 || result.Width != 640 || result.Height != 480 || !result.CreatedAt.Equal(store.row.CreatedAt) {
		t.Fatalf("immutable facts changed: %#v", result)
	}
	event := events.events[0]
	if event.Type != "media.image_metadata_updated" || !event.OccurredAt.Equal(now) || len(event.IdempotencyKey) != len("media.image_metadata_updated:")+64 {
		t.Fatalf("event=%#v", event)
	}
	var payload struct {
		ImageID       int64    `json:"image_id"`
		Actor         int64    `json:"actor"`
		ChangedFields []string `json:"changed_fields"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ImageID != result.ID || payload.Actor != 7 || !reflect.DeepEqual(payload.ChangedFields, []string{"category", "description", "enabled", "name", "tags"}) ||
		string(event.Payload) != `{"image_id":11,"actor":7,"changed_fields":["category","description","enabled","name","tags"]}` {
		t.Fatalf("payload=%s decoded=%#v", event.Payload, payload)
	}
}

func TestUpdateImageMetadataNoOpAndRetryDoNotWriteOrEmit(t *testing.T) {
	store := &imageMetadataUpdateTestStore{row: imageMetadataUpdateFixture()}
	events := &imageMetadataUpdateTestEvents{}
	service := NewImageMetadataService(&imageMetadataUpdateTestUOW{store: store, events: events}, store, events)
	service.now = func() time.Time { return store.row.UpdatedAt.Add(time.Hour) }
	for _, patch := range []ImageMetadataPatch{{}, sameImageMetadataPatch(store.row)} {
		result, err := service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 9, Patch: patch})
		if err != nil || !reflect.DeepEqual(result, store.row) {
			t.Fatalf("patch=%#v result=%#v err=%v", patch, result, err)
		}
	}
	if store.lockCalls != 2 || store.updateCalls != 0 || len(events.events) != 0 {
		t.Fatalf("locks=%d updates=%d events=%d", store.lockCalls, store.updateCalls, len(events.events))
	}
}

func TestUpdateImageMetadataEventFailureRollsBackAndSanitizes(t *testing.T) {
	store := &imageMetadataUpdateTestStore{row: imageMetadataUpdateFixture()}
	before := store.row
	events := &imageMetadataUpdateTestEvents{err: errors.New("private event backend failure actor=7")}
	service := NewImageMetadataService(&imageMetadataUpdateTestUOW{store: store, events: events}, store, events)
	name := "changed"
	result, err := service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 7, Patch: ImageMetadataPatch{Name: &name}})
	if !errors.Is(err, ErrImageMetadataUnavailable) || result.ID != 0 || !reflect.DeepEqual(store.row, before) || len(events.events) != 0 || store.updateCalls != 1 {
		t.Fatalf("result=%#v err=%v row=%#v events=%#v updates=%d", result, err, store.row, events.events, store.updateCalls)
	}
}

func TestUpdateImageMetadataRejectsInvalidOrMissingRowsWithoutLeak(t *testing.T) {
	store := &imageMetadataUpdateTestStore{row: imageMetadataUpdateFixture(), lockErr: ErrImageMetadataNotFound}
	events := &imageMetadataUpdateTestEvents{}
	service := NewImageMetadataService(&imageMetadataUpdateTestUOW{store: store, events: events}, store, events)
	name := "x"
	_, err := service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 7, Patch: ImageMetadataPatch{Name: &name}})
	if !errors.Is(err, ErrImageMetadataNotFound) || store.updateCalls != 0 || len(events.events) != 0 {
		t.Fatalf("err=%v updates=%d events=%d", err, store.updateCalls, len(events.events))
	}
	badTags := []string{""}
	_, err = service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 7, Patch: ImageMetadataPatch{Tags: &badTags}})
	if !errors.Is(err, ErrInvalidImageMetadataUpdate) || store.lockCalls != 1 {
		t.Fatalf("err=%v locks=%d", err, store.lockCalls)
	}
	for _, mutate := range []func(*ImageMetadata){
		func(image *ImageMetadata) { image.FileSize = 10<<20 + 1 },
		func(image *ImageMetadata) { image.MimeType = "image/webp" },
		func(image *ImageMetadata) { image.Width, image.Height = 10_000, 10_000 },
	} {
		corrupt := imageMetadataUpdateFixture()
		mutate(&corrupt)
		corruptStore := &imageMetadataUpdateTestStore{row: corrupt}
		corruptEvents := &imageMetadataUpdateTestEvents{}
		corruptService := NewImageMetadataService(&imageMetadataUpdateTestUOW{store: corruptStore, events: corruptEvents}, corruptStore, corruptEvents)
		_, err := corruptService.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 7, Patch: ImageMetadataPatch{Name: &name}})
		if !errors.Is(err, ErrImageMetadataUnavailable) || corruptStore.updateCalls != 0 || len(corruptEvents.events) != 0 {
			t.Fatalf("corrupt=%#v err=%v updates=%d events=%d", corrupt, err, corruptStore.updateCalls, len(corruptEvents.events))
		}
	}
}

func TestUpdateImageMetadataCountsUnicodeCodePointsNotBytes(t *testing.T) {
	store := &imageMetadataUpdateTestStore{row: imageMetadataUpdateFixture()}
	events := &imageMetadataUpdateTestEvents{}
	service := NewImageMetadataService(&imageMetadataUpdateTestUOW{store: store, events: events}, store, events)
	name := strings.Repeat("界", 200)
	result, err := service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 7, Patch: ImageMetadataPatch{Name: &name}})
	if err != nil || result.Name != name || store.updateCalls != 1 || len(events.events) != 1 {
		t.Fatalf("result=%#v err=%v updates=%d events=%d", result, err, store.updateCalls, len(events.events))
	}
}

func TestUpdateImageMetadataFieldBoundariesAndCanonicalTagAtoms(t *testing.T) {
	makeTags := func(count int) []string {
		tags := make([]string, count)
		for index := range tags {
			tags[index] = "tag" + strconv.Itoa(index)
		}
		return tags
	}
	for _, test := range []struct {
		name    string
		patch   ImageMetadataPatch
		invalid bool
	}{
		{"name zero", imageMetadataTextPatch("name", ""), false},
		{"name one", imageMetadataTextPatch("name", "x"), false},
		{"name 200", imageMetadataTextPatch("name", strings.Repeat("x", 200)), false},
		{"name 201", imageMetadataTextPatch("name", strings.Repeat("x", 201)), true},
		{"description zero", imageMetadataTextPatch("description", ""), false},
		{"description one", imageMetadataTextPatch("description", "x"), false},
		{"description 10000", imageMetadataTextPatch("description", strings.Repeat("x", 10_000)), false},
		{"description 10001", imageMetadataTextPatch("description", strings.Repeat("x", 10_001)), true},
		{"category zero", imageMetadataTextPatch("category", ""), false},
		{"category one", imageMetadataTextPatch("category", "x"), false},
		{"category 200", imageMetadataTextPatch("category", strings.Repeat("x", 200)), false},
		{"category 201", imageMetadataTextPatch("category", strings.Repeat("x", 201)), true},
		{"tags zero", ImageMetadataPatch{Tags: imageMetadataTagsPointer([]string{})}, false},
		{"tags one", ImageMetadataPatch{Tags: imageMetadataTagsPointer([]string{"x"})}, false},
		{"tags 50", ImageMetadataPatch{Tags: imageMetadataTagsPointer(makeTags(50))}, false},
		{"tags 51", ImageMetadataPatch{Tags: imageMetadataTagsPointer(makeTags(51))}, true},
		{"tag 64", ImageMetadataPatch{Tags: imageMetadataTagsPointer([]string{strings.Repeat("x", 64)})}, false},
		{"tag 65", ImageMetadataPatch{Tags: imageMetadataTagsPointer([]string{strings.Repeat("x", 65)})}, true},
		{"comma tag", ImageMetadataPatch{Tags: imageMetadataTagsPointer([]string{"one,two"})}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &imageMetadataUpdateTestStore{row: imageMetadataUpdateFixture()}
			events := &imageMetadataUpdateTestEvents{}
			service := NewImageMetadataService(&imageMetadataUpdateTestUOW{store: store, events: events}, store, events)
			_, err := service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 7, Patch: test.patch})
			if test.invalid {
				if !errors.Is(err, ErrInvalidImageMetadataUpdate) || store.lockCalls != 0 || store.updateCalls != 0 || len(events.events) != 0 {
					t.Fatalf("err=%v locks=%d updates=%d events=%d", err, store.lockCalls, store.updateCalls, len(events.events))
				}
				return
			}
			if err != nil || store.lockCalls != 1 || store.updateCalls != 1 || len(events.events) != 1 {
				t.Fatalf("err=%v locks=%d updates=%d events=%d", err, store.lockCalls, store.updateCalls, len(events.events))
			}
		})
	}
}

func TestUpdateImageMetadataFailsClosedForNonCanonicalPersistedTags(t *testing.T) {
	makeTags := func(count int) string {
		tags := make([]string, count)
		for index := range tags {
			tags[index] = "tag" + strconv.Itoa(index)
		}
		return strings.Join(tags, ",")
	}
	for _, tags := range []string{
		"hero,",
		" hero",
		strings.Repeat("x", 65),
		makeTags(51),
		string([]byte{0xff}),
	} {
		store := &imageMetadataUpdateTestStore{row: imageMetadataUpdateFixture()}
		store.row.Tags = tags
		events := &imageMetadataUpdateTestEvents{}
		service := NewImageMetadataService(&imageMetadataUpdateTestUOW{store: store, events: events}, store, events)
		name := "changed"
		_, err := service.UpdateImageMetadata(context.Background(), ImageMetadataUpdateCommand{ImageID: 11, Actor: 7, Patch: ImageMetadataPatch{Name: &name}})
		if !errors.Is(err, ErrImageMetadataUnavailable) || store.lockCalls != 1 || store.updateCalls != 0 || len(events.events) != 0 {
			t.Fatalf("tags=%q err=%v locks=%d updates=%d events=%d", tags, err, store.lockCalls, store.updateCalls, len(events.events))
		}
	}
}

func imageMetadataUpdateFixture() ImageMetadata {
	created := time.Date(2026, 8, 19, 9, 0, 0, 123456000, time.UTC)
	return ImageMetadata{ID: 11, Name: "cover", FileName: "cover.png", MimeType: "image/png", FileSize: 42, Enabled: true,
		Description: "before", Tags: "hero,首页", Category: "cover", Width: 640, Height: 480, CreatedAt: created, UpdatedAt: created.Add(time.Second)}
}

func sameImageMetadataPatch(image ImageMetadata) ImageMetadataPatch {
	name, description, category, enabled := image.Name, image.Description, image.Category, image.Enabled
	tags := []string{"hero", "首页"}
	return ImageMetadataPatch{Name: &name, Description: &description, Tags: &tags, Category: &category, Enabled: &enabled}
}

func imageMetadataTextPatch(field, value string) ImageMetadataPatch {
	switch field {
	case "name":
		return ImageMetadataPatch{Name: &value}
	case "description":
		return ImageMetadataPatch{Description: &value}
	case "category":
		return ImageMetadataPatch{Category: &value}
	default:
		panic("unsupported field")
	}
}

func imageMetadataTagsPointer(tags []string) *[]string {
	return &tags
}
