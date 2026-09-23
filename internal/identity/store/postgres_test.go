package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgresStoreRejectsWritesOutsideTransaction(t *testing.T) {
	store := NewPostgresStore()
	fact := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:test", "outside")
	if _, err := store.Provision(context.Background(), fact); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("Provision outside transaction error=%v", err)
	}
}

func TestPostgresStoreIntegration(t *testing.T) {
	pool, cleanup := identityPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore()
	service := identityapp.OneIDService{Store: store}
	within := func(fn func(context.Context) error) {
		if err := unit.Within(context.Background(), fn); err != nil {
			t.Fatalf("identity transaction: %s", safePostgresDiagnostic(err))
		}
	}

	t.Run("same key concurrent provision has one root", func(t *testing.T) {
		fact := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:test", "concurrent")
		var wg sync.WaitGroup
		ids := make(chan int64, 12)
		errs := make(chan error, 12)
		for range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := unit.Within(context.Background(), func(ctx context.Context) error {
					got, e := service.ProvisionCustomerFromVerifiedIdentity(ctx, fact)
					if e == nil {
						ids <- int64(got.CustomerID)
					}
					return e
				})
				if err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(ids)
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		var first int64
		for id := range ids {
			if first == 0 {
				first = id
			}
			if id != first {
				t.Fatalf("got roots %d and %d", first, id)
			}
		}
	})

	var wecom, alipay identityport.ProvisionResult
	wecomFact := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:test", "wecom-a")
	aliFact := testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:test", "ali-a")
	within(func(ctx context.Context) error {
		var e error
		wecom, e = service.ProvisionCustomerFromVerifiedIdentity(ctx, wecomFact)
		if e != nil {
			return e
		}
		alipay, e = service.ProvisionCustomerFromVerifiedIdentity(ctx, aliFact)
		return e
	})

	var candidate identityapp.LinkResult
	within(func(ctx context.Context) error {
		var e error
		candidate, e = service.LinkVerifiedIdentity(ctx, identityapp.LinkCommand{SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: testEvidence(identitydomain.EvidenceStrong)})
		return e
	})
	if candidate.Status != identityapp.LinkCandidate || candidate.Candidate == nil {
		t.Fatalf("candidate=%+v", candidate)
	}
	within(func(ctx context.Context) error {
		again, e := service.LinkVerifiedIdentity(ctx, identityapp.LinkCommand{SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: testEvidence(identitydomain.EvidenceStrong)})
		if e != nil {
			return e
		}
		if again.Candidate == nil || again.Candidate.ID != candidate.Candidate.ID {
			t.Fatalf("candidate duplicate=%+v", again)
		}
		return nil
	})

	t.Run("strong wecom conflict fails closed", func(t *testing.T) {
		other := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:test", "wecom-b")
		var otherRoot identityport.ProvisionResult
		within(func(ctx context.Context) error {
			var e error
			otherRoot, e = service.ProvisionCustomerFromVerifiedIdentity(ctx, other)
			return e
		})
		within(func(ctx context.Context) error {
			c, e := service.LinkVerifiedIdentity(ctx, identityapp.LinkCommand{SourceCustomerID: otherRoot.CustomerID, Target: wecomFact, Evidence: testEvidence(identitydomain.EvidenceStrong)})
			if e != nil {
				return e
			}
			got, e := service.ConfirmMerge(ctx, identityapp.ConfirmMergeCommand{CandidateID: c.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "test"})
			if e != nil {
				return e
			}
			if got.Status != identityapp.LinkConflict {
				t.Fatalf("conflict=%+v", got)
			}
			return nil
		})
	})

	var merged identityapp.LinkResult
	within(func(ctx context.Context) error {
		var e error
		merged, e = service.ConfirmMerge(ctx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "test"})
		return e
	})
	if merged.Status != identityapp.LinkMerged || merged.Merge == nil {
		t.Fatalf("merge=%+v", merged)
	}
	within(func(ctx context.Context) error { _, e := service.RevertConfirmedMerge(ctx, merged.Merge.ID); return e })

	t.Run("intent replay and payload drift", func(t *testing.T) {
		var intent identityapp.CreatedLinkIntent
		within(func(ctx context.Context) error {
			var e error
			intent, e = service.CreateLinkIntent(ctx, identityapp.LinkIntentCommand{SourceCustomerID: alipay.CustomerID, Purpose: identityapp.LinkIntentBindWeCom, TargetKind: identitydomain.KindWeComExternalUserID, ExpectedScope: "wecom-corp:test", ExpiresAt: time.Now().Add(time.Minute), Source: "test.intent"})
			return e
		})
		target := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:test", "intent")
		within(func(ctx context.Context) error {
			got, e := service.ConsumeLinkIntent(ctx, identityapp.ConsumeLinkIntentCommand{Token: intent.Token, Target: target, Evidence: testEvidence(identitydomain.EvidenceStrong)})
			if e != nil {
				return e
			}
			if got.Status != identityapp.LinkAttached {
				return errors.New("unexpected intent attach")
			}
			return nil
		})
		within(func(ctx context.Context) error {
			got, e := service.ConsumeLinkIntent(ctx, identityapp.ConsumeLinkIntentCommand{Token: intent.Token, Target: target, Evidence: testEvidence(identitydomain.EvidenceStrong)})
			if e != nil {
				return e
			}
			if got.Status != identityapp.LinkIntentReplay {
				return errors.New("missing replay")
			}
			return nil
		})
		if err := unit.Within(context.Background(), func(ctx context.Context) error {
			_, e := service.ConsumeLinkIntent(ctx, identityapp.ConsumeLinkIntentCommand{Token: intent.Token, Target: testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:test", "drift"), Evidence: testEvidence(identitydomain.EvidenceStrong)})
			return e
		}); !errors.Is(err, identityapp.ErrLinkIntentPayloadMismatch) {
			t.Fatalf("drift=%v", err)
		}
	})
}

func testFact(t *testing.T, kind identitydomain.Kind, scope, value string) identitydomain.VerifiedFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: kind, Scope: scope, Value: value, Source: "postgres.test"})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}
func testEvidence(s identitydomain.EvidenceStrength) identitydomain.LinkEvidence {
	return identitydomain.LinkEvidence{Type: "test", Strength: s, Source: "postgres.test", EventID: "event", Digest: "digest", PolicyVersion: "v1"}
}

func identityPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping true PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_identity_test_" + hex.EncodeToString(b[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatal(err)
	}
	path := identityMigration(t)
	sql, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
func identityMigration(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0002_identity.sql"))
}
