package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	DefaultLimit  int32 = 50
	MaximumLimit  int32 = 200
	MaximumOffset int32 = 1_000_000
)

var (
	ErrInvalidCoupon = errors.New("invalid coupon")
	ErrInvalidTarget = errors.New("invalid coupon target")
	ErrNotFound      = errors.New("coupon not found")
	ErrConflict      = errors.New("coupon command conflict")
	ErrRulesFrozen   = errors.New("claimed coupon rules are frozen")
	ErrUnavailable   = errors.New("coupon service unavailable")
)

type Receipt struct {
	ID                           int64
	Operation, ActorScope, State string
	KeyDigest, PayloadDigest     [32]byte
	ResultSnapshot               json.RawMessage
}

type Reservation struct {
	Operation, ActorScope    string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}

type Store interface {
	List(context.Context, int32, int32, string, string) ([]couponport.Coupon, error)
	Count(context.Context, string, string) (int64, error)
	Get(context.Context, couponport.ID) (couponport.Coupon, error)
	Lock(context.Context, couponport.ID) (couponport.Coupon, error)
	Create(context.Context, couponport.UpsertCommand, []int64, time.Time) (couponport.Coupon, error)
	Update(context.Context, couponport.UpsertCommand, []int64, time.Time) (couponport.Coupon, error)
	SetStatus(context.Context, couponport.ID, string, int64, time.Time) (couponport.Coupon, error)
	DeleteDraft(context.Context, couponport.ID) error
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}

type Service struct {
	uow      platformport.UnitOfWork
	store    Store
	products couponport.ProductReader
	events   couponport.EventAppender
	now      func() time.Time
}

var _ couponport.RuleApplication = (*Service)(nil)

func NewService(uow platformport.UnitOfWork, store Store, products couponport.ProductReader, events couponport.EventAppender) *Service {
	return &Service{uow: uow, store: store, products: products, events: events, now: time.Now}
}

func (s *Service) List(ctx context.Context, limit, offset int32, search, status string) (couponport.Page, error) {
	if !ready(s) {
		return couponport.Page{}, ErrUnavailable
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	search, status = strings.TrimSpace(search), strings.TrimSpace(status)
	if limit < 1 || limit > MaximumLimit || offset < 0 || offset > MaximumOffset || len(search) > 80 || len(status) > 32 || status != "" && status != "draft" && status != "published" && status != "stopped" && status != "archived" {
		return couponport.Page{}, ErrInvalidCoupon
	}
	page := couponport.Page{Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		page.Items, err = s.store.List(tx, limit, offset, search, status)
		if err == nil {
			page.Total, err = s.store.Count(tx, search, status)
		}
		return err
	})
	if err != nil {
		return couponport.Page{}, classify(err)
	}
	if page.Total < 0 || len(page.Items) > int(limit) {
		return couponport.Page{}, ErrUnavailable
	}
	for i := range page.Items {
		if !validStored(page.Items[i]) {
			return couponport.Page{}, ErrUnavailable
		}
		page.Items[i] = withAvailability(page.Items[i], s.now().UTC())
	}
	return page, nil
}

func (s *Service) Get(ctx context.Context, id couponport.ID) (couponport.Coupon, error) {
	if !ready(s) || id < 1 {
		return couponport.Coupon{}, ErrNotFound
	}
	var result couponport.Coupon
	err := s.uow.Within(ctx, func(tx context.Context) error { var err error; result, err = s.store.Get(tx, id); return err })
	if err != nil {
		return couponport.Coupon{}, classify(err)
	}
	if !validStored(result) {
		return couponport.Coupon{}, ErrUnavailable
	}
	return withAvailability(result, s.now().UTC()), nil
}

