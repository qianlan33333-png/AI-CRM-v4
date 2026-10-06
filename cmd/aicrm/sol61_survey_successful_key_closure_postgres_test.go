package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
)

// Provider facts and the final retirement stimulus are synthetic. All issued
// sessions, customer roots, merge/reversal, authorization and Survey facts use
// their actual applications and PostgreSQL stores in an isolated test database.
func TestPostgreSQLSurveySuccessfulKeyAuthorizationClosure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	cipher, err := surveysecure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := surveystore.NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	queries := identityquery.NewPostgreSQL()
	submissions := surveyapp.NewSubmissionService(uow, repository, cipher)
	if err = submissions.BindCanonicalLineage(queries); err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindIdentitySubjects(queries); err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindCollectedPhone(collectedPhoneTestRecorder(t)); err != nil {
		t.Fatal(err)
	}
	actorID := surveyJourneyGovernedActor(t, ctx, native, "survey-key-closure", "Survey Key Closure")
	definitions := surveyapp.NewService(uow, repository)
	create := func(slug string) surveyport.Questionnaire {
		t.Helper()
		definition := publicSurveyPresentationDefinition(slug, surveyport.DisplayAllInOne, "Successful key closure", "")
		definition.Questions = []surveyport.Question{{Type: surveyport.QuestionTextarea, Title: "答案", Required: true, Options: []surveyport.Option{}}}
		q, e := definitions.Create(ctx, surveyport.CreateCommand{Questionnaire: definition, ActorID: actorID, IdempotencyKey: slug + "-create-closure"})
		if e != nil {
			t.Fatal(e)
		}
		q, e = definitions.Publish(ctx, q.ID, q.Version, actorID, slug+"-publish-closure")
		if e != nil {
			t.Fatal(e)
		}
		return q
	}
	beforeMerge := create("successful-before-merge-closure")
	duringMerge := create("successful-during-merge-closure")
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	loserFact := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:survey-key-closure", "synthetic-original-subject")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:survey-key-closure", "synthetic-survivor")
	var loserID, survivorID customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		loser, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, loserFact)
		if e != nil {
			return e
		}
		survivor, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, survivorFact)
		loserID, survivorID = loser.CustomerID, survivor.CustomerID
		return e
	}); err != nil {
		t.Fatal(err)
	}
	oauth := surveyapp.NewOAuthService(uow, repository, surveyDetachOAuthProvider{fact: loserFact}, oneID)
	if err = oauth.BindIdentitySubjects(queries); err != nil {
		t.Fatal(err)
	}
	issueSession := func(slug string, want customerdomain.CustomerID) (string, surveyport.SubmissionIdentity) {
		t.Helper()
		location, e := oauth.Start(ctx, slug)
		if e != nil {
			t.Fatal(e)
		}
		parsed, e := url.Parse(location)
		if e != nil {
			t.Fatal(e)
		}
		session, _, e := oauth.Complete(ctx, parsed.Query().Get("state"), "synthetic-closure-code")
		if e != nil {
			t.Fatal(e)
		}
		identity, e := oauth.ResolveSession(ctx, session)
		if e != nil || identity.State != surveyport.IdentityResolved || identity.CustomerID == nil || *identity.CustomerID != want || identity.ProviderIdentityID < 1 {
			t.Fatalf("actual OAuth subject must resolve to expected root: resolved=%t root_matches=%t handle_present=%t error=%v", identity.State == surveyport.IdentityResolved, identity.CustomerID != nil && *identity.CustomerID == want, identity.ProviderIdentityID > 0, e)
		}
		return session, identity
	}
	_, initialIdentity := issueSession(beforeMerge.Slug, loserID)
	initialCommand := surveyport.SubmitCommand{Slug: beforeMerge.Slug, DefinitionVersion: beforeMerge.DefinitionVersion, SubmissionKey: strings.Repeat("a", 43), Identity: initialIdentity, Answers: []surveyport.SubmissionAnswer{{QuestionID: beforeMerge.Questions[0].ID, TextValue: "before real merge"}}}
	initialReceipt, err := submissions.Submit(ctx, initialCommand)
	if err != nil || initialReceipt.SubmissionID < 1 || initialReceipt.ResultToken == "" {
		t.Fatalf("initial successful original command: receipt_present=%t error=%v", initialReceipt.SubmissionID > 0 && initialReceipt.ResultToken != "", err)
	}
	assertSurveyClosureOwner(t, ctx, native, beforeMerge.ID, initialReceipt.SubmissionID, loserID)
	initialFacts := snapshotSurveyClosureFacts(t, ctx, native, beforeMerge.ID, initialReceipt.SubmissionID)

	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "survey.closure.test", EventID: "synthetic-closure-merge", Digest: "synthetic-closure-evidence", PolicyVersion: "test-v1"}})
		if e != nil {
			return e
		}
		if candidate.Candidate == nil {
			t.Fatal("expected actual OneID merge candidate")
		}
		merged, e = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "synthetic-survey-closure"})
		return e
	}); err != nil || merged.Merge == nil {
		t.Fatalf("actual merge: merge_present=%t error=%v", merged.Merge != nil, err)
	}
	t.Run("original_key_replays_while_merged", func(t *testing.T) {
		replayed, e := submissions.Submit(ctx, initialCommand)
		assertSurveyClosureReceipt(t, replayed, initialReceipt, e)
		assertSurveyClosureOwner(t, ctx, native, beforeMerge.ID, initialReceipt.SubmissionID, loserID)
		assertSurveyClosureFactsUnchanged(t, ctx, native, beforeMerge.ID, initialReceipt.SubmissionID, initialFacts)
	})
	mergedSession, mergedIdentity := issueSession(duringMerge.Slug, survivorID)
	if mergedIdentity.ProviderIdentityID != initialIdentity.ProviderIdentityID {
		t.Fatal("actual merge must retain the immutable OAuth subject handle")
	}
	mergedCommand := surveyport.SubmitCommand{Slug: duringMerge.Slug, DefinitionVersion: duringMerge.DefinitionVersion, SubmissionKey: strings.Repeat("b", 43), Identity: mergedIdentity, Answers: []surveyport.SubmissionAnswer{{QuestionID: duringMerge.Questions[0].ID, TextValue: "during real merge"}}}
	mergedReceipt, err := submissions.Submit(ctx, mergedCommand)
	if err != nil || mergedReceipt.SubmissionID < 1 || mergedReceipt.ResultToken == "" {
		t.Fatalf("merged successful original command: receipt_present=%t error=%v", mergedReceipt.SubmissionID > 0 && mergedReceipt.ResultToken != "", err)
	}
	assertSurveyClosureOwner(t, ctx, native, duringMerge.ID, mergedReceipt.SubmissionID, survivorID)
	mergedFacts := snapshotSurveyClosureFacts(t, ctx, native, duringMerge.ID, mergedReceipt.SubmissionID)
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, merged.Merge.ID); return e }); err != nil {
		t.Fatalf("actual OneID reversal: %v", err)
	}
	detachedIdentity, err := oauth.ResolveSession(ctx, mergedSession)
	if err != nil || detachedIdentity.State != surveyport.IdentityResolved || detachedIdentity.CustomerID == nil || *detachedIdentity.CustomerID != loserID || detachedIdentity.ProviderIdentityID != mergedIdentity.ProviderIdentityID {
		t.Fatalf("same active handle must resolve back to detached A: subject_matches=%t error=%v", detachedIdentity.CustomerID != nil && *detachedIdentity.CustomerID == loserID && detachedIdentity.ProviderIdentityID == mergedIdentity.ProviderIdentityID, err)
	}
	t.Run("original_keys_replay_after_detach_without_rewriting_history", func(t *testing.T) {
		replayed, e := submissions.Submit(ctx, initialCommand)
		assertSurveyClosureReceipt(t, replayed, initialReceipt, e)
		replayed, e = submissions.Submit(ctx, mergedCommand)
		assertSurveyClosureReceipt(t, replayed, mergedReceipt, e)
		freshCommand := mergedCommand
		freshCommand.Identity = detachedIdentity
		replayed, e = submissions.Submit(ctx, freshCommand)
		assertSurveyClosureReceipt(t, replayed, mergedReceipt, e)
		assertSurveyClosureOwner(t, ctx, native, beforeMerge.ID, initialReceipt.SubmissionID, loserID)
		assertSurveyClosureOwner(t, ctx, native, duringMerge.ID, mergedReceipt.SubmissionID, survivorID)
		assertSurveyClosureFactsUnchanged(t, ctx, native, beforeMerge.ID, initialReceipt.SubmissionID, initialFacts)
		assertSurveyClosureFactsUnchanged(t, ctx, native, duringMerge.ID, mergedReceipt.SubmissionID, mergedFacts)
	})
	handler, err := surveyhttp.NewHandler(definitions, submissions, surveyJourneySecurity{actorID: actorID}, oauth)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("historical_owner_does_not_expand_detached_result_authorization", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"result_token": mergedReceipt.ResultToken})
		response := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", body, "", []*http.Cookie{{Name: "__Host-aicrm_survey_identity", Value: mergedSession}}, "")
		if response.Code != http.StatusForbidden {
			t.Fatalf("detached A must not borrow historical B result scope: status=%d", response.Code)
		}
		assertSurveyClosureFactsUnchanged(t, ctx, native, duringMerge.ID, mergedReceipt.SubmissionID, mergedFacts)
	})

	// Synthetic lifecycle stimulus in the Identity-owned fixture, with the
	// exact existing handle and actual owner root lock. No public request can
	// select or retire a handle, and no real Provider is contacted.
	if err = uow.Within(ctx, func(tx context.Context) error {
		if e := oneID.Store.(identityport.CustomerIdentityLocker).LockActiveCustomerWithin(tx, loserID); e != nil {
			return e
		}
		db, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		result, e := db.Exec(tx, `UPDATE customer_identities SET status='retired',version=version+1 WHERE id=$1 AND customer_id=$2 AND status='active'`, detachedIdentity.ProviderIdentityID, loserID)
		if e != nil {
			return e
		}
		if result.RowsAffected() != 1 {
			t.Fatalf("synthetic retirement must affect exactly the previously verified subject: rows=%d", result.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	retired, err := oauth.ResolveSession(ctx, mergedSession)
	if err != nil || retired.State != surveyport.IdentityAnonymous {
		t.Fatalf("retired session must require fresh OAuth: anonymous=%t error=%v", retired.State == surveyport.IdentityAnonymous, err)
	}
	for _, scenario := range []struct {
		name    string
		command surveyport.SubmitCommand
		q       surveyport.Questionnaire
		receipt surveyport.SubmissionReceipt
		facts   map[string][32]byte
	}{
		{name: "successful_before_merge", command: initialCommand, q: beforeMerge, receipt: initialReceipt, facts: initialFacts},
		{name: "successful_during_merge", command: mergedCommand, q: duringMerge, receipt: mergedReceipt, facts: mergedFacts},
	} {
		t.Run("retired_handle_rejects_original_key/"+scenario.name, func(t *testing.T) {
			// Keep the entire successful command unchanged, including the key,
			// answers, evidence and cached identity. This must not hit its receipt
			// until current authorization succeeds in the same Submit UoW.
			replayed, e := submissions.Submit(ctx, scenario.command)
			if !errors.Is(e, surveyport.ErrReauthorizationRequired) || !reflect.DeepEqual(replayed, surveyport.SubmissionReceipt{}) {
				t.Fatalf("retired original successful key exposed a receipt: receipt_empty=%t error=%v", reflect.DeepEqual(replayed, surveyport.SubmissionReceipt{}), e)
			}
			assertSurveyClosureFactsUnchanged(t, ctx, native, scenario.q.ID, scenario.receipt.SubmissionID, scenario.facts)
		})
	}
	t.Log("closure facts: two successful original keys; active handle replays through actual merge/reversal; historical owners A/B retained; retired cached handle rejected before both receipts; six facts per questionnaire unchanged")
}

func assertSurveyClosureReceipt(t *testing.T, got, want surveyport.SubmissionReceipt, err error) {
	t.Helper()
	if err != nil || want.SubmissionID < 1 || want.ResultToken == "" || !reflect.DeepEqual(got, want) {
		t.Fatalf("original successful key must recover its complete original receipt: exact_receipt=%t error=%v", reflect.DeepEqual(got, want), err)
	}
}

func assertSurveyClosureOwner(t *testing.T, ctx context.Context, native *pgxpool.Pool, qid, sid surveyport.ID, want customerdomain.CustomerID) {
	t.Helper()
	var owner, claimedOwner, claimedSubmission int64
	if err := native.QueryRow(ctx, `SELECT customer_id FROM survey_submissions WHERE id=$1`, sid).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `SELECT customer_id,submission_id FROM survey_submission_claims WHERE questionnaire_id=$1`, qid).Scan(&claimedOwner, &claimedSubmission); err != nil {
		t.Fatal(err)
	}
	if owner != int64(want) || claimedOwner != int64(want) || claimedSubmission != int64(sid) {
		t.Fatalf("immutable submission/claim owner changed: owner_matches=%t claim_owner_matches=%t claim_receipt_matches=%t", owner == int64(want), claimedOwner == int64(want), claimedSubmission == int64(sid))
	}
}

func snapshotSurveyClosureFacts(t *testing.T, ctx context.Context, native *pgxpool.Pool, qid, sid surveyport.ID) map[string][32]byte {
	t.Helper()
	facts := make(map[string][32]byte)
	for _, check := range []struct {
		table, where string
		id           surveyport.ID
	}{
		{"survey_submissions", "questionnaire_id=$1", qid},
		{"survey_submission_claims", "questionnaire_id=$1", qid},
		{"survey_result_tokens", "submission_id=$1", sid},
		{"survey_submission_answers", "submission_id=$1", sid},
		{"survey_audit_events", "aggregate_id=$1 AND event_type='submission_created'", qid},
		{"survey_outbox", "aggregate_id=$1 AND event_type='submission_created'", qid},
	} {
		var count int
		var snapshot string
		// Identifiers and predicates are a fixed Survey-owned test allowlist.
		// Hash complete rows, including encrypted answers, payload/key digests,
		// immutable owner and timestamps. Never print tokens or row contents.
		query := "SELECT count(*),COALESCE(jsonb_agg(to_jsonb(fact) ORDER BY to_jsonb(fact)::text),'[]'::jsonb)::text FROM " + check.table + " fact WHERE " + check.where
		if err := native.QueryRow(ctx, query, check.id).Scan(&count, &snapshot); err != nil || count != 1 {
			t.Fatalf("expected exactly one retained %s fact: rows=%d error=%v", check.table, count, err)
		}
		facts[check.table] = sha256.Sum256([]byte(snapshot))
	}
	return facts
}

func assertSurveyClosureFactsUnchanged(t *testing.T, ctx context.Context, native *pgxpool.Pool, qid, sid surveyport.ID, want map[string][32]byte) {
	t.Helper()
	got := snapshotSurveyClosureFacts(t, ctx, native, qid, sid)
	for table, expected := range want {
		if got[table] != expected {
			t.Fatalf("successful-key replay changed retained %s fact", table)
		}
	}
}
