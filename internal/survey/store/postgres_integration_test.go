package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

func TestPostgreSQLCompletionReceiptBindsReadsAndRollsBackAtomically(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)

	var actorID, customerID, questionnaireID, versionID, submissionID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-completion-test','$argon2id$test','Survey Completion Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Completion questionnaire','Completion questionnaire','','survey','all_in_one','completion-questionnaire','published',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Completion questionnaire','', '{}', $2, TRUE, $3, $4, $3) RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,'resolved',$4,$5,'completion-questionnaire','Completion questionnaire','survey','{}',$6,$6) RETURNING id`, questionnaireID, versionID, customerID, make([]byte, 32), bytes32(1), now).Scan(&submissionID); err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("需要回访")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO survey_submission_answers(submission_id,question_type,question_title_snapshot,text_value_ciphertext,answer_digest,created_at) VALUES($1,'textarea','需求',$2,$3,$4)`, submissionID, encrypted, bytes32(2), now); err != nil {
		t.Fatal(err)
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := "sha256:" + strings.Repeat("0", 64)
	identityCiphertext, err := cipher.Encrypt("union-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(sourceDigest))
	rollback := errors.New("force rollback")
	err = uow.Within(ctx, func(txCtx context.Context) error {
		if recordErr := repository.RecordCompletionEffect(txCtx, surveyport.ID(questionnaireID), surveyport.ID(submissionID), "local-webhook", "eer_rollback", "queued", digest, now); recordErr != nil {
			return recordErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error=%v", err)
	}
	var count int
	if err := native.QueryRow(ctx, `SELECT count(*) FROM survey_external_operation_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back receipt count=%d err=%v", count, err)
	}
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		if err := repository.RecordCompletionEffect(txCtx, surveyport.ID(questionnaireID), surveyport.ID(submissionID), "local-webhook", "eer_1", "queued", digest, now); err != nil {
			return err
		}
		return repository.RecordCompletionSnapshot(txCtx, surveyport.ID(questionnaireID), surveyport.ID(submissionID), surveyport.CompletionPolicy{ConfigurationReference: "local-webhook", ConfigurationVersion: "v1", ConfigurationDigest: sourceDigest}, strings.Repeat("a", 64), identityCiphertext, now)
	}); err != nil {
		t.Fatal(err)
	}
	var disabled surveyport.OperationReceipt
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var disabledErr error
		disabled, disabledErr = repository.RecordDisabledOperation(txCtx, surveyport.ID(questionnaireID), nil, "external_push", sha256.Sum256([]byte("disabled-operation-scan")), now)
		return disabledErr
	}); err != nil || disabled.ID < 1 || disabled.SourcePK != "" || disabled.ProviderCallAttempted != nil {
		t.Fatalf("disabled receipt=%+v err=%v", disabled, err)
	}

	var payload surveyport.CompletionPayload
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var readErr error
		payload, readErr = repository.ReadCompletionPayload(txCtx, sourceDigest)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if payload.CustomerID != customerID || payload.ExternalUserID != "union-snapshot" || len(payload.Answers) != 1 || payload.Answers[0].TextValue != "需要回访" {
		t.Fatalf("completion payload=%+v", payload)
	}
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		return repository.CompleteCompletionEffect(txCtx, "eer_1", "executed", true, true, boolPointer(true), sourceDigest, 1, now.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	var status string
	var callAttempted, realCall, resultReceived bool
	var providerAttempt int32
	if err := native.QueryRow(ctx, `SELECT status,provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE effect_id='eer_1'`).Scan(&status, &callAttempted, &realCall, &resultReceived, &providerAttempt); err != nil || status != "executed" || !callAttempted || !realCall || !resultReceived || providerAttempt != 1 {
		t.Fatalf("completion receipt status=%q call=%v real=%v result=%v attempt=%d err=%v", status, callAttempted, realCall, resultReceived, providerAttempt, err)
	}
}

func boolPointer(value bool) *bool { return &value }

func bytes32(value byte) []byte {
	result := make([]byte, 32)
	result[0] = value
	return result
}

func TestPostgreSQLSubmissionClaimsAreAtomicPerCustomerAndQuestionnaire(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)

	var actorID, firstCustomer, secondCustomer, rollbackCustomer, concurrentCustomer, historicalCustomer int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-single-submit','$argon2id$test','Survey Single Submit') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []*int64{&firstCustomer, &secondCustomer, &rollbackCustomer, &concurrentCustomer, &historicalCustomer} {
		if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	var questionnaireID, definitionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Single submit','Single submit','','survey','all_in_one','single-submit','published',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Single submit','','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, bytes32(91), now, actorID).Scan(&definitionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, definitionID, questionnaireID); err != nil {
		t.Fatal(err)
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	questionnaire := surveyport.Questionnaire{ID: surveyport.ID(questionnaireID), DefinitionVersion: 1, Slug: "single-submit", Title: "Single submit", Mode: surveyport.ModeSurvey}
	digest := func(marker byte) [32]byte {
		var value [32]byte
		value[0] = marker
		return value
	}
	persist := func(customer int64, key, payload byte) (surveyport.Submission, bool, error) {
		input := surveyapp.PersistSubmission{
			Questionnaire:       questionnaire,
			Command:             surveyport.SubmitCommand{Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: customerPointer(customer), EvidenceDigest: strings.Repeat("a", 64)}},
			SubmissionKeyDigest: digest(key),
			PayloadDigest:       digest(payload),
			TokenDigest:         digest(key + 60),
			Answers:             []surveyport.AnswerSnapshot{},
			Now:                 now,
		}
		var submission surveyport.Submission
		var created bool
		err := uow.Within(ctx, func(txCtx context.Context) error {
			var createErr error
			submission, created, createErr = repository.CreateSubmission(txCtx, input)
			return createErr
		})
		return submission, created, err
	}

	first, created, err := persist(firstCustomer, 1, 11)
	if err != nil || !created || first.ID < 1 {
		t.Fatalf("first submission=%+v created=%t err=%v", first, created, err)
	}
	replayed, created, err := persist(firstCustomer, 1, 11)
	if err != nil || created || replayed.ID != first.ID {
		t.Fatalf("same key replay=%+v created=%t err=%v", replayed, created, err)
	}
	if _, _, err = persist(firstCustomer, 1, 12); !errors.Is(err, surveyport.ErrConflict) {
		t.Fatalf("same key payload drift error=%v", err)
	}
	if _, _, err = persist(firstCustomer, 2, 21); !errors.Is(err, surveyport.ErrAlreadySubmitted) {
		t.Fatalf("different key duplicate error=%v", err)
	}
	if _, created, err = persist(secondCustomer, 2, 21); err != nil || !created {
		t.Fatalf("different customer created=%t err=%v", created, err)
	}

	rollback := errors.New("force single-submit rollback")
	err = uow.Within(ctx, func(txCtx context.Context) error {
		_, _, createErr := repository.CreateSubmission(txCtx, surveyapp.PersistSubmission{
			Questionnaire:       questionnaire,
			Command:             surveyport.SubmitCommand{Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: customerPointer(rollbackCustomer), EvidenceDigest: strings.Repeat("b", 64)}},
			SubmissionKeyDigest: digest(3), PayloadDigest: digest(31), TokenDigest: digest(63), Answers: []surveyport.AnswerSnapshot{}, Now: now,
		})
		if createErr != nil {
			return createErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error=%v", err)
	}
	if _, created, err = persist(rollbackCustomer, 4, 41); err != nil || !created {
		t.Fatalf("rolled-back claim blocked retry created=%t err=%v", created, err)
	}

	// An imported/older submission is intentionally not claimed by this
	// migration; its customer can make one new post-cutover submission.
	if _, err = native.Exec(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,'resolved',$4,$5,'single-submit','Single submit','survey','{}',$6,$6)`, questionnaireID, definitionID, historicalCustomer, bytes32(70), bytes32(71), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, created, err = persist(historicalCustomer, 5, 51); err != nil || !created {
		t.Fatalf("historical row was backfilled or blocked created=%t err=%v", created, err)
	}

	start := make(chan struct{})
	type concurrentResult struct {
		created bool
		err     error
	}
	results := make(chan concurrentResult, 2)
	for _, key := range []byte{6, 7} {
		key := key
		go func() {
			<-start
			_, made, createErr := persist(concurrentCustomer, key, key+50)
			results <- concurrentResult{created: made, err: createErr}
		}()
	}
	close(start)
	firstResult, secondResult := <-results, <-results
	createdCount, duplicateCount := 0, 0
	for _, result := range []concurrentResult{firstResult, secondResult} {
		if result.err == nil && result.created {
			createdCount++
		}
		if errors.Is(result.err, surveyport.ErrAlreadySubmitted) {
			duplicateCount++
		}
	}
	if createdCount != 1 || duplicateCount != 1 {
		t.Fatalf("concurrent results=%+v/%+v", firstResult, secondResult)
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM survey_submission_claims WHERE questionnaire_id=$1 AND customer_id=$2`, questionnaireID, concurrentCustomer).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent claim count=%d err=%v", count, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1 AND customer_id=$2`, questionnaireID, concurrentCustomer).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent submission count=%d err=%v", count, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM survey_result_tokens token JOIN survey_submissions submission ON submission.id=token.submission_id WHERE submission.questionnaire_id=$1 AND submission.customer_id=$2`, questionnaireID, concurrentCustomer).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent result token count=%d err=%v", count, err)
	}

	// Re-publishing changes the active mutable definition, not a retained
	// receipt. The narrow lookup that precedes public validation must still
	// recover that receipt by its original key and payload.
	var republishedDefinitionID int64
	if err = native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,2,'survey','all_in_one','Single submit v2','','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, bytes32(92), now.Add(time.Minute), actorID).Scan(&republishedDefinitionID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1,version=version+1 WHERE id=$2`, republishedDefinitionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txCtx context.Context) error {
		retained, lookupErr := repository.GetBySlug(txCtx, "single-submit")
		if lookupErr != nil || retained.ID != surveyport.ID(questionnaireID) || retained.Status != surveyport.StatusPublished {
			return fmt.Errorf("republished questionnaire=%+v err=%w", retained, lookupErr)
		}
		replayed, found, lookupErr := repository.FindSubmissionByKey(txCtx, surveyport.ID(questionnaireID), digest(1), digest(11))
		if lookupErr != nil || !found || replayed.ID != first.ID || replayed.DefinitionVersion != 1 {
			return fmt.Errorf("republished receipt=%+v found=%t err=%w", replayed, found, lookupErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, stopped := range []surveyport.QuestionnaireStatus{surveyport.StatusDisabled, surveyport.StatusArchived} {
		if _, err = native.Exec(ctx, `UPDATE survey_questionnaires SET status=$1 WHERE id=$2`, stopped, questionnaireID); err != nil {
			t.Fatal(err)
		}
		if err = uow.Within(ctx, func(txCtx context.Context) error {
			retained, lookupErr := repository.GetBySlug(txCtx, "single-submit")
			if lookupErr != nil || retained.Status != stopped {
				return fmt.Errorf("stopped questionnaire=%+v err=%w", retained, lookupErr)
			}
			claimed, lookupErr := repository.HasSubmissionClaim(txCtx, surveyport.ID(questionnaireID), customerdomain.CustomerID(firstCustomer))
			if lookupErr != nil || !claimed {
				return fmt.Errorf("stopped claim=%t err=%w", claimed, lookupErr)
			}
			replayed, found, lookupErr := repository.FindSubmissionByKey(txCtx, surveyport.ID(questionnaireID), digest(1), digest(11))
			if lookupErr != nil || !found || replayed.ID != first.ID {
				return fmt.Errorf("stopped receipt=%+v found=%t err=%w", replayed, found, lookupErr)
			}
			if _, lookupErr = repository.GetPublishedBySlug(txCtx, "single-submit"); !errors.Is(lookupErr, surveyport.ErrNotFound) {
				return fmt.Errorf("stopped public lookup err=%w", lookupErr)
			}
			return nil
		}); err != nil {
			t.Fatalf("state=%s retained claim lookup: %v", stopped, err)
		}
	}
}

func customerPointer(value int64) *customerdomain.CustomerID {
	customer := customerdomain.CustomerID(value)
	return &customer
}

func TestPostgreSQLListLoadsActiveDefinitionsAfterClosingBaseRows(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()

	var actorID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-list-test','$argon2id$test','Survey List Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	var questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Imported questionnaire','Stale title','','survey','all_in_one','imported-questionnaire','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	var versionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Imported title','Imported description','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		keyDigest, payloadDigest := make([]byte, 32), make([]byte, 32)
		keyDigest[0], payloadDigest[0] = byte(index+1), byte(index+11)
		if _, err := native.Exec(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,'anonymous',$3,$4,'imported-questionnaire','Imported title','survey','{}',$5,$5)`, questionnaireID, versionID, keyDigest, payloadDigest, now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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

	var items []surveyport.Questionnaire
	var total int64
	err = uow.Within(ctx, func(txCtx context.Context) error {
		var listErr error
		items, total, listErr = repository.List(txCtx, 50, 0, "", "")
		return listErr
	})
	if err != nil {
		t.Fatalf("list questionnaire with active definition: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("total=%d items=%d", total, len(items))
	}
	if items[0].ID != surveyport.ID(questionnaireID) || items[0].Title != "Imported title" || items[0].DefinitionVersion != 1 || items[0].SubmissionCount != 3 {
		t.Fatalf("item=%+v", items[0])
	}
}

func TestPostgreSQLExternalSubmissionProjectionKeepsHistoricUnionBoundaryAndLoadsProtectedAnswers(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)

	var actorID, questionnaireID, versionID, batchID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-external-projection','$argon2id$test','Survey External Projection') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('External projection','External projection','','survey','all_in_one','external-projection','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','External projection','', '{}'::jsonb,$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, bytes32(71), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_migration_batches(batch_key,source_system,snapshot_at,manifest,manifest_digest,status,created_at,updated_at) VALUES('external-projection-fixture','ai-crm-v2',$1,'{}'::jsonb,$2,'reconciled',$1,$1) RETURNING id`, now, bytes32(72)).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO survey_migration_source_map(migration_batch_id,source_system,source_table,source_pk,target_table,target_pk,record_digest,import_state,created_at) VALUES($1,'ai-crm-v2','questionnaires','41','survey_questionnaires',$2,$3,'imported',$4)`, batchID, questionnaireID, bytes32(73), now); err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	insertSubmission := func(sourcePK string, submittedAt time.Time, result string, text string, marker byte) int64 {
		t.Helper()
		var submissionID int64
		if err := native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,'unresolved',$3,$4,'external-projection','External projection','survey',$5,$6,$6) RETURNING id`, questionnaireID, versionID, bytes32(marker), bytes32(marker+1), result, submittedAt).Scan(&submissionID); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `INSERT INTO survey_migration_source_map(migration_batch_id,source_system,source_table,source_pk,target_table,target_pk,record_digest,import_state,created_at) VALUES($1,'ai-crm-v2','questionnaire_submissions',$2,'survey_submissions',$3,$4,'imported',$5)`, batchID, sourcePK, submissionID, bytes32(marker+2), now); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `INSERT INTO survey_legacy_external_projections(submission_id,historical_unionid,source_projection_digest,created_at) VALUES($1,'union-history',$2,$3)`, submissionID, bytes32(marker+3), now); err != nil {
			t.Fatal(err)
		}
		encrypted, encryptErr := cipher.Encrypt(text)
		if encryptErr != nil {
			t.Fatal(encryptErr)
		}
		if _, err := native.Exec(ctx, `INSERT INTO survey_submission_answers(submission_id,question_type,question_title_snapshot,selected_options_snapshot,text_value_ciphertext,answer_digest,score_snapshot,created_at) VALUES($1,'textarea','What do you need?',$2,$3,$4,2.5,$5)`, submissionID, `[{"option_text":"Consulting"}]`, encrypted, bytes32(marker+4), now); err != nil {
			t.Fatal(err)
		}
		return submissionID
	}
	oldest := insertSubmission("501", now.Add(-2*time.Hour), `{"summary":"old","_legacy_final_tags":["warm"],"_legacy_matched_by":"unionid"}`, "old protected answer", 80)
	newest := insertSubmission("502", now.Add(-time.Hour), `{"summary":"new","_legacy_final_tags":["hot"],"_legacy_matched_by":"unionid"}`, "new protected answer", 90)
	if oldest >= newest {
		t.Fatalf("fixture submission ids oldest=%d newest=%d", oldest, newest)
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	service := surveyapp.NewSubmissionService(uow, repository, cipher)
	page, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{"union-history"}, QuestionnaireSourceID: 41, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Limit != 100 || page.Offset != 0 || len(page.Items) != 2 {
		t.Fatalf("external page=%+v", page)
	}
	first := page.Items[0]
	if int64(first.SubmissionID) != newest || first.SourceSystem != "ai-crm-v2" || first.SourceRecordID != "502" || first.HistoricalUnionID != "union-history" || first.QuestionnaireSourceID != 41 || first.DefinitionVersion != 1 || first.QuestionnaireTitle != "External projection" || !first.SubmittedAt.Equal(now.Add(-time.Hour)) || string(first.FinalTags) != `["hot"]` || len(first.Answers) != 1 || first.Answers[0].QuestionTitle != "What do you need?" || len(first.Answers[0].SelectedOptionTexts) != 1 || first.Answers[0].SelectedOptionTexts[0] != "Consulting" || first.Answers[0].TextValue != "new protected answer" || first.Answers[0].ScoreContribution != 2.5 {
		t.Fatalf("external item=%+v", first)
	}
	var assessment map[string]any
	if err := json.Unmarshal(first.AssessmentResult, &assessment); err != nil || len(assessment) != 1 || assessment["summary"] != "new" {
		t.Fatalf("assessment=%s err=%v", first.AssessmentResult, err)
	}
	paged, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{"union-history"}, SubmittedFrom: now.Add(-90 * time.Minute), Limit: 1, Offset: 0})
	if err != nil || paged.Total != 1 || len(paged.Items) != 1 || !paged.Items[0].SubmittedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("time filtered page=%+v err=%v", paged, err)
	}
	exclusive, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{"union-history"}, SubmittedFrom: now.Add(-3 * time.Hour), SubmittedTo: now.Add(-time.Hour), SubmittedEndExclusive: true, Limit: 100})
	if err != nil || exclusive.Total != 1 || len(exclusive.Items) != 1 || int64(exclusive.Items[0].SubmissionID) != oldest {
		t.Fatalf("exclusive end page=%+v err=%v", exclusive, err)
	}
	empty, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{"union-history"}, QuestionnaireSourceID: 42, Limit: 100})
	if err != nil || empty.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("questionnaire filter page=%+v err=%v", empty, err)
	}
	if _, err = service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{" union-history"}, Limit: 100}); !errors.Is(err, surveyport.ErrInvalid) {
		t.Fatalf("invalid historic union error=%v", err)
	}
	sourceFiltered, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{"union-history"}, SourceSystem: "ai-crm-v2", SourceRecordID: "502", Limit: 100})
	if err != nil || sourceFiltered.Total != 1 || len(sourceFiltered.Items) != 1 || int64(sourceFiltered.Items[0].SubmissionID) != newest || sourceFiltered.Items[0].SourceRecordID != "502" {
		t.Fatalf("source filtered page=%+v err=%v", sourceFiltered, err)
	}
	if _, err = service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{"union-history"}, SourceSystem: "ai-crm-v2", Limit: 100}); !errors.Is(err, surveyport.ErrInvalid) {
		t.Fatalf("partial source filter error=%v", err)
	}

	// A newly submitted V3 record has no migration source row or historic
	// union projection. It remains visible through the resolved canonical
	// customer while the two legacy records remain union-scoped.
	var nativeCustomerID, otherCustomerID, nativeSubmissionID int64
	if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&nativeCustomerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&otherCustomerID); err != nil {
		t.Fatal(err)
	}
	nativeAt := now.Add(-30 * time.Minute)
	if err := native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,evidence_digest,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,'resolved',$4,$5,$6,'external-projection','External projection','survey','{"tag_codes":["native-tag"],"summary":"native"}',$7,$7) RETURNING id`, questionnaireID, versionID, nativeCustomerID, bytes32(101), bytes32(102), bytes32(103), nativeAt).Scan(&nativeSubmissionID); err != nil {
		t.Fatal(err)
	}
	nativeText, err := cipher.Encrypt("native protected answer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO survey_submission_answers(submission_id,question_type,question_title_snapshot,selected_options_snapshot,text_value_ciphertext,answer_digest,score_snapshot,created_at) VALUES($1,'textarea','What do you need?',$2,$3,$4,3.5,$5)`, nativeSubmissionID, `[{"option_text":"Native consulting"}]`, nativeText, bytes32(104), nativeAt); err != nil {
		t.Fatal(err)
	}
	mixed, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: nativeCustomerID, HistoricalUnionIDs: []string{"union-history"}, QuestionnaireSourceID: 41, Limit: 2})
	if err != nil || mixed.Total != 3 || len(mixed.Items) != 2 || int64(mixed.Items[0].SubmissionID) != nativeSubmissionID || mixed.Items[0].SourceSystem != "aicrm_v3" || mixed.Items[0].SourceRecordID != fmt.Sprint(nativeSubmissionID) || mixed.Items[0].Legacy || mixed.Items[0].HistoricalUnionID != "" || mixed.Items[0].QuestionnaireSourceID != 41 || !mixed.Items[0].SubmittedAt.Equal(nativeAt) || string(mixed.Items[0].FinalTags) != `["native-tag"]` || mixed.Items[0].Answers[0].TextValue != "native protected answer" {
		t.Fatalf("mixed first page=%+v err=%v", mixed, err)
	}
	mixedNext, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: nativeCustomerID, HistoricalUnionIDs: []string{"union-history"}, QuestionnaireSourceID: 41, Limit: 2, Offset: 2})
	if err != nil || mixedNext.Total != 3 || len(mixedNext.Items) != 1 || !mixedNext.Items[0].Legacy || !mixedNext.Items[0].SubmittedAt.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("mixed second page=%+v err=%v", mixedNext, err)
	}

	// V1 uses an exclusive end watermark plus a descending keyset rather than
	// an offset. A record committed after the first page with a submitted_at
	// past that watermark cannot shift the next page or create a duplicate.
	windowEnd := now
	keysetFirst, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: nativeCustomerID, HistoricalUnionIDs: []string{"union-history"}, QuestionnaireSourceID: 41, SubmittedTo: windowEnd, SubmittedEndExclusive: true, Limit: 1})
	if err != nil || keysetFirst.Total != 3 || len(keysetFirst.Items) != 1 || int64(keysetFirst.Items[0].SubmissionID) != nativeSubmissionID {
		t.Fatalf("keyset first page=%+v err=%v", keysetFirst, err)
	}
	var concurrentSubmissionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,evidence_digest,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,'resolved',$4,$5,$6,'external-projection','External projection','survey','{}',$7,$8) RETURNING id`, questionnaireID, versionID, nativeCustomerID, bytes32(105), bytes32(106), bytes32(107), now.Add(time.Minute), now).Scan(&concurrentSubmissionID); err != nil {
		t.Fatal(err)
	}
	keysetNext, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: nativeCustomerID, HistoricalUnionIDs: []string{"union-history"}, QuestionnaireSourceID: 41, SubmittedTo: windowEnd, SubmittedEndExclusive: true, BeforeSubmittedAt: keysetFirst.Items[0].SubmittedAt, BeforeSubmissionID: keysetFirst.Items[0].SubmissionID, Limit: 2})
	if err != nil || keysetNext.Total != 2 || len(keysetNext.Items) != 2 || int64(keysetNext.Items[0].SubmissionID) != newest || int64(keysetNext.Items[1].SubmissionID) != oldest {
		t.Fatalf("keyset next page=%+v err=%v", keysetNext, err)
	}
	for _, item := range keysetNext.Items {
		if int64(item.SubmissionID) == nativeSubmissionID || int64(item.SubmissionID) == concurrentSubmissionID {
			t.Fatalf("keyset page repeated or admitted post-watermark item=%+v", item)
		}
	}

	foreign, err := service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: otherCustomerID, Limit: 100})
	if err != nil || foreign.Total != 0 || len(foreign.Items) != 0 {
		t.Fatalf("foreign customer native page=%+v err=%v", foreign, err)
	}
}

func TestPostgreSQLExternalSubmissionSourceQuestionnaireIDCollisionFailsClosed(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	var actorID, mappedQuestionnaireID, versionID, batchID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-external-collision','$argon2id$test','Survey External Collision') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Mapped questionnaire','Mapped questionnaire','','survey','all_in_one','mapped-questionnaire','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&mappedQuestionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Mapped questionnaire','', '{}'::jsonb,$2,TRUE,$3,$4,$3) RETURNING id`, mappedQuestionnaireID, bytes32(111), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, mappedQuestionnaireID); err != nil {
		t.Fatal(err)
	}
	// id=41 is an unrelated V3-native questionnaire while donor source id 41
	// maps to mappedQuestionnaireID. The legacy integer cannot choose either.
	if _, err := native.Exec(ctx, `INSERT INTO survey_questionnaires(id,name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) OVERRIDING SYSTEM VALUE VALUES(41,'Native collision','Native collision','','survey','all_in_one','native-collision','disabled',$1,$1,$2,$2)`, actorID, now); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_migration_batches(batch_key,source_system,snapshot_at,manifest,manifest_digest,status,created_at,updated_at) VALUES('external-projection-collision','ai-crm-v2',$1,'{}'::jsonb,$2,'reconciled',$1,$1) RETURNING id`, now, bytes32(112)).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO survey_migration_source_map(migration_batch_id,source_system,source_table,source_pk,target_table,target_pk,record_digest,import_state,created_at) VALUES($1,'ai-crm-v2','questionnaires','41','survey_questionnaires',$2,$3,'imported',$4)`, batchID, mappedQuestionnaireID, bytes32(113), now); err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	service := surveyapp.NewSubmissionService(uow, repository, cipher)
	if _, err = service.ExternalSubmissions(ctx, surveyport.ExternalSubmissionQuery{CustomerID: 1, HistoricalUnionIDs: []string{"union-history"}, QuestionnaireSourceID: 41, Limit: 100}); !errors.Is(err, surveyport.ErrConflict) {
		t.Fatalf("source/V3 questionnaire collision error=%v", err)
	}
}

