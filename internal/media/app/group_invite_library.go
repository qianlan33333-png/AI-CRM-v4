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
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const (
	DefaultGroupInviteLimit  int32 = 100
	MaximumGroupInviteLimit  int32 = 100
	MaximumGroupInviteOffset int32 = 1_000_000
)

var (
	ErrInvalidGroupInviteOperation = errors.New("invalid group invite operation")
	ErrGroupInviteNotFound         = errors.New("group invite not found")
	ErrGroupInviteInvalidReference = errors.New("group invite image not found")
	ErrGroupInviteConflict         = errors.New("group invite operation conflict")
	ErrGroupInviteHasReferences    = errors.New("group invite has channel references")
	ErrGroupInviteUnavailable      = errors.New("group invite service unavailable")
)

type GroupInviteReceipt struct {
	ID                                        int64
	Operation, ActorScope, BusinessKey, State string
	KeyDigest, PayloadDigest                  [32]byte
	ResultSnapshot                            json.RawMessage
}

type GroupInviteReservation struct {
	Operation, ActorScope, BusinessKey string
	KeyDigest, PayloadDigest           [32]byte
	CreatedAt                          time.Time
}

type GroupInviteStore interface {
	ListGroupInvites(context.Context, mediaport.GroupInviteListQuery) ([]mediaport.GroupInvite, error)
	CountGroupInvites(context.Context, mediaport.GroupInviteListQuery) (int64, error)
	GetGroupInvite(context.Context, int64) (mediaport.GroupInvite, error)
	LockGroupInvite(context.Context, int64) (mediaport.GroupInvite, error)
	CreateGroupInvite(context.Context, mediaport.GroupInvite) (mediaport.GroupInvite, error)
	UpdateGroupInvite(context.Context, mediaport.GroupInvite) (mediaport.GroupInvite, error)
	ArchiveGroupInvite(context.Context, mediaport.GroupInvite) (mediaport.GroupInvite, error)
	ReserveGroupInvite(context.Context, GroupInviteReservation) (GroupInviteReceipt, bool, error)
	CompleteGroupInvite(context.Context, int64, json.RawMessage, time.Time) (GroupInviteReceipt, error)
}

type GroupInviteService struct {
	uow     platformport.UnitOfWork
	store   GroupInviteStore
	images  mediaport.ImageMetadataReader
	events  mediaport.EventAppender
	contact mediaport.ChannelGroupInviteDeletionReferenceReader
	now     func() time.Time
}

func NewGroupInviteService(uow platformport.UnitOfWork, store GroupInviteStore, images mediaport.ImageMetadataReader, events mediaport.EventAppender) *GroupInviteService {
	return &GroupInviteService{uow: uow, store: store, images: images, events: events, now: time.Now}
}

func NewGroupInviteServiceWithChannelReferences(uow platformport.UnitOfWork, store GroupInviteStore, images mediaport.ImageMetadataReader, events mediaport.EventAppender, contact mediaport.ChannelGroupInviteDeletionReferenceReader) *GroupInviteService {
	return &GroupInviteService{uow: uow, store: store, images: images, events: events, contact: contact, now: time.Now}
}

func (service *GroupInviteService) List(ctx context.Context, query mediaport.GroupInviteListQuery) (mediaport.GroupInvitePage, error) {
	if !groupInviteReady(service) {
		return mediaport.GroupInvitePage{}, ErrGroupInviteUnavailable
	}
	if query.Limit == 0 {
		query.Limit = DefaultGroupInviteLimit
	}
	query.Search = strings.TrimSpace(query.Search)
	if query.Limit < 1 || query.Limit > MaximumGroupInviteLimit || query.Offset < 0 || query.Offset > MaximumGroupInviteOffset || len(query.Search) > domain.MaxGroupInviteTitleBytes {
		return mediaport.GroupInvitePage{}, ErrInvalidGroupInviteOperation
	}
	page := mediaport.GroupInvitePage{Limit: query.Limit, Offset: query.Offset}
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		page.Items, err = service.store.ListGroupInvites(tx, query)
		if err == nil {
			page.Total, err = service.store.CountGroupInvites(tx, query)
		}
		return err
	})
	if err != nil || page.Total < 0 || len(page.Items) > int(query.Limit) {
		return mediaport.GroupInvitePage{}, classifyGroupInvite(err)
	}
	for _, item := range page.Items {
		if !domain.ValidGroupInvite(item, true) || item.ArchivedAt != nil {
			return mediaport.GroupInvitePage{}, ErrGroupInviteUnavailable
		}
	}
	return page, nil
}

func (service *GroupInviteService) Get(ctx context.Context, id int64) (mediaport.GroupInvite, error) {
	if !groupInviteReady(service) || id < 1 {
		return mediaport.GroupInvite{}, ErrInvalidGroupInviteOperation
	}
	var item mediaport.GroupInvite
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		item, err = service.store.GetGroupInvite(tx, id)
		return err
	})
	if err != nil {
		return mediaport.GroupInvite{}, classifyGroupInvite(err)
	}
	if !domain.ValidGroupInvite(item, true) || item.ArchivedAt != nil {
		return mediaport.GroupInvite{}, ErrGroupInviteUnavailable
	}
	return item, nil
}

