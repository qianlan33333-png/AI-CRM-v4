package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const (
	defaultMiniProgramLimit int32 = 100

	miniProgramCreatedEvent           = "media.miniprogram.created"
	miniProgramUpdatedEvent           = "media.miniprogram.updated"
	miniProgramDeletedEvent           = "media.miniprogram.deleted"
	miniProgramThumbnailResolvedEvent = "media.miniprogram.thumbnail_cache_resolved"
)

var (
	ErrInvalidMiniProgramOperation = errors.New("invalid miniprogram material operation")
	ErrMiniProgramNotFound         = errors.New("miniprogram material not found")
	ErrMiniProgramConflict         = errors.New("miniprogram material operation conflict")
	ErrMiniProgramImageNotFound    = errors.New("miniprogram thumbnail image not found")
	ErrMiniProgramUnsafeResolver   = errors.New("miniprogram thumbnail resolver reported a forbidden external effect")
	ErrMiniProgramHasReferences    = errors.New("miniprogram material has channel references")
	ErrMiniProgramUnavailable      = errors.New("miniprogram material service unavailable")
)

type MiniProgramReceipt struct {
	ID                                        int64
	Operation, ActorScope, BusinessKey, State string
	KeyDigest, PayloadDigest                  [32]byte
	ResultSnapshot                            json.RawMessage
}

type MiniProgramReservation struct {
	Operation, ActorScope, BusinessKey string
	KeyDigest, PayloadDigest           [32]byte
	CreatedAt                          time.Time
}

// miniProgramReceiptSnapshot persists the exact canonical command payload
// alongside the result. The reservation binds operation/actor/business key and
// idempotency key; this snapshot closes the replay check over every command
// field, including create's resolve_thumb_media choice.
type miniProgramReceiptSnapshot[T any] struct {
	Command json.RawMessage `json:"command"`
	Result  T               `json:"result"`
}

// MiniProgramStore is deliberately a local persistence seam. Its future SQL
// adapter and unnumbered DDL design are integration work; every mutation here
// still receives the transaction context created by the shared UoW.
type MiniProgramStore interface {
	ListMiniPrograms(context.Context, mediaport.MiniProgramListQuery) ([]mediaport.MiniProgram, error)
	CountMiniPrograms(context.Context, mediaport.MiniProgramListQuery) (int64, error)
	GetMiniProgram(context.Context, int64) (mediaport.MiniProgram, error)
	LockMiniProgram(context.Context, int64) (mediaport.MiniProgram, error)
	CreateMiniProgram(context.Context, mediaport.MiniProgram) (mediaport.MiniProgram, error)
	UpdateMiniProgram(context.Context, mediaport.MiniProgram) (mediaport.MiniProgram, error)
	DeleteMiniProgram(context.Context, int64) error
	ReserveMiniProgram(context.Context, MiniProgramReservation) (MiniProgramReceipt, bool, error)
	CompleteMiniProgram(context.Context, int64, json.RawMessage, time.Time) (MiniProgramReceipt, error)
}

type MiniProgramService struct {
	uow      platformport.UnitOfWork
	store    MiniProgramStore
	images   mediaport.ImageMetadataReader
	events   mediaport.EventAppender
	resolver mediaport.ThumbnailCacheResolver
	contact  mediaport.ChannelMiniProgramDeletionReferenceReader
	now      func() time.Time
}

func NewMiniProgramService(uow platformport.UnitOfWork, store MiniProgramStore, images mediaport.ImageMetadataReader, events mediaport.EventAppender, resolver mediaport.ThumbnailCacheResolver) *MiniProgramService {
	return &MiniProgramService{uow: uow, store: store, images: images, events: events, resolver: resolver, now: time.Now}
}

func NewMiniProgramServiceWithChannelReferences(uow platformport.UnitOfWork, store MiniProgramStore, images mediaport.ImageMetadataReader, events mediaport.EventAppender, resolver mediaport.ThumbnailCacheResolver, contact mediaport.ChannelMiniProgramDeletionReferenceReader) *MiniProgramService {
	return &MiniProgramService{uow: uow, store: store, images: images, events: events, resolver: resolver, contact: contact, now: time.Now}
}

