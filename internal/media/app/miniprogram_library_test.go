package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	eventport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type miniProgramTestState struct {
	mu                sync.Mutex
	receipts          map[string]MiniProgramReceipt
	items             map[int64]mediaport.MiniProgram
	images            map[int64]bool
	events            []eventport.Event
	channelReferences []int64
	nextID            int64
	fault             string
}

type miniProgramTestUOW struct{ state *miniProgramTestState }
type miniProgramTestStore struct{ state *miniProgramTestState }
type miniProgramTestImages struct{ state *miniProgramTestState }
type miniProgramTestEvents struct{ state *miniProgramTestState }
type miniProgramTestContacts struct{ state *miniProgramTestState }
type miniProgramTestResolver struct {
	resolution mediaport.ThumbnailCacheResolution
	err        error
	calls      int
}

func (u miniProgramTestUOW) Within(ctx context.Context, run func(context.Context) error) error {
	u.state.mu.Lock()
	defer u.state.mu.Unlock()
	before := cloneMiniProgramTestState(u.state)
	if err := run(ctx); err != nil {
		restoreMiniProgramTestState(u.state, before)
		return err
	}
	return nil
}

func (store miniProgramTestStore) ListMiniPrograms(_ context.Context, query mediaport.MiniProgramListQuery) ([]mediaport.MiniProgram, error) {
	if store.state.fault == "list" {
		return nil, errors.New("list failed")
	}
	items := make([]mediaport.MiniProgram, 0, len(store.state.items))
	for _, item := range store.state.items {
		if query.EnabledOnly && !item.Enabled {
			continue
		}
		if query.Search != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.AppID+" "+item.Title), strings.ToLower(query.Search)) {
			continue
		}
		items = append(items, cloneMiniProgram(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if store.state.fault == "unsorted-list" {
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	}
	start := int(query.Offset)
	if start > len(items) {
		start = len(items)
	}
	end := start + int(query.Limit)
	if end > len(items) {
		end = len(items)
	}
	page := items[start:end]
	if store.state.fault == "list-over-limit" && len(page) > 0 {
		page = append(page, cloneMiniProgram(page[0]))
	}
	return page, nil
}

func (store miniProgramTestStore) CountMiniPrograms(ctx context.Context, query mediaport.MiniProgramListQuery) (int64, error) {
	if store.state.fault == "negative-total" {
		return -1, nil
	}
	if store.state.fault == "short-total" {
		return 0, nil
	}
	all, err := store.ListMiniPrograms(ctx, mediaport.MiniProgramListQuery{Limit: int32(len(store.state.items)) + 1, EnabledOnly: query.EnabledOnly, Search: query.Search})
	return int64(len(all)), err
}

func (store miniProgramTestStore) GetMiniProgram(_ context.Context, id int64) (mediaport.MiniProgram, error) {
	item, ok := store.state.items[id]
	if !ok {
		return mediaport.MiniProgram{}, ErrMiniProgramNotFound
	}
	return cloneMiniProgram(item), nil
}

func (store miniProgramTestStore) LockMiniProgram(ctx context.Context, id int64) (mediaport.MiniProgram, error) {
	if store.state.fault == "lock" {
		return mediaport.MiniProgram{}, errors.New("lock failed")
	}
	return store.GetMiniProgram(ctx, id)
}

func (store miniProgramTestStore) CreateMiniProgram(_ context.Context, item mediaport.MiniProgram) (mediaport.MiniProgram, error) {
	if store.state.fault == "create" {
		return mediaport.MiniProgram{}, errors.New("create failed")
	}
	store.state.nextID++
	item.ID = store.state.nextID
	store.state.items[item.ID] = cloneMiniProgram(item)
	return cloneMiniProgram(item), nil
}

func (store miniProgramTestStore) UpdateMiniProgram(_ context.Context, item mediaport.MiniProgram) (mediaport.MiniProgram, error) {
	if store.state.fault == "update" {
		return mediaport.MiniProgram{}, errors.New("write failure")
	}
	if _, ok := store.state.items[item.ID]; !ok {
		return mediaport.MiniProgram{}, ErrMiniProgramNotFound
	}
	store.state.items[item.ID] = cloneMiniProgram(item)
	return cloneMiniProgram(item), nil
}

func (store miniProgramTestStore) DeleteMiniProgram(_ context.Context, id int64) error {
	if store.state.fault == "delete" {
		return errors.New("delete failed")
	}
	if _, ok := store.state.items[id]; !ok {
		return ErrMiniProgramNotFound
	}
	delete(store.state.items, id)
	return nil
}

func (store miniProgramTestStore) ReserveMiniProgram(_ context.Context, reservation MiniProgramReservation) (MiniProgramReceipt, bool, error) {
	if store.state.fault == "reserve" {
		return MiniProgramReceipt{}, false, errors.New("reserve failed")
	}
	key := miniProgramReceiptKey(reservation)
	if receipt, ok := store.state.receipts[key]; ok {
		return cloneMiniProgramReceipt(receipt), false, nil
	}
	if store.state.fault == "owned-completed" {
		return MiniProgramReceipt{ID: 1, Operation: reservation.Operation, ActorScope: reservation.ActorScope, BusinessKey: reservation.BusinessKey,
			KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "completed"}, true, nil
	}
	receipt := MiniProgramReceipt{ID: int64(len(store.state.receipts) + 1), Operation: reservation.Operation, ActorScope: reservation.ActorScope, BusinessKey: reservation.BusinessKey,
		KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}
	store.state.receipts[key] = receipt
	return receipt, true, nil
}

func (store miniProgramTestStore) CompleteMiniProgram(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (MiniProgramReceipt, error) {
	if store.state.fault == "complete" {
		return MiniProgramReceipt{}, errors.New("complete failed")
	}
	for key, receipt := range store.state.receipts {
		if receipt.ID == id {
			receipt.State = "completed"
			receipt.ResultSnapshot = append(json.RawMessage{}, snapshot...)
			store.state.receipts[key] = receipt
			return cloneMiniProgramReceipt(receipt), nil
		}
	}
	return MiniProgramReceipt{}, errors.New("receipt missing")
}

func (images miniProgramTestImages) ImageExists(_ context.Context, id int64) (bool, error) {
	if images.state.fault == "image" {
		return false, errors.New("image lookup failed")
	}
	return images.state.images[id], nil
}

func (events miniProgramTestEvents) Append(_ context.Context, event eventport.Event) (eventport.EventID, error) {
	if events.state.fault == "event" {
		return 0, errors.New("event append failed")
	}
	events.state.events = append(events.state.events, event)
	return eventport.EventID(len(events.state.events)), nil
}

func (contacts miniProgramTestContacts) ListMiniProgramReferenceChannelIDs(_ context.Context, _ int64) ([]int64, error) {
	if contacts.state.fault == "contact" {
		return nil, errors.New("channel reference scan failed")
	}
	return append([]int64{}, contacts.state.channelReferences...), nil
}

func (resolver *miniProgramTestResolver) ResolveThumbnailFromCache(_ context.Context, _ mediaport.MiniProgram) (mediaport.ThumbnailCacheResolution, error) {
	resolver.calls++
	return cloneMiniProgramResolution(resolver.resolution), resolver.err
}

func newMiniProgramTestService() (*MiniProgramService, *miniProgramTestState, *miniProgramTestResolver) {
	state := &miniProgramTestState{receipts: map[string]MiniProgramReceipt{}, items: map[int64]mediaport.MiniProgram{}, images: map[int64]bool{11: true}}
	resolver := &miniProgramTestResolver{resolution: mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailResolved, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "local-cache:11", MediaID: "cache-media-11"}}
	service := NewMiniProgramServiceWithChannelReferences(miniProgramTestUOW{state}, miniProgramTestStore{state}, miniProgramTestImages{state}, miniProgramTestEvents{state}, resolver, miniProgramTestContacts{state})
	service.now = func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }
	return service, state, resolver
}

