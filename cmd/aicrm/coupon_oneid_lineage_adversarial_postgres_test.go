package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// Actual OneID provisioning, strong-evidence merge, and native Coupon UoWs.
// No Payment callback, Provider, worker or external effect is constructed.
func TestPostgreSQLCouponClaimsFollowOneIDMergeWithoutRewritingOwners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := couponstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.BindCanonicalLineage(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	checkout, err := couponapp.NewCheckoutService(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	public, err := couponapp.NewPublicCouponService(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := couponapp.NewCustomerCouponApplication(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	products := commerceCheckoutProductFacts{9: {ID: 9, Name: "Lineage course", ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 8800}}
	catalog, err := couponapp.NewSidebarClaimableCatalog(uow, repository, products)
	if err != nil {
		t.Fatal(err)
	}
	actorID := surveyJourneyGovernedActor(t, ctx, native, "coupon-lineage-evidence", "Coupon Lineage Evidence")
	rules := couponapp.NewService(uow, repository, products, repository)
	now := time.Now().UTC().Truncate(time.Microsecond)
	newRule := func(suffix string) couponport.Coupon {
		days := int32(7)
		rule, err := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{
			Name: "Lineage " + suffix, DiscountAmountTotal: 1000, TotalIssueLimit: 10, PerUserIssueLimit: 1,
			ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays,
			RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"},
		}, Actor: actorID, IdempotencyKey: "coupon-lineage-create-0001-" + suffix})
		if err != nil {
			t.Fatal(err)
		}
		rule, err = rules.Publish(ctx, rule.ID, actorID, "coupon-lineage-publish-0001-"+suffix)
		if err != nil {
			t.Fatal(err)
		}
		return rule
	}
	rule, concurrentRule := newRule("original"), newRule("concurrent")
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	loserFact := evidenceMergeFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:coupon-lineage", "loser")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:coupon-lineage", "survivor")
	outsiderFact := evidenceMergeFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:coupon-lineage", "outsider")
	var loserID, survivorID, outsiderID customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		for _, input := range []struct {
			fact identitydomain.VerifiedFact
			id   *customerdomain.CustomerID
		}{{loserFact, &loserID}, {survivorFact, &survivorID}, {outsiderFact, &outsiderID}} {
			resolved, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, input.fact)
			if e != nil {
				return e
			}
			*input.id = resolved.CustomerID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	original := couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: int64(loserID), ActorScope: "coupon-session:original-loser", IdempotencyKey: "coupon-lineage-original-claim-0001", ClaimedAt: now}
	claim, err := checkout.Claim(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	// Retain a real pre-merge reservation receipt, then release its claim so
	// the read/eligibility checks below still exercise an available old fact.
	historicalReserve := couponport.ReserveCommand{HolderCustomerID: int64(loserID), ClaimID: claim.ClaimID,
		ProductID: 9, ProductCode: "lineage-course-9", ProductType: "standard_product", GrossAmountMinor: 8800, Currency: "CNY",
		OrderReference: "coupon-lineage-history-order-0001", ActorScope: "coupon-order:historical", IdempotencyKey: "coupon-lineage-history-reserve-0001", ReservedAt: now}
	reserve := func(command couponport.ReserveCommand) (couponport.ReservationSnapshot, error) {
		var result couponport.ReservationSnapshot
		e := uow.Within(ctx, func(tx context.Context) error {
			var e error
			result, e = checkout.ReserveWithin(tx, command)
			return e
		})
		return result, e
	}
	historicalReservation, err := reserve(historicalReserve)
	if err != nil || !historicalReservation.CouponApplied {
		t.Fatalf("historical reservation=%+v err=%v", historicalReservation, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, e := checkout.ReleaseWithin(tx, couponport.ReleaseCommand{ReservationRef: historicalReservation.ReservationRef, OrderReference: historicalReserve.OrderReference,
			CloseReason: "local_validation", ActorScope: historicalReserve.ActorScope, IdempotencyKey: "coupon-lineage-history-release-0001", ClosedAt: now})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var candidate identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		candidate, e = oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact,
			Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "coupon.lineage.evidence", EventID: "coupon-lineage-merge", Digest: "synthetic-coupon-lineage", PolicyVersion: "test-v1"}})
		return e
	}); err != nil || candidate.Candidate == nil {
		t.Fatalf("strong-evidence candidate=%+v err=%v", candidate, err)
	}
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		merged, e = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "coupon-lineage-evidence"})
		return e
	}); err != nil || merged.Merge == nil || merged.Merge.FromCustomerID != loserID || merged.Merge.ToCustomerID != survivorID {
		t.Fatalf("actual merge=%+v err=%v", merged, err)
	}
	t.Run("public_and_profile_reads", func(t *testing.T) {
		for _, id := range []customerdomain.CustomerID{loserID, survivorID} {
			state, e := public.PublicClaimState(ctx, int64(id), rule.ID)
			if e != nil || state.ClaimCount != 1 {
				t.Errorf("root/old-session %d claim count=%d err=%v", id, state.ClaimCount, e)
			}
			available, e := public.ListAvailableClaims(ctx, int64(id), "standard_product:9", now)
			if e != nil || len(available) != 1 || available[0].ClaimID != claim.ClaimID {
				t.Errorf("root/old-session %d lost original available claim: %+v err=%v", id, available, e)
			}
			page, e := profile.ListCustomerCoupons(ctx, int64(id), 10)
			if e != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ClaimID != claim.ClaimID {
				t.Errorf("root/old-session %d lost original history: %+v err=%v", id, page, e)
			}
			item, e := catalog.ReadSidebarClaimable(ctx, int64(id), rule.ID)
			if e != nil || !item.UserLimitReached {
				t.Errorf("root/old-session %d directory quota reset: %+v err=%v", id, item, e)
			}
		}
		available, e := public.ListAvailableClaims(ctx, int64(outsiderID), "standard_product:9", now)
		if e != nil || len(available) != 0 {
			t.Errorf("unrelated holder sees merged claim: %+v err=%v", available, e)
		}
	})
	t.Run("receipt_replay", func(t *testing.T) {
		for _, id := range []customerdomain.CustomerID{loserID, survivorID} {
			input := original
			input.HolderCustomerID = int64(id)
			replay, e := checkout.Claim(ctx, input)
			if e != nil || replay.ClaimID != claim.ClaimID {
				t.Fatalf("committed claim holder=%d must replay unchanged: %+v err=%v", id, replay, e)
			}
			command := historicalReserve
			command.HolderCustomerID = int64(id)
			reserved, e := reserve(command)
			if e != nil || reserved != historicalReservation {
				t.Fatalf("committed reservation holder=%d must replay unchanged: %+v err=%v", id, reserved, e)
			}
		}
		outsider := original
		outsider.HolderCustomerID = int64(outsiderID)
		if _, e := checkout.Claim(ctx, outsider); !errors.Is(e, couponapp.ErrConflict) {
			t.Fatalf("outsider borrowed claim receipt: %v", e)
		}
		changed := original
		changed.HolderCustomerID, changed.CouponID = int64(survivorID), concurrentRule.ID
		if _, e := checkout.Claim(ctx, changed); !errors.Is(e, couponapp.ErrConflict) {
			t.Fatalf("changed coupon borrowed claim receipt: %v", e)
		}
		for _, mutate := range []func(*couponport.ReserveCommand){
			func(c *couponport.ReserveCommand) { c.HolderCustomerID = int64(outsiderID) },
			func(c *couponport.ReserveCommand) { c.GrossAmountMinor++ },
			func(c *couponport.ReserveCommand) { c.ClaimID = 0 },
		} {
			changed := historicalReserve
			changed.HolderCustomerID = int64(survivorID)
			mutate(&changed)
			if _, e := reserve(changed); !errors.Is(e, couponapp.ErrConflict) {
				t.Fatalf("changed subject/payload borrowed reservation receipt: %v", e)
			}
		}
	})
	t.Run("per_user_limit", func(t *testing.T) {
		_, e := checkout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: int64(survivorID), ActorScope: "coupon-session:new-root", IdempotencyKey: "coupon-lineage-quota-second-0001", ClaimedAt: now})
		if !errors.Is(e, couponapp.ErrNoEligibleCoupon) {
			t.Fatalf("canonical holder bypassed existing per-user limit: %v", e)
		}
	})
	t.Run("reservation_and_release", func(t *testing.T) {
		var reserved couponport.ReservationSnapshot
		e := uow.Within(ctx, func(tx context.Context) error {
			var e error
			reserved, e = checkout.ReserveWithin(tx, couponport.ReserveCommand{HolderCustomerID: int64(survivorID), ClaimID: claim.ClaimID,
				ProductID: 9, ProductCode: "lineage-course-9", ProductType: "standard_product", GrossAmountMinor: 8800, Currency: "CNY",
				OrderReference: "coupon-lineage-local-order-0001", ActorScope: "coupon-order:root", IdempotencyKey: "coupon-lineage-reserve-0001", ReservedAt: now})
			return e
		})
		if e != nil || !reserved.CouponApplied || reserved.ClaimID != claim.ClaimID || reserved.PayableAmountMinor != 7800 {
			t.Fatalf("canonical holder cannot reserve its historical claim: %+v err=%v", reserved, e)
		}
		e = uow.Within(ctx, func(tx context.Context) error {
			_, e := checkout.ReleaseWithin(tx, couponport.ReleaseCommand{ReservationRef: reserved.ReservationRef, OrderReference: "coupon-lineage-local-order-0001",
				CloseReason: "local_validation", ActorScope: "coupon-order:root", IdempotencyKey: "coupon-lineage-release-0001", ClosedAt: now})
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
	})
	t.Run("old_and_new_session_concurrent_limit", func(t *testing.T) {
		start, results := make(chan struct{}), make(chan error, 2)
		for index, id := range []customerdomain.CustomerID{loserID, survivorID} {
			go func(index int, id customerdomain.CustomerID) {
				<-start
				_, e := checkout.Claim(ctx, couponport.ClaimCommand{CouponID: concurrentRule.ID, HolderCustomerID: int64(id), ActorScope: fmt.Sprintf("coupon-session:concurrent-%d", index), IdempotencyKey: fmt.Sprintf("coupon-lineage-concurrent-%d-0001", index), ClaimedAt: now})
				results <- e
			}(index, id)
		}
		close(start)
		var success, limited int
		for range 2 {
			e := <-results
			if e == nil {
				success++
			} else if errors.Is(e, couponapp.ErrNoEligibleCoupon) {
				limited++
			} else {
				t.Errorf("concurrent claim unexpected err=%v", e)
			}
		}
		var held, wrongOwner int
		if e := native.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE customer_id<>$2) FROM coupon_customer_claims WHERE coupon_id=$1`, concurrentRule.ID, survivorID).Scan(&held, &wrongOwner); e != nil || success != 1 || limited != 1 || held != 1 || wrongOwner != 0 {
			t.Fatalf("single canonical quota/new-write root failed: success=%d limited=%d held=%d wrongOwner=%d err=%v", success, limited, held, wrongOwner, e)
		}
	})
	var historicalOwner int64
	if err = native.QueryRow(ctx, `SELECT customer_id FROM coupon_customer_claims WHERE id=$1`, claim.ClaimID).Scan(&historicalOwner); err != nil || historicalOwner != int64(loserID) {
		t.Fatalf("immutable old claim owner rewritten: owner=%d err=%v", historicalOwner, err)
	}
	if t.Failed() {
		return
	}
	t.Run("read_snapshot_pins_revert", func(t *testing.T) {
		ready, release := make(chan struct{}), make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		readerStore, e := couponstore.NewPostgreSQL(native, uow)
		if e != nil {
			t.Fatal(e)
		}
		if e = readerStore.BindCanonicalLineage(couponReadLineageBarrier{PostgreSQL: identityquery.NewPostgreSQL(), ready: ready, release: release}); e != nil {
			t.Fatal(e)
		}
		reader, e := couponapp.NewPublicCouponService(uow, readerStore)
		if e != nil {
			t.Fatal(e)
		}
		result := make(chan error, 1)
		go func() {
			state, e := reader.PublicClaimState(ctx, int64(survivorID), rule.ID)
			if e == nil && state.ClaimCount != 1 {
				e = fmt.Errorf("pinned lineage claim count=%d", state.ClaimCount)
			}
			result <- e
		}()
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		lockProven := false
		e = uow.Within(ctx, func(txctx context.Context) error {
			tx, e := platformpostgres.RequireTransaction(txctx)
			if e != nil {
				return e
			}
			// Identity intentionally hides PgError details. Prove contention on
			// the same root independently, recover the test transaction, then
			// exercise the actual revert application under a bounded timeout.
			if _, e = tx.Exec(txctx, `SAVEPOINT coupon_lineage_lock_probe`); e != nil {
				return e
			}
			_, probeErr := tx.Exec(txctx, `SELECT id FROM customers WHERE id=$1 FOR UPDATE NOWAIT`, survivorID)
			var blocked *pgconn.PgError
			if !errors.As(probeErr, &blocked) || blocked.Code != "55P03" {
				return fmt.Errorf("Coupon read root was not pinned: %v", probeErr)
			}
			lockProven = true
			if _, e = tx.Exec(txctx, `ROLLBACK TO SAVEPOINT coupon_lineage_lock_probe`); e != nil {
				return e
			}
			if _, e = tx.Exec(txctx, `SET LOCAL lock_timeout='100ms'`); e != nil {
				return e
			}
			_, e = oneID.RevertConfirmedMerge(txctx, merged.Merge.ID)
			return e
		})
		if !lockProven || e == nil {
			t.Fatalf("revert must contend on the pending Coupon read's roots: %v", e)
		}
		close(release)
		released = true
		if e = <-result; e != nil {
			t.Fatal(e)
		}
	})
	if t.Failed() {
		return
	}
	// Reverting the actual Identity merge must separate ownership again;
	// reading old facts through a merge must never transfer the stored owner.
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, e := oneID.RevertConfirmedMerge(tx, merged.Merge.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	state, err := public.PublicClaimState(ctx, int64(survivorID), rule.ID)
	if err != nil || state.ClaimCount != 0 {
		t.Fatalf("reverted survivor retains unrelated old claim: %+v err=%v", state, err)
	}
	available, err := public.ListAvailableClaims(ctx, int64(loserID), "standard_product:9", now)
	if err != nil || len(available) != 1 || available[0].ClaimID != claim.ClaimID {
		t.Fatalf("reverted original owner lost claim: %+v err=%v", available, err)
	}
	rootReplay := original
	rootReplay.HolderCustomerID = int64(survivorID)
	if _, err = checkout.Claim(ctx, rootReplay); !errors.Is(err, couponapp.ErrConflict) {
		t.Fatalf("reverted root borrowed unrelated claim receipt: %v", err)
	}
	rootReservationReplay := historicalReserve
	rootReservationReplay.HolderCustomerID = int64(survivorID)
	if _, err = reserve(rootReservationReplay); !errors.Is(err, couponapp.ErrConflict) {
		t.Fatalf("reverted root borrowed unrelated reservation receipt: %v", err)
	}
}

// Pause after the real Identity share lock, before Coupon's owner SQL. The
// competing real revert must be blocked until that SQL transaction finishes.
type couponReadLineageBarrier struct {
	identityquery.PostgreSQL
	ready   chan struct{}
	release chan struct{}
}

func (b couponReadLineageBarrier) LockedCanonicalLineage(ctx context.Context, id customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	ids, err := b.PostgreSQL.LockedCanonicalLineage(ctx, id)
	if err != nil {
		return nil, err
	}
	close(b.ready)
	select {
	case <-b.release:
		return ids, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
