package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type postgresHarness struct {
	pool    *platformpostgres.Pool
	unit    *platformpostgres.UnitOfWork
	service identityapp.OneIDService
}

func newPostgresHarness(t *testing.T) *postgresHarness {
	t.Helper()
	pool, cleanup := identityPool(t)
	t.Cleanup(cleanup)
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	return &postgresHarness{pool: pool, unit: unit, service: identityapp.OneIDService{Store: NewPostgresStore()}}
}

func (h *postgresHarness) run(fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return h.unit.Within(ctx, fn)
}

func (h *postgresHarness) within(t *testing.T, fn func(context.Context) error) {
	t.Helper()
	if err := h.run(fn); err != nil {
		t.Fatalf("identity transaction: %s", safePostgresDiagnostic(err))
	}
}

func (h *postgresHarness) provision(t *testing.T, fact identitydomain.VerifiedFact) identityport.ProvisionResult {
	t.Helper()
	var result identityport.ProvisionResult
	h.within(t, func(ctx context.Context) error {
		var err error
		result, err = h.service.ProvisionCustomerFromVerifiedIdentity(ctx, fact)
		return err
	})
	return result
}

func (h *postgresHarness) link(t *testing.T, command identityapp.LinkCommand) identityapp.LinkResult {
	t.Helper()
	var result identityapp.LinkResult
	h.within(t, func(ctx context.Context) error {
		var err error
		result, err = h.service.LinkVerifiedIdentity(ctx, command)
		return err
	})
	return result
}

func (h *postgresHarness) confirm(t *testing.T, command identityapp.ConfirmMergeCommand) identityapp.LinkResult {
	t.Helper()
	var result identityapp.LinkResult
	h.within(t, func(ctx context.Context) error {
		var err error
		result, err = h.service.ConfirmMerge(ctx, command)
		return err
	})
	return result
}

func safePostgresDiagnostic(err error) string {
	var failure *persistenceError
	if !errors.As(err, &failure) {
		return err.Error()
	}
	if failure.constraint == "" {
		return fmt.Sprintf("%s (sqlstate=%s)", failure.Error(), failure.code)
	}
	return fmt.Sprintf("%s (sqlstate=%s constraint=%s)", failure.Error(), failure.code, failure.constraint)
}

func TestPersistenceErrorExposesOnlySafePostgresLabelsToTests(t *testing.T) {
	postgresErr := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "ux_safe_constraint",
		Message:        "duplicate secret@example.test",
		Detail:         "token=do-not-leak",
		Where:          "SQL statement containing private-value",
	}
	err := persistenceFailure(postgresErr)
	if err.Error() != "identity persistence failed" {
		t.Fatalf("public error=%q", err)
	}
	diagnostic := safePostgresDiagnostic(err)
	if diagnostic != "identity persistence failed (sqlstate=23505 constraint=ux_safe_constraint)" {
		t.Fatalf("safe diagnostic=%q", diagnostic)
	}
	for _, secret := range []string{"secret@example.test", "do-not-leak", "private-value"} {
		if strings.Contains(diagnostic, secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, diagnostic)
		}
	}
}

