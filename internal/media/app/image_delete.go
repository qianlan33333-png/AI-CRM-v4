package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrInvalidImageDelete     = errors.New("invalid image delete")
	ErrImageDeleteNotFound    = errors.New("image delete not found")
	ErrImageDeleteConflict    = errors.New("image delete conflict")
	ErrImageHasReferences     = errors.New("image has references")
	ErrImageDeleteUnavailable = errors.New("image delete unavailable")
)

type ImageDeleteCommand struct {
	ImageID        int64
	Actor          int64
	IdempotencyKey string
	Force          bool
}

type ImageDeleteReferences struct {
	Miniprograms     []int64 `json:"miniprograms"`
	CampaignSteps    []int64 `json:"campaign_steps"`
	GroupInvites     []int64 `json:"group_invites"`
	AutomationAgents []int64 `json:"automation_agents"`
	Channels         []int64 `json:"channels"`
	RadarLinks       []int64 `json:"radar_links"`
	ImportPreflights []int64 `json:"import_preflights"`
}

func (references ImageDeleteReferences) Any() bool {
	return len(references.Miniprograms) != 0 || len(references.CampaignSteps) != 0 ||
		len(references.GroupInvites) != 0 || len(references.AutomationAgents) != 0 ||
		len(references.Channels) != 0 || len(references.RadarLinks) != 0 || len(references.ImportPreflights) != 0
}

type ImageDeleteResult struct {
	ID          int64                 `json:"id"`
	Deleted     bool                  `json:"deleted"`
	HardDeleted bool                  `json:"hard_deleted"`
	References  ImageDeleteReferences `json:"references"`
}

type ImageDeleteReceipt struct {
	ID, BusinessKey          int64
	ActorScope, State        string
	KeyDigest, PayloadDigest [32]byte
	ResultSnapshot           json.RawMessage
}

type ImageDeleteReservation struct {
	ActorScope               string
	BusinessKey              int64
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}

// ImageDeleteStore owns only Media's rows and Media-local reference facts.
// Automation and Contact references remain behind their owners' public ports.
type ImageDeleteStore interface {
	LockImageForDelete(context.Context, int64) (bool, error)
	ListImageDeleteMediaReferences(context.Context, int64) (ImageDeleteReferences, error)
	GetImageDeleteReceipt(context.Context, ImageDeleteReservation) (ImageDeleteReceipt, bool, error)
	ReserveImageDelete(context.Context, ImageDeleteReservation) (ImageDeleteReceipt, bool, error)
	DeleteImage(context.Context, int64) (int64, error)
	CompleteImageDelete(context.Context, int64, json.RawMessage, time.Time) (ImageDeleteReceipt, error)
}

type ImageDeleteService struct {
	uow        platformport.UnitOfWork
	store      ImageDeleteStore
	automation mediaport.AutomationImageReferenceReader
	contact    mediaport.ChannelImageReferenceReader
	radar      mediaport.RadarImageReferenceReader
	events     mediaport.EventAppender
	now        func() time.Time
}

func NewImageDeleteService(uow platformport.UnitOfWork, store ImageDeleteStore, automation mediaport.AutomationImageReferenceReader, contact mediaport.ChannelImageReferenceReader, radar mediaport.RadarImageReferenceReader, events mediaport.EventAppender) *ImageDeleteService {
	return &ImageDeleteService{uow: uow, store: store, automation: automation, contact: contact, radar: radar, events: events, now: time.Now}
}

