package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
)

func phoneRecoveryHarness(t *testing.T) *postgresHarness {
	t.Helper()
	h := newPostgresHarness(t)
	if _, err := h.pool.Native().Exec(context.Background(), `CREATE TABLE survey_submissions(id BIGINT PRIMARY KEY);CREATE TABLE survey_submission_answers(id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0038_survey_oauth_phone_vault.sql", "0220_identity_phone_canonical_recovery.sql"} {
		b, err := os.ReadFile(identityMigrationNamed(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.pool.Native().Exec(context.Background(), string(b)); err != nil {
			t.Fatal(err)
		}
	}
	vault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	h.service.Store = NewPostgresStore(vault).WithCanonicalPhoneRecovery(identityquery.PostgreSQL{})
	return h
}
func recoveryRoot(t *testing.T, h *postgresHarness, name string) (identityport.ProvisionResult, identitydomain.VerifiedFact) {
	f := testFact(t, identitydomain.KindExtension, "ext:canonical-recovery", name)
	return h.provision(t, f), f
}
func recoveryMerge(t *testing.T, h *postgresHarness, from, to identityport.ProvisionResult, f identitydomain.VerifiedFact) identityapp.LinkResult {
	c := h.link(t, identityapp.LinkCommand{SourceCustomerID: from.CustomerID, Target: f, Evidence: testEvidence(identitydomain.EvidenceStrong)})
	if c.Candidate == nil {
		t.Fatalf("no merge candidate: %+v", c)
	}
	return h.confirm(t, identityapp.ConfirmMergeCommand{CandidateID: c.Candidate.ID, SurvivorCustomerID: to.CustomerID, Operator: "canonical-recovery-synthetic"})
}
func recoveryCommand(id customerdomain.CustomerID, key string) identityport.DeclaredPhoneCommand {
	return identityport.DeclaredPhoneCommand{CustomerID: id, Phone: "13800000999", Source: "order", SourceEventID: "order:" + key + ":contact", IdempotencyKey: "order-phone:" + key}
}
func invalidRecoveryReceipt(t *testing.T, h *postgresHarness, c identityport.DeclaredPhoneCommand) {
	h.within(t, func(ctx context.Context) error {
		r, e := h.service.AttachDeclaredPhoneToCustomer(ctx, c)
		if e == nil && r.Status != identityport.DeclaredInvalid {
			t.Fatalf("original=%+v", r)
		}
		return e
	})
}
func recoverPhone(t *testing.T, h *postgresHarness, c identityport.DeclaredPhoneCommand, apply bool) identityport.PhoneRecoveryResult {
	t.Helper()
	var r identityport.PhoneRecoveryResult
	h.within(t, func(ctx context.Context) error {
		var e error
		if apply {
			r, e = h.service.RecoverDeclaredPhoneToCanonicalCustomer(ctx, c)
		} else {
			r, e = h.service.InspectDeclaredPhoneRecovery(ctx, c)
		}
		return e
	})
	return r
}

func TestPhoneCanonicalRecoveryAuditsInvalidAndNeverReassignsSource(t *testing.T) {
	h := phoneRecoveryHarness(t)
	a, _ := recoveryRoot(t, h, "a")
	b, bf := recoveryRoot(t, h, "b")
	c, cf := recoveryRoot(t, h, "c")
	recoveryMerge(t, h, a, b, bf)
	recoveryMerge(t, h, b, c, cf)
	cmd := recoveryCommand(a.CustomerID, "multi")
	invalidRecoveryReceipt(t, h, cmd)
	if r := recoverPhone(t, h, cmd, false); r.Disposition != "recoverable" || r.Binding.CustomerID != c.CustomerID {
		t.Fatalf("inspection=%+v", r)
	}
	if r := recoverPhone(t, h, cmd, true); r.Disposition != "recovered" || r.Binding.Status != identityport.DeclaredAttached || r.Binding.CustomerID != c.CustomerID {
		t.Fatalf("recovery=%+v", r)
	}
	if r := recoverPhone(t, h, cmd, true); r.Disposition != "replayed:recovery_attached" {
		t.Fatalf("replay=%+v", r)
	}
	var oldOutcome string
	var originals, audits, phones int
	if e := h.pool.Native().QueryRow(context.Background(), `SELECT (SELECT outcome FROM identity_declared_phone_receipts WHERE source_event_id=$1 AND outcome='invalid'),(SELECT count(*) FROM identity_declared_phone_receipts),(SELECT count(*) FROM identity_phone_canonical_recoveries),(SELECT count(*) FROM customer_identities WHERE kind='phone')`, cmd.SourceEventID).Scan(&oldOutcome, &originals, &audits, &phones); e != nil || oldOutcome != "invalid" || originals != 2 || audits != 1 || phones != 1 {
		t.Fatalf("old=%s receipts=%d audits=%d phones=%d err=%v", oldOutcome, originals, audits, phones, e)
	}
	// A later confirmed root change moves the existing identity. Recovery
	// follows that actual Owner fact and never creates another phone.
	d, df := recoveryRoot(t, h, "d")
	recoveryMerge(t, h, c, d, df)
	if r := recoverPhone(t, h, cmd, true); r.Disposition != "replayed:recovery_attached" || r.Binding.CustomerID != d.CustomerID {
		t.Fatalf("later root=%+v", r)
	}
	changed := cmd
	changed.Phone = "13900000999"
	if e := h.run(func(ctx context.Context) error {
		_, e := h.service.RecoverDeclaredPhoneToCanonicalCustomer(ctx, changed)
		return e
	}); !errors.Is(e, identityapp.ErrDeclaredPayloadMismatch) {
		t.Fatalf("changed payload err=%v", e)
	}
	// Reversal/identity drift cannot make the same source bind a second root.
	if _, e := h.pool.Native().Exec(context.Background(), `UPDATE customer_identities SET status='retired' WHERE kind='phone'`); e != nil {
		t.Fatal(e)
	}
	if r := recoverPhone(t, h, cmd, true); r.Disposition != "recovery_identity_drift" {
		t.Fatalf("drift=%+v", r)
	}
}

func TestPhoneCanonicalRecoveryPreservesConflictSuccessAndReversedMerge(t *testing.T) {
	h := phoneRecoveryHarness(t)
	a, _ := recoveryRoot(t, h, "ca")
	b, bf := recoveryRoot(t, h, "cb")
	other, _ := recoveryRoot(t, h, "other")
	ownerCmd := recoveryCommand(other.CustomerID, "owner")
	h.within(t, func(ctx context.Context) error {
		_, e := h.service.AttachDeclaredPhoneToCustomer(ctx, ownerCmd)
		return e
	})
	recoveryMerge(t, h, a, b, bf)
	cmd := recoveryCommand(a.CustomerID, "conflict")
	invalidRecoveryReceipt(t, h, cmd)
	if r := recoverPhone(t, h, cmd, true); r.Disposition != "recovery_conflict" {
		t.Fatalf("conflict=%+v", r)
	}
	if r := recoverPhone(t, h, cmd, true); r.Disposition != "replayed:recovery_conflict" {
		t.Fatalf("conflict replay=%+v", r)
	}
	if r := recoverPhone(t, h, ownerCmd, true); r.Disposition != "original:attached" {
		t.Fatalf("prior success=%+v", r)
	}
	oldConflict := recoveryCommand(b.CustomerID, "old-conflict")
	h.within(t, func(ctx context.Context) error {
		r, e := h.service.AttachDeclaredPhoneToCustomer(ctx, oldConflict)
		if r.Status != identityport.DeclaredConflict {
			t.Fatalf("original=%+v", r)
		}
		return e
	})
	if r := recoverPhone(t, h, oldConflict, true); r.Disposition != "original:conflict" {
		t.Fatalf("prior conflict=%+v", r)
	}
	x, _ := recoveryRoot(t, h, "reverse-a")
	y, yf := recoveryRoot(t, h, "reverse-b")
	m := recoveryMerge(t, h, x, y, yf)
	c := recoveryCommand(x.CustomerID, "reversed")
	invalidRecoveryReceipt(t, h, c)
	h.within(t, func(ctx context.Context) error { _, e := h.service.RevertConfirmedMerge(ctx, m.Merge.ID); return e })
	if r := recoverPhone(t, h, c, true); r.Disposition != "invalid_without_active_alias" {
		t.Fatalf("reversed=%+v", r)
	}
}

func TestPhoneCanonicalRecoveryRejectsUnauditedCorruptAndClosedRoots(t *testing.T) {
	for _, kind := range []string{"unaudited", "cycle", "closed", "broken", "depth"} {
		t.Run(kind, func(t *testing.T) {
			h := phoneRecoveryHarness(t)
			a, _ := recoveryRoot(t, h, kind+"a")
			b, _ := recoveryRoot(t, h, kind+"b")
			ctx := context.Background()
			if _, e := h.pool.Native().Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now() WHERE id=$1`, a.CustomerID, b.CustomerID); e != nil {
				t.Fatal(e)
			}
			cmd := recoveryCommand(a.CustomerID, kind)
			invalidRecoveryReceipt(t, h, cmd)
			switch kind {
			case "cycle":
				_, _ = h.pool.Native().Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now() WHERE id=$1`, b.CustomerID, a.CustomerID)
			case "closed":
				_, _ = h.pool.Native().Exec(ctx, `UPDATE customers SET status='closed' WHERE id=$1`, b.CustomerID)
			case "broken":
				_, e := h.pool.Native().Exec(ctx, `ALTER TABLE customers DROP CONSTRAINT ck_customers_merged_state`)
				if e != nil {
					t.Fatal(e)
				}
				_, e = h.pool.Native().Exec(ctx, `UPDATE customers SET merged_into_customer_id=NULL WHERE id=$1`, a.CustomerID)
				if e != nil {
					t.Fatal(e)
				}
			case "depth":
				prev := b.CustomerID
				for i := 0; i < 128; i++ {
					next, _ := recoveryRoot(t, h, fmt.Sprintf("deep-%d", i))
					if _, e := h.pool.Native().Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now() WHERE id=$1`, prev, next.CustomerID); e != nil {
						t.Fatal(e)
					}
					prev = next.CustomerID
				}
			}
			r := recoverPhone(t, h, cmd, true)
			if r.Disposition != "unaudited_alias" && r.Disposition != "unsafe_lineage" && r.Disposition != "invalid_without_active_alias" {
				t.Fatalf("unsafe result=%+v", r)
			}
			var n int
			if e := h.pool.Native().QueryRow(ctx, `SELECT count(*) FROM identity_phone_canonical_recoveries`).Scan(&n); e != nil || n != 0 {
				t.Fatalf("unsafe audit count=%d err=%v", n, e)
			}
		})
	}
}