func TestPostgreSQLOperationConfigurationVersionConflictPreservesConcurrentToggleAndReference(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)

	var actorID, questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-config-cas-test','$argon2id$test','Survey Config CAS Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Configuration CAS questionnaire','Configuration CAS questionnaire','','survey','all_in_one','configuration-cas-questionnaire','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	service := surveyapp.NewSubmissionService(uow, repository, cipher)

	initial, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	if err != nil || initial.Version != 0 {
		t.Fatalf("initial config=%+v err=%v", initial, err)
	}
	first, err := service.SaveOperationConfiguration(ctx, surveyport.OperationConfiguration{QuestionnaireID: surveyport.ID(questionnaireID), ExternalPushEnabled: true, ExternalPushConfigurationRef: "push.v1", ExternalPushMetadata: json.RawMessage(`{"remark":"first"}`), Version: initial.Version}, actorID, "survey-config-cas-first-0001")
	if err != nil || first.Version != 1 {
		t.Fatalf("first config=%+v err=%v", first, err)
	}

	// Request A has read v1. Request B changes the independent enable/ref
	// controls before A attempts its metadata-only save.
	requestA, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	if err != nil || requestA.Version != 1 {
		t.Fatalf("request A config=%+v err=%v", requestA, err)
	}
	requestB := requestA
	requestB.ExternalPushEnabled = false
	requestB.ExternalPushConfigurationRef = "push.v2"
	requestB.ExternalPushMetadata = json.RawMessage(`{"remark":"changed-by-b"}`)
	updatedByB, err := service.SaveOperationConfiguration(ctx, requestB, actorID, "survey-config-cas-second-0002")
	if err != nil || updatedByB.Version != 2 {
		t.Fatalf("request B config=%+v err=%v", updatedByB, err)
	}
	requestA.ExternalPushMetadata = json.RawMessage(`{"remark":"stale-a","custom_params":{"campaign":"autumn"}}`)
	if _, err = service.SaveOperationConfiguration(ctx, requestA, actorID, "survey-config-cas-stale-a-0003"); !errors.Is(err, surveyport.ErrConflict) {
		t.Fatalf("stale request error=%v want conflict", err)
	}

	stored, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	var storedMetadata map[string]string
	metadataErr := json.Unmarshal(stored.ExternalPushMetadata, &storedMetadata)
	if err != nil || metadataErr != nil || stored.Version != 2 || stored.ExternalPushEnabled || stored.ExternalPushConfigurationRef != "push.v2" || storedMetadata["remark"] != "changed-by-b" {
		t.Fatalf("stale save overwrote concurrent configuration: %+v err=%v", stored, err)
	}
	for table, want := range map[string]int64{"survey_audit_events": 2, "survey_outbox": 2} {
		var got int64
		if err := native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("stale configuration transaction left %s rows=%d want=%d", table, got, want)
		}
	}
}