func (service *ImageDeleteService) DeleteImage(ctx context.Context, command ImageDeleteCommand) (ImageDeleteResult, error) {
	if service == nil || service.uow == nil || service.store == nil || service.automation == nil || service.contact == nil || service.radar == nil || service.events == nil || service.now == nil ||
		command.ImageID < 1 || command.Actor < 1 || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 || command.IdempotencyKey != strings.TrimSpace(command.IdempotencyKey) {
		return ImageDeleteResult{}, ErrInvalidImageDelete
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return ImageDeleteResult{}, ErrImageDeleteUnavailable
	}
	payload, err := json.Marshal(struct {
		ImageID int64 `json:"image_id"`
		Force   bool  `json:"force"`
	}{ImageID: command.ImageID, Force: command.Force})
	if err != nil {
		return ImageDeleteResult{}, ErrImageDeleteUnavailable
	}
	reservation := ImageDeleteReservation{
		ActorScope: fmt.Sprintf("admin:%d", command.Actor), BusinessKey: command.ImageID,
		KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now,
	}
	var result ImageDeleteResult
	err = service.uow.Within(ctx, func(tx context.Context) error {
		// A completed receipt is an actor/key-scoped stable result. Checking it
		// first makes ordinary retries avoid touching the image or its owners.
		if receipt, found, receiptErr := service.store.GetImageDeleteReceipt(tx, reservation); receiptErr != nil {
			return ErrImageDeleteUnavailable
		} else if found {
			return replayImageDeleteReceipt(receipt, reservation, &result)
		}
		exists, lockErr := service.store.LockImageForDelete(tx, command.ImageID)
		if lockErr != nil {
			return ErrImageDeleteUnavailable
		}
		if !exists {
			return service.replayOrNotFound(tx, reservation, &result)
		}
		// Check the actor/key binding before looking at any current references. A
		// reused key for a different command is a generic conflict, not an
		// opportunity to reveal which resources currently reference this image.
		receipt, found, receiptErr := service.store.GetImageDeleteReceipt(tx, reservation)
		if receiptErr != nil {
			return ErrImageDeleteUnavailable
		}
		if found {
			return replayImageDeleteReceipt(receipt, reservation, &result)
		}
		references, referenceErr := service.references(tx, command.ImageID)
		if referenceErr != nil {
			return ErrImageDeleteUnavailable
		}
		if references.Any() {
			result = ImageDeleteResult{ID: command.ImageID, References: references}
			return ErrImageHasReferences
		}
		receipt, owned, reserveErr := service.store.ReserveImageDelete(tx, reservation)
		if reserveErr != nil || !validImageDeleteReceipt(receipt, reservation) {
			return ErrImageDeleteUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 || receipt.BusinessKey != reservation.BusinessKey {
			return ErrImageDeleteConflict
		}
		if !owned {
			return replayImageDeleteReceipt(receipt, reservation, &result)
		}
		deleted, deleteErr := service.store.DeleteImage(tx, command.ImageID)
		if deleteErr != nil || deleted != 1 {
			return ErrImageDeleteUnavailable
		}
		result = ImageDeleteResult{ID: command.ImageID, Deleted: true, HardDeleted: true, References: emptyImageDeleteReferences()}
		snapshot, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return ErrImageDeleteUnavailable
		}
		eventPayload, marshalErr := json.Marshal(struct {
			ImageID int64 `json:"image_id"`
			Actor   int64 `json:"actor"`
		}{ImageID: command.ImageID, Actor: command.Actor})
		if marshalErr != nil {
			return ErrImageDeleteUnavailable
		}
		eventDigest := sha256.Sum256([]byte(reservation.ActorScope + "\x00" + command.IdempotencyKey))
		if _, appendErr := service.events.Append(tx, mediaport.Event{Type: "media.image_deleted", Payload: eventPayload, OccurredAt: now, IdempotencyKey: "media.image_deleted:" + hex.EncodeToString(eventDigest[:])}); appendErr != nil {
			return ErrImageDeleteUnavailable
		}
		completed, completeErr := service.store.CompleteImageDelete(tx, receipt.ID, snapshot, now)
		if completeErr != nil || !validImageDeleteReceipt(completed, reservation) || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
			return ErrImageDeleteUnavailable
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrImageDeleteNotFound), errors.Is(err, ErrImageDeleteConflict), errors.Is(err, ErrImageHasReferences):
			return result, err
		default:
			return ImageDeleteResult{}, ErrImageDeleteUnavailable
		}
	}
	return result, nil
}

