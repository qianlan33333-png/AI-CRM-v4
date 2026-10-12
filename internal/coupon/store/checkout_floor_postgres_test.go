package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLCouponFloorPreservesFaceAndConsumesActualDiscount(t *testing.T) {
	pool, cleanup := couponIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, _ := platformpostgres.Wrap(pool, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapped)
	repo, _ := NewPostgreSQL(pool, uow)
	checkout, _ := couponapp.NewCheckoutService(uow, repo)
	now := time.Now().UTC()
	rule := checkoutPublishedRule(t, ctx, uow, repo, now, 20, "floor-boundary")
	if _, e := pool.Exec(ctx, `INSERT INTO coupon_rule_targets(coupon_id,target_ref,position) VALUES($1,'service_period:9',1)`, rule.ID); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"standard_product", "service_period"} {
		for _, gross := range []int64{1, 2, 99, 100, 101, 200} {
			t.Run(fmt.Sprintf("%s/%d", kind, gross), func(t *testing.T) {
				holder := checkoutCustomer(t, pool)
				key := fmt.Sprintf("coupon-floor-%s-%d", kind, gross)
				claim, e := checkout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: holder, ActorScope: "floor-holder", IdempotencyKey: key + "-claim", ClaimedAt: now})
				if e != nil {
					t.Fatal(e)
				}
				c := couponport.ReserveCommand{HolderCustomerID: holder, ProductID: 9, ProductType: kind, ProductCode: "floor-product", GrossAmountMinor: gross, Currency: "CNY", OrderReference: key, ActorScope: "floor-checkout", IdempotencyKey: key + "-reserve", ReservedAt: now}
				var got couponport.ReservationSnapshot
				e = uow.Within(ctx, func(tx context.Context) error { var e error; got, e = checkout.ReserveWithin(tx, c); return e })
				if e != nil {
					t.Fatal(e)
				}
				if gross == 1 {
					if got.CouponApplied || got.PayableAmountMinor != 1 {
						t.Fatalf("one cent result=%+v", got)
					}
					var status string
					if e = pool.QueryRow(ctx, `SELECT status FROM coupon_customer_claims WHERE id=$1`, claim.ClaimID).Scan(&status); e != nil || status != "available" {
						t.Fatalf("claim touched=%s err=%v", status, e)
					}
					return
				}
				expectedDiscount := min(int64(100), gross-1)
				if !got.CouponApplied || got.DiscountAmountMinor != expectedDiscount || got.PayableAmountMinor != gross-expectedDiscount {
					t.Fatalf("floor=%+v", got)
				}
				e = uow.Within(ctx, func(tx context.Context) error {
					_, e := checkout.ConsumeWithin(tx, couponport.ConsumeCommand{ReservationRef: got.ReservationRef, OrderReference: c.OrderReference, SettledAmountMinor: got.PayableAmountMinor, SettledCurrency: "CNY", ActorScope: "floor-consume", IdempotencyKey: key + "-consume", SettledAt: now})
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
				var face, discount, payable int64
				var status string
				e = pool.QueryRow(ctx, `SELECT r.discount_amount_total,d.discount_amount_minor,d.payable_amount_minor,c.status FROM coupon_rules r JOIN coupon_customer_claims c ON c.coupon_id=r.id JOIN coupon_order_redemptions d ON d.claim_id=c.id WHERE c.id=$1`, claim.ClaimID).Scan(&face, &discount, &payable, &status)
				if e != nil || face != 100 || discount != expectedDiscount || payable != got.PayableAmountMinor || status != "redeemed" {
					t.Fatalf("persisted face=%d discount=%d payable=%d status=%s err=%v", face, discount, payable, status, e)
				}
			})
		}
	}
}
