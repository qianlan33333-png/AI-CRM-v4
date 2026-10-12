package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
)

const surveyEditorProductionPayload = `{"name":"测试新能力","title":"123新能力","description":"123","answer_display_mode":"one_by_one","assessment_enabled":false,"assessment_config":{},"slug":"","is_disabled":false,"questions":[{"type":"shipping_address","title":"收货人","sidebar_profile_field":"","required":false,"sort_order":0,"placeholder_text":""},{"type":"redemption_code","title":"兑换码","sidebar_profile_field":"","required":true,"sort_order":1,"placeholder_text":""},{"type":"attachment","title":"图片","sidebar_profile_field":"","required":true,"sort_order":2,"placeholder_text":""}],"score_rules":[]}`

func TestPostgreSQLSurveyEditorCreatesUnassignedSlugDefinitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	actor := surveyJourneyGovernedActor(t, ctx, pool, "editor-create", "Editor Create")
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := surveysecure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := surveystore.NewPostgreSQL(pool, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	definitions := surveyapp.NewService(uow, repo)
	submissions := surveyapp.NewSubmissionService(uow, repo, cipher)
	handler, err := surveyhttp.NewHandler(definitions, submissions, surveyJourneySecurity{actorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err = json.Unmarshal([]byte(surveyEditorProductionPayload), &original); err != nil {
		t.Fatal(err)
	}
	options := []any{map[string]any{"option_text": "A", "score": 0, "tag_codes": []any{}, "is_other": false, "other_placeholder": "", "other_max_length": 80, "sort_order": 0}, map[string]any{"option_text": "B", "score": 0, "tag_codes": []any{}, "is_other": false, "other_placeholder": "", "other_max_length": 80, "sort_order": 1}}
	cases := []struct {
		name      string
		questions []any
	}{
		{"production mixed", original["questions"].([]any)},
		{"shipping only", []any{map[string]any{"type": "shipping_address", "title": "收货人", "required": false, "sort_order": 0, "sidebar_profile_field": "", "placeholder_text": ""}}},
		{"code only", []any{map[string]any{"type": "redemption_code", "title": "兑换码", "required": true, "sort_order": 0, "sidebar_profile_field": "", "placeholder_text": ""}}},
		{"attachment only", []any{map[string]any{"type": "attachment", "title": "图片", "required": true, "sort_order": 0, "sidebar_profile_field": "", "placeholder_text": ""}}},
		{"single choice", []any{map[string]any{"type": "single_choice", "title": "单选", "required": true, "sort_order": 0, "sidebar_profile_field": "", "options": options}}},
		{"multi choice", []any{map[string]any{"type": "multi_choice", "title": "多选", "required": true, "sort_order": 0, "sidebar_profile_field": "", "options": options}}},
		{"textarea", []any{map[string]any{"type": "textarea", "title": "文本", "required": true, "sort_order": 0, "sidebar_profile_field": "", "placeholder_text": ""}}},
		{"mobile", []any{map[string]any{"type": "mobile", "title": "手机号", "required": true, "sort_order": 0, "sidebar_profile_field": "", "placeholder_text": ""}}},
	}
	seen := map[string]bool{}
	for index, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			body := map[string]any{}
			for key, value := range original {
				body[key] = value
			}
			body["questions"] = scenario.questions
			raw, _ := json.Marshal(body)
			if index == 0 {
				raw = []byte(surveyEditorProductionPayload)
			}
			key := fmt.Sprintf("editor-create-production-shape-%02d", index)
			create := func(payload []byte) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodPost, "/api/admin/questionnaires", strings.NewReader(string(payload)))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", key)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, request)
				return w
			}
			w := create(raw)
			if w.Code != 200 {
				t.Fatalf("real production-shaped create=%d %s", w.Code, w.Body.String())
			}
			var response struct {
				Questionnaire struct {
					ID   surveyport.ID `json:"id"`
					Slug string        `json:"slug"`
				} `json:"questionnaire"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			q, err := definitions.Get(ctx, response.Questionnaire.ID)
			if err != nil || q.ID < 1 || len(q.Slug) != 34 || !strings.HasPrefix(q.Slug, "q-") || seen[q.Slug] || len(q.Questions) != len(scenario.questions) {
				t.Fatalf("saved definition=%+v error=%v", q, err)
			}
			seen[q.Slug] = true
			replay := create(raw)
			if replay.Code != 200 || replay.Body.String() != w.Body.String() {
				t.Fatalf("original request replay changed generated identifier: %d %s", replay.Code, replay.Body.String())
			}
			for i, question := range q.Questions {
				if question.ID < 1 || question.SortOrder != i {
					t.Fatal("definition fields were not persisted")
				}
			}
			published, err := definitions.Publish(ctx, q.ID, q.Version, actor, key+"-publish")
			if err != nil || published.Status != surveyport.StatusPublished {
				t.Fatalf("publish=%+v error=%v", published, err)
			}
			reopened, err := definitions.Get(ctx, q.ID)
			if err != nil || reopened.Slug != q.Slug || reopened.DefinitionVersion != published.DefinitionVersion {
				t.Fatalf("reopened definition %+v error=%v", reopened, err)
			}
			body["title"] = "changed same-key content"
			changed, _ := json.Marshal(body)
			if conflict := create(changed); conflict.Code != 409 {
				t.Fatalf("changed content replay=%d %s", conflict.Code, conflict.Body.String())
			}
		})
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM survey_questionnaires`).Scan(&count); err != nil || count != len(cases) {
		t.Fatalf("replay left extra definitions count=%d error=%v", count, err)
	}
	// Invalid real-shaped input must leave neither a definition nor an operation receipt.
	original["questions"] = []any{map[string]any{"type": "attachment", "title": "图片", "required": true, "sort_order": 0, "sidebar_profile_field": "contact_phone"}}
	invalid, _ := json.Marshal(original)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/questionnaires", strings.NewReader(string(invalid)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "editor-invalid-profile-map")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 400 {
		t.Fatalf("invalid mapping accepted: %d", w.Code)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM survey_questionnaires`).Scan(&count); err != nil || count != len(cases) {
		t.Fatal("failed creation left partial definition")
	}
	var receipts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM survey_operation_receipts`).Scan(&receipts); err != nil || receipts != len(cases)*2 {
		t.Fatalf("invalid mapping reserved a receipt count=%d error=%v", receipts, err)
	}
	// Fail after the real definition, receipt, audit and outbox have been written
	// in the owned UoW. No generated identifier or partial record may survive.
	counts := func() [5]int {
		var result [5]int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM survey_questionnaires),(SELECT count(*) FROM survey_definition_versions),(SELECT count(*) FROM survey_operation_receipts),(SELECT count(*) FROM survey_audit_events),(SELECT count(*) FROM survey_outbox)`).Scan(&result[0], &result[1], &result[2], &result[3], &result[4]); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := counts()
	definition := surveyport.Questionnaire{Name: "事务失败后重试", Title: "事务失败后重试", Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{{Type: surveyport.QuestionAttachment, Title: "图片", Required: true}}}
	command := surveyport.CreateCommand{Questionnaire: definition, ActorID: actor, IdempotencyKey: "editor-create-after-write-failure"}
	failing := surveyapp.NewService(uow, editorCreateAuditFailure{Store: repo})
	if _, err = failing.Create(ctx, command); err == nil || counts() != before {
		t.Fatalf("mid-transaction failure left partial records: before=%v after=%v error=%v", before, counts(), err)
	}
	created, err := definitions.Create(ctx, command)
	if err != nil || !strings.HasPrefix(created.Slug, "q-") {
		t.Fatalf("same-key retry after rollback could not create: %+v error=%v", created, err)
	}
}

type editorCreateAuditFailure struct{ surveyapp.Store }

func (s editorCreateAuditFailure) AppendAuditAndOutbox(ctx context.Context, event string, id surveyport.ID, actor string, payload json.RawMessage, key string, now time.Time) error {
	if err := s.Store.AppendAuditAndOutbox(ctx, event, id, actor, payload, key, now); err != nil {
		return err
	}
	return errors.New("injected failure after audit and outbox")
}