func TestPostgreSQLSyntheticCompletionTestSnapshotReplaysWithoutCustomer(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	var actorID, questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-test-push','$argon2id$test','Survey Test Push') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Synthetic test push','Synthetic test push','','survey','all_in_one','synthetic-test-push','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	value := surveyapp.CompletionTestSnapshot{QuestionnaireID: surveyport.ID(questionnaireID), TestRunID: "questionnaire-test-0123456789abcdef0123456789abcdef", QuestionnaireTitle: "Synthetic test push", SubmittedAt: now, Policy: surveyport.CompletionPolicy{ConfigurationReference: "test-webhook", ConfigurationVersion: "v1", ConfigurationDigest: "sha256:" + strings.Repeat("a", 64), CustomParams: map[string]string{"campaign": "autumn"}}, SourceDigest: "sha256:" + strings.Repeat("b", 64), TargetDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: "sha256:" + strings.Repeat("d", 64), PolicyDigest: "sha256:" + strings.Repeat("e", 64), IdempotencyKey: "survey-synthetic-test-push-0001"}
	var created bool
	if err = uow.Within(ctx, func(tx context.Context) error {
		stored, didCreate, recordErr := repository.RecordCompletionTestSnapshot(tx, value)
		if recordErr != nil || !didCreate || stored.TestRunID != value.TestRunID {
			t.Fatalf("store synthetic snapshot=%+v created=%v err=%v", stored, didCreate, recordErr)
		}
		created = didCreate
		digest := sha256.Sum256([]byte(value.SourceDigest))
		return repository.RecordCompletionTestEffect(tx, value.QuestionnaireID, value.TestRunID, value.Policy.ConfigurationReference, "eer_synthetic_1", "queued", digest, now)
	}); err != nil || !created {
		t.Fatalf("persist synthetic snapshot err=%v created=%v", err, created)
	}
	var payload surveyport.CompletionPayload
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		payload, readErr = repository.ReadCompletionPayload(tx, value.SourceDigest)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if !payload.SyntheticTest || payload.TestRunID != value.TestRunID || payload.CustomerID != 0 || payload.SubmissionID != 0 || payload.ExternalUserID != "questionnaire_test" || len(payload.Answers) != 0 || payload.Policy.CustomParams["campaign"] != "autumn" {
		t.Fatalf("synthetic payload=%+v", payload)
	}
	// The outbound provider runs after the accepting transaction has committed.
	// It must reconstruct this protected synthetic payload through the
	// repository's read-only pool path, not require a transaction that no
	// longer exists.
	payload, err = repository.ReadCompletionPayload(ctx, value.SourceDigest)
	if err != nil {
		t.Fatalf("synthetic payload outside transaction: %v", err)
	}
	if !payload.SyntheticTest || payload.TestRunID != value.TestRunID || payload.ExternalUserID != "questionnaire_test" || len(payload.Answers) != 0 || payload.Policy.CustomParams["campaign"] != "autumn" {
		t.Fatalf("outside transaction synthetic payload=%+v", payload)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, didCreate, recordErr := repository.RecordCompletionTestSnapshot(tx, value)
		if recordErr != nil || didCreate {
			t.Fatalf("replay snapshot created=%v err=%v", didCreate, recordErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drift := value
	drift.PayloadDigest = "sha256:" + strings.Repeat("f", 64)
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, _, recordErr := repository.RecordCompletionTestSnapshot(tx, drift)
		return recordErr
	}); !errors.Is(err, surveyport.ErrConflict) {
		t.Fatalf("synthetic drift error=%v", err)
	}
}

func TestPostgreSQLSyntheticCompletionTerminalReplayKeepsExecutionFacts(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 13, 30, 0, 0, time.UTC)
	var actorID, questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-terminal-replay','$argon2id$test','Survey terminal replay') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Synthetic terminal replay','Synthetic terminal replay','','survey','all_in_one','synthetic-terminal-replay','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	cases := []struct {
		name, terminal, wantStatus string
		callAttempted              bool
		resultReceived             *bool
	}{
		{name: "retryable", terminal: "retryable_failed", wantStatus: "attempted", callAttempted: true, resultReceived: boolPointer(true)},
		{name: "final", terminal: "final_failed", wantStatus: "failed", callAttempted: true, resultReceived: boolPointer(true)},
		{name: "cancelled", terminal: "cancelled", wantStatus: "queued", callAttempted: false, resultReceived: nil},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testRunID := "questionnaire-test-" + strings.Repeat(string(rune('a'+index)), 32)
			effectID := "eer_terminal_" + tc.name
			digest := sha256.Sum256([]byte("survey-terminal-replay:" + tc.name))
			if err := uow.Within(ctx, func(tx context.Context) error {
				return repository.RecordCompletionTestEffect(tx, surveyport.ID(questionnaireID), testRunID, "test-webhook", effectID, "queued", digest, now)
			}); err != nil {
				t.Fatal(err)
			}
			if tc.terminal != "cancelled" {
				if err := uow.Within(ctx, func(tx context.Context) error {
					return repository.CompleteCompletionEffect(tx, effectID, tc.terminal, tc.callAttempted, tc.callAttempted, tc.resultReceived, "sha256:"+strings.Repeat("a", 64), 1, now.Add(time.Minute))
				}); err != nil {
					t.Fatal(err)
				}
			}
			// EER can return a terminal projection when the same operator key is
			// replayed. The prospective insert must satisfy the legacy receipt
			// CHECK while the existing terminal execution facts remain unchanged.
			if err := uow.Within(ctx, func(tx context.Context) error {
				return repository.RecordCompletionTestEffect(tx, surveyport.ID(questionnaireID), testRunID, "test-webhook", effectID, tc.terminal, digest, now.Add(2*time.Minute))
			}); err != nil {
				t.Fatalf("terminal replay: %v", err)
			}
			var status string
			var callAttempted, realCall *bool
			var resultReceived *bool
			var attempt *int32
			if err := native.QueryRow(ctx, `SELECT status,provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE effect_id=$1`, effectID).Scan(&status, &callAttempted, &realCall, &resultReceived, &attempt); err != nil {
				t.Fatal(err)
			}
			if status != tc.wantStatus || (tc.terminal == "cancelled" && (callAttempted != nil || realCall != nil || resultReceived != nil || attempt != nil)) {
				t.Fatalf("terminal replay status=%q call=%v real=%v result=%v attempt=%v", status, callAttempted, realCall, resultReceived, attempt)
			}
			if tc.terminal != "cancelled" && (callAttempted == nil || !*callAttempted || realCall == nil || !*realCall || resultReceived == nil || !*resultReceived || attempt == nil || *attempt != 1) {
				t.Fatalf("terminal facts changed call=%v real=%v result=%v attempt=%v", callAttempted, realCall, resultReceived, attempt)
			}
		})
	}
}