func (service *MiniProgramService) List(ctx context.Context, query mediaport.MiniProgramListQuery) (mediaport.MiniProgramPage, error) {
	if !miniProgramReady(service) {
		return mediaport.MiniProgramPage{}, ErrMiniProgramUnavailable
	}
	if query.Limit == 0 {
		query.Limit = defaultMiniProgramLimit
	}
	query.Search = strings.TrimSpace(query.Search)
	if query.Limit < 1 || query.Offset < 0 {
		return mediaport.MiniProgramPage{}, ErrInvalidMiniProgramOperation
	}
	page := mediaport.MiniProgramPage{Limit: query.Limit, Offset: query.Offset}
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		page.Items, err = service.store.ListMiniPrograms(tx, query)
		if err == nil {
			page.Total, err = service.store.CountMiniPrograms(tx, query)
		}
		return err
	})
	if err != nil {
		return mediaport.MiniProgramPage{}, classifyMiniProgram(err)
	}
	if page.Total < 0 || len(page.Items) > int(query.Limit) || len(page.Items) > 0 && int64(query.Offset)+int64(len(page.Items)) > page.Total || !miniProgramsSorted(page.Items) {
		return mediaport.MiniProgramPage{}, ErrMiniProgramUnavailable
	}
	for _, item := range page.Items {
		if !domain.ValidMiniProgram(item, true) {
			return mediaport.MiniProgramPage{}, ErrMiniProgramUnavailable
		}
	}
	return page, nil
}

func (service *MiniProgramService) Get(ctx context.Context, id int64) (mediaport.MiniProgram, error) {
	if !miniProgramReady(service) || id < 1 {
		return mediaport.MiniProgram{}, ErrInvalidMiniProgramOperation
	}
	var item mediaport.MiniProgram
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		item, err = service.store.GetMiniProgram(tx, id)
		return err
	})
	if err != nil {
		return mediaport.MiniProgram{}, classifyMiniProgram(err)
	}
	if !domain.ValidMiniProgram(item, true) {
		return mediaport.MiniProgram{}, ErrMiniProgramUnavailable
	}
	return item, nil
}

func (service *MiniProgramService) Create(ctx context.Context, command mediaport.MiniProgramCreateCommand) (mediaport.MiniProgramMutationResult, error) {
	now, err := service.commandTime(command.Actor, command.IdempotencyKey)
	if err != nil {
		return mediaport.MiniProgramMutationResult{}, err
	}
	if command.ThumbMediaID.Present {
		return mediaport.MiniProgramMutationResult{}, ErrInvalidMiniProgramOperation
	}
	item, err := domain.NewMiniProgram(command, now)
	if err != nil {
		return mediaport.MiniProgramMutationResult{}, ErrInvalidMiniProgramOperation
	}
	payload, err := json.Marshal(struct {
		Name, AppID, PagePath, Title string
		ThumbnailImageID             *int64
		ThumbMediaID                 mediaport.OptionalString
		ResolveThumbMedia            *bool
		Enabled                      bool
	}{item.Name, item.AppID, item.PagePath, item.Title, item.ThumbnailImageID, command.ThumbMediaID, command.ResolveThumbMedia, item.Enabled})
	if err != nil {
		return mediaport.MiniProgramMutationResult{}, ErrMiniProgramUnavailable
	}
	reservation := service.reservation("create", "create", command.Actor, command.IdempotencyKey, payload, now)
	var result mediaport.MiniProgramMutationResult
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveMiniProgram(tx, reservation)
		if reserveErr := validateMiniProgramReservation(receipt, owned, reservation); reserveErr != nil {
			return reserveErr
		}
		if !owned {
			return decodeMiniProgramMutationReplay(receipt, 0, payload, &result)
		}
		if err := service.validateThumbnailImage(tx, item.ThumbnailImageID); err != nil {
			return err
		}
		result.Item, reserveErr = service.store.CreateMiniProgram(tx, item)
		if reserveErr != nil {
			return reserveErr
		}
		if !domain.ValidMiniProgram(result.Item, true) {
			return ErrMiniProgramUnavailable
		}
		var thumbnailChanged bool
		if result.Item, result.ThumbnailResolve, thumbnailChanged, reserveErr = service.resolveThumbnailFromCache(tx, result.Item, command.Actor, command.ResolveThumbMedia, now); reserveErr != nil {
			return reserveErr
		}
		if thumbnailChanged {
			result.Item, reserveErr = service.store.UpdateMiniProgram(tx, result.Item)
			if reserveErr != nil {
				return reserveErr
			}
			if !domain.ValidMiniProgram(result.Item, true) {
				return ErrMiniProgramUnavailable
			}
		}
		result.Changed = true
		if err := service.appendMiniProgramEvent(tx, miniProgramCreatedEvent, result.Item, command.Actor, reservation, now); err != nil {
			return err
		}
		return service.completeMiniProgramMutation(tx, receipt.ID, reservation, payload, result, now)
	})
	if err != nil {
		return mediaport.MiniProgramMutationResult{}, classifyMiniProgram(err)
	}
	return result, nil
}

