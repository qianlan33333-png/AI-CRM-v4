package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type catalogUOW struct {
	in    bool
	calls int
}

func (uow *catalogUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	uow.in = true
	defer func() { uow.in = false }()
	return callback(ctx)
}

type catalogEvents struct {
	uow   *catalogUOW
	items []tagport.Event
}

func (events *catalogEvents) Append(_ context.Context, event tagport.Event) (int64, error) {
	if events.uow == nil || !events.uow.in {
		return 0, errors.New("event outside uow")
	}
	events.items = append(events.items, event)
	return int64(len(events.items)), nil
}

type zeroIDCatalogEvents struct{ uow *catalogUOW }

func (events *zeroIDCatalogEvents) Append(_ context.Context, _ tagport.Event) (int64, error) {
	if events.uow == nil || !events.uow.in {
		return 0, errors.New("event outside uow")
	}
	return 0, nil
}

type catalogStore struct {
	uow             *catalogUOW
	groups          []domain.Group
	tags            []domain.Tag
	writes          int
	receipt         tagport.MutationReceipt
	receiptOwned    bool
	tagReferences   map[int64]int64
	groupReferences map[int64]int64
}

func (store *catalogStore) check() error {
	if store.uow == nil || !store.uow.in {
		return errors.New("store outside uow")
	}
	return nil
}

func (store *catalogStore) ListGroups(_ context.Context) ([]domain.Group, error) {
	return append([]domain.Group{}, store.groups...), store.check()
}

func (store *catalogStore) ListTags(_ context.Context) ([]domain.Tag, error) {
	return append([]domain.Tag{}, store.tags...), store.check()
}

func (store *catalogStore) CreateGroup(_ context.Context, name string) (domain.Group, error) {
	if err := store.check(); err != nil {
		return domain.Group{}, err
	}
	store.writes++
	group := domain.Group{ID: int64(len(store.groups) + 1), Name: name, SortOrder: int32(len(store.groups))}
	store.groups = append(store.groups, group)
	return group, nil
}

func (store *catalogStore) CreateTag(_ context.Context, groupID int64, name string) (domain.Tag, error) {
	if err := store.check(); err != nil {
		return domain.Tag{}, err
	}
	store.writes++
	for _, group := range store.groups {
		if group.ID == groupID {
			tag := domain.Tag{ID: int64(len(store.tags) + 1), GroupID: groupID, GroupName: group.Name, Name: name, SortOrder: int32(len(store.tags))}
			store.tags = append(store.tags, tag)
			return tag, nil
		}
	}
	return domain.Tag{}, ErrNotFound
}

func (store *catalogStore) UpdateGroup(_ context.Context, id int64, name string) (domain.Group, error) {
	if err := store.check(); err != nil {
		return domain.Group{}, err
	}
	for index := range store.groups {
		if store.groups[index].ID == id {
			store.groups[index].Name = name
			for tagIndex := range store.tags {
				if store.tags[tagIndex].GroupID == id {
					store.tags[tagIndex].GroupName = name
				}
			}
			return store.groups[index], nil
		}
	}
	return domain.Group{}, ErrNotFound
}

func (store *catalogStore) ArchiveGroup(_ context.Context, id int64) (domain.Group, error) {
	if err := store.check(); err != nil {
		return domain.Group{}, err
	}
	for index := range store.groups {
		if store.groups[index].ID == id {
			store.groups[index].Name = "archived:" + string(rune('0'+id))
			return store.groups[index], nil
		}
	}
	return domain.Group{}, ErrNotFound
}

func (store *catalogStore) UpdateTag(_ context.Context, id int64, name string) (domain.Tag, error) {
	if err := store.check(); err != nil {
		return domain.Tag{}, err
	}
	for index := range store.tags {
		if store.tags[index].ID == id {
			store.tags[index].Name = name
			return store.tags[index], nil
		}
	}
	return domain.Tag{}, ErrNotFound
}