// Stats returns rule-owned counters only. IssuedCount is retained as a
// historical-compatible aggregate, while claim rows and customer ownership
// remain outside this PR05 slice.
func (s *Service) Stats(ctx context.Context, id couponport.ID) (couponport.RuleStats, error) {
	if !ready(s) || id < 1 {
		return couponport.RuleStats{}, ErrNotFound
	}
	var coupon couponport.Coupon
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		coupon, readErr = s.store.Get(tx, id)
		return readErr
	})
	if err != nil {
		return couponport.RuleStats{}, classify(err)
	}
	if !validStored(coupon) {
		return couponport.RuleStats{}, ErrUnavailable
	}
	coupon = withAvailability(coupon, s.now().UTC())
	remaining := coupon.TotalIssueLimit - coupon.IssuedCount
	if remaining < 0 {
		return couponport.RuleStats{}, ErrUnavailable
	}
	return couponport.RuleStats{
		CouponID:            coupon.ID,
		TotalIssueLimit:     coupon.TotalIssueLimit,
		IssuedCount:         coupon.IssuedCount,
		RemainingIssueCount: remaining,
		Status:              coupon.Status,
		AvailabilityStatus:  coupon.AvailabilityStatus,
		UpdatedAt:           coupon.UpdatedAt,
	}, nil
}

func (s *Service) Create(ctx context.Context, input couponport.UpsertCommand) (couponport.Coupon, error) {
	input.ID = 0
	return s.mutate(ctx, "create", input, "", false)
}
func (s *Service) Update(ctx context.Context, input couponport.UpsertCommand) (couponport.Coupon, error) {
	if input.ID < 1 {
		return couponport.Coupon{}, ErrNotFound
	}
	return s.mutate(ctx, "update", input, "", false)
}

// UpdateDraft is the browser-admin mutation boundary. It deliberately keeps
// the broader internal Update semantics (including claimed-rule limits) out
// of the legacy PUT route: the locked row must still be an unissued draft.
func (s *Service) UpdateDraft(ctx context.Context, input couponport.UpsertCommand) (couponport.Coupon, error) {
	if input.ID < 1 {
		return couponport.Coupon{}, ErrNotFound
	}
	return s.mutate(ctx, "update", input, "", true)
}
func (s *Service) Publish(ctx context.Context, id couponport.ID, actor int64, key string) (couponport.Coupon, error) {
	return s.mutate(ctx, "publish", couponport.UpsertCommand{Coupon: couponport.Coupon{ID: id}, Actor: actor, IdempotencyKey: key}, "published", false)
}
func (s *Service) Stop(ctx context.Context, id couponport.ID, actor int64, key string) (couponport.Coupon, error) {
	return s.mutate(ctx, "stop", couponport.UpsertCommand{Coupon: couponport.Coupon{ID: id}, Actor: actor, IdempotencyKey: key}, "stopped", false)
}

func (s *Service) Archive(ctx context.Context, id couponport.ID, expectedVersion, actor int64, key string) (couponport.Coupon, error) {
	if expectedVersion < 1 {
		return couponport.Coupon{}, ErrInvalidCoupon
	}
	return s.ruleMutation(ctx, "archive", id, actor, key, expectedVersion, func(tx context.Context, now time.Time) (couponport.Coupon, bool, error) {
		old, e := s.store.Lock(tx, id)
		if e != nil {
			return couponport.Coupon{}, false, e
		}
		if old.Version != expectedVersion {
			return couponport.Coupon{}, false, ErrConflict
		}
		if old.HistoryOnly {
			return couponport.Coupon{}, false, ErrConflict
		}
		if old.Status == "archived" {
			return withAvailability(old, now), false, nil
		}
		if old.Status != "draft" && old.Status != "published" && old.Status != "stopped" {
			return couponport.Coupon{}, false, ErrConflict
		}
		item, e := s.store.SetStatus(tx, id, "archived", actor, now)
		return withAvailability(item, now), e == nil, e
	})
}

