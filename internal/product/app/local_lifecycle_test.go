package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"testing"
	"time"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestLocalProductLifecycleEnableDisableCASIdempotencyAndNoop(t *testing.T) {
	service, store, events := newLocalProductLifecycleFixture()
	product := seedLocalProduct(t, store, 7, productport.LocalProductDraft, false, []string{"https://local.invalid/a.png"})

	enabledCommand := productport.SetLocalProductEnabledCommand{
		ID: product.ID, ExpectedVersion: product.Version, Enabled: true, Actor: 41, IdempotencyKey: "wechat-enable-key-0001",
	}
	enabled, err := service.SetLocalProductEnabled(context.Background(), enabledCommand)
	if err != nil || enabled.Lifecycle != productport.LocalProductEnabled || !enabled.Enabled || enabled.Version != product.Version+1 {
		t.Fatalf("enabled=%+v err=%v", enabled, err)
	}
	if store.updateCalls != 1 || len(events.events) != 1 {
		t.Fatalf("enable calls/events=%d/%d", store.updateCalls, len(events.events))
	}

	replayed, err := service.SetLocalProductEnabled(context.Background(), enabledCommand)
	if err != nil || !reflect.DeepEqual(replayed, enabled) {
		t.Fatalf("replay=%+v err=%v want=%+v", replayed, err, enabled)
	}
	if store.updateCalls != 1 || len(events.events) != 1 {
		t.Fatalf("replay mutated calls/events=%d/%d", store.updateCalls, len(events.events))
	}

	disabled, err := service.SetLocalProductEnabled(context.Background(), productport.SetLocalProductEnabledCommand{
		ID: enabled.ID, ExpectedVersion: enabled.Version, Enabled: false, Actor: 41, IdempotencyKey: "wechat-disable-key-0001",
	})
	if err != nil || disabled.Lifecycle != productport.LocalProductDisabled || disabled.Enabled || disabled.Version != enabled.Version+1 {
		t.Fatalf("disabled=%+v err=%v", disabled, err)
	}
	updatesBefore, eventsBefore := store.updateCalls, len(events.events)
	noOp, err := service.SetLocalProductEnabled(context.Background(), productport.SetLocalProductEnabledCommand{
		ID: disabled.ID, ExpectedVersion: disabled.Version, Enabled: false, Actor: 41, IdempotencyKey: "wechat-disable-noop-01",
	})
	if err != nil || !reflect.DeepEqual(noOp, disabled) {
		t.Fatalf("noop=%+v err=%v want=%+v", noOp, err, disabled)
	}
	if store.updateCalls != updatesBefore || len(events.events) != eventsBefore {
		t.Fatalf("noop mutated calls/events=%d/%d want=%d/%d", store.updateCalls, len(events.events), updatesBefore, eventsBefore)
	}

	if _, err = service.SetLocalProductEnabled(context.Background(), productport.SetLocalProductEnabledCommand{
		ID: disabled.ID, ExpectedVersion: enabled.Version, Enabled: true, Actor: 41, IdempotencyKey: "wechat-stale-enable-01",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS error=%v", err)
	}
	if _, err = service.SetLocalProductEnabled(context.Background(), productport.SetLocalProductEnabledCommand{
		ID: disabled.ID, ExpectedVersion: disabled.Version, Enabled: true, Actor: 41, IdempotencyKey: "wechat-disable-noop-01",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-key payload conflict=%v", err)
	}
}

func TestLocalProductLifecycleArchiveRetainsReferencesAndBecomesTerminal(t *testing.T) {
	service, store, events := newLocalProductLifecycleFixture()
	product := seedLocalProduct(t, store, 41, productport.LocalProductEnabled, true, []string{"https://local.invalid/referenced.png"})
	// These are the Product-owned test stand-ins for order and coupon history.
	// Archive must retain their Product ID even though it closes new discovery.
	store.references[product.ID] = true
	store.couponTargets[product.ID] = true

	command := productport.ArchiveLocalProductCommand{
		ID: product.ID, ExpectedVersion: product.Version, Actor: 41, IdempotencyKey: "wechat-archive-key-0001",
	}
	archived, err := service.ArchiveLocalProduct(context.Background(), command)
	if err != nil || archived.ID != product.ID || archived.Lifecycle != productport.LocalProductArchived || archived.Enabled || archived.Version != product.Version+1 {
		t.Fatalf("archive=%+v err=%v", archived, err)
	}
	persisted, ok := store.products[product.ID]
	if !ok || persisted.ID != product.ID || !store.references[product.ID] || !store.couponTargets[product.ID] {
		t.Fatalf("archive lost retained Product/history facts: product=%+v order=%v coupon=%v", persisted, store.references[product.ID], store.couponTargets[product.ID])
	}
	if store.updateCalls != 1 || len(events.events) != 1 || events.events[0].Type != productport.EventProductUpdated {
		t.Fatalf("archive writes/events=%d/%d %+v", store.updateCalls, len(events.events), events.events)
	}

	replayed, err := service.ArchiveLocalProduct(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replayed, archived) || store.updateCalls != 1 || len(events.events) != 1 {
		t.Fatalf("archive replay=%+v err=%v writes/events=%d/%d", replayed, err, store.updateCalls, len(events.events))
	}
	if _, err = service.ArchiveLocalProduct(context.Background(), productport.ArchiveLocalProductCommand{
		ID: product.ID, ExpectedVersion: product.Version, Actor: 41, IdempotencyKey: "wechat-archive-stale-001",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale archive error=%v", err)
	}
	if store.updateCalls != 1 || len(events.events) != 1 {
		t.Fatalf("stale archive wrote product/events=%d/%d", store.updateCalls, len(events.events))
	}

	if _, err = service.SetLocalProductEnabled(context.Background(), productport.SetLocalProductEnabledCommand{
		ID: archived.ID, ExpectedVersion: archived.Version, Enabled: true, Actor: 41, IdempotencyKey: "wechat-archive-enable-001",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived enable error=%v", err)
	}
	if _, err = service.CopyLocalProduct(context.Background(), productport.CopyLocalProductCommand{
		ID: archived.ID, ExpectedVersion: archived.Version, Actor: 41, IdempotencyKey: "wechat-archive-copy-0001",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived copy error=%v", err)
	}
	if _, err = service.ShareLocalProduct(context.Background(), archived.ID); !errors.Is(err, ErrLocalProductNotEnabled) {
		t.Fatalf("archived share error=%v", err)
	}
	if store.updateCalls != 1 || store.createCalls != 0 || len(events.events) != 1 {
		t.Fatalf("terminal actions mutated archive writes/create/events=%d/%d/%d", store.updateCalls, store.createCalls, len(events.events))
	}
}

func TestLocalProductLifecycleCopyRetainsTypedBodyAndImagesButResetsDraft(t *testing.T) {
	service, store, events := newLocalProductLifecycleFixture()
	source := seedLocalProduct(t, store, 11, productport.LocalProductDisabled, false, []string{"https://local.invalid/one.png", "https://local.invalid/two.png"})

	command := productport.CopyLocalProductCommand{ID: source.ID, ExpectedVersion: source.Version, Actor: 11, IdempotencyKey: "wechat-copy-key-000001"}
	copied, err := service.CopyLocalProduct(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if copied.ID == source.ID || copied.Version != 1 || copied.Lifecycle != productport.LocalProductDraft || copied.Enabled ||
		copied.ProductCode == source.ProductCode || copied.Name == source.Name || copied.Description != source.Description ||
		copied.PriceMinor != source.PriceMinor || copied.Currency != source.Currency || copied.StockQuantity != source.StockQuantity ||
		fmt.Sprint(copied.Images) != fmt.Sprint(source.Images) {
		t.Fatalf("copy=%+v source=%+v", copied, source)
	}
	if store.createCalls != 1 || len(events.events) != 1 || events.events[0].Type != productport.EventProductCreated {
		t.Fatalf("copy calls/event=%d/%d %+v", store.createCalls, len(events.events), events.events)
	}

	replayed, err := service.CopyLocalProduct(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replayed, copied) {
		t.Fatalf("copy replay=%+v err=%v want=%+v", replayed, err, copied)
	}
	if store.createCalls != 1 || len(events.events) != 1 {
		t.Fatalf("copy replay mutated calls/events=%d/%d", store.createCalls, len(events.events))
	}
	if got := store.products[source.ID]; got.Version != source.Version || localProductLifecycleForTest(got) != source.Lifecycle {
		t.Fatalf("source changed=%+v", got)
	}
}

func TestLocalProductLifecycleDeleteIsDraftOnlyAndReferenceSafe(t *testing.T) {
	service, store, events := newLocalProductLifecycleFixture()
	draft := seedLocalProduct(t, store, 21, productport.LocalProductDraft, false, nil)
	result, err := service.DeleteLocalProduct(context.Background(), productport.DeleteLocalProductCommand{
		ID: draft.ID, ExpectedVersion: draft.Version, Actor: 21, IdempotencyKey: "wechat-delete-key-0001",
	})
	if err != nil || !result.Deleted || result.ProductID != draft.ID {
		t.Fatalf("delete=%+v err=%v", result, err)
	}
	if _, ok := store.products[draft.ID]; ok || len(events.events) != 1 || events.events[0].Type != productport.EventProductUpdated {
		t.Fatalf("deleted product/events remain=%v/%d", store.products[draft.ID], len(events.events))
	}
	replayed, err := service.DeleteLocalProduct(context.Background(), productport.DeleteLocalProductCommand{
		ID: draft.ID, ExpectedVersion: draft.Version, Actor: 21, IdempotencyKey: "wechat-delete-key-0001",
	})
	if err != nil || replayed != result {
		t.Fatalf("delete replay=%+v err=%v want=%+v", replayed, err, result)
	}

	referenced := seedLocalProduct(t, store, 22, productport.LocalProductDraft, false, nil)
	store.references[referenced.ID] = true
	if _, err = service.DeleteLocalProduct(context.Background(), productport.DeleteLocalProductCommand{
		ID: referenced.ID, ExpectedVersion: referenced.Version, Actor: 22, IdempotencyKey: "wechat-delete-ref-0001",
	}); !errors.Is(err, ErrLocalProductDeleteNotAllowed) || !errors.Is(err, ErrConflict) {
		t.Fatalf("referenced delete error=%v", err)
	}
	if _, ok := store.products[referenced.ID]; !ok {
		t.Fatal("referenced draft was removed")
	}

	couponTargeted := seedLocalProduct(t, store, 24, productport.LocalProductDraft, false, nil)
	store.couponTargets[couponTargeted.ID] = true
	if _, err = service.DeleteLocalProduct(context.Background(), productport.DeleteLocalProductCommand{
		ID: couponTargeted.ID, ExpectedVersion: couponTargeted.Version, Actor: 24, IdempotencyKey: "wechat-delete-coupon-target-01",
	}); !errors.Is(err, ErrLocalProductDeleteNotAllowed) || !errors.Is(err, ErrConflict) {
		t.Fatalf("coupon-targeted delete error=%v", err)
	}
	if _, ok := store.products[couponTargeted.ID]; !ok {
		t.Fatal("coupon-targeted draft was removed")
	}

	disabled := seedLocalProduct(t, store, 23, productport.LocalProductDisabled, false, nil)
	if _, err = service.DeleteLocalProduct(context.Background(), productport.DeleteLocalProductCommand{
		ID: disabled.ID, ExpectedVersion: disabled.Version, Actor: 23, IdempotencyKey: "wechat-delete-disabled-01",
	}); !errors.Is(err, ErrLocalProductDeleteNotAllowed) || !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled delete error=%v", err)
	}
	if _, ok := store.products[disabled.ID]; !ok {
		t.Fatal("disabled product was removed")
	}
}

func TestLocalProductLifecycleShareUsesCanonicalPublicRouteOnlyWhenEnabled(t *testing.T) {
	service, store, _ := newLocalProductLifecycleFixture()
	product := seedLocalProduct(t, store, 31, productport.LocalProductEnabled, true, nil)
	share, err := service.ShareLocalProduct(context.Background(), product.ID)
	if err != nil || share.ProductID != product.ID || share.ProductCode != product.ProductCode || share.Lifecycle != productport.LocalProductEnabled ||
		!share.Available || share.Reason != "" || share.PurchaseURL != "/p/"+url.PathEscape(product.ProductCode) || share.QRCodeURL != "" {
		t.Fatalf("share=%+v err=%v", share, err)
	}
	disabled := seedLocalProduct(t, store, 32, productport.LocalProductDisabled, false, nil)
	if _, err = service.ShareLocalProduct(context.Background(), disabled.ID); !errors.Is(err, ErrLocalProductNotEnabled) {
		t.Fatalf("disabled share error=%v", err)
	}
	if _, err = service.ShareLocalProduct(context.Background(), 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing share error=%v", err)
	}

	invalid := store.products[product.ID]
	invalid.ID = 33
	invalid.LegacyAdminProjection = json.RawMessage(`{"schema_version":1,"status":"unknown","enabled":false}`)
	store.products[invalid.ID] = invalid
	if _, err = service.ShareLocalProduct(context.Background(), invalid.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid projection share error=%v", err)
	}
}

func TestLocalProductLifecycleRejectsInvalidCommandsAndDependencySurface(t *testing.T) {
	service, _, _ := newLocalProductLifecycleFixture()
	for name, command := range map[string]productport.SetLocalProductEnabledCommand{
		"zero product": {ExpectedVersion: 1, Enabled: true, Actor: 1, IdempotencyKey: "wechat-invalid-key-01"},
		"zero version": {ID: 1, Enabled: true, Actor: 1, IdempotencyKey: "wechat-invalid-key-02"},
		"short key":    {ID: 1, ExpectedVersion: 1, Enabled: true, Actor: 1, IdempotencyKey: "short"},
		"zero actor":   {ID: 1, ExpectedVersion: 1, Enabled: true, IdempotencyKey: "wechat-invalid-key-03"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.SetLocalProductEnabled(context.Background(), command); !errors.Is(err, ErrInvalidProduct) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	var nilService *LocalProductLifecycleService
	if _, err := nilService.ShareLocalProduct(context.Background(), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil service share=%v", err)
	}
}

func seedLocalProduct(t *testing.T, store *localProductLifecycleTestStore, actor int64, lifecycle productport.LocalProductLifecycle, enabled bool, images []string) productport.LocalProduct {
	t.Helper()
	projection, err := localProductProjectionForLifecycle(nil, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	id := store.nextProductID
	store.nextProductID++
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC).Add(time.Duration(id) * time.Minute)
	product := productport.Product{
		ID: id, ProductCode: fmt.Sprintf("wechat-local-%d", id), Name: fmt.Sprintf("商品-%d", id), Description: "本地商品",
		PriceMinor: 9900, Currency: "CNY", StockQuantity: 8, Images: append([]string(nil), images...), CreatedBy: actor,
		CreatedAt: now, UpdatedAt: now, Version: 1, LegacyAdminProjection: projection,
	}
	if (lifecycle == productport.LocalProductEnabled) != enabled {
		t.Fatalf("seed lifecycle/enabled mismatch=%s/%v", lifecycle, enabled)
	}
	store.products[id] = cloneLocalLifecycleProduct(product)
	return productport.LocalProduct{
		ID: product.ID, ProductCode: product.ProductCode, Name: product.Name, Description: product.Description,
		PriceMinor: product.PriceMinor, Currency: product.Currency, StockQuantity: product.StockQuantity, Images: append([]string(nil), product.Images...),
		CreatedBy: product.CreatedBy, CreatedAt: product.CreatedAt, UpdatedAt: product.UpdatedAt, Lifecycle: lifecycle, Enabled: enabled, Version: product.Version,
	}
}

func newLocalProductLifecycleFixture() (*LocalProductLifecycleService, *localProductLifecycleTestStore, *localProductLifecycleTestEvents) {
	store := &localProductLifecycleTestStore{products: map[productport.ID]productport.Product{}, receipts: map[string]Receipt{}, references: map[productport.ID]bool{}, couponTargets: map[productport.ID]bool{}, nextProductID: 1, nextReceiptID: 1}
	events := &localProductLifecycleTestEvents{}
	service := NewLocalProductLifecycleService(&localProductLifecycleTestUOW{}, store, events)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	var tick time.Duration
	service.now = func() time.Time {
		value := base.Add(tick)
		tick += time.Second
		return value
	}
	return service, store, events
}

type localProductLifecycleTestUOW struct{}

func (*localProductLifecycleTestUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type localProductLifecycleTestStore struct {
	products      map[productport.ID]productport.Product
	receipts      map[string]Receipt
	references    map[productport.ID]bool
	couponTargets map[productport.ID]bool
	nextProductID productport.ID
	nextReceiptID int64
	createCalls   int
	updateCalls   int
}

func (store *localProductLifecycleTestStore) Get(_ context.Context, id productport.ID) (productport.Product, error) {
	product, ok := store.products[id]
	if !ok {
		return productport.Product{}, ErrNotFound
	}
	return cloneLocalLifecycleProduct(product), nil
}

func (store *localProductLifecycleTestStore) GetForUpdate(ctx context.Context, id productport.ID) (productport.Product, error) {
	return store.Get(ctx, id)
}

func (store *localProductLifecycleTestStore) Create(_ context.Context, command productport.CreateCommand, now time.Time) (productport.Product, error) {
	store.createCalls++
	for _, product := range store.products {
		if product.ProductCode == command.ProductCode {
			return productport.Product{}, ErrConflict
		}
	}
	id := store.nextProductID
	store.nextProductID++
	product := productport.Product{ID: id, ProductCode: command.ProductCode, Name: command.Name, Description: command.Description, PriceMinor: command.PriceMinor, Currency: command.Currency, StockQuantity: command.StockQuantity, Images: append([]string(nil), command.Images...), CreatedBy: command.Actor, CreatedAt: now, UpdatedAt: now, Version: 1, LegacyAdminProjection: append([]byte(nil), command.LegacyAdminProjection...)}
	store.products[id] = cloneLocalLifecycleProduct(product)
	return product, nil
}

func (store *localProductLifecycleTestStore) UpdateLocalProductLifecycle(_ context.Context, command LocalProductLifecycleStoreUpdate, now time.Time) (productport.Product, error) {
	store.updateCalls++
	product, ok := store.products[command.ID]
	if !ok {
		return productport.Product{}, ErrNotFound
	}
	if product.Version != command.ExpectedVersion {
		return productport.Product{}, ErrConflict
	}
	product.LegacyAdminProjection = append([]byte(nil), command.LegacyAdminProjection...)
	product.Version++
	product.UpdatedAt = now
	store.products[command.ID] = cloneLocalLifecycleProduct(product)
	return product, nil
}

func (store *localProductLifecycleTestStore) DeleteLocalProductIfSafe(_ context.Context, id productport.ID, expectedVersion int64) (bool, error) {
	product, ok := store.products[id]
	if !ok {
		return false, ErrNotFound
	}
	if product.Version != expectedVersion {
		return false, ErrConflict
	}
	if store.references[id] || store.couponTargets[id] {
		return false, nil
	}
	delete(store.products, id)
	return true, nil
}

func (store *localProductLifecycleTestStore) Reserve(_ context.Context, reservation Reservation) (Receipt, bool, error) {
	key := localProductLifecycleReceiptKey(reservation.Operation, reservation.ActorScope, reservation.KeyDigest)
	if receipt, ok := store.receipts[key]; ok {
		return cloneLocalLifecycleReceipt(receipt), false, nil
	}
	receipt := Receipt{ID: store.nextReceiptID, Operation: reservation.Operation, ActorScope: reservation.ActorScope, KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}
	store.nextReceiptID++
	store.receipts[key] = receipt
	return receipt, true, nil
}

func (store *localProductLifecycleTestStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	for key, receipt := range store.receipts {
		if receipt.ID != id || receipt.State != "in_progress" {
			continue
		}
		receipt.State = "completed"
		receipt.ResultSnapshot = append([]byte(nil), snapshot...)
		store.receipts[key] = receipt
		return cloneLocalLifecycleReceipt(receipt), nil
	}
	return Receipt{}, ErrUnavailable
}

func (store *localProductLifecycleTestStore) LifecycleForTest(id productport.ID) productport.LocalProductLifecycle {
	product, ok := store.products[id]
	if !ok {
		return ""
	}
	lifecycle, _, _ := localProductLifecycleFromProjection(product.LegacyAdminProjection)
	return lifecycle
}

func (store *localProductLifecycleTestStore) productsOrdered() []productport.ID {
	ids := make([]productport.ID, 0, len(store.products))
	for id := range store.products {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

type localProductLifecycleTestEvents struct {
	events []productport.Event
}

func (events *localProductLifecycleTestEvents) Append(_ context.Context, event productport.Event) (productport.EventID, error) {
	events.events = append(events.events, event)
	return productport.EventID(len(events.events)), nil
}

func localProductLifecycleReceiptKey(operation, actorScope string, digest [32]byte) string {
	return fmt.Sprintf("%s|%s|%x", operation, actorScope, digest)
}

func cloneLocalLifecycleProduct(product productport.Product) productport.Product {
	product.Images = append([]string(nil), product.Images...)
	product.LegacyAdminProjection = append([]byte(nil), product.LegacyAdminProjection...)
	return product
}

func cloneLocalLifecycleReceipt(receipt Receipt) Receipt {
	receipt.ResultSnapshot = append([]byte(nil), receipt.ResultSnapshot...)
	return receipt
}

func localProductLifecycleForTest(product productport.Product) productport.LocalProductLifecycle {
	lifecycle, _, _ := localProductLifecycleFromProjection(product.LegacyAdminProjection)
	return lifecycle
}