func (store *catalogStore) ArchiveTag(_ context.Context, id int64) (domain.Tag, error) {
	if err := store.check(); err != nil {
		return domain.Tag{}, err
	}
	for index := range store.tags {
		if store.tags[index].ID == id {
			store.tags[index].Name = "archived:" + string(rune('0'+id))
			return store.tags[index], nil
		}
	}
	return domain.Tag{}, ErrNotFound
}

func (store *catalogStore) GetGroup(_ context.Context, id int64) (domain.Group, error) {
	for _, group := range store.groups {
		if group.ID == id {
			return group, nil
		}
	}
	return domain.Group{}, ErrNotFound
}

func (store *catalogStore) GetTag(_ context.Context, id int64) (domain.Tag, error) {
	for _, tag := range store.tags {
		if tag.ID == id {
			return tag, nil
		}
	}
	return domain.Tag{}, ErrNotFound
}

func (store *catalogStore) ReorderGroups(_ context.Context, ids []int64) ([]domain.Group, error) {
	if err := store.check(); err != nil {
		return nil, err
	}
	if !domain.SameIDSet(domain.GroupIDs(store.groups), ids) {
		return nil, ErrConflict
	}
	byID := make(map[int64]domain.Group, len(store.groups))
	for _, group := range store.groups {
		byID[group.ID] = group
	}
	reordered := make([]domain.Group, 0, len(ids))
	for index, id := range ids {
		group := byID[id]
		group.SortOrder = int32(index)
		reordered = append(reordered, group)
	}
	store.groups = reordered
	return append([]domain.Group(nil), store.groups...), nil
}

func (store *catalogStore) ReorderTags(_ context.Context, ids []int64) ([]domain.Tag, error) {
	if err := store.check(); err != nil {
		return nil, err
	}
	if !domain.SameIDSet(domain.TagIDs(store.tags), ids) {
		return nil, ErrConflict
	}
	byID := make(map[int64]domain.Tag, len(store.tags))
	for _, tag := range store.tags {
		byID[tag.ID] = tag
	}
	reordered := make([]domain.Tag, 0, len(ids))
	for index, id := range ids {
		tag := byID[id]
		tag.SortOrder = int32(index)
		reordered = append(reordered, tag)
	}
	store.tags = reordered
	return append([]domain.Tag(nil), store.tags...), nil
}

func (store *catalogStore) ReserveMutation(_ context.Context, reservation tagport.MutationReceiptReservation) (tagport.MutationReceipt, bool, error) {
	if err := store.check(); err != nil {
		return tagport.MutationReceipt{}, false, err
	}
	if store.receipt.ID == 0 {
		store.receipt = tagport.MutationReceipt{ID: 1, Operation: reservation.Operation, Actor: reservation.Actor, IdempotencyKey: reservation.IdempotencyKey, PayloadDigest: append([]byte(nil), reservation.PayloadDigest...), State: tagport.MutationInProgress}
		store.receiptOwned = true
		return store.receipt, true, nil
	}
	return store.receipt, false, nil
}

func (store *catalogStore) CompleteMutation(_ context.Context, id int64, resultIDs []int64, _ time.Time) (tagport.MutationReceipt, error) {
	if err := store.check(); err != nil {
		return tagport.MutationReceipt{}, err
	}
	if id != store.receipt.ID || !store.receiptOwned {
		return tagport.MutationReceipt{}, ErrConflict
	}
	store.receipt.State = tagport.MutationCompleted
	store.receipt.ResultIDs = append([]int64(nil), resultIDs...)
	store.receiptOwned = false
	return store.receipt, nil
}

type referenceGuard struct{ store *catalogStore }

func (guard referenceGuard) TagReferences(_ context.Context, id int64) (int64, error) {
	if err := guard.store.check(); err != nil {
		return 0, err
	}
	return guard.store.tagReferences[id], nil
}

func (guard referenceGuard) GroupReferences(_ context.Context, id int64) (int64, error) {
	if err := guard.store.check(); err != nil {
		return 0, err
	}
	return guard.store.groupReferences[id], nil
}

