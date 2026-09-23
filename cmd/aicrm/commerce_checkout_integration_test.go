package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type commerceCheckoutProductFacts map[int64]productport.ProductOption

func (facts commerceCheckoutProductFacts) ReadProductTarget(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	fact, ok := facts[int64(id)]
	if !ok || fact.ProductType != kind {
		return productport.ProductOption{}, errors.New("missing product fact")
	}
	return fact, nil
}

// TestPostgreSQLCommerceNoCouponReplayRetainsCheckoutPrice runs both owner
// stores through the real composition migration sequence. It verifies Coupon's
// no-eligible result creates no redemption fact and that an Order receipt
// prevents a later eligible coupon from changing that original checkout.
func TestPostgreSQLCommerceNoCouponReplayRetainsCheckoutPrice(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	coupons, err := couponstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	couponCheckout, err := couponapp.NewCheckoutService(uow, coupons)
	if err != nil {
		t.Fatal(err)
	}
	orderService := orderapp.NewService(uow, orders)
	if err = orderService.SetCheckoutCouponCoordinator(couponCheckout); err != nil {
		t.Fatal(err)
	}

	var customerID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	command := orderport.PaymentOrderCommand{
		Provider: domain.ProviderWeChatPay, MerchantOrderNo: "commerce-no-coupon-replay-001",
		PayerCustomerID: customerID, BeneficiaryCustomerID: customerID,
		ProductID: 9, ProductCode: "course-9", ProductName: "九号课程", ProductVersion: 2,
		ProductType: "standard_product", UnitAmountMinor: 8800, Currency: "CNY",
		ActorScope: "payment-session:commerce-no-coupon", IdempotencyKey: "commerce-no-coupon-replay-key-0001",
	}
	var first domain.Snapshot
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var createErr error
		first, createErr = orderService.CreatePaymentOrderWithin(txctx, command)
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	var redemptions, checkoutReceipts int
	var applied bool
	var gross, discount, payable int64
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_order_redemptions),(SELECT count(*) FROM coupon_claim_operation_receipts)`).Scan(&redemptions, &checkoutReceipts); err != nil || redemptions != 0 || checkoutReceipts != 0 {
		t.Fatalf("first checkout coupon facts redemptions=%d receipts=%d err=%v", redemptions, checkoutReceipts, err)
	}
	if err = native.QueryRow(ctx, `SELECT coupon_applied,gross_amount_minor,discount_amount_minor,payable_amount_minor FROM order_checkout_snapshots WHERE order_id=$1`, first.ID).Scan(&applied, &gross, &discount, &payable); err != nil || applied || gross != 8800 || discount != 0 || payable != 8800 {
		t.Fatalf("first snapshot applied=%t gross=%d discount=%d payable=%d err=%v", applied, gross, discount, payable, err)
	}

	now := time.Now().UTC()
	days := int32(7)
	rules := couponapp.NewService(uow, coupons, commerceCheckoutProductFacts{9: {ID: 9, ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 8800}}, coupons)
	rule, err := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{
		Name: "后来可用券", DiscountAmountTotal: 1000, TotalIssueLimit: 5, PerUserIssueLimit: 1,
		ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays,
		RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"},
	}, Actor: 1, IdempotencyKey: "commerce-no-coupon-rule-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rules.Publish(ctx, rule.ID, 1, "commerce-no-coupon-rule-publish-001"); err != nil {
		t.Fatal(err)
	}
	if _, err = couponCheckout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: customerID, ActorScope: "commerce-no-coupon-claim", IdempotencyKey: "commerce-no-coupon-claim-key-0001", ClaimedAt: now}); err != nil {
		t.Fatal(err)
	}

	var replay domain.Snapshot
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var replayErr error
		replay, replayErr = orderService.CreatePaymentOrderWithin(txctx, command)
		return replayErr
	}); err != nil {
		t.Fatal(err)
	}
	if replay.ID != first.ID || replay.Amount.AmountMinor != 8800 {
		t.Fatalf("replay=%+v first=%+v", replay, first)
	}
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_order_redemptions),(SELECT count(*) FROM coupon_claim_operation_receipts)`).Scan(&redemptions, &checkoutReceipts); err != nil || redemptions != 0 || checkoutReceipts != 1 {
		t.Fatalf("replay must not reserve later coupon redemptions=%d receipts=%d err=%v", redemptions, checkoutReceipts, err)
	}
	if err = native.QueryRow(ctx, `SELECT coupon_applied,gross_amount_minor,discount_amount_minor,payable_amount_minor FROM order_checkout_snapshots WHERE order_id=$1`, first.ID).Scan(&applied, &gross, &discount, &payable); err != nil || applied || gross != 8800 || discount != 0 || payable != 8800 {
		t.Fatalf("replayed snapshot applied=%t gross=%d discount=%d payable=%d err=%v", applied, gross, discount, payable, err)
	}
}
