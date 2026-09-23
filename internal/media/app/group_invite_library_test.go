package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	eventport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type groupInviteMemory struct {
	mu                sync.Mutex
	receipts          map[string]GroupInviteReceipt
	items             map[int64]mediaport.GroupInvite
	images            map[int64]bool
	events            []eventport.Event
	channelReferences []int64
	nextID            int64
	fault             string
}

type groupInviteUOW struct{ state *groupInviteMemory }
type groupInviteStore struct{ state *groupInviteMemory }
type groupInviteImages struct{ state *groupInviteMemory }
type groupInviteEvents struct{ state *groupInviteMemory }
type groupInviteContacts struct{ state *groupInviteMemory }

func (u groupInviteUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	u.state.mu.Lock()
	defer u.state.mu.Unlock()
	receipts := make(map[string]GroupInviteReceipt, len(u.state.receipts))
	for key, value := range u.state.receipts {
		value.ResultSnapshot = append(json.RawMessage{}, value.ResultSnapshot...)
		receipts[key] = value
	}
	items := make(map[int64]mediaport.GroupInvite, len(u.state.items))
	for key, value := range u.state.items {
		items[key] = cloneGroupInvite(value)
	}
	events := append([]eventport.Event{}, u.state.events...)
	nextID := u.state.nextID
	if err := fn(ctx); err != nil {
		u.state.receipts, u.state.items, u.state.events, u.state.nextID = receipts, items, events, nextID
		return err
	}
	return nil
}