func (s *Service) Delete(ctx context.Context, id couponport.ID, actor int64, key string) (couponport.Coupon, error) {
	return s.ruleMutation(ctx, "delete", id, actor, key, 0, func(tx context.Context, now time.Time) (couponport.Coupon, bool, error) {
		old, e := s.store.Lock(tx, id)
		if e != nil {
			return couponport.Coupon{}, false, e
		}
		if old.Status != "draft" || old.IssuedCount != 0 {
			return couponport.Coupon{}, false, ErrConflict
		}
		if e = s.store.DeleteDraft(tx, id); e != nil {
			return couponport.Coupon{}, false, e
		}
		result := withAvailability(old, now)
		result.Status = "deleted"
		result.AvailabilityStatus = "deleted"
		return result, true, nil
	})
}

func (s *Service) Copy(ctx context.Context, id couponport.ID, actor int64, key string) (couponport.Coupon, error) {
	return s.ruleMutation(ctx, "copy", id, actor, key, 0, func(tx context.Context, now time.Time) (couponport.Coupon, bool, error) {
		old, e := s.store.Lock(tx, id)
		if e != nil {
			return couponport.Coupon{}, false, e
		}
		if old.HistoryOnly || old.Status == "archived" {
			return couponport.Coupon{}, false, ErrConflict
		}
		command, ids, e := normalize(couponport.UpsertCommand{Coupon: couponport.Coupon{Name: copiedCouponName(old.Name), DiscountAmountTotal: old.DiscountAmountTotal, TotalIssueLimit: old.TotalIssueLimit, PerUserIssueLimit: old.PerUserIssueLimit, ClaimStartsAt: old.ClaimStartsAt, ClaimEndsAt: old.ClaimEndsAt, ValidityMode: old.ValidityMode, UseStartsAt: old.UseStartsAt, UseEndsAt: old.UseEndsAt, RelativeValidityDays: old.RelativeValidityDays, Instructions: old.Instructions, TargetRefs: old.TargetRefs}, Actor: actor, IdempotencyKey: key})
		if e != nil {
			return couponport.Coupon{}, false, e
		}
		result, e := s.store.Create(tx, command, ids, now)
		return withAvailability(result, now), e == nil, e
	})
}

func (s *Service) ruleMutation(ctx context.Context, operation string, id couponport.ID, actor int64, key string, expectedVersion int64, apply func(context.Context, time.Time) (couponport.Coupon, bool, error)) (couponport.Coupon, error) {
	if !ready(s) || id < 1 || actor < 1 || !validRuleMutationKey(key) || apply == nil {
		return couponport.Coupon{}, ErrInvalidCoupon
	}
	now := s.now().UTC()
	payload, e := json.Marshal(struct {
		CouponID        couponport.ID `json:"coupon_id"`
		Operation       string        `json:"operation"`
		ExpectedVersion int64         `json:"expected_version,omitempty"`
	}{CouponID: id, Operation: operation, ExpectedVersion: expectedVersion})
	if e != nil {
		return couponport.Coupon{}, ErrUnavailable
	}
	reservation := Reservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now}
	var result couponport.Coupon
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.Reserve(tx, reservation)
		if e != nil {
			return e
		}
		if !receiptMatches(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil || !validStored(result) {
				return ErrUnavailable
			}
			return nil
		}
		var changed bool
		result, changed, e = apply(tx, now)
		if e != nil {
			return e
		}
		if !validStored(result) {
			return ErrUnavailable
		}
		if changed {
			if e = s.appendRuleEvent(tx, operation, result, actor, key, now); e != nil {
				return e
			}
		}
		snapshot, e := json.Marshal(result)
		if e != nil {
			return e
		}
		completed, e := s.store.Complete(tx, receipt.ID, snapshot, now)
		if e != nil || completed.State != "completed" || !jsonEquivalent(completed.ResultSnapshot, snapshot) {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return couponport.Coupon{}, classify(err)
	}
	return result, nil
}

func validRuleMutationKey(key string) bool {
	return len(key) >= 16 && len(key) <= 128 && strings.TrimSpace(key) == key
}

