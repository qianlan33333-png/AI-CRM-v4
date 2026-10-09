package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// These are read-only Port projections. No Payment service, worker, provider,
// refund or settlement command is constructed by this fixture.
type distributionReview20261009Product struct{ beforeList func(context.Context) error }

func (s distributionReview20261009Product) ListProductOptions(ctx context.Context, q productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	if s.beforeList != nil {
		if err := s.beforeList(ctx); err != nil {
			return productport.ProductOptionPage{}, err
		}
	}
	p := productport.ProductOptionPage{Total: 1, Offset: q.Offset, Limit: q.Limit}
	if q.Offset == 0 {
		p.Items = []productport.ProductOption{{ID: 11, ProductType: productport.ProductOptionStandard, Code: "review-product", Name: "Review product", PriceMinor: 1000, Currency: "CNY"}}
	}
	return p, nil
}
func (distributionReview20261009Product) ReadSidebarShareProduct(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.SidebarShareProduct, error) {
	if kind != productport.ProductOptionStandard || id != 11 {
		return productport.SidebarShareProduct{}, productport.ErrSaleableProductNotFound
	}
	return productport.SidebarShareProduct{ID: id, ProductType: kind, Code: "review-product", Name: "Review product"}, nil
}

type distributionReview20261009Purchase struct {
	owner  int64
	paidAt time.Time
}

func (p distributionReview20261009Purchase) ListQualificationPurchaseEvidenceWithin(_ context.Context, q orderport.QualificationPurchaseQuery) ([]orderport.QualificationPurchaseEvidence, error) {
	if q.ProductID != 11 || q.ProductType != "standard_product" {
		return nil, nil
	}
	return []orderport.QualificationPurchaseEvidence{{OrderID: 7001, ProductID: 11, OrderItemLine: 1, PayerCustomerID: p.owner, BeneficiaryCustomerID: p.owner, ItemPaidMinor: 1000, PaymentConfirmedAt: p.paidAt, RecordOrigin: "native"}}, nil
}
func (p distributionReview20261009Purchase) DistributionPaymentStateWithin(context.Context, int64) (paymentport.DistributionPaymentState, error) {
	return paymentport.DistributionPaymentState{ConfirmedPaid: true, OriginalMinor: 1000, AvailableMinor: 1000, ConfirmedPaidAt: p.paidAt}, nil
}