func (service *ImageDeleteService) replayOrNotFound(ctx context.Context, reservation ImageDeleteReservation, result *ImageDeleteResult) error {
	receipt, found, receiptErr := service.store.GetImageDeleteReceipt(ctx, reservation)
	if receiptErr != nil {
		return ErrImageDeleteUnavailable
	}
	if !found {
		return ErrImageDeleteNotFound
	}
	return replayImageDeleteReceipt(receipt, reservation, result)
}

func (service *ImageDeleteService) references(ctx context.Context, imageID int64) (ImageDeleteReferences, error) {
	references, err := service.store.ListImageDeleteMediaReferences(ctx, imageID)
	if err != nil {
		return ImageDeleteReferences{}, err
	}
	references.AutomationAgents, err = service.automation.ListImageReferenceAgentIDs(ctx, imageID)
	if err != nil {
		return ImageDeleteReferences{}, err
	}
	references.Channels, err = service.contact.ListImageReferenceChannelIDs(ctx, imageID)
	if err != nil {
		return ImageDeleteReferences{}, err
	}
	references.RadarLinks, err = service.radar.ListImageReferenceLinkIDs(ctx, imageID)
	if err != nil {
		return ImageDeleteReferences{}, err
	}
	if !validImageDeleteReferences(references) {
		return ImageDeleteReferences{}, ErrImageDeleteUnavailable
	}
	return references, nil
}

func replayImageDeleteReceipt(receipt ImageDeleteReceipt, reservation ImageDeleteReservation, result *ImageDeleteResult) error {
	if !validImageDeleteReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 || receipt.BusinessKey != reservation.BusinessKey {
		return ErrImageDeleteConflict
	}
	if receipt.State != "completed" || result == nil || json.Unmarshal(receipt.ResultSnapshot, result) != nil || !validImageDeleteResult(*result) || result.ID != reservation.BusinessKey {
		return ErrImageDeleteUnavailable
	}
	canonical, err := json.Marshal(*result)
	if err != nil || !jsonEquivalent(canonical, receipt.ResultSnapshot) {
		return ErrImageDeleteUnavailable
	}
	return nil
}

func validImageDeleteReceipt(receipt ImageDeleteReceipt, reservation ImageDeleteReservation) bool {
	return receipt.ID > 0 && receipt.ActorScope == reservation.ActorScope && receipt.BusinessKey == reservation.BusinessKey &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 && (receipt.State == "in_progress" || receipt.State == "completed")
}

func emptyImageDeleteReferences() ImageDeleteReferences {
	return ImageDeleteReferences{Miniprograms: []int64{}, CampaignSteps: []int64{}, GroupInvites: []int64{}, AutomationAgents: []int64{}, Channels: []int64{}, RadarLinks: []int64{}, ImportPreflights: []int64{}}
}

func validImageDeleteResult(result ImageDeleteResult) bool {
	return result.ID > 0 && result.Deleted && result.HardDeleted && !result.References.Any() && validImageDeleteReferences(result.References)
}

func validImageDeleteReferences(references ImageDeleteReferences) bool {
	return sortedPositiveImageReferenceIDs(references.Miniprograms) && sortedPositiveImageReferenceIDs(references.CampaignSteps) &&
		sortedPositiveImageReferenceIDs(references.GroupInvites) && sortedPositiveImageReferenceIDs(references.AutomationAgents) &&
		sortedPositiveImageReferenceIDs(references.Channels) && sortedPositiveImageReferenceIDs(references.RadarLinks) &&
		sortedPositiveImageReferenceIDs(references.ImportPreflights)
}

func sortedPositiveImageReferenceIDs(values []int64) bool {
	return values != nil && sort.SliceIsSorted(values, func(left, right int) bool { return values[left] < values[right] }) && allPositiveUnique(values)
}

func allPositiveUnique(values []int64) bool {
	for index, value := range values {
		if value < 1 || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}