func groupInviteTestReceiptKey(input GroupInviteReservation) string {
	return input.Operation + ":" + input.ActorScope + ":" + input.BusinessKey + ":" + string(input.KeyDigest[:])
}
func (store groupInviteStore) ReserveGroupInvite(_ context.Context, input GroupInviteReservation) (GroupInviteReceipt, bool, error) {
	if store.state.fault == "reserve" {
		return GroupInviteReceipt{}, false, errors.New("reserve failed")
	}
	key := groupInviteTestReceiptKey(input)
	if value, ok := store.state.receipts[key]; ok {
		return value, false, nil
	}
	value := GroupInviteReceipt{ID: int64(len(store.state.receipts) + 1), Operation: input.Operation,
		ActorScope: input.ActorScope, BusinessKey: input.BusinessKey, KeyDigest: input.KeyDigest,
		PayloadDigest: input.PayloadDigest, State: "in_progress"}
	store.state.receipts[key] = value
	return value, true, nil
}
func (store groupInviteStore) CompleteGroupInvite(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (GroupInviteReceipt, error) {
	if store.state.fault == "complete" {
		return GroupInviteReceipt{}, errors.New("complete failed")
	}
	for key, value := range store.state.receipts {
		if value.ID == id {
			value.State, value.ResultSnapshot = "completed", append(json.RawMessage{}, snapshot...)
			store.state.receipts[key] = value
			return value, nil
		}
	}
	return GroupInviteReceipt{}, errors.New("receipt missing")
}
func (store groupInviteStore) ListGroupInvites(_ context.Context, input mediaport.GroupInviteListQuery) ([]mediaport.GroupInvite, error) {
	items := make([]mediaport.GroupInvite, 0, len(store.state.items))
	for _, item := range store.state.items {
		if item.ArchivedAt != nil || input.EnabledOnly && !item.Enabled {
			continue
		}
		if input.Search != "" && item.Name != input.Search && item.Title != input.Search {
			continue
		}
		items = append(items, cloneGroupInvite(item))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	start := min(int(input.Offset), len(items))
	end := min(start+int(input.Limit), len(items))
	return items[start:end], nil
}
func (store groupInviteStore) CountGroupInvites(_ context.Context, input mediaport.GroupInviteListQuery) (int64, error) {
	items, err := store.ListGroupInvites(context.Background(), mediaport.GroupInviteListQuery{Limit: MaximumGroupInviteLimit, EnabledOnly: input.EnabledOnly, Search: input.Search})
	return int64(len(items)), err
}
func (store groupInviteStore) GetGroupInvite(_ context.Context, id int64) (mediaport.GroupInvite, error) {
	item, ok := store.state.items[id]
	if !ok || item.ArchivedAt != nil {
		return mediaport.GroupInvite{}, ErrGroupInviteNotFound
	}
	return cloneGroupInvite(item), nil
}
func (store groupInviteStore) LockGroupInvite(ctx context.Context, id int64) (mediaport.GroupInvite, error) {
	if store.state.fault == "lock" {
		return mediaport.GroupInvite{}, errors.New("lock failed")
	}
	return store.GetGroupInvite(ctx, id)
}
func (store groupInviteStore) CreateGroupInvite(_ context.Context, item mediaport.GroupInvite) (mediaport.GroupInvite, error) {
	if store.state.fault == "fact" {
		return mediaport.GroupInvite{}, errors.New("fact failed")
	}
	store.state.nextID++
	item.ID = store.state.nextID
	store.state.items[item.ID] = cloneGroupInvite(item)
	return item, nil
}
func (store groupInviteStore) UpdateGroupInvite(_ context.Context, item mediaport.GroupInvite) (mediaport.GroupInvite, error) {
	if store.state.fault == "fact" {
		return mediaport.GroupInvite{}, errors.New("fact failed")
	}
	store.state.items[item.ID] = cloneGroupInvite(item)
	return item, nil
}
func (store groupInviteStore) ArchiveGroupInvite(ctx context.Context, item mediaport.GroupInvite) (mediaport.GroupInvite, error) {
	return store.UpdateGroupInvite(ctx, item)
}
func (images groupInviteImages) ImageExists(_ context.Context, id int64) (bool, error) {
	if images.state.fault == "image" {
		return false, errors.New("image failed")
	}
	return images.state.images[id], nil
}
func (events groupInviteEvents) Append(_ context.Context, event eventport.Event) (eventport.EventID, error) {
	if events.state.fault == "event" {
		return 0, errors.New("event failed")
	}
	events.state.events = append(events.state.events, event)
	return eventport.EventID(len(events.state.events)), nil
}

func (contacts groupInviteContacts) ListGroupInviteReferenceChannelIDs(_ context.Context, _ int64) ([]int64, error) {
	if contacts.state.fault == "contact" {
		return nil, errors.New("channel reference scan failed")
	}
	return append([]int64{}, contacts.state.channelReferences...), nil
}

func TestH03CreateReplayConflictActorIsolationAndConcurrentSingleFact(t *testing.T) {
	service, state := newGroupInviteService()
	command := validGroupInviteCommand()
	first, err := service.Create(context.Background(), command)
	if err != nil || first.ID != 1 || len(state.items) != 1 || len(state.events) != 1 || state.events[0].Type != eventport.EvMediaGroupInviteCreated {
		t.Fatalf("first=%#v err=%v items=%d events=%#v", first, err, len(state.items), state.events)
	}
	service.now = func() time.Time { return first.CreatedAt.Add(time.Hour) }
	replayed, err := service.Create(context.Background(), command)
	if err != nil || !reflect.DeepEqual(first, replayed) || len(state.items) != 1 || len(state.events) != 1 {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	conflict := command
	conflict.Title = "另一张卡"
	if _, err = service.Create(context.Background(), conflict); !errors.Is(err, ErrGroupInviteConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	other := command
	other.Actor = 8
	if _, err = service.Create(context.Background(), other); err != nil || len(state.items) != 2 || len(state.events) != 2 || state.events[0].IdempotencyKey == state.events[1].IdempotencyKey {
		t.Fatalf("actor isolation err=%v events=%#v", err, state.events)
	}

	parallelService, parallelState := newGroupInviteService()
	const workers = 24
	var group sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := parallelService.Create(context.Background(), command)
			errorsChannel <- err
		}()
	}
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent err=%v", err)
		}
	}
	if len(parallelState.items) != 1 || len(parallelState.events) != 1 || len(parallelState.receipts) != 1 {
		t.Fatalf("parallel items=%d events=%d receipts=%d", len(parallelState.items), len(parallelState.events), len(parallelState.receipts))
	}
}

func TestH03MediaReferenceAndFourFailurePointsRollbackAllFacts(t *testing.T) {
	for _, fault := range []string{"image", "fact", "event", "complete"} {
		t.Run(fault, func(t *testing.T) {
			service, state := newGroupInviteService()
			state.fault = fault
			if _, err := service.Create(context.Background(), validGroupInviteCommand()); !errors.Is(err, ErrGroupInviteUnavailable) {
				t.Fatalf("err=%v", err)
			}
			if len(state.items) != 0 || len(state.events) != 0 || len(state.receipts) != 0 {
				t.Fatalf("rollback items=%d events=%d receipts=%d", len(state.items), len(state.events), len(state.receipts))
			}
		})
	}
	service, state := newGroupInviteService()
	command := validGroupInviteCommand()
	command.CoverImageID = 404
	if _, err := service.Create(context.Background(), command); !errors.Is(err, ErrGroupInviteInvalidReference) || len(state.receipts) != 0 {
		t.Fatalf("missing media err=%v receipts=%d", err, len(state.receipts))
	}
}