func copiedCouponName(name string) string {
	suffix := " 副本"
	runes := []rune(strings.TrimSpace(name))
	if len(runes) > 45-len([]rune(suffix)) {
		runes = runes[:45-len([]rune(suffix))]
	}
	return string(runes) + suffix
}
func (s *Service) appendRuleEvent(ctx context.Context, operation string, item couponport.Coupon, actor int64, key string, now time.Time) error {
	payload, e := json.Marshal(struct {
		CouponID couponport.ID `json:"coupon_id"`
		Actor    int64         `json:"actor"`
		Status   string        `json:"status"`
	}{item.ID, actor, item.Status})
	if e != nil {
		return e
	}
	d := sha256.Sum256([]byte("coupon." + operation + "\x00" + key))
	_, e = s.events.Append(ctx, couponport.Event{Type: eventType(operation), Payload: payload, OccurredAt: now, IdempotencyKey: "coupon." + operation + ":" + hex.EncodeToString(d[:])})
	return e
}

func (s *Service) mutate(ctx context.Context, operation string, input couponport.UpsertCommand, desired string, draftOnly bool) (couponport.Coupon, error) {
	minimumKeyLength := 1
	if operation == "create" || operation == "update" {
		minimumKeyLength = 16
	}
	if !ready(s) || input.Actor < 1 || len(input.IdempotencyKey) < minimumKeyLength || len(input.IdempotencyKey) > 128 || strings.TrimSpace(input.IdempotencyKey) != input.IdempotencyKey {
		return couponport.Coupon{}, ErrInvalidCoupon
	}
	now := s.now().UTC()
	if now.IsZero() {
		return couponport.Coupon{}, ErrUnavailable
	}
	var command couponport.UpsertCommand
	var productIDs []int64
	var err error
	if operation == "create" || operation == "update" {
		command, productIDs, err = normalize(input)
		if err != nil {
			return couponport.Coupon{}, err
		}
	} else {
		command = input
	}
	digestRaw, _ := json.Marshal(struct {
		Operation string
		Command   couponport.UpsertCommand
		Desired   string
	}{operation, command, desired})
	reservation := Reservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", input.Actor), KeyDigest: sha256.Sum256([]byte(input.IdempotencyKey)), PayloadDigest: sha256.Sum256(digestRaw), CreatedAt: now}
	var result couponport.Coupon
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.Reserve(tx, reservation)
		if e != nil {
			return e
		}
		if !receiptMatches(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil || !validStored(result) {
				return ErrUnavailable
			}
			return nil
		}
		changed := true
		switch operation {
		case "create":
			result, e = s.store.Create(tx, command, productIDs, now)
		case "update":
			var old couponport.Coupon
			old, e = s.store.Lock(tx, command.ID)
			if e == nil && (old.HistoryOnly || old.Status == "archived") {
				e = ErrConflict
			}
			if e == nil && draftOnly {
				if old.Status != "draft" || old.IssuedCount != 0 {
					e = ErrConflict
				}
			} else if e == nil {
				if old.Status != "draft" && old.IssuedCount == 0 {
					e = ErrConflict
				}
				if e == nil && old.IssuedCount > 0 && !claimedUpdateAllowed(old, command.Coupon) {
					e = ErrRulesFrozen
				}
			}
			if e == nil {
				result, e = s.store.Update(tx, command, productIDs, now)
			}
		case "publish", "stop":
			var old couponport.Coupon
			old, e = s.store.Lock(tx, input.ID)
			if e == nil && old.Status == desired {
				result = old
				changed = false
			}
			if e == nil && changed && (operation == "publish" && old.Status != "draft" || operation == "stop" && old.Status != "published") {
				e = ErrConflict
			}
			if e == nil && changed && operation == "publish" {
				e = s.validateProducts(tx, old)
			}
			if e == nil && changed {
				result, e = s.store.SetStatus(tx, input.ID, desired, input.Actor, now)
			}
		default:
			e = ErrInvalidCoupon
		}
		if e != nil {
			return e
		}
		if !validStored(result) {
			return ErrUnavailable
		}
		result = withAvailability(result, now)
		if changed {
			payload, e := json.Marshal(struct {
				CouponID couponport.ID `json:"coupon_id"`
				Actor    int64         `json:"actor"`
				Status   string        `json:"status"`
			}{result.ID, input.Actor, result.Status})
			if e != nil {
				return e
			}
			d := sha256.Sum256([]byte(reservation.ActorScope + "\x00" + operation + "\x00" + input.IdempotencyKey))
			if _, e = s.events.Append(tx, couponport.Event{Type: eventType(operation), Payload: payload, OccurredAt: now, IdempotencyKey: "coupon." + operation + ":" + hex.EncodeToString(d[:])}); e != nil {
				return e
			}
		}
		snapshot, e := json.Marshal(result)
		if e != nil {
			return e
		}
		completed, e := s.store.Complete(tx, receipt.ID, snapshot, now)
		if e != nil || completed.State != "completed" || !jsonEquivalent(completed.ResultSnapshot, snapshot) {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return couponport.Coupon{}, classify(err)
	}
	return result, nil
}