func TestPhoneCanonicalRecoveryConcurrentOriginalSourceIsOnce(t *testing.T) {
	h := phoneRecoveryHarness(t)
	a, _ := recoveryRoot(t, h, "parallel-a")
	b, bf := recoveryRoot(t, h, "parallel-b")
	recoveryMerge(t, h, a, b, bf)
	cmd := recoveryCommand(a.CustomerID, "parallel")
	invalidRecoveryReceipt(t, h, cmd)
	var wg sync.WaitGroup
	results := make(chan identityport.PhoneRecoveryResult, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r identityport.PhoneRecoveryResult
			e := h.run(func(ctx context.Context) error {
				var e error
				r, e = h.service.RecoverDeclaredPhoneToCanonicalCustomer(ctx, cmd)
				return e
			})
			results <- r
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	n := 0
	for r := range results {
		if r.Disposition == "recovered" {
			n++
		} else if r.Disposition != "replayed:recovery_attached" {
			t.Fatalf("concurrent=%+v", r)
		}
	}
	if n != 1 {
		t.Fatalf("new recoveries=%d", n)
	}
}

type recoveryObservedRoots struct {
	reader   identityport.CanonicalCustomerRootsReader
	observed chan struct{}
	resume   chan struct{}
	once     sync.Once
}

func (r *recoveryObservedRoots) CanonicalCustomerRoots(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerdomain.CustomerID, error) {
	roots, err := r.reader.CanonicalCustomerRoots(ctx, ids)
	if err == nil {
		r.once.Do(func() {
			close(r.observed)
			select {
			case <-r.resume:
			case <-ctx.Done():
			}
		})
	}
	return roots, err
}
func TestPhoneCanonicalRecoveryConcurrentMergeCannotUseObservedOldRoot(t *testing.T) {
	h := phoneRecoveryHarness(t)
	a, _ := recoveryRoot(t, h, "race-a")
	b, bf := recoveryRoot(t, h, "race-b")
	c, cf := recoveryRoot(t, h, "race-c")
	recoveryMerge(t, h, a, b, bf)
	cmd := recoveryCommand(a.CustomerID, "merge-race")
	invalidRecoveryReceipt(t, h, cmd)
	r := &recoveryObservedRoots{reader: identityquery.PostgreSQL{}, observed: make(chan struct{}), resume: make(chan struct{})}
	h.service.Store.(*PostgresStore).WithCanonicalPhoneRecovery(r)
	done := make(chan identityport.PhoneRecoveryResult, 1)
	errs := make(chan error, 1)
	go func() {
		var result identityport.PhoneRecoveryResult
		e := h.run(func(ctx context.Context) error {
			var e error
			result, e = h.service.RecoverDeclaredPhoneToCanonicalCustomer(ctx, cmd)
			return e
		})
		done <- result
		errs <- e
	}()
	<-r.observed
	recoveryMerge(t, h, b, c, cf)
	close(r.resume)
	result := <-done
	if e := <-errs; e != nil {
		t.Fatal(e)
	}
	if result.Disposition != "canonical_changed" {
		t.Fatalf("stale observed root=%+v", result)
	}
	if r := recoverPhone(t, h, cmd, true); r.Disposition != "recovered" || r.Binding.CustomerID != c.CustomerID {
		t.Fatalf("fresh retry=%+v", r)
	}
}
