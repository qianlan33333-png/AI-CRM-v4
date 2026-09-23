package channel

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
)

func TestCatalogServiceCreateReplayCASAndReferenceValidation(t *testing.T) {
	store := &catalogMemoryStore{channels: map[int64]channeldomain.Channel{}, receipts: map[[32]byte]channelport.OperationReceipt{}}
	events := &catalogMemoryEvents{}
	service := NewCatalogService(catalogDirectUOW{}, store, store, events, catalogMaterialRefs{}, catalogTagRefs{}, catalogStaffRefs{})
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	create := CatalogMutation{ActorID: 7, IdempotencyKey: "channel-create-0001", Create: validCatalogCreate()}

	first, err := service.Create(context.Background(), create)
	if err != nil || first.ID != 1 || first.Version != 1 || len(events.items) != 1 {
		t.Fatalf("create=%#v events=%d err=%v", first, len(events.items), err)
	}
	replay, err := service.Create(context.Background(), create)
	if err != nil || !reflect.DeepEqual(replay, first) || len(events.items) != 1 {
		t.Fatalf("replay=%#v events=%d err=%v", replay, len(events.items), err)
	}
	drift := create
	drift.Create.Config.Name = "Different"
	if _, err = service.Create(context.Background(), drift); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("payload drift error=%v", err)
	}

	update := CatalogMutation{ActorID: 7, IdempotencyKey: "channel-update-0001", Update: channeldomain.UpdateChannel{ExpectedVersion: 1, Code: first.Code, Status: channeldomain.StatusInactive, Config: first.Config}}
	updated, err := service.Update(context.Background(), first.ID, update)
	if err != nil || updated.Version != 2 || updated.ConfigVersion != 2 || updated.Status != channeldomain.StatusInactive || len(events.items) != 2 {
		t.Fatalf("update=%#v events=%d err=%v", updated, len(events.items), err)
	}
	stale := update
	stale.IdempotencyKey = "channel-update-0002"
	if _, err = service.Update(context.Background(), first.ID, stale); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("stale CAS error=%v", err)
	}
}

func TestCatalogServiceFailsClosedOnUnknownReferences(t *testing.T) {
	store := &catalogMemoryStore{channels: map[int64]channeldomain.Channel{}, receipts: map[[32]byte]channelport.OperationReceipt{}}
	service := NewCatalogService(catalogDirectUOW{}, store, store, &catalogMemoryEvents{}, catalogMaterialRefs{err: errors.New("media unavailable")}, catalogTagRefs{}, catalogStaffRefs{})
	create := validCatalogCreate()
	create.Config.Media.Images = []int64{44}
	_, err := service.Create(context.Background(), CatalogMutation{ActorID: 7, IdempotencyKey: "channel-create-refs", Create: create})
	if err == nil || len(store.channels) != 0 {
		t.Fatalf("reference failure err=%v channels=%d", err, len(store.channels))
	}
}

func validCatalogCreate() channeldomain.CreateChannel {
	return channeldomain.CreateChannel{Code: "campaign.autumn", Status: channeldomain.StatusActive, Config: channeldomain.Config{
		Type: channeldomain.ChannelQRCode, Carrier: channeldomain.CarrierQRCode, Name: "Autumn campaign",
		Assignment: channeldomain.Assignment{Mode: channeldomain.AssignmentSingle, Strategy: channeldomain.StrategyRatio, Assignees: []channeldomain.Assignee{{StaffID: 9, Priority: 1, Ratio: 100}}},
	}}
}

type catalogDirectUOW struct{}

func (catalogDirectUOW) Within(ctx context.Context, run func(context.Context) error) error {
	return run(ctx)
}

type catalogMemoryStore struct {
	channels    map[int64]channeldomain.Channel
	receipts    map[[32]byte]channelport.OperationReceipt
	nextReceipt int64
}

func (store *catalogMemoryStore) Get(_ context.Context, id int64) (channeldomain.Channel, error) {
	channel, ok := store.channels[id]
	if !ok {
		return channeldomain.Channel{}, ErrCatalogNotFound
	}
	return channel, nil
}

func (store *catalogMemoryStore) List(context.Context, channelport.CatalogFilter) ([]channeldomain.Channel, int64, error) {
	items := make([]channeldomain.Channel, 0, len(store.channels))
	for _, item := range store.channels {
		items = append(items, item)
	}
	return items, int64(len(items)), nil
}

func (store *catalogMemoryStore) Create(_ context.Context, value channeldomain.Channel, _ int64) (channeldomain.Channel, error) {
	value.ID = int64(len(store.channels) + 1)
	store.channels[value.ID] = value
	return value, nil
}

func (store *catalogMemoryStore) Update(_ context.Context, value channeldomain.Channel, _ int64) (channeldomain.Channel, error) {
	store.channels[value.ID] = value
	return value, nil
}

func (store *catalogMemoryStore) ReferenceCount(context.Context, int64) (int64, error) { return 0, nil }

func (store *catalogMemoryStore) Reserve(_ context.Context, value channelport.OperationReceipt) (channelport.OperationReceipt, bool, error) {
	if existing, ok := store.receipts[value.KeyDigest]; ok {
		return existing, false, nil
	}
	store.nextReceipt++
	value.ID = store.nextReceipt
	value.State = channelport.ReceiptInProgress
	store.receipts[value.KeyDigest] = value
	return value, true, nil
}

func (store *catalogMemoryStore) Complete(_ context.Context, id, channelID, version int64, now time.Time) (channelport.OperationReceipt, error) {
	for key, value := range store.receipts {
		if value.ID == id {
			value.State = channelport.ReceiptCompleted
			value.ChannelID = channelID
			value.Version = version
			value.CompletedAt = &now
			store.receipts[key] = value
			return value, nil
		}
	}
	return channelport.OperationReceipt{}, ErrCatalogConflict
}

type catalogMemoryEvents struct{ items []channelport.CatalogEvent }

func (events *catalogMemoryEvents) Append(_ context.Context, event channelport.CatalogEvent) error {
	events.items = append(events.items, event)
	return nil
}

type catalogMaterialRefs struct{ err error }

func (refs catalogMaterialRefs) ValidateChannelMaterials(context.Context, channelport.MaterialReferences) error {
	return refs.err
}

type catalogTagRefs struct{}

func (catalogTagRefs) ReadChannelTag(_ context.Context, id int64) (channelport.TagSnapshot, error) {
	return channelport.TagSnapshot{ID: id, Name: "Lead", GroupName: "Lifecycle", Active: true}, nil
}

type catalogStaffRefs struct{}

func (catalogStaffRefs) ReadChannelStaff(_ context.Context, ids []int64) ([]channelport.StaffSnapshot, error) {
	result := make([]channelport.StaffSnapshot, len(ids))
	for index, id := range ids {
		result[index] = channelport.StaffSnapshot{ID: id, Active: true}
	}
	return result, nil
}