func normalize(input couponport.UpsertCommand) (couponport.UpsertCommand, []int64, error) {
	c := &input.Coupon
	c.Name = strings.TrimSpace(c.Name)
	c.Instructions = strings.TrimSpace(c.Instructions)
	c.ClaimStartsAt = canonicalTime(c.ClaimStartsAt)
	c.ClaimEndsAt = canonicalTime(c.ClaimEndsAt)
	if c.UseStartsAt != nil {
		value := canonicalTime(*c.UseStartsAt)
		c.UseStartsAt = &value
	}
	if c.UseEndsAt != nil {
		value := canonicalTime(*c.UseEndsAt)
		c.UseEndsAt = &value
	}
	c.Currency = "CNY"
	c.Status = "draft"
	if c.Name == "" || len(c.Name) > 45 || c.DiscountAmountTotal < 1 || c.TotalIssueLimit < 1 || c.PerUserIssueLimit < 1 || c.PerUserIssueLimit > c.TotalIssueLimit || !c.ClaimEndsAt.After(c.ClaimStartsAt) || len(c.Instructions) > 200 || len(c.TargetRefs) < 1 || len(c.TargetRefs) > 100 {
		return couponport.UpsertCommand{}, nil, ErrInvalidCoupon
	}
	if c.ValidityMode == couponport.ValidityFixedRange {
		if c.UseStartsAt == nil || c.UseEndsAt == nil || !c.UseEndsAt.After(*c.UseStartsAt) || c.RelativeValidityDays != nil {
			return couponport.UpsertCommand{}, nil, ErrInvalidCoupon
		}
	} else if c.ValidityMode == couponport.ValidityRelativeDays {
		if c.UseStartsAt != nil || c.UseEndsAt != nil || c.RelativeValidityDays == nil || *c.RelativeValidityDays < 1 {
			return couponport.UpsertCommand{}, nil, ErrInvalidCoupon
		}
	} else {
		return couponport.UpsertCommand{}, nil, ErrInvalidCoupon
	}
	seen := map[string]bool{}
	ids := make([]int64, len(c.TargetRefs))
	for i, ref := range c.TargetRefs {
		if seen[ref] {
			return couponport.UpsertCommand{}, nil, ErrInvalidTarget
		}
		seen[ref] = true
		parts := strings.Split(ref, ":")
		if len(parts) != 2 || (parts[0] != "standard_product" && parts[0] != "service_period") {
			return couponport.UpsertCommand{}, nil, ErrInvalidTarget
		}
		id, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || id < 1 || parts[1] != strconv.FormatInt(id, 10) {
			return couponport.UpsertCommand{}, nil, ErrInvalidTarget
		}
		ids[i] = id
	}
	return input, ids, nil
}

func canonicalTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func (s *Service) validateProducts(ctx context.Context, c couponport.Coupon) error {
	seen := map[string]bool{}
	for _, ref := range c.TargetRefs {
		parts := strings.Split(ref, ":")
		if len(parts) != 2 || (parts[0] != "standard_product" && parts[0] != "service_period") {
			return ErrInvalidTarget
		}
		id, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || id < 1 || seen[ref] {
			return ErrInvalidTarget
		}
		seen[ref] = true
		kind := productport.ProductOptionStandard
		if parts[0] == "service_period" {
			kind = productport.ProductOptionServicePeriod
		}
		p, e := s.products.ReadProductTarget(ctx, kind, productport.ID(id))
		if e != nil || p.ID != productport.ID(id) || p.ProductType != kind || p.Currency != "CNY" || p.PriceMinor <= c.DiscountAmountTotal {
			return ErrInvalidTarget
		}
	}
	return nil
}
func claimedUpdateAllowed(old, next couponport.Coupon) bool {
	return next.TotalIssueLimit >= old.TotalIssueLimit && old.Name == next.Name && old.DiscountAmountTotal == next.DiscountAmountTotal && old.PerUserIssueLimit == next.PerUserIssueLimit && old.ClaimStartsAt.Equal(next.ClaimStartsAt) && old.ClaimEndsAt.Equal(next.ClaimEndsAt) && old.ValidityMode == next.ValidityMode && sameTime(old.UseStartsAt, next.UseStartsAt) && sameTime(old.UseEndsAt, next.UseEndsAt) && reflect.DeepEqual(old.RelativeValidityDays, next.RelativeValidityDays) && old.Instructions == next.Instructions && reflect.DeepEqual(old.TargetRefs, next.TargetRefs)
}
func sameTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
func validStored(c couponport.Coupon) bool {
	if c.ID < 1 || c.CreatedBy < 1 || c.UpdatedBy < 1 || c.Version < 1 || c.IssuedCount < 0 || c.IssuedCount > c.TotalIssueLimit || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return false
	}
	_, _, e := normalize(couponport.UpsertCommand{Coupon: c, Actor: c.UpdatedBy, IdempotencyKey: strings.Repeat("v", 16)})
	return e == nil
}
func withAvailability(c couponport.Coupon, now time.Time) couponport.Coupon {
	c.AvailabilityStatus = couponport.AvailabilityStatusAt(c, now)
	return c
}
func ready(s *Service) bool {
	return s != nil && s.uow != nil && s.store != nil && s.products != nil && s.events != nil
}
func receiptMatches(r Receipt, x Reservation) bool {
	return r.ID > 0 && r.Operation == x.Operation && r.ActorScope == x.ActorScope && subtle.ConstantTimeCompare(r.KeyDigest[:], x.KeyDigest[:]) == 1
}
func jsonEquivalent(a, b []byte) bool {
	var x, y any
	da := json.NewDecoder(strings.NewReader(string(a)))
	db := json.NewDecoder(strings.NewReader(string(b)))
	da.UseNumber()
	db.UseNumber()
	return da.Decode(&x) == nil && db.Decode(&y) == nil && reflect.DeepEqual(x, y)
}
func classify(e error) error {
	switch {
	case errors.Is(e, ErrInvalidCoupon), errors.Is(e, ErrInvalidTarget), errors.Is(e, ErrNotFound), errors.Is(e, ErrConflict), errors.Is(e, ErrRulesFrozen):
		return e
	default:
		return ErrUnavailable
	}
}
func eventType(operation string) string {
	switch operation {
	case "create":
		return couponport.EventCouponCreated
	case "update":
		return couponport.EventCouponUpdated
	case "publish":
		return couponport.EventCouponPublished
	case "stop":
		return couponport.EventCouponStopped
	case "archive":
		return couponport.EventCouponArchived
	case "delete":
		return couponport.EventCouponDeleted
	case "copy":
		return couponport.EventCouponCopied
	default:
		return ""
	}
}
