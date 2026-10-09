package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// The embedded nil port makes any accidental provider/money operation fail.
// Only the explicitly side-effect-free capability read is available here.
type distributionReview20261009DisabledPayment struct {
	paymentport.DistributionSettlementPort
}

func (distributionReview20261009DisabledPayment) SettlementCapability(context.Context) (paymentport.SettlementCapability, error) {
	return paymentport.SettlementCapability{Reason: "merchant_settlement_disabled"}, nil
}

type distributionReview20261009IdentityFixture struct {
	ctx          context.Context
	pool         *pgxpool.Pool
	uow          platformport.UnitOfWork
	repo         *distributionstore.Repository
	registration *distributionapp.RegistrationService
	reads        *distributionapp.ReadModelService
	sessions     *distributionapp.BrowserSessionService
	queries      identityquery.PostgreSQL
	oneID        identityapp.OneIDService
	a, b         identityport.ProvisionResult
	factA, factB identitydomain.VerifiedFact
}

func distributionReview20261009Bind(t *testing.T, service any, queries identityquery.PostgreSQL) {
	t.Helper()
	if binder, ok := service.(interface {
		BindIdentitySubjects(identityport.VerifiedIdentitySubjectReader) error
	}); ok {
		if err := binder.BindIdentitySubjects(queries); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("distribution actor service has no Identity binding")
	}
}

func newDistributionReview20261009IdentityFixture(t *testing.T) *distributionReview20261009IdentityFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := distributionapp.NewRegistrationService(uow, repo, distributionReview20261009DisabledPayment{})
	if err != nil {
		t.Fatal(err)
	}
	reads, err := distributionapp.NewReadModelService(uow, repo)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := distributionapp.NewBrowserSessionService(uow, repo)
	if err != nil {
		t.Fatal(err)
	}
	f := &distributionReview20261009IdentityFixture{ctx: ctx, pool: pool, uow: uow, repo: repo, registration: registration, reads: reads, sessions: sessions, queries: identityquery.NewPostgreSQL(), oneID: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}}
	f.factA = evidenceMergeFact(t, identitydomain.KindMPOpenID, "wechat-app:distribution-review", "distribution-review-a")
	f.factB = evidenceMergeFact(t, identitydomain.KindMPOpenID, "wechat-app:distribution-review-b", "distribution-review-b")
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		if f.a, e = f.oneID.ProvisionCustomerFromVerifiedIdentity(tx, f.factA); e != nil {
			return e
		}
		f.b, e = f.oneID.ProvisionCustomerFromVerifiedIdentity(tx, f.factB)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	for _, service := range []any{registration, reads, sessions} {
		distributionReview20261009Bind(t, service, f.queries)
	}
	return f
}

func (f *distributionReview20261009IdentityFixture) actor(subject identityport.ProvisionResult) distributionport.TrustedSessionActor {
	a := distributionport.TrustedSessionActor{CustomerID: int64(subject.CustomerID), IdentityID: subject.IdentityID, Channel: "mini_program", AppID: "distribution-review", AppScope: "wechat-app:distribution-review", OccurredAt: time.Now().UTC()}
	if subject.IdentityID == f.b.IdentityID {
		a.AppID, a.AppScope = "distribution-review-b", "wechat-app:distribution-review-b"
	}
	return a
}