func (f *distributionReview20261009IdentityFixture) promotion(t *testing.T, owner distributionport.TrustedSessionActor, sources ...distributionReview20261009Product) *distributionapp.PromotionService {
	t.Helper()
	now := time.Now().UTC()
	if err := f.uow.Within(f.ctx, func(tx context.Context) error {
		d, _, e := f.repo.ReadDistributorByCustomerWithin(tx, owner.CustomerID, true)
		if e != nil {
			return e
		}
		if _, _, e = f.repo.UpdateReceiverReadinessWithin(tx, d.ID, d.Version, distributionport.ReceiverReadiness{Ready: true, AppID: owner.AppID, Reference: "psreceiver_7001", CheckedAt: now}, now); e != nil {
			return e
		}
		policy, e := distributiondomain.NewPolicy(11, distributiondomain.ProductTypeStandard, true, 1000, 1, now)
		if e != nil {
			return e
		}
		_, e = f.repo.InsertProductPolicyWithin(tx, policy)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	evidence := distributionReview20261009Purchase{owner: owner.CustomerID, paidAt: now.Add(-time.Hour)}
	qualification, err := distributionapp.NewQualificationService(f.queries, evidence, evidence)
	if err != nil {
		t.Fatal(err)
	}
	products := distributionReview20261009Product{}
	if len(sources) > 0 {
		products = sources[0]
	}
	s, err := distributionapp.NewPromotionService(f.uow, f.repo, qualification, products, distributionReview20261009Product{}, f.queries, "https://distribution-review.test", base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s.SetSettlementEnabled(true)
	distributionReview20261009Bind(t, s, f.queries)
	return s
}

func TestPostgreSQLDistributionReview20261009PromotionRetainsOriginalReceiptScope(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	a := f.actor(f.a)
	if _, e := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: a, AgreementVersion: "v1", IdempotencyKey: "distribution-review-promoter-register"}); e != nil {
		t.Fatal(e)
	}
	promotion := f.promotion(t, a)
	command := distributionport.IssuePromotionCommand{Actor: a, ProductID: 11, ProductType: distributiondomain.ProductTypeStandard, IdempotencyKey: "distribution-review-original-credential"}
	original, e := promotion.IssuePromotionLink(f.ctx, command)
	if e != nil {
		t.Fatal(e)
	}
	before := distributionReview20261009Facts(t, f)
	f.merge(t)
	command.Actor.CustomerID = int64(f.b.CustomerID)
	replay, e := promotion.IssuePromotionLink(f.ctx, command)
	if e != nil || replay.URL != original.URL || !replay.ExpiresAt.Equal(original.ExpiresAt) {
		t.Fatalf("historical credential changed: original=%+v replay=%+v err=%v", original, replay, e)
	}
	page, e := promotion.ListPromotionProducts(f.ctx, command.Actor, "", 10)
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("merged promoter list=%+v err=%v", page, e)
	}
	if after := distributionReview20261009Facts(t, f); before != after {
		t.Fatalf("exact credential replay appended facts: before=%v after=%v", before, after)
	}
	command.ProductType = distributiondomain.ProductTypeServicePeriod
	if _, e = promotion.IssuePromotionLink(f.ctx, command); !errors.Is(e, distributionport.ErrConflict) {
		t.Fatalf("changed product type replay=%v", e)
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE customer_identities SET status='retired',version=version+1 WHERE id=$1`, a.IdentityID); e != nil {
		t.Fatal(e)
	}
	command.ProductType = distributiondomain.ProductTypeStandard
	_, issueErr := promotion.IssuePromotionLink(f.ctx, command)
	_, listErr := promotion.ListPromotionProducts(f.ctx, command.Actor, "", 10)
	if !errors.Is(issueErr, distributionport.ErrUnauthorized) || !errors.Is(listErr, distributionport.ErrUnauthorized) {
		t.Fatalf("retired promotion handle: issue=%v list=%v", issueErr, listErr)
	}
	if after := distributionReview20261009Facts(t, f); before != after {
		t.Fatalf("rejected promotion changed facts: before=%v after=%v", before, after)
	}
}

func TestPostgreSQLDistributionReview20261009PromotionReadRechecksSubjectBetweenTransactions(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	a := f.actor(f.a)
	if _, err := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: a, AgreementVersion: "v1", IdempotencyKey: "distribution-review-pagination-split"}); err != nil {
		t.Fatal(err)
	}
	var mergeID int64
	products := distributionReview20261009Product{beforeList: func(ctx context.Context) error {
		return f.uow.Within(ctx, func(tx context.Context) error { _, err := f.oneID.RevertConfirmedMerge(tx, mergeID); return err })
	}}
	promotion := f.promotion(t, a, products)
	mergeID = f.merge(t)
	// B may view A's unique history while merged. Reverse the merge after
	// the first distributor read, before this request checks eligibility.
	page, err := promotion.ListPromotionProducts(f.ctx, f.actor(f.b), "", 10)
	if !errors.Is(err, distributionport.ErrUnauthorized) || len(page.Items) != 0 {
		t.Fatalf("promotion list reused another subject's distributor across split: page=%+v err=%v", page, err)
	}
}

func TestPostgreSQLDistributionReview20261009SplitCannotReplayAnotherOwnersCredential(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	a := f.actor(f.a)
	if _, err := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: a, AgreementVersion: "v1", IdempotencyKey: "distribution-review-split-credential-register"}); err != nil {
		t.Fatal(err)
	}
	promotion := f.promotion(t, a)
	command := distributionport.IssuePromotionCommand{Actor: a, ProductID: 11, ProductType: distributiondomain.ProductTypeStandard, IdempotencyKey: "distribution-review-split-original-credential"}
	original, err := promotion.IssuePromotionLink(f.ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	id := f.merge(t)
	command.Actor = f.actor(f.b)
	merged, err := promotion.IssuePromotionLink(f.ctx, command)
	if err != nil || merged.URL != original.URL {
		t.Fatalf("unique merged history credential=%+v err=%v", merged, err)
	}
	if err = f.uow.Within(f.ctx, func(tx context.Context) error { _, err := f.oneID.RevertConfirmedMerge(tx, id); return err }); err != nil {
		t.Fatal(err)
	}
	before := distributionReview20261009Facts(t, f)
	if _, err = promotion.IssuePromotionLink(f.ctx, command); !errors.Is(err, distributionport.ErrNotFound) {
		t.Fatalf("split replay granted another owner's credential: %v", err)
	}
	command.Actor = a
	owner, err := promotion.IssuePromotionLink(f.ctx, command)
	if err != nil || owner.URL != original.URL {
		t.Fatalf("original owner lost credential replay: %+v err=%v", owner, err)
	}
	if after := distributionReview20261009Facts(t, f); before != after {
		t.Fatalf("split credential replay appended facts: before=%v after=%v", before, after)
	}
}

func TestPostgreSQLDistributionReview20261009AmbiguousMergedHistoryRejectsAllOwnerEntries(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	for _, actor := range []distributionport.TrustedSessionActor{f.actor(f.a), f.actor(f.b)} {
		if _, e := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: actor, AgreementVersion: "v1", IdempotencyKey: "distribution-review-shared-register"}); e != nil {
			t.Fatal(e)
		}
	}
	promotion := f.promotion(t, f.actor(f.a))
	f.merge(t)
	a := f.actor(f.a)
	a.CustomerID = int64(f.b.CustomerID)
	before := distributionReview20261009Facts(t, f)
	_, profileErr := f.registration.Profile(f.ctx, a)
	_, registerErr := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: a, AgreementVersion: "v1", IdempotencyKey: "distribution-review-ambiguous-new"})
	_, prepareErr := f.registration.PrepareReceiver(f.ctx, a)
	_, earningsErr := f.reads.Earnings(f.ctx, a)
	_, commissionsErr := f.reads.ListCommissions(f.ctx, a, "", "", 10)
	_, listErr := promotion.ListPromotionProducts(f.ctx, a, "", 10)
	_, issueErr := promotion.IssuePromotionLink(f.ctx, distributionport.IssuePromotionCommand{Actor: a, ProductID: 11, ProductType: distributiondomain.ProductTypeStandard, IdempotencyKey: "distribution-review-ambiguous-credential"})
	for name, e := range map[string]error{"profile": profileErr, "register": registerErr, "prepare": prepareErr, "earnings": earningsErr, "commissions": commissionsErr, "products": listErr, "credential": issueErr} {
		if !errors.Is(e, distributionport.ErrConflict) {
			t.Errorf("ambiguous histories %s selected an owner: %v", name, e)
		}
	}
	if after := distributionReview20261009Facts(t, f); before != after {
		t.Fatalf("ambiguous histories changed facts: before=%v after=%v", before, after)
	}
}

func (f *distributionReview20261009IdentityFixture) seedCommission(t *testing.T, owner distributionport.TrustedSessionActor) {
	t.Helper()
	now := time.Now().UTC()
	if err := f.uow.Within(f.ctx, func(tx context.Context) error {
		d, _, e := f.repo.ReadDistributorByCustomerWithin(tx, owner.CustomerID, false)
		if e != nil {
			return e
		}
		policy, e := distributiondomain.NewPolicy(11, distributiondomain.ProductTypeStandard, true, 1000, 1, now)
		if e != nil {
			return e
		}
		if _, e = f.repo.InsertProductPolicyWithin(tx, policy); e != nil {
			return e
		}
		policyID, e := f.repo.ProductPolicyIDWithin(tx, 11, distributiondomain.ProductTypeStandard)
		if e != nil {
			return e
		}
		credential, e := f.repo.InsertPromotionCredentialWithin(tx, distributiondomain.PromotionCredential{DistributorID: d.ID, ProductID: 11, ProductType: distributiondomain.ProductTypeStandard, TokenDigest: sha256.Sum256([]byte("distribution-review-ledger")), Status: distributiondomain.CredentialActive, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
		if e != nil {
			return e
		}
		a, _, e := f.repo.InsertAttributionWithin(tx, distributiondomain.Attribution{OrderID: 7001, OrderItemLine: 1, ProductCode: "review-product", ProductName: "Review ledger", DistributorID: d.ID, PromotionCredentialID: credential.ID, QualificationEvidenceRef: "order:7000:item:1", QualificationState: distributiondomain.QualificationEligible, PolicyVersion: 1, CommissionRateBasisPoints: 1000, WaitDays: 1, AttributedAt: now}, policyID)
		if e != nil {
			return e
		}
		c, e := distributiondomain.NewCommission(a, 1000, now)
		if e != nil {
			return e
		}
		_, _, e = f.repo.InsertCommissionWithin(tx, c)
		return e
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLDistributionReview20261009SplitEarningsCannotReadOtherOwner(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	b := f.actor(f.b)
	if _, e := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: b, AgreementVersion: "v1", IdempotencyKey: "distribution-review-split-ledger-register"}); e != nil {
		t.Fatal(e)
	}
	f.seedCommission(t, b)
	id := f.merge(t)
	stale := f.actor(f.a)
	stale.CustomerID = b.CustomerID
	if e := f.uow.Within(f.ctx, func(tx context.Context) error { _, e := f.oneID.RevertConfirmedMerge(tx, id); return e }); e != nil {
		t.Fatal(e)
	}
	earnings, e := f.reads.Earnings(f.ctx, stale)
	if e != nil || earnings.GrossPaidSalesMinor != 0 || earnings.InitialCommissionMinor != 0 {
		t.Fatalf("split leaked other owner earnings: %+v err=%v", earnings, e)
	}
	page, e := f.reads.ListCommissions(f.ctx, stale, "", "", 10)
	if e != nil || len(page.Items) != 0 {
		t.Fatalf("split leaked other owner commission rows: %+v err=%v", page, e)
	}
	owner, e := f.reads.Earnings(f.ctx, b)
	if e != nil || owner.GrossPaidSalesMinor != 1000 || owner.InitialCommissionMinor != 100 {
		t.Fatalf("proper owner lost ledger: %+v err=%v", owner, e)
	}
}

func TestPostgreSQLDistributionReview20261009BoundRegistrationConcurrentAndRestartReplay(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	command := distributionport.RegisterCommand{Actor: f.actor(f.a), AgreementVersion: "v1", IdempotencyKey: "distribution-review-concurrent-register"}
	start := make(chan struct{})
	profiles := make(chan distributionport.DistributorProfile, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			p, e := f.registration.Register(f.ctx, command)
			profiles <- p
			errs <- e
		}()
	}
	close(start)
	wait.Wait()
	close(profiles)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id int64
	for p := range profiles {
		if id != 0 && id != p.Distributor.ID {
			t.Fatalf("duplicate concurrent owner: %d %d", id, p.Distributor.ID)
		}
		id = p.Distributor.ID
	}
	restarted, e := distributionapp.NewRegistrationService(f.uow, f.repo, distributionReview20261009DisabledPayment{})
	if e != nil {
		t.Fatal(e)
	}
	distributionReview20261009Bind(t, restarted, f.queries)
	replay, e := restarted.Register(f.ctx, command)
	if e != nil || replay.Distributor.ID != id {
		t.Fatalf("restart replay id=%d result=%+v err=%v", id, replay, e)
	}
	if facts := distributionReview20261009Facts(t, f); facts != [4]int64{1, 1, 1, 1} {
		t.Fatalf("concurrent/restart facts=%v", facts)
	}
}

type distributionReview20261009FailOutbox struct {
	*distributionstore.Repository
	cancel context.CancelFunc
}

func (s distributionReview20261009FailOutbox) AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error {
	if s.cancel != nil {
		s.cancel()
		return context.Canceled
	}
	return errors.New("synthetic distribution outbox failure")
}

func TestPostgreSQLDistributionReview20261009RegistrationFailureAndCancellationRollBackAllFacts(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "outbox_failure"
		if cancelled {
			name = "cancel_after_audit"
		}
		t.Run(name, func(t *testing.T) {
			f := newDistributionReview20261009IdentityFixture(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			store := distributionReview20261009FailOutbox{Repository: f.repo}
			if cancelled {
				store.cancel = cancel
			}
			failing, e := distributionapp.NewRegistrationService(f.uow, store, distributionReview20261009DisabledPayment{})
			if e != nil {
				t.Fatal(e)
			}
			distributionReview20261009Bind(t, failing, f.queries)
			command := distributionport.RegisterCommand{Actor: f.actor(f.a), AgreementVersion: "v1", IdempotencyKey: "distribution-review-atomic-register"}
			if _, e = failing.Register(ctx, command); e == nil {
				t.Fatal("injected transaction failure accepted")
			}
			if facts := distributionReview20261009Facts(t, f); facts != [4]int64{} {
				t.Fatalf("transaction retained partial facts: %v", facts)
			}
			if _, e = f.registration.Register(f.ctx, command); e != nil {
				t.Fatalf("original key cannot recover: %v", e)
			}
			if facts := distributionReview20261009Facts(t, f); facts != [4]int64{1, 1, 1, 1} {
				t.Fatalf("retry facts=%v", facts)
			}
		})
	}
}