type providerMutationStore struct {
	*catalogStore
	intents []tagport.CatalogMutationIntent
}

func (*providerMutationStore) GuardCatalogMutation(context.Context, tagport.CatalogMutationScope) error {
	return nil
}

func (store *providerMutationStore) ReserveCatalogMutation(_ context.Context, plan tagport.CatalogMutationPlan) (tagport.CatalogMutationIntent, error) {
	if err := store.check(); err != nil {
		return tagport.CatalogMutationIntent{}, err
	}
	intent := tagport.CatalogMutationIntent{ID: int64(len(store.intents) + 1), Operation: plan.Operation, Actor: plan.Actor, GroupID: plan.GroupID, TagID: plan.TagID, GroupName: plan.GroupName, TagName: plan.TagName}
	store.intents = append(store.intents, intent)
	return intent, nil
}

func (store *providerMutationStore) AcceptCatalogMutation(_ context.Context, id int64, effect tagport.CatalogMutationEffectReceipt) error {
	if err := store.check(); err != nil {
		return err
	}
	if id < 1 || int(id) > len(store.intents) || effect.EffectState != "queued" {
		return ErrConflict
	}
	intent := store.intents[id-1]
	for index := range store.groups {
		if store.groups[index].ID == intent.GroupID {
			store.groups[index].ProviderMutationState = effect.EffectState
		}
	}
	for index := range store.tags {
		if store.tags[index].ID == intent.TagID {
			store.tags[index].ProviderMutationState = effect.EffectState
		}
	}
	return nil
}

func (*providerMutationStore) ReadCatalogMutationDispatch(context.Context, string) (tagport.CatalogMutationDispatch, error) {
	return tagport.CatalogMutationDispatch{}, ErrNotFound
}
func (*providerMutationStore) CompleteCatalogMutation(context.Context, tagport.CatalogMutationCompletion) error {
	return nil
}

type providerMutationEnqueuer struct{ calls int }

func (enqueuer *providerMutationEnqueuer) EnqueueCatalogMutation(_ context.Context, intent tagport.CatalogMutationIntent, _ string) (tagport.CatalogMutationEffectReceipt, error) {
	enqueuer.calls++
	return tagport.CatalogMutationEffectReceipt{EffectID: intent.ID, QueueJobID: intent.ID, EffectRef: "eer_1", EffectState: "queued", AcceptReceiptID: "eerop_1", QueueReceiptID: "eerop_1"}, nil
}

var _ platformport.UnitOfWork = (*catalogUOW)(nil)
var _ tagport.CatalogStore = (*catalogStore)(nil)
var _ tagport.MutationReceiptStore = (*catalogStore)(nil)
var _ tagport.CatalogMutationStore = (*providerMutationStore)(nil)
var _ tagport.CatalogMutationEnqueuer = (*providerMutationEnqueuer)(nil)

// archiveReplayStore models the PostgreSQL public-read behavior: once
// archived, GetTag no longer exposes the row, while the narrow internal
// replay reader can still return the original result.
type archiveReplayStore struct {
	*catalogStore
	archived map[int64]domain.Tag
}

func (store *archiveReplayStore) ArchiveTag(ctx context.Context, id int64) (domain.Tag, error) {
	tag, err := store.catalogStore.ArchiveTag(ctx, id)
	if err == nil {
		if store.archived == nil {
			store.archived = map[int64]domain.Tag{}
		}
		store.archived[id] = tag
	}
	return tag, err
}

func (store *archiveReplayStore) GetTag(_ context.Context, id int64) (domain.Tag, error) {
	if _, archived := store.archived[id]; archived {
		return domain.Tag{}, ErrNotFound
	}
	return store.catalogStore.GetTag(context.Background(), id)
}

func (store *archiveReplayStore) GetTagIncludingArchived(_ context.Context, id int64) (domain.Tag, error) {
	if tag, archived := store.archived[id]; archived {
		return tag, nil
	}
	return store.catalogStore.GetTag(context.Background(), id)
}