func (service *MiniProgramService) Update(ctx context.Context, command mediaport.MiniProgramUpdateCommand) (mediaport.MiniProgramMutationResult, error) {
	now, err := service.commandTime(command.Actor, command.IdempotencyKey)
	if err != nil || command.ID < 1 {
		return mediaport.MiniProgramMutationResult{}, ErrInvalidMiniProgramOperation
	}
	if command.ThumbMediaID.Present {
		return mediaport.MiniProgramMutationResult{}, ErrInvalidMiniProgramOperation
	}
	command.MiniProgramPatch = normalizeMiniProgramPatch(command.MiniProgramPatch)
	payload, err := json.Marshal(struct {
		ID    int64
		Patch mediaport.MiniProgramPatch
	}{command.ID, command.MiniProgramPatch})
	if err != nil {
		return mediaport.MiniProgramMutationResult{}, ErrMiniProgramUnavailable
	}
	reservation := service.reservation("update", fmt.Sprintf("%d", command.ID), command.Actor, command.IdempotencyKey, payload, now)
	var result mediaport.MiniProgramMutationResult
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveMiniProgram(tx, reservation)
		if reserveErr := validateMiniProgramReservation(receipt, owned, reservation); reserveErr != nil {
			return reserveErr
		}
		if !owned {
			return decodeMiniProgramMutationReplay(receipt, command.ID, payload, &result)
		}
		current, reserveErr := service.store.LockMiniProgram(tx, command.ID)
		if reserveErr != nil {
			return reserveErr
		}
		result.Item, result.Changed, reserveErr = domain.ApplyMiniProgramPatch(current, command.MiniProgramPatch, command.Actor, now)
		if reserveErr != nil {
			return ErrInvalidMiniProgramOperation
		}
		if result.Changed && !result.Item.Enabled {
			if err := service.requireNoChannelReferences(tx, command.ID); err != nil {
				return err
			}
		}
		if result.Changed {
			if err := service.validateThumbnailImage(tx, result.Item.ThumbnailImageID); err != nil {
				return err
			}
			result.Item, reserveErr = service.store.UpdateMiniProgram(tx, result.Item)
			if reserveErr != nil {
				return reserveErr
			}
			if !domain.ValidMiniProgram(result.Item, true) {
				return ErrMiniProgramUnavailable
			}
		}
		var thumbnailChanged bool
		if result.Item, result.ThumbnailResolve, thumbnailChanged, reserveErr = service.resolveThumbnailFromCache(tx, result.Item, command.Actor, command.ResolveThumbMedia, now); reserveErr != nil {
			return reserveErr
		}
		if thumbnailChanged {
			result.Item, reserveErr = service.store.UpdateMiniProgram(tx, result.Item)
			if reserveErr != nil {
				return reserveErr
			}
			if !domain.ValidMiniProgram(result.Item, true) {
				return ErrMiniProgramUnavailable
			}
		}
		result.Changed = result.Changed || thumbnailChanged
		if result.Changed {
			if err := service.appendMiniProgramEvent(tx, miniProgramUpdatedEvent, result.Item, command.Actor, reservation, now); err != nil {
				return err
			}
		}
		return service.completeMiniProgramMutation(tx, receipt.ID, reservation, payload, result, now)
	})
	if err != nil {
		return mediaport.MiniProgramMutationResult{}, classifyMiniProgram(err)
	}
	return result, nil
}