func miniProgramTestCreate(key string) mediaport.MiniProgramCreateCommand {
	thumbnailID := int64(11)
	return mediaport.MiniProgramCreateCommand{Name: "卡片", AppID: "wx-demo", PagePath: "pages/home", Title: "首页", ThumbnailImageID: &thumbnailID, Actor: 7, IdempotencyKey: key}
}

func miniProgramTestCreateWithoutResolve(key string) mediaport.MiniProgramCreateCommand {
	command := miniProgramTestCreate(key)
	disabled := false
	command.ResolveThumbMedia = &disabled
	return command
}

func TestMiniProgramCreateUpdateNoOpReplayAndConflict(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0001"))
	if err != nil || !created.Changed || created.Item.ID != 1 || len(state.events) != 1 || len(state.receipts) != 1 {
		t.Fatalf("created=%#v state=%#v err=%v", created, state, err)
	}
	name := "卡片"
	noOpKey := "miniprogram-update-key-0001"
	noOp, err := service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: 1, MiniProgramPatch: mediaport.MiniProgramPatch{Name: &name}, Actor: 7, IdempotencyKey: noOpKey})
	if err != nil || noOp.Changed || noOp.Item.Version != created.Item.Version || noOp.ThumbnailResolve == nil || len(state.events) != 1 || len(state.receipts) != 2 {
		t.Fatalf("noOp=%#v events=%#v receipts=%#v err=%v", noOp, state.events, state.receipts, err)
	}
	spacedName := " 卡片 "
	replay, err := service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: 1, MiniProgramPatch: mediaport.MiniProgramPatch{Name: &spacedName}, Actor: 7, IdempotencyKey: noOpKey})
	if err != nil || replay.Changed || replay.Item.ID != noOp.Item.ID || replay.Item.Version != noOp.Item.Version || replay.Item.Name != noOp.Item.Name || len(state.events) != 1 || len(state.receipts) != 2 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	title := "changed"
	if _, err = service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: 1, MiniProgramPatch: mediaport.MiniProgramPatch{Title: &title}, Actor: 7, IdempotencyKey: noOpKey}); !errors.Is(err, ErrMiniProgramConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	if state.items[1].Title != "首页" || len(state.events) != 1 || len(state.receipts) != 2 {
		t.Fatalf("conflict changed state=%#v", state)
	}
}

