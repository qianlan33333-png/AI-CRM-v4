package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrCatalogUnavailable    = errors.New("channel catalog unavailable")
	ErrCatalogNotFound       = errors.New("channel not found")
	ErrCatalogConflict       = errors.New("channel catalog conflict")
	ErrCatalogReferenced     = errors.New("channel is referenced")
	ErrInvalidCatalogCommand = errors.New("invalid channel catalog command")
)

type CatalogMutation struct {
	ActorID        int64
	IdempotencyKey string
	Create         channeldomain.CreateChannel
	Update         channeldomain.UpdateChannel
}

type CatalogService struct {
	uow       platformport.UnitOfWork
	store     channelport.CatalogStore
	receipts  channelport.OperationReceiptStore
	events    channelport.CatalogEventAppender
	materials channelport.MaterialReferenceValidator
	tags      channelport.TagReferenceReader
	staff     channelport.StaffReferenceReader
	now       func() time.Time
}

func NewCatalogService(uow platformport.UnitOfWork, store channelport.CatalogStore, receipts channelport.OperationReceiptStore, events channelport.CatalogEventAppender, materials channelport.MaterialReferenceValidator, tags channelport.TagReferenceReader, staff channelport.StaffReferenceReader) *CatalogService {
	return &CatalogService{uow: uow, store: store, receipts: receipts, events: events, materials: materials, tags: tags, staff: staff, now: time.Now}
}

func (service *CatalogService) Get(ctx context.Context, id int64) (channeldomain.Channel, error) {
	if !service.readReady() || ctx == nil || id < 1 {
		return channeldomain.Channel{}, ErrCatalogNotFound
	}
	var result channeldomain.Channel
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = service.store.Get(tx, id)
		return err
	}); err != nil {
		return channeldomain.Channel{}, mapCatalogError(err)
	}
	return result, nil
}

func (service *CatalogService) List(ctx context.Context, filter channelport.CatalogFilter) (channelport.CatalogPage, error) {
	if !service.readReady() || ctx == nil || filter.Limit < 0 || filter.Limit > 100 || filter.AfterID < 0 {
		return channelport.CatalogPage{}, ErrInvalidCatalogCommand
	}
	if filter.Status != "" && filter.Status != channeldomain.StatusActive && filter.Status != channeldomain.StatusInactive && filter.Status != channeldomain.StatusArchived {
		return channelport.CatalogPage{}, ErrInvalidCatalogCommand
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	var items []channeldomain.Channel
	var total int64
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, total, err = service.store.List(tx, filter)
		return err
	}); err != nil {
		return channelport.CatalogPage{}, mapCatalogError(err)
	}
	page := channelport.CatalogPage{Items: items, Total: total}
	if len(items) == filter.Limit {
		page.NextCursor = strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	return page, nil
}

func (service *CatalogService) Create(ctx context.Context, command CatalogMutation) (channeldomain.Channel, error) {
	now := service.clock()
	candidate, err := channeldomain.NewChannel(command.Create, now)
	if err != nil {
		return channeldomain.Channel{}, errors.Join(ErrInvalidCatalogCommand, err)
	}
	return service.mutate(ctx, "create", command.ActorID, command.IdempotencyKey, command.Create, func(tx context.Context) (channeldomain.Channel, error) {
		if err := service.validateReferences(tx, candidate.Config); err != nil {
			return channeldomain.Channel{}, err
		}
		return service.store.Create(tx, candidate, command.ActorID)
	})
}

func (service *CatalogService) Update(ctx context.Context, id int64, command CatalogMutation) (channeldomain.Channel, error) {
	if id < 1 {
		return channeldomain.Channel{}, ErrCatalogNotFound
	}
	return service.mutate(ctx, "update", command.ActorID, command.IdempotencyKey, struct {
		ID     int64                       `json:"id"`
		Update channeldomain.UpdateChannel `json:"update"`
	}{id, command.Update}, func(tx context.Context) (channeldomain.Channel, error) {
		current, err := service.store.Get(tx, id)
		if err != nil {
			return channeldomain.Channel{}, err
		}
		updated, err := current.Update(command.Update, service.clock())
		if err != nil {
			return channeldomain.Channel{}, err
		}
		if err = service.validateReferences(tx, updated.Config); err != nil {
			return channeldomain.Channel{}, err
		}
		return service.store.Update(tx, updated, command.ActorID)
	})
}

