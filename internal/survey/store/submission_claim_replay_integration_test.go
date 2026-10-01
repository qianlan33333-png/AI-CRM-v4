package store

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

// Freeze the actual first SELECT result, allow another complete transaction to
// commit, then execute the claim SELECT in the original transaction. No sleeps
// or mocked claim/receipt results are used.
type submissionLookupBarrier struct {
	surveyapp.SubmissionStore
	reached, release chan struct{}
	lookups          atomic.Int32
	claimed          atomic.Bool
}

func (s *submissionLookupBarrier) FindSubmissionByKey(ctx context.Context, id surveyport.ID, key, payload [32]byte) (surveyport.Submission, bool, error) {
	value, found, err := s.SubmissionStore.FindSubmissionByKey(ctx, id, key, payload)
	if s.lookups.Add(1) == 1 && !found && err == nil {
		close(s.reached)
		select {
		case <-s.release:
		case <-ctx.Done():
			return surveyport.Submission{}, false, ctx.Err()
		}
	}
	return value, found, err
}

func (s *submissionLookupBarrier) HasSubmissionClaim(ctx context.Context, id surveyport.ID, customer customerdomain.CustomerID) (bool, error) {
	claimed, err := s.SubmissionStore.HasSubmissionClaim(ctx, id, customer)
	s.claimed.Store(claimed)
	return claimed, err
}

type submissionClaimObserver struct{ calls atomic.Int32 }

func (s *submissionClaimObserver) SubmissionCreatedWithin(context.Context, int64, int64) error {
	s.calls.Add(1)
	return nil
}

type submissionClaimEffectAccepter struct{ calls atomic.Int32 }

func (s *submissionClaimEffectAccepter) AcceptCompletionWithin(context.Context, surveyport.CompletionIntent) (surveyport.EffectBinding, error) {
	s.calls.Add(1)
	return surveyport.EffectBinding{EffectID: "eer_claim_replay", State: "queued"}, nil
}

// The fixture has only a textarea; these required bindings must never be used.
type submissionClaimPhoneBinding struct {
	identityport.DeclaredPhoneAttacher
	customerport.ProjectionWriter
}

func TestPostgreSQLSubmissionClaimConcurrentReceiptReplay(t *testing.T) {
	for _, scenario := range []struct {
		name                           string
		differentKey, differentPayload bool
		wantErr                        error
	}{
		{name: "same_key_same_payload"},
		{name: "same_key_conflicting_payload", differentPayload: true, wantErr: surveyport.ErrConflict},
		{name: "different_key_same_customer", differentKey: true, wantErr: surveyport.ErrAlreadySubmitted},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			native, cleanup := surveyIntegrationPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var actor, customer, questionnaire, definition, question int64
			if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('claim-replay','$argon2id$test','Claim replay') RETURNING id`).Scan(&actor); err != nil {
				t.Fatal(err)
			}
			if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customer); err != nil {
				t.Fatal(err)
			}
			if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Claim replay','Claim replay','','survey','all_in_one','claim-replay','published',$1,$1,now(),now()) RETURNING id`, actor).Scan(&questionnaire); err != nil {
				t.Fatal(err)
			}
			if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Claim replay','','{}',$2,TRUE,now(),$3,now()) RETURNING id`, questionnaire, bytes32(1), actor).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, definition, questionnaire); err != nil {
				t.Fatal(err)
			}
			if err := native.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,required,sort_order,validation) VALUES($1,'textarea','Response',TRUE,0,'{}') RETURNING id`, definition).Scan(&question); err != nil {
				t.Fatal(err)
			}
			if _, err := native.Exec(ctx, `INSERT INTO survey_operation_configurations(questionnaire_id,external_push_enabled,external_push_configuration_ref,updated_by,updated_at) VALUES($1,TRUE,'claim-replay-local',$2,now())`, questionnaire, actor); err != nil {
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
			barrier := &submissionLookupBarrier{SubmissionStore: repository, reached: make(chan struct{}), release: make(chan struct{})}
			observer := &submissionClaimObserver{}
			effects := &submissionClaimEffectAccepter{}
			newService := func(store surveyapp.SubmissionStore) *surveyapp.SubmissionService {
				service := surveyapp.NewSubmissionService(uow, store, cipher)
				binding := submissionClaimPhoneBinding{}
				if err := service.BindDeclaredPhone(binding, binding); err != nil {
					t.Fatal(err)
				}
				service.BindSubmissionObserver(observer)
				if err := service.BindCompletionIntent(effects); err != nil {
					t.Fatal(err)
				}
				return service
			}
			winner, delayed := newService(repository), newService(barrier)
			command := surveyport.SubmitCommand{Slug: "claim-replay", DefinitionVersion: 1, SubmissionKey: strings.Repeat("a", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: customerPointer(customer), EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(question), TextValue: "original"}}}
			delayedCommand := command
			if scenario.differentKey {
				delayedCommand.SubmissionKey = strings.Repeat("c", 43)
			}
			if scenario.differentPayload {
				delayedCommand.Answers = []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(question), TextValue: "changed"}}
			}
			type result struct {
				receipt surveyport.SubmissionReceipt
				err     error
			}
			results := make(chan result, 1)
			go func() { receipt, err := delayed.Submit(ctx, delayedCommand); results <- result{receipt, err} }()
			select {
			case <-barrier.reached:
			case premature := <-results:
				t.Fatalf("submission returned before barrier: %v", premature.err)
			case <-ctx.Done():
				t.Fatal("first receipt SELECT did not reach barrier")
			}
			first, err := winner.Submit(ctx, command)
			close(barrier.release)
			if err != nil || first.SubmissionID < 1 || first.ResultToken == "" {
				t.Fatalf("winner=%+v err=%v", first, err)
			}
			var second result
			select {
			case second = <-results:
			case <-ctx.Done():
				t.Fatal("delayed submission did not finish")
			}
			if !barrier.claimed.Load() {
				t.Fatal("test did not enter committed claim branch")
			}
			if !errors.Is(second.err, scenario.wantErr) {
				t.Fatalf("delayed err=%v want=%v receipt=%+v", second.err, scenario.wantErr, second.receipt)
			}
			if scenario.wantErr == nil && (second.receipt.SubmissionID != first.SubmissionID || second.receipt.ResultToken != first.ResultToken || second.receipt.QuestionnaireID != first.QuestionnaireID) {
				t.Fatalf("receipts differ first=%+v second=%+v", first, second.receipt)
			}
			if observer.calls.Load() != 1 {
				t.Fatalf("observer invoked %d times", observer.calls.Load())
			}
			if effects.calls.Load() != 1 {
				t.Fatalf("completion effect accepted %d times", effects.calls.Load())
			}
			for _, table := range []string{"survey_submissions", "survey_submission_claims", "survey_result_tokens", "survey_submission_answers", "survey_audit_events", "survey_outbox", "survey_completion_push_snapshots", "survey_external_operation_receipts"} {
				var count int
				if err := native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 1 {
					t.Fatalf("%s count=%d err=%v", table, count, err)
				}
			}
		})
	}
}