func (service *GroupInviteService) Create(ctx context.Context, command mediaport.GroupInviteCreateCommand) (mediaport.GroupInvite, error) {
	now, err := service.commandTime(command.Actor, command.IdempotencyKey)
	if err != nil {
		return mediaport.GroupInvite{}, err
	}
	item, err := domain.NewGroupInvite(command, now)
	if err != nil {
		return mediaport.GroupInvite{}, ErrInvalidGroupInviteOperation
	}
	return service.mutate(ctx, "create", "create", command.Actor, command.IdempotencyKey, item, mediaport.GroupInvitePatch{})
}

func (service *GroupInviteService) Update(ctx context.Context, command mediaport.GroupInviteUpdateCommand) (mediaport.GroupInvite, error) {
	now, err := service.commandTime(command.Actor, command.IdempotencyKey)
	if err != nil || command.ID < 1 || domain.EmptyGroupInvitePatch(command.GroupInvitePatch) {
		return mediaport.GroupInvite{}, ErrInvalidGroupInviteOperation
	}
	return service.mutateAt(ctx, "update", strconv.FormatInt(command.ID, 10), command.Actor, command.IdempotencyKey,
		mediaport.GroupInvite{ID: command.ID, UpdatedAt: now}, command.GroupInvitePatch, now)
}

func (service *GroupInviteService) Archive(ctx context.Context, command mediaport.GroupInviteArchiveCommand) (mediaport.GroupInvite, error) {
	now, err := service.commandTime(command.Actor, command.IdempotencyKey)
	if err != nil || command.ID < 1 {
		return mediaport.GroupInvite{}, ErrInvalidGroupInviteOperation
	}
	return service.mutateAt(ctx, "archive", strconv.FormatInt(command.ID, 10), command.Actor, command.IdempotencyKey,
		mediaport.GroupInvite{ID: command.ID, UpdatedAt: now}, mediaport.GroupInvitePatch{}, now)
}

func (service *GroupInviteService) mutate(ctx context.Context, operation, businessKey string, actor int64, key string, item mediaport.GroupInvite, patch mediaport.GroupInvitePatch) (mediaport.GroupInvite, error) {
	return service.mutateAt(ctx, operation, businessKey, actor, key, item, patch, item.UpdatedAt)
}

func (service *GroupInviteService) mutateAt(ctx context.Context, operation, businessKey string, actor int64, key string, seed mediaport.GroupInvite, patch mediaport.GroupInvitePatch, now time.Time) (mediaport.GroupInvite, error) {
	digestSeed := seed
	digestSeed.CreatedBy, digestSeed.UpdatedBy, digestSeed.Version = 0, 0, 0
	digestSeed.CreatedAt, digestSeed.UpdatedAt = time.Time{}, time.Time{}
	payload, err := json.Marshal(struct {
		Operation, BusinessKey string
		Seed                   mediaport.GroupInvite
		Patch                  mediaport.GroupInvitePatch
	}{operation, businessKey, digestSeed, patch})
	if err != nil {
		return mediaport.GroupInvite{}, ErrGroupInviteUnavailable
	}
	reservation := GroupInviteReservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), BusinessKey: businessKey,
		KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now}
	var result mediaport.GroupInvite
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, innerErr := service.store.ReserveGroupInvite(tx, reservation)
		if innerErr != nil {
			return innerErr
		}
		if !groupInviteReceiptMatches(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrGroupInviteConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil || !domain.ValidGroupInvite(result, true) || !jsonSemanticEqual(receipt.ResultSnapshot, mustJSON(result)) {
				return ErrGroupInviteUnavailable
			}
			return nil
		}
		switch operation {
		case "create":
			result = seed
		case "update", "archive":
			result, innerErr = service.store.LockGroupInvite(tx, seed.ID)
			if innerErr != nil {
				return innerErr
			}
			if operation == "update" {
				result, innerErr = domain.ApplyGroupInvitePatch(result, patch, actor, now)
			} else {
				result, innerErr = domain.ArchiveGroupInvite(result, actor, now)
			}
		default:
			return ErrInvalidGroupInviteOperation
		}
		if innerErr != nil {
			return ErrInvalidGroupInviteOperation
		}
		if operation == "archive" {
			if err := service.requireNoChannelReferences(tx, seed.ID); err != nil {
				return err
			}
		}
		if operation == "update" && !result.Enabled {
			if err := service.requireNoChannelReferences(tx, seed.ID); err != nil {
				return err
			}
		}
		if result.CoverImageID > 0 {
			exists, lookupErr := service.images.ImageExists(tx, result.CoverImageID)
			if lookupErr != nil {
				return lookupErr
			}
			if !exists {
				return ErrGroupInviteInvalidReference
			}
		}
		switch operation {
		case "create":
			result, innerErr = service.store.CreateGroupInvite(tx, result)
		case "update":
			result, innerErr = service.store.UpdateGroupInvite(tx, result)
		case "archive":
			result, innerErr = service.store.ArchiveGroupInvite(tx, result)
		}
		if innerErr != nil {
			return innerErr
		}
		if !domain.ValidGroupInvite(result, true) {
			return ErrGroupInviteUnavailable
		}
		eventPayload, innerErr := json.Marshal(map[string]any{"group_invite_id": result.ID, "actor": actor, "version": result.Version})
		if innerErr != nil {
			return innerErr
		}
		eventDigest := sha256.Sum256([]byte(reservation.ActorScope + "\x00" + operation + "\x00" + businessKey + "\x00" + key))
		if _, innerErr = service.events.Append(tx, mediaport.Event{Type: groupInviteEventType(operation), Payload: eventPayload, OccurredAt: now,
			IdempotencyKey: "media.group_invite_" + operation + ":" + hex.EncodeToString(eventDigest[:])}); innerErr != nil {
			return innerErr
		}
		snapshot, innerErr := json.Marshal(result)
		if innerErr != nil {
			return innerErr
		}
		completed, innerErr := service.store.CompleteGroupInvite(tx, receipt.ID, snapshot, now)
		if innerErr != nil || completed.State != "completed" || !jsonSemanticEqual(completed.ResultSnapshot, snapshot) {
			return ErrGroupInviteUnavailable
		}
		return nil
	})
	if err != nil {
		return mediaport.GroupInvite{}, classifyGroupInvite(err)
	}
	return result, nil
}