func TestCatalogServiceListsStableCatalogAndEmptySlices(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{uow: uow, groups: []domain.Group{{ID: 11, Name: "客户阶段", SortOrder: 0}}, tags: []domain.Tag{{ID: 21, GroupID: 11, GroupName: "客户阶段", Name: "新客", SortOrder: 0}}}
	service := NewService(uow, store, nil, nil, nil)
	service.now = func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }
	got, err := service.List(context.Background())
	if err != nil || len(got.Groups) != 1 || len(got.Tags) != 1 || !got.SyncedAt.IsZero() {
		t.Fatalf("List() = %#v, %v", got, err)
	}
	store.groups, store.tags = []domain.Group{}, []domain.Tag{}
	got, err = service.List(context.Background())
	if err != nil || got.Groups == nil || got.Tags == nil {
		t.Fatalf("empty List() = %#v, %v", got, err)
	}
}

func TestCatalogServiceRejectsInvalidOrderAndGroupMismatch(t *testing.T) {
	for _, catalog := range []domain.Catalog{
		{Groups: []domain.Group{{ID: 2, Name: "二", SortOrder: 1}, {ID: 1, Name: "一", SortOrder: 0}}, Tags: []domain.Tag{}},
		{Groups: []domain.Group{{ID: 1, Name: "一", SortOrder: 0}}, Tags: []domain.Tag{{ID: 2, GroupID: 1, GroupName: "错", Name: "标签", SortOrder: 0}}},
	} {
		uow := &catalogUOW{}
		store := &catalogStore{uow: uow, groups: catalog.Groups, tags: catalog.Tags}
		if _, err := NewService(uow, store, nil, nil, nil).List(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid catalog error = %v", err)
		}
	}
}

func TestCatalogServiceUsesGroupSortOrderForTagOrder(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{
		uow:    uow,
		groups: []domain.Group{{ID: 20, Name: "优先", SortOrder: 0}, {ID: 10, Name: "次级", SortOrder: 1}},
		tags: []domain.Tag{
			{ID: 201, GroupID: 20, GroupName: "优先", Name: "A", SortOrder: 0},
			{ID: 101, GroupID: 10, GroupName: "次级", Name: "B", SortOrder: 0},
		},
	}
	if _, err := NewService(uow, store, nil, nil, nil).List(context.Background()); err != nil {
		t.Fatalf("List() rejected group-ordered tags: %v", err)
	}
}

func TestCatalogServiceCreateReplaysAndRejectsPayloadDrift(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{uow: uow, groups: []domain.Group{}, tags: []domain.Tag{}}
	events := &catalogEvents{uow: uow}
	service := NewService(uow, store, store, events, referenceGuard{store})
	command := domain.Command{Actor: 7, IdempotencyKey: "catalog-key-0001", GroupName: "来源", FirstTagName: "活动"}
	firstGroup, firstTag, err := service.CreateGroup(context.Background(), command)
	if err != nil || firstGroup.ID != 1 || firstTag.ID != 1 {
		t.Fatalf("first CreateGroup() = %#v/%#v, %v", firstGroup, firstTag, err)
	}
	secondGroup, secondTag, err := service.CreateGroup(context.Background(), command)
	if err != nil || !reflect.DeepEqual(secondGroup, firstGroup) || !reflect.DeepEqual(secondTag, firstTag) {
		t.Fatalf("replay CreateGroup() = %#v/%#v, %v", secondGroup, secondTag, err)
	}
	if store.writes != 2 || len(events.items) != 1 {
		t.Fatalf("replay writes/events = %d/%d", store.writes, len(events.items))
	}
	command.GroupName = "漂移"
	if _, _, err = service.CreateGroup(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("payload drift error = %v", err)
	}
}