func TestPostgresStaleCandidateRejectionCommitsWithoutMerge(t *testing.T) {
	h := newPostgresHarness(t)
	leftFact := testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:stale-candidate", "left")
	rightFact := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:stale-candidate", "right")
	left := h.provision(t, leftFact)
	right := h.provision(t, rightFact)
	candidate := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: left.CustomerID, Target: rightFact, Evidence: testEvidence(identitydomain.EvidenceStrong),
	})
	if candidate.Candidate == nil {
		t.Fatalf("candidate=%+v", candidate)
	}
	attached := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: left.CustomerID,
		Target:           testFact(t, identitydomain.KindExtension, "ext:stale-candidate", "later"),
		Evidence:         testEvidence(identitydomain.EvidenceStrong),
	})
	if attached.Status != identityapp.LinkAttached {
		t.Fatalf("attach=%+v", attached)
	}

	rejected := h.confirm(t, identityapp.ConfirmMergeCommand{
		CandidateID: candidate.Candidate.ID, SurvivorCustomerID: left.CustomerID, Operator: "postgres-hardening-stale",
	})
	if rejected.Status != identityapp.LinkCandidateRejected || rejected.Candidate == nil ||
		rejected.Candidate.ID != candidate.Candidate.ID || rejected.Candidate.Status != "rejected" || rejected.Merge != nil {
		t.Fatalf("rejected=%+v", rejected)
	}
	var status, resolvedBy string
	var resolved, survivorUnset bool
	var candidateVersion, mergeCount int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT status,resolved_by,resolved_at IS NOT NULL,selected_survivor_customer_id IS NULL,version,(SELECT count(*) FROM customer_merges WHERE candidate_id=$1) FROM customer_merge_candidates WHERE id=$1`, candidate.Candidate.ID).Scan(&status, &resolvedBy, &resolved, &survivorUnset, &candidateVersion, &mergeCount); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" || resolvedBy != "system" || !resolved || !survivorUnset || candidateVersion != 2 || mergeCount != 0 {
		t.Fatalf("candidate status=%s resolved_by=%s resolved=%v survivor_unset=%v version=%d merges=%d", status, resolvedBy, resolved, survivorUnset, candidateVersion, mergeCount)
	}
	assertCustomerState(t, h, left.CustomerID, "active", 0, candidate.Candidate.LeftVersion+1, 1)
	assertCustomerState(t, h, right.CustomerID, "active", 0, candidate.Candidate.RightVersion, 1)

	replacement := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: left.CustomerID, Target: rightFact, Evidence: testEvidence(identitydomain.EvidenceStrong),
	})
	if replacement.Status != identityapp.LinkCandidate || replacement.Candidate == nil ||
		replacement.Candidate.ID == candidate.Candidate.ID || replacement.Candidate.Status != "open" {
		t.Fatalf("replacement=%+v", replacement)
	}
}

func TestPostgresMergeLedgerAndReverseUseExactSnapshots(t *testing.T) {
	h := newPostgresHarness(t)
	wecomFact := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:roundtrip", "wecom-survivor")
	loserFact := testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:roundtrip", "alipay-loser")
	wecom := h.provision(t, wecomFact)
	loser := h.provision(t, loserFact)
	extraLoser := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: loser.CustomerID,
		Target:           testFact(t, identitydomain.KindExtension, "ext:merge-roundtrip", "extra-identity"),
		Evidence:         testEvidence(identitydomain.EvidenceStrong),
	})
	if extraLoser.Status != identityapp.LinkAttached {
		t.Fatalf("extra loser identity=%+v", extraLoser)
	}
	candidateResult := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: loser.CustomerID,
		Target:           wecomFact,
		Evidence:         testEvidence(identitydomain.EvidenceStrong),
	})
	if candidateResult.Candidate == nil {
		t.Fatalf("candidate=%+v", candidateResult)
	}
	candidate := *candidateResult.Candidate
	merged := h.confirm(t, identityapp.ConfirmMergeCommand{
		CandidateID:        candidate.ID,
		SurvivorCustomerID: wecom.CustomerID,
		Operator:           "postgres-hardening-test",
	})
	if merged.Status != identityapp.LinkMerged || merged.Merge == nil || merged.Candidate == nil || merged.Candidate.Status != "confirmed" {
		t.Fatalf("merged=%+v", merged)
	}

	var candidateStatus string
	var selectedSurvivor, candidateVersion int64
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT status,selected_survivor_customer_id,version FROM customer_merge_candidates WHERE id=$1`, candidate.ID).Scan(&candidateStatus, &selectedSurvivor, &candidateVersion); err != nil {
		t.Fatal(err)
	}
	if candidateStatus != "confirmed" || selectedSurvivor != int64(wecom.CustomerID) || candidateVersion != 2 {
		t.Fatalf("candidate state=%s survivor=%d version=%d", candidateStatus, selectedSurvivor, candidateVersion)
	}

	var ledgerMatches bool
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT m.candidate_left_customer_id=c.left_customer_id AND m.candidate_right_customer_id=c.right_customer_id AND m.to_customer_id=c.selected_survivor_customer_id AND m.evidence_id=c.evidence_id AND m.from_customer_version_after=m.from_customer_version_before+1 AND m.to_customer_version_after=m.to_customer_version_before+1 AND m.from_lineage_version_after=m.from_lineage_version_before+1 AND m.to_lineage_version_after=m.to_lineage_version_before+1 FROM customer_merges m JOIN customer_merge_candidates c ON c.id=m.candidate_id WHERE m.id=$1`, merged.Merge.ID).Scan(&ledgerMatches); err != nil {
		t.Fatal(err)
	}
	if !ledgerMatches {
		t.Fatal("merge ledger did not preserve the confirmed candidate and version steps")
	}

	var movedCount int
	var memberSnapshotsValid bool
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT count(*),bool_and(m.identity_version_after=m.identity_version_before+1 AND i.customer_id=m.to_customer_id AND i.version=m.identity_version_after) FROM customer_merge_identity_members m JOIN customer_identities i ON i.id=m.identity_id WHERE m.merge_id=$1`, merged.Merge.ID).Scan(&movedCount, &memberSnapshotsValid); err != nil {
		t.Fatal(err)
	}
	if movedCount != 2 || !memberSnapshotsValid {
		t.Fatalf("moved members=%d valid=%t", movedCount, memberSnapshotsValid)
	}

	later := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: wecom.CustomerID,
		Target:           testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:roundtrip", "later-member"),
		Evidence:         testEvidence(identitydomain.EvidenceStrong),
	})
	if later.Status != identityapp.LinkAttached {
		t.Fatalf("later identity=%+v", later)
	}
	var reverted identityapp.MergeRecord
	h.within(t, func(ctx context.Context) error {
		var err error
		reverted, err = h.service.RevertConfirmedMerge(ctx, merged.Merge.ID)
		return err
	})
	if !reverted.Reversed || reverted.Evidence != merged.Merge.Evidence {
		t.Fatalf("reverted=%+v", reverted)
	}

	var restoredCount int
	var restoredSnapshotsValid bool
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT count(*),bool_and(m.restored_at IS NOT NULL AND m.identity_version_after_restore=m.identity_version_after+1 AND i.customer_id=m.from_customer_id AND i.version=m.identity_version_after_restore) FROM customer_merge_identity_members m JOIN customer_identities i ON i.id=m.identity_id WHERE m.merge_id=$1`, merged.Merge.ID).Scan(&restoredCount, &restoredSnapshotsValid); err != nil {
		t.Fatal(err)
	}
	if restoredCount != 2 || !restoredSnapshotsValid {
		t.Fatalf("restored members=%d valid=%t", restoredCount, restoredSnapshotsValid)
	}
	var laterOwner int64
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT customer_id FROM customer_identities WHERE id=$1`, later.IdentityID).Scan(&laterOwner); err != nil {
		t.Fatal(err)
	}
	if laterOwner != int64(wecom.CustomerID) {
		t.Fatalf("later identity moved to %d", laterOwner)
	}

	assertCustomerState(t, h, loser.CustomerID, "active", 0, merged.Merge.FromVersionAfter+1, merged.Merge.FromLineageAfter+1)
	assertCustomerState(t, h, wecom.CustomerID, "active", 0, merged.Merge.ToVersionAfter+2, merged.Merge.ToLineageAfter+1)
	var reversibleStatus string
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT reversible_status FROM customer_merges WHERE id=$1`, merged.Merge.ID).Scan(&reversibleStatus); err != nil {
		t.Fatal(err)
	}
	if reversibleStatus != "reversed" {
		t.Fatalf("reversible_status=%s", reversibleStatus)
	}
}

func assertCustomerState(t *testing.T, h *postgresHarness, id customerdomain.CustomerID, wantStatus string, wantMergedTo, wantVersion, wantLineage int64) {
	t.Helper()
	var status string
	var mergedTo, version, lineage int64
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT status,COALESCE(merged_into_customer_id,0),version,lineage_version FROM customers WHERE id=$1`, id).Scan(&status, &mergedTo, &version, &lineage); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || mergedTo != wantMergedTo || version != wantVersion || lineage != wantLineage {
		t.Fatalf("customer %d state=%s merged_to=%d version=%d lineage=%d", id, status, mergedTo, version, lineage)
	}
}

