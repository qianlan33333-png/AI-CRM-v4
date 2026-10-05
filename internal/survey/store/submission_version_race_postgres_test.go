package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

// submissionVersionSwitchStore lets the real Submit flow finish loading and
// validating one published definition, then commits a real admin update and
// publish on another connection before Submit reaches CreateSubmission.
type submissionVersionSwitchStore struct {
	surveyapp.SubmissionStore
	afterRead func(surveyport.Questionnaire) error
	called    atomic.Bool
}

func (s *submissionVersionSwitchStore) GetPublishedBySlug(ctx context.Context, slug string) (surveyport.Questionnaire, error) {
	questionnaire, err := s.SubmissionStore.GetPublishedBySlug(ctx, slug)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	if s.called.CompareAndSwap(false, true) {
		if err = s.afterRead(questionnaire); err != nil {
			return surveyport.Questionnaire{}, err
		}
	}
	return questionnaire, nil
}

type versionRaceNoPhoneRecorder struct {
	customerport.CollectedPhoneRecorder
}

func TestPostgreSQLSubmissionRejectsDefinitionSwitchedAfterValidation(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var postgresVersion string
	if err := native.QueryRow(ctx, `SELECT version()`).Scan(&postgresVersion); err != nil {
		t.Fatal(err)
	}
	t.Logf("postgres_version=%s", postgresVersion)

	var actorID, v1CustomerID, staleCustomerID, v2CustomerID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-version-race','$argon2id$test','Survey Version Race') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []*int64{&v1CustomerID, &staleCustomerID, &v2CustomerID} {
		if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	var questionnaireID, v1DefinitionID, v1QuestionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,version,created_at,updated_at) VALUES('Version race','Version race v1','','survey','all_in_one','version-race','published',$1,$1,1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Version race v1','','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, bytes32(111), now, actorID).Scan(&v1DefinitionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, v1DefinitionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,required,sort_order,validation) VALUES($1,'textarea','Answer for v1',TRUE,0,'{}') RETURNING id`, v1DefinitionID).Scan(&v1QuestionID); err != nil {
		t.Fatal(err)
	}

	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
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
	definitions := surveyapp.NewService(uow, repository)

	newSubmissionService := func(store surveyapp.SubmissionStore) *surveyapp.SubmissionService {
		service := surveyapp.NewSubmissionService(uow, store, cipher)
		if err := service.BindCollectedPhone(versionRaceNoPhoneRecorder{}); err != nil {
			t.Fatal(err)
		}
		return service
	}
	identity := func(customer int64, evidence string) surveyport.SubmissionIdentity {
		id := customerdomain.CustomerID(customer)
		return surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &id, EvidenceDigest: evidence}
	}
	assertSubmissionBinding := func(receipt surveyport.SubmissionReceipt, wantDefinitionID, wantVersion, wantQuestionID int64) error {
		var definitionID, definitionVersion, actualVersion, questionID, questionVersionID int64
		var answerTitle string
		err := native.QueryRow(ctx, `SELECT submission.definition_version_id,submission.definition_version_number,definition.version_number,answer.definition_question_id,question.definition_version_id,answer.question_title_snapshot
			FROM survey_submissions submission
			JOIN survey_definition_versions definition ON definition.id=submission.definition_version_id
			JOIN survey_submission_answers answer ON answer.submission_id=submission.id
			JOIN survey_definition_questions question ON question.id=answer.definition_question_id
			WHERE submission.id=$1`, receipt.SubmissionID).Scan(&definitionID, &definitionVersion, &actualVersion, &questionID, &questionVersionID, &answerTitle)
		if err != nil {
			return err
		}
		if definitionID != wantDefinitionID || definitionVersion != wantVersion || actualVersion != wantVersion || questionID != wantQuestionID || questionVersionID != wantDefinitionID {
			return fmt.Errorf("receipt=%+v stored definition_id=%d definition_version_number=%d actual_definition_version=%d answer_question_id=%d question_definition_id=%d title=%q; want definition_id=%d version=%d question_id=%d",
				receipt, definitionID, definitionVersion, actualVersion, questionID, questionVersionID, answerTitle, wantDefinitionID, wantVersion, wantQuestionID)
		}
		return nil
	}

	// Positive control: a matching v1 questionnaire and answer are stored under
	// the same immutable version before the publication race.
	v1Reader := newSubmissionService(repository)
	v1Receipt, err := v1Reader.Submit(ctx, surveyport.SubmitCommand{
		Slug: "version-race", DefinitionVersion: 1, SubmissionKey: strings.Repeat("a", 43),
		Identity: identity(v1CustomerID, strings.Repeat("1", 64)),
		Answers:  []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(v1QuestionID), TextValue: "v1 answer"}},
	})
	if err != nil {
		t.Fatalf("matching v1 submit: %v", err)
	}
	if err := assertSubmissionBinding(v1Receipt, v1DefinitionID, 1, v1QuestionID); err != nil {
		t.Fatalf("matching v1 binding: %v", err)
	}

	var v2DefinitionID, v2QuestionID int64
	versionSwitcher := &submissionVersionSwitchStore{SubmissionStore: repository}
	versionSwitcher.afterRead = func(validated surveyport.Questionnaire) error {
		if validated.DefinitionVersion != 1 || validated.Version != 1 || validated.Questions[0].ID != surveyport.ID(v1QuestionID) {
			return fmt.Errorf("race did not capture v1 before mutation: %+v", validated)
		}
		updated := validated
		updated.Title = "Version race v2"
		updated.Questions = append([]surveyport.Question(nil), validated.Questions...)
		updated.Questions[0].Title = "Answer for v2"
		updatedV2, updateErr := definitions.Update(ctx, surveyport.UpdateCommand{Questionnaire: updated, ExpectedVersion: validated.Version, ActorID: actorID, IdempotencyKey: "version-race-update-v2-01"})
		if updateErr != nil {
			return fmt.Errorf("update to v2 after v1 validation: %w", updateErr)
		}
		publishedV2, publishErr := definitions.Publish(ctx, updatedV2.ID, updatedV2.Version, actorID, "version-race-publish-v2-01")
		if publishErr != nil {
			return fmt.Errorf("publish v2 after v1 validation: %w", publishErr)
		}
		if publishedV2.Status != surveyport.StatusPublished || publishedV2.DefinitionVersion != 2 || len(publishedV2.Questions) != 1 || publishedV2.Questions[0].ID == surveyport.ID(v1QuestionID) {
			return fmt.Errorf("unexpected published v2 definition: %+v", publishedV2)
		}
		v2QuestionID = int64(publishedV2.Questions[0].ID)
		return nil
	}

	staleService := newSubmissionService(versionSwitcher)
	staleReceipt, staleErr := staleService.Submit(ctx, surveyport.SubmitCommand{
		Slug: "version-race", DefinitionVersion: 1, SubmissionKey: strings.Repeat("b", 43),
		Identity: identity(staleCustomerID, strings.Repeat("2", 64)),
		Answers:  []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(v1QuestionID), TextValue: "stale v1 answer"}},
	})
	if err = native.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1`, questionnaireID).Scan(&v2DefinitionID); err != nil {
		t.Fatalf("read active v2 definition ID: %v", err)
	}

	// Positive control: after the switch, a request validated against v2 still
	// commits with v2's actual definition and its own question IDs.
	var v2Questionnaire surveyport.Questionnaire
	if err = uow.Within(ctx, func(txCtx context.Context) error {
		var readErr error
		v2Questionnaire, readErr = repository.GetPublishedBySlug(txCtx, "version-race")
		return readErr
	}); err != nil {
		t.Fatalf("read published v2: %v", err)
	}
	if v2Questionnaire.DefinitionVersion != 2 || int64(v2Questionnaire.Questions[0].ID) != v2QuestionID {
		t.Fatalf("published v2 read=%+v; IDs=%d/%d", v2Questionnaire, v2Questionnaire.DefinitionVersion, v2Questionnaire.Questions[0].ID)
	}
	v2Receipt, v2Err := newSubmissionService(repository).Submit(ctx, surveyport.SubmitCommand{
		Slug: "version-race", DefinitionVersion: v2Questionnaire.DefinitionVersion, SubmissionKey: strings.Repeat("c", 43),
		Identity: identity(v2CustomerID, strings.Repeat("3", 64)),
		Answers:  []surveyport.SubmissionAnswer{{QuestionID: v2Questionnaire.Questions[0].ID, TextValue: "v2 answer"}},
	})
	if v2Err != nil {
		t.Fatalf("matching v2 submit: %v", v2Err)
	}
	// A published version change must not invalidate a receipt that already
	// committed: a lost v1 response still recovers with its original key/body.
	replay, replayErr := v1Reader.Submit(ctx, surveyport.SubmitCommand{
		Slug: "version-race", DefinitionVersion: 1, SubmissionKey: strings.Repeat("a", 43),
		Identity: identity(v1CustomerID, strings.Repeat("1", 64)),
		Answers:  []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(v1QuestionID), TextValue: "v1 answer"}},
	})
	if replayErr != nil || replay.SubmissionID != v1Receipt.SubmissionID || replay.DefinitionVersion != 1 {
		t.Fatalf("committed v1 receipt after republish: receipt=%+v error=%v", replay, replayErr)
	}
	if err := assertSubmissionBinding(v2Receipt, v2DefinitionID, 2, v2QuestionID); err != nil {
		t.Fatalf("matching v2 binding: %v", err)
	}
	t.Logf("same-version controls: v1 submission_id=%d definition_id=%d question_id=%d; v2 submission_id=%d definition_id=%d question_id=%d",
		v1Receipt.SubmissionID, v1DefinitionID, v1QuestionID, v2Receipt.SubmissionID, v2DefinitionID, v2QuestionID)

	if !errors.Is(staleErr, surveyport.ErrConflict) {
		if staleErr == nil && staleReceipt.SubmissionID > 0 {
			var storedDefinitionID, storedVersionNumber, actualVersion, answerQuestionID, questionDefinitionID int64
			if readErr := native.QueryRow(ctx, `SELECT submission.definition_version_id,submission.definition_version_number,definition.version_number,answer.definition_question_id,question.definition_version_id
				FROM survey_submissions submission
				JOIN survey_definition_versions definition ON definition.id=submission.definition_version_id
				JOIN survey_submission_answers answer ON answer.submission_id=submission.id
				JOIN survey_definition_questions question ON question.id=answer.definition_question_id
				WHERE submission.id=$1`, staleReceipt.SubmissionID).Scan(&storedDefinitionID, &storedVersionNumber, &actualVersion, &answerQuestionID, &questionDefinitionID); readErr != nil {
				t.Fatalf("read wrongly accepted stale submission: %v", readErr)
			}
			t.Errorf("stale v1 submission was accepted after v2 publish on %s: receipt_definition_version=%d stored_definition_id=%d stored_version_number=%d actual_definition_version=%d answer_question_id=%d answer_question_definition_id=%d; current v2 definition_id=%d question_id=%d",
				postgresVersion, staleReceipt.DefinitionVersion, storedDefinitionID, storedVersionNumber, actualVersion, answerQuestionID, questionDefinitionID, v2DefinitionID, v2QuestionID)
		} else {
			t.Errorf("stale v1 submit after v2 republish returned %v; want definition conflict", staleErr)
		}
	}

	var staleRows, submissions, claims, tokens, answers, submissionAudits, submissionOutbox int
	for _, check := range []struct {
		name  string
		query string
		dest  *int
	}{
		{name: "stale submissions", query: `SELECT count(*) FROM survey_submissions WHERE customer_id=$1`, dest: &staleRows},
		{name: "all submissions", query: `SELECT count(*) FROM survey_submissions`, dest: &submissions},
		{name: "all claims", query: `SELECT count(*) FROM survey_submission_claims`, dest: &claims},
		{name: "all result tokens", query: `SELECT count(*) FROM survey_result_tokens`, dest: &tokens},
		{name: "all answers", query: `SELECT count(*) FROM survey_submission_answers`, dest: &answers},
		{name: "submission audits", query: `SELECT count(*) FROM survey_audit_events WHERE event_type='submission_created'`, dest: &submissionAudits},
		{name: "submission outbox", query: `SELECT count(*) FROM survey_outbox WHERE event_type='submission_created'`, dest: &submissionOutbox},
	} {
		var queryErr error
		if check.name == "stale submissions" {
			queryErr = native.QueryRow(ctx, check.query, staleCustomerID).Scan(check.dest)
		} else {
			queryErr = native.QueryRow(ctx, check.query).Scan(check.dest)
		}
		if queryErr != nil {
			t.Fatalf("count %s: %v", check.name, queryErr)
		}
	}
	t.Logf("submission state: stale_error=%v stale_rows=%d submissions=%d claims=%d result_tokens=%d answers=%d submission_audits=%d submission_outbox=%d",
		staleErr, staleRows, submissions, claims, tokens, answers, submissionAudits, submissionOutbox)
	if errors.Is(staleErr, surveyport.ErrConflict) {
		if staleRows != 0 || submissions != 2 || claims != 2 || tokens != 2 || answers != 2 || submissionAudits != 2 || submissionOutbox != 2 {
			t.Errorf("rejected stale submit wrote state: stale=%d submissions=%d claims=%d tokens=%d answers=%d submission_audits=%d submission_outbox=%d", staleRows, submissions, claims, tokens, answers, submissionAudits, submissionOutbox)
		}
	} else if staleErr == nil {
		if staleRows != 1 || submissions != 3 || claims != 3 || tokens != 3 || answers != 3 || submissionAudits != 3 || submissionOutbox != 3 {
			t.Errorf("accepted stale submit did not persist a complete transaction: stale=%d submissions=%d claims=%d tokens=%d answers=%d submission_audits=%d submission_outbox=%d", staleRows, submissions, claims, tokens, answers, submissionAudits, submissionOutbox)
		}
	}
}
