package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type couponTestUOW struct{}

func (couponTestUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(context.WithValue(ctx, couponTestTxKey{}, true))
}

type couponTestTxKey struct{}

type couponTestEvents struct{ rows []couponport.Event }

func (e *couponTestEvents) Append(ctx context.Context, event couponport.Event) (couponport.EventID, error) {
	if in, _ := ctx.Value(couponTestTxKey{}).(bool); !in {
		return 0, errors.New("event escaped coupon transaction")
	}
	e.rows = append(e.rows, event)
	return couponport.EventID(len(e.rows)), nil
}

type couponTestProducts struct{}

func (couponTestProducts) ReadProductTarget(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	return productport.ProductOption{ID: id, ProductType: kind, Currency: "CNY", PriceMinor: 99999}, nil
}

type couponTestStore struct {
	coupons  map[couponport.ID]couponport.Coupon
	receipts map[string]Receipt
	nextID   couponport.ID
	updates  int
}

func newCouponTestStore(items ...couponport.Coupon) *couponTestStore {
	store := &couponTestStore{coupons: map[couponport.ID]couponport.Coupon{}, receipts: map[string]Receipt{}, nextID: 100}
	for _, item := range items {
		store.coupons[item.ID] = item
		if item.ID >= store.nextID {
			store.nextID = item.ID + 1
		}
	}
	return store
}

func (s *couponTestStore) List(_ context.Context, limit, offset int32, search, status string) ([]couponport.Coupon, error) {
	items := make([]couponport.Coupon, 0, len(s.coupons))
	for _, item := range s.coupons {
		if (search == "" || item.Name == search) && (status == "" || item.Status == status) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := int(offset)
	if start > len(items) {
		start = len(items)
	}
	end := start + int(limit)
	if end > len(items) {
		end = len(items)
	}
	return append([]couponport.Coupon(nil), items[start:end]...), nil
}
func (s *couponTestStore) Count(_ context.Context, search, status string) (int64, error) {
	items, _ := s.List(context.Background(), MaximumLimit, 0, search, status)
	return int64(len(items)), nil
}
func (s *couponTestStore) Get(_ context.Context, id couponport.ID) (couponport.Coupon, error) {
	item, ok := s.coupons[id]
	if !ok {
		return couponport.Coupon{}, ErrNotFound
	}
	return item, nil
}
func (s *couponTestStore) Lock(ctx context.Context, id couponport.ID) (couponport.Coupon, error) {
	return s.Get(ctx, id)
}
func (s *couponTestStore) Create(_ context.Context, command couponport.UpsertCommand, _ []int64, now time.Time) (couponport.Coupon, error) {
	id := s.nextID
	s.nextID++
	item := command.Coupon
	item.ID, item.Status, item.Currency = id, "draft", "CNY"
	item.CreatedBy, item.UpdatedBy, item.Version, item.CreatedAt, item.UpdatedAt = command.Actor, command.Actor, 1, now, now
	s.coupons[id] = item
	return item, nil
}
func (s *couponTestStore) Update(_ context.Context, command couponport.UpsertCommand, _ []int64, now time.Time) (couponport.Coupon, error) {
	s.updates++
	old, err := s.Get(context.Background(), command.ID)
	if err != nil {
		return couponport.Coupon{}, err
	}
	item := command.Coupon
	item.Status, item.Currency = old.Status, "CNY"
	item.CreatedBy, item.UpdatedBy, item.Version, item.CreatedAt, item.UpdatedAt = old.CreatedBy, command.Actor, old.Version+1, old.CreatedAt, now
	s.coupons[item.ID] = item
	return item, nil
}
func (s *couponTestStore) SetStatus(_ context.Context, id couponport.ID, status string, actor int64, now time.Time) (couponport.Coupon, error) {
	item, err := s.Get(context.Background(), id)
	if err != nil {
		return couponport.Coupon{}, err
	}
	item.Status, item.UpdatedBy, item.UpdatedAt, item.Version = status, actor, now, item.Version+1
	s.coupons[id] = item
	return item, nil
}
func couponTestReceiptKey(x Reservation) string {
	return x.Operation + "\x00" + x.ActorScope + "\x00" + fmt.Sprintf("%x", x.KeyDigest)
}
func (s *couponTestStore) Reserve(_ context.Context, x Reservation) (Receipt, bool, error) {
	key := couponTestReceiptKey(x)
	if row, ok := s.receipts[key]; ok {
		return row, false, nil
	}
	row := Receipt{ID: int64(len(s.receipts) + 1), Operation: x.Operation, ActorScope: x.ActorScope, KeyDigest: x.KeyDigest, PayloadDigest: x.PayloadDigest, State: "in_progress"}
	s.receipts[key] = row
	return row, true, nil
}
func (s *couponTestStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	for key, row := range s.receipts {
		if row.ID != id || row.State != "in_progress" {
			continue
		}
		row.State, row.ResultSnapshot = "completed", append(json.RawMessage(nil), snapshot...)
		s.receipts[key] = row
		return row, nil
	}
	return Receipt{}, ErrUnavailable
}
func (s *couponTestStore) DeleteDraft(_ context.Context, id couponport.ID) error {
	item, err := s.Get(context.Background(), id)
	if err != nil {
		return err
	}
	if item.Status != "draft" || item.IssuedCount != 0 {
		return ErrConflict
	}
	delete(s.coupons, id)
	return nil
}

func couponTestService(now time.Time, store *couponTestStore, events *couponTestEvents) *Service {
	service := NewService(couponTestUOW{}, store, couponTestProducts{}, events)
	service.now = func() time.Time { return now }
	return service
}
func couponTestItem(id couponport.ID, now time.Time) couponport.Coupon {
	days := int32(30)
	return couponport.Coupon{ID: id, Name: "满减券", DiscountAmountTotal: 100, Currency: "CNY", Status: "published", TotalIssueLimit: 2, PerUserIssueLimit: 1, ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, Instructions: "", TargetRefs: []string{"standard_product:7"}, CreatedBy: 1, UpdatedBy: 1, Version: 1, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour)}
}