func TestPostgreSQLArchiveRetainsChildrenAndBlocksOperationConfigurationWritesAtomically(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()

	var actorID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-status-test','$argon2id$test','Survey Status Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	var questionnaireID, versionID, questionID, optionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Imported status questionnaire','Imported status questionnaire','','survey','all_in_one','imported-status-questionnaire','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Imported status questionnaire','','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,sort_order) VALUES($1,'single_choice','Retained question',0) RETURNING id`, versionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,sort_order) VALUES($1,$2,'Retained option',0) RETURNING id`, questionID, versionID).Scan(&optionID); err != nil {
		t.Fatal(err)
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	submissions := surveyapp.NewSubmissionService(uow, repository, cipher)
	configured, err := submissions.SaveOperationConfiguration(ctx, surveyport.OperationConfiguration{
		QuestionnaireID: surveyport.ID(questionnaireID), CompletionNavigationRef: "retained-navigation",
		ExternalPushEnabled: true, ExternalPushConfigurationRef: "retained-push",
		ExternalPushMetadata: json.RawMessage(`{"retained":true}`), Version: 0,
	}, actorID, "survey-config-retained-before-archive-0001")
	if err != nil || configured.Version != 1 {
		t.Fatalf("configure before archive=%+v err=%v", configured, err)
	}

	updated, err := definitions.SetStatus(ctx, surveyport.ID(questionnaireID), 1, surveyport.StatusPublished, actorID, "survey-enable-integration-0001")
	if err != nil {
		t.Fatalf("enable imported questionnaire: %v", err)
	}
	if updated.Status != surveyport.StatusPublished || updated.Version != 2 {
		t.Fatalf("updated=%+v", updated)
	}
	archived, err := definitions.SetStatus(ctx, surveyport.ID(questionnaireID), updated.Version, surveyport.StatusArchived, actorID, "survey-archive-integration-0002")
	if err != nil || archived.Status != surveyport.StatusArchived || archived.Version != 3 {
		t.Fatalf("archive=%+v err=%v", archived, err)
	}
	replay, err := definitions.SetStatus(ctx, surveyport.ID(questionnaireID), updated.Version, surveyport.StatusArchived, actorID, "survey-archive-integration-0002")
	if err != nil || replay.ID != archived.ID || replay.Version != archived.Version || replay.Status != surveyport.StatusArchived {
		t.Fatalf("archive replay=%+v err=%v", replay, err)
	}

	page, err := definitions.List(ctx, 20, 0, "", "")
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("archived questionnaire remained in default list=%+v err=%v", page, err)
	}
	historical, err := definitions.Get(ctx, surveyport.ID(questionnaireID))
	if err != nil || historical.Status != surveyport.StatusArchived || len(historical.Questions) != 1 || len(historical.Questions[0].Options) != 1 || historical.Questions[0].ID != surveyport.ID(questionID) || historical.Questions[0].Options[0].ID != surveyport.ID(optionID) {
		t.Fatalf("archived historical definition=%+v err=%v", historical, err)
	}
	if _, err = submissions.ReadPublic(ctx, "imported-status-questionnaire"); !errors.Is(err, surveyport.ErrNotFound) {
		t.Fatalf("archived questionnaire public read error=%v", err)
	}
	retained, err := submissions.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	if err != nil || retained.Version != configured.Version || retained.CompletionNavigationRef != configured.CompletionNavigationRef || retained.ExternalPushConfigurationRef != configured.ExternalPushConfigurationRef || !retained.ExternalPushEnabled || string(retained.ExternalPushMetadata) != string(configured.ExternalPushMetadata) {
		t.Fatalf("retained configuration=%+v err=%v", retained, err)
	}
	blocked := retained
	blocked.ExternalPushConfigurationRef = "must-not-write"
	blocked.ExternalPushMetadata = json.RawMessage(`{"retained":false}`)
	if _, err = submissions.SaveOperationConfiguration(ctx, blocked, actorID, "survey-config-after-archive-0003"); !errors.Is(err, surveyport.ErrNotFound) {
		t.Fatalf("archived configuration write error=%v", err)
	}
	afterBlocked, err := submissions.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	if err != nil || afterBlocked.Version != retained.Version || afterBlocked.ExternalPushConfigurationRef != retained.ExternalPushConfigurationRef || string(afterBlocked.ExternalPushMetadata) != string(retained.ExternalPushMetadata) {
		t.Fatalf("blocked write changed historical configuration=%+v err=%v", afterBlocked, err)
	}
	for table, want := range map[string]int64{"survey_operation_receipts": 2, "survey_audit_events": 3, "survey_outbox": 3} {
		var got int64
		if err := native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d want=%d", table, got, want)
		}
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM survey_operation_configurations WHERE questionnaire_id=` + fmt.Sprint(questionnaireID): 1,
		`SELECT count(*) FROM survey_definition_questions WHERE definition_version_id=` + fmt.Sprint(versionID):      1,
		`SELECT count(*) FROM survey_definition_options WHERE definition_version_id=` + fmt.Sprint(versionID):        1,
	} {
		var got int64
		if err := native.QueryRow(ctx, query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("retained history query %q count=%d want=%d", query, got, want)
		}
	}
}

func TestPostgreSQLAudienceChoicesReadFirstResolvedCompletion(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	var actorID, customerID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-audience-test','$argon2id$test','Survey Audience Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	var questionnaireID, definitionID, questionID, firstOptionID, secondOptionID int64
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at)
		VALUES('Audience source','Audience source','','survey','all_in_one','audience-source','published',$1,$1,$2,$2)
		RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at)
		VALUES($1,1,'survey','all_in_one','Audience source','','{}',$2,TRUE,$3,$4,$3)
		RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&definitionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, definitionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_questions(definition_version_id,question_type,title,sort_order)
		VALUES($1,'multi_choice','Which choices?',0) RETURNING id`, definitionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,sort_order)
		VALUES($1,$2,'First choice',0) RETURNING id`, questionID, definitionID).Scan(&firstOptionID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,sort_order)
		VALUES($1,$2,'Second choice',1) RETURNING id`, questionID, definitionID).Scan(&secondOptionID); err != nil {
		t.Fatal(err)
	}

	insertSubmission := func(identityState, staffID string, customer *int64, submittedAt time.Time, key byte) int64 {
		t.Helper()
		keyDigest, payloadDigest := make([]byte, 32), make([]byte, 32)
		keyDigest[0], payloadDigest[0] = key, key+10
		var submissionID int64
		err := native.QueryRow(ctx, `
			INSERT INTO survey_submissions(
				questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,
				submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,
				result_snapshot,staff_id,submitted_at,created_at
			) VALUES($1,$2,1,$3,$4,$5,$6,'audience-source','Audience source','survey','{}',$7,$8,$8)
			RETURNING id`, questionnaireID, definitionID, customer, identityState, keyDigest, payloadDigest, staffID, submittedAt).Scan(&submissionID)
		if err != nil {
			t.Fatal(err)
		}
		return submissionID
	}
	insertAnswer := func(submissionID int64, options string, key byte) {
		t.Helper()
		digest := make([]byte, 32)
		digest[0] = key
		if _, err := native.Exec(ctx, `
			INSERT INTO survey_submission_answers(
				submission_id,definition_question_id,question_type,question_title_snapshot,
				selected_options_snapshot,answer_digest,created_at
			) VALUES($1,$2,'multi_choice','Which choices?',$3::jsonb,$4,$5)`,
			submissionID, questionID, options, digest, now); err != nil {
			t.Fatal(err)
		}
	}

	firstSubmissionID := insertSubmission("resolved", "owner-first", &customerID, now.Add(-48*time.Hour), 1)
	insertAnswer(firstSubmissionID, fmt.Sprintf(`[{"option_id":%d},{"option_id":%d}]`, firstOptionID, secondOptionID), 1)
	laterSubmissionID := insertSubmission("resolved", "owner-later", &customerID, now.Add(-24*time.Hour), 2)
	insertAnswer(laterSubmissionID, fmt.Sprintf(`[{"option_id":%d}]`, secondOptionID), 2)
	unresolvedSubmissionID := insertSubmission("unresolved", "owner-unresolved", nil, now.Add(-72*time.Hour), 3)
	insertAnswer(unresolvedSubmissionID, fmt.Sprintf(`[{"option_id":%d}]`, firstOptionID), 3)

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	var facts []surveyport.AudienceChoiceAnswer
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var readErr error
		facts, readErr = repository.FirstCompleteAudienceChoices(txCtx, now)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	// Submissions without choice answers and later submissions remain facts;
	// unresolved and future submissions do not qualify.
	insertSubmission("resolved", "owner-text-only", &customerID, now.Add(-time.Hour), 4)
	insertSubmission("resolved", "owner-future", &customerID, now.Add(time.Hour), 5)
	var submissions []surveyport.AudienceSubmission
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var e error
		submissions, e = repository.AudienceSubmissions(txCtx, now)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 3 {
		t.Fatalf("submission facts=%d", len(submissions))
	}
	if len(facts) != 1 {
		t.Fatalf("facts=%+v", facts)
	}
	fact := facts[0]
	if int64(fact.CustomerID) != customerID || fact.QuestionnaireID != surveyport.ID(questionnaireID) || fact.SubmissionID != surveyport.ID(firstSubmissionID) || fact.StaffID != "owner-first" || !fact.SubmittedAt.Equal(now.Add(-48*time.Hour)) || fact.QuestionID != surveyport.ID(questionID) {
		t.Fatalf("fact=%+v", fact)
	}
	if len(fact.OptionIDs) != 2 || fact.OptionIDs[0] != surveyport.ID(firstOptionID) || fact.OptionIDs[1] != surveyport.ID(secondOptionID) {
		t.Fatalf("option ids=%v", fact.OptionIDs)
	}
}