func createReversibleMerge(t *testing.T, h *postgresHarness, suffix string) (identityport.ProvisionResult, identityport.ProvisionResult, identitydomain.VerifiedFact, identityapp.LinkResult) {
	t.Helper()
	survivorFact := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:"+suffix, "wecom-"+suffix)
	loserFact := testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:"+suffix, "alipay-"+suffix)
	survivor := h.provision(t, survivorFact)
	loser := h.provision(t, loserFact)
	extra := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: loser.CustomerID,
		Target:           testFact(t, identitydomain.KindExtension, "ext:"+suffix, "extra-"+fmt.Sprintf("%08d", loser.CustomerID)),
		Evidence:         testEvidence(identitydomain.EvidenceStrong),
	})
	if extra.Status != identityapp.LinkAttached {
		t.Fatalf("extra member=%+v", extra)
	}
	candidate := h.link(t, identityapp.LinkCommand{SourceCustomerID: loser.CustomerID, Target: survivorFact, Evidence: testEvidence(identitydomain.EvidenceStrong)})
	if candidate.Candidate == nil {
		t.Fatalf("candidate=%+v", candidate)
	}
	merged := h.confirm(t, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivor.CustomerID, Operator: "postgres-hardening-test"})
	if merged.Merge == nil {
		t.Fatalf("merge=%+v", merged)
	}
	return survivor, loser, survivorFact, merged
}

func TestPostgresReverseRejectsSnapshotOrLineageDriftAtomically(t *testing.T) {
	t.Run("member version drift", func(t *testing.T) {
		h := newPostgresHarness(t)
		survivor, loser, _, merged := createReversibleMerge(t, h, "member-drift")
		var tamperedIdentity int64
		if err := h.pool.Native().QueryRow(context.Background(), `SELECT identity_id FROM customer_merge_identity_members WHERE merge_id=$1 ORDER BY identity_id DESC LIMIT 1`, merged.Merge.ID).Scan(&tamperedIdentity); err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Native().Exec(context.Background(), `UPDATE customer_identities SET version=version+1 WHERE id=$1`, tamperedIdentity); err != nil {
			t.Fatal(err)
		}
		err := h.run(func(ctx context.Context) error {
			_, revertErr := h.service.RevertConfirmedMerge(ctx, merged.Merge.ID)
			return revertErr
		})
		if !errors.Is(err, identityapp.ErrMergeNotReversible) {
			t.Fatalf("member drift reverse=%v", err)
		}
		var memberCount, stillOwned, restored int
		if err = h.pool.Native().QueryRow(context.Background(), `SELECT count(*),count(*) FILTER (WHERE i.customer_id=$2),count(*) FILTER (WHERE m.restored_at IS NOT NULL) FROM customer_merge_identity_members m JOIN customer_identities i ON i.id=m.identity_id WHERE m.merge_id=$1`, merged.Merge.ID, survivor.CustomerID).Scan(&memberCount, &stillOwned, &restored); err != nil {
			t.Fatal(err)
		}
		if memberCount != 2 || stillOwned != memberCount || restored != 0 {
			t.Fatalf("failed reverse partially moved members: count=%d survivor=%d restored=%d", memberCount, stillOwned, restored)
		}
		assertCustomerState(t, h, loser.CustomerID, "merged", int64(survivor.CustomerID), merged.Merge.FromVersionAfter, merged.Merge.FromLineageAfter)
		assertCustomerState(t, h, survivor.CustomerID, "active", 0, merged.Merge.ToVersionAfter, merged.Merge.ToLineageAfter)
	})

	t.Run("lineage drift", func(t *testing.T) {
		h := newPostgresHarness(t)
		survivor, loser, _, merged := createReversibleMerge(t, h, "lineage-drift")
		if _, err := h.pool.Native().Exec(context.Background(), `UPDATE customers SET lineage_version=lineage_version+1 WHERE id=$1`, survivor.CustomerID); err != nil {
			t.Fatal(err)
		}
		err := h.run(func(ctx context.Context) error {
			_, revertErr := h.service.RevertConfirmedMerge(ctx, merged.Merge.ID)
			return revertErr
		})
		if !errors.Is(err, identityapp.ErrMergeNotReversible) {
			t.Fatalf("lineage drift reverse=%v", err)
		}
		var movedBack int
		if err = h.pool.Native().QueryRow(context.Background(), `SELECT count(*) FROM customer_merge_identity_members m JOIN customer_identities i ON i.id=m.identity_id WHERE m.merge_id=$1 AND i.customer_id=$2`, merged.Merge.ID, loser.CustomerID).Scan(&movedBack); err != nil {
			t.Fatal(err)
		}
		if movedBack != 0 {
			t.Fatalf("lineage failure moved %d identities", movedBack)
		}
	})
}