func TestMiniProgramReadsUseLegacyDefaultWithoutInventedMaximum(t *testing.T) {
	service, _, _ := newMiniProgramTestService()
	if _, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0008")); err != nil {
		t.Fatal(err)
	}
	page, err := service.List(context.Background(), mediaport.MiniProgramListQuery{})
	if err != nil || page.Limit != defaultMiniProgramLimit || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	page, err = service.List(context.Background(), mediaport.MiniProgramListQuery{Limit: 200})
	if err != nil || page.Limit != 200 || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("larger legacy page=%#v err=%v", page, err)
	}
	empty, err := service.List(context.Background(), mediaport.MiniProgramListQuery{Limit: 200, Offset: 99})
	if err != nil || empty.Total != 1 || len(empty.Items) != 0 || empty.Offset != 99 {
		t.Fatalf("offset past total is a valid empty page: %#v err=%v", empty, err)
	}
	if _, err = service.List(context.Background(), mediaport.MiniProgramListQuery{Limit: -1}); !errors.Is(err, ErrInvalidMiniProgramOperation) {
		t.Fatalf("negative list limit err=%v", err)
	}
	if _, err = service.Get(context.Background(), 999); !errors.Is(err, ErrMiniProgramNotFound) {
		t.Fatalf("missing item err=%v", err)
	}
}

func TestMiniProgramListFailsClosedForImpossibleStorePageAndSort(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	if _, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-list-01")); err != nil {
		t.Fatal(err)
	}
	state.fault = "negative-total"
	if _, err := service.List(context.Background(), mediaport.MiniProgramListQuery{}); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("negative total err=%v", err)
	}
	state.fault = "list-over-limit"
	if _, err := service.List(context.Background(), mediaport.MiniProgramListQuery{Limit: 1}); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("oversized item page err=%v", err)
	}
	state.fault = "short-total"
	if _, err := service.List(context.Background(), mediaport.MiniProgramListQuery{Limit: 1}); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("nonempty page beyond total err=%v", err)
	}
	state.fault = ""
	if _, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-list-02")); err != nil {
		t.Fatal(err)
	}
	page, err := service.List(context.Background(), mediaport.MiniProgramListQuery{})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != 2 || page.Items[1].ID != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	state.fault = "unsorted-list"
	if _, err = service.List(context.Background(), mediaport.MiniProgramListQuery{}); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("unstable sort err=%v", err)
	}
}

func TestMiniProgramCreateUpdateDefaultLocalResolveAndLegacyAliases(t *testing.T) {
	service, state, resolver := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-resolve-01"))
	if err != nil || !created.Changed || created.ThumbnailResolve == nil || created.ThumbnailResolve.Status != mediaport.ThumbnailResolved || created.Item.ThumbnailMediaID != "cache-media-11" || resolver.calls != 1 || len(state.events) != 1 || len(state.receipts) != 1 {
		t.Fatalf("created=%#v calls=%d state=%#v err=%v", created, resolver.calls, state, err)
	}
	encoded, err := json.Marshal(created.Item)
	if err != nil || !strings.Contains(string(encoded), `"appid":"wx-demo"`) || !strings.Contains(string(encoded), `"pagepath":"pages/home"`) || !strings.Contains(string(encoded), `"page_path":"pages/home"`) {
		t.Fatalf("legacy aliases json=%s err=%v", encoded, err)
	}
	if strings.Contains(string(encoded), `"app_id":`) {
		t.Fatalf("app_id is an accepted legacy input alias, not a legacy output field: %s", encoded)
	}
	title := "新的首页"
	updated, err := service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{Title: &title}, Actor: 7, IdempotencyKey: "miniprogram-update-key-resolve-01"})
	if err != nil || !updated.Changed || updated.ThumbnailResolve == nil || updated.ThumbnailResolve.Status != mediaport.ThumbnailResolved || resolver.calls != 2 || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("updated=%#v calls=%d state=%#v err=%v", updated, resolver.calls, state, err)
	}
	disabled := false
	legacyMediaID := "untrusted-client-value"
	if _, err = service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{ThumbMediaID: mediaport.OptionalString{Present: true, Value: &legacyMediaID}, ResolveThumbMedia: &disabled}, Actor: 7, IdempotencyKey: "miniprogram-update-key-resolve-02"}); !errors.Is(err, ErrInvalidMiniProgramOperation) {
		t.Fatalf("untrusted thumb media must be rejected err=%v", err)
	}
	if state.items[created.Item.ID].ThumbnailMediaID != "cache-media-11" || resolver.calls != 2 || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("untrusted thumb media mutated state=%#v calls=%d", state, resolver.calls)
	}
}

