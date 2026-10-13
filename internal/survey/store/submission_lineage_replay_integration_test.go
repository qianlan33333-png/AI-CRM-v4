package store

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

func TestPostgreSQLSubmissionReceiptOwnershipAfterMergeReverse(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	identities := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	var formerAlias, survivor identityport.ProvisionResult
	// Synthetic internal adapter facts have no live Provider or credentials.
	leftFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:survey-split-test", Value: "left-synthetic-subject", Source: "local.synthetic.adapter"})
	if err != nil {
		t.Fatal(err)
	}
	rightFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindFirstPartyMemberID, Scope: "first-party:survey-split-test", Value: "right-synthetic-subject", Source: "local.synthetic.adapter"})
	if err != nil {
		t.Fatal(err)
	}
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		formerAlias, e = identities.ProvisionCustomerFromVerifiedIdentity(tx, leftFact)
		if e != nil {
			return e
		}
		survivor, e = identities.ProvisionCustomerFromVerifiedIdentity(tx, rightFact)
		if e != nil {
			return e
		}
		candidate, e := identities.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: formerAlias.CustomerID, Target: rightFact, Evidence: identitydomain.LinkEvidence{Type: "synthetic_reviewed_link", Strength: identitydomain.EvidenceStrong, Source: "local.synthetic.adapter", EventID: "survey-split", Digest: strings.Repeat("c", 64), PolicyVersion: "synthetic-v1"}})
		if e != nil {
			return e
		}
		if candidate.Candidate == nil {
			return errors.New("expected explicit merge candidate")
		}
		merged, e = identities.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivor.CustomerID, Operator: "synthetic-reviewer"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if merged.Merge == nil {
		t.Fatal("merge was not confirmed")
	}
	var actor int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-split','$argon2id$test','Synthetic split') RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	definitions := surveyapp.NewService(uow, repository)
	q, err := definitions.Create(ctx, surveyport.CreateCommand{ActorID: actor, IdempotencyKey: "survey-split-create-01", Questionnaire: surveyport.Questionnaire{Name: "Synthetic split", Title: "Synthetic split", Slug: "synthetic-split", Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{{Type: surveyport.QuestionTextarea, Title: "Response", Required: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	q, err = definitions.Publish(ctx, q.ID, q.Version, actor, "survey-split-publish-01")
	if err != nil {
		t.Fatal(err)
	}
	service := surveyapp.NewSubmissionService(uow, repository, cipher)
	if err = service.BindCollectedPhone(submissionClaimPhoneBinding{}); err != nil {
		t.Fatal(err)
	}
	if err = service.BindIdentitySubjects(identityquery.PostgreSQL{}); err != nil {
		t.Fatal(err)
	}
	if err = service.BindCanonicalLineage(identityquery.PostgreSQL{}); err != nil {
		t.Fatal(err)
	}
	command := surveyport.SubmitCommand{Slug: q.Slug, DefinitionVersion: q.DefinitionVersion, SubmissionKey: strings.Repeat("a", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &formerAlias.CustomerID, ProviderIdentityID: formerAlias.IdentityID, EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: q.Questions[0].ID, TextValue: "合成回答 😀\n下一行"}}}
	first, err := service.Submit(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.QueryResultForIdentity(ctx, first.ResultToken, command.Identity)
	if err != nil || stored.Identity.CustomerID == nil || *stored.Identity.CustomerID != survivor.CustomerID || len(stored.Answers) != 1 || stored.Answers[0].TextValue != "" || stored.Answers[0].TextValueMasked != "[protected]" {
		t.Fatalf("merged result owner=%+v err=%v", stored.Identity, err)
	}
	var ciphertext []byte
	if err = native.QueryRow(ctx, `SELECT text_value_ciphertext FROM survey_submission_answers WHERE submission_id=$1`, first.SubmissionID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if preserved, e := cipher.Decrypt(ciphertext); e != nil || preserved != command.Answers[0].TextValue {
		t.Fatalf("synthetic Unicode answer did not survive encrypted storage: err=%v", e)
	}
	if replay, e := service.Submit(ctx, command); e != nil || replay != first {
		t.Fatalf("merged replay=%+v first=%+v err=%v", replay, first, e)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, e := identities.RevertConfirmedMerge(tx, merged.Merge.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if replay, e := service.Submit(ctx, command); e != nil || replay != first {
		t.Fatalf("same active original subject lost its exact receipt after split=%+v err=%v", replay, e)
	}
	if _, e := service.QueryResultForIdentity(ctx, first.ResultToken, command.Identity); !errors.Is(e, surveyport.ErrResultForbidden) {
		t.Fatalf("split result authorization err=%v", e)
	}
	var frozenOwner, frozenHandle int64
	if err = native.QueryRow(ctx, `SELECT customer_id,provider_identity_id FROM survey_submissions WHERE id=$1`, first.SubmissionID).Scan(&frozenOwner, &frozenHandle); err != nil || frozenOwner != int64(survivor.CustomerID) || frozenHandle != formerAlias.IdentityID {
		t.Fatalf("receipt recovery rewrote historical owner or subject: owner_matches=%t handle_matches=%t err=%v", frozenOwner == int64(survivor.CustomerID), frozenHandle == formerAlias.IdentityID, err)
	}
	t.Run("legacy_snapshot_without_handle_keeps_lineage_boundary", func(t *testing.T) {
		// Fixture-only simulation of a pre-0244 row. The runtime must neither
		// infer its actor from evidence nor backfill it during receipt lookup.
		if _, e := native.Exec(ctx, `UPDATE survey_submissions SET provider_identity_id=NULL WHERE id=$1`, first.SubmissionID); e != nil {
			t.Fatal(e)
		}
		if got, e := service.Submit(ctx, command); !errors.Is(e, surveyport.ErrConflict) || got.SubmissionID != 0 || got.ResultToken != "" {
			t.Fatalf("legacy detached receipt escaped current lineage: receipt_empty=%t err=%v", got.SubmissionID == 0 && got.ResultToken == "", e)
		}
		var remainsNull bool
		if e := native.QueryRow(ctx, `SELECT provider_identity_id IS NULL FROM survey_submissions WHERE id=$1`, first.SubmissionID).Scan(&remainsNull); e != nil || !remainsNull {
			t.Fatalf("runtime guessed a legacy actor: null=%t err=%v", remainsNull, e)
		}
		if _, e := native.Exec(ctx, `UPDATE survey_submissions SET provider_identity_id=$2 WHERE id=$1`, first.SubmissionID, frozenHandle); e != nil {
			t.Fatal(e)
		}
	})
	command.SubmissionKey = strings.Repeat("d", 43)
	second, err := service.Submit(ctx, command)
	if err != nil || second.SubmissionID == first.SubmissionID {
		t.Fatalf("new split-root submission=%+v err=%v", second, err)
	}
	stored, err = service.QueryResultForIdentity(ctx, second.ResultToken, command.Identity)
	if err != nil || stored.Identity.CustomerID == nil || *stored.Identity.CustomerID != formerAlias.CustomerID {
		t.Fatalf("new split-root result owner=%+v err=%v", stored.Identity, err)
	}
	for _, table := range []string{"survey_submissions", "survey_submission_claims", "survey_result_tokens", "survey_submission_answers"} {
		var count int
		if err = native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 2 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}