func TestPostgresReverseRejectsEveryLaterRelatedMerge(t *testing.T) {
	h := newPostgresHarness(t)
	survivor, loser, survivorFact, firstMerge := createReversibleMerge(t, h, "later-merge")
	thirdFact := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:later-merge", "third-root")
	third := h.provision(t, thirdFact)
	secondCandidate := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: third.CustomerID,
		Target:           survivorFact,
		Evidence:         testEvidence(identitydomain.EvidenceStrong),
	})
	secondMerge := h.confirm(t, identityapp.ConfirmMergeCommand{
		CandidateID:        secondCandidate.Candidate.ID,
		SurvivorCustomerID: survivor.CustomerID,
		Operator:           "postgres-hardening-test-2",
	})
	if secondMerge.Merge == nil {
		t.Fatalf("second merge=%+v", secondMerge)
	}
	h.within(t, func(ctx context.Context) error {
		_, err := h.service.RevertConfirmedMerge(ctx, secondMerge.Merge.ID)
		return err
	})
	err := h.run(func(ctx context.Context) error {
		_, revertErr := h.service.RevertConfirmedMerge(ctx, firstMerge.Merge.ID)
		return revertErr
	})
	if !errors.Is(err, identityapp.ErrMergeNotReversible) {
		t.Fatalf("first reverse after later reversed merge=%v", err)
	}
	assertCustomerState(t, h, loser.CustomerID, "merged", int64(survivor.CustomerID), firstMerge.Merge.FromVersionAfter, firstMerge.Merge.FromLineageAfter)
	if third.CustomerID == loser.CustomerID {
		t.Fatal("test roots unexpectedly overlap")
	}
}

