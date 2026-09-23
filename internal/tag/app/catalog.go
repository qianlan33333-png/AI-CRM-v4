// Package app implements the tag catalog use cases.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"reflect"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

var (
	ErrInvalidCommand = errors.New("invalid tag catalog command")
	ErrNotFound       = tagport.ErrNotFound
	ErrReferenced     = errors.New("tag catalog item is still referenced by customers")
	ErrConflict       = tagport.ErrConflict
	ErrUnavailable    = errors.New("tag catalog unavailable")
	// ErrProviderMutationUnavailable distinguishes the deliberately closed
	// official write gate from a generic catalog read outage. Callers must not
	// fall back to a local-only create, rename, or archive.
	ErrProviderMutationUnavailable = errors.New("tag catalog official write is unavailable")
	// ErrArchiveOperationReadUnsupported only means an older read-only test
	// adapter cannot expose the optional recovery surface. A real store error
	// remains unavailable and is never silently converted to an empty list.
	ErrArchiveOperationReadUnsupported = errors.New("tag archive operation reader unsupported")
)

// Service owns catalog and group metadata only. It intentionally exposes no
// customer target, mark or unmark method. A later outbound adapter may use a
// separate effect contract for provider writes.
type Service struct {
	retrier                   effectport.TagCatalogMutationRetrier
	uow                       platformport.UnitOfWork
	store                     tagport.CatalogStore
	receipts                  tagport.MutationReceiptStore
	refs                      tagport.ReferenceGuard
	events                    tagport.EventAppender
	now                       func() time.Time
	mutationStore             tagport.CatalogMutationStore
	mutations                 tagport.CatalogMutationEnqueuer
	providerMutationsRequired bool
}

// NewService creates a catalog application service. Reads require only uow and
// store; mutations additionally require receipt and event ports. refs is
// required only by archive operations and is kept as a read-only port.
func NewService(uow platformport.UnitOfWork, store tagport.CatalogStore, receipts tagport.MutationReceiptStore, events tagport.EventAppender, refs tagport.ReferenceGuard) *Service {
	return &Service{uow: uow, store: store, receipts: receipts, events: events, refs: refs, now: time.Now}
}

// NewCatalogService is the descriptive constructor used by callers that do
// not need the shorter NewService name.
func NewCatalogService(uow platformport.UnitOfWork, store tagport.CatalogStore, receipts tagport.MutationReceiptStore, events tagport.EventAppender, refs tagport.ReferenceGuard) *Service {
	return NewService(uow, store, receipts, events, refs)
}

// BindProviderMutations installs the Tag-to-Outbound intent boundary. It is
// called by composition after both owners share the PostgreSQL UoW.
func (service *Service) BindProviderMutations(enqueuer tagport.CatalogMutationEnqueuer) error {
	if service == nil || enqueuer == nil || service.mutations != nil {
		return ErrUnavailable
	}
	store, ok := service.store.(tagport.CatalogMutationStore)
	if !ok || store == nil {
		return ErrUnavailable
	}
	service.mutationStore, service.mutations, service.providerMutationsRequired = store, enqueuer, true
	return nil
}

// RequireProviderMutations makes the official catalog-write boundary
// fail-closed at the Tag owner. Composition uses it even while the provider
// gate is disabled, so create/rename/archive cannot quietly become local-only
// mutations. Reorder operations remain local because they do not write WeCom.
func (service *Service) RequireProviderMutations() error {
	if service == nil {
		return ErrUnavailable
	}
	service.providerMutationsRequired = true
	return nil
}

func (service *Service) List(ctx context.Context) (domain.Catalog, error) {
	if !service.readReady() || ctx == nil {
		return domain.Catalog{}, ErrUnavailable
	}
	var result domain.Catalog
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Groups, err = service.store.ListGroups(tx)
		if err != nil {
			return err
		}
		result.Tags, err = service.store.ListTags(tx)
		return err
	})
	if err != nil {
		return domain.Catalog{}, errors.Join(ErrUnavailable, err)
	}
	if err = domain.ValidateCatalog(result); err != nil {
		return domain.Catalog{}, errors.Join(ErrUnavailable, err)
	}
	// A local catalog read is not a provider observation. HTTP obtains the
	// actual readback timestamp from the durable sync receipt instead.
	result.SyncedAt = time.Time{}
	result.Groups = append([]domain.Group{}, result.Groups...)
	result.Tags = append([]domain.Tag{}, result.Tags...)
	return result, nil
}