func TestMiniProgramUpdateLeafDTOKeepsNullClearAndLegacyResolverFields(t *testing.T) {
	var absent mediaport.MiniProgramPatch
	if err := json.Unmarshal([]byte(`{}`), &absent); err != nil || absent.ThumbnailImageID.Present {
		t.Fatalf("absent=%#v err=%v", absent, err)
	}
	var clear mediaport.MiniProgramPatch
	if err := json.Unmarshal([]byte(`{"app_id":"wx-alias","page_path":"pages/alias","thumb_image_id":null,"thumb_media_id":"legacy-cache","resolve_thumb_media":false}`), &clear); err != nil || !clear.ThumbnailImageID.Present || clear.ThumbnailImageID.Value != nil || clear.AppID == nil || *clear.AppID != "wx-alias" || clear.PagePath == nil || *clear.PagePath != "pages/alias" || !clear.ThumbMediaID.Present || clear.ThumbMediaID.Value == nil || *clear.ThumbMediaID.Value != "legacy-cache" || clear.ResolveThumbMedia == nil || *clear.ResolveThumbMedia {
		t.Fatalf("clear=%#v err=%v", clear, err)
	}
	var clearThumbMedia mediaport.MiniProgramPatch
	if err := json.Unmarshal([]byte(`{"thumb_media_id": null}`), &clearThumbMedia); err != nil || !clearThumbMedia.ThumbMediaID.Present || clearThumbMedia.ThumbMediaID.Value != nil {
		t.Fatalf("explicit null thumb_media_id must retain presence: %#v err=%v", clearThumbMedia, err)
	}
	var canonical mediaport.MiniProgramPatch
	if err := json.Unmarshal([]byte(`{"appid":"wx-canonical","app_id":"wx-alias","pagepath":"pages/canonical","page_path":"pages/alias"}`), &canonical); err != nil || canonical.AppID == nil || *canonical.AppID != "wx-canonical" || canonical.PagePath == nil || *canonical.PagePath != "pages/canonical" {
		t.Fatalf("canonical aliases must take precedence: %#v err=%v", canonical, err)
	}
	var set mediaport.MiniProgramPatch
	if err := json.Unmarshal([]byte(`{"thumb_image_id":11}`), &set); err != nil || !set.ThumbnailImageID.Present || set.ThumbnailImageID.Value == nil || *set.ThumbnailImageID.Value != 11 {
		t.Fatalf("set=%#v err=%v", set, err)
	}
}

func TestMiniProgramRejectsDirectThumbMediaWritesAndOnlyResolverPersistsCache(t *testing.T) {
	service, state, resolver := newMiniProgramTestService()
	unsafeMediaID := "untrusted-client-cache"
	create := miniProgramTestCreate("miniprogram-create-key-thumb-media-01")
	create.ThumbMediaID = mediaport.OptionalString{Present: true, Value: &unsafeMediaID}
	if _, err := service.Create(context.Background(), create); !errors.Is(err, ErrInvalidMiniProgramOperation) {
		t.Fatalf("direct create thumb media err=%v", err)
	}
	if len(state.items) != 0 || len(state.events) != 0 || len(state.receipts) != 0 || resolver.calls != 0 {
		t.Fatalf("direct create thumb media changed state=%#v calls=%d", state, resolver.calls)
	}
	created, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-thumb-media-02"))
	if err != nil || created.Item.ThumbnailMediaID != "cache-media-11" || resolver.calls != 1 {
		t.Fatalf("local cache create=%#v calls=%d err=%v", created, resolver.calls, err)
	}
	nullWrite := mediaport.MiniProgramPatch{ThumbMediaID: mediaport.OptionalString{Present: true}}
	if _, err = service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: nullWrite, Actor: 7, IdempotencyKey: "miniprogram-update-key-thumb-media-01"}); !errors.Is(err, ErrInvalidMiniProgramOperation) {
		t.Fatalf("direct null thumb media err=%v", err)
	}
	if state.items[created.Item.ID].ThumbnailMediaID != "cache-media-11" || len(state.events) != 1 || len(state.receipts) != 1 || resolver.calls != 1 {
		t.Fatalf("direct null thumb media changed state=%#v calls=%d", state, resolver.calls)
	}
}