func (service *GroupInviteService) requireNoChannelReferences(ctx context.Context, id int64) error {
	if service == nil || service.contact == nil {
		return ErrGroupInviteUnavailable
	}
	references, err := service.contact.ListGroupInviteReferenceChannelIDs(ctx, id)
	if err != nil {
		return ErrGroupInviteUnavailable
	}
	if len(references) != 0 {
		return ErrGroupInviteHasReferences
	}
	return nil
}

func (service *GroupInviteService) commandTime(actor int64, key string) (time.Time, error) {
	if !groupInviteReady(service) || actor < 1 || len(key) < 16 || len(key) > 128 || strings.TrimSpace(key) != key {
		return time.Time{}, ErrInvalidGroupInviteOperation
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return time.Time{}, ErrGroupInviteUnavailable
	}
	return now, nil
}

func groupInviteReady(service *GroupInviteService) bool {
	return service != nil && service.uow != nil && service.store != nil && service.images != nil && service.events != nil && service.now != nil
}

func groupInviteReceiptMatches(receipt GroupInviteReceipt, reservation GroupInviteReservation) bool {
	return receipt.ID > 0 && receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope && receipt.BusinessKey == reservation.BusinessKey &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 && (receipt.State == "in_progress" || receipt.State == "completed")
}

func groupInviteEventType(operation string) string {
	switch operation {
	case "create":
		return mediaport.EventGroupInviteCreated
	case "update":
		return mediaport.EventGroupInviteUpdated
	case "archive":
		return mediaport.EventGroupInviteArchived
	default:
		return ""
	}
}

func classifyGroupInvite(err error) error {
	switch {
	case errors.Is(err, ErrInvalidGroupInviteOperation), errors.Is(err, ErrGroupInviteNotFound), errors.Is(err, ErrGroupInviteInvalidReference), errors.Is(err, ErrGroupInviteConflict), errors.Is(err, ErrGroupInviteHasReferences):
		return err
	default:
		return ErrGroupInviteUnavailable
	}
}

func mustJSON(value any) []byte { encoded, _ := json.Marshal(value); return encoded }

func jsonSemanticEqual(left, right []byte) bool {
	var a, b any
	if decodeJSON(left, &a) != nil || decodeJSON(right, &b) != nil {
		return false
	}
	return semanticJSONValueEqual(a, b)
}

func decodeJSON(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrGroupInviteUnavailable
	}
	return nil
}

func semanticJSONValueEqual(left, right any) bool {
	ln, lok := left.(json.Number)
	rn, rok := right.(json.Number)
	if lok || rok {
		if !lok || !rok {
			return false
		}
		lr, lgood := new(big.Rat).SetString(string(ln))
		rr, rgood := new(big.Rat).SetString(string(rn))
		return lgood && rgood && lr.Cmp(rr) == 0
	}
	lm, lok := left.(map[string]any)
	rm, rok := right.(map[string]any)
	if lok || rok {
		if !lok || !rok || len(lm) != len(rm) {
			return false
		}
		for key, value := range lm {
			other, ok := rm[key]
			if !ok || !semanticJSONValueEqual(value, other) {
				return false
			}
		}
		return true
	}
	ls, lok := left.([]any)
	rs, rok := right.([]any)
	if lok || rok {
		if !lok || !rok || len(ls) != len(rs) {
			return false
		}
		for i := range ls {
			if !semanticJSONValueEqual(ls[i], rs[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}