func (service *Service) GetGroup(ctx context.Context, id int64) (domain.Group, error) {
	if id < 1 {
		return domain.Group{}, ErrNotFound
	}
	catalog, err := service.List(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	for _, group := range catalog.Groups {
		if group.ID == id {
			return group, nil
		}
	}
	return domain.Group{}, ErrNotFound
}

func (service *Service) GetTag(ctx context.Context, id int64) (domain.Tag, error) {
	if id < 1 {
		return domain.Tag{}, ErrNotFound
	}
	catalog, err := service.List(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	for _, tag := range catalog.Tags {
		if tag.ID == id {
			return tag, nil
		}
	}
	return domain.Tag{}, ErrNotFound
}

// ArchiveOperations returns the durable provider outcomes that remain relevant
// after an archive command removes the item from the active catalog. It is a
// read-only recovery surface; callers cannot retry or reconstruct a provider
// write through it.
func (service *Service) ArchiveOperations(ctx context.Context) ([]tagport.ArchiveMutationOperation, error) {
	if !service.readReady() || ctx == nil {
		return nil, ErrUnavailable
	}
	reader, ok := service.store.(tagport.ArchiveMutationOperationReader)
	if !ok || nilDependency(reader) {
		return nil, ErrArchiveOperationReadUnsupported
	}
	var operations []tagport.ArchiveMutationOperation
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		operations, err = reader.ListArchiveMutationOperations(tx, 100)
		return err
	})
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	return append([]tagport.ArchiveMutationOperation(nil), operations...), nil
}

func (service *Service) CreateGroup(ctx context.Context, command domain.Command) (domain.Group, domain.Tag, error) {
	if !domain.ValidCommand(command, command.GroupName, command.FirstTagName) {
		return domain.Group{}, domain.Tag{}, ErrInvalidCommand
	}
	result, err := service.mutate(ctx, "group_create", command, func(tx context.Context) (mutationResult, error) {
		group, err := service.store.CreateGroup(tx, strings.TrimSpace(command.GroupName))
		if err != nil {
			return mutationResult{}, err
		}
		tag, err := service.store.CreateTag(tx, group.ID, strings.TrimSpace(command.FirstTagName))
		if err != nil {
			return mutationResult{}, err
		}
		return mutationResult{value: groupCreateResult{group: group, tag: tag}, resultIDs: []int64{group.ID, tag.ID}}, nil
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		if len(ids) != 2 {
			return mutationResult{}, ErrConflict
		}
		group, err := service.store.GetGroup(tx, ids[0])
		if err != nil {
			return mutationResult{}, err
		}
		tag, err := service.store.GetTag(tx, ids[1])
		if err != nil || tag.GroupID != group.ID {
			return mutationResult{}, errors.Join(ErrConflict, err)
		}
		return mutationResult{value: groupCreateResult{group: group, tag: tag}, resultIDs: ids}, nil
	}, true)
	if err != nil {
		return domain.Group{}, domain.Tag{}, err
	}
	pair, ok := result.value.(groupCreateResult)
	if !ok {
		return domain.Group{}, domain.Tag{}, ErrUnavailable
	}
	return pair.group, pair.tag, nil
}

func (service *Service) UpdateGroup(ctx context.Context, command domain.Command) (domain.Group, error) {
	if command.GroupID < 1 {
		return domain.Group{}, ErrNotFound
	}
	if !domain.ValidCommand(command, command.GroupName) {
		return domain.Group{}, ErrInvalidCommand
	}
	result, err := service.mutate(ctx, "group_update", command, func(tx context.Context) (mutationResult, error) {
		group, err := service.store.UpdateGroup(tx, command.GroupID, strings.TrimSpace(command.GroupName))
		return mutationResult{value: group, resultIDs: []int64{group.ID}}, err
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		if len(ids) != 1 || ids[0] != command.GroupID {
			return mutationResult{}, ErrConflict
		}
		group, err := service.store.GetGroup(tx, command.GroupID)
		return mutationResult{value: group, resultIDs: ids}, err
	}, true)
	if err != nil {
		return domain.Group{}, err
	}
	group, ok := result.value.(domain.Group)
	if !ok {
		return domain.Group{}, ErrUnavailable
	}
	return group, nil
}

