package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/http"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type sol61CouponCurrentSubjectProducts struct {
	commerceCheckoutProductFacts
}

func (facts sol61CouponCurrentSubjectProducts) ReadCheckoutProductWithin(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.CheckoutProduct, error) {
	item, err := facts.ReadProductTarget(ctx, kind, id)
	if err != nil {
		return productport.CheckoutProduct{}, err
	}
	return productport.CheckoutProduct{ID: item.ID, ProductType: item.ProductType, Code: item.Code, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, Version: 1}, nil
}

// Pause after the real subject query has taken roots/handle locks, before
// Coupon's own SQL. The competing command is the real reversible merge API.
type sol61CouponSubjectBarrier struct {
	identityquery.PostgreSQL
	write          bool
	ready, release chan struct{}
}

func (b *sol61CouponSubjectBarrier) VerifiedIdentitySubject(ctx context.Context, identityID int64) (identityport.VerifiedIdentitySubject, bool, error) {
	subject, found, err := b.PostgreSQL.VerifiedIdentitySubject(ctx, identityID)
	if err == nil && found && !b.write {
		close(b.ready)
		select {
		case <-b.release:
		case <-ctx.Done():
			return identityport.VerifiedIdentitySubject{}, false, ctx.Err()
		}
	}
	return subject, found, err
}

func (b *sol61CouponSubjectBarrier) WriteLockedVerifiedIdentitySubject(ctx context.Context, identityID int64) (identityport.VerifiedIdentitySubject, bool, error) {
	subject, found, err := b.PostgreSQL.WriteLockedVerifiedIdentitySubject(ctx, identityID)
	if err == nil && found && b.write {
		close(b.ready)
		select {
		case <-b.release:
		case <-ctx.Done():
			return identityport.VerifiedIdentitySubject{}, false, ctx.Err()
		}
	}
	return subject, found, err
}