func TestMiniProgramCreateResolveChoiceConflictsAndIsStoredInReceiptSnapshot(t *testing.T) {
	service, state, resolver := newMiniProgramTestService()
	key := "miniprogram-create-key-receipt-resolve-01"
	withoutResolve := miniProgramTestCreateWithoutResolve(key)
	created, err := service.Create(context.Background(), withoutResolve)
	if err != nil || created.ThumbnailResolve != nil || resolver.calls != 0 || len(state.receipts) != 1 {
		t.Fatalf("without resolve=%#v calls=%d receipts=%#v err=%v", created, resolver.calls, state.receipts, err)
	}
	for _, receipt := range state.receipts {
		var snapshot miniProgramReceiptSnapshot[mediaport.MiniProgramMutationResult]
		if err := json.Unmarshal(receipt.ResultSnapshot, &snapshot); err != nil {
			t.Fatal(err)
		}
		var command struct{ ResolveThumbMedia *bool }
		if err := json.Unmarshal(snapshot.Command, &command); err != nil || command.ResolveThumbMedia == nil || *command.ResolveThumbMedia {
			t.Fatalf("receipt command lacks false resolve choice command=%s err=%v", snapshot.Command, err)
		}
	}
	withResolve := miniProgramTestCreate(key)
	resolve := true
	withResolve.ResolveThumbMedia = &resolve
	if _, err = service.Create(context.Background(), withResolve); !errors.Is(err, ErrMiniProgramConflict) {
		t.Fatalf("resolve choice conflict err=%v", err)
	}
	if len(state.items) != 1 || len(state.events) != 1 || len(state.receipts) != 1 || resolver.calls != 0 {
		t.Fatalf("resolve conflict changed state=%#v calls=%d", state, resolver.calls)
	}
}

func TestMiniProgramUpdateAllowsEmptyNameAndRejectsEmptyTitle(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreateWithoutResolve("miniprogram-create-key-empty-name-01"))
	if err != nil {
		t.Fatal(err)
	}
	emptyName := ""
	updated, err := service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{Name: &emptyName}, Actor: 7, IdempotencyKey: "miniprogram-update-key-empty-name-01"})
	if err != nil || !updated.Changed || updated.Item.Name != "" || updated.Item.Title != "首页" || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("empty name update=%#v state=%#v err=%v", updated, state, err)
	}
	emptyTitle := ""
	if _, err = service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{Title: &emptyTitle}, Actor: 7, IdempotencyKey: "miniprogram-update-key-empty-title-01"}); !errors.Is(err, ErrInvalidMiniProgramOperation) {
		t.Fatalf("empty title err=%v", err)
	}
	if state.items[created.Item.ID].Name != "" || state.items[created.Item.ID].Title != "首页" || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("empty title mutation changed state=%#v", state)
	}
}

func TestMiniProgramUpdateAndResolveSnapshotsBindCommandFields(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreateWithoutResolve("miniprogram-create-key-binding-01"))
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	update := mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{ResolveThumbMedia: &disabled}, Actor: 7, IdempotencyKey: "miniprogram-update-key-binding-01"}
	if _, err = service.Update(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	resolve := mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-resolve-key-binding-01"}
	if _, err = service.ResolveThumbnail(context.Background(), resolve); err != nil {
		t.Fatal(err)
	}
	var sawUpdate, sawResolve bool
	for _, receipt := range state.receipts {
		switch receipt.Operation {
		case "update":
			var snapshot miniProgramReceiptSnapshot[mediaport.MiniProgramMutationResult]
			if err := json.Unmarshal(receipt.ResultSnapshot, &snapshot); err != nil || !strings.Contains(string(snapshot.Command), `"ID":1`) || !strings.Contains(string(snapshot.Command), `"resolve_thumb_media":false`) {
				t.Fatalf("mutation command binding=%s err=%v", snapshot.Command, err)
			}
			sawUpdate = receipt.ActorScope == "admin:7" && receipt.BusinessKey == "1"
		case "test-resolve":
			var snapshot miniProgramReceiptSnapshot[mediaport.MiniProgramThumbnailResolutionResult]
			if err := json.Unmarshal(receipt.ResultSnapshot, &snapshot); err != nil || !strings.Contains(string(snapshot.Command), `"ID":1`) {
				t.Fatalf("resolve command binding=%s err=%v", snapshot.Command, err)
			}
			sawResolve = receipt.ActorScope == "admin:7" && receipt.BusinessKey == "1"
		}
	}
	if !sawUpdate || !sawResolve {
		t.Fatalf("missing receipt metadata binding receipts=%#v", state.receipts)
	}
	resolveEnabled := true
	if _, err = service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{ResolveThumbMedia: &resolveEnabled}, Actor: 7, IdempotencyKey: update.IdempotencyKey}); !errors.Is(err, ErrMiniProgramConflict) {
		t.Fatalf("mutation command-field conflict err=%v", err)
	}
}