func (service *Service) ArchiveGroup(ctx context.Context, command domain.Command) (domain.Group, error) {
	if command.GroupID < 1 {
		return domain.Group{}, ErrNotFound
	}
	if !domain.ValidCommand(command) {
		return domain.Group{}, ErrInvalidCommand
	}
	if service.refs == nil || nilDependency(service.refs) {
		return domain.Group{}, ErrUnavailable
	}
	result, err := service.mutate(ctx, "group_archive", command, func(tx context.Context) (mutationResult, error) {
		references, err := service.refs.GroupReferences(tx, command.GroupID)
		if err != nil {
			return mutationResult{}, err
		}
		if references > 0 {
			return mutationResult{}, ErrReferenced
		}
		// Archiving a group archives its active tag rows as well. Check each
		// opaque reference count first; a failed guard closes the operation.
		tags, err := service.store.ListTags(tx)
		if err != nil {
			return mutationResult{}, err
		}
		for _, tag := range tags {
			if tag.GroupID != command.GroupID {
				continue
			}
			count, refErr := service.refs.TagReferences(tx, tag.ID)
			if refErr != nil {
				return mutationResult{}, refErr
			}
			if count > 0 {
				return mutationResult{}, ErrReferenced
			}
		}
		group, err := service.store.ArchiveGroup(tx, command.GroupID)
		return mutationResult{value: group, resultIDs: []int64{group.ID}}, err
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		if len(ids) != 1 || ids[0] != command.GroupID {
			return mutationResult{}, ErrConflict
		}
		group, err := archivedGroupForReplay(service.store, tx, command.GroupID)
		return mutationResult{value: group, resultIDs: ids}, err
	}, true)
	if err != nil {
		return domain.Group{}, err
	}
	group, ok := result.value.(domain.Group)
	if !ok {
		return domain.Group{}, ErrUnavailable
	}
	return group, nil
}

func (service *Service) CreateTag(ctx context.Context, command domain.Command) (domain.Tag, error) {
	if command.GroupID < 1 {
		return domain.Tag{}, ErrNotFound
	}
	if !domain.ValidCommand(command, command.GroupName, command.TagName) {
		return domain.Tag{}, ErrInvalidCommand
	}
	result, err := service.mutate(ctx, "tag_create", command, func(tx context.Context) (mutationResult, error) {
		tag, err := service.store.CreateTag(tx, command.GroupID, strings.TrimSpace(command.TagName))
		return mutationResult{value: tag, resultIDs: []int64{tag.ID}}, err
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		if len(ids) != 1 {
			return mutationResult{}, ErrConflict
		}
		tag, err := service.store.GetTag(tx, ids[0])
		return mutationResult{value: tag, resultIDs: ids}, err
	}, true)
	if err != nil {
		return domain.Tag{}, err
	}
	tag, ok := result.value.(domain.Tag)
	if !ok {
		return domain.Tag{}, ErrUnavailable
	}
	return tag, nil
}

func (service *Service) UpdateTag(ctx context.Context, command domain.Command) (domain.Tag, error) {
	if command.TagID < 1 {
		return domain.Tag{}, ErrNotFound
	}
	if !domain.ValidCommand(command, command.TagName) {
		return domain.Tag{}, ErrInvalidCommand
	}
	result, err := service.mutate(ctx, "tag_update", command, func(tx context.Context) (mutationResult, error) {
		tag, err := service.store.UpdateTag(tx, command.TagID, strings.TrimSpace(command.TagName))
		return mutationResult{value: tag, resultIDs: []int64{tag.ID}}, err
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		if len(ids) != 1 || ids[0] != command.TagID {
			return mutationResult{}, ErrConflict
		}
		tag, err := service.store.GetTag(tx, command.TagID)
		return mutationResult{value: tag, resultIDs: ids}, err
	}, true)
	if err != nil {
		return domain.Tag{}, err
	}
	tag, ok := result.value.(domain.Tag)
	if !ok {
		return domain.Tag{}, ErrUnavailable
	}
	return tag, nil
}

func (service *Service) ArchiveTag(ctx context.Context, command domain.Command) (domain.Tag, error) {
	if command.TagID < 1 {
		return domain.Tag{}, ErrNotFound
	}
	if !domain.ValidCommand(command) {
		return domain.Tag{}, ErrInvalidCommand
	}
	if service.refs == nil || nilDependency(service.refs) {
		return domain.Tag{}, ErrUnavailable
	}
	result, err := service.mutate(ctx, "tag_archive", command, func(tx context.Context) (mutationResult, error) {
		references, err := service.refs.TagReferences(tx, command.TagID)
		if err != nil {
			return mutationResult{}, err
		}
		if references > 0 {
			return mutationResult{}, ErrReferenced
		}
		tag, err := service.store.ArchiveTag(tx, command.TagID)
		return mutationResult{value: tag, resultIDs: []int64{tag.ID}}, err
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		if len(ids) != 1 || ids[0] != command.TagID {
			return mutationResult{}, ErrConflict
		}
		tag, err := archivedTagForReplay(service.store, tx, command.TagID)
		return mutationResult{value: tag, resultIDs: ids}, err
	}, true)
	if err != nil {
		return domain.Tag{}, err
	}
	tag, ok := result.value.(domain.Tag)
	if !ok {
		return domain.Tag{}, ErrUnavailable
	}
	return tag, nil
}

