package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	"github.com/riverqueue/river"
)

// Synchronize only shared-lock acquisition to expose an unsafe later upgrade.
// An exclusive lineage implementation retains normal contention instead.
type surveyClaimLineageBarrier struct {
	identityquery.PostgreSQL
	mu      sync.Mutex
	arrived int
	ready   chan struct{}
}

func (b *surveyClaimLineageBarrier) LockedCanonicalLineage(ctx context.Context, id customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	ids, err := b.PostgreSQL.LockedCanonicalLineage(ctx, id)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.arrived++
	if b.arrived == 2 {
		close(b.ready)
	}
	b.mu.Unlock()
	select {
	case <-b.ready:
		return ids, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Actual OneID app merge and Survey UoW, synthetic verified identities only.
// No effect worker or Provider client. Historical claims retain their source.
func TestPostgreSQLSurveyClaimFollowsOneIDMergeLineage(t *testing.T) {
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
	defer wrapped.Close()
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
	if err = submissions.BindCollectedPhone(collectedPhoneTestRecorder(t)); err != nil {
		t.Fatal(err)
	}
	actorID := surveyJourneyGovernedActor(t, ctx, native, "survey-merge-claim-evidence", "Survey Merge Claim Evidence")
	var questionnaireID, definitionID, questionID int64
	now := time.Now().UTC()
	if err = native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Merge claim fixture','Merge claim fixture','','survey','all_in_one','merge-claim-evidence','published',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Merge claim fixture','','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&definitionID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,required,sort_order,validation) VALUES($1,'textarea','Answer',TRUE,0,'{}') RETURNING id`, definitionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, definitionID, questionnaireID); err != nil {
		t.Fatal(err)
	}

	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	loserFact := evidenceMergeFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:survey-merge-claim", "loser")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:survey-merge-claim", "survivor")
	var loserID, survivorID customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		loser, provisionErr := oneID.ProvisionCustomerFromVerifiedIdentity(tx, loserFact)
		if provisionErr != nil {
			return provisionErr
		}
		survivor, provisionErr := oneID.ProvisionCustomerFromVerifiedIdentity(tx, survivorFact)
		if provisionErr != nil {
			return provisionErr
		}
		loserID, survivorID = loser.CustomerID, survivor.CustomerID
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	submit := func(customerID customerdomain.CustomerID, key, answer string) error {
		_, submitErr := submissions.Submit(ctx, surveyport.SubmitCommand{
			Slug: "merge-claim-evidence", DefinitionVersion: 1, SubmissionKey: strings.Repeat(key, 43),
			Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customerID, EvidenceDigest: strings.Repeat("a", 64)},
			Answers:  []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(questionID), TextValue: answer}},
		})
		return submitErr
	}
	if err = submit(loserID, "a", "before merge"); err != nil {
		t.Fatalf("pre-merge Submit(loser) failed: %v", err)
	}

	evidence := identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "survey.merge.claim.test", EventID: "claim-lineage", Digest: "synthetic-claim-lineage", PolicyVersion: "test-v1"}
	var candidate identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var linkErr error
		candidate, linkErr = oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact, Evidence: evidence})
		return linkErr
	}); err != nil {
		t.Fatal(err)
	}
	if candidate.Candidate == nil {
		t.Fatalf("link did not create a merge candidate: %+v", candidate)
	}
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var mergeErr error
		merged, mergeErr = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "survey-claim-lineage-evidence"})
		return mergeErr
	}); err != nil {
		t.Fatal(err)
	}
	if merged.Merge == nil || merged.Merge.FromCustomerID != loserID || merged.Merge.ToCustomerID != survivorID {
		t.Fatalf("unexpected merge direction: %+v", merged.Merge)
	}

	secondErr := submit(survivorID, "b", "after merge")
	var claims, submissionsAfter int
	ids := []int64{int64(loserID), int64(survivorID)}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM survey_submission_claims WHERE questionnaire_id=$1 AND customer_id=ANY($2::bigint[])`, questionnaireID, ids).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1 AND customer_id=ANY($2::bigint[])`, questionnaireID, ids).Scan(&submissionsAfter); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(secondErr, surveyport.ErrAlreadySubmitted) || claims != 1 || submissionsAfter != 1 {
		t.Fatalf("canonical single-submit invariant failed: second Submit err=%v claims=%d submissions=%d alias_ids=%v", secondErr, claims, submissionsAfter, ids)
	}
	for _, id := range []customerdomain.CustomerID{loserID, survivorID} {
		status, err := submissions.PublicSubmissionStatus(ctx, "merge-claim-evidence", surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &id, EvidenceDigest: strings.Repeat("a", 64)})
		if err != nil || !status.Submitted {
			t.Fatalf("merged root/old session status must see original claim: submitted=%t error=%v", status.Submitted, err)
		}
	}
	if err = submit(loserID, "a", "before merge"); err != nil {
		t.Fatalf("original committed receipt after merge must replay: %v", err)
	}

	// A still-valid old session and a new canonical session must contend on
	// one current-root PK even when neither lineage member has participated.
	definitions := surveyapp.NewService(uow, repository)
	freshDefinition := publicSurveyPresentationDefinition("merge-claim-concurrent", surveyport.DisplayAllInOne, "Merge claim concurrent", "")
	freshDefinition.Questions = []surveyport.Question{{Type: surveyport.QuestionMobile, Title: "手机号", Required: true, Options: []surveyport.Option{}}}
	fresh, err := definitions.Create(ctx, surveyport.CreateCommand{Questionnaire: freshDefinition, ActorID: actorID, IdempotencyKey: "merge-concurrent-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindCanonicalLineage(&surveyClaimLineageBarrier{PostgreSQL: identityquery.NewPostgreSQL(), ready: make(chan struct{})}); err != nil {
		t.Fatal(err)
	}
	fresh, err = definitions.Publish(ctx, fresh.ID, fresh.Version, actorID, "merge-concurrent-publish-0001")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for index, id := range []customerdomain.CustomerID{loserID, survivorID} {
		wg.Add(1)
		go func(index int, id customerdomain.CustomerID) {
			defer wg.Done()
			<-start
			_, e := submissions.Submit(ctx, surveyport.SubmitCommand{Slug: fresh.Slug, DefinitionVersion: fresh.DefinitionVersion, SubmissionKey: strings.Repeat([]string{"c", "d"}[index], 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &id, EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: fresh.Questions[0].ID, TextValue: "13800000000"}}})
			results <- e
		}(index, id)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for e := range results {
		if e == nil {
			accepted++
		} else if errors.Is(e, surveyport.ErrAlreadySubmitted) {
			rejected++
		} else {
			t.Fatalf("concurrent old/new sessions: %v", e)
		}
	}
	var owner int64
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*),min(customer_id) FROM survey_submissions WHERE questionnaire_id=$1`, fresh.ID).Scan(&count, &owner); err != nil || count != 1 || owner != int64(survivorID) || accepted != 1 || rejected != 1 {
		t.Fatalf("old/new sessions must create one canonical submission: rows=%d owner=%d accepted=%d rejected=%d error=%v", count, owner, accepted, rejected, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*),min(customer_id) FROM customer_identities WHERE kind='phone' AND status='active' AND customer_id=ANY($1::bigint[])`, ids).Scan(&count, &owner); err != nil || count != 1 || owner != int64(survivorID) {
		t.Fatalf("collected phone must belong to current root: rows=%d owner=%d error=%v", count, owner, err)
	}
	if err = native.QueryRow(ctx, `SELECT customer_id FROM survey_submission_claims WHERE questionnaire_id=$1`, questionnaireID).Scan(&owner); err != nil || owner != int64(loserID) {
		t.Fatalf("historical claim must retain original identity: owner=%d error=%v", owner, err)
	}
	var outsiderID customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		outsider, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, evidenceMergeFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:survey-merge-claim", "outsider"))
		outsiderID = outsider.CustomerID
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = submit(outsiderID, "e", "unrelated customer"); err != nil {
		t.Fatal(err)
	}
	// Equal timestamps must still paginate globally by submission ID, rather
	// than concatenate per-alias pages. Other customers must remain excluded.
	stamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if _, err = native.Exec(ctx, `UPDATE survey_submissions SET submitted_at=$1 WHERE customer_id=ANY($2::bigint[])`, stamp, ids); err != nil {
		t.Fatal(err)
	}
	profile := customerSurveyAdapter{reader: submissions}
	history, err := profile.CustomerSurveys(ctx, survivorID, customerport.PageQuery{Limit: 1, Watermark: stamp})
	if err != nil || history.Total != 2 || len(history.Items) != 1 {
		t.Fatalf("canonical profile must include both lineage facts: total=%d rows=%d error=%v", history.Total, len(history.Items), err)
	}
	first := history.Items[0]
	second, err := profile.CustomerSurveys(ctx, survivorID, customerport.PageQuery{Limit: 1, Watermark: stamp, AfterAt: first.SubmittedAt, AfterID: first.ID})
	if err != nil || second.Total != 2 || len(second.Items) != 1 || second.Items[0].ID >= first.ID {
		t.Fatalf("lineage cursor must return older distinct fact: second=%+v error=%v", second, err)
	}
	last := second.Items[0]
	end, err := profile.CustomerSurveys(ctx, loserID, customerport.PageQuery{Limit: 1, Watermark: stamp, AfterAt: last.SubmittedAt, AfterID: last.ID})
	if err != nil || end.Total != 2 || len(end.Items) != 0 {
		t.Fatalf("old alias cursor must end without repeats: page=%+v error=%v", end, err)
	}
	before, err := profile.CustomerSurveys(ctx, survivorID, customerport.PageQuery{Limit: 1, Watermark: stamp.Add(-time.Microsecond)})
	if err != nil || len(before.Items) != 0 {
		t.Fatalf("lineage history must respect frozen watermark: page=%+v error=%v", before, err)
	}
	for _, answer := range first.Answers {
		for _, value := range answer.Answers {
			if strings.Contains(value, "13800000000") {
				t.Fatal("profile leaked collected phone plaintext")
			}
		}
	}
	verifyMergedTagCommandGuard(t, ctx, native, uow, loserID, survivorID)
	t.Log("actual merge: historical claim retained; root and stale session status agree; old receipt replayed; concurrent old/new sessions accepted once under current root")
}