func TestH03UpdateArchiveListGetAndBusinessKeyIsolation(t *testing.T) {
	service, state := newGroupInviteService()
	first, err := service.Create(context.Background(), validGroupInviteCommand())
	if err != nil {
		t.Fatal(err)
	}
	title := "更新标题"
	updated, err := service.Update(context.Background(), mediaport.GroupInviteUpdateCommand{ID: first.ID,
		GroupInvitePatch: mediaport.GroupInvitePatch{Title: &title}, Actor: 7, IdempotencyKey: "group-invite-shared-key-0001"})
	if err != nil || updated.Title != title || updated.Version != 2 || state.events[1].Type != eventport.EvMediaGroupInviteUpdated {
		t.Fatalf("updated=%#v err=%v events=%#v", updated, err, state.events)
	}
	got, err := service.Get(context.Background(), first.ID)
	if err != nil || !reflect.DeepEqual(got, updated) {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	page, err := service.List(context.Background(), mediaport.GroupInviteListQuery{Limit: 100, EnabledOnly: true, Search: title})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	archived, err := service.Archive(context.Background(), mediaport.GroupInviteArchiveCommand{ID: first.ID, Actor: 7, IdempotencyKey: "group-invite-shared-key-0001"})
	if err != nil || archived.ArchivedAt == nil || archived.Enabled || state.events[2].Type != eventport.EvMediaGroupInviteArchived {
		t.Fatalf("archived=%#v err=%v events=%#v", archived, err, state.events)
	}
	replay, err := service.Archive(context.Background(), mediaport.GroupInviteArchiveCommand{ID: first.ID, Actor: 7, IdempotencyKey: "group-invite-shared-key-0001"})
	if err != nil || !reflect.DeepEqual(replay, archived) || len(state.events) != 3 {
		t.Fatalf("archive replay=%#v err=%v", replay, err)
	}
	if _, err = service.Get(context.Background(), first.ID); !errors.Is(err, ErrGroupInviteNotFound) {
		t.Fatalf("archived get err=%v", err)
	}
}

func TestGroupInviteArchiveFailsClosedWhenChannelReferencesExistOrCannotBeRead(t *testing.T) {
	service, state := newGroupInviteService()
	created, err := service.Create(context.Background(), validGroupInviteCommand())
	if err != nil {
		t.Fatal(err)
	}
	state.channelReferences = []int64{9}
	if _, err = service.Archive(context.Background(), mediaport.GroupInviteArchiveCommand{ID: created.ID, Actor: 7, IdempotencyKey: "group-invite-archive-key-0002"}); !errors.Is(err, ErrGroupInviteHasReferences) || state.items[created.ID].ArchivedAt != nil {
		t.Fatalf("referenced archive err=%v item=%#v", err, state.items[created.ID])
	}
	state.channelReferences = nil
	state.fault = "contact"
	if _, err = service.Archive(context.Background(), mediaport.GroupInviteArchiveCommand{ID: created.ID, Actor: 7, IdempotencyKey: "group-invite-archive-key-0003"}); !errors.Is(err, ErrGroupInviteUnavailable) || state.items[created.ID].ArchivedAt != nil {
		t.Fatalf("unavailable scan err=%v item=%#v", err, state.items[created.ID])
	}
}

func TestH03SemanticReceiptNumbers(t *testing.T) {
	if !jsonSemanticEqual([]byte(`{"id":1,"nested":[2.0]}`), []byte(`{"nested":[2],"id":1.0}`)) {
		t.Fatal("numeric JSON equality rejected")
	}
}

func newGroupInviteService() (*GroupInviteService, *groupInviteMemory) {
	state := &groupInviteMemory{receipts: map[string]GroupInviteReceipt{}, items: map[int64]mediaport.GroupInvite{}, images: map[int64]bool{19: true, 20: true}}
	service := NewGroupInviteServiceWithChannelReferences(groupInviteUOW{state}, groupInviteStore{state}, groupInviteImages{state}, groupInviteEvents{state}, groupInviteContacts{state})
	service.now = func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }
	return service, state
}

func validGroupInviteCommand() mediaport.GroupInviteCreateCommand {
	return mediaport.GroupInviteCreateCommand{Name: "体验群", Title: "加入体验群", Description: "点击卡片入群",
		JoinURL: "https://work.weixin.qq.com/gm/safe-token", CoverImageID: 19, Actor: 7, IdempotencyKey: "group-invite-create-0001"}
}

func cloneGroupInvite(item mediaport.GroupInvite) mediaport.GroupInvite {
	if item.ArchivedAt != nil {
		value := *item.ArchivedAt
		item.ArchivedAt = &value
	}
	return item
}