func (service *Service) ReorderGroups(ctx context.Context, command domain.Command) ([]domain.Group, error) {
	if !domain.ValidCommand(command) || !domain.ValidIDs(command.IDs) {
		return nil, ErrInvalidCommand
	}
	result, err := service.mutate(ctx, "group_reorder", command, func(tx context.Context) (mutationResult, error) {
		current, err := service.store.ListGroups(tx)
		if err != nil || !domain.SameIDSet(domain.GroupIDs(current), command.IDs) {
			return mutationResult{}, errors.Join(ErrConflict, err)
		}
		groups, err := service.store.ReorderGroups(tx, append([]int64(nil), command.IDs...))
		return mutationResult{value: groups, resultIDs: domain.GroupIDs(groups)}, err
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		groups, err := service.store.ListGroups(tx)
		if err != nil || !domain.SameIDs(domain.GroupIDs(groups), ids) {
			return mutationResult{}, errors.Join(ErrConflict, err)
		}
		return mutationResult{value: groups, resultIDs: ids}, nil
	}, false)
	if err != nil {
		return nil, err
	}
	groups, ok := result.value.([]domain.Group)
	if !ok {
		return nil, ErrUnavailable
	}
	return append([]domain.Group(nil), groups...), nil
}

func (service *Service) ReorderTags(ctx context.Context, command domain.Command) ([]domain.Tag, error) {
	if !domain.ValidCommand(command) || !domain.ValidIDs(command.IDs) {
		return nil, ErrInvalidCommand
	}
	result, err := service.mutate(ctx, "tag_reorder", command, func(tx context.Context) (mutationResult, error) {
		current, err := service.store.ListTags(tx)
		if err != nil || !domain.SameIDSet(domain.TagIDs(current), command.IDs) {
			return mutationResult{}, errors.Join(ErrConflict, err)
		}
		tags, err := service.store.ReorderTags(tx, append([]int64(nil), command.IDs...))
		return mutationResult{value: tags, resultIDs: domain.TagIDs(tags)}, err
	}, func(tx context.Context, ids []int64) (mutationResult, error) {
		tags, err := service.store.ListTags(tx)
		if err != nil || !domain.SameIDs(domain.TagIDs(tags), ids) {
			return mutationResult{}, errors.Join(ErrConflict, err)
		}
		return mutationResult{value: tags, resultIDs: ids}, nil
	}, false)
	if err != nil {
		return nil, err
	}
	tags, ok := result.value.([]domain.Tag)
	if !ok {
		return nil, ErrUnavailable
	}
	return append([]domain.Tag(nil), tags...), nil
}

type mutationResult struct {
	value     any
	resultIDs []int64
}

type groupCreateResult struct {
	group domain.Group
	tag   domain.Tag
}

func providerMutationPlan(operation string, command domain.Command, value any) (tagport.CatalogMutationPlan, error) {
	plan := tagport.CatalogMutationPlan{Actor: command.Actor, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID}
	switch operation {
	case "group_create":
		pair, ok := value.(groupCreateResult)
		if !ok {
			return tagport.CatalogMutationPlan{}, ErrUnavailable
		}
		plan.Operation, plan.GroupID, plan.TagID, plan.GroupName, plan.TagName = tagport.CatalogGroupCreate, pair.group.ID, pair.tag.ID, pair.group.Name, pair.tag.Name
	case "group_update":
		group, ok := value.(domain.Group)
		if !ok {
			return tagport.CatalogMutationPlan{}, ErrUnavailable
		}
		plan.Operation, plan.GroupID, plan.GroupName = tagport.CatalogGroupUpdate, group.ID, group.Name
	case "group_archive":
		group, ok := value.(domain.Group)
		if !ok {
			return tagport.CatalogMutationPlan{}, ErrUnavailable
		}
		plan.Operation, plan.GroupID = tagport.CatalogGroupArchive, group.ID
	case "tag_create":
		tag, ok := value.(domain.Tag)
		if !ok {
			return tagport.CatalogMutationPlan{}, ErrUnavailable
		}
		plan.Operation, plan.GroupID, plan.TagID, plan.TagName = tagport.CatalogTagCreate, tag.GroupID, tag.ID, tag.Name
	case "tag_update":
		tag, ok := value.(domain.Tag)
		if !ok {
			return tagport.CatalogMutationPlan{}, ErrUnavailable
		}
		plan.Operation, plan.TagID, plan.TagName = tagport.CatalogTagUpdate, tag.ID, tag.Name
	case "tag_archive":
		tag, ok := value.(domain.Tag)
		if !ok {
			return tagport.CatalogMutationPlan{}, ErrUnavailable
		}
		plan.Operation, plan.TagID = tagport.CatalogTagArchive, tag.ID
	default:
		return tagport.CatalogMutationPlan{}, ErrUnavailable
	}
	return plan, nil
}

