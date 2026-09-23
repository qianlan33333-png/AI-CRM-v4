package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	eventport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type memoryState struct {
	receipts map[string]Receipt
	images   []mediaport.Image
	events   []eventport.Event
	fail     bool
}
type memoryUOW struct{ state *memoryState }
type memoryStore struct{ state *memoryState }
type memoryEvents struct{ state *memoryState }

func (u memoryUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	backupReceipts := make(map[string]Receipt, len(u.state.receipts))
	for key, value := range u.state.receipts {
		backupReceipts[key] = value
	}
	images, events := append([]mediaport.Image(nil), u.state.images...), append([]eventport.Event(nil), u.state.events...)
	if err := fn(ctx); err != nil {
		u.state.receipts, u.state.images, u.state.events = backupReceipts, images, events
		return err
	}
	return nil
}
func receiptKey(actor string, digest [32]byte) string { return actor + ":" + string(digest[:]) }
func (s memoryStore) Reserve(_ context.Context, input Reservation) (Receipt, bool, error) {
	key := receiptKey(input.ActorScope, input.KeyDigest)
	if old, ok := s.state.receipts[key]; ok {
		return old, false, nil
	}
	value := Receipt{ID: int64(len(s.state.receipts) + 1), ActorScope: input.ActorScope, KeyDigest: input.KeyDigest, PayloadDigest: input.PayloadDigest, State: "in_progress"}
	s.state.receipts[key] = value
	return value, true, nil
}
func (s memoryStore) Create(_ context.Context, input CreateInput) (mediaport.Image, error) {
	enabled := input.Command.Enabled == nil || *input.Command.Enabled
	value := mediaport.Image{ID: int64(len(s.state.images) + 1), Name: input.Command.Name, FileName: input.Command.FileName,
		FileSize: int32(len(input.Command.Content)), MimeType: input.MediaType, Width: input.Width, Height: input.Height,
		Description: input.Command.Description, Tags: input.Command.Tags, Category: input.Command.Category,
		Enabled:   enabled,
		CreatedAt: input.Now, UpdatedAt: input.Now}
	s.state.images = append(s.state.images, value)
	return value, nil
}
func (s memoryStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	for key, value := range s.state.receipts {
		if value.ID == id {
			value.State, value.ResultSnapshot = "completed", append([]byte(nil), snapshot...)
			s.state.receipts[key] = value
			return value, nil
		}
	}
	return Receipt{}, ErrUnavailable
}
func (e memoryEvents) Append(_ context.Context, value eventport.Event) (eventport.EventID, error) {
	if e.state.fail {
		return 0, errors.New("event unavailable")
	}
	e.state.events = append(e.state.events, value)
	return eventport.EventID(len(e.state.events)), nil
}

func TestUploadActorScopedReplayConflictAndRollback(t *testing.T) {
	state := &memoryState{receipts: map[string]Receipt{}}
	service := NewImageUploadService(memoryUOW{state}, memoryStore{state}, memoryEvents{state})
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	command := validCommand(t)
	first, err := service.Upload(context.Background(), command)
	if err != nil || len(state.images) != 1 || len(state.events) != 1 || state.events[0].Type != eventport.EvMediaImageCreated {
		t.Fatalf("create failed: %#v %v images=%d events=%d", first, err, len(state.images), len(state.events))
	}
	replay, err := service.Upload(context.Background(), command)
	if err != nil || replay != first || len(state.images) != 1 || len(state.events) != 1 {
		t.Fatalf("replay changed effects: %#v %v", replay, err)
	}
	conflict := command
	conflict.Name = "different"
	if _, err = service.Upload(context.Background(), conflict); !errors.Is(err, ErrConflict) || len(state.images) != 1 || len(state.events) != 1 {
		t.Fatalf("conflict not isolated: %v", err)
	}
	otherActor := command
	otherActor.Actor = 8
	if _, err = service.Upload(context.Background(), otherActor); err != nil || len(state.images) != 2 || len(state.events) != 2 || state.events[0].IdempotencyKey == state.events[1].IdempotencyKey {
		t.Fatalf("actor isolation failed: %v", err)
	}
	state.fail = true
	failing := command
	failing.IdempotencyKey = "rollback-key-000001"
	if _, err = service.Upload(context.Background(), failing); !errors.Is(err, ErrUnavailable) || len(state.images) != 2 || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("rollback failed: %v state=%#v", err, state)
	}
}

