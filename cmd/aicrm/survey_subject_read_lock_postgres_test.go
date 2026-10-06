package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLSurveySubjectReadPinsLineageUntilCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	loserFact := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:subject-read", "synthetic-subject-loser")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:subject-read", "synthetic-survivor-own-handle")
	var loserID, survivorID customerdomain.CustomerID
	var survivorHandle, mergeID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		loser, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, loserFact)
		if e != nil {
			return e
		}
		survivor, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, survivorFact)
		if e != nil {
			return e
		}
		loserID, survivorID, survivorHandle = loser.CustomerID, survivor.CustomerID, survivor.IdentityID
		candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "survey.subject.read", EventID: "synthetic-read-lock", Digest: "synthetic-read-lock-evidence", PolicyVersion: "test-v1"}})
		if e != nil {
			return e
		}
		if candidate.Candidate == nil {
			t.Fatal("expected actual merge candidate")
		}
		merged, e := oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "synthetic-read-lock"})
		if merged.Merge != nil {
			mergeID = merged.Merge.ID
		}
		return e
	}); err != nil || mergeID < 1 {
		t.Fatalf("actual merge: error=%v", err)
	}
	// Hold the actual subject read transaction open while a separate real
	// reversal command attempts its normal roots-first mutation. No sleep or
	// mocked subject is used; the local timeout bounds only the lock probe.
	readTx, err := native.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer readTx.Rollback(ctx)
	queries := identityquery.NewPostgreSQL()
	subject, found, err := queries.VerifiedIdentitySubject(platformpostgres.BindTransaction(ctx, readTx), survivorHandle)
	if err != nil || !found || subject.CustomerID != survivorID || len(subject.Lineage) != 2 {
		t.Fatalf("actual survivor subject: found=%v subject=%+v error=%v", found, subject, err)
	}
	revertTx, err := native.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer revertTx.Rollback(ctx)
	if _, err = revertTx.Exec(ctx, `SET LOCAL lock_timeout='150ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err = revertTx.Exec(ctx, `SAVEPOINT root_lock_probe`); err != nil {
		t.Fatal(err)
	}
	_, lockErr := revertTx.Exec(ctx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) ORDER BY id FOR UPDATE NOWAIT`, []int64{int64(loserID), int64(survivorID)})
	// Identity intentionally hides SQLSTATE outside its Store. Probe that exact
	// root lock directly, reset the savepoint, then invoke the actual mutation.
	if _, err = revertTx.Exec(ctx, `ROLLBACK TO SAVEPOINT root_lock_probe`); err != nil {
		t.Fatal(err)
	}
	_, revertErr := oneID.RevertConfirmedMerge(platformpostgres.BindTransaction(ctx, revertTx), mergeID)
	if revertErr == nil {
		if err = revertTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		t.Fatal("actual Revert committed while subject read still retained the previous two-customer lineage")
	}
	var postgresErr *pgconn.PgError
	if !errors.As(lockErr, &postgresErr) || postgresErr.Code != "55P03" {
		t.Fatalf("actual reversal rejection must correspond to pinned root locks: probe=%v mutation=%v", lockErr, revertErr)
	}
	if err = revertTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = readTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, mergeID); return e }); err != nil {
		t.Fatalf("reversal must resume after subject read commits: %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		current, ok, e := queries.VerifiedIdentitySubject(tx, survivorHandle)
		if e != nil {
			return e
		}
		if !ok || current.CustomerID != survivorID || len(current.Lineage) != 1 || current.Lineage[0] != survivorID {
			return errors.New("survivor's unchanged own handle retained detached customer's lineage")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