// The public read API intentionally hides archived rows. An idempotency
// replay must still return the original archive result, so stores may expose a
// narrow replay-only reader without making archived rows visible to HTTP.
type archivedGroupReader interface {
	GetGroupIncludingArchived(context.Context, int64) (domain.Group, error)
}
type archivedTagReader interface {
	GetTagIncludingArchived(context.Context, int64) (domain.Tag, error)
}

func archivedGroupForReplay(store tagport.CatalogStore, ctx context.Context, id int64) (domain.Group, error) {
	if archived, ok := store.(archivedGroupReader); ok {
		return archived.GetGroupIncludingArchived(ctx, id)
	}
	return store.GetGroup(ctx, id)
}
func archivedTagForReplay(store tagport.CatalogStore, ctx context.Context, id int64) (domain.Tag, error) {
	if archived, ok := store.(archivedTagReader); ok {
		return archived.GetTagIncludingArchived(ctx, id)
	}
	return store.GetTag(ctx, id)
}

func (service *Service) mutate(ctx context.Context, operation string, command domain.Command, apply func(context.Context) (mutationResult, error), replay func(context.Context, []int64) (mutationResult, error), requiresProviderWrite bool) (mutationResult, error) {
	if !service.mutationReady() || ctx == nil {
		return mutationResult{}, ErrUnavailable
	}
	if requiresProviderWrite && service.providerMutationsRequired && (service.mutationStore == nil || service.mutations == nil) {
		return mutationResult{}, errors.Join(ErrUnavailable, ErrProviderMutationUnavailable)
	}
	now := service.clock().UTC()
	if now.IsZero() {
		return mutationResult{}, ErrUnavailable
	}
	digest := commandDigest(operation, command)
	var result mutationResult
	err := service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := service.receipts.ReserveMutation(tx, tagport.MutationReceiptReservation{
			Operation: operation, Actor: command.Actor, IdempotencyKey: command.IdempotencyKey, PayloadDigest: digest,
		})
		if err != nil {
			return err
		}
		if receipt.ID <= 0 || receipt.Operation != operation || receipt.Actor != command.Actor || !equalDigest(receipt.PayloadDigest, digest) {
			return ErrConflict
		}
		if !owned {
			if receipt.State != tagport.MutationCompleted || len(receipt.ResultIDs) == 0 {
				return ErrConflict
			}
			result, err = replay(tx, append([]int64(nil), receipt.ResultIDs...))
			if err != nil || !domain.SameIDs(result.resultIDs, receipt.ResultIDs) {
				return errors.Join(ErrConflict, err)
			}
			return nil
		}
		if receipt.State != tagport.MutationInProgress {
			return ErrConflict
		}
		if requiresProviderWrite && service.mutationStore != nil {
			scope, guarded, scopeErr := providerMutationScope(operation, command)
			if scopeErr != nil {
				return scopeErr
			}
			if guarded {
				if guardErr := service.mutationStore.GuardCatalogMutation(tx, scope); guardErr != nil {
					return guardErr
				}
			}
		}
		result, err = apply(tx)
		if err != nil {
			return err
		}
		if !domain.ValidResultIDs(result.resultIDs) {
			return ErrUnavailable
		}
		if requiresProviderWrite && service.mutations != nil && service.mutationStore != nil {
			plan, planErr := providerMutationPlan(operation, command, result.value)
			if planErr != nil {
				return planErr
			}
			intent, reserveErr := service.mutationStore.ReserveCatalogMutation(tx, plan)
			if reserveErr != nil {
				return reserveErr
			}
			effect, enqueueErr := service.mutations.EnqueueCatalogMutation(tx, intent, command.IdempotencyKey)
			if enqueueErr != nil {
				return enqueueErr
			}
			if acceptErr := service.mutationStore.AcceptCatalogMutation(tx, intent.ID, effect); acceptErr != nil {
				return acceptErr
			}
			// The accepted effect receipt is persisted by the Tag owner in this
			// transaction. Re-read the affected local rows so callers report the
			// durable receipt state (usually queued), rather than inferring it
			// from the requested operation. This same replay reader is used for
			// idempotent requests and deliberately supports archived rows.
			hydrated, hydrateErr := replay(tx, append([]int64(nil), result.resultIDs...))
			if hydrateErr != nil || !domain.SameIDs(hydrated.resultIDs, result.resultIDs) {
				return errors.Join(ErrConflict, hydrateErr)
			}
			result = hydrated
		}
		payload, err := json.Marshal(map[string]any{
			"actor": command.Actor, "operation": operation, "result": result.value, "trace_id": strings.TrimSpace(command.TraceID),
		})
		if err != nil {
			return err
		}
		eventID, err := service.events.Append(tx, tagport.Event{
			Type: "tag.catalog_" + operation, Payload: payload, OccurredAt: now,
			IdempotencyKey: "tag-catalog:" + hex.EncodeToString(digest),
		})
		if err != nil || eventID <= 0 {
			return errors.Join(ErrUnavailable, err)
		}
		completed, err := service.receipts.CompleteMutation(tx, receipt.ID, append([]int64(nil), result.resultIDs...), now)
		if err != nil || completed.ID != receipt.ID || completed.State != tagport.MutationCompleted || !domain.SameIDs(completed.ResultIDs, result.resultIDs) {
			return errors.Join(ErrUnavailable, err)
		}
		return nil
	})
	if err != nil {
		return mutationResult{}, classifyError(err)
	}
	return result, nil
}