func TestUploadEnabledDigestAndLegacyReceiptReplay(t *testing.T) {
	command := validCommand(t)
	inspection, err := domain.Inspect(command.FileName, command.DeclaredType, command.Content)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(command.Content)
	nilPayload, err := uploadPayload(command, inspection.MediaType, checksum, inspection.Width, inspection.Height)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	trueCommand := command
	trueCommand.Enabled = &enabled
	truePayload, err := uploadPayload(trueCommand, inspection.MediaType, checksum, inspection.Width, inspection.Height)
	if err != nil || string(nilPayload) != string(truePayload) {
		t.Fatalf("nil/true payload compatibility = %q / %q err=%v", nilPayload, truePayload, err)
	}
	disabled := false
	falseCommand := command
	falseCommand.Enabled = &disabled
	falsePayload, err := uploadPayload(falseCommand, inspection.MediaType, checksum, inspection.Width, inspection.Height)
	if err != nil || string(nilPayload) == string(falsePayload) {
		t.Fatalf("false payload did not distinguish: %q / %q err=%v", nilPayload, falsePayload, err)
	}

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	legacySnapshot, err := json.Marshal(struct {
		ID          int64     `json:"id"`
		Name        string    `json:"name"`
		FileName    string    `json:"file_name"`
		FileSize    int32     `json:"file_size"`
		MimeType    string    `json:"mime_type"`
		Width       int32     `json:"width"`
		Height      int32     `json:"height"`
		Description string    `json:"description"`
		Tags        string    `json:"tags"`
		Category    string    `json:"category"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
	}{1, command.Name, command.FileName, int32(len(command.Content)), inspection.MediaType, inspection.Width, inspection.Height, command.Description, command.Tags, command.Category, now, now})
	if err != nil {
		t.Fatal(err)
	}
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	payloadDigest := sha256.Sum256(nilPayload)
	state := &memoryState{receipts: map[string]Receipt{
		receiptKey("admin:7", keyDigest): {
			ID: 1, ActorScope: "admin:7", KeyDigest: keyDigest, PayloadDigest: payloadDigest, State: "completed", ResultSnapshot: legacySnapshot,
		},
	}}
	service := NewImageUploadService(memoryUOW{state}, memoryStore{state}, memoryEvents{state})
	service.now = func() time.Time { return now }
	replay, err := service.Upload(context.Background(), trueCommand)
	if err != nil || !replay.Enabled || len(state.images) != 0 || len(state.events) != 0 {
		t.Fatalf("legacy receipt replay = %#v err=%v images=%d events=%d", replay, err, len(state.images), len(state.events))
	}
	if _, err := service.Upload(context.Background(), falseCommand); !errors.Is(err, ErrConflict) {
		t.Fatalf("false against legacy true receipt error=%v, want conflict", err)
	}
}

func TestUploadTextRuneBoundariesAndUTF8AreValidatedBeforeUoW(t *testing.T) {
	for _, test := range []struct {
		name  string
		apply func(*mediaport.UploadCommand)
		want  error
	}{
		{"name exact Chinese runes", func(command *mediaport.UploadCommand) { command.Name = strings.Repeat("名", 200) }, nil},
		{"name Chinese runes over limit", func(command *mediaport.UploadCommand) { command.Name = strings.Repeat("名", 201) }, ErrInvalidUpload},
		{"description exact Chinese runes", func(command *mediaport.UploadCommand) { command.Description = strings.Repeat("描", 10_000) }, nil},
		{"description Chinese runes over limit", func(command *mediaport.UploadCommand) { command.Description = strings.Repeat("描", 10_001) }, ErrInvalidUpload},
		{"category exact Chinese runes", func(command *mediaport.UploadCommand) { command.Category = strings.Repeat("类", 200) }, nil},
		{"category Chinese runes over limit", func(command *mediaport.UploadCommand) { command.Category = strings.Repeat("类", 201) }, ErrInvalidUpload},
		{"tags exact Chinese runes", func(command *mediaport.UploadCommand) { command.Tags = strings.Repeat("标", 10_000) }, nil},
		{"tags Chinese runes over limit", func(command *mediaport.UploadCommand) { command.Tags = strings.Repeat("标", 10_001) }, ErrInvalidUpload},
		{"invalid UTF-8", func(command *mediaport.UploadCommand) { command.Name = string([]byte{0xff}) }, ErrInvalidUpload},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &memoryState{receipts: map[string]Receipt{}}
			service := NewImageUploadService(memoryUOW{state}, memoryStore{state}, memoryEvents{state})
			service.now = func() time.Time { return time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC) }
			command := validCommand(t)
			command.IdempotencyKey = strings.Repeat("i", 16)
			test.apply(&command)
			_, err := service.Upload(context.Background(), command)
			if !errors.Is(err, test.want) || (test.want == nil && err != nil) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if test.want != nil && (len(state.receipts) != 0 || len(state.images) != 0 || len(state.events) != 0) {
				t.Fatalf("invalid text reached UoW: %#v", state)
			}
		})
	}
}

func validCommand(t *testing.T) mediaport.UploadCommand {
	t.Helper()
	var data bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 5, A: 255})
	if err := png.Encode(&data, pixel); err != nil {
		t.Fatal(err)
	}
	return mediaport.UploadCommand{Actor: 7, IdempotencyKey: "upload-key-000001", FileName: "image.png", DeclaredType: "image/png", Content: data.Bytes(), Name: "image", Description: "desc", Tags: "a,b", Category: "cover"}
}