func TestCatalogServiceReturnsPersistedProviderStateAfterAcceptedMutationAndReplay(t *testing.T) {
	uow := &catalogUOW{}
	base := &catalogStore{uow: uow, groups: []domain.Group{}, tags: []domain.Tag{}}
	store := &providerMutationStore{catalogStore: base}
	enqueuer := &providerMutationEnqueuer{}
	service := NewService(uow, store, base, &catalogEvents{uow: uow}, referenceGuard{store: base})
	if err := service.BindProviderMutations(enqueuer); err != nil {
		t.Fatal(err)
	}
	command := domain.Command{Actor: 7, IdempotencyKey: "provider-state-key-0001", GroupName: "来源", FirstTagName: "活动"}
	firstGroup, firstTag, err := service.CreateGroup(context.Background(), command)
	if err != nil || firstGroup.ProviderMutationState != "queued" || firstTag.ProviderMutationState != "queued" {
		t.Fatalf("first CreateGroup() = %#v/%#v, %v", firstGroup, firstTag, err)
	}
	secondGroup, secondTag, err := service.CreateGroup(context.Background(), command)
	if err != nil || secondGroup.ProviderMutationState != "queued" || secondTag.ProviderMutationState != "queued" || enqueuer.calls != 1 {
		t.Fatalf("replayed CreateGroup() = %#v/%#v, %v calls=%d", secondGroup, secondTag, err, enqueuer.calls)
	}
}

func TestCatalogServiceFailsClosedWhenProviderMutationIsRequiredButUnbound(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{uow: uow, groups: []domain.Group{}, tags: []domain.Tag{}}
	service := NewService(uow, store, store, &catalogEvents{uow: uow}, referenceGuard{store})
	if err := service.RequireProviderMutations(); err != nil {
		t.Fatal(err)
	}
	_, _, err := service.CreateGroup(context.Background(), domain.Command{Actor: 7, IdempotencyKey: "provider-required-key-0001", GroupName: "来源", FirstTagName: "活动"})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrProviderMutationUnavailable) || store.writes != 0 || len(store.groups) != 0 || len(store.tags) != 0 {
		t.Fatalf("err=%v writes=%d groups=%+v tags=%+v", err, store.writes, store.groups, store.tags)
	}
}

