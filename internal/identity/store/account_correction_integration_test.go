package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"

	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestDistinctWeComAccountCorrectionIntegration(t *testing.T) {
	pool, cleanup := identityPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Native().Exec(ctx, `CREATE TABLE survey_submissions(id BIGINT PRIMARY KEY); CREATE TABLE survey_submission_answers(id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_platform.sql", "0038_survey_oauth_phone_vault.sql", "0063_identity_hxc_source_observations.sql"} {
		raw, err := os.ReadFile(identityMigrationNamed(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Native().Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	phone, _ := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32)))
	observation, _ := identitysecure.NewObservationVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32)))
	repository := NewPostgresStoreWithObservation(phone, observation)
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	one := identityapp.OneIDService{Store: repository}
	source := identityapp.HXCSourceService{Inspector: identityquery.NewPostgreSQL(phone), Store: repository, OneID: one, VerifiedIdentity: identityadapter.HXCVerifiedUnionIDFactory{Enabled: true}}
	within := func(f func(context.Context) error) {
		t.Helper()
		if err := uow.Within(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	leftExternal := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:hxc", "external-left")
	rightExternal := testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:hxc", "external-right")
	var left, right identityport.ProvisionResult
	within(func(tx context.Context) error {
		var err error
		left, err = one.ProvisionCustomerFromVerifiedIdentity(tx, leftExternal)
		if err != nil {
			return err
		}
		right, err = one.ProvisionCustomerFromVerifiedIdentity(tx, rightExternal)
		return err
	})
	within(func(tx context.Context) error {
		_, err := one.AttachDeclaredPhoneToCustomer(tx, identityport.DeclaredPhoneCommand{CustomerID: right.CustomerID, Phone: "13800138000", Source: "phone_import", SourceEventID: "phone-account-right", IdempotencyKey: "phone-account-right"})
		return err
	})
	subject := hxcIntegrationSubject("reviewed-subject", "first-payload", "union-left", "13800138000")
	within(func(tx context.Context) error { _, err := source.ApplyHXCSubject(tx, subject); return err })
	subject.PayloadDigest = sha256.Sum256([]byte("current-payload"))
	within(func(tx context.Context) error {
		result, err := source.ApplyHXCSubject(tx, subject)
		if err == nil && result.MatchedBy != identityport.HXCMatchBoth {
			return fmt.Errorf("fixture must reproduce incorrect both-key match: %+v", result)
		}
		return err
	})
	leftUnion := testFact(t, identitydomain.KindUnionID, subject.UnionIDScope, "union-left")
	rightUnion := testFact(t, identitydomain.KindUnionID, subject.UnionIDScope, "union-right")
	evidence := testEvidence(identitydomain.EvidenceStrong)
	evidence.Type = "verified_wecom_unionid_contact_pair"
	evidence.Source = "wecom.directory_sync"
	var candidate, conflict identityapp.LinkResult
	within(func(tx context.Context) error {
		var err error
		candidate, err = one.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: left.CustomerID, Target: leftUnion, Evidence: evidence})
		if err != nil {
			return err
		}
		conflict, err = one.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: right.CustomerID, Target: rightUnion, Evidence: evidence})
		return err
	})
	if candidate.Candidate == nil || conflict.Conflict == nil {
		t.Fatal("fixture did not create both conflicts")
	}
	plan := identityport.AccountCorrectionPlan{RunKey: "reviewed-account-correction", Operator: "user-approved-maintenance", LeftCustomerID: int64(left.CustomerID), RightCustomerID: int64(right.CustomerID), CandidateID: candidate.Candidate.ID, ConflictIDs: []int64{conflict.Conflict.ID}}
	if err := pool.Native().QueryRow(ctx, `SELECT (SELECT version FROM customers WHERE id=$1),(SELECT version FROM customers WHERE id=$2),(SELECT id FROM customer_identities WHERE customer_id=$2 AND kind='unionid' AND status='active'),(SELECT version FROM customer_identities WHERE customer_id=$2 AND kind='unionid' AND status='active'),(SELECT version FROM customer_merge_candidates WHERE id=$3),(SELECT id FROM identity_source_subjects),(SELECT version FROM identity_source_subjects)`, left.CustomerID, right.CustomerID, plan.CandidateID).Scan(&plan.LeftVersion, &plan.RightVersion, &plan.WrongIdentityID, &plan.WrongIdentityVersion, &plan.CandidateVersion, &plan.HXCSubjectID, &plan.HXCSubjectVersion); err != nil {
		t.Fatal(err)
	}
	cmd := identityport.AccountCorrectionCommand{Plan: plan, LeftExternal: leftExternal, RightExternal: rightExternal, LeftUnion: leftUnion, RightUnion: rightUnion}
	snapshot := func() string {
		t.Helper()
		var state string
		if err := pool.Native().QueryRow(ctx, `SELECT json_build_object('customers',(SELECT json_agg(row_to_json(c)) FROM customers c),'identities',(SELECT json_agg(row_to_json(i)) FROM customer_identities i),'candidate',(SELECT json_agg(row_to_json(c)) FROM customer_merge_candidates c),'conflicts',(SELECT json_agg(row_to_json(c)) FROM customer_identity_conflicts c),'sources',(SELECT json_agg(row_to_json(s)) FROM identity_source_subjects s),'receipts',(SELECT json_agg(row_to_json(r)) FROM identity_source_resolution_receipts r),'evidence',(SELECT count(*) FROM identity_link_evidence),'audit',(SELECT count(*) FROM audit_events))::text`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	original := snapshot()
	t.Run("refuses stale and incorrect proof without changes", func(t *testing.T) {
		for _, bad := range []identityport.AccountCorrectionCommand{
			func() identityport.AccountCorrectionCommand { v := cmd; v.Plan.LeftVersion++; return v }(),
			func() identityport.AccountCorrectionCommand { v := cmd; v.Plan.WrongIdentityVersion++; return v }(),
			func() identityport.AccountCorrectionCommand { v := cmd; v.Plan.HXCSubjectVersion++; return v }(),
			func() identityport.AccountCorrectionCommand { v := cmd; v.LeftExternal = rightExternal; return v }(),
			func() identityport.AccountCorrectionCommand {
				v := cmd
				v.LeftUnion = rightUnion
				v.RightUnion = leftUnion
				return v
			}(),
			func() identityport.AccountCorrectionCommand { v := cmd; v.RightUnion = leftUnion; return v }(),
		} {
			err := uow.Within(ctx, func(tx context.Context) error { _, err := repository.CorrectDistinctWeComAccounts(tx, bad); return err })
			if err == nil {
				t.Fatal("invalid correction accepted")
			}
			if snapshot() != original {
				t.Fatal("rejected correction changed data")
			}
		}
	})
	t.Run("audit failure and dry-run roll back the whole correction", func(t *testing.T) {
		failure := errors.New("audit unavailable")
		err := uow.Within(ctx, func(tx context.Context) error {
			if _, err := repository.CorrectDistinctWeComAccounts(tx, cmd); err != nil {
				return err
			}
			return failure
		})
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if snapshot() != original {
			t.Fatal("correction survived audit rollback")
		}
	})
	var applied identityport.AccountCorrectionResult
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	apply := func(tx context.Context) error {
		var err error
		applied, err = repository.CorrectDistinctWeComAccounts(tx, cmd)
		if err != nil || applied.Replayed {
			return err
		}
		payload, _ := json.Marshal(applied)
		_, err = audit.Append(tx, platformaudit.Event{IdempotencyKey: idempotency.Key("identity:account-correction:" + strconv.FormatInt(applied.EvidenceID, 10)), Action: "identity.wecom_accounts_corrected", ActorType: "maintenance", ResourceType: "identity_account_correction", ResourceID: strconv.FormatInt(applied.EvidenceID, 10), Payload: payload})
		return err
	}
	within(apply)
	if applied.Replayed || applied.LeftUnionID == applied.RightUnionID || applied.RetiredIdentityID != plan.WrongIdentityID {
		t.Fatalf("wrong correction result: %+v", applied)
	}
	var correct bool
	err := pool.Native().QueryRow(ctx, `SELECT
 (SELECT count(*)=2 AND bool_and(status='active' AND merged_into_customer_id IS NULL AND lineage_version=1) FROM customers)
 AND (SELECT status='retired' AND customer_id=$2 FROM customer_identities WHERE id=$3)
 AND (SELECT count(*)=2 FROM customer_identities WHERE status='active' AND kind='unionid')
 AND (SELECT customer_id=$1 AND normalized_value='union-left' FROM customer_identities WHERE id=$4)
 AND (SELECT customer_id=$2 AND normalized_value='union-right' FROM customer_identities WHERE id=$5)
 AND (SELECT status='rejected' AND resolved_by=$6 FROM customer_merge_candidates WHERE id=$7)
 AND (SELECT status='resolved' FROM customer_identity_conflicts WHERE id=$8)
 AND (SELECT customer_id=$1 AND matched_by='unionid' FROM identity_source_subjects WHERE id=$9)
 AND (SELECT count(*)=1 FROM audit_events)
 AND (SELECT count(*)=0 FROM customer_merges)
 AND (SELECT count(*)=2 FROM identity_source_resolution_receipts WHERE customer_id=$2)
 AND (SELECT count(*)=1 FROM identity_source_resolution_receipts WHERE customer_id=$1)`, plan.LeftCustomerID, plan.RightCustomerID, plan.WrongIdentityID, applied.LeftUnionID, applied.RightUnionID, plan.Operator, plan.CandidateID, plan.ConflictIDs[0], plan.HXCSubjectID).Scan(&correct)
	if err != nil || !correct {
		t.Fatalf("correction readback incorrect: %v", safePostgresDiagnostic(err))
	}
	t.Run("retry and payload drift cannot duplicate or change effects", func(t *testing.T) {
		before := snapshot()
		within(apply)
		within(apply)
		if !applied.Replayed || snapshot() != before {
			t.Fatal("replay changed identities or audit")
		}
		changed := cmd
		changed.Plan.Operator = "different-review"
		err := uow.Within(ctx, func(tx context.Context) error {
			_, err := repository.CorrectDistinctWeComAccounts(tx, changed)
			return err
		})
		if !errors.Is(err, identityapp.ErrDeclaredPayloadMismatch) || snapshot() != before {
			t.Fatal("payload drift accepted")
		}
	})
	t.Run("HXC replay and changed payload retain accounts and reviewed union attribution", func(t *testing.T) {
		for _, payload := range []string{"current-payload", "first-payload", "changed-payload", "changed-payload"} {
			subject.PayloadDigest = sha256.Sum256([]byte(payload))
			within(func(tx context.Context) error {
				preview, err := source.InspectHXCSubjects(tx, []identityport.HXCSubject{subject})
				if err != nil {
					return err
				}
				if len(preview) != 1 || preview[0].CustomerID != left.CustomerID || preview[0].MatchedBy != identityport.HXCMatchUnionID {
					return fmt.Errorf("incorrect reviewed preview: %+v", preview)
				}
				result, err := source.ApplyHXCSubject(tx, subject)
				if err == nil && (result.CustomerID != left.CustomerID || result.MatchedBy != identityport.HXCMatchUnionID || result.MergeCandidateID != 0) {
					return fmt.Errorf("incorrect reviewed HXC: %+v", result)
				}
				return err
			})
		}
		subject.ConflictReason = identityport.HXCReasonDuplicatePhone
		within(func(tx context.Context) error {
			_, err := source.ApplyHXCSubject(tx, subject)
			if err != nil {
				return err
			}
			replay, err := source.ApplyHXCSubject(tx, subject)
			if err == nil && (!replay.Replayed || replay.CustomerID != left.CustomerID) {
				return fmt.Errorf("reviewed shared-phone account must replay: %+v", replay)
			}
			if err != nil {
				return err
			}
			return nil
		})
		beforeRetry := snapshot()
		var beforeObservations string
		if err := pool.Native().QueryRow(ctx, `SELECT COALESCE(string_agg(id::text||':'||version::text||':'||last_seen_at::text,',' ORDER BY id),'') FROM identity_source_observations`).Scan(&beforeObservations); err != nil {
			t.Fatal(err)
		}
		within(func(tx context.Context) error {
			replay, err := source.ApplyHXCSubject(tx, subject)
			if err == nil && !replay.Replayed {
				return errors.New("reviewed shared-phone retry was not replayed")
			}
			return err
		})
		var afterObservations string
		if err := pool.Native().QueryRow(ctx, `SELECT COALESCE(string_agg(id::text||':'||version::text||':'||last_seen_at::text,',' ORDER BY id),'') FROM identity_source_observations`).Scan(&afterObservations); err != nil {
			t.Fatal(err)
		}
		if snapshot() != beforeRetry || beforeObservations != afterObservations {
			t.Fatal("reviewed replay changed versions or appended receipts")
		}
		strong := subject
		strong.ConflictReason = identityport.HXCReasonDuplicateUnionID
		within(func(tx context.Context) error {
			_, found, err := repository.ReviewedHXCAccount(tx, strong)
			if found {
				return errors.New("review bypassed a strong UnionID conflict")
			}
			if err != nil {
				return err
			}
			results, err := source.InspectHXCSubjects(tx, []identityport.HXCSubject{strong})
			if err == nil && (len(results) != 1 || results[0].Disposition != identityport.HXCConflict || results[0].Reason != identityport.HXCReasonDuplicateUnionID) {
				return errors.New("strong conflict was cleared")
			}
			return err
		})
		var newCandidates, phones, merges int
		if err := pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM customer_merge_candidates WHERE status='open'),(SELECT count(*) FROM customer_identities WHERE kind='phone' AND customer_id=$1 AND status='active'),(SELECT count(*) FROM customer_merges)`, right.CustomerID).Scan(&newCandidates, &phones, &merges); err != nil || newCandidates != 0 || phones != 1 || merges != 0 {
			t.Fatal("HXC recreated cross-account effects")
		}
		unreviewed := subject
		unreviewed.SubjectDigest = sha256.Sum256([]byte("another-subject"))
		within(func(tx context.Context) error {
			_, found, err := repository.ReviewedHXCAccount(tx, unreviewed)
			if found {
				return errors.New("review spread to an unreviewed subject")
			}
			return err
		})
	})
}