func TestCouponRuleLifecycleAndStats(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	store, events := newCouponTestStore(), &couponTestEvents{}
	service := couponTestService(now, store, events)
	days := int32(14)
	draft, err := service.Create(context.Background(), couponport.UpsertCommand{Coupon: couponport.Coupon{
		Name: "新客立减", DiscountAmountTotal: 100, TotalIssueLimit: 10, PerUserIssueLimit: 1,
		ClaimStartsAt: now, ClaimEndsAt: now.Add(24 * time.Hour), ValidityMode: couponport.ValidityRelativeDays,
		RelativeValidityDays: &days, TargetRefs: []string{"standard_product:7"}, Instructions: "仅限指定商品",
	}, Actor: 7, IdempotencyKey: "create-rule-key-0001"})
	if err != nil || draft.ID != 100 || draft.Status != "draft" || draft.Currency != "CNY" || len(events.rows) != 1 || events.rows[0].Type != couponport.EventCouponCreated {
		t.Fatalf("draft=%#v err=%v events=%#v", draft, err, events.rows)
	}
	stats, err := service.Stats(context.Background(), draft.ID)
	if err != nil || stats.CouponID != draft.ID || stats.TotalIssueLimit != 10 || stats.IssuedCount != 0 || stats.RemainingIssueCount != 10 || stats.Status != "draft" || stats.AvailabilityStatus != "draft" {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
	draft.Name = "新客立减升级"
	updated, err := service.UpdateDraft(context.Background(), couponport.UpsertCommand{Coupon: draft, Actor: 7, IdempotencyKey: "update-rule-key-0001"})
	if err != nil || updated.Name != "新客立减升级" || updated.Version != draft.Version+1 || len(events.rows) != 2 || events.rows[1].Type != couponport.EventCouponUpdated {
		t.Fatalf("updated=%#v err=%v events=%#v", updated, err, events.rows)
	}
	published, err := service.Publish(context.Background(), updated.ID, 7, "publish-rule-key-0001")
	if err != nil || published.Status != "published" || published.AvailabilityStatus != "active" || len(events.rows) != 3 || events.rows[2].Type != couponport.EventCouponPublished {
		t.Fatalf("published=%#v err=%v events=%#v", published, err, events.rows)
	}
	store.coupons[published.ID] = published
	store.coupons[published.ID] = func(value couponport.Coupon) couponport.Coupon { value.IssuedCount = 3; return value }(store.coupons[published.ID])
	stats, err = service.Stats(context.Background(), published.ID)
	if err != nil || stats.IssuedCount != 3 || stats.RemainingIssueCount != 7 || stats.AvailabilityStatus != "active" {
		t.Fatalf("issued stats=%#v err=%v", stats, err)
	}
	stopped, err := service.Stop(context.Background(), published.ID, 7, "stop-rule-key-000001")
	if err != nil || stopped.Status != "stopped" || stopped.AvailabilityStatus != "stopped" || len(events.rows) != 4 || events.rows[3].Type != couponport.EventCouponStopped {
		t.Fatalf("stopped=%#v err=%v events=%#v", stopped, err, events.rows)
	}
}

func TestCouponRuleAcceptsProductPortServicePeriodTarget(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	store, events := newCouponTestStore(), &couponTestEvents{}
	service := couponTestService(now, store, events)
	days := int32(7)
	created, err := service.Create(context.Background(), couponport.UpsertCommand{Coupon: couponport.Coupon{
		Name: "仅规则", DiscountAmountTotal: 100, TotalIssueLimit: 2, PerUserIssueLimit: 1,
		ClaimStartsAt: now, ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays,
		RelativeValidityDays: &days, TargetRefs: []string{"service_period:7"},
	}, Actor: 7, IdempotencyKey: "service-period-key-001"})
	if err != nil || created.ID < 1 || len(store.coupons) != 1 || len(events.rows) != 1 {
		t.Fatalf("service-period target: created=%#v err=%v coupons=%d events=%d", created, err, len(store.coupons), len(events.rows))
	}
	published, err := service.Publish(context.Background(), created.ID, 7, "service-period-publish-01")
	if err != nil || published.Status != "published" || len(events.rows) != 2 {
		t.Fatalf("service-period target publish=%#v err=%v events=%d", published, err, len(events.rows))
	}
}

func TestUpdateDraftLocksPublishedRules(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	assertRejected := func(t *testing.T, service *Service, store *couponTestStore, events *couponTestEvents) {
		t.Helper()
		current := store.coupons[7]
		beforeUpdates, beforeEvents := store.updates, len(events.rows)
		_, err := service.UpdateDraft(context.Background(), couponport.UpsertCommand{
			Coupon:         current,
			Actor:          1,
			IdempotencyKey: "browser-draft-update-key",
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("UpdateDraft error=%v", err)
		}
		if store.updates != beforeUpdates || len(events.rows) != beforeEvents {
			t.Fatalf("rejected draft update wrote update/events: updates=%d events=%d", store.updates, len(events.rows))
		}
		for _, receipt := range store.receipts {
			if receipt.Operation == "update" && receipt.State == "completed" {
				t.Fatalf("rejected draft update completed receipt=%#v", receipt)
			}
		}
	}
	t.Run("published row", func(t *testing.T) {
		item := couponTestItem(7, now)
		item.Status = "draft"
		store, events := newCouponTestStore(item), &couponTestEvents{}
		service := couponTestService(now, store, events)
		if _, err := service.Publish(context.Background(), 7, 1, "publish-before-browser-update"); err != nil {
			t.Fatalf("publish=%v", err)
		}
		assertRejected(t, service, store, events)
	})
}

func TestCouponRuleMutationsUseReceiptReplayAndConflicts(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	first, second, third := couponTestItem(7, now), couponTestItem(8, now), couponTestItem(9, now)
	first.Status, second.Status, third.Status = "draft", "draft", "draft"
	store, events := newCouponTestStore(first, second, third), &couponTestEvents{}
	service := couponTestService(now, store, events)
	key := "archive-key-00001"
	archived, err := service.Archive(context.Background(), 7, first.Version, 9, key)
	if err != nil || archived.Status != "archived" || len(events.rows) != 1 {
		t.Fatalf("archive=%#v err=%v events=%d", archived, err, len(events.rows))
	}
	if replay, replayErr := service.Archive(context.Background(), 7, first.Version, 9, key); replayErr != nil || replay.ID != archived.ID || replay.Status != archived.Status || len(events.rows) != 1 {
		t.Fatalf("archive replay=%#v err=%v events=%d", replay, replayErr, len(events.rows))
	}
	if _, err = service.Archive(context.Background(), 8, second.Version, 9, key); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-coupon same-key error=%v", err)
	}
	if _, err = service.Copy(context.Background(), 7, 9, "copy-archived-key-01"); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived copy error=%v", err)
	}
	if _, err = service.Publish(context.Background(), 7, 9, "publish-archived-001"); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived publish error=%v", err)
	}
	archivedEdit := store.coupons[7]
	archivedEdit.Name = "不能编辑的归档券"
	if _, err = service.Update(context.Background(), couponport.UpsertCommand{Coupon: archivedEdit, Actor: 9, IdempotencyKey: "update-archived-key-01"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived update error=%v", err)
	}
	if got := store.coupons[7]; got.Status != "archived" || got.Name != first.Name || store.updates != 0 || len(events.rows) != 1 {
		t.Fatalf("terminal coupon changed=%#v updates=%d events=%d", got, store.updates, len(events.rows))
	}
	deleted, err := service.Delete(context.Background(), 8, 9, "delete-key-000001")
	if err != nil || deleted.Status != "deleted" {
		t.Fatalf("delete=%#v err=%v", deleted, err)
	}
	if replay, replayErr := service.Delete(context.Background(), 8, 9, "delete-key-000001"); replayErr != nil || replay.Status != "deleted" {
		t.Fatalf("delete replay=%#v err=%v", replay, replayErr)
	}
	copied, err := service.Copy(context.Background(), 9, 9, "copy-key-00000001")
	if err != nil || copied.ID == 9 || copied.Name != "满减券 副本" || copied.Status != "draft" || copied.AvailabilityStatus != "draft" || copied.IssuedCount != 0 || copied.CreatedBy != 9 || copied.UpdatedBy != 9 {
		t.Fatalf("copy=%#v err=%v", copied, err)
	}
	if replay, replayErr := service.Copy(context.Background(), 9, 9, "copy-key-00000001"); replayErr != nil || replay.ID != copied.ID || len(events.rows) != 3 || events.rows[2].Type != "coupon.copied" {
		t.Fatalf("copy replay=%#v err=%v events=%#v", replay, replayErr, events.rows)
	}
	if _, err = service.Copy(context.Background(), 8, 9, "copy-key-00000001"); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-coupon copy key error=%v", err)
	}
}
