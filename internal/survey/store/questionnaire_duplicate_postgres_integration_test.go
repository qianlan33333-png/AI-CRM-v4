package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

func TestPostgreSQLQuestionnaireCopiesKeepOldPublicLinkAndIndependentSlugs(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	var actorID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-copy-slug-test','$argon2id$test','Survey Copy Test') RETURNING id`).Scan(&actorID); err != nil {
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
	oldSlug := strings.Repeat("copy-chain-", 11) + "origin"
	original, err := definitions.Create(ctx, surveyport.CreateCommand{ActorID: actorID, IdempotencyKey: "survey-copy-original-create", Questionnaire: surveyport.Questionnaire{Name: "旧问卷", Title: "旧问卷", Slug: oldSlug, Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Status: surveyport.StatusDraft, AssessmentConfig: json.RawMessage(`{}`), Questions: []surveyport.Question{{Type: surveyport.QuestionTextarea, Title: "需求", Required: true, SortOrder: 0, Options: []surveyport.Option{}}}}})
	if err != nil {
		t.Fatal(err)
	}
	original, err = definitions.Publish(ctx, original.ID, original.Version, actorID, "survey-copy-original-publish")
	if err != nil {
		t.Fatal(err)
	}
	prior := original
	seen := map[string]bool{oldSlug: true}
	for i := range 3 {
		if i == 1 {
			prior = original
		} // A distinct command may copy the same source again.
		key := fmt.Sprintf("survey-copy-independent-%04d", i)
		copy, err := definitions.Duplicate(ctx, prior.ID, actorID, key)
		if err != nil {
			t.Fatal(err)
		}
		if copy.ID == prior.ID || len(copy.Slug) != 34 || !strings.HasPrefix(copy.Slug, "q-") || seen[copy.Slug] || copy.Status != surveyport.StatusDraft || len(copy.Questions) != 1 || copy.Questions[0].Title != "需求" || copy.Questions[0].ID == prior.Questions[0].ID {
			t.Fatalf("invalid copied questionnaire: %+v", copy)
		}
		replay, err := definitions.Duplicate(ctx, prior.ID, actorID, key)
		if err != nil || replay.ID != copy.ID || replay.Slug != copy.Slug {
			t.Fatalf("replay=%+v err=%v", replay, err)
		}
		savedPrior, err := definitions.Get(ctx, prior.ID)
		if err != nil || savedPrior.Slug != prior.Slug || savedPrior.Version != prior.Version || savedPrior.Questions[0].ID != prior.Questions[0].ID {
			t.Fatalf("source changed: %+v err=%v", savedPrior, err)
		}
		copy, err = definitions.Publish(ctx, copy.ID, copy.Version, actorID, fmt.Sprintf("survey-copy-publish-%04d", i))
		if err != nil {
			t.Fatal(err)
		}
		published, err := submissions.ReadPublic(ctx, copy.Slug)
		if err != nil || published.ID != copy.ID || len(published.Questions) != 1 {
			t.Fatalf("new link read=%+v err=%v", published, err)
		}
		seen[copy.Slug] = true
		prior = copy
	}
	old, err := submissions.ReadPublic(ctx, oldSlug)
	if err != nil || old.ID != original.ID || old.Slug != oldSlug || old.DefinitionVersion != original.DefinitionVersion {
		t.Fatalf("old public link changed: %+v err=%v", old, err)
	}
	for table, want := range map[string]int{"survey_questionnaires": 4, "survey_operation_receipts": 8, "survey_audit_events": 8, "survey_outbox": 8} {
		var count int
		if err := native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s rows=%d want=%d; replay must not create additional state", table, count, want)
		}
	}
}