func TestMiniProgramDefaultUnknownResolutionCompletesAndReplayNeverCallsResolver(t *testing.T) {
	service, state, resolver := newMiniProgramTestService()
	resolver.resolution = mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailOutcomeUnknown, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "local-cache:11"}
	command := miniProgramTestCreate("miniprogram-create-key-unknown-01")
	first, err := service.Create(context.Background(), command)
	if err != nil || !first.Changed || first.ThumbnailResolve == nil || first.ThumbnailResolve.Status != mediaport.ThumbnailOutcomeUnknown || first.Item.ThumbnailMediaID != "" || resolver.calls != 1 || len(state.events) != 1 || len(state.receipts) != 1 {
		t.Fatalf("first=%#v calls=%d state=%#v err=%v", first, resolver.calls, state, err)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ThumbnailResolve == nil || replay.ThumbnailResolve.Status != mediaport.ThumbnailOutcomeUnknown || resolver.calls != 1 || len(state.events) != 1 || len(state.receipts) != 1 {
		t.Fatalf("replay=%#v calls=%d state=%#v err=%v", replay, resolver.calls, state, err)
	}
}

func TestMiniProgramMutationEventAndReceiptRollbackAsOneUOW(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	state.fault = "event"
	if _, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0002")); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("append error=%v", err)
	}
	if len(state.items) != 0 || len(state.events) != 0 || len(state.receipts) != 0 {
		t.Fatalf("append rollback state=%#v", state)
	}
	state.fault = "complete"
	if _, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0003")); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("complete error=%v", err)
	}
	if len(state.items) != 0 || len(state.events) != 0 || len(state.receipts) != 0 {
		t.Fatalf("complete rollback state=%#v", state)
	}
	state.fault = ""
	created, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0004"))
	if err != nil {
		t.Fatal(err)
	}
	title := "new title"
	state.fault = "event"
	if _, err = service.Update(context.Background(), mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{Title: &title}, Actor: 7, IdempotencyKey: "miniprogram-update-key-0002"}); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("write append failure=%v", err)
	}
	if state.items[created.Item.ID].Title != "首页" || len(state.events) != 1 || len(state.receipts) != 1 {
		t.Fatalf("write rollback state=%#v", state)
	}
}

func TestMiniProgramReceiptReplayBindsCommandIDAndRejectsOwnedCompleted(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	state.fault = "owned-completed"
	if _, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-receipt-01")); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("owned completed err=%v", err)
	}
	if len(state.items) != 0 || len(state.events) != 0 || len(state.receipts) != 0 {
		t.Fatalf("owned completed mutated state=%#v", state)
	}
	state.fault = ""
	created, err := service.Create(context.Background(), miniProgramTestCreateWithoutResolve("miniprogram-create-key-receipt-02"))
	if err != nil {
		t.Fatal(err)
	}
	title := "新的标题"
	command := mediaport.MiniProgramUpdateCommand{ID: created.Item.ID, MiniProgramPatch: mediaport.MiniProgramPatch{Title: &title}, Actor: 7, IdempotencyKey: "miniprogram-update-key-receipt-01"}
	_, err = service.Update(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	for key, receipt := range state.receipts {
		if receipt.Operation != "update" {
			continue
		}
		var corrupt miniProgramReceiptSnapshot[mediaport.MiniProgramMutationResult]
		if unmarshalErr := json.Unmarshal(receipt.ResultSnapshot, &corrupt); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		corrupt.Result.Item.ID++
		snapshot, marshalErr := json.Marshal(corrupt)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		receipt.ResultSnapshot = snapshot
		state.receipts[key] = receipt
	}
	if _, err = service.Update(context.Background(), command); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("cross-command replay err=%v", err)
	}
	if state.items[created.Item.ID].Title != "新的标题" || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("corrupt replay mutated state=%#v", state)
	}
}