func verifyMergedTagCommandGuard(t *testing.T, ctx context.Context, native *pgxpool.Pool, uow *platformpostgres.UnitOfWork, alias, root customerdomain.CustomerID) {
	t.Helper()
	var groupID, tagID int64
	if err := native.QueryRow(ctx, `INSERT INTO tag_groups(group_name,sort_order) VALUES('merge fixture',0) RETURNING id`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES($1,'merge fixture tag',0) RETURNING id`, groupID).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('synthetic-merge-tag',$1)`, tagID); err != nil {
		t.Fatal(err)
	}
	tags, err := tagstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	// Actual production gate and OneID/tag reads; only queue insertion, no
	// worker starts and no provider exists in this fixture.
	gate := customerTagCommandGate{uow: uow, corpID: "survey-merge-claim", tags: tags, identities: identityquery.NewPostgreSQL()}
	service, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, gate, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindCanonicalLineages(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	command := customerport.TagCommand{Source: "product_paid_purchase", SourceRef: "synthetic-root-order", IdempotencyKey: "synthetic-root-order", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: root, AddTagIDs: []int64{tagID}}}}
	first, err := service.SubmitTagCommand(ctx, command)
	if err != nil || len(first.Lines) != 1 || first.Lines[0].State != "queued" {
		t.Fatalf("root tag acceptance: result=%+v error=%v", first, err)
	}
	command.SourceRef, command.IdempotencyKey = "synthetic-old-order", "synthetic-old-order"
	command.Targets[0].CustomerID = alias
	old, err := gate.FreezeTagCommandTarget(ctx, command.Source, command.Targets[0])
	if err != nil || old.TargetDigest != first.Lines[0].TargetDigest || old.BindingDigest != first.Lines[0].BindingDigest {
		t.Fatalf("alias must target the same actual frozen identity/binding: error=%v", err)
	}
	_, err = service.SubmitTagCommand(ctx, command)
	var count int
	if e := native.QueryRow(ctx, `SELECT count(*) FROM customer_tag_command_lines`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(err, customerport.ErrTagCommandConflict) || count != 1 {
		t.Fatalf("old order alias bypassed pending root guard: error=%v lines=%d", err, count)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		// Local completion evidence only; no claim of a provider outcome.
		return customerstore.TagCommandPostgreSQL{}.CompleteTagCommand(tx, customerport.TagCommandCompletion{EffectRef: first.Lines[0].EffectRef, State: "outcome_unknown", ResultDigest: string(effectport.Hash("synthetic-unknown")), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.SubmitTagCommand(ctx, command)
	if !errors.Is(err, customerport.ErrTagCommandConflict) {
		t.Fatalf("unknown root outcome must block old alias: %v", err)
	}
	command.SourceRef, command.IdempotencyKey = "synthetic-root-order", "synthetic-root-order"
	command.Targets[0].CustomerID = root
	replay, err := service.SubmitTagCommand(ctx, command)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("original tag key must replay despite pending/unknown: error=%v", err)
	}
	command.Targets[0].AddTagIDs = nil
	command.Targets[0].RemoveTagIDs = []int64{tagID}
	if _, err = service.SubmitTagCommand(ctx, command); !errors.Is(err, customerport.ErrTagCommandConflict) {
		t.Fatalf("changed payload under original key must conflict: %v", err)
	}
	for _, table := range []string{"customer_tag_commands", "customer_tag_command_lines", "external_effects", "river_job"} {
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("rejected aliases/replays must not add %s: rows=%d error=%v", table, count, err)
		}
	}
}

func evidenceMergeFact(t *testing.T, kind identitydomain.Kind, scope, value string) identitydomain.VerifiedFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: kind, Scope: scope, Value: value, Source: "survey.merge.claim.evidence"})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}