func (service *MiniProgramService) Delete(ctx context.Context, command mediaport.MiniProgramDeleteCommand) (mediaport.MiniProgramDeleteResult, error) {
	now, err := service.commandTime(command.Actor, command.IdempotencyKey)
	if err != nil || command.ID < 1 {
		return mediaport.MiniProgramDeleteResult{}, ErrInvalidMiniProgramOperation
	}
	payload, err := json.Marshal(struct{ ID int64 }{command.ID})
	if err != nil {
		return mediaport.MiniProgramDeleteResult{}, ErrMiniProgramUnavailable
	}
	reservation := service.reservation("delete", fmt.Sprintf("%d", command.ID), command.Actor, command.IdempotencyKey, payload, now)
	var result mediaport.MiniProgramDeleteResult
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveMiniProgram(tx, reservation)
		if reserveErr := validateMiniProgramReservation(receipt, owned, reservation); reserveErr != nil {
			return reserveErr
		}
		if !owned {
			return decodeMiniProgramDeleteReplay(receipt, command.ID, payload, &result)
		}
		item, reserveErr := service.store.LockMiniProgram(tx, command.ID)
		if reserveErr != nil {
			return reserveErr
		}
		if !domain.ValidMiniProgram(item, true) {
			return ErrMiniProgramUnavailable
		}
		if err := service.requireNoChannelReferences(tx, command.ID); err != nil {
			return err
		}
		if reserveErr = service.store.DeleteMiniProgram(tx, command.ID); reserveErr != nil {
			return reserveErr
		}
		result = mediaport.MiniProgramDeleteResult{ID: command.ID, Deleted: true}
		if err := service.appendMiniProgramEvent(tx, miniProgramDeletedEvent, item, command.Actor, reservation, now); err != nil {
			return err
		}
		return service.completeMiniProgramDelete(tx, receipt.ID, reservation, payload, result, now)
	})
	if err != nil {
		return mediaport.MiniProgramDeleteResult{}, classifyMiniProgram(err)
	}
	return result, nil
}

func (service *MiniProgramService) requireNoChannelReferences(ctx context.Context, id int64) error {
	if service == nil || service.contact == nil {
		return ErrMiniProgramUnavailable
	}
	references, err := service.contact.ListMiniProgramReferenceChannelIDs(ctx, id)
	if err != nil {
		return ErrMiniProgramUnavailable
	}
	if len(references) != 0 {
		return ErrMiniProgramHasReferences
	}
	return nil
}

func (service *MiniProgramService) ResolveThumbnail(ctx context.Context, command mediaport.MiniProgramResolveThumbnailCommand) (mediaport.MiniProgramThumbnailResolutionResult, error) {
	now, err := service.commandTime(command.Actor, command.IdempotencyKey)
	if err != nil || command.ID < 1 || service == nil || service.resolver == nil {
		return mediaport.MiniProgramThumbnailResolutionResult{}, ErrInvalidMiniProgramOperation
	}
	payload, err := json.Marshal(struct{ ID int64 }{command.ID})
	if err != nil {
		return mediaport.MiniProgramThumbnailResolutionResult{}, ErrMiniProgramUnavailable
	}
	reservation := service.reservation("test-resolve", fmt.Sprintf("%d", command.ID), command.Actor, command.IdempotencyKey, payload, now)
	var result mediaport.MiniProgramThumbnailResolutionResult
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveMiniProgram(tx, reservation)
		if reserveErr := validateMiniProgramReservation(receipt, owned, reservation); reserveErr != nil {
			return reserveErr
		}
		if !owned {
			return decodeMiniProgramResolutionReplay(receipt, command.ID, payload, &result)
		}
		result.Item, reserveErr = service.store.LockMiniProgram(tx, command.ID)
		if reserveErr != nil {
			return reserveErr
		}
		if !domain.ValidMiniProgram(result.Item, true) {
			return ErrMiniProgramUnavailable
		}
		result.Resolution, reserveErr = service.resolver.ResolveThumbnailFromCache(tx, result.Item)
		if reserveErr != nil {
			return reserveErr
		}
		result.Resolution = normalizeThumbnailResolution(result.Resolution)
		if err := validateThumbnailResolution(result.Resolution); err != nil {
			return err
		}
		if result.Resolution.Status != mediaport.ThumbnailResolved {
			// not_available and outcome_unknown are completed local command facts,
			// not material changes. Replays return this result and never retry.
			return service.completeMiniProgramResolution(tx, receipt.ID, reservation, payload, result, now)
		}
		result.Item, result.Changed, reserveErr = domain.ApplyThumbnailCacheResolution(result.Item, result.Resolution, command.Actor, now)
		if reserveErr != nil {
			return ErrMiniProgramUnavailable
		}
		if result.Changed {
			result.Item, reserveErr = service.store.UpdateMiniProgram(tx, result.Item)
			if reserveErr != nil {
				return reserveErr
			}
			if !domain.ValidMiniProgram(result.Item, true) {
				return ErrMiniProgramUnavailable
			}
			if err := service.appendMiniProgramEvent(tx, miniProgramThumbnailResolvedEvent, result.Item, command.Actor, reservation, now); err != nil {
				return err
			}
		}
		return service.completeMiniProgramResolution(tx, receipt.ID, reservation, payload, result, now)
	})
	if err != nil {
		return mediaport.MiniProgramThumbnailResolutionResult{}, classifyMiniProgram(err)
	}
	return result, nil
}