func TestMiniProgramResolveValidatesLockedMaterialBeforeEveryOutcome(t *testing.T) {
	service, state, resolver := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreateWithoutResolve("miniprogram-create-key-lock-01"))
	if err != nil {
		t.Fatal(err)
	}
	broken := state.items[created.Item.ID]
	broken.Title = ""
	state.items[created.Item.ID] = broken
	outcomes := []mediaport.ThumbnailCacheResolution{
		{Status: mediaport.ThumbnailResolved, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "local-cache:11", MediaID: "cache-media-11"},
		{Status: mediaport.ThumbnailNotAvailable, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "local-cache:11"},
		{Status: mediaport.ThumbnailOutcomeUnknown, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "local-cache:11"},
	}
	for index, outcome := range outcomes {
		resolver.resolution = outcome
		key := "miniprogram-resolve-key-lock-0" + string(rune('1'+index))
		if _, err = service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: key}); !errors.Is(err, ErrMiniProgramUnavailable) {
			t.Fatalf("outcome=%s err=%v", outcome.Status, err)
		}
		if resolver.calls != 0 || len(state.events) != 1 || len(state.receipts) != 1 {
			t.Fatalf("outcome=%s resolver called or persisted state=%#v calls=%d", outcome.Status, state, resolver.calls)
		}
	}
}

func TestMiniProgramPhysicalDeleteHasOneEventAndReplayDoesNotDuplicate(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0005"))
	if err != nil {
		t.Fatal(err)
	}
	key := "miniprogram-delete-key-0001"
	deleted, err := service.Delete(context.Background(), mediaport.MiniProgramDeleteCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: key})
	if err != nil || !deleted.Deleted || len(state.items) != 0 || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("deleted=%#v state=%#v err=%v", deleted, state, err)
	}
	replay, err := service.Delete(context.Background(), mediaport.MiniProgramDeleteCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: key})
	if err != nil || replay != deleted || len(state.events) != 2 || len(state.receipts) != 2 {
		t.Fatalf("replay=%#v err=%v state=%#v", replay, err, state)
	}
}

func TestMiniProgramDeleteFailsClosedWhenChannelReferencesExistOrCannotBeRead(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0006"))
	if err != nil {
		t.Fatal(err)
	}
	state.channelReferences = []int64{9}
	if _, err = service.Delete(context.Background(), mediaport.MiniProgramDeleteCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-delete-key-0002"}); !errors.Is(err, ErrMiniProgramHasReferences) || len(state.items) != 1 {
		t.Fatalf("referenced delete err=%v items=%#v", err, state.items)
	}
	state.channelReferences = nil
	state.fault = "contact"
	if _, err = service.Delete(context.Background(), mediaport.MiniProgramDeleteCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-delete-key-0003"}); !errors.Is(err, ErrMiniProgramUnavailable) || len(state.items) != 1 {
		t.Fatalf("unavailable scan err=%v items=%#v", err, state.items)
	}
}