func TestPostgresLinkIntentPersistsVersionFingerprintAndStableResultSnapshot(t *testing.T) {
	h := newPostgresHarness(t)
	sourceFact := testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:intent-snapshot", "source")
	targetFact := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:intent-snapshot", "target")
	source := h.provision(t, sourceFact)
	target := h.provision(t, targetFact)
	var intent identityapp.CreatedLinkIntent
	h.within(t, func(ctx context.Context) error {
		var err error
		intent, err = h.service.CreateLinkIntent(ctx, identityapp.LinkIntentCommand{
			SourceCustomerID: source.CustomerID,
			Purpose:          identityapp.LinkIntentBindProviderIdentity,
			TargetKind:       identitydomain.KindFirstPartyMemberID,
			ExpectedScope:    "first-party:intent-snapshot",
			ExpiresAt:        time.Now().Add(time.Minute),
			Source:           "postgres.hardening",
			SourceEventID:    "intent-snapshot",
		})
		return err
	})
	consume := identityapp.ConsumeLinkIntentCommand{Token: intent.Token, Target: targetFact, Evidence: testEvidence(identitydomain.EvidenceStrong)}
	var first identityapp.LinkResult
	h.within(t, func(ctx context.Context) error {
		var err error
		first, err = h.service.ConsumeLinkIntent(ctx, consume)
		return err
	})
	if first.Status != identityapp.LinkCandidate || first.Candidate == nil || first.Candidate.Status != "open" {
		t.Fatalf("first consumption=%+v", first)
	}

	var storedHash, fingerprint, status, resultStatus string
	var sourceVersion, storedCandidate int64
	var hasSnapshot bool
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT token_hash,source_customer_version,status,consumption_fingerprint,result_status,result_candidate_id,metadata_json ? 'result_snapshot' FROM identity_link_intents WHERE id=$1`, intent.ID).Scan(&storedHash, &sourceVersion, &status, &fingerprint, &resultStatus, &storedCandidate, &hasSnapshot); err != nil {
		t.Fatal(err)
	}
	if storedHash == intent.Token || storedHash != tokenHash(intent.Token) || sourceVersion != 1 || status != "consumed" || fingerprint == "" || resultStatus != string(identityapp.LinkCandidate) || storedCandidate != first.Candidate.ID || !hasSnapshot {
		t.Fatalf("intent row hash_match=%t source_version=%d status=%s fingerprint=%q result=%s candidate=%d snapshot=%t", storedHash == tokenHash(intent.Token), sourceVersion, status, fingerprint, resultStatus, storedCandidate, hasSnapshot)
	}

	confirmed := h.confirm(t, identityapp.ConfirmMergeCommand{
		CandidateID:        first.Candidate.ID,
		SurvivorCustomerID: target.CustomerID,
		Operator:           "postgres-hardening-test",
	})
	if confirmed.Merge == nil {
		t.Fatalf("confirmed=%+v", confirmed)
	}
	var replay identityapp.LinkResult
	h.within(t, func(ctx context.Context) error {
		var err error
		replay, err = h.service.ConsumeLinkIntent(ctx, consume)
		return err
	})
	if replay.Status != identityapp.LinkIntentReplay || replay.ReplayOf != identityapp.LinkCandidate || replay.CustomerID != first.CustomerID || replay.IdentityID != first.IdentityID || replay.Candidate == nil || replay.Candidate.ID != first.Candidate.ID || replay.Candidate.Status != "open" || replay.Candidate.Evidence != first.Candidate.Evidence {
		t.Fatalf("stable replay first=%+v replay=%+v", first, replay)
	}

	drift := consume
	drift.Target = testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:intent-snapshot", "different-target")
	err := h.run(func(ctx context.Context) error {
		_, consumeErr := h.service.ConsumeLinkIntent(ctx, drift)
		return consumeErr
	})
	if !errors.Is(err, identityapp.ErrLinkIntentPayloadMismatch) {
		t.Fatalf("payload drift=%v", err)
	}
}

func TestPostgresOpenConflictReplayKeepsPersistedEvidence(t *testing.T) {
	h := newPostgresHarness(t)
	source := h.provision(t, testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:conflict-replay", "first"))
	target := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:conflict-replay", "second")
	firstEvidence := testEvidence(identitydomain.EvidenceStrong)
	firstEvidence.Digest = "first-digest"
	secondEvidence := firstEvidence
	secondEvidence.Digest = "second-digest"
	first := h.link(t, identityapp.LinkCommand{SourceCustomerID: source.CustomerID, Target: target, Evidence: firstEvidence})
	second := h.link(t, identityapp.LinkCommand{SourceCustomerID: source.CustomerID, Target: target, Evidence: secondEvidence})
	if first.Conflict == nil || second.Conflict == nil || first.Conflict.ID != second.Conflict.ID || second.Conflict.Evidence != firstEvidence {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	var evidenceRows int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT count(*) FROM identity_link_evidence`).Scan(&evidenceRows); err != nil {
		t.Fatal(err)
	}
	if evidenceRows != 1 {
		t.Fatalf("conflict replay evidence rows=%d", evidenceRows)
	}
}

func TestPostgresLinkIntentStaleSourceFailsClosed(t *testing.T) {
	h := newPostgresHarness(t)
	source := h.provision(t, testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:stale-intent", "source"))
	var intent identityapp.CreatedLinkIntent
	h.within(t, func(ctx context.Context) error {
		var err error
		intent, err = h.service.CreateLinkIntent(ctx, identityapp.LinkIntentCommand{
			SourceCustomerID: source.CustomerID,
			Purpose:          identityapp.LinkIntentBindProviderIdentity,
			TargetKind:       identitydomain.KindFirstPartyMemberID,
			ExpectedScope:    "first-party:stale-intent",
			ExpiresAt:        time.Now().Add(time.Minute),
			Source:           "postgres.hardening",
		})
		return err
	})
	attached := h.link(t, identityapp.LinkCommand{
		SourceCustomerID: source.CustomerID,
		Target:           testFact(t, identitydomain.KindExtension, "ext:stale-intent", "source-mutation"),
		Evidence:         testEvidence(identitydomain.EvidenceStrong),
	})
	if attached.Status != identityapp.LinkAttached {
		t.Fatalf("source mutation=%+v", attached)
	}
	target := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:stale-intent", "must-not-attach")
	command := identityapp.ConsumeLinkIntentCommand{Token: intent.Token, Target: target, Evidence: testEvidence(identitydomain.EvidenceStrong)}
	var invalidated identityapp.LinkResult
	h.within(t, func(ctx context.Context) error {
		var err error
		invalidated, err = h.service.ConsumeLinkIntent(ctx, command)
		return err
	})
	if invalidated.Status != identityapp.LinkIntentInvalidated {
		t.Fatalf("invalidated=%+v", invalidated)
	}
	var intentStatus string
	var targetCount int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT status FROM identity_link_intents WHERE id=$1`, intent.ID).Scan(&intentStatus); err != nil {
		t.Fatal(err)
	}
	ref := target.Reference()
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT count(*) FROM customer_identities WHERE kind=$1 AND scope_key=$2 AND normalized_value=$3`, ref.Kind, ref.Scope, ref.NormalizedValue).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if intentStatus != "cancelled" || targetCount != 0 {
		t.Fatalf("stale intent status=%s target_count=%d", intentStatus, targetCount)
	}
	h.within(t, func(ctx context.Context) error {
		replayed, err := h.service.ConsumeLinkIntent(ctx, command)
		if err == nil && replayed.Status != identityapp.LinkIntentInvalidated {
			return fmt.Errorf("cancelled replay=%+v", replayed)
		}
		return err
	})
}