func (service *MiniProgramService) commandTime(actor int64, key string) (time.Time, error) {
	if !miniProgramReady(service) || actor < 1 || len(key) < 16 || len(key) > 128 || strings.TrimSpace(key) != key {
		return time.Time{}, ErrInvalidMiniProgramOperation
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return time.Time{}, ErrMiniProgramUnavailable
	}
	return now, nil
}

func miniProgramReady(service *MiniProgramService) bool {
	return service != nil && service.uow != nil && service.store != nil && service.images != nil && service.events != nil && service.now != nil
}

func (service *MiniProgramService) reservation(operation, businessKey string, actor int64, key string, payload []byte, now time.Time) MiniProgramReservation {
	return MiniProgramReservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), BusinessKey: businessKey,
		KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now}
}

func (service *MiniProgramService) validateThumbnailImage(ctx context.Context, imageID *int64) error {
	if imageID == nil {
		return nil
	}
	exists, err := service.images.ImageExists(ctx, *imageID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrMiniProgramImageNotFound
	}
	return nil
}

// resolveThumbnailFromCache is shared by explicit test-resolve and the legacy
// create/update default. The resolver receives only a local, already validated
// material fact and may report cache state, never a provider effect.
func (service *MiniProgramService) resolveThumbnailFromCache(ctx context.Context, item mediaport.MiniProgram, actor int64, enabled *bool, now time.Time) (mediaport.MiniProgram, *mediaport.ThumbnailCacheResolution, bool, error) {
	if item.ThumbnailImageID == nil || !resolveThumbMedia(enabled) {
		return item, nil, false, nil
	}
	if service == nil || service.resolver == nil || !domain.ValidMiniProgram(item, true) {
		return mediaport.MiniProgram{}, nil, false, ErrMiniProgramUnavailable
	}
	if err := service.validateThumbnailImage(ctx, item.ThumbnailImageID); err != nil {
		return mediaport.MiniProgram{}, nil, false, err
	}
	resolution, err := service.resolver.ResolveThumbnailFromCache(ctx, item)
	if err != nil {
		return mediaport.MiniProgram{}, nil, false, err
	}
	resolution = normalizeThumbnailResolution(resolution)
	if err = validateThumbnailResolution(resolution); err != nil {
		return mediaport.MiniProgram{}, nil, false, err
	}
	if resolution.Status != mediaport.ThumbnailResolved {
		return item, &resolution, false, nil
	}
	updated, changed, err := domain.ApplyThumbnailCacheResolution(item, resolution, actor, now)
	if err != nil {
		return mediaport.MiniProgram{}, nil, false, ErrMiniProgramUnavailable
	}
	return updated, &resolution, changed, nil
}

func resolveThumbMedia(value *bool) bool { return value == nil || *value }

func (service *MiniProgramService) appendMiniProgramEvent(ctx context.Context, eventType string, item mediaport.MiniProgram, actor int64, reservation MiniProgramReservation, now time.Time) error {
	payload, err := json.Marshal(struct {
		MiniProgramID int64 `json:"miniprogram_id"`
		Actor         int64 `json:"actor"`
		Version       int64 `json:"version"`
	}{item.ID, actor, item.Version})
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(reservation.ActorScope + "\x00" + reservation.Operation + "\x00" + reservation.BusinessKey + "\x00" + hex.EncodeToString(reservation.KeyDigest[:])))
	_, err = service.events.Append(ctx, mediaport.Event{Type: eventType, Payload: payload, OccurredAt: now,
		IdempotencyKey: "media.miniprogram:" + reservation.Operation + ":" + hex.EncodeToString(digest[:])})
	return err
}