func providerMutationScope(operation string, command domain.Command) (tagport.CatalogMutationScope, bool, error) {
	scope := tagport.CatalogMutationScope{}
	switch operation {
	case "group_create":
		// A new group has no pre-existing resource to serialize. Its completed
		// local ID is guarded by ReserveCatalogMutation before any effect exists.
		return scope, false, nil
	case "group_update":
		scope.Operation, scope.GroupID = tagport.CatalogGroupUpdate, command.GroupID
	case "group_archive":
		scope.Operation, scope.GroupID = tagport.CatalogGroupArchive, command.GroupID
	case "tag_create":
		scope.Operation, scope.GroupID = tagport.CatalogTagCreate, command.GroupID
	case "tag_update":
		scope.Operation, scope.TagID = tagport.CatalogTagUpdate, command.TagID
	case "tag_archive":
		scope.Operation, scope.TagID = tagport.CatalogTagArchive, command.TagID
	default:
		return tagport.CatalogMutationScope{}, false, ErrUnavailable
	}
	return scope, true, nil
}

func commandDigest(operation string, command domain.Command) []byte {
	payload, _ := json.Marshal(struct {
		Operation string  `json:"operation"`
		Actor     int64   `json:"actor"`
		GroupID   int64   `json:"group_id"`
		TagID     int64   `json:"tag_id"`
		GroupName string  `json:"group_name"`
		FirstTag  string  `json:"first_tag_name"`
		TagName   string  `json:"tag_name"`
		IDs       []int64 `json:"ids"`
	}{operation, command.Actor, command.GroupID, command.TagID, strings.TrimSpace(command.GroupName), strings.TrimSpace(command.FirstTagName), strings.TrimSpace(command.TagName), command.IDs})
	sum := sha256.Sum256(payload)
	return sum[:]
}

func equalDigest(left, right []byte) bool {
	return len(left) == sha256.Size && len(right) == sha256.Size && string(left) == string(right)
}

func classifyError(err error) error {
	if errors.Is(err, ErrInvalidCommand) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrReferenced) || errors.Is(err, ErrConflict) {
		return err
	}
	return errors.Join(ErrUnavailable, err)
}

func (service *Service) readReady() bool {
	return service != nil && !nilDependency(service.uow) && !nilDependency(service.store) && service.now != nil
}

func (service *Service) mutationReady() bool {
	return service.readReady() && !nilDependency(service.receipts) && !nilDependency(service.events)
}

func (service *Service) clock() time.Time {
	if service == nil || service.now == nil {
		return time.Time{}
	}
	return service.now()
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