func TestPostgreSQLDefinitionReaderLoadsScopedQuestionAndOptionReferences(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)

	var actorID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-reference-test','$argon2id$test','Survey Reference Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	type fixture struct{ questionnaire, acquisitionQuestion, acquisitionOption, conversionOption int64 }
	insert := func(name, title, slug string, includeConversion bool) fixture {
		t.Helper()
		var item fixture
		if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES($1,$2,'','survey','all_in_one',$3,'published',$4,$4,$5,$5) RETURNING id`, name, title, slug, actorID, now).Scan(&item.questionnaire); err != nil {
			t.Fatal(err)
		}
		var definitionID int64
		if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one',$2,'','{}',$3,TRUE,$4,$5,$4) RETURNING id`, item.questionnaire, title, make([]byte, 32), now, actorID).Scan(&definitionID); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, definitionID, item.questionnaire); err != nil {
			t.Fatal(err)
		}
		if err := native.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,sort_order) VALUES($1,'single_choice','获客方式',0) RETURNING id`, definitionID).Scan(&item.acquisitionQuestion); err != nil {
			t.Fatal(err)
		}
		if err := native.QueryRow(ctx, `INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,sort_order) VALUES($1,$2,'内容',0) RETURNING id`, item.acquisitionQuestion, definitionID).Scan(&item.acquisitionOption); err != nil {
			t.Fatal(err)
		}
		if includeConversion {
			var conversionQuestion int64
			if err := native.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,sort_order) VALUES($1,'single_choice','成交方式',1) RETURNING id`, definitionID).Scan(&conversionQuestion); err != nil {
				t.Fatal(err)
			}
			if err := native.QueryRow(ctx, `INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,sort_order) VALUES($1,$2,'内容',0) RETURNING id`, conversionQuestion, definitionID).Scan(&item.conversionOption); err != nil {
				t.Fatal(err)
			}
		}
		return item
	}
	first := insert("customer-research", "客户调研", "customer-research", true)
	second := insert("other-research", "另一问卷", "other-research", false)

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	page, err := definitions.List(ctx, 100, 0, "客户调研", "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || int64(page.Items[0].ID) != first.questionnaire || len(page.Items[0].Questions) != 2 {
		t.Fatalf("page=%+v", page)
	}
	loaded := page.Items[0]
	if int64(loaded.Questions[0].ID) != first.acquisitionQuestion || len(loaded.Questions[0].Options) != 1 || int64(loaded.Questions[0].Options[0].ID) != first.acquisitionOption {
		t.Fatalf("acquisition question=%+v", loaded.Questions[0])
	}
	if int64(loaded.Questions[1].Options[0].ID) != first.conversionOption || first.acquisitionOption == first.conversionOption || first.acquisitionQuestion == second.acquisitionQuestion {
		t.Fatalf("same-title scope fixture was not distinct: first=%+v second=%+v", first, second)
	}
	if _, err = definitions.Get(ctx, surveyport.ID(second.questionnaire)); err != nil {
		t.Fatalf("get second questionnaire: %v", err)
	}
}

func surveyIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Survey PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "aicrm_survey_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	for _, migrationName := range []string{"0002_identity.sql", "0003_access.sql", "0018_survey.sql", "0038_survey_oauth_phone_vault.sql", "0067_survey_completion_snapshots.sql", "0073_survey_completion_test_push_snapshots.sql", "0074_survey_external_operation_execution_facts.sql", "0090_survey_oauth_state_redirect.sql", "0091_survey_assessment_business_keys.sql", "0099_survey_historical_external_projection.sql", "0168_survey_questionnaire_archive.sql", "0169_survey_questionnaire_archive_receipts.sql", "0176_survey_single_submission_claims.sql", "0177_survey_operation_legacy_parity.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migrationName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", migrationName, execErr)
		}
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
		admin.Close(cleanupCtx)
	}
}

func TestPostgreSQLUserinfoMigrationRevokesOnlyExistingActiveSessions(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldRevoked := now.Add(-time.Hour)
	for index, expires := range []time.Time{now.Add(time.Hour), now.Add(-time.Hour), now.Add(time.Hour)} {
		digest := sha256.Sum256([]byte(fmt.Sprintf("userinfo-policy-session-%d", index)))
		var revoked any
		if index == 2 {
			revoked = oldRevoked
		}
		if _, err := native.Exec(ctx, `INSERT INTO survey_identity_sessions(session_digest,identity_state,evidence_digest,expires_at,revoked_at,created_at) VALUES($1,'unresolved',$1,$2,$3,$4)`, digest[:], expires, revoked, now.Add(-2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	_, file, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0141_survey_require_userinfo_reauthorization.sql"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := native.Exec(ctx, string(migration))
	if err != nil || result.RowsAffected() != 1 {
		t.Fatalf("migration rows=%d err=%v", result.RowsAffected(), err)
	}
	var retained int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM survey_identity_sessions`).Scan(&retained); err != nil || retained != 3 {
		t.Fatalf("records lost: %d %v", retained, err)
	}
	var preserved bool
	if err = native.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_identity_sessions WHERE revoked_at=$1)`, oldRevoked).Scan(&preserved); err != nil || !preserved {
		t.Fatal("prior revocation changed")
	}
	fresh := sha256.Sum256([]byte("new-userinfo-proof-session"))
	if _, err = native.Exec(ctx, `INSERT INTO survey_identity_sessions(session_digest,identity_state,evidence_digest,expires_at,created_at) VALUES($1,'unresolved',$1,$2,$3)`, fresh[:], now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	var usable int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM survey_identity_sessions WHERE revoked_at IS NULL AND expires_at>now()`).Scan(&usable); err != nil || usable != 1 {
		t.Fatalf("post-policy session unusable: %d %v", usable, err)
	}
}
