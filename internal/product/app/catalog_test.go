package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type productTestUoW struct{ calls int }

func (u *productTestUoW) Within(ctx context.Context, callback func(context.Context) error) error {
	u.calls++
	return callback(context.WithValue(ctx, productTestTxKey{}, true))
}

type productTestTxKey struct{}

type productTestStore struct {
	products        []productport.Product
	productReceipts map[string]Receipt
	nextReceiptID   int64
	createCalls     int
	listOffset      int32
	listLimit       int32
	countCalls      int
	completeCalls   int
}

func (s *productTestStore) List(_ context.Context, after *productport.ID, limit int32) ([]productport.Product, error) {
	items := append([]productport.Product(nil), s.products...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	if after != nil {
		filtered := items[:0]
		for _, item := range items {
			if item.ID < *after {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if len(items) > int(limit) {
		items = items[:limit]
	}
	return items, nil
}
func (s *productTestStore) ListOffset(_ context.Context, limit, offset int32) ([]productport.Product, error) {
	s.listLimit, s.listOffset = limit, offset
	items := append([]productport.Product(nil), s.products...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	if int(offset) >= len(items) {
		return []productport.Product{}, nil
	}
	end := int(offset + limit)
	if end > len(items) {
		end = len(items)
	}
	return append([]productport.Product(nil), items[offset:end]...), nil
}
func (s *productTestStore) Count(context.Context) (int64, error) {
	s.countCalls++
	return int64(len(s.products)), nil
}
func (s *productTestStore) Get(_ context.Context, id productport.ID) (productport.Product, error) {
	for _, product := range s.products {
		if product.ID == id {
			return product, nil
		}
	}
	return productport.Product{}, ErrNotFound
}
func (s *productTestStore) GetByCode(_ context.Context, code string) (productport.Product, error) {
	for _, product := range s.products {
		if product.ProductCode == code {
			return product, nil
		}
	}
	return productport.Product{}, ErrNotFound
}
func (s *productTestStore) GetForUpdate(ctx context.Context, id productport.ID) (productport.Product, error) {
	return s.Get(ctx, id)
}
func (s *productTestStore) Create(_ context.Context, command productport.CreateCommand, now time.Time) (productport.Product, error) {
	s.createCalls++
	product := productport.Product{ID: productport.ID(len(s.products) + 1), ProductCode: command.ProductCode, Name: command.Name,
		Description: command.Description, PriceMinor: command.PriceMinor, Currency: command.Currency,
		StockQuantity: command.StockQuantity, Images: append([]string(nil), command.Images...), CreatedBy: command.Actor,
		CreatedAt: now, UpdatedAt: now, Version: 1, LegacyAdminProjection: append([]byte(nil), command.LegacyAdminProjection...)}
	s.products = append(s.products, product)
	return product, nil
}
func (s *productTestStore) Update(_ context.Context, command productport.UpdateCommand, now time.Time) (productport.Product, error) {
	for index, product := range s.products {
		if product.ID != command.ID {
			continue
		}
		if product.Version != command.ExpectedVersion {
			return productport.Product{}, ErrConflict
		}
		product.Name, product.Description, product.PriceMinor, product.Currency, product.StockQuantity = command.Name, command.Description, command.PriceMinor, command.Currency, command.StockQuantity
		if command.Images != nil {
			product.Images = append([]string(nil), command.Images...)
		}
		if len(command.LegacyAdminProjection) > 0 {
			product.LegacyAdminProjection = append([]byte(nil), command.LegacyAdminProjection...)
		}
		product.Version, product.UpdatedAt = product.Version+1, now
		s.products[index] = product
		return product, nil
	}
	return productport.Product{}, ErrNotFound
}
func receiptKey(reservation Reservation) string {
	return reservation.Operation + "\x00" + reservation.ActorScope + "\x00" + string(reservation.KeyDigest[:])
}

func (s *productTestStore) reserve(receipts *map[string]Receipt, reservation Reservation) (Receipt, bool, error) {
	if *receipts == nil {
		*receipts = make(map[string]Receipt)
	}
	key := receiptKey(reservation)
	if receipt, ok := (*receipts)[key]; ok {
		return receipt, false, nil
	}
	s.nextReceiptID++
	receipt := Receipt{ID: s.nextReceiptID, Operation: reservation.Operation, ActorScope: reservation.ActorScope, KeyDigest: reservation.KeyDigest,
		PayloadDigest: reservation.PayloadDigest, State: "in_progress"}
	(*receipts)[key] = receipt
	return receipt, true, nil
}

func (s *productTestStore) Reserve(_ context.Context, reservation Reservation) (Receipt, bool, error) {
	return s.reserve(&s.productReceipts, reservation)
}
func (s *productTestStore) complete(receipts map[string]Receipt, id int64, snapshot json.RawMessage) (Receipt, error) {
	s.completeCalls++
	var value any
	if json.Unmarshal(snapshot, &value) != nil {
		return Receipt{}, ErrUnavailable
	}
	canonical, _ := json.Marshal(value)
	for key, receipt := range receipts {
		if receipt.ID != id {
			continue
		}
		receipt.State = "completed"
		receipt.ResultSnapshot = canonical
		receipts[key] = receipt
		return receipt, nil
	}
	return Receipt{}, ErrUnavailable
}
func (s *productTestStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	return s.complete(s.productReceipts, id, snapshot)
}

type productTestEvents struct {
	events []productport.Event
	err    error
}

func (e *productTestEvents) Append(_ context.Context, event productport.Event) (productport.EventID, error) {
	if e.err != nil {
		return 0, e.err
	}
	e.events = append(e.events, event)
	return productport.EventID(len(e.events)), nil
}

func validTestProduct(id int64) productport.Product {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	return productport.Product{ID: productport.ID(id), ProductCode: "sku-" + string(rune('a'+id)), Name: "商品",
		Description: "说明", PriceMinor: 9900, Currency: "CNY", StockQuantity: 0, Images: []string{},
		CreatedBy: 7, CreatedAt: now, UpdatedAt: now, Version: 1, LegacyAdminProjection: DefaultLegacyAdminProjection()}
}

func TestNormalizeClonesImagesWithoutCollapsingEmptySlice(t *testing.T) {
	command := productport.CreateCommand{
		ProductCode: "sku-empty-images", Name: "商品", Currency: "CNY", Images: []string{},
		Actor: 7, IdempotencyKey: "product-empty-images", LegacyAdminProjection: DefaultLegacyAdminProjection(),
	}
	normalized, _, err := normalize(command)
	if err != nil {
		t.Fatalf("normalize() error = %v", err)
	}
	if normalized.Images == nil {
		t.Fatal("normalize() collapsed a non-nil empty image list")
	}
}

func TestEnabledLegacyAdminProjectionForCreatePreservesConfigurationAndEnablesProduct(t *testing.T) {
	projection, err := EnabledLegacyAdminProjectionForCreate(json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"require_mobile":true,"slices":[{"image_id":8}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Status        string           `json:"status"`
		Enabled       bool             `json:"enabled"`
		RequireMobile bool             `json:"require_mobile"`
		Slices        []map[string]any `json:"slices"`
	}
	if json.Unmarshal(projection, &value) != nil || value.Status != "active" || !value.Enabled || !value.RequireMobile || len(value.Slices) != 1 {
		t.Fatalf("create projection=%s", projection)
	}
}

func TestListReturnsNewestProductsFirstAcrossCursor(t *testing.T) {
	store := &productTestStore{products: []productport.Product{validTestProduct(1), validTestProduct(2), validTestProduct(3)}}
	service := NewService(&productTestUoW{}, store, &productTestEvents{})
	first, err := service.List(context.Background(), "", 2)
	if err != nil || len(first.Items) != 2 || first.Items[0].ID != 3 || first.Items[1].ID != 2 || first.NextCursor == "" {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	second, err := service.List(context.Background(), first.NextCursor, 2)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != 1 || second.NextCursor != "" {
		t.Fatalf("second page=%+v err=%v", second, err)
	}
}

func TestProductWriteRejectsUnsafeEnabledPaidPurchaseActionBeforePersistence(t *testing.T) {
	unsafe := []json.RawMessage{
		json.RawMessage(`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"qr"}`),
		json.RawMessage(`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"qr","lead_channel_id":null}`),
		json.RawMessage(`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect"}`),
		json.RawMessage(`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect","completion_redirect_url":"javascript:alert(1)"}`),
		json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":true}}`),
		json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":true,"tag_ids":[]}}`),
		json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":true,"tag_ids":[7,7]}}`),
		json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":true,"tag_ids":["7"]}}`),
		json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":true,"tag_ids":[0]}}`),
	}
	for _, projection := range unsafe {
		uow, store, events := &productTestUoW{}, &productTestStore{}, &productTestEvents{}
		service := NewService(uow, store, events)
		_, err := service.Create(context.Background(), productport.CreateCommand{
			ProductCode: "safe-write-rejection", Name: "商品", Currency: "CNY", Actor: 7,
			IdempotencyKey: "product-paid-action-reject", LegacyAdminProjection: projection,
		})
		if !errors.Is(err, ErrInvalidProduct) {
			t.Fatalf("Create projection=%s error=%v, want invalid product", projection, err)
		}
		if store.createCalls != 0 || len(events.events) != 0 || uow.calls != 0 {
			t.Fatalf("Create projection=%s persisted before validation: creates=%d events=%d transactions=%d", projection, store.createCalls, len(events.events), uow.calls)
		}
		_, err = service.Update(context.Background(), productport.UpdateCommand{
			ID: 1, ExpectedVersion: 1, Name: "商品", Currency: "CNY", Actor: 7,
			IdempotencyKey: "product-paid-action-reject", LegacyAdminProjection: projection,
		})
		if !errors.Is(err, ErrInvalidProduct) {
			t.Fatalf("Update projection=%s error=%v, want invalid product", projection, err)
		}
		if store.createCalls != 0 || len(events.events) != 0 || uow.calls != 0 {
			t.Fatalf("Update projection=%s persisted before validation: creates=%d events=%d transactions=%d", projection, store.createCalls, len(events.events), uow.calls)
		}
	}
}

func TestProductWriteKeepsDisabledAndLegacyTaggingDrafts(t *testing.T) {
	for _, projection := range []json.RawMessage{
		json.RawMessage(`{"schema_version":1,"wecom_tagging":{"tag_ids":["legacy-tag"]}}`),
		json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":false,"tag_ids":["draft-tag"]}}`),
	} {
		if _, err := CanonicalLegacyAdminProjection(projection); err != nil {
			t.Fatalf("projection=%s should remain compatible: %v", projection, err)
		}
	}
}

func TestExplicitPaidPurchaseTaggingEnforcesMaximumTagCount(t *testing.T) {
	tagIDs := make([]int, 100)
	for index := range tagIDs {
		tagIDs[index] = index + 1
	}
	projection, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"wecom_tagging":  map[string]any{"enabled": true, "tag_ids": tagIDs},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CanonicalLegacyAdminProjection(projection); err != nil {
		t.Fatalf("100 enabled tag IDs rejected: %v", err)
	}
	tagIDs = append(tagIDs, 101)
	projection, err = json.Marshal(map[string]any{
		"schema_version": 1,
		"wecom_tagging":  map[string]any{"enabled": true, "tag_ids": tagIDs},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CanonicalLegacyAdminProjection(projection); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("101 enabled tag IDs error=%v, want invalid product", err)
	}
}

func TestLegacyProjectionMissingPostPurchaseDefaultsRemainsReadable(t *testing.T) {
	legacyOrdinary := json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`)
	product := validTestProduct(71)
	product.LegacyAdminProjection = legacyOrdinary
	product.LocalLifecycle = productport.LocalProductEnabled
	local, err := ProjectLocalProduct(product)
	if err != nil || !local.Enabled || local.Lifecycle != productport.LocalProductEnabled {
		t.Fatalf("legacy ordinary local=%+v err=%v", local, err)
	}
	legacyServicePeriod := json.RawMessage(`{"schema_version":1,"status":"service_period_enabled","enabled":true,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[{"image_id":88}]}`)
	if !IsServicePeriodProjection(legacyServicePeriod) {
		t.Fatal("legacy service-period projection was hidden by added defaults")
	}
	presentation, err := publicServicePeriodPresentation(legacyServicePeriod)
	if err != nil || len(presentation.Media) != 1 || presentation.Media[0].ImageID != 88 {
		t.Fatalf("legacy service-period presentation=%+v err=%v", presentation, err)
	}
}

func validServicePeriodProjection(t *testing.T, status string, enabled bool) json.RawMessage {
	t.Helper()
	projection, err := CanonicalLegacyAdminProjection(json.RawMessage(`{"schema_version":1,"status":"` + status + `","enabled":` + map[bool]string{false: "false", true: "true"}[enabled] + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func TestCreateCanonicalLegacyProjectionParticipatesInIdempotency(t *testing.T) {
	uow, store, events := &productTestUoW{}, &productTestStore{}, &productTestEvents{}
	service := NewService(uow, store, events)
	service.now = func() time.Time { return time.Date(2026, 8, 14, 12, 1, 0, 0, time.UTC) }
	command := productport.CreateCommand{ProductCode: "sku-001", Name: "普通商品", Description: "说明", PriceMinor: 1999,
		Currency: "cny", StockQuantity: 0, Images: []string{"https://img.example/a.png"}, Actor: 7,
		IdempotencyKey: "product-idempotency-001", LegacyAdminProjection: json.RawMessage(`{"enabled":true,"status":"active","schema_version":1,"slices":[{"image_id":8}]}`)}

	first, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	command.LegacyAdminProjection = json.RawMessage(`{"schema_version":1,"slices":[{"image_id":8}],"status":"active","enabled":true}`)
	replayed, err := service.Create(context.Background(), command)
	if err != nil || replayed.ID != first.ID || store.createCalls != 1 || len(events.events) != 1 || store.completeCalls != 1 {
		t.Fatalf("replay product=%+v err=%v creates=%d events=%d completes=%d", replayed, err, store.createCalls, len(events.events), store.completeCalls)
	}
	command.LegacyAdminProjection = json.RawMessage(`{"schema_version":1,"slices":[{"image_id":9}],"status":"active","enabled":true}`)
	if _, err = service.Create(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed projection error = %v, want conflict", err)
	}
	if events.events[0].Type != productport.EventProductCreated || string(events.events[0].Payload) != `{"product_id":1,"actor":7}` {
		t.Fatalf("event = %+v", events.events[0])
	}
}

func TestListLegacyReturnsBoundedOffsetPageAndExactTotal(t *testing.T) {
	store := &productTestStore{products: []productport.Product{validTestProduct(1), validTestProduct(2), validTestProduct(3)}}
	service := NewService(&productTestUoW{}, store, &productTestEvents{})
	page, err := service.ListLegacy(context.Background(), 1, 1)
	if err != nil || page.Total != 3 || page.Limit != 1 || page.Offset != 1 || len(page.Items) != 1 || page.Items[0].ID != 2 {
		t.Fatalf("ListLegacy() = %+v, %v", page, err)
	}
	if store.listLimit != 1 || store.listOffset != 1 || store.countCalls != 1 {
		t.Fatalf("store list/count = %d/%d/%d", store.listLimit, store.listOffset, store.countCalls)
	}
	for _, input := range [][2]int32{{0, 0}, {101, 0}, {1, -1}, {1, MaximumLegacyOffset + 1}} {
		if _, err = service.ListLegacy(context.Background(), input[0], input[1]); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("ListLegacy(%d,%d) error = %v", input[0], input[1], err)
		}
	}
}

func TestGetUsesOneTransactionForAnOrdinaryExternalRead(t *testing.T) {
	uow := &productTestUoW{}
	store := &productTestStore{products: []productport.Product{validTestProduct(19)}}
	service := NewService(uow, store, &productTestEvents{})

	got, err := service.Get(context.Background(), 19)
	if err != nil || got.ID != 19 {
		t.Fatalf("Get() product=%+v err=%v", got, err)
	}
	if uow.calls != 1 {
		t.Fatalf("ordinary Get opened %d transactions, want 1", uow.calls)
	}
}

func TestOrdinaryCatalogRejectsServicePeriodProjectionAtEveryApplicationBoundary(t *testing.T) {
	servicePeriod := validTestProduct(7)
	servicePeriod.LegacyAdminProjection = validServicePeriodProjection(t, ServicePeriodProjectionDraftStatus, false)
	store := &productTestStore{products: []productport.Product{servicePeriod}}
	service := NewService(&productTestUoW{}, store, &productTestEvents{})

	if _, err := service.List(context.Background(), "", 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("list error=%v", err)
	}
	if _, err := service.ListLegacy(context.Background(), 10, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("legacy list error=%v", err)
	}
	if _, err := service.Get(context.Background(), servicePeriod.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get error=%v", err)
	}
	if _, err := service.Update(context.Background(), productport.UpdateCommand{
		ID: servicePeriod.ID, ExpectedVersion: servicePeriod.Version, Name: "ordinary", Description: "ordinary", PriceMinor: 1, Currency: "CNY", StockQuantity: 1, Actor: 1, IdempotencyKey: "ordinary-service-period-001",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update error=%v", err)
	}
}

func TestUpdateUsesProductVersionCASAndOperationScopedReceipt(t *testing.T) {
	store := &productTestStore{products: []productport.Product{validTestProduct(1)}}
	events := &productTestEvents{}
	service := NewService(&productTestUoW{}, store, events)
	service.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC) }
	command := productport.UpdateCommand{ID: 1, ExpectedVersion: 1, Name: "更新商品", Description: "更新说明", PriceMinor: 2999, Currency: "cny", StockQuantity: 3, Images: []string{"https://cdn.example.test/product.png"}, LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"立即购买","require_mobile":true}`), Actor: 7, IdempotencyKey: "product-update-idempotency-001"}

	first, err := service.Update(context.Background(), command)
	if err != nil || first.Version != 2 || first.ProductCode != validTestProduct(1).ProductCode || first.Name != "更新商品" || len(first.Images) != 1 || first.Images[0] != "https://cdn.example.test/product.png" || !strings.Contains(string(first.LegacyAdminProjection), `"buy_button_text":"立即购买"`) || len(events.events) != 1 {
		t.Fatalf("first Update() product=%+v error=%v events=%d", first, err, len(events.events))
	}
	replayed, err := service.Update(context.Background(), command)
	if err != nil || replayed.ID != first.ID || replayed.Version != first.Version || replayed.Name != first.Name || len(events.events) != 1 || store.completeCalls != 1 {
		t.Fatalf("replayed Update() product=%+v error=%v events=%d completes=%d", replayed, err, len(events.events), store.completeCalls)
	}
	changed := command
	changed.Name = "changed payload"
	if _, err = service.Update(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same update key with changed payload error=%v, want conflict", err)
	}
	stale := command
	stale.IdempotencyKey = "product-update-idempotency-002"
	if _, err = service.Update(context.Background(), stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale product version error=%v, want conflict", err)
	}
	for _, version := range []int64{0, math.MaxInt64} {
		invalid := command
		invalid.ExpectedVersion = version
		invalid.IdempotencyKey = "product-update-idempotency-max"
		if _, err = service.Update(context.Background(), invalid); !errors.Is(err, ErrInvalidProduct) {
			t.Fatalf("expected version %d error=%v, want invalid", version, err)
		}
	}
	if events.events[0].Type != productport.EventProductUpdated {
		t.Fatalf("event type=%s, want %s", events.events[0].Type, productport.EventProductUpdated)
	}
}

func TestProductUpdateRejectsArchivedTerminalWithoutChangingTheRetainedFact(t *testing.T) {
	archived := validTestProduct(44)
	projection, err := localProductProjectionForLifecycle(archived.LegacyAdminProjection, productport.LocalProductArchived)
	if err != nil {
		t.Fatal(err)
	}
	archived.LegacyAdminProjection = projection
	store := &productTestStore{products: []productport.Product{archived}}
	events := &productTestEvents{}
	service := NewService(&productTestUoW{}, store, events)
	_, err = service.Update(context.Background(), productport.UpdateCommand{
		ID: archived.ID, ExpectedVersion: archived.Version, Name: "不应修改", Description: "仍保留的历史商品", PriceMinor: 100,
		Currency: "CNY", StockQuantity: 1, Actor: 7, IdempotencyKey: "product-archive-edit-0001",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived update error=%v", err)
	}
	if got := store.products[0]; got.ID != archived.ID || got.Version != archived.Version || got.Name != archived.Name || len(events.events) != 0 || store.completeCalls != 0 {
		t.Fatalf("archived fact changed=%+v events=%d receipts=%d", got, len(events.events), store.completeCalls)
	}
}

func TestProductValidationRejectsTimeBeforeCreation(t *testing.T) {
	product := validTestProduct(1)
	product.UpdatedAt = product.CreatedAt.Add(-time.Nanosecond)
	if validProduct(product) {
		t.Fatal("product with update timestamp before creation became valid")
	}
}

func TestCanonicalLegacyAdminProjectionRejectsUnknownAndWrongTypes(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":2}`,
		`{"schema_version":1,"unknown_field":"not-supported"}`,
		`{"schema_version":1,"enabled":"yes"}`,
		`{"schema_version":1,"slices":{}}`,
		`{"schema_version":1,"completion_target":3}`,
		`{"schema_version":1,"wecom_tagging":true}`,
		`{"schema_version":1,"lead_program_id":0}`,
		`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"qr"}`,
		`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"qr","lead_channel_id":null}`,
		`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect"}`,
		`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect","completion_redirect_url":"https://example.test/path#fragment"}`,
		`{"schema_version":1} 42`,
	} {
		if _, err := CanonicalLegacyAdminProjection(json.RawMessage(raw)); !errors.Is(err, ErrInvalidProduct) {
			t.Fatalf("projection %s error = %v", raw, err)
		}
	}
	projection := DefaultLegacyAdminProjection()
	if string(projection) == "" || !json.Valid(projection) {
		t.Fatalf("default projection = %s", projection)
	}
	explicitNulls, err := CanonicalLegacyAdminProjection(json.RawMessage(`{"schema_version":1,"lead_program_id":null,"lead_channel_id":null,"completion_target":null}`))
	if err != nil || !jsonEquivalent(projection, explicitNulls) {
		t.Fatalf("missing fields must equal their frozen null defaults: projection=%s explicit=%s error=%v", projection, explicitNulls, err)
	}
	objectTarget, err := CanonicalLegacyAdminProjection(json.RawMessage(`{"schema_version":1,"completion_target":{}}`))
	if err != nil || jsonEquivalent(explicitNulls, objectTarget) {
		t.Fatalf("null and object targets must remain distinct: nulls=%s object=%s error=%v", explicitNulls, objectTarget, err)
	}
	if jsonEquivalent([]byte(`{"lead_program_id":9007199254740992}`), []byte(`{"lead_program_id":9007199254740993}`)) {
		t.Fatal("JSON semantic comparison lost integer precision above 2^53")
	}
}

func TestClassifyPreservesSuccessfulResult(t *testing.T) {
	if err := classify(nil); err != nil {
		t.Fatalf("classify(nil) = %v, want nil", err)
	}
}
