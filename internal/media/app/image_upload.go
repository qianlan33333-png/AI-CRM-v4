package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrInvalidUpload = errors.New("invalid image upload")
	ErrConflict      = errors.New("image upload conflict")
	ErrUnavailable   = errors.New("image upload unavailable")
)

type Reservation struct {
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}
type Receipt struct {
	ID                       int64
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	State                    string
	ResultSnapshot           json.RawMessage
}
type CreateInput struct {
	Command   mediaport.UploadCommand
	MediaType string
	Width     int32
	Height    int32
	Checksum  [32]byte
	Now       time.Time
}
type Store interface {
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Create(context.Context, CreateInput) (mediaport.Image, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}
type ImageUploadService struct {
	uow    platformport.UnitOfWork
	store  Store
	events mediaport.EventAppender
	now    func() time.Time
}

func NewImageUploadService(uow platformport.UnitOfWork, store Store, events mediaport.EventAppender) *ImageUploadService {
	return &ImageUploadService{uow: uow, store: store, events: events, now: time.Now}
}

func (s *ImageUploadService) Upload(ctx context.Context, command mediaport.UploadCommand) (mediaport.Image, error) {
	command.Name = strings.TrimSpace(command.Name)
	command.Description = strings.TrimSpace(command.Description)
	command.Tags = strings.TrimSpace(command.Tags)
	command.Category = strings.TrimSpace(command.Category)
	inspection, err := domain.Inspect(command.FileName, command.DeclaredType, command.Content)
	if err != nil || command.Actor < 1 || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 ||
		strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey || !validUploadText(command.Name, 200) ||
		!validUploadText(command.Description, 10_000) || !validUploadText(command.Tags, 10_000) || !validUploadText(command.Category, 200) {
		return mediaport.Image{}, ErrInvalidUpload
	}
	if s == nil || s.uow == nil || s.store == nil || s.events == nil {
		return mediaport.Image{}, ErrUnavailable
	}
	now := s.now().UTC()
	if now.IsZero() {
		return mediaport.Image{}, ErrUnavailable
	}
	checksum := sha256.Sum256(command.Content)
	payload, err := uploadPayload(command, inspection.MediaType, checksum, inspection.Width, inspection.Height)
	if err != nil {
		return mediaport.Image{}, ErrUnavailable
	}
	actorScope := fmt.Sprintf("admin:%d", command.Actor)
	reservation := Reservation{ActorScope: actorScope, KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now}
	var result mediaport.Image
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation)
		if reserveErr != nil || !validReceipt(receipt, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || !decodeUploadReceipt(receipt.ResultSnapshot, &result) || !validImage(result) || result.Enabled != expectedUploadEnabled(command) {
				return ErrUnavailable
			}
			return nil
		}
		result, reserveErr = s.store.Create(tx, CreateInput{Command: command, MediaType: inspection.MediaType, Width: inspection.Width, Height: inspection.Height, Checksum: checksum, Now: now})
		if reserveErr != nil || !validImage(result) || result.Enabled != expectedUploadEnabled(command) {
			return ErrUnavailable
		}
		snapshot, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return ErrUnavailable
		}
		eventPayload, marshalErr := json.Marshal(struct {
			ImageID int64 `json:"image_id"`
			Actor   int64 `json:"actor"`
		}{result.ID, command.Actor})
		if marshalErr != nil {
			return ErrUnavailable
		}
		eventDigest := sha256.Sum256([]byte(actorScope + "\x00" + command.IdempotencyKey))
		if _, reserveErr = s.events.Append(tx, mediaport.Event{Type: mediaport.EventImageCreated, Payload: eventPayload, OccurredAt: now, IdempotencyKey: "media.image_created:" + hex.EncodeToString(eventDigest[:])}); reserveErr != nil {
			return reserveErr
		}
		completed, completeErr := s.store.Complete(tx, receipt.ID, snapshot, now)
		if completeErr != nil || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return mediaport.Image{}, ErrConflict
		}
		return mediaport.Image{}, ErrUnavailable
	}
	return result, nil
}

func validUploadText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes
}

// uploadPayload preserves the exact pre-0357 canonical payload for an
// omitted or explicitly true Enabled value. Only false adds an immutable
// discriminator, so an old completed receipt remains replayable.
func uploadPayload(command mediaport.UploadCommand, mediaType string, checksum [32]byte, width, height int32) ([]byte, error) {
	base := struct {
		FileName, MediaType, Name, Description, Tags, Category string
		Size, Width, Height                                    int32
		Checksum                                               string
	}{command.FileName, mediaType, command.Name, command.Description, command.Tags, command.Category,
		int32(len(command.Content)), width, height, hex.EncodeToString(checksum[:])}
	if command.Enabled == nil || *command.Enabled {
		return json.Marshal(base)
	}
	return json.Marshal(struct {
		FileName, MediaType, Name, Description, Tags, Category string
		Size, Width, Height                                    int32
		Checksum                                               string
		Enabled                                                bool
	}{base.FileName, base.MediaType, base.Name, base.Description, base.Tags, base.Category,
		base.Size, base.Width, base.Height, base.Checksum, false})
}

func expectedUploadEnabled(command mediaport.UploadCommand) bool {
	return command.Enabled == nil || *command.Enabled
}

// decodeUploadReceipt accepts an exact pre-0357 receipt snapshot that lacks
// enabled as the historical enabled=true result. New snapshots must include
// it, so a false result can never be mistaken for that legacy default.
func decodeUploadReceipt(snapshot json.RawMessage, result *mediaport.Image) bool {
	if result == nil || json.Unmarshal(snapshot, result) != nil {
		return false
	}
	var persisted map[string]json.RawMessage
	if json.Unmarshal(snapshot, &persisted) != nil {
		return false
	}
	if _, present := persisted["enabled"]; !present {
		result.Enabled = true
	}
	canonical, err := json.Marshal(*result)
	if err != nil {
		return false
	}
	if jsonEquivalent(canonical, snapshot) {
		return true
	}
	if _, present := persisted["enabled"]; present || !result.Enabled {
		return false
	}
	var normalized map[string]json.RawMessage
	if json.Unmarshal(canonical, &normalized) != nil {
		return false
	}
	delete(normalized, "enabled")
	legacyCanonical, err := json.Marshal(normalized)
	return err == nil && jsonEquivalent(legacyCanonical, snapshot)
}

func validReceipt(receipt Receipt, expected Reservation) bool {
	return receipt.ID > 0 && receipt.ActorScope == expected.ActorScope &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], expected.KeyDigest[:]) == 1 &&
		(receipt.State == "in_progress" || receipt.State == "completed")
}
func validImage(image mediaport.Image) bool {
	return image.ID > 0 && image.FileName != "" && image.FileSize > 0 && image.Width > 0 && image.Height > 0 && !image.CreatedAt.IsZero() && !image.UpdatedAt.IsZero()
}
func jsonEquivalent(left, right []byte) bool {
	var a, b any
	return decodeExact(left, &a) == nil && decodeExact(right, &b) == nil && fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
}
func decodeExact(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrUnavailable
	}
	return nil
}