func TestCatalogServiceRejectsReceiptWhenEventIDIsNotDurable(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{uow: uow, groups: []domain.Group{}, tags: []domain.Tag{}}
	service := NewService(uow, store, store, &zeroIDCatalogEvents{uow: uow}, referenceGuard{store})
	_, _, err := service.CreateGroup(context.Background(), domain.Command{Actor: 7, IdempotencyKey: "event-id-key-0001", GroupName: "组", FirstTagName: "标签"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreateGroup() error = %v, want unavailable", err)
	}
	if store.receipt.State != tagport.MutationInProgress {
		t.Fatalf("receipt state = %q, want in_progress before transaction rollback", store.receipt.State)
	}
}

func TestCatalogServiceArchiveChecksReadOnlyReferenceGuard(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{uow: uow, groups: []domain.Group{{ID: 11, Name: "阶段", SortOrder: 0}}, tags: []domain.Tag{{ID: 21, GroupID: 11, GroupName: "阶段", Name: "新客", SortOrder: 0}}, tagReferences: map[int64]int64{21: 1}}
	service := NewService(uow, store, store, &catalogEvents{uow: uow}, referenceGuard{store})
	if _, err := service.ArchiveTag(context.Background(), domain.Command{Actor: 1, TagID: 21, IdempotencyKey: "archive-tag-key-1"}); !errors.Is(err, ErrReferenced) {
		t.Fatalf("ArchiveTag() error = %v", err)
	}
	if store.writes != 0 {
		t.Fatalf("archive wrote %d times", store.writes)
	}
}

func TestCatalogServiceArchiveTagReplaysArchivedResultWithSameKey(t *testing.T) {
	uow := &catalogUOW{}
	base := &catalogStore{uow: uow, groups: []domain.Group{{ID: 11, Name: "阶段", SortOrder: 0}}, tags: []domain.Tag{{ID: 21, GroupID: 11, GroupName: "阶段", Name: "新客", SortOrder: 0}}, tagReferences: map[int64]int64{}}
	store := &archiveReplayStore{catalogStore: base}
	service := NewService(uow, store, base, &catalogEvents{uow: uow}, referenceGuard{base})
	command := domain.Command{Actor: 1, TagID: 21, IdempotencyKey: "archive-tag-replay-key"}
	first, err := service.ArchiveTag(context.Background(), command)
	if err != nil {
		t.Fatalf("first ArchiveTag() error = %v", err)
	}
	second, err := service.ArchiveTag(context.Background(), command)
	if err != nil || !reflect.DeepEqual(second, first) {
		t.Fatalf("replayed ArchiveTag() = %#v, %v; want %#v", second, err, first)
	}
	if base.writes != 0 { // the fake archive is a metadata mark, not a Create write.
		t.Fatalf("unexpected create writes = %d", base.writes)
	}
}

func TestCatalogServiceReorderRejectsStalePartialList(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{uow: uow, groups: []domain.Group{{ID: 11, Name: "一", SortOrder: 0}, {ID: 12, Name: "二", SortOrder: 1}}, tags: []domain.Tag{}}
	service := NewService(uow, store, store, &catalogEvents{uow: uow}, referenceGuard{store})
	_, err := service.ReorderGroups(context.Background(), domain.Command{Actor: 1, IdempotencyKey: "reorder-group-key", IDs: []int64{11}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reorder error = %v", err)
	}
}

func TestCatalogServiceReordersCompletePermutationAndReplaysOrderedReceipt(t *testing.T) {
	uow := &catalogUOW{}
	store := &catalogStore{
		uow:    uow,
		groups: []domain.Group{{ID: 11, Name: "一", SortOrder: 0}, {ID: 12, Name: "二", SortOrder: 1}},
		tags: []domain.Tag{
			{ID: 21, GroupID: 11, GroupName: "一", Name: "甲", SortOrder: 0},
			{ID: 22, GroupID: 11, GroupName: "一", Name: "乙", SortOrder: 1},
		},
	}
	events := &catalogEvents{uow: uow}
	service := NewService(uow, store, store, events, referenceGuard{store})
	groupCommand := domain.Command{Actor: 1, IdempotencyKey: "reorder-groups-key", IDs: []int64{12, 11}}
	groups, err := service.ReorderGroups(context.Background(), groupCommand)
	if err != nil || !reflect.DeepEqual(domain.GroupIDs(groups), []int64{12, 11}) || groups[0].SortOrder != 0 || groups[1].SortOrder != 1 {
		t.Fatalf("ReorderGroups() = %#v, %v", groups, err)
	}
	if len(events.items) != 1 || events.items[0].Type != "tag.catalog_group_reorder" {
		t.Fatalf("group reorder event = %#v", events.items)
	}
	replayed, err := service.ReorderGroups(context.Background(), groupCommand)
	if err != nil || !reflect.DeepEqual(domain.GroupIDs(replayed), []int64{12, 11}) || len(events.items) != 1 {
		t.Fatalf("replayed ReorderGroups() = %#v, %v events=%d", replayed, err, len(events.items))
	}

	store.receipt = tagport.MutationReceipt{}
	store.receiptOwned = false
	tagCommand := domain.Command{Actor: 1, IdempotencyKey: "reorder-tags-key", IDs: []int64{22, 21}}
	tags, err := service.ReorderTags(context.Background(), tagCommand)
	if err != nil || !reflect.DeepEqual(domain.TagIDs(tags), []int64{22, 21}) || tags[0].SortOrder != 0 || tags[1].SortOrder != 1 {
		t.Fatalf("ReorderTags() = %#v, %v", tags, err)
	}
	if len(events.items) != 2 || events.items[1].Type != "tag.catalog_tag_reorder" {
		t.Fatalf("tag reorder event = %#v", events.items)
	}
}