func (service *MiniProgramService) completeMiniProgramMutation(ctx context.Context, receiptID int64, reservation MiniProgramReservation, payload []byte, result mediaport.MiniProgramMutationResult, now time.Time) error {
	snapshot, err := marshalMiniProgramReceiptSnapshot(payload, result)
	if err != nil {
		return err
	}
	completed, err := service.store.CompleteMiniProgram(ctx, receiptID, snapshot, now)
	if err != nil || !miniProgramCompletionMatches(completed, receiptID, reservation, snapshot) {
		return ErrMiniProgramUnavailable
	}
	return nil
}

func (service *MiniProgramService) completeMiniProgramDelete(ctx context.Context, receiptID int64, reservation MiniProgramReservation, payload []byte, result mediaport.MiniProgramDeleteResult, now time.Time) error {
	snapshot, err := marshalMiniProgramReceiptSnapshot(payload, result)
	if err != nil {
		return err
	}
	completed, err := service.store.CompleteMiniProgram(ctx, receiptID, snapshot, now)
	if err != nil || !miniProgramCompletionMatches(completed, receiptID, reservation, snapshot) {
		return ErrMiniProgramUnavailable
	}
	return nil
}

func (service *MiniProgramService) completeMiniProgramResolution(ctx context.Context, receiptID int64, reservation MiniProgramReservation, payload []byte, result mediaport.MiniProgramThumbnailResolutionResult, now time.Time) error {
	snapshot, err := marshalMiniProgramReceiptSnapshot(payload, result)
	if err != nil {
		return err
	}
	completed, err := service.store.CompleteMiniProgram(ctx, receiptID, snapshot, now)
	if err != nil || !miniProgramCompletionMatches(completed, receiptID, reservation, snapshot) {
		return ErrMiniProgramUnavailable
	}
	return nil
}

func miniProgramReceiptMatches(receipt MiniProgramReceipt, reservation MiniProgramReservation) bool {
	return receipt.ID > 0 && receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope && receipt.BusinessKey == reservation.BusinessKey &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 && (receipt.State == "in_progress" || receipt.State == "completed")
}

func validateMiniProgramReservation(receipt MiniProgramReceipt, owned bool, reservation MiniProgramReservation) error {
	if !miniProgramReceiptMatches(receipt, reservation) {
		return ErrMiniProgramUnavailable
	}
	if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
		return ErrMiniProgramConflict
	}
	if owned && receipt.State != "in_progress" || !owned && receipt.State != "completed" {
		return ErrMiniProgramUnavailable
	}
	return nil
}

func miniProgramCompletionMatches(receipt MiniProgramReceipt, receiptID int64, reservation MiniProgramReservation, snapshot json.RawMessage) bool {
	return receipt.ID == receiptID && receipt.State == "completed" && miniProgramReceiptMatches(receipt, reservation) &&
		subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) == 1 && jsonSemanticEqual(snapshot, receipt.ResultSnapshot)
}

func decodeMiniProgramMutationReplay(receipt MiniProgramReceipt, commandID int64, payload []byte, result *mediaport.MiniProgramMutationResult) error {
	var snapshot miniProgramReceiptSnapshot[mediaport.MiniProgramMutationResult]
	if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &snapshot) != nil || !jsonSemanticEqual(snapshot.Command, payload) || !domain.ValidMiniProgram(snapshot.Result.Item, true) ||
		commandID > 0 && snapshot.Result.Item.ID != commandID || snapshot.Result.ThumbnailResolve != nil && validateThumbnailResolution(*snapshot.Result.ThumbnailResolve) != nil || !jsonSemanticEqual(mustJSON(snapshot), receipt.ResultSnapshot) {
		return ErrMiniProgramUnavailable
	}
	*result = snapshot.Result
	return nil
}