func TestPostgresConcurrentSameIntentConsumptionHasOneIdentity(t *testing.T) {
	h := newPostgresHarness(t)
	source := h.provision(t, testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:concurrent-intent", "source"))
	var intent identityapp.CreatedLinkIntent
	h.within(t, func(ctx context.Context) error {
		var err error
		intent, err = h.service.CreateLinkIntent(ctx, identityapp.LinkIntentCommand{
			SourceCustomerID: source.CustomerID,
			Purpose:          identityapp.LinkIntentBindProviderIdentity,
			TargetKind:       identitydomain.KindFirstPartyMemberID,
			ExpectedScope:    "first-party:concurrent-intent",
			ExpiresAt:        time.Now().Add(time.Minute),
			Source:           "postgres.hardening",
		})
		return err
	})
	command := identityapp.ConsumeLinkIntentCommand{
		Token:    intent.Token,
		Target:   testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:concurrent-intent", "one-target"),
		Evidence: testEvidence(identitydomain.EvidenceStrong),
	}
	const workers = 12
	results := make(chan identityapp.LinkResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			err := h.run(func(ctx context.Context) error {
				result, consumeErr := h.service.ConsumeLinkIntent(ctx, command)
				if consumeErr == nil {
					results <- result
				}
				return consumeErr
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent consume: %s", safePostgresDiagnostic(err))
	}
	attached, replayed := 0, 0
	var identityID int64
	for result := range results {
		switch result.Status {
		case identityapp.LinkAttached:
			attached++
			identityID = result.IdentityID
		case identityapp.LinkIntentReplay:
			replayed++
			if result.ReplayOf != identityapp.LinkAttached {
				t.Fatalf("replay=%+v", result)
			}
			if identityID == 0 {
				identityID = result.IdentityID
			}
			if result.IdentityID != identityID {
				t.Fatalf("replay identity=%d want=%d", result.IdentityID, identityID)
			}
		default:
			t.Fatalf("consume status=%s", result.Status)
		}
	}
	if attached != 1 || replayed != workers-1 {
		t.Fatalf("attached=%d replayed=%d", attached, replayed)
	}
	var customers, identities int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM customer_identities)`).Scan(&customers, &identities); err != nil {
		t.Fatal(err)
	}
	if customers != 1 || identities != 2 {
		t.Fatalf("customers=%d identities=%d", customers, identities)
	}
}

func TestPostgresConcurrentIdentityCreationLeavesNoOrphanCustomer(t *testing.T) {
	h := newPostgresHarness(t)
	fact := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:provision-race", "one-external-id")
	const workers = 20
	results := make(chan identityport.ProvisionResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			err := h.run(func(ctx context.Context) error {
				result, provisionErr := h.service.ProvisionCustomerFromVerifiedIdentity(ctx, fact)
				if provisionErr == nil {
					results <- result
				}
				return provisionErr
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent provision: %s", safePostgresDiagnostic(err))
	}
	var customerID customerdomain.CustomerID
	created := 0
	for result := range results {
		if customerID == 0 {
			customerID = result.CustomerID
		}
		if result.CustomerID != customerID {
			t.Fatalf("customer ids=%d,%d", customerID, result.CustomerID)
		}
		if result.Created {
			created++
		}
	}
	var customers, identities, orphanCustomers int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM customer_identities),(SELECT count(*) FROM customers c WHERE NOT EXISTS (SELECT 1 FROM customer_identities i WHERE i.customer_id=c.id))`).Scan(&customers, &identities, &orphanCustomers); err != nil {
		t.Fatal(err)
	}
	if created != 1 || customers != 1 || identities != 1 || orphanCustomers != 0 {
		t.Fatalf("created=%d customers=%d identities=%d orphans=%d", created, customers, identities, orphanCustomers)
	}
}

func TestPostgresConcurrentAttachUsesOneIdentity(t *testing.T) {
	h := newPostgresHarness(t)
	source := h.provision(t, testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:attach-race", "source"))
	target := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:attach-race", "one-target")
	command := identityapp.LinkCommand{SourceCustomerID: source.CustomerID, Target: target, Evidence: testEvidence(identitydomain.EvidenceStrong)}
	const workers = 20
	results := make(chan identityapp.LinkResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			err := h.run(func(ctx context.Context) error {
				result, linkErr := h.service.LinkVerifiedIdentity(ctx, command)
				if linkErr == nil {
					results <- result
				}
				return linkErr
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent attach: %s", safePostgresDiagnostic(err))
	}
	attached, already := 0, 0
	var identityID int64
	for result := range results {
		switch result.Status {
		case identityapp.LinkAttached:
			attached++
		case identityapp.LinkAlreadyLinked:
			already++
		default:
			t.Fatalf("link=%+v", result)
		}
		if identityID == 0 {
			identityID = result.IdentityID
		}
		if result.IdentityID != identityID {
			t.Fatalf("identity ids=%d,%d", identityID, result.IdentityID)
		}
	}
	var customers, identities, version int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM customer_identities),(SELECT version FROM customers WHERE id=$1)`, source.CustomerID).Scan(&customers, &identities, &version); err != nil {
		t.Fatal(err)
	}
	if attached != 1 || already != workers-1 || customers != 1 || identities != 2 || version != 2 {
		t.Fatalf("attached=%d already=%d customers=%d identities=%d version=%d", attached, already, customers, identities, version)
	}
}

func TestPostgresOppositeLinksDeduplicateAndMergeOrReverseOnlyOnce(t *testing.T) {
	h := newPostgresHarness(t)
	leftFact := testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:opposite", "left")
	rightFact := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:opposite", "right")
	left := h.provision(t, leftFact)
	right := h.provision(t, rightFact)
	const workers = 20
	candidateIDs := make(chan int64, workers)
	errs := make(chan error, workers)
	var links sync.WaitGroup
	for worker := range workers {
		worker := worker
		links.Add(1)
		go func() {
			defer links.Done()
			command := identityapp.LinkCommand{SourceCustomerID: left.CustomerID, Target: rightFact, Evidence: testEvidence(identitydomain.EvidenceStrong)}
			if worker%2 == 1 {
				command.SourceCustomerID = right.CustomerID
				command.Target = leftFact
			}
			err := h.run(func(ctx context.Context) error {
				result, linkErr := h.service.LinkVerifiedIdentity(ctx, command)
				if linkErr == nil {
					if result.Candidate == nil {
						return errors.New("missing merge candidate")
					}
					candidateIDs <- result.Candidate.ID
				}
				return linkErr
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	links.Wait()
	close(candidateIDs)
	close(errs)
	for err := range errs {
		t.Fatalf("opposite link: %s", safePostgresDiagnostic(err))
	}
	var candidateID int64
	for id := range candidateIDs {
		if candidateID == 0 {
			candidateID = id
		}
		if id != candidateID {
			t.Fatalf("candidate ids=%d,%d", candidateID, id)
		}
	}
	var openCandidates int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT count(*) FROM customer_merge_candidates WHERE status='open'`).Scan(&openCandidates); err != nil {
		t.Fatal(err)
	}
	if candidateID == 0 || openCandidates != 1 {
		t.Fatalf("candidate=%d open=%d", candidateID, openCandidates)
	}

	type confirmOutcome struct {
		result identityapp.LinkResult
		err    error
	}
	confirmations := make(chan confirmOutcome, 2)
	start := make(chan struct{})
	for _, survivor := range []customerdomain.CustomerID{left.CustomerID, right.CustomerID} {
		survivor := survivor
		go func() {
			<-start
			var result identityapp.LinkResult
			err := h.run(func(ctx context.Context) error {
				var mergeErr error
				result, mergeErr = h.service.ConfirmMerge(ctx, identityapp.ConfirmMergeCommand{CandidateID: candidateID, SurvivorCustomerID: survivor, Operator: "postgres-hardening-race"})
				return mergeErr
			})
			confirmations <- confirmOutcome{result: result, err: err}
		}()
	}
	close(start)
	var winner identityapp.LinkResult
	confirmSuccess, confirmFailure := 0, 0
	for range 2 {
		outcome := <-confirmations
		if outcome.err == nil {
			confirmSuccess++
			winner = outcome.result
		} else if errors.Is(outcome.err, identityapp.ErrInvalidLinkCommand) || errors.Is(outcome.err, identityapp.ErrConcurrentIdentityChange) {
			confirmFailure++
		} else {
			t.Fatalf("confirmation race: %s", safePostgresDiagnostic(outcome.err))
		}
	}
	if confirmSuccess != 1 || confirmFailure != 1 || winner.Merge == nil {
		t.Fatalf("confirm success=%d failure=%d winner=%+v", confirmSuccess, confirmFailure, winner)
	}
	var selected, mergeCount int64
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT selected_survivor_customer_id,(SELECT count(*) FROM customer_merges WHERE candidate_id=$1) FROM customer_merge_candidates WHERE id=$1`, candidateID).Scan(&selected, &mergeCount); err != nil {
		t.Fatal(err)
	}
	if selected != int64(winner.CustomerID) || mergeCount != 1 {
		t.Fatalf("selected=%d winner=%d merges=%d", selected, winner.CustomerID, mergeCount)
	}

	reversals := make(chan error, 2)
	reverseStart := make(chan struct{})
	for range 2 {
		go func() {
			<-reverseStart
			reversals <- h.run(func(ctx context.Context) error {
				_, reverseErr := h.service.RevertConfirmedMerge(ctx, winner.Merge.ID)
				return reverseErr
			})
		}()
	}
	close(reverseStart)
	reverseSuccess, reverseFailure := 0, 0
	for range 2 {
		err := <-reversals
		if err == nil {
			reverseSuccess++
		} else if errors.Is(err, identityapp.ErrMergeNotReversible) {
			reverseFailure++
		} else {
			t.Fatalf("reverse race: %s", safePostgresDiagnostic(err))
		}
	}
	if reverseSuccess != 1 || reverseFailure != 1 {
		t.Fatalf("reverse success=%d failure=%d", reverseSuccess, reverseFailure)
	}
}

func TestPostgresOppositeDirectionCandidateUpgradePreservesCompositeEvidenceFK(t *testing.T) {
	h := newPostgresHarness(t)
	leftFact := testFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:upgrade", "left")
	rightFact := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:upgrade", "right")
	left := h.provision(t, leftFact)
	right := h.provision(t, rightFact)
	weak := h.link(t, identityapp.LinkCommand{SourceCustomerID: left.CustomerID, Target: rightFact, Evidence: testEvidence(identitydomain.EvidenceWeak)})
	strong := h.link(t, identityapp.LinkCommand{SourceCustomerID: right.CustomerID, Target: leftFact, Evidence: testEvidence(identitydomain.EvidenceStrong)})
	if weak.Candidate == nil || strong.Candidate == nil || weak.Candidate.ID != strong.Candidate.ID || strong.Candidate.Evidence.Strength != identitydomain.EvidenceStrong {
		t.Fatalf("weak=%+v strong=%+v", weak, strong)
	}
	var evidenceEndpointsMatch bool
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT e.left_customer_id=c.left_customer_id AND e.right_customer_id=c.right_customer_id FROM customer_merge_candidates c JOIN identity_link_evidence e ON e.id=c.evidence_id WHERE c.id=$1`, strong.Candidate.ID).Scan(&evidenceEndpointsMatch); err != nil {
		t.Fatal(err)
	}
	if !evidenceEndpointsMatch {
		t.Fatal("upgraded evidence endpoints drifted from candidate composite FK")
	}
	merged := h.confirm(t, identityapp.ConfirmMergeCommand{CandidateID: strong.Candidate.ID, SurvivorCustomerID: right.CustomerID, Operator: "postgres-hardening-upgrade"})
	if merged.Merge == nil || merged.CustomerID != right.CustomerID {
		t.Fatalf("merge=%+v", merged)
	}
}

func TestPostgresLaterConfirmationVersusReverseFailsClosed(t *testing.T) {
	h := newPostgresHarness(t)
	survivor, loser, survivorFact, firstMerge := createReversibleMerge(t, h, "confirm-reverse-race")
	thirdFact := testFact(t, identitydomain.KindFirstPartyMemberID, "first-party:confirm-reverse-race", "third")
	third := h.provision(t, thirdFact)
	laterCandidate := h.link(t, identityapp.LinkCommand{SourceCustomerID: third.CustomerID, Target: survivorFact, Evidence: testEvidence(identitydomain.EvidenceStrong)})
	if laterCandidate.Candidate == nil {
		t.Fatalf("later candidate=%+v", laterCandidate)
	}
	type raceOutcome struct {
		operation string
		result    identityapp.LinkResult
		err       error
	}
	outcomes := make(chan raceOutcome, 2)
	start := make(chan struct{})
	go func() {
		<-start
		var result identityapp.LinkResult
		err := h.run(func(ctx context.Context) error {
			var confirmErr error
			result, confirmErr = h.service.ConfirmMerge(ctx, identityapp.ConfirmMergeCommand{CandidateID: laterCandidate.Candidate.ID, SurvivorCustomerID: survivor.CustomerID, Operator: "postgres-hardening-race"})
			return confirmErr
		})
		outcomes <- raceOutcome{operation: "confirm", result: result, err: err}
	}()
	go func() {
		<-start
		err := h.run(func(ctx context.Context) error {
			_, reverseErr := h.service.RevertConfirmedMerge(ctx, firstMerge.Merge.ID)
			return reverseErr
		})
		outcomes <- raceOutcome{operation: "reverse", err: err}
	}()
	close(start)
	first, second := <-outcomes, <-outcomes
	effects := 0
	for _, outcome := range []raceOutcome{first, second} {
		if outcome.err == nil {
			if outcome.operation == "reverse" || outcome.result.Status == identityapp.LinkMerged {
				effects++
			} else if outcome.operation != "confirm" || outcome.result.Status != identityapp.LinkCandidateRejected ||
				outcome.result.Candidate == nil || outcome.result.Candidate.Status != "rejected" {
				t.Fatalf("unexpected successful %s result=%+v", outcome.operation, outcome.result)
			}
			continue
		}
		if !errors.Is(outcome.err, identityapp.ErrMergeNotReversible) && !errors.Is(outcome.err, identityapp.ErrConcurrentIdentityChange) && !errors.Is(outcome.err, identityapp.ErrInvalidLinkCommand) {
			t.Fatalf("%s race error: %s", outcome.operation, safePostgresDiagnostic(outcome.err))
		}
	}
	if effects != 1 {
		t.Fatalf("race outcomes first=%+v second=%+v", first, second)
	}
	var firstStatus string
	var laterMergeCount int
	if err := h.pool.Native().QueryRow(context.Background(), `SELECT reversible_status,(SELECT count(*) FROM customer_merges WHERE candidate_id=$2) FROM customer_merges WHERE id=$1`, firstMerge.Merge.ID, laterCandidate.Candidate.ID).Scan(&firstStatus, &laterMergeCount); err != nil {
		t.Fatal(err)
	}
	if firstStatus == "reversed" {
		if laterMergeCount != 0 {
			t.Fatalf("reverse won but later merge count=%d", laterMergeCount)
		}
		assertCustomerState(t, h, loser.CustomerID, "active", 0, firstMerge.Merge.FromVersionAfter+1, firstMerge.Merge.FromLineageAfter+1)
	} else {
		if firstStatus != "not_reversed" || laterMergeCount != 1 {
			t.Fatalf("confirmation won first_status=%s later_merges=%d", firstStatus, laterMergeCount)
		}
	}
}
