package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

// Only the verified Provider fact is synthetic. Sessions, merge/reversal and
// submissions use the production PostgreSQL stores and application services.
type surveyDetachOAuthProvider struct{ fact identitydomain.VerifiedFact }

func (surveyDetachOAuthProvider) Enabled() bool { return true }
func (surveyDetachOAuthProvider) AuthorizationURL(state string) string {
	return "https://synthetic.example/authorize?state=" + url.QueryEscape(state)
}
func (p surveyDetachOAuthProvider) Exchange(context.Context, string) (identitydomain.VerifiedFact, error) {
	return p.fact, nil
}

func TestPostgreSQLSurveySessionIssuedDuringMergeAfterDetach(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	submissions := surveyapp.NewSubmissionService(uow, repository, cipher)
	if err = submissions.BindCanonicalLineage(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindIdentitySubjects(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindCollectedPhone(collectedPhoneTestRecorder(t)); err != nil {
		t.Fatal(err)
	}
	actorID := surveyJourneyGovernedActor(t, ctx, native, "survey-detach-session", "Survey Detach Session")
	definitions := surveyapp.NewService(uow, repository)
	definition := publicSurveyPresentationDefinition("detach-session", surveyport.DisplayAllInOne, "Detach session", "")
	definition.Questions = []surveyport.Question{{Type: surveyport.QuestionTextarea, Title: "答案", Required: true, Options: []surveyport.Option{}}}
	questionnaire, err := definitions.Create(ctx, surveyport.CreateCommand{Questionnaire: definition, ActorID: actorID, IdempotencyKey: "detach-session-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	questionnaire, err = definitions.Publish(ctx, questionnaire.ID, questionnaire.Version, actorID, "detach-session-publish-0001")
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	loserFact := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:survey-detach", "synthetic-detached-subject")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:survey-detach", "synthetic-survivor")
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
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "survey.detach.test", EventID: "synthetic-detach", Digest: "synthetic-detach-evidence", PolicyVersion: "test-v1"}})
		if e != nil {
			return e
		}
		if candidate.Candidate == nil {
			t.Fatal("expected merge candidate")
		}
		merged, e = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "synthetic-detach"})
		return e
	}); err != nil || merged.Merge == nil {
		t.Fatalf("merge: result=%+v error=%v", merged, err)
	}
	oauth := surveyapp.NewOAuthService(uow, repository, surveyDetachOAuthProvider{fact: loserFact}, oneID)
	if err = oauth.BindIdentitySubjects(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	location, err := oauth.Start(ctx, questionnaire.Slug)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := oauth.Complete(ctx, parsed.Query().Get("state"), "synthetic-code")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := oauth.ResolveSession(ctx, session)
	if err != nil || identity.CustomerID == nil || *identity.CustomerID != survivorID {
		t.Fatalf("session must begin at merged root: identity=%+v error=%v", identity, err)
	}
	// Capture the real private identity before detach. The subsequent command
	// must revalidate it even if the HTTP session read has already completed.
	beforeDetachIdentity := identity
	handler, err := surveyhttp.NewHandler(definitions, submissions, surveyJourneySecurity{actorID: actorID}, oauth)
	if err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: "__Host-aicrm_survey_identity", Value: session}}
	before := surveyJourneyServe(t, handler, http.MethodGet, "/api/public/questionnaires/"+questionnaire.Slug, nil, "", cookies, "")
	beforeContext := before.Header().Get("X-Aicrm-Survey-Authorization-Context")
	if before.Code != http.StatusOK || beforeContext == "" {
		t.Fatalf("merged form context: status=%d context=%q", before.Code, beforeContext)
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, merged.Merge.ID); return e }); err != nil {
		t.Fatalf("detach: %v", err)
	}
	identity, err = oauth.ResolveSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	// The newly issued session has the exact immutable verified handle. It must
	// follow that still-active subject back to A, with every downstream check
	// executed. Legacy or retired sessions are separate rejection cases below.
	if identity.State != surveyport.IdentityResolved || identity.CustomerID == nil || *identity.CustomerID != loserID {
		t.Fatalf("identity misattribution after detach: resolved session=%+v expected customer=%d", identity, loserID)
	}
	after := surveyJourneyServe(t, handler, http.MethodGet, "/api/public/questionnaires/"+questionnaire.Slug, nil, "", cookies, "")
	if after.Code != http.StatusOK || after.Header().Get("X-Aicrm-Survey-Authorization-Context") == "" || after.Header().Get("X-Aicrm-Survey-Authorization-Context") == beforeContext {
		t.Fatalf("detached subject must replace the draft authorization context: status=%d old=%q new=%q", after.Code, beforeContext, after.Header().Get("X-Aicrm-Survey-Authorization-Context"))
	}
	command := surveyport.SubmitCommand{Slug: questionnaire.Slug, DefinitionVersion: questionnaire.DefinitionVersion, SubmissionKey: strings.Repeat("d", 43), Identity: beforeDetachIdentity, Answers: []surveyport.SubmissionAnswer{{QuestionID: questionnaire.Questions[0].ID, TextValue: "after detach"}}}
	receipt, err := submissions.Submit(ctx, command)
	if err != nil {
		t.Fatalf("submit after detach: %v", err)
	}
	var owner int64
	if err = native.QueryRow(ctx, `SELECT customer_id FROM survey_submissions WHERE id=$1`, receipt.SubmissionID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != int64(loserID) {
		t.Errorf("detached subject submission attributed to another customer: stored customer=%d expected=%d", owner, loserID)
	}
	command.Identity = identity
	replay, err := submissions.Submit(ctx, command)
	if err != nil || replay.SubmissionID != receipt.SubmissionID || replay.ResultToken != receipt.ResultToken {
		t.Fatalf("current subject must replay the cached command receipt: receipt=%+v error=%v", replay, err)
	}
	// Merge the actual roots again, then prove the original result token works
	// for the currently canonical subject without rewriting its stored owner.
	if err = uow.Within(ctx, func(tx context.Context) error {
		candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "survey.detach.test", EventID: "synthetic-merge-result", Digest: "synthetic-result-evidence", PolicyVersion: "test-v1"}})
		if e != nil {
			return e
		}
		if candidate.Candidate == nil {
			t.Fatal("expected result merge candidate")
		}
		merged, e = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "synthetic-result"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	resultBody, _ := json.Marshal(map[string]string{"result_token": receipt.ResultToken})
	result := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", resultBody, "", cookies, "")
	if result.Code != http.StatusOK || strings.Contains(result.Body.String(), "provider_identity_id") || strings.Contains(result.Body.String(), "ProviderIdentityID") {
		t.Fatalf("merged subject result must be readable without a handle leak: status=%d body=%s", result.Code, result.Body.String())
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, merged.Merge.ID); return e }); err != nil {
		t.Fatal(err)
	}
	// A distinct verified subject receives a real OAuth session; possession of
	// the other customer's result token never expands its current lineage.
	otherOAuth := surveyapp.NewOAuthService(uow, repository, surveyDetachOAuthProvider{fact: evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:survey-detach", "synthetic-other-subject")}, oneID)
	if err = otherOAuth.BindIdentitySubjects(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	location, err = otherOAuth.Start(ctx, questionnaire.Slug)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = url.Parse(location)
	otherSession, _, err := otherOAuth.Complete(ctx, parsed.Query().Get("state"), "synthetic-other-code")
	if err != nil {
		t.Fatal(err)
	}
	denied := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", resultBody, "", []*http.Cookie{{Name: "__Host-aicrm_survey_identity", Value: otherSession}}, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unrelated subject borrowed result token: status=%d body=%s", denied.Code, denied.Body.String())
	}
	// Simulate a retained pre-migration session by removing only its handle.
	legacyToken := strings.Repeat("l", 43)
	legacyDigest := sha256.Sum256([]byte(legacyToken))
	legacyIdentity := identity
	legacyIdentity.ProviderIdentityID = 0
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.CreateIdentitySession(tx, legacyDigest, legacyIdentity, time.Now().Add(time.Hour), time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := oauth.ResolveSession(ctx, legacyToken)
	if err != nil || legacy.State != surveyport.IdentityAnonymous {
		t.Fatalf("legacy unbound session must require OAuth: identity=%+v error=%v", legacy, err)
	}
	legacyHTTP := surveyJourneyServe(t, handler, http.MethodGet, "/api/public/questionnaires/"+questionnaire.Slug, nil, "", []*http.Cookie{{Name: "__Host-aicrm_survey_identity", Value: legacyToken}}, "")
	if legacyHTTP.Code != http.StatusUnauthorized || !strings.Contains(legacyHTTP.Body.String(), "survey_oauth_required") {
		t.Fatalf("legacy session recovery feedback: status=%d body=%s", legacyHTTP.Code, legacyHTTP.Body.String())
	}
	// Identity-owned lifecycle fixture only: retire the verified handle while
	// retaining the issued session. No provider, customer data or secret exists.
	if err = uow.Within(ctx, func(tx context.Context) error {
		if e := oneID.Store.(identityport.CustomerIdentityLocker).LockActiveCustomerWithin(tx, loserID); e != nil {
			return e
		}
		db, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		_, e = db.Exec(tx, `UPDATE customer_identities SET status='retired',version=version+1 WHERE id=$1`, identity.ProviderIdentityID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	retired, err := oauth.ResolveSession(ctx, session)
	if err != nil || retired.State != surveyport.IdentityAnonymous {
		t.Fatalf("retired subject must require OAuth: identity=%+v error=%v", retired, err)
	}
	command.SubmissionKey = strings.Repeat("r", 43)
	if _, err = submissions.Submit(ctx, command); !errors.Is(err, surveyport.ErrReauthorizationRequired) {
		t.Fatalf("cached retired subject must be rejected inside Submit UoW: %v", err)
	}
	for _, check := range []struct {
		table, where string
		id           surveyport.ID
	}{
		{"survey_submissions", "questionnaire_id=$1", questionnaire.ID},
		{"survey_submission_claims", "questionnaire_id=$1", questionnaire.ID},
		{"survey_result_tokens", "submission_id=$1", receipt.SubmissionID},
		{"survey_submission_answers", "submission_id=$1", receipt.SubmissionID},
		{"survey_audit_events", "aggregate_id=$1 AND event_type='submission_created'", questionnaire.ID},
		{"survey_outbox", "aggregate_id=$1 AND event_type='submission_created'", questionnaire.ID},
	} {
		var count int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+check.table+" WHERE "+check.where, check.id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("retired subject changed %s: rows=%d error=%v", check.table, count, err)
		}
	}
}