func TestMiniProgramThumbnailResolverIsLocalFailClosedAndNeverAutoRetriesUnknown(t *testing.T) {
	service, state, resolver := newMiniProgramTestService()
	created, err := service.Create(context.Background(), miniProgramTestCreateWithoutResolve("miniprogram-create-key-0006"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-resolve-key-0001"})
	if err != nil || !resolved.Changed || resolved.Item.ThumbnailMediaID != "cache-media-11" || len(state.events) != 2 || resolver.calls != 1 {
		t.Fatalf("resolved=%#v events=%#v calls=%d err=%v", resolved, state.events, resolver.calls, err)
	}
	sameCache, err := service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-resolve-key-0001b"})
	if err != nil || sameCache.Changed || sameCache.Item.ThumbnailMediaID != "cache-media-11" || len(state.events) != 2 || resolver.calls != 2 {
		t.Fatalf("same cache=%#v events=%#v calls=%d err=%v", sameCache, state.events, resolver.calls, err)
	}
	resolver.resolution = mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailOutcomeUnknown, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "local-cache:11"}
	unknownKey := "miniprogram-resolve-key-0002"
	unknown, err := service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: unknownKey})
	if err != nil || unknown.Changed || unknown.Resolution.Status != mediaport.ThumbnailOutcomeUnknown || unknown.Item.ThumbnailMediaID != "cache-media-11" || len(state.events) != 2 || resolver.calls != 3 {
		t.Fatalf("unknown=%#v events=%#v calls=%d err=%v", unknown, state.events, resolver.calls, err)
	}
	replay, err := service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: unknownKey})
	if err != nil || replay.Resolution.Status != mediaport.ThumbnailOutcomeUnknown || resolver.calls != 3 || len(state.events) != 2 {
		t.Fatalf("unknown replay=%#v calls=%d events=%d err=%v", replay, resolver.calls, len(state.events), err)
	}
	resolver.resolution = mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailResolved, CacheOwner: mediaport.ThumbnailCacheOwner, CacheReceipt: "external", MediaID: "provider-media", SideEffectExecuted: true, RealExternalCallExecuted: true}
	if _, err = service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-resolve-key-0003"}); !errors.Is(err, ErrMiniProgramUnsafeResolver) {
		t.Fatalf("unsafe resolver err=%v", err)
	}
	if state.items[created.Item.ID].ThumbnailMediaID != "cache-media-11" || len(state.events) != 2 || len(state.receipts) != 4 {
		t.Fatalf("unsafe resolver changed local fact state=%#v", state)
	}
	resolver.resolution = mediaport.ThumbnailCacheResolution{Status: mediaport.ThumbnailResolved, CacheOwner: "unowned-cache", CacheReceipt: "other", MediaID: "untrusted"}
	if _, err = service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-resolve-key-0005"}); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("unowned resolver err=%v", err)
	}
	if state.items[created.Item.ID].ThumbnailMediaID != "cache-media-11" || len(state.events) != 2 || len(state.receipts) != 4 {
		t.Fatalf("unowned resolver changed local fact state=%#v", state)
	}
	resolver.err = errors.New("cache read failed")
	if _, err = service.ResolveThumbnail(context.Background(), mediaport.MiniProgramResolveThumbnailCommand{ID: created.Item.ID, Actor: 7, IdempotencyKey: "miniprogram-resolve-key-0004"}); !errors.Is(err, ErrMiniProgramUnavailable) {
		t.Fatalf("resolver failure err=%v", err)
	}
	if state.items[created.Item.ID].ThumbnailMediaID != "cache-media-11" || len(state.events) != 2 || len(state.receipts) != 4 {
		t.Fatalf("resolver failure changed local fact state=%#v", state)
	}
}

func TestMiniProgramConcurrentCreateReplayConvergesToOneFactEventAndReceipt(t *testing.T) {
	service, state, _ := newMiniProgramTestService()
	const callers = 12
	var group sync.WaitGroup
	errorsOut := make(chan error, callers)
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.Create(context.Background(), miniProgramTestCreate("miniprogram-create-key-0007"))
			errorsOut <- err
		}()
	}
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(state.items) != 1 || len(state.events) != 1 || len(state.receipts) != 1 {
		t.Fatalf("concurrent state=%#v", state)
	}
}

func miniProgramReceiptKey(reservation MiniProgramReservation) string {
	return reservation.Operation + ":" + reservation.ActorScope + ":" + reservation.BusinessKey + ":" + string(reservation.KeyDigest[:])
}

func cloneMiniProgramTestState(source *miniProgramTestState) *miniProgramTestState {
	copy := &miniProgramTestState{receipts: make(map[string]MiniProgramReceipt, len(source.receipts)), items: make(map[int64]mediaport.MiniProgram, len(source.items)), images: source.images, events: append([]eventport.Event{}, source.events...), nextID: source.nextID, fault: source.fault}
	for key, receipt := range source.receipts {
		copy.receipts[key] = cloneMiniProgramReceipt(receipt)
	}
	for id, item := range source.items {
		copy.items[id] = cloneMiniProgram(item)
	}
	return copy
}

func restoreMiniProgramTestState(target, source *miniProgramTestState) {
	target.receipts, target.items, target.images, target.events, target.nextID, target.fault = source.receipts, source.items, source.images, source.events, source.nextID, source.fault
}

func cloneMiniProgramReceipt(receipt MiniProgramReceipt) MiniProgramReceipt {
	receipt.ResultSnapshot = append(json.RawMessage{}, receipt.ResultSnapshot...)
	return receipt
}

func cloneMiniProgram(item mediaport.MiniProgram) mediaport.MiniProgram {
	if item.ThumbnailImageID != nil {
		copy := *item.ThumbnailImageID
		item.ThumbnailImageID = &copy
	}
	if item.ThumbnailMediaExpiresAt != nil {
		copy := *item.ThumbnailMediaExpiresAt
		item.ThumbnailMediaExpiresAt = &copy
	}
	return item
}

func cloneMiniProgramResolution(resolution mediaport.ThumbnailCacheResolution) mediaport.ThumbnailCacheResolution {
	if resolution.ExpiresAt != nil {
		copy := *resolution.ExpiresAt
		resolution.ExpiresAt = &copy
	}
	return resolution
}