func (service *CatalogService) mutate(ctx context.Context, operation string, actorID int64, key string, payload any, apply func(context.Context) (channeldomain.Channel, error)) (channeldomain.Channel, error) {
	if !service.writeReady() || ctx == nil || actorID < 1 || !validOperationKey(key) {
		return channeldomain.Channel{}, ErrInvalidCatalogCommand
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return channeldomain.Channel{}, ErrInvalidCatalogCommand
	}
	payloadDigest := sha256.Sum256(payloadJSON)
	keyDigest := sha256.Sum256([]byte(key))
	var result channeldomain.Channel
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, created, reserveErr := service.receipts.Reserve(tx, channelport.OperationReceipt{
			Operation: operation, ActorID: actorID, KeyDigest: keyDigest, PayloadDigest: payloadDigest,
		})
		if reserveErr != nil {
			return reserveErr
		}
		if !created {
			if receipt.PayloadDigest != payloadDigest || receipt.State != channelport.ReceiptCompleted || receipt.ChannelID < 1 {
				return ErrCatalogConflict
			}
			result, reserveErr = service.store.Get(tx, receipt.ChannelID)
			return reserveErr
		}
		result, reserveErr = apply(tx)
		if reserveErr != nil {
			return reserveErr
		}
		eventPayload, marshalErr := json.Marshal(struct {
			ChannelID int64  `json:"channel_id"`
			Version   int64  `json:"version"`
			ActorID   int64  `json:"actor_id"`
			Operation string `json:"operation"`
		}{result.ID, result.Version, actorID, operation})
		if marshalErr != nil {
			return marshalErr
		}
		if reserveErr = service.events.Append(tx, channelport.CatalogEvent{
			Type: "channel." + operation, ChannelID: result.ID, Version: result.Version,
			ActorID: actorID, OccurredAt: service.clock(), IdempotencyKey: "channel:" + operation + ":" + hex.EncodeToString(keyDigest[:]), Payload: eventPayload,
		}); reserveErr != nil {
			return reserveErr
		}
		_, reserveErr = service.receipts.Complete(tx, receipt.ID, result.ID, result.Version, service.clock())
		return reserveErr
	})
	if err != nil {
		return channeldomain.Channel{}, mapCatalogError(err)
	}
	return result, nil
}

func (service *CatalogService) validateReferences(ctx context.Context, config channeldomain.Config) error {
	refs := channelport.MaterialReferences{ImageIDs: config.Media.Images, MiniProgramIDs: config.Media.MiniPrograms, AttachmentIDs: config.Media.Attachments, GroupInviteIDs: config.Media.GroupInvites}
	if len(refs.ImageIDs)+len(refs.MiniProgramIDs)+len(refs.AttachmentIDs)+len(refs.GroupInviteIDs) > 0 {
		if service.materials == nil {
			return ErrCatalogUnavailable
		}
		if err := service.materials.ValidateChannelMaterials(ctx, refs); err != nil {
			return err
		}
	}
	if config.EntryTagID > 0 {
		if service.tags == nil {
			return ErrCatalogUnavailable
		}
		tag, err := service.tags.ReadChannelTag(ctx, config.EntryTagID)
		if err != nil || !tag.Active || tag.Name != config.EntryTagName || tag.GroupName != config.EntryTagGroupName {
			return errors.Join(ErrInvalidCatalogCommand, err)
		}
	}
	ids := make([]int64, len(config.Assignment.Assignees))
	for index, assignee := range config.Assignment.Assignees {
		ids[index] = assignee.StaffID
	}
	if service.staff == nil {
		return ErrCatalogUnavailable
	}
	staff, err := service.staff.ReadChannelStaff(ctx, ids)
	if err != nil || len(staff) != len(ids) {
		return errors.Join(ErrInvalidCatalogCommand, err)
	}
	active := make(map[int64]bool, len(staff))
	for _, item := range staff {
		active[item.ID] = item.Active
	}
	for _, id := range ids {
		if !active[id] {
			return ErrInvalidCatalogCommand
		}
	}
	return nil
}

func (service *CatalogService) readReady() bool {
	return service != nil && service.uow != nil && service.store != nil
}

func (service *CatalogService) writeReady() bool {
	return service.readReady() && service.receipts != nil && service.events != nil
}

func (service *CatalogService) clock() time.Time {
	if service.now == nil {
		return time.Now().UTC()
	}
	return service.now().UTC()
}

func validOperationKey(value string) bool {
	return len(value) >= 8 && value == strings.TrimSpace(value) && len(value) <= 200 && !strings.ContainsAny(value, "\r\n\t ")
}

func mapCatalogError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrCatalogNotFound), errors.Is(err, ErrCatalogConflict), errors.Is(err, ErrCatalogReferenced), errors.Is(err, ErrInvalidCatalogCommand):
		return err
	case errors.Is(err, channeldomain.ErrVersionConflict), errors.Is(err, channeldomain.ErrImmutableCode), errors.Is(err, channeldomain.ErrInvalidTransition):
		return errors.Join(ErrCatalogConflict, err)
	case errors.Is(err, channeldomain.ErrInvalidChannel), errors.Is(err, channeldomain.ErrInvalidAssignment):
		return errors.Join(ErrInvalidCatalogCommand, err)
	default:
		return errors.Join(ErrCatalogUnavailable, err)
	}
}