func decodeMiniProgramDeleteReplay(receipt MiniProgramReceipt, commandID int64, payload []byte, result *mediaport.MiniProgramDeleteResult) error {
	var snapshot miniProgramReceiptSnapshot[mediaport.MiniProgramDeleteResult]
	if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &snapshot) != nil || !jsonSemanticEqual(snapshot.Command, payload) || !snapshot.Result.Deleted || snapshot.Result.ID != commandID || !jsonSemanticEqual(mustJSON(snapshot), receipt.ResultSnapshot) {
		return ErrMiniProgramUnavailable
	}
	*result = snapshot.Result
	return nil
}

func decodeMiniProgramResolutionReplay(receipt MiniProgramReceipt, commandID int64, payload []byte, result *mediaport.MiniProgramThumbnailResolutionResult) error {
	var snapshot miniProgramReceiptSnapshot[mediaport.MiniProgramThumbnailResolutionResult]
	if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &snapshot) != nil || !jsonSemanticEqual(snapshot.Command, payload) || !domain.ValidMiniProgram(snapshot.Result.Item, true) || snapshot.Result.Item.ID != commandID || validateThumbnailResolution(snapshot.Result.Resolution) != nil || !jsonSemanticEqual(mustJSON(snapshot), receipt.ResultSnapshot) {
		return ErrMiniProgramUnavailable
	}
	*result = snapshot.Result
	return nil
}

func marshalMiniProgramReceiptSnapshot[T any](payload []byte, result T) (json.RawMessage, error) {
	return json.Marshal(miniProgramReceiptSnapshot[T]{Command: append(json.RawMessage{}, payload...), Result: result})
}

func validateThumbnailResolution(resolution mediaport.ThumbnailCacheResolution) error {
	if resolution.SideEffectExecuted || resolution.RealExternalCallExecuted {
		return ErrMiniProgramUnsafeResolver
	}
	if resolution.CacheOwner != mediaport.ThumbnailCacheOwner || strings.TrimSpace(resolution.CacheReceipt) == "" {
		return ErrMiniProgramUnavailable
	}
	switch resolution.Status {
	case mediaport.ThumbnailResolved:
		if strings.TrimSpace(resolution.MediaID) == "" {
			return ErrMiniProgramUnavailable
		}
	case mediaport.ThumbnailNotAvailable, mediaport.ThumbnailOutcomeUnknown:
		if resolution.MediaID != "" || resolution.ExpiresAt != nil {
			return ErrMiniProgramUnavailable
		}
	default:
		return ErrMiniProgramUnavailable
	}
	return nil
}

func normalizeMiniProgramPatch(patch mediaport.MiniProgramPatch) mediaport.MiniProgramPatch {
	patch.Name = normalizeMiniProgramText(patch.Name, 200)
	patch.AppID = normalizeMiniProgramText(patch.AppID, 120)
	patch.PagePath = normalizeMiniProgramText(patch.PagePath, 500)
	patch.Title = normalizeMiniProgramText(patch.Title, 200)
	return patch
}

func normalizeMiniProgramText(value *string, limit int) *string {
	if value == nil {
		return nil
	}
	normalized := normalizeMiniProgramString(*value, limit)
	return &normalized
}

func normalizeThumbnailResolution(resolution mediaport.ThumbnailCacheResolution) mediaport.ThumbnailCacheResolution {
	resolution.MediaID = normalizeMiniProgramString(resolution.MediaID, 255)
	return resolution
}

func normalizeMiniProgramString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.ValidString(value) && utf8.RuneCountInString(value) > limit {
		return string([]rune(value)[:limit])
	}
	return value
}

func miniProgramsSorted(items []mediaport.MiniProgram) bool {
	for index := 1; index < len(items); index++ {
		previous, current := items[index-1], items[index]
		if previous.UpdatedAt.Before(current.UpdatedAt) || previous.UpdatedAt.Equal(current.UpdatedAt) && previous.ID <= current.ID {
			return false
		}
	}
	return true
}

func classifyMiniProgram(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidMiniProgramOperation), errors.Is(err, ErrMiniProgramNotFound), errors.Is(err, ErrMiniProgramConflict), errors.Is(err, ErrMiniProgramImageNotFound), errors.Is(err, ErrMiniProgramUnsafeResolver), errors.Is(err, ErrMiniProgramHasReferences):
		return err
	default:
		return ErrMiniProgramUnavailable
	}
}
