package app

import (
	"context"
	"errors"
	"testing"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
)

func TestFixedCouponRuleAvailabilityEndsAtUseDeadline(t *testing.T) {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	for _, scenario := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"before_deadline", deadline.Add(-time.Microsecond), "active"},
		{"at_deadline", deadline, "ended"},
		{"after_deadline", deadline.Add(time.Microsecond), "ended"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, events := newCouponTestStore(), &couponTestEvents{}
			service := couponTestService(scenario.at, store, events)
			item := couponTestItem(7, now)
			start := now.Add(-time.Hour)
			item.ClaimEndsAt = deadline.Add(24 * time.Hour)
			item.ValidityMode = couponport.ValidityFixedRange
			item.RelativeValidityDays = nil
			item.UseStartsAt, item.UseEndsAt = &start, &deadline
			store.coupons[item.ID] = item
			read, err := service.Get(context.Background(), item.ID)
			if err != nil || read.Status != "published" || read.AvailabilityStatus != scenario.want {
				t.Fatalf("rule lifecycle=%s availability=%s want=%s err=%v", read.Status, read.AvailabilityStatus, scenario.want, err)
			}
			stats, err := service.Stats(context.Background(), item.ID)
			if err != nil || stats.AvailabilityStatus != scenario.want {
				t.Fatalf("stats=%+v want=%s err=%v", stats, scenario.want, err)
			}
			if len(events.rows) != 0 || store.coupons[item.ID].Status != "published" {
				t.Fatal("availability read changed the durable rule")
			}
		})
	}
}

func TestCouponAvailabilityKeepsFutureFixedUseAndRelativeClaimsOpen(t *testing.T) {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	item := couponTestItem(7, now)
	start, end := now.Add(24*time.Hour), now.Add(48*time.Hour)
	item.ValidityMode, item.RelativeValidityDays = couponport.ValidityFixedRange, nil
	item.UseStartsAt, item.UseEndsAt = &start, &end
	if got := couponport.AvailabilityStatusAt(item, now); got != "active" {
		t.Fatalf("future fixed use must still permit advance claiming: %s", got)
	}
	// Relative validity starts at each successful claim; it must not acquire a
	// global deadline from the definition's fixed-range-only fields.
	item = couponTestItem(7, now)
	if got := couponport.AvailabilityStatusAt(item, now); got != "active" {
		t.Fatalf("relative claim availability=%s", got)
	}
	item.ClaimStartsAt = now.Add(time.Hour)
	if got := couponport.AvailabilityStatusAt(item, now); got != "scheduled" {
		t.Fatalf("scheduled claim availability=%s", got)
	}
	item.ClaimStartsAt, item.ClaimEndsAt = now.Add(-time.Hour), now
	if got := couponport.AvailabilityStatusAt(item, now); got != "ended" {
		t.Fatalf("claim-window deadline availability=%s", got)
	}
}

type fixedDeadlineClaimOwner struct {
	CheckoutStore // Other methods are intentionally absent and never invoked.
	calls         int
	claimedAt     time.Time
}

func (s *fixedDeadlineClaimOwner) ClaimCoupon(_ context.Context, _ couponport.ClaimCommand, _, _ [32]byte, at time.Time) (couponport.CustomerCoupon, error) {
	s.calls++
	s.claimedAt = at
	return couponport.CustomerCoupon{}, ErrNoEligibleCoupon
}

// The fixed validity calculation in the durable owner is reviewed statically.
// This fake proves that Claim preserves the owner's refusal; it does not
// substitute for a PostgreSQL claim-validity or checkout integration test.
func TestCouponClaimPreservesOwnerRefusalAtFixedUseDeadline(t *testing.T) {
	deadline := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	owner := &fixedDeadlineClaimOwner{}
	service, err := NewCheckoutService(couponTestUOW{}, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Claim(context.Background(), couponport.ClaimCommand{CouponID: 7, HolderCustomerID: 9, ActorScope: "coupon-public:fixed-end", IdempotencyKey: "fixed-end-claim-key-0001", ClaimedAt: deadline})
	if !errors.Is(err, ErrNoEligibleCoupon) || owner.calls != 1 || !owner.claimedAt.Equal(deadline) {
		t.Fatalf("claim refusal err=%v calls=%d at=%s", err, owner.calls, owner.claimedAt)
	}
}
