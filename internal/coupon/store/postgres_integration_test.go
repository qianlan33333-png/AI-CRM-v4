package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type productFacts map[int64]productport.ProductOption

func (p productFacts) ReadProductTarget(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	x, ok := p[int64(id)]
	if !ok {
		return productport.ProductOption{}, errors.New("missing")
	}
	if x.ProductType != kind {
		return productport.ProductOption{}, errors.New("wrong product type")
	}
	return x, nil
}

func TestPostgreSQLCouponRulesAtomicReceiptAuditAndOutbox(t *testing.T) {
	native, cleanup := couponIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, table := range []string{"coupon_rules", "coupon_rule_targets", "coupon_operation_receipts", "coupon_audit_events", "coupon_outbox"} {
		var owned bool
		if err := native.QueryRow(ctx, `SELECT tableowner=current_user FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1`, table).Scan(&owned); err != nil || !owned {
			t.Fatalf("table %s owner=%t err=%v", table, owned, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := couponapp.NewService(uow, repository, productFacts{9: {ID: 9, ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 1000}}, repository)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	days := int32(7)
	input := couponport.UpsertCommand{Coupon: couponport.Coupon{Name: "新客券", DiscountAmountTotal: 100, TotalIssueLimit: 10, PerUserIssueLimit: 1, ClaimStartsAt: now, ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"}}, Actor: 7, IdempotencyKey: "coupon-pg-create-key-0001"}
	created, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Create(ctx, input)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	published, err := service.Publish(ctx, created.ID, 7, "coupon-pg-publish-key-01")
	if err != nil {
		t.Fatal(err)
	}
	archived, err := service.Archive(ctx, created.ID, published.Version, 7, "coupon-pg-archive-key-01")
	if err != nil || archived.Status != "archived" {
		t.Fatalf("archive=%+v err=%v", archived, err)
	}
	if replay, replayErr := service.Archive(ctx, created.ID, published.Version, 7, "coupon-pg-archive-key-01"); replayErr != nil || replay.ID != archived.ID || replay.Version != archived.Version || replay.Status != "archived" {
		t.Fatalf("archive replay=%+v err=%v", replay, replayErr)
	}
	if _, err = service.Archive(ctx, created.ID, published.Version, 7, "coupon-pg-archive-stale-01"); !errors.Is(err, couponapp.ErrConflict) {
		t.Fatalf("stale archive error=%v", err)
	}
	retained, err := service.Get(ctx, created.ID)
	if err != nil || retained.ID != created.ID || retained.Status != "archived" || retained.Version != archived.Version {
		t.Fatalf("retained archive=%+v err=%v", retained, err)
	}
	if _, err = service.Copy(ctx, created.ID, 7, "coupon-pg-copy-archived-01"); !errors.Is(err, couponapp.ErrConflict) {
		t.Fatalf("archived copy error=%v", err)
	}
	if _, err = service.Publish(ctx, created.ID, 7, "coupon-pg-publish-archived-01"); !errors.Is(err, couponapp.ErrConflict) {
		t.Fatalf("archived publish error=%v", err)
	}
	var repositoryItems []couponport.Coupon
	if err = uow.Within(ctx, func(txCtx context.Context) error {
		repositoryItems, err = repository.List(txCtx, 50, 0, "", "")
		return err
	}); err != nil {
		t.Fatalf("repository list persisted coupons: %v", err)
	}
	if len(repositoryItems) != 0 {
		t.Fatalf("repository list=%#v", repositoryItems)
	}
	page, err := service.List(ctx, 50, 0, "", "")
	if err != nil {
		t.Fatalf("list persisted coupons: %v", err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("normal list must exclude archived coupon: %#v", page)
	}
	historyPage, err := service.List(ctx, 50, 0, "", "archived")
	if err != nil || historyPage.Total != 1 || len(historyPage.Items) != 1 || historyPage.Items[0].ID != created.ID || len(historyPage.Items[0].TargetRefs) != 1 || historyPage.Items[0].TargetRefs[0] != "standard_product:9" {
		t.Fatalf("retained history list=%#v err=%v", historyPage, err)
	}
	var rules, targets, receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_rules),(SELECT count(*) FROM coupon_rule_targets),(SELECT count(*) FROM coupon_operation_receipts),(SELECT count(*) FROM coupon_audit_events),(SELECT count(*) FROM coupon_outbox)`).Scan(&rules, &targets, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if rules != 1 || targets != 1 || receipts != 3 || audits != 3 || outbox != 3 {
		t.Fatalf("rows rules=%d targets=%d receipts=%d audits=%d outbox=%d", rules, targets, receipts, audits, outbox)
	}
	if _, err = native.Exec(ctx, `UPDATE coupon_audit_events SET event_type=event_type`); err == nil {
		t.Fatal("audit update unexpectedly succeeded")
	}
	// Outbox delivery bookkeeping deliberately remains mutable by its future
	// owner; only the audit ledger is database-append-only.
}

func TestPostgreSQLCouponPublicSlugIsStableAuditedAndNeverNumeric(t *testing.T) {
	native, cleanup := couponIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	rule := checkoutPublishedRule(t, ctx, uow, repository, now, 2, "public-slug")
	second := checkoutPublishedRule(t, ctx, uow, repository, now, 2, "public-slug-second")
	public, err := couponapp.NewPublicCouponService(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	first, err := public.EnsurePublicShare(ctx, rule.ID, 7)
	if err != nil || first.CouponID != rule.ID || !strings.HasPrefix(first.PublicSlug, "cp-") || first.URL != "/c/"+first.PublicSlug {
		t.Fatalf("first share=%+v err=%v", first, err)
	}
	replayed, err := public.EnsurePublicShare(ctx, rule.ID, 7)
	if err != nil || replayed != first {
		t.Fatalf("share replay=%+v err=%v", replayed, err)
	}
	lookedUp, err := public.GetPublicCoupon(ctx, first.PublicSlug)
	if err != nil || lookedUp.ID != rule.ID || lookedUp.PublicSlug != first.PublicSlug || len(lookedUp.TargetRefs) != 1 || lookedUp.TargetRefs[0] != "standard_product:9" {
		t.Fatalf("public lookup=%+v err=%v", lookedUp, err)
	}
	if _, err = native.Exec(ctx, `UPDATE coupon_rules SET public_slug=$2 WHERE id=$1`, second.ID, first.PublicSlug); err == nil {
		t.Fatal("database accepted duplicate public slug")
	}
	if _, err = native.Exec(ctx, `UPDATE coupon_rules SET public_slug='cp-z9y8x7' WHERE id=$1`, rule.ID); err == nil {
		t.Fatal("database accepted public slug mutation")
	}
	var auditCount, outboxCount int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_audit_events WHERE event_type='coupon.public_shared'),(SELECT count(*) FROM coupon_outbox WHERE event_type='coupon.public_shared')`).Scan(&auditCount, &outboxCount); err != nil || auditCount != 1 || outboxCount != 1 {
		t.Fatalf("share audit=%d outbox=%d err=%v", auditCount, outboxCount, err)
	}
}

func TestPostgreSQLCouponCheckoutConcurrentClaimReservationAndSettlement(t *testing.T) {
	native, cleanup := couponIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := couponapp.NewCheckoutService(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first, second, third := checkoutCustomer(t, native), checkoutCustomer(t, native), checkoutCustomer(t, native)

	// Distinct callers compete for the final available issue. PostgreSQL row
	// locks choose exactly one winner; no mock covers this concurrency fence.
	lastRule := checkoutPublishedRule(t, ctx, uow, repository, now, 1, "checkout-last")
	lastResults := make(chan error, 2)
	for i, holder := range []int64{first, second} {
		go func(i int, holder int64) {
			_, claimErr := checkout.Claim(ctx, couponport.ClaimCommand{CouponID: lastRule.ID, HolderCustomerID: holder, ActorScope: "checkout:last", IdempotencyKey: "checkout-last-claim-key-" + string(rune('a'+i)) + "000", ClaimedAt: now})
			lastResults <- claimErr
		}(i, holder)
	}
	var lastSuccess, lastUnavailable int
	for range 2 {
		err = <-lastResults
		switch {
		case err == nil:
			lastSuccess++
		case errors.Is(err, couponapp.ErrNoEligibleCoupon):
			lastUnavailable++
		default:
			t.Fatalf("last coupon error=%v", err)
		}
	}
	if lastSuccess != 1 || lastUnavailable != 1 {
		t.Fatalf("last coupon success=%d unavailable=%d", lastSuccess, lastUnavailable)
	}
	var originalPrice couponport.ReservationSnapshot
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var reserveErr error
		originalPrice, reserveErr = checkout.ReserveWithin(txctx, couponport.ReserveCommand{HolderCustomerID: third, ProductID: 10, ProductType: "standard_product", ProductCode: "standard-10", GrossAmountMinor: 1000, Currency: "CNY", OrderReference: "order-coupon-auto-none-001", ActorScope: "payment:checkout", IdempotencyKey: "checkout-auto-none-reserve-0001", ReservedAt: now})
		return reserveErr
	}); err != nil || originalPrice.CouponApplied || originalPrice.ReservationRef != "" || originalPrice.PayableAmountMinor != 1000 {
		t.Fatalf("automatic no-coupon snapshot=%+v err=%v", originalPrice, err)
	}

	// Equal key and scope must replay after the advisory lock, even under a
	// concurrent first request. Different scope/holder with that browser key
	// is independently valid and cannot collide in the canonical claim table.
	rule := checkoutPublishedRule(t, ctx, uow, repository, now, 4, "checkout-main")
	claimCommand := couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: first, ActorScope: "checkout:payer:one", IdempotencyKey: "checkout-same-claim-key-0001", ClaimedAt: now}
	var claims [2]couponport.CustomerCoupon
	claimErrs := make(chan error, 2)
	var claimWait sync.WaitGroup
	for i := range claims {
		claimWait.Add(1)
		go func(index int) {
			defer claimWait.Done()
			claim, claimErr := checkout.Claim(ctx, claimCommand)
			claims[index] = claim
			claimErrs <- claimErr
		}(i)
	}
	claimWait.Wait()
	close(claimErrs)
	for claimErr := range claimErrs {
		if claimErr != nil {
			t.Fatalf("same-key concurrent claim: %v", claimErr)
		}
	}
	if claims[0].ClaimID == 0 || claims[0].ClaimID != claims[1].ClaimID {
		t.Fatalf("same-key claims=%+v", claims)
	}
	// The replay is deliberately after both the original claim window and the
	// relative validity boundary. Its frozen receipt must win before a fresh
	// eligibility check, while a changed holder remains a true conflict.
	laterClaim := claimCommand
	laterClaim.ClaimedAt = now.AddDate(0, 0, 4)
	if replayed, replayErr := checkout.Claim(ctx, laterClaim); replayErr != nil || replayed.ClaimID != claims[0].ClaimID {
		t.Fatalf("same-key later claim replay=%+v err=%v", replayed, replayErr)
	}
	if _, err = checkout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: second, ActorScope: claimCommand.ActorScope, IdempotencyKey: claimCommand.IdempotencyKey, ClaimedAt: laterClaim.ClaimedAt}); !errors.Is(err, couponapp.ErrConflict) {
		t.Fatalf("same key changed payload error=%v", err)
	}
	for _, command := range []couponport.ClaimCommand{
		{CouponID: rule.ID, HolderCustomerID: second, ActorScope: "checkout:payer:two", IdempotencyKey: claimCommand.IdempotencyKey, ClaimedAt: now},
		{CouponID: rule.ID, HolderCustomerID: third, ActorScope: "checkout:payer:three", IdempotencyKey: claimCommand.IdempotencyKey, ClaimedAt: now},
	} {
		if _, err = checkout.Claim(ctx, command); err != nil {
			t.Fatalf("same client key different scope: %v", err)
		}
	}
	var distinctNativeClaims int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1`, rule.ID).Scan(&distinctNativeClaims); err != nil || distinctNativeClaims != 3 {
		t.Fatalf("scoped claims=%d err=%v", distinctNativeClaims, err)
	}

	reserve := couponport.ReserveCommand{HolderCustomerID: first, ClaimID: claims[0].ClaimID, ProductID: 9, ProductType: "standard_product", ProductCode: "standard-9", GrossAmountMinor: 1000, Currency: "CNY", OrderReference: "order-coupon-checkout-001", ActorScope: "payment:checkout", IdempotencyKey: "checkout-same-reserve-key-0001", ReservedAt: now.Add(time.Minute)}
	var reservations [2]couponport.ReservationSnapshot
	reservationErrs := make(chan error, 2)
	var reserveWait sync.WaitGroup
	for i := range reservations {
		reserveWait.Add(1)
		go func(index int) {
			defer reserveWait.Done()
			reservationErrs <- uow.Within(ctx, func(txctx context.Context) error {
				var reserveErr error
				reservations[index], reserveErr = checkout.ReserveWithin(txctx, reserve)
				return reserveErr
			})
		}(i)
	}
	reserveWait.Wait()
	close(reservationErrs)
	for reserveErr := range reservationErrs {
		if reserveErr != nil {
			t.Fatalf("same-key concurrent reserve: %v", reserveErr)
		}
	}
	if !reservations[0].CouponApplied || reservations[0].ReservationRef == "" || reservations[0] != reservations[1] || reservations[0].ProductType != "standard_product" || reservations[0].DiscountAmountMinor != 100 || reservations[0].PayableAmountMinor != 900 {
		t.Fatalf("frozen reservation=%+v / %+v", reservations[0], reservations[1])
	}
	laterReserve := reserve
	laterReserve.ReservedAt = now.AddDate(0, 0, 4)
	if err = uow.Within(ctx, func(txctx context.Context) error {
		replayed, reserveErr := checkout.ReserveWithin(txctx, laterReserve)
		if reserveErr != nil {
			return reserveErr
		}
		if replayed != reservations[0] {
			return fmt.Errorf("same-key later reserve replay=%+v", replayed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changedReserve := laterReserve
	changedReserve.GrossAmountMinor++
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, reserveErr := checkout.ReserveWithin(txctx, changedReserve)
		if !errors.Is(reserveErr, couponapp.ErrConflict) {
			return fmt.Errorf("same key changed reserve payload error=%v", reserveErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// An enclosing order UoW failure must leave neither an order reservation nor
	// a claim state transition behind.
	rollbackClaim, err := checkout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: second, ActorScope: "checkout:rollback", IdempotencyKey: "checkout-rollback-claim-0001", ClaimedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	rollback := reserve
	rollback.HolderCustomerID, rollback.ClaimID, rollback.OrderReference, rollback.IdempotencyKey = second, rollbackClaim.ClaimID, "order-coupon-rollback-001", "checkout-rollback-reserve-0001"
	rollbackReserved := false
	if err = uow.Within(ctx, func(txctx context.Context) error {
		if _, reserveErr := checkout.ReserveWithin(txctx, rollback); reserveErr != nil {
			return reserveErr
		}
		rollbackReserved = true
		return errors.New("force enclosing order rollback")
	}); err == nil {
		t.Fatal("rollback unexpectedly committed")
	}
	if !rollbackReserved {
		t.Fatal("rollback test did not execute reservation before forcing rollback")
	}
	var rollbackStatus string
	var rollbackRedemptions int
	if err = native.QueryRow(ctx, `SELECT status FROM coupon_customer_claims WHERE id=$1`, rollbackClaim.ClaimID).Scan(&rollbackStatus); err != nil || rollbackStatus != "available" {
		t.Fatalf("rollback claim status=%q err=%v", rollbackStatus, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM coupon_order_redemptions WHERE order_reference=$1`, rollback.OrderReference).Scan(&rollbackRedemptions); err != nil || rollbackRedemptions != 0 {
		t.Fatalf("rollback redemptions=%d err=%v", rollbackRedemptions, err)
	}
	var rollbackReceipts, rollbackAudits, rollbackOutbox int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$2),
		(SELECT count(*) FROM coupon_claim_audit_events WHERE claim_id=$1 AND operation='reserve'),
		(SELECT count(*) FROM coupon_claim_outbox WHERE claim_id=$1 AND event_type='coupon.reserved.v1')`, rollbackClaim.ClaimID, rollback.OrderReference).Scan(&rollbackReceipts, &rollbackAudits, &rollbackOutbox); err != nil || rollbackReceipts != 0 || rollbackAudits != 0 || rollbackOutbox != 0 {
		// The forced failure must erase the reservation receipt, audit and outbox
		// along with the claim status transition.
		t.Fatalf("rollback lifecycle receipts=%d audits=%d outbox=%d err=%v", rollbackReceipts, rollbackAudits, rollbackOutbox, err)
	}

	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, consumeErr := checkout.ConsumeWithin(txctx, couponport.ConsumeCommand{ReservationRef: reservations[0].ReservationRef, OrderReference: reserve.OrderReference, SettledAmountMinor: 899, SettledCurrency: "CNY", ActorScope: "payment:settlement", IdempotencyKey: "checkout-wrong-settlement-0001", SettledAt: now.Add(2 * time.Minute)})
		if !errors.Is(consumeErr, couponapp.ErrConflict) {
			return fmt.Errorf("wrong settlement error=%v", consumeErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	consume := couponport.ConsumeCommand{ReservationRef: reservations[0].ReservationRef, OrderReference: reserve.OrderReference, SettledAmountMinor: 900, SettledCurrency: "CNY", ActorScope: "payment:settlement", IdempotencyKey: "checkout-settlement-0001", SettledAt: now.Add(2 * time.Minute)}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, consumeErr := checkout.ConsumeWithin(txctx, consume)
		return consumeErr
	}); err != nil {
		t.Fatal(err)
	}
	laterConsume := consume
	laterConsume.SettledAt = now.AddDate(0, 0, 4)
	if err = uow.Within(ctx, func(txctx context.Context) error {
		replayed, consumeErr := checkout.ConsumeWithin(txctx, laterConsume)
		if consumeErr != nil {
			return consumeErr
		}
		if replayed != reservations[0] {
			return fmt.Errorf("same-key later settlement replay=%+v", replayed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changedConsume := laterConsume
	changedConsume.SettledAmountMinor++
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, consumeErr := checkout.ConsumeWithin(txctx, changedConsume)
		if !errors.Is(consumeErr, couponapp.ErrConflict) {
			return fmt.Errorf("same key changed settlement payload error=%v", consumeErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var consumed string
	if err = native.QueryRow(ctx, `SELECT status FROM coupon_order_redemptions WHERE order_reference=$1`, reserve.OrderReference).Scan(&consumed); err != nil || consumed != "consumed" {
		t.Fatalf("consumed status=%q err=%v", consumed, err)
	}

	// An authoritative close releases a still-reserved claim exactly once. If
	// the close is after the claim's fixed local validity boundary, the claim
	// is terminally expired rather than becoming available again.
	fourth := checkoutCustomer(t, native)
	releaseRule := checkoutPublishedRule(t, ctx, uow, repository, now, 1, "checkout-expired-release")
	releaseClaim, err := checkout.Claim(ctx, couponport.ClaimCommand{CouponID: releaseRule.ID, HolderCustomerID: fourth, ActorScope: "checkout:expired-release", IdempotencyKey: "checkout-expired-claim-0001", ClaimedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	releaseReserve := couponport.ReserveCommand{HolderCustomerID: fourth, ClaimID: releaseClaim.ClaimID, ProductID: 9, ProductType: "standard_product", ProductCode: "standard-9", GrossAmountMinor: 1000, Currency: "CNY", OrderReference: "order-coupon-expired-release-001", ActorScope: "payment:checkout", IdempotencyKey: "checkout-expired-reserve-0001", ReservedAt: now.Add(time.Minute)}
	var releaseSnapshot couponport.ReservationSnapshot
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var reserveErr error
		releaseSnapshot, reserveErr = checkout.ReserveWithin(txctx, releaseReserve)
		return reserveErr
	}); err != nil {
		t.Fatal(err)
	}
	releaseCommand := couponport.ReleaseCommand{ReservationRef: releaseSnapshot.ReservationRef, OrderReference: releaseReserve.OrderReference, CloseReason: "payment_closed", ActorScope: "payment:close", IdempotencyKey: "checkout-expired-release-0001", ClosedAt: now.AddDate(0, 0, 4)}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, releaseErr := checkout.ReleaseWithin(txctx, releaseCommand)
		return releaseErr
	}); err != nil {
		t.Fatal(err)
	}
	laterRelease := releaseCommand
	laterRelease.ClosedAt = releaseCommand.ClosedAt.Add(time.Hour)
	if err = uow.Within(ctx, func(txctx context.Context) error {
		replayed, releaseErr := checkout.ReleaseWithin(txctx, laterRelease)
		if releaseErr != nil {
			return releaseErr
		}
		if replayed != releaseSnapshot {
			return fmt.Errorf("same-key later release replay=%+v", replayed)
		}
		return nil
	}); err != nil {
		t.Fatalf("idempotent close release: %v", err)
	}
	changedRelease := laterRelease
	changedRelease.CloseReason = "payment_failed"
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, releaseErr := checkout.ReleaseWithin(txctx, changedRelease)
		if !errors.Is(releaseErr, couponapp.ErrConflict) {
			return fmt.Errorf("same key changed close payload error=%v", releaseErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var released, expired string
	if err = native.QueryRow(ctx, `SELECT redemption.status,claim.status FROM coupon_order_redemptions redemption JOIN coupon_customer_claims claim ON claim.id=redemption.claim_id WHERE redemption.order_reference=$1`, releaseReserve.OrderReference).Scan(&released, &expired); err != nil || released != "released" || expired != "expired" {
		t.Fatalf("expired close released=%q claim=%q err=%v", released, expired, err)
	}
}

func checkoutCustomer(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func checkoutPublishedRule(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *Repository, now time.Time, total int64, key string) couponport.Coupon {
	t.Helper()
	days := int32(3)
	rules := couponapp.NewService(uow, repository, productFacts{9: {ID: 9, ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 1000}}, repository)
	created, err := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{Name: key, DiscountAmountTotal: 100, Currency: "CNY", TotalIssueLimit: total, PerUserIssueLimit: total, ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"}}, Actor: 7, IdempotencyKey: key + "-create-key-0001"})
	if err != nil {
		t.Fatal(err)
	}
	published, err := rules.Publish(ctx, created.ID, 7, key+"-publish-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func couponIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping coupon PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_coupon_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	for _, name := range []string{"0002_identity.sql", "0011_coupon_rules.sql", "0056_coupon_customer_claims.sql", "0069_coupon_claim_redemption_lifecycle.sql", "0077_coupon_public_slug.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}

func TestPostgreSQLSidebarClaimableCatalogPreservesDirectoryAndClaimFacts(t *testing.T) {
	native, cleanup := couponIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	products := productFacts{
		9:  {ID: 9, ProductType: productport.ProductOptionStandard, Name: "普通商品", Currency: "CNY", PriceMinor: 1000},
		10: {ID: 10, ProductType: productport.ProductOptionServicePeriod, Name: "周期商品", Currency: "CNY", PriceMinor: 1000},
	}
	rules := couponapp.NewService(uow, repository, products, repository)
	createPublished := func(name string, starts, ends time.Time, total, perUser int64, targets []string) couponport.Coupon {
		t.Helper()
		days := int32(3)
		created, createErr := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{
			Name: name, DiscountAmountTotal: 100, Currency: "CNY", TotalIssueLimit: total, PerUserIssueLimit: perUser,
			ClaimStartsAt: starts, ClaimEndsAt: ends, ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: targets,
		}, Actor: 7, IdempotencyKey: name + "-catalog-create-key"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		published, publishErr := rules.Publish(ctx, created.ID, 7, name+"-catalog-publish-key")
		if publishErr != nil {
			t.Fatal(publishErr)
		}
		return published
	}
	active := createPublished("catalog-active", now.Add(-time.Hour), now.Add(time.Hour), 2, 1, []string{"standard_product:9", "service_period:10"})
	scheduled := createPublished("catalog-scheduled", now.Add(time.Hour), now.Add(2*time.Hour), 2, 1, []string{"standard_product:9"})
	ended := createPublished("catalog-ended", now.Add(-2*time.Hour), now.Add(-time.Hour), 2, 1, []string{"standard_product:9"})
	soldOut := createPublished("catalog-soldout", now.Add(-time.Hour), now.Add(time.Hour), 1, 1, []string{"standard_product:9"})
	archived := createPublished("catalog-archived", now.Add(-time.Hour), now.Add(time.Hour), 2, 1, []string{"standard_product:9"})

	updates := []struct {
		coupon    couponport.Coupon
		slug      string
		issued    int64
		updatedAt time.Time
	}{
		{coupon: active, slug: "cp-active", issued: 0, updatedAt: now.Add(4 * time.Minute)},
		{coupon: scheduled, slug: "", issued: 0, updatedAt: now.Add(3 * time.Minute)},
		{coupon: ended, slug: "cp-ended", issued: 0, updatedAt: now.Add(2 * time.Minute)},
		{coupon: soldOut, slug: "cp-soldout", issued: 1, updatedAt: now.Add(time.Minute)},
		{coupon: archived, slug: "cp-archived", issued: 0, updatedAt: now.Add(5 * time.Minute)},
	}
	if _, err = native.Exec(ctx, `UPDATE coupon_rules SET status='archived' WHERE id=$1`, archived.ID); err != nil {
		t.Fatal(err)
	}
	for _, update := range updates {
		if _, err = native.Exec(ctx, `UPDATE coupon_rules SET public_slug=NULLIF($2,''),issued_count=$3,updated_at=$4 WHERE id=$1`, update.coupon.ID, update.slug, update.issued, update.updatedAt); err != nil {
			t.Fatal(err)
		}
	}
	customerID := checkoutCustomer(t, native)
	otherCustomerID := checkoutCustomer(t, native)
	claimDigest := make([]byte, 32)
	claimDigest[0] = 1
	if _, err = native.Exec(ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claim_no_masked,claimed_at,source_digest,created_at,updated_at) VALUES('catalog-test','active-current',$1,$2,'claimed','',$3,$4,$3,$3),('catalog-test','scheduled-other',$5,$6,'claimed','',$3,$4,$3,$3)`, customerID, active.ID, now, claimDigest, otherCustomerID, scheduled.ID); err != nil {
		t.Fatal(err)
	}
	countsBefore := couponCatalogCounts(t, ctx, native)
	catalog, err := couponapp.NewSidebarClaimableCatalog(uow, repository, products)
	if err != nil {
		t.Fatal(err)
	}
	first, err := catalog.ListSidebarClaimable(ctx, customerID, couponport.SidebarClaimableQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 4 || first.Limit != 2 || first.Offset != 0 || len(first.Items) != 2 {
		t.Fatalf("first page=%+v", first)
	}
	activeItem, scheduledItem := first.Items[0], first.Items[1]
	if activeItem.CouponID != active.ID || activeItem.Name != active.Name || activeItem.DiscountMinor != 100 || activeItem.Currency != "CNY" || activeItem.PublicSlug != "cp-active" || activeItem.AvailabilityStatus != "active" || !activeItem.UserLimitReached || !activeItem.ClaimEndsAt.Equal(active.ClaimEndsAt) {
		t.Fatalf("active catalog item=%+v", activeItem)
	}
	if len(activeItem.Targets) != 2 || activeItem.Targets[0] != (couponport.SidebarClaimableTarget{Title: "普通商品", ProductType: productport.ProductOptionStandard}) || activeItem.Targets[1] != (couponport.SidebarClaimableTarget{Title: "周期商品", ProductType: productport.ProductOptionServicePeriod}) {
		t.Fatalf("active targets=%+v", activeItem.Targets)
	}
	if scheduledItem.CouponID != scheduled.ID || scheduledItem.AvailabilityStatus != "scheduled" || scheduledItem.UserLimitReached || scheduledItem.PublicSlug != "" {
		t.Fatalf("scheduled catalog item=%+v", scheduledItem)
	}

	second, err := catalog.ListSidebarClaimable(ctx, customerID, couponport.SidebarClaimableQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 4 || second.Limit != 2 || second.Offset != 2 || len(second.Items) != 2 || second.Items[0].CouponID != ended.ID || second.Items[0].AvailabilityStatus != "ended" || second.Items[1].CouponID != soldOut.ID || second.Items[1].AvailabilityStatus != "sold_out" {
		t.Fatalf("second page=%+v", second)
	}
	if _, err = catalog.ReadSidebarClaimable(ctx, customerID, archived.ID); !errors.Is(err, couponapp.ErrNotFound) {
		t.Fatalf("archived coupon must not be selected by exact sidebar ID err=%v", err)
	}
	if countsAfter := couponCatalogCounts(t, ctx, native); countsAfter != countsBefore {
		t.Fatalf("catalog read mutated coupon state before=%v after=%v", countsBefore, countsAfter)
	}
}

func couponCatalogCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) [4]int64 {
	t.Helper()
	var counts [4]int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_rules),(SELECT count(*) FROM coupon_customer_claims),(SELECT count(*) FROM coupon_audit_events),(SELECT count(*) FROM coupon_outbox)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3]); err != nil {
		t.Fatal(err)
	}
	return counts
}