func (f *distributionReview20261009IdentityFixture) merge(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := f.uow.Within(f.ctx, func(tx context.Context) error {
		linked, e := f.oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: f.a.CustomerID, Target: f.factB, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "distribution.review", EventID: "distribution-review-merge", Digest: "synthetic-distribution-merge", PolicyVersion: "test-v1"}})
		if e != nil {
			return e
		}
		if linked.Candidate == nil {
			return errors.New("merge candidate missing")
		}
		merged, e := f.oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: linked.Candidate.ID, SurvivorCustomerID: f.b.CustomerID, Operator: "distribution-review-synthetic"})
		if e != nil {
			return e
		}
		if merged.Merge == nil {
			return errors.New("confirmed merge missing; synthetic evidence must satisfy OneID")
		}
		id = merged.Merge.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgreSQLDistributionReview20261009RegistrationFollowsUniqueMergedHistory(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	a := f.actor(f.a)
	first, err := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: a, AgreementVersion: "v1", IdempotencyKey: "distribution-review-initial-register"})
	if err != nil {
		t.Fatal(err)
	}
	f.merge(t)
	merged := a
	merged.CustomerID = int64(f.b.CustomerID)
	profile, err := f.registration.Profile(f.ctx, merged)
	if err != nil || profile.Distributor.ID != first.Distributor.ID || profile.RegistrationRequired {
		t.Errorf("unique historical distributor lost after merge: first=%d profile=%+v err=%v", first.Distributor.ID, profile, err)
	}
	replayed, err := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: merged, AgreementVersion: "v1", IdempotencyKey: "distribution-review-initial-register"})
	var rows int
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM distribution_distributors`).Scan(&rows); e != nil {
		t.Fatal(e)
	}
	if err != nil || replayed.Distributor.ID != first.Distributor.ID || rows != 1 {
		t.Fatalf("merge duplicate registration: first=%d replay=%d rows=%d err=%v", first.Distributor.ID, replayed.Distributor.ID, rows, err)
	}
}

func TestPostgreSQLDistributionReview20261009SplitSessionCannotReadOtherOwner(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	b := f.actor(f.b)
	if _, err := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: b, AgreementVersion: "v1", IdempotencyKey: "distribution-review-b-register"}); err != nil {
		t.Fatal(err)
	}
	mergeID := f.merge(t)
	merged := f.actor(f.a)
	merged.CustomerID = b.CustomerID
	token, _, err := f.sessions.Issue(f.ctx, merged)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.uow.Within(f.ctx, func(tx context.Context) error { _, e := f.oneID.RevertConfirmedMerge(tx, mergeID); return e }); err != nil {
		t.Fatal(err)
	}
	current, err := f.sessions.Resolve(f.ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := f.registration.Profile(f.ctx, current)
	if err != nil || !profile.RegistrationRequired || profile.Distributor.ID != 0 {
		t.Fatalf("split retained another customer's distributor authority: session_owner=%d handle=%d expected_owner=%d profile=%+v err=%v", current.CustomerID, current.IdentityID, f.a.CustomerID, profile, err)
	}
}

func TestPostgreSQLDistributionReview20261009RetiredHandleRejectsSessionAndCommands(t *testing.T) {
	f := newDistributionReview20261009IdentityFixture(t)
	a := f.actor(f.a)
	if _, err := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: a, AgreementVersion: "v1", IdempotencyKey: "distribution-review-before-retire"}); err != nil {
		t.Fatal(err)
	}
	token, _, err := f.sessions.Issue(f.ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE customer_identities SET status='retired',version=version+1 WHERE id=$1`, a.IdentityID); err != nil {
		t.Fatal(err)
	}
	before := distributionReview20261009Facts(t, f)
	_, resolveErr := f.sessions.Resolve(f.ctx, token)
	_, _, issueErr := f.sessions.Issue(f.ctx, a)
	_, profileErr := f.registration.Profile(f.ctx, a)
	_, registerErr := f.registration.Register(f.ctx, distributionport.RegisterCommand{Actor: a, AgreementVersion: "v1", IdempotencyKey: "distribution-review-after-retire"})
	_, prepareErr := f.registration.PrepareReceiver(f.ctx, a)
	_, earningsErr := f.reads.Earnings(f.ctx, a)
	_, commissionsErr := f.reads.ListCommissions(f.ctx, a, "", "", 10)
	for name, e := range map[string]error{"session": resolveErr, "session_issue": issueErr, "profile": profileErr, "register": registerErr, "receiver_preparation": prepareErr, "earnings": earningsErr, "commissions": commissionsErr} {
		if !errors.Is(e, distributionport.ErrUnauthorized) {
			t.Errorf("retired handle %s still authorized: %v", name, e)
		}
	}
	if after := distributionReview20261009Facts(t, f); before != after {
		t.Fatalf("retired identity appended facts: before=%v after=%v", before, after)
	}
}

func distributionReview20261009Facts(t *testing.T, f *distributionReview20261009IdentityFixture) [4]int64 {
	t.Helper()
	var v [4]int64
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM distribution_distributors),(SELECT count(*) FROM distribution_operation_receipts),(SELECT count(*) FROM distribution_audit_events),(SELECT count(*) FROM distribution_outbox)`).Scan(&v[0], &v[1], &v[2], &v[3]); err != nil {
		t.Fatal(err)
	}
	return v
}
