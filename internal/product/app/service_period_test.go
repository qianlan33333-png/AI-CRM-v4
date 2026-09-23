package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestServicePeriodServiceDependencySurfaceIsLocalOnly(t *testing.T) {
	typeOfService := reflect.TypeOf(ServicePeriodService{})
	want := map[string]bool{"uow": true, "store": true, "events": true, "policy": true, "now": true}
	if typeOfService.NumField() != len(want) {
		t.Fatalf("service dependency fields=%d want=%d", typeOfService.NumField(), len(want))
	}
	for index := 0; index < typeOfService.NumField(); index++ {
		field := typeOfService.Field(index)
		if !want[field.Name] {
			t.Fatalf("unexpected external dependency field=%s type=%s", field.Name, field.Type)
		}
	}
}

func TestServicePeriodCreateReplayAndPayloadConflict(t *testing.T) {
	service, store, events := newServicePeriodFixture()
	command := productport.CreateServicePeriodProductCommand{
		ProductCode:    "period-basic",
		Name:           "周期服务",
		Description:    "local only",
		PriceMinor:     19900,
		Currency:       "cny",
		DurationDays:   31,
		StockQuantity:  8,
		Actor:          41,
		IdempotencyKey: "period-create-key-0001",
	}

	created, err := service.CreateServicePeriodProduct(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if created.Lifecycle != productport.ServicePeriodDraft || created.Enabled || created.Archived || created.Version != 1 || created.Currency != "CNY" {
		t.Fatalf("created=%+v", created)
	}
	replayed, err := service.CreateServicePeriodProduct(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replayed, created) {
		t.Fatalf("replayed=%+v err=%v want=%+v", replayed, err, created)
	}
	if store.createCalls != 1 || len(events.events) != 1 {
		t.Fatalf("create calls/events=%d/%d, want 1/1", store.createCalls, len(events.events))
	}

	command.PriceMinor++
	if _, err = service.CreateServicePeriodProduct(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed-payload replay error=%v, want conflict", err)
	}
	if store.createCalls != 1 || len(events.events) != 1 {
		t.Fatalf("conflict mutated calls/events=%d/%d", store.createCalls, len(events.events))
	}
}

func TestServicePeriodCASCopyEnableDisableArchiveAndReferenceRetention(t *testing.T) {
	service, store, events := newServicePeriodFixture()
	created := mustCreateServicePeriod(t, service, "period-lifecycle", 52, "period-create-key-0002")
	store.references[created.ServiceProductID] = 2

	_, err := service.UpdateServicePeriodProduct(context.Background(), productport.UpdateServicePeriodProductCommand{
		ID:              created.ServiceProductID,
		ExpectedVersion: created.Version + 1,
		Name:            "stale",
		Description:     "stale",
		PriceMinor:      1,
		Currency:        "CNY",
		DurationDays:    31,
		StockQuantity:   1,
		Actor:           52,
		IdempotencyKey:  "period-update-stale-01",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error=%v", err)
	}

	updated, err := service.UpdateServicePeriodProduct(context.Background(), productport.UpdateServicePeriodProductCommand{
		ID:              created.ServiceProductID,
		ExpectedVersion: created.Version,
		Name:            "周期服务已更新",
		Description:     "still local only",
		PriceMinor:      29900,
		Currency:        "cny",
		DurationDays:    62,
		StockQuantity:   9,
		Images:          []string{"https://cdn.example.test/service-period.png"},
		AdminProjection: json.RawMessage(`{"schema_version":1,"status":"service_period_draft","enabled":false,"buy_button_text":"立即订阅","require_mobile":true}`),
		Actor:           52,
		IdempotencyKey:  "period-update-key-0001",
	})
	if err != nil || updated.Version != 2 || updated.Name != "周期服务已更新" || updated.Lifecycle != productport.ServicePeriodDraft || len(updated.Images) != 1 || updated.Images[0] != "https://cdn.example.test/service-period.png" || !strings.Contains(string(updated.AdminProjection), `"buy_button_text":"立即订阅"`) {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}

	enabled, err := service.SetServicePeriodProductEnabled(context.Background(), productport.SetServicePeriodProductEnabledCommand{
		ID:              updated.ServiceProductID,
		ExpectedVersion: updated.Version,
		Enabled:         true,
		Actor:           52,
		IdempotencyKey:  "enable-enable-01",
	})
	if err != nil || !enabled.Enabled || enabled.Archived || enabled.Lifecycle != productport.ServicePeriodEnabled || enabled.Version != 3 {
		t.Fatalf("enabled=%+v err=%v", enabled, err)
	}

	disabled, err := service.SetServicePeriodProductEnabled(context.Background(), productport.SetServicePeriodProductEnabledCommand{
		ID:              enabled.ServiceProductID,
		ExpectedVersion: enabled.Version,
		Enabled:         false,
		Actor:           52,
		IdempotencyKey:  "disable-disable-1",
	})
	if err != nil || disabled.Enabled || disabled.Lifecycle != productport.ServicePeriodDisabled || disabled.Version != 4 {
		t.Fatalf("disabled=%+v err=%v", disabled, err)
	}

	copied, err := service.CopyServicePeriodProduct(context.Background(), productport.CopyServicePeriodProductCommand{
		ID:              disabled.ServiceProductID,
		ExpectedVersion: disabled.Version,
		Actor:           52,
		IdempotencyKey:  "period-copy-key-000001",
	})
	if err != nil || copied.ServiceProductID == disabled.ServiceProductID || copied.Version != 1 || copied.Lifecycle != productport.ServicePeriodDraft || copied.Enabled || copied.Archived {
		t.Fatalf("copied=%+v err=%v", copied, err)
	}
	if copied.PriceMinor != disabled.PriceMinor || copied.StockQuantity != disabled.StockQuantity || copied.ProductCode == disabled.ProductCode || !reflect.DeepEqual(copied.Images, disabled.Images) || !strings.Contains(string(copied.AdminProjection), `"buy_button_text":"立即订阅"`) {
		t.Fatalf("copy did not retain stable local Product facts: source=%+v copy=%+v", disabled, copied)
	}
	replayedCopy, err := service.CopyServicePeriodProduct(context.Background(), productport.CopyServicePeriodProductCommand{
		ID:              disabled.ServiceProductID,
		ExpectedVersion: disabled.Version,
		Actor:           52,
		IdempotencyKey:  "period-copy-key-000001",
	})
	if err != nil || !reflect.DeepEqual(replayedCopy, copied) {
		t.Fatalf("copy replay=%+v err=%v want=%+v", replayedCopy, err, copied)
	}

	archived, err := service.ArchiveServicePeriodProduct(context.Background(), productport.ArchiveServicePeriodProductCommand{
		ID:              disabled.ServiceProductID,
		ExpectedVersion: disabled.Version,
		Actor:           52,
		IdempotencyKey:  "period-archive-key-01",
	})
	if err != nil || !archived.Archived || archived.Enabled || archived.Lifecycle != productport.ServicePeriodArchived || archived.Version != 5 {
		t.Fatalf("archived=%+v err=%v", archived, err)
	}
	if _, exists := store.products[archived.ServiceProductID]; !exists {
		t.Fatal("archive physically removed the authoritative Product")
	}
	if got := store.references[archived.ServiceProductID]; got != 2 {
		t.Fatalf("archive changed local reference facts: got=%d want=2", got)
	}
	if len(store.products) != 2 {
		t.Fatalf("products after copy+archive=%d, want 2", len(store.products))
	}
	if len(events.events) != 6 { // create, update, enable, disable, copy, archive
		t.Fatalf("events=%d want=6", len(events.events))
	}
	for _, event := range events.events {
		if event.Type != productport.EventProductCreated && event.Type != productport.EventProductUpdated {
			t.Fatalf("unexpected local event type=%q", event.Type)
		}
	}
}

func TestServicePeriodNoOpLifecycleStillCompletesReceiptWithoutVersionBump(t *testing.T) {
	service, store, events := newServicePeriodFixture()
	created := mustCreateServicePeriod(t, service, "period-noop", 63, "period-create-key-0003")

	disabled, err := service.SetServicePeriodProductEnabled(context.Background(), productport.SetServicePeriodProductEnabledCommand{
		ID:              created.ServiceProductID,
		ExpectedVersion: created.Version,
		Enabled:         false,
		Actor:           63,
		IdempotencyKey:  "period-disable-first-01",
	})
	if err != nil || disabled.Version != created.Version+1 || disabled.Lifecycle != productport.ServicePeriodDisabled {
		t.Fatalf("first disable result=%+v err=%v", disabled, err)
	}
	updatesBefore := store.updateCalls
	eventsBefore := len(events.events)
	result, err := service.SetServicePeriodProductEnabled(context.Background(), productport.SetServicePeriodProductEnabledCommand{
		ID:              disabled.ServiceProductID,
		ExpectedVersion: disabled.Version,
		Enabled:         false,
		Actor:           63,
		IdempotencyKey:  "noop-noop-noop-01",
	})
	if err != nil || result.Version != disabled.Version || result.Lifecycle != productport.ServicePeriodDisabled {
		t.Fatalf("no-op result=%+v err=%v", result, err)
	}
	if store.updateCalls != updatesBefore || len(events.events) != eventsBefore {
		t.Fatalf("no-op update calls/events=%d/%d want=%d/%d", store.updateCalls, len(events.events), updatesBefore, eventsBefore)
	}
	if !store.hasCompletedReceipt("update", servicePeriodActorScope(63), "noop-noop-noop-01") {
		t.Fatal("no-op lifecycle receipt was not completed")
	}
}

func TestServicePeriodListBoundsAndStablePagination(t *testing.T) {
	service, _, _ := newServicePeriodFixture()
	for index := 0; index < 3; index++ {
		mustCreateServicePeriod(t, service, fmt.Sprintf("period-list-%d", index), 74, fmt.Sprintf("period-list-create-%02d-key", index))
	}
	page, err := service.ListServicePeriodProducts(context.Background(), 2, 1)
	if err != nil || page.Total != 3 || page.Limit != 2 || page.Offset != 1 || len(page.Items) != 2 || page.Items[0].ServiceProductID >= page.Items[1].ServiceProductID {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err = service.ListServicePeriodProducts(context.Background(), MaximumLimit+1, 0); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("over-limit error=%v", err)
	}
	if _, err = service.ListServicePeriodProducts(context.Background(), 1, MaximumLegacyOffset+1); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("over-offset error=%v", err)
	}
}

func TestServicePeriodDatabaseUnknownOutcomeIsNotRetried(t *testing.T) {
	service, store, events := newServicePeriodFixture()
	store.reserveError = errors.New("database outcome unknown")
	_, err := service.CreateServicePeriodProduct(context.Background(), productport.CreateServicePeriodProductCommand{
		ProductCode:    "period-unknown",
		Name:           "unknown",
		PriceMinor:     100,
		Currency:       "CNY",
		DurationDays:   31,
		StockQuantity:  1,
		Actor:          85,
		IdempotencyKey: "period-unknown-key-01",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unknown result error=%v, want unavailable", err)
	}
	if store.reserveCalls != 1 || store.createCalls != 0 || len(events.events) != 0 {
		t.Fatalf("unknown outcome calls reserve/create/events=%d/%d/%d; service retried or continued", store.reserveCalls, store.createCalls, len(events.events))
	}
}

func TestServicePeriodReadStorageFailureIsNotRetried(t *testing.T) {
	service, store, _ := newServicePeriodFixture()
	store.listError = errors.New("database unavailable")
	_, err := service.ListServicePeriodProducts(context.Background(), 1, 0)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("list error=%v, want unavailable", err)
	}
	if store.listCalls != 1 {
		t.Fatalf("list calls=%d, want exactly one", store.listCalls)
	}
}

func TestServicePeriodArchivedProductCannotBeEditedOrEnabled(t *testing.T) {
	service, _, _ := newServicePeriodFixture()
	created := mustCreateServicePeriod(t, service, "period-terminal", 96, "period-create-key-0004")
	archived, err := service.ArchiveServicePeriodProduct(context.Background(), productport.ArchiveServicePeriodProductCommand{
		ID: created.ServiceProductID, ExpectedVersion: created.Version, Actor: 96, IdempotencyKey: "period-terminal-archive",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetServicePeriodProductEnabled(context.Background(), productport.SetServicePeriodProductEnabledCommand{
		ID: archived.ServiceProductID, ExpectedVersion: archived.Version, Enabled: true, Actor: 96, IdempotencyKey: "period-terminal-enable-1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("enable archived error=%v", err)
	}
	_, err = service.UpdateServicePeriodProduct(context.Background(), productport.UpdateServicePeriodProductCommand{
		ID: archived.ServiceProductID, ExpectedVersion: archived.Version, Name: archived.Name, Description: archived.Description,
		PriceMinor: archived.PriceMinor, Currency: archived.Currency, StockQuantity: archived.StockQuantity,
		DurationDays: archived.DurationDays,
		Actor:        96, IdempotencyKey: "period-terminal-update-1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("update archived error=%v", err)
	}
}

func TestServicePeriodPublicReaderUsesExactEnabledCode(t *testing.T) {
	service, store, _ := newServicePeriodFixture()
	created := mustCreateServicePeriod(t, service, "period-public-exact", 75, "period-public-create-0001")
	if _, err := service.ReadPublicServicePeriodByCode(context.Background(), created.ProductCode); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft public reader err=%v", err)
	}
	enabled, err := service.SetServicePeriodProductEnabled(context.Background(), productport.SetServicePeriodProductEnabledCommand{ID: created.ServiceProductID, ExpectedVersion: created.Version, Enabled: true, Actor: 75, IdempotencyKey: "period-public-enable-0001"})
	if err != nil {
		t.Fatal(err)
	}
	public, err := service.ReadPublicServicePeriodByCode(context.Background(), enabled.ProductCode)
	if err != nil || public.ID != enabled.ServiceProductID || public.ProductType != productport.ProductOptionServicePeriod || public.ServicePeriodDurationDays < 1 {
		t.Fatalf("public reader=%+v err=%v", public, err)
	}
	if _, err = service.ReadPublicServicePeriodByCode(context.Background(), "1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("numeric alias selected a service period err=%v", err)
	}
	if _, ok := store.products[created.ServiceProductID]; !ok {
		t.Fatal("fixture unexpectedly changed")
	}
}

func mustCreateServicePeriod(t *testing.T, service *ServicePeriodService, code string, actor int64, key string) productport.ServicePeriodProduct {
	t.Helper()
	product, err := service.CreateServicePeriodProduct(context.Background(), productport.CreateServicePeriodProductCommand{
		ProductCode: code, Name: code, Description: "local", PriceMinor: 8800, Currency: "CNY", DurationDays: 31, StockQuantity: 5,
		Actor: actor, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return product
}

type servicePeriodTestUoW struct {
	calls int
}

func (uow *servicePeriodTestUoW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	return callback(ctx)
}

type servicePeriodTestStore struct {
	products      map[productport.ID]productport.Product
	durations     map[productport.ID]int32
	receipts      map[string]Receipt
	references    map[productport.ID]int
	nextProductID productport.ID
	nextReceiptID int64
	createCalls   int
	updateCalls   int
	listCalls     int
	reserveCalls  int
	listError     error
	reserveError  error
}

func newServicePeriodTestStore() *servicePeriodTestStore {
	return &servicePeriodTestStore{
		products:      map[productport.ID]productport.Product{},
		durations:     map[productport.ID]int32{},
		receipts:      map[string]Receipt{},
		references:    map[productport.ID]int{},
		nextProductID: 1,
		nextReceiptID: 1,
	}
}

func (store *servicePeriodTestStore) ListServicePeriodProducts(_ context.Context, limit, offset int32) ([]productport.Product, int64, error) {
	store.listCalls++
	if store.listError != nil {
		return nil, 0, store.listError
	}
	ids := make([]int, 0, len(store.products))
	for id := range store.products {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	rows := make([]productport.Product, 0, limit)
	var total int64
	for _, rawID := range ids {
		product := store.products[productport.ID(rawID)]
		if _, _, err := servicePeriodLifecycleFromProjection(product.LegacyAdminProjection); err != nil {
			continue
		}
		if total >= int64(offset) && len(rows) < int(limit) {
			rows = append(rows, cloneServicePeriodTestProduct(product))
		}
		total++
	}
	return rows, total, nil
}

func (store *servicePeriodTestStore) GetServicePeriodProduct(_ context.Context, id productport.ID) (productport.Product, error) {
	product, ok := store.products[id]
	if !ok {
		return productport.Product{}, ErrNotFound
	}
	if _, _, err := servicePeriodLifecycleFromProjection(product.LegacyAdminProjection); err != nil {
		return productport.Product{}, ErrNotFound
	}
	return cloneServicePeriodTestProduct(product), nil
}

func (store *servicePeriodTestStore) GetServicePeriodProductByCode(ctx context.Context, code string) (productport.Product, error) {
	for _, product := range store.products {
		if product.ProductCode == code {
			return store.GetServicePeriodProduct(ctx, product.ID)
		}
	}
	return productport.Product{}, ErrNotFound
}

func (store *servicePeriodTestStore) GetServicePeriodProductForUpdate(ctx context.Context, id productport.ID) (productport.Product, error) {
	return store.GetServicePeriodProduct(ctx, id)
}

func (store *servicePeriodTestStore) ReadServicePeriodDuration(_ context.Context, id productport.ID) (int32, error) {
	duration, ok := store.durations[id]
	if !ok || duration < 1 {
		return 0, ErrNotFound
	}
	return duration, nil
}

func (store *servicePeriodTestStore) SetServicePeriodDuration(_ context.Context, id productport.ID, duration int32) error {
	if _, ok := store.products[id]; !ok || duration < 1 {
		return ErrNotFound
	}
	store.durations[id] = duration
	return nil
}

func (store *servicePeriodTestStore) Create(_ context.Context, command productport.CreateCommand, now time.Time) (productport.Product, error) {
	store.createCalls++
	for _, product := range store.products {
		if product.ProductCode == command.ProductCode {
			return productport.Product{}, ErrConflict
		}
	}
	id := store.nextProductID
	store.nextProductID++
	product := productport.Product{
		ID: id, ProductCode: command.ProductCode, Name: command.Name, Description: command.Description,
		PriceMinor: command.PriceMinor, Currency: command.Currency, StockQuantity: command.StockQuantity,
		Images: append([]string(nil), command.Images...), CreatedBy: command.Actor, CreatedAt: now, UpdatedAt: now, Version: 1,
		LegacyAdminProjection: append([]byte(nil), command.LegacyAdminProjection...),
	}
	store.products[id] = cloneServicePeriodTestProduct(product)
	return product, nil
}

func (store *servicePeriodTestStore) UpdateServicePeriodProduct(_ context.Context, command ServicePeriodStoreUpdate, now time.Time) (productport.Product, error) {
	store.updateCalls++
	product, ok := store.products[command.ID]
	if !ok {
		return productport.Product{}, ErrNotFound
	}
	if product.Version != command.ExpectedVersion {
		return productport.Product{}, ErrConflict
	}
	product.Name = command.Name
	product.Description = command.Description
	product.PriceMinor = command.PriceMinor
	product.Currency = command.Currency
	product.StockQuantity = command.StockQuantity
	product.Images = append([]string(nil), command.Images...)
	product.LegacyAdminProjection = append([]byte(nil), command.LegacyAdminProjection...)
	product.Version++
	product.UpdatedAt = now
	store.products[command.ID] = cloneServicePeriodTestProduct(product)
	return product, nil
}

func (store *servicePeriodTestStore) Reserve(_ context.Context, reservation Reservation) (Receipt, bool, error) {
	store.reserveCalls++
	if store.reserveError != nil {
		return Receipt{}, false, store.reserveError
	}
	key := servicePeriodReceiptMapKey(reservation.Operation, reservation.ActorScope, reservation.KeyDigest)
	if receipt, ok := store.receipts[key]; ok {
		return cloneServicePeriodTestReceipt(receipt), false, nil
	}
	receipt := Receipt{
		ID: store.nextReceiptID, Operation: reservation.Operation, ActorScope: reservation.ActorScope,
		KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress",
	}
	store.nextReceiptID++
	store.receipts[key] = receipt
	return receipt, true, nil
}

func (store *servicePeriodTestStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	for key, receipt := range store.receipts {
		if receipt.ID != id || receipt.State != "in_progress" {
			continue
		}
		receipt.State = "completed"
		receipt.ResultSnapshot = append([]byte(nil), snapshot...)
		store.receipts[key] = receipt
		return cloneServicePeriodTestReceipt(receipt), nil
	}
	return Receipt{}, ErrUnavailable
}

func (store *servicePeriodTestStore) hasCompletedReceipt(operation, actorScope, key string) bool {
	digest := sha256.Sum256([]byte(key))
	receipt, ok := store.receipts[servicePeriodReceiptMapKey(operation, actorScope, digest)]
	return ok && receipt.State == "completed"
}

func servicePeriodReceiptMapKey(operation, actorScope string, digest [32]byte) string {
	return fmt.Sprintf("%s|%s|%x", operation, actorScope, digest)
}

func cloneServicePeriodTestProduct(product productport.Product) productport.Product {
	product.Images = append([]string(nil), product.Images...)
	product.LegacyAdminProjection = append([]byte(nil), product.LegacyAdminProjection...)
	return product
}

func cloneServicePeriodTestReceipt(receipt Receipt) Receipt {
	receipt.ResultSnapshot = append([]byte(nil), receipt.ResultSnapshot...)
	return receipt
}

type servicePeriodTestEvents struct {
	events []productport.Event
	err    error
}

func (events *servicePeriodTestEvents) Append(_ context.Context, event productport.Event) (productport.EventID, error) {
	if events.err != nil {
		return 0, events.err
	}
	event.Payload = append([]byte(nil), event.Payload...)
	events.events = append(events.events, event)
	return productport.EventID(len(events.events)), nil
}

func newServicePeriodFixture() (*ServicePeriodService, *servicePeriodTestStore, *servicePeriodTestEvents) {
	store := newServicePeriodTestStore()
	events := &servicePeriodTestEvents{}
	service := NewServicePeriodService(&servicePeriodTestUoW{}, store, events)
	base := time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC)
	var tick time.Duration
	service.now = func() time.Time {
		result := base.Add(tick)
		tick += time.Second
		return result
	}
	return service, store, events
}