// The session is minted through actual Payment IssueTrusted while the actual
// OneID merge is active, then used against actual Coupon HTTP after OneID's
// actual reversal. No session/customer/merge state is written by the test.
// Product facts are local, synthetic port facts; no Payment callback, worker,
// Provider client, browser, deployment or external effect is constructed.
func TestPostgreSQLCouponSol61MergePeriodSessionFollowsCurrentVerifiedSubject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	queries := identityquery.NewPostgreSQL()
	repository, err := couponstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.BindCanonicalLineage(queries); err != nil {
		t.Fatal(err)
	}
	checkout, err := couponapp.NewCheckoutService(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkout.BindIdentitySubjects(queries); err != nil {
		t.Fatal(err)
	}
	public, err := couponapp.NewPublicCouponService(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err = public.BindIdentitySubjects(queries); err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	sessions, err := paymentsession.NewService(uow, oneID, customerstore.NewPostgreSQL(), paymentsession.NewPostgreSQL(), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	products := sol61CouponCurrentSubjectProducts{commerceCheckoutProductFacts{9: {ID: 9, Code: "sol61-current-subject-course", Name: "Current subject course", ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 8800}}}
	handler, err := couponhttp.NewPublicHandler(public, checkout, sessions, products, uow)
	if err != nil {
		t.Fatal(err)
	}
	actorID := surveyJourneyGovernedActor(t, ctx, native, "sol61-coupon-current-subject", "Coupon Current Subject Evidence")
	rules := couponapp.NewService(uow, repository, products, repository)
	now := time.Now().UTC().Truncate(time.Microsecond)
	newRule := func(suffix string) couponport.PublicCouponShare {
		t.Helper()
		days := int32(3)
		rule, e := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{Name: "Current subject " + suffix, DiscountAmountTotal: 1000, TotalIssueLimit: 10, PerUserIssueLimit: 3, ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"}}, Actor: actorID, IdempotencyKey: "sol61-subject-create-" + suffix})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = rules.Publish(ctx, rule.ID, actorID, "sol61-subject-publish-"+suffix); e != nil {
			t.Fatal(e)
		}
		share, e := public.EnsurePublicShare(ctx, rule.ID, actorID)
		if e != nil {
			t.Fatal(e)
		}
		return share
	}
	survivorRule, loserRule, freshRule := newRule("survivor-history"), newRule("loser-history"), newRule("fresh")
	loserFact := evidenceMergeFact(t, identitydomain.KindOAOpenID, "wechat-app:sol61-coupon-current", "sol61-current-loser-openid")
	loserUnion := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:sol61-coupon-current", "sol61-current-loser-unionid")
	// Distinct legitimate channel scopes avoid the existing exclusive-identity
	// conflict for two different OpenIDs/UnionIDs in one Provider scope.
	survivorFact := evidenceMergeFact(t, identitydomain.KindOAOpenID, "wechat-app:sol61-coupon-survivor", "sol61-current-survivor-openid")
	survivorUnion := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:sol61-coupon-survivor", "sol61-current-survivor-unionid")
	issue := func(fact, union identitydomain.VerifiedFact, key string) paymentsession.Issued {
		t.Helper()
		issued, e := sessions.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: fact, UnionID: union, IdempotencyKey: key})
		if e != nil {
			t.Fatal(e)
		}
		return issued
	}
	loser := issue(loserFact, loserUnion, "sol61-current-loser-first-session")
	survivor := issue(survivorFact, survivorUnion, "sol61-current-survivor-first-session")
	if loser.PayerCustomerID == survivor.PayerCustomerID || loser.PayerIdentityID == survivor.PayerIdentityID {
		t.Fatal("independently verified subjects did not provision separate roots/handles")
	}
	request := func(method, path, token, key string, recoveryOnly bool) *httptest.ResponseRecorder {
		t.Helper()
		body := ""
		if method == http.MethodPost {
			body = "{}"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("User-Agent", "Mozilla/5.0 MicroMessenger")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-AICRM-Coupon-Session-Binding", paymentport.CheckoutSessionBinding(token))
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if recoveryOnly {
			r.Header.Set("X-AICRM-Coupon-Recovery-Only", "true")
		}
		r.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	claimID := func(check *testing.T, w *httptest.ResponseRecorder) int64 {
		check.Helper()
		var result struct {
			OK    bool `json:"ok"`
			Claim struct {
				ID int64 `json:"claim_id"`
			} `json:"claim"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.OK || result.Claim.ID < 1 {
			check.Fatalf("actual Coupon claim status=%d body=%s", w.Code, w.Body.String())
		}
		return result.Claim.ID
	}
	survivorKey, loserKey := "sol61-current-survivor-original-key", "sol61-current-loser-original-key"
	survivorClaim := claimID(t, request(http.MethodPost, "/api/h5/coupons/"+survivorRule.PublicSlug+"/claim", survivor.Token, survivorKey, false))
	loserClaim := claimID(t, request(http.MethodPost, "/api/h5/coupons/"+loserRule.PublicSlug+"/claim", loser.Token, loserKey, false))
	var candidate identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		candidate, e = oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loser.PayerCustomerID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "coupon.current.subject.evidence", EventID: "sol61-coupon-current-merge", Digest: "synthetic-sol61-coupon-current-merge", PolicyVersion: "test-v1"}})
		return e
	}); err != nil || candidate.Candidate == nil {
		t.Fatalf("actual strong-evidence merge candidate=%+v err=%v", candidate, err)
	}
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		merged, e = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivor.PayerCustomerID, Operator: "sol61-current-subject-synthetic"})
		return e
	}); err != nil || merged.Merge == nil {
		t.Fatalf("actual merge=%+v err=%v", merged, err)
	}
	mergePeriod := issue(loserFact, loserUnion, "sol61-current-loser-merge-period-session")
	if mergePeriod.PayerCustomerID != survivor.PayerCustomerID || mergePeriod.PayerIdentityID != loser.PayerIdentityID {
		t.Fatal("actual merge-period OAuth session must freeze survivor customer and original loser handle")
	}
	for _, writing := range []bool{false, true} {
		name := "subject_read_lock_covers_coupon_sql"
		if writing {
			name = "subject_write_lock_covers_receipt_replay"
		}
		t.Run(name, func(t *testing.T) {
			barrier := &sol61CouponSubjectBarrier{PostgreSQL: queries, write: writing, ready: make(chan struct{}), release: make(chan struct{})}
			released := false
			defer func() {
				if !released {
					close(barrier.release)
				}
			}()
			barrierPublic, e := couponapp.NewPublicCouponService(uow, repository)
			if e != nil {
				t.Fatal(e)
			}
			if e = barrierPublic.BindIdentitySubjects(barrier); e != nil {
				t.Fatal(e)
			}
			barrierCheckout, e := couponapp.NewCheckoutService(uow, repository)
			if e != nil {
				t.Fatal(e)
			}
			if e = barrierCheckout.BindIdentitySubjects(barrier); e != nil {
				t.Fatal(e)
			}
			originalHandler := handler
			handler, e = couponhttp.NewPublicHandler(barrierPublic, barrierCheckout, sessions, products, uow)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { handler = originalHandler }()
			readDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				if writing {
					readDone <- request(http.MethodPost, "/api/h5/coupons/"+loserRule.PublicSlug+"/claim", mergePeriod.Token, loserKey, true)
				} else {
					readDone <- request(http.MethodGet, "/api/h5/coupons/available?target_ref=standard_product%3A9", mergePeriod.Token, "", false)
				}
			}()
			select {
			case <-barrier.ready:
			case response := <-readDone:
				t.Fatalf("Coupon request finished without using locked subject: status=%d", response.Code)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			rollbackProbe := errors.New("rollback successful real merge reversal probe")
			revertDone, started := make(chan error, 1), make(chan struct{})
			lockProbe := make(chan error, 1)
			go func() {
				revertDone <- uow.Within(ctx, func(tx context.Context) error {
					db, e := platformpostgres.RequireTransaction(tx)
					if e != nil {
						lockProbe <- e
						return e
					}
					if _, e = db.Exec(tx, `SAVEPOINT coupon_subject_lock_probe`); e != nil {
						lockProbe <- e
						return e
					}
					_, lockedErr := db.Exec(tx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) ORDER BY id FOR UPDATE NOWAIT`, []int64{int64(loser.PayerCustomerID), int64(survivor.PayerCustomerID)})
					if _, e = db.Exec(tx, `ROLLBACK TO SAVEPOINT coupon_subject_lock_probe`); e != nil {
						lockProbe <- e
						return e
					}
					if _, e = db.Exec(tx, `RELEASE SAVEPOINT coupon_subject_lock_probe`); e != nil {
						lockProbe <- e
						return e
					}
					lockProbe <- lockedErr
					close(started)
					if _, e := oneID.RevertConfirmedMerge(tx, merged.Merge.ID); e != nil {
						return e
					}
					return rollbackProbe
				})
			}()
			select {
			case e := <-lockProbe:
				var pgError *pgconn.PgError
				if !errors.As(e, &pgError) || pgError.Code != "55P03" {
					t.Errorf("Coupon subject did not pin actual roots: want SQLSTATE55P03, got %v", e)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-started:
			case e := <-revertDone:
				t.Fatalf("real reversal lock probe failed before entering API: %v", e)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			completedEarly := false
			select {
			case e := <-revertDone:
				completedEarly = true
				t.Errorf("real reversal passed the current subject lock before Coupon SQL: %v", e)
			case <-time.After(100 * time.Millisecond):
			}
			close(barrier.release)
			released = true
			select {
			case response := <-readDone:
				if response.Code != http.StatusOK {
					t.Errorf("same-transaction Coupon read/replay failed: status=%d body=%s", response.Code, response.Body.String())
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if !completedEarly {
				select {
				case e := <-revertDone:
					if !errors.Is(e, rollbackProbe) {
						t.Errorf("real reversal did not complete after Coupon SQL committed: %v", e)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, e := oneID.RevertConfirmedMerge(tx, merged.Merge.ID)
		return e
	}); err != nil {
		t.Fatalf("actual reversible OneID merge: %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		subject, found, e := queries.VerifiedIdentitySubject(tx, mergePeriod.PayerIdentityID)
		if e != nil {
			return e
		}
		if !found || subject.CustomerID != loser.PayerCustomerID || len(subject.Lineage) != 1 {
			t.Fatalf("actual restored verified handle subject=%+v found=%t", subject, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("available_and_page_do_not_disclose_former_survivor_claim", func(t *testing.T) {
		w := request(http.MethodGet, "/api/h5/coupons/available?target_ref=standard_product%3A9", mergePeriod.Token, "", false)
		var result struct {
			OK    bool `json:"ok"`
			Items []struct {
				ClaimID int64 `json:"claim_id"`
			} `json:"items"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.OK || len(result.Items) != 1 || result.Items[0].ClaimID != loserClaim {
			t.Errorf("restored loser session must see only its original claim: status=%d body=%s", w.Code, w.Body.String())
		}
		page := request(http.MethodGet, "/c/"+survivorRule.PublicSlug, mergePeriod.Token, "", false)
		if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "领取成功，可选择下方商品使用") {
			t.Errorf("restored loser session sees former survivor claimed page: status=%d claimed=%t", page.Code, strings.Contains(page.Body.String(), "领取成功，可选择下方商品使用"))
		}
	})
	t.Run("recovery_only_rejects_former_survivor_receipt", func(t *testing.T) {
		w := request(http.MethodPost, "/api/h5/coupons/"+survivorRule.PublicSlug+"/claim", mergePeriod.Token, survivorKey, true)
		if w.Code != http.StatusConflict {
			t.Errorf("restored loser session replayed former survivor receipt: status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("recovery_only_preserves_original_loser_receipt", func(t *testing.T) {
		w := request(http.MethodPost, "/api/h5/coupons/"+loserRule.PublicSlug+"/claim", mergePeriod.Token, loserKey, true)
		if id := claimID(t, w); id != loserClaim {
			t.Errorf("restored loser original receipt changed: claim=%d want=%d", id, loserClaim)
		}
	})
	t.Run("fresh_claim_belongs_to_current_verified_subject", func(t *testing.T) {
		id := claimID(t, request(http.MethodPost, "/api/h5/coupons/"+freshRule.PublicSlug+"/claim", mergePeriod.Token, "sol61-current-fresh-claim-key", false))
		var owner int64
		if e := native.QueryRow(ctx, `SELECT customer_id FROM coupon_customer_claims WHERE id=$1`, id).Scan(&owner); e != nil || owner != int64(loser.PayerCustomerID) {
			t.Errorf("fresh Coupon claim inherited frozen survivor instead of current loser subject: owner=%d want=%d err=%v", owner, loser.PayerCustomerID, e)
		}
	})
	var count, survivorOwner, loserOwner int64
	if err = native.QueryRow(ctx, `SELECT count(*),(SELECT customer_id FROM coupon_customer_claims WHERE id=$1),(SELECT customer_id FROM coupon_customer_claims WHERE id=$2) FROM coupon_customer_claims`, survivorClaim, loserClaim).Scan(&count, &survivorOwner, &loserOwner); err != nil || count != 3 || survivorOwner != int64(survivor.PayerCustomerID) || loserOwner != int64(loser.PayerCustomerID) {
		t.Errorf("HTTP recovery minted facts or rewrote historical owners: count=%d survivorOwner=%d loserOwner=%d err=%v", count, survivorOwner, loserOwner, err)
	}
	t.Run("verified_handle_never_falls_back_when_reader_is_missing", func(t *testing.T) {
		unboundCheckout, e := couponapp.NewCheckoutService(uow, repository)
		if e != nil {
			t.Fatal(e)
		}
		_, e = unboundCheckout.Claim(ctx, couponport.ClaimCommand{CouponID: loserRule.CouponID, HolderCustomerID: int64(survivor.PayerCustomerID), HolderIdentityID: loser.PayerIdentityID, ActorScope: "coupon-public:" + loserRule.PublicSlug, IdempotencyKey: loserKey, ClaimedAt: now, RecoveryOnly: true})
		if !errors.Is(e, couponapp.ErrUnavailable) {
			t.Errorf("unbound trusted Claim fell back to frozen numeric owner: %v", e)
		}
		unboundPublic, e := couponapp.NewPublicCouponService(uow, repository)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = unboundPublic.PublicClaimStateForIdentity(ctx, loser.PayerIdentityID, loserRule.CouponID); !errors.Is(e, couponapp.ErrUnavailable) {
			t.Errorf("unbound public subject state accepted numeric fallback: %v", e)
		}
		if _, e = unboundPublic.ListAvailableClaimsForIdentity(ctx, loser.PayerIdentityID, "standard_product:9", now); !errors.Is(e, couponapp.ErrUnavailable) {
			t.Errorf("unbound available subject state accepted numeric fallback: %v", e)
		}
	})
	t.Run("retired_handle_rejects_original_receipt_before_replay", func(t *testing.T) {
		// There is no published Identity retirement command. Only this synthetic
		// verified handle's status is withdrawn under the existing roots lock;
		// customer/session/merge state and historical Coupon owners are intact.
		if e := uow.Within(ctx, func(tx context.Context) error {
			if _, e := queries.WriteLockedCanonicalLineage(tx, loser.PayerCustomerID); e != nil {
				return e
			}
			db, e := platformpostgres.RequireTransaction(tx)
			if e != nil {
				return e
			}
			_, e = db.Exec(tx, `UPDATE customer_identities SET status='retired',version=version+1 WHERE id=$1`, loser.PayerIdentityID)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		for _, response := range []*httptest.ResponseRecorder{
			request(http.MethodGet, "/api/h5/coupons/available?target_ref=standard_product%3A9", mergePeriod.Token, "", false),
			request(http.MethodPost, "/api/h5/coupons/"+loserRule.PublicSlug+"/claim", mergePeriod.Token, loserKey, true),
			request(http.MethodPost, "/api/h5/coupons/"+freshRule.PublicSlug+"/claim", mergePeriod.Token, "sol61-current-retired-new-key", false),
		} {
			if response.Code != http.StatusUnauthorized {
				t.Errorf("retired handle retains public Coupon authority: status=%d body=%s", response.Code, response.Body.String())
			}
		}
		var after int64
		if e := native.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims`).Scan(&after); e != nil || after != count {
			t.Errorf("retired/original-key controls appended Coupon facts: before=%d after=%d err=%v", count, after, e)
		}
	})
}
