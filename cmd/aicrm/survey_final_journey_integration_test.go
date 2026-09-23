package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveymodule "github.com/qianlan33333-png/AI-CRM-v3/internal/survey"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveyprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/provider"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
)

// TestSurveyOAuthJourneyDefinitionFixtureIsAcceptedByApplication makes the
// exact draft shape used by the PG/HTTP journey executable without a database.
// In particular a single-choice question must have two choices and an explicit
// maximum of one selection; this catches fixture drift before the PG lane.
func TestSurveyOAuthJourneyDefinitionFixtureIsAcceptedByApplication(t *testing.T) {
	definition := surveyJourneyDefinition("oauth-journey", surveyport.DisplayAllInOne)
	if err := surveydomain.ValidateQuestionnaire(definition); err != nil {
		t.Fatalf("journey fixture violates survey validation: %v", err)
	}
	store := &surveyJourneyDefinitionStore{}
	created, err := surveyapp.NewService(surveyJourneyUnitOfWork{}, store).Create(context.Background(), surveyport.CreateCommand{Questionnaire: definition, ActorID: 1, IdempotencyKey: "survey-oauth-fixture-create-0001"})
	if err != nil || created.ID != 1 || len(store.created.Questions) != 1 || len(store.created.Questions[0].Options) != 2 {
		t.Fatalf("application create=%+v stored=%+v err=%v", created, store.created, err)
	}
}

// TestSurveyOAuthRedirectConstraintReadinessPostgreSQL proves the 0018
// regular-expression defect is fail-closed and that the real 0090 migration
// restores both Host callback paths without widening the redirect boundary.
func TestSurveyOAuthRedirectConstraintReadinessPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()

	// This is the exact 0018 definition. In standard-conforming PostgreSQL
	// literals its doubled backslashes reject the legitimate Host redirect.
	if _, err = native.Exec(ctx, `ALTER TABLE survey_oauth_states DROP CONSTRAINT survey_oauth_states_redirect;
		ALTER TABLE survey_oauth_states ADD CONSTRAINT survey_oauth_states_redirect
		CHECK (redirect_path ~ '^/h5/(all|one)\\.html\\?slug=[a-z0-9][a-z0-9-]{0,127}$');`); err != nil {
		t.Fatal(err)
	}
	module := surveymodule.NewModuleRegistration()
	if err = module.Readiness(ctx, native); err == nil {
		t.Fatal("Readiness accepted the known-broken 0018 OAuth redirect constraint")
	}
	surveyJourneyAssertOAuthRedirect(t, ctx, native, 1, "/h5/all.html?slug=oauth-ready", false)

	if _, err = native.Exec(ctx, surveyJourneyMigrationSQL(t, "0090_survey_oauth_state_redirect.sql")); err != nil {
		t.Fatalf("apply 0090: %v", err)
	}
	if err = module.Readiness(ctx, native); err != nil {
		t.Fatalf("Readiness after 0090: %v", err)
	}
	surveyJourneyAssertOAuthRedirect(t, ctx, native, 2, "/h5/all.html?slug=oauth-ready", true)
	surveyJourneyAssertOAuthRedirect(t, ctx, native, 3, "/h5/one.html?slug=oauth-ready-one", true)
	surveyJourneyAssertOAuthRedirect(t, ctx, native, 4, "https://outside.invalid/h5/all.html?slug=oauth-ready", false)
	surveyJourneyAssertOAuthRedirect(t, ctx, native, 5, "/h5/all.html?slug=not_valid", false)
}

// TestSurveyAssessmentBusinessKeyConstraintReadinessPostgreSQL makes the
// forward-only 0091 repair executable. The old 0018 ASCII checks must fail
// readiness and reject a real legacy key; after 0091, the same PostgreSQL
// owner accepts Chinese/internal-space/slash keys while keeping boundary
// violations out.
func TestSurveyAssessmentBusinessKeyConstraintReadinessPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()

	if _, err = native.Exec(ctx, `ALTER TABLE survey_definition_questions DROP CONSTRAINT survey_definition_questions_dimension;
		ALTER TABLE survey_definition_questions ADD CONSTRAINT survey_definition_questions_dimension
		CHECK (assessment_dimension_key = '' OR assessment_dimension_key ~ '^[A-Za-z0-9._:-]{1,128}$');
		ALTER TABLE survey_definition_options DROP CONSTRAINT survey_definition_options_type;
		ALTER TABLE survey_definition_options ADD CONSTRAINT survey_definition_options_type
		CHECK (assessment_type_key = '' OR assessment_type_key ~ '^[A-Za-z0-9._:-]{1,128}$');`); err != nil {
		t.Fatal(err)
	}
	module := surveymodule.NewModuleRegistration()
	if err = module.Readiness(ctx, native); err == nil {
		t.Fatal("Readiness accepted the known-broken 0018 assessment business key constraints")
	}
	fixture := newSurveyJourneyAssessmentKeyFixture(t, ctx, native)
	surveyJourneyAssertAssessmentQuestionKey(t, ctx, native, fixture.versionID, 0, "维度 1/增长", false)

	if _, err = native.Exec(ctx, surveyJourneyMigrationSQL(t, "0091_survey_assessment_business_keys.sql")); err != nil {
		t.Fatalf("apply 0091: %v", err)
	}
	if err = module.Readiness(ctx, native); err != nil {
		t.Fatalf("Readiness after 0091: %v", err)
	}
	surveyJourneyAssertAssessmentQuestionKey(t, ctx, native, fixture.versionID, 0, "维度 1/增长", true)
	surveyJourneyAssertAssessmentOptionKey(t, ctx, native, fixture.questionID, fixture.versionID, 0, "暖男/女型", true)
	for index, key := range []string{" key", "key ", "key\nline", strings.Repeat("字", 129)} {
		surveyJourneyAssertAssessmentQuestionKey(t, ctx, native, fixture.versionID, index+1, key, false)
		surveyJourneyAssertAssessmentOptionKey(t, ctx, native, fixture.questionID, fixture.versionID, index+1, key, false)
	}
}

type surveyJourneyAssessmentKeyRows struct{ versionID, questionID int64 }

func surveyJourneyGovernedActor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username, displayName string) int64 {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var actorID int64
	if err = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,login_enabled,access_granted_at) VALUES($1,'$argon2id$test',$2,true,true,clock_timestamp()) RETURNING id`, username, displayName).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES($1,'super_admin')`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO access_super_admin_control(singleton,admin_user_id,version,updated_at) VALUES(TRUE,$1,1,clock_timestamp())`, actorID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return actorID
}

func newSurveyJourneyAssessmentKeyFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) surveyJourneyAssessmentKeyRows {
	t.Helper()
	now := time.Now().UTC()
	var actorID, questionnaireID, versionID, questionID int64
	actorID = surveyJourneyGovernedActor(t, ctx, pool, "survey-key-ready", "Survey Key Ready")
	if err := pool.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('assessment-key-ready','Assessment key ready','','assessment','all_in_one','assessment-key-ready','draft',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	digest := bytes.Repeat([]byte{9}, sha256.Size)
	if err := pool.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,created_by,created_at) VALUES($1,1,'assessment','all_in_one','Assessment key ready','', '{}'::jsonb,$2,$3,$4) RETURNING id`, questionnaireID, digest, actorID, now).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$2 WHERE id=$1`, questionnaireID, versionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,assessment_dimension_key,sidebar_profile_field,required,sort_order,placeholder_text,validation) VALUES($1,'single_choice','Key question','','',TRUE,99,'','{}'::jsonb) RETURNING id`, versionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	return surveyJourneyAssessmentKeyRows{versionID: versionID, questionID: questionID}
}

func surveyJourneyAssertAssessmentQuestionKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, versionID int64, sortOrder int, key string, accepted bool) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,assessment_dimension_key,sidebar_profile_field,required,sort_order,placeholder_text,validation) VALUES($1,'textarea','Key boundary',$2,'',FALSE,$3,'','{}'::jsonb)`, versionID, key, sortOrder)
	if accepted && err != nil {
		t.Fatalf("assessment dimension key %q rejected: %v", key, err)
	}
	if !accepted && err == nil {
		t.Fatalf("assessment dimension key %q unexpectedly accepted", key)
	}
}

func surveyJourneyAssertAssessmentOptionKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID, versionID int64, sortOrder int, key string, accepted bool) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,score,assessment_type_key,tag_codes,is_other,other_placeholder,other_max_length,sort_order) VALUES($1,$2,'Key boundary',0,$3,'[]'::jsonb,FALSE,'',0,$4)`, questionID, versionID, key, sortOrder)
	if accepted && err != nil {
		t.Fatalf("assessment type key %q rejected: %v", key, err)
	}
	if !accepted && err == nil {
		t.Fatalf("assessment type key %q unexpectedly accepted", key)
	}
}

func surveyJourneyMigrationSQL(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate survey journey migration")
	}
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(sql)
}

func surveyJourneyAssertOAuthRedirect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, marker byte, redirect string, accepted bool) {
	t.Helper()
	digest := bytes.Repeat([]byte{marker}, sha256.Size)
	now := time.Now().UTC()
	_, err := pool.Exec(ctx, `INSERT INTO survey_oauth_states(state_digest,questionnaire_slug,redirect_path,expires_at,created_at) VALUES($1,$2,$3,$4,$5)`, digest, "oauth-ready", redirect, now.Add(time.Minute), now)
	if accepted && err != nil {
		t.Fatalf("redirect %q rejected: %v", redirect, err)
	}
	if !accepted && err == nil {
		t.Fatalf("redirect %q unexpectedly accepted", redirect)
	}
}

func surveyJourneyDefinition(slug string, mode surveyport.AnswerDisplayMode) surveyport.Questionnaire {
	maximum := 1
	return surveyport.Questionnaire{Name: "OAuth journey", Title: "OAuth journey", Description: "", Mode: surveyport.ModeSurvey, AnswerDisplayMode: mode, AssessmentConfig: json.RawMessage(`{}`), Slug: slug, Status: surveyport.StatusDraft,
		Questions: []surveyport.Question{{Type: surveyport.QuestionSingleChoice, Title: "Continue?", Required: true, SortOrder: 0, Validation: surveyport.Validation{MaximumSelections: &maximum}, Options: []surveyport.Option{{Text: "Yes", TagCodes: []string{}, SortOrder: 0}, {Text: "No", TagCodes: []string{}, SortOrder: 1}}}}}
}

func surveyJourneyPublicKey(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

type surveyJourneyUnitOfWork struct{}

func (surveyJourneyUnitOfWork) Within(ctx context.Context, work func(context.Context) error) error {
	return work(ctx)
}

type surveyJourneyDefinitionStore struct {
	surveyapp.Store
	created surveyport.Questionnaire
}

func (s *surveyJourneyDefinitionStore) Reserve(_ context.Context, value surveyapp.Reservation) (surveyapp.Receipt, bool, error) {
	return surveyapp.Receipt{ID: 1, Operation: value.Operation, ActorScope: value.ActorScope, KeyDigest: value.KeyDigest, PayloadDigest: value.PayloadDigest, State: "in_progress"}, true, nil
}
func (s *surveyJourneyDefinitionStore) Create(_ context.Context, value surveyport.Questionnaire, _ int64, _ time.Time) (surveyport.Questionnaire, error) {
	value.ID = 1
	s.created = value
	return value, nil
}
func (*surveyJourneyDefinitionStore) AppendAuditAndOutbox(context.Context, string, surveyport.ID, string, json.RawMessage, string, time.Time) error {
	return nil
}
func (*surveyJourneyDefinitionStore) Complete(_ context.Context, id int64, result json.RawMessage, _ time.Time) (surveyapp.Receipt, error) {
	return surveyapp.Receipt{ID: id, State: "completed", Result: result}, nil
}

// TestSurveyOAuthSubmissionResultJourneyPostgreSQL exercises the composition
// boundary with actual Survey and OneID PostgreSQL owners. The OAuth adapter
// itself is real; only the WeChat HTTPS exchange is strictly forwarded to this
// local httptest server, so the test cannot reach any other network endpoint.
func TestSurveyOAuthSubmissionResultJourneyPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()

	var actorID int64
	actorID = surveyJourneyGovernedActor(t, ctx, native, "survey-oauth-journey", "Survey OAuth Journey")
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	definitions := surveyapp.NewService(uow, repository)
	submissions := surveyapp.NewSubmissionService(uow, repository, cipher)
	projection := customerstore.NewPostgreSQL()
	if err = submissions.BindDeclaredPhone(oneID, projection); err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindCustomerTimeline(projection); err != nil {
		t.Fatal(err)
	}
	oauthProvider, err := surveyprovider.NewWeChatOAuth(true, "survey-journey-app", "survey-journey-secret", "survey-journey-platform", "https://journey.example.test/api/h5/surveys/oauth/callback", "snsapi_userinfo")
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := &surveyJourneyOAuthStore{OAuthStore: repository}
	oauth := surveyapp.NewOAuthService(uow, oauthStore, oauthProvider, oneID)
	handler, err := surveyhttp.NewHandler(definitions, submissions, surveyJourneySecurity{actorID: actorID}, oauth)
	if err != nil {
		t.Fatal(err)
	}

	createBody := []byte(`{"name":"OAuth journey","title":"OAuth journey","description":"","answer_display_mode":"all_in_one","assessment_enabled":false,"assessment_config":{},"slug":"oauth-journey","questions":[{"type":"single_choice","title":"Continue?","required":true,"sort_order":0,"validation":{"max_selections":1},"options":[{"option_text":"Yes","score":0,"tag_codes":[],"is_other":false,"sort_order":0},{"option_text":"No","score":0,"tag_codes":[],"is_other":false,"sort_order":1}]}],"score_rules":[]}`)
	created := surveyJourneyServe(t, handler, http.MethodPost, "/api/admin/questionnaires", createBody, "survey-oauth-create-0001", nil, "")
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var createResult struct {
		Questionnaire surveyport.Questionnaire `json:"questionnaire"`
	}
	mustSurveyJourneyJSON(t, created, &createResult)
	if createResult.Questionnaire.ID < 1 || len(createResult.Questionnaire.Questions) != 1 || len(createResult.Questionnaire.Questions[0].Options) != 2 {
		t.Fatalf("created questionnaire=%+v", createResult.Questionnaire)
	}

	published := surveyJourneyServe(t, handler, http.MethodPost, fmt.Sprintf("/api/admin/questionnaires/%d/public-publish", createResult.Questionnaire.ID), nil, "survey-oauth-publish-0001", nil, "")
	if published.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", published.Code, published.Body.String())
	}
	var publishResult struct {
		Questionnaire surveyport.Questionnaire `json:"questionnaire"`
	}
	mustSurveyJourneyJSON(t, published, &publishResult)
	// The frozen admin HTTP DTO uses the legacy-compatible `active` status;
	// verify the Owner's separate domain projection below rather than treating
	// that compatibility value as surveyport.StatusPublished.
	if publishResult.Questionnaire.Status != "active" || publishResult.Questionnaire.DefinitionVersion != 1 {
		t.Fatalf("published questionnaire=%+v", publishResult.Questionnaire)
	}
	ownerPublished, err := definitions.Get(ctx, createResult.Questionnaire.ID)
	if err != nil || ownerPublished.Status != surveyport.StatusPublished || ownerPublished.DefinitionVersion != 1 {
		t.Fatalf("Owner published=%+v err=%v", ownerPublished, err)
	}
	// OAuth Start reads through the same published-by-slug Store method in its
	// own Unit of Work. Keep that read explicit so a migration or projection
	// failure is reported as its underlying Owner error, rather than only as the
	// public 503 mapping used by the HTTP boundary.
	var oauthPublished surveyport.Questionnaire
	err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		oauthPublished, readErr = repository.GetPublishedBySlug(tx, "oauth-journey")
		return readErr
	})
	if err != nil || oauthPublished.ID != createResult.Questionnaire.ID || oauthPublished.Status != surveyport.StatusPublished {
		t.Fatalf("OAuth published-by-slug=%+v err=%v", oauthPublished, err)
	}

	start := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/start?slug=oauth-journey", nil, "", nil, "MicroMessenger Survey Journey")
	if start.Code != http.StatusSeeOther {
		t.Fatalf("OAuth start status=%d body=%s StoreError=%v", start.Code, start.Body.String(), oauthStore.lastError)
	}
	authorization := surveyFinalJourneyLocation(t, start)
	state := authorization.Query().Get("state")
	if authorization.Host != "open.weixin.qq.com" || state == "" || authorization.Query().Get("scope") != "snsapi_userinfo" {
		t.Fatalf("OAuth authorization=%s", authorization.String())
	}

	weChat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected OAuth method", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/sns/oauth2/access_token":
			if r.URL.Query().Get("grant_type") != "authorization_code" || (r.URL.Query().Get("code") != "journey-code" && r.URL.Query().Get("code") != "journey-code-one") {
				http.Error(w, "unexpected OAuth exchange", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"journey-userinfo-token","openid":"journey-openid","scope":"snsapi_userinfo"}`))
		case "/sns/userinfo":
			if r.URL.Query().Get("access_token") != "journey-userinfo-token" || r.URL.Query().Get("openid") != "journey-openid" {
				http.Error(w, "unexpected userinfo identity", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"openid":"journey-openid","unionid":"journey-unionid"}`))
		default:
			http.Error(w, "unexpected OAuth endpoint", http.StatusBadRequest)
		}
	}))
	defer weChat.Close()
	weChatURL, err := url.Parse(weChat.URL)
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	transport := &surveyOAuthAllowlistTransport{base: originalTransport, target: weChatURL}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	wrongState := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/callback?state="+url.QueryEscape("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")+"&code=journey-code", nil, "", nil, "")
	if wrongState.Code != http.StatusSeeOther || wrongState.Header().Get("Location") != "/h5/error.html?code=survey_oauth_failed" || transport.calls != 0 {
		t.Fatalf("wrong OAuth state status=%d location=%q calls=%d", wrongState.Code, wrongState.Header().Get("Location"), transport.calls)
	}
	callback := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/callback?state="+url.QueryEscape(state)+"&code=journey-code", nil, "", nil, "")
	if callback.Code != http.StatusSeeOther || callback.Header().Get("Location") != "/h5/all.html?slug=oauth-journey" || transport.calls != 2 {
		t.Fatalf("OAuth callback status=%d calls=%d body=%s", callback.Code, transport.calls, callback.Body.String())
	}
	cookies := callback.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-aicrm_survey_identity" || !cookies[0].HttpOnly {
		t.Fatalf("OAuth callback cookie=%+v", cookies)
	}
	replayedCallback := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/callback?state="+url.QueryEscape(state)+"&code=journey-code", nil, "", nil, "")
	if replayedCallback.Code != http.StatusSeeOther || replayedCallback.Header().Get("Location") != "/h5/error.html?code=survey_oauth_failed" || transport.calls != 2 {
		t.Fatalf("replayed OAuth callback status=%d location=%q calls=%d", replayedCallback.Code, replayedCallback.Header().Get("Location"), transport.calls)
	}

	question := publishResult.Questionnaire.Questions[0]
	submissionKey := surveyJourneyPublicKey("oauth-submission")
	answer := map[string]any{"version": publishResult.Questionnaire.DefinitionVersion, "submission_key": submissionKey, "answers": []map[string]any{{"question_id": question.ID, "option_ids": []surveyport.ID{question.Options[0].ID}}}}
	submitBody, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	concurrentSubmissions := surveyJourneyConcurrentSubmit(handler, "/api/public/questionnaires/oauth-journey/submissions", submitBody, cookies)
	for _, submitted := range concurrentSubmissions {
		if submitted.Code != http.StatusCreated {
			t.Fatalf("concurrent submit status=%d body=%s", submitted.Code, submitted.Body.String())
		}
	}
	submitted := concurrentSubmissions[0]
	var submissionResult struct {
		Receipt     surveyport.SubmissionReceipt `json:"receipt"`
		ResultToken string                       `json:"result_token"`
	}
	mustSurveyJourneyJSON(t, submitted, &submissionResult)
	if submissionResult.Receipt.SubmissionID < 1 || submissionResult.ResultToken == "" || submissionResult.Receipt.ResultToken != "" {
		t.Fatalf("submission receipt=%+v token=%q", submissionResult.Receipt, submissionResult.ResultToken)
	}
	var concurrentResult struct {
		Receipt surveyport.SubmissionReceipt `json:"receipt"`
	}
	mustSurveyJourneyJSON(t, concurrentSubmissions[1], &concurrentResult)
	if concurrentResult.Receipt.SubmissionID != submissionResult.Receipt.SubmissionID {
		t.Fatalf("concurrent receipts diverged first=%+v second=%+v", submissionResult.Receipt, concurrentResult.Receipt)
	}
	replayedSubmission := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/questionnaires/oauth-journey/submissions", submitBody, "", cookies, "")
	if replayedSubmission.Code != http.StatusCreated {
		t.Fatalf("replayed submit status=%d body=%s", replayedSubmission.Code, replayedSubmission.Body.String())
	}
	var replayedReceipt struct {
		Receipt surveyport.SubmissionReceipt `json:"receipt"`
	}
	mustSurveyJourneyJSON(t, replayedSubmission, &replayedReceipt)
	if replayedReceipt.Receipt.SubmissionID != submissionResult.Receipt.SubmissionID {
		t.Fatalf("replayed receipt=%+v original=%+v", replayedReceipt.Receipt, submissionResult.Receipt)
	}
	driftBody, err := json.Marshal(map[string]any{"version": publishResult.Questionnaire.DefinitionVersion, "submission_key": submissionKey, "answers": []map[string]any{{"question_id": question.ID, "option_ids": []surveyport.ID{question.Options[1].ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	driftedSubmission := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/questionnaires/oauth-journey/submissions", driftBody, "", cookies, "")
	if driftedSubmission.Code != http.StatusConflict {
		t.Fatalf("submission payload drift status=%d body=%s", driftedSubmission.Code, driftedSubmission.Body.String())
	}

	resultBody, err := json.Marshal(map[string]string{"result_token": submissionResult.ResultToken})
	if err != nil {
		t.Fatal(err)
	}
	result := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", resultBody, "", cookies, "")
	if result.Code != http.StatusOK {
		t.Fatalf("result status=%d body=%s", result.Code, result.Body.String())
	}
	var publicResult struct {
		SubmissionID surveyport.ID `json:"submission_id"`
	}
	mustSurveyJourneyJSON(t, result, &publicResult)
	if publicResult.SubmissionID != submissionResult.Receipt.SubmissionID {
		t.Fatalf("result submission=%d receipt=%d", publicResult.SubmissionID, submissionResult.Receipt.SubmissionID)
	}

	anonymous := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", resultBody, "", nil, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous result status=%d body=%s", anonymous.Code, anonymous.Body.String())
	}
	initialDefinitionVersion := ownerPublished.DefinitionVersion
	editedDefinition := ownerPublished
	editedDefinition.Title = "OAuth journey revised"
	edited, err := definitions.Update(ctx, surveyport.UpdateCommand{Questionnaire: editedDefinition, ExpectedVersion: ownerPublished.Version, ActorID: actorID, IdempotencyKey: "survey-oauth-edit-0001"})
	if err != nil || edited.Status != surveyport.StatusDraft || edited.DefinitionVersion <= initialDefinitionVersion {
		t.Fatalf("definition edit=%+v err=%v", edited, err)
	}
	if _, err = definitions.Publish(ctx, edited.ID, edited.Version, actorID, "survey-oauth-republish-0001"); err != nil {
		t.Fatal(err)
	}
	historical, err := submissions.GetSubmission(ctx, submissionResult.Receipt.SubmissionID)
	if err != nil || historical.DefinitionVersion != initialDefinitionVersion || historical.QuestionnaireTitle != "OAuth journey" || len(historical.Answers) != 1 || len(historical.Answers[0].SelectedOptions) != 1 || historical.Answers[0].SelectedOptions[0].OptionText != "Yes" {
		t.Fatalf("historical submission=%+v err=%v", historical, err)
	}
	analytics := surveyJourneyServe(t, handler, http.MethodGet, fmt.Sprintf("/api/admin/questionnaires/%d/results", publishResult.Questionnaire.ID), nil, "", nil, "")
	if analytics.Code != http.StatusOK {
		t.Fatalf("analytics status=%d body=%s", analytics.Code, analytics.Body.String())
	}
	var analyticsResult struct {
		Results struct {
			SubmissionCount int64 `json:"submission_count"`
		} `json:"results"`
	}
	mustSurveyJourneyJSON(t, analytics, &analyticsResult)
	if analyticsResult.Results.SubmissionCount != 1 {
		t.Fatalf("analytics=%+v", analyticsResult)
	}
	export := surveyJourneyServe(t, handler, http.MethodGet, fmt.Sprintf("/api/admin/questionnaires/%d/export", publishResult.Questionnaire.ID), nil, "survey-oauth-export-0001", nil, "")
	if export.Code != http.StatusOK || export.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("export status=%d content_type=%q body=%s", export.Code, export.Header().Get("Content-Type"), export.Body.String())
	}
	csvRows, err := csv.NewReader(bytes.NewReader(export.Body.Bytes())).ReadAll()
	if err != nil || len(csvRows) != 2 || len(csvRows[1]) != 5 || csvRows[1][0] != fmt.Sprint(submissionResult.Receipt.SubmissionID) || csvRows[1][2] != "已关联客户" || csvRows[1][3] == "" || csvRows[1][4] != "0" {
		t.Fatalf("export rows=%q err=%v", csvRows, err)
	}

	oneDefinition, err := definitions.Create(ctx, surveyport.CreateCommand{Questionnaire: surveyJourneyDefinition("oauth-journey-one", surveyport.DisplayOneByOne), ActorID: actorID, IdempotencyKey: "survey-oauth-one-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = definitions.Publish(ctx, oneDefinition.ID, oneDefinition.Version, actorID, "survey-oauth-one-publish-0001"); err != nil {
		t.Fatal(err)
	}
	oneOwner, err := definitions.Get(ctx, oneDefinition.ID)
	if err != nil || oneOwner.Status != surveyport.StatusPublished || len(oneOwner.Questions) != 1 || len(oneOwner.Questions[0].Options) != 2 {
		t.Fatalf("one-mode Owner=%+v err=%v", oneOwner, err)
	}
	oneStart := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/start?slug=oauth-journey-one", nil, "", nil, "MicroMessenger Survey Journey")
	if oneStart.Code != http.StatusSeeOther {
		t.Fatalf("one-mode OAuth start status=%d body=%s", oneStart.Code, oneStart.Body.String())
	}
	oneState := surveyFinalJourneyLocation(t, oneStart).Query().Get("state")
	oneCallback := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/callback?state="+url.QueryEscape(oneState)+"&code=journey-code-one", nil, "", nil, "")
	if oneCallback.Code != http.StatusSeeOther || oneCallback.Header().Get("Location") != "/h5/one.html?slug=oauth-journey-one" || transport.calls != 4 {
		t.Fatalf("one-mode callback status=%d location=%q calls=%d", oneCallback.Code, oneCallback.Header().Get("Location"), transport.calls)
	}
	oneCookies := oneCallback.Result().Cookies()
	if len(oneCookies) != 1 || oneCookies[0].Name != "__Host-aicrm_survey_identity" || !oneCookies[0].HttpOnly {
		t.Fatalf("one-mode cookie=%+v", oneCookies)
	}
	oneSubmitBody, err := json.Marshal(map[string]any{"version": oneOwner.DefinitionVersion, "submission_key": surveyJourneyPublicKey("oauth-one-submission"), "answers": []map[string]any{{"question_id": oneOwner.Questions[0].ID, "option_ids": []surveyport.ID{oneOwner.Questions[0].Options[0].ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	oneSubmitted := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/questionnaires/oauth-journey-one/submissions", oneSubmitBody, "", oneCookies, "")
	if oneSubmitted.Code != http.StatusCreated {
		t.Fatalf("one-mode submit status=%d body=%s", oneSubmitted.Code, oneSubmitted.Body.String())
	}
	var oneReceipt struct {
		Receipt     surveyport.SubmissionReceipt `json:"receipt"`
		ResultToken string                       `json:"result_token"`
	}
	mustSurveyJourneyJSON(t, oneSubmitted, &oneReceipt)
	oneResultBody, err := json.Marshal(map[string]string{"result_token": oneReceipt.ResultToken})
	if err != nil {
		t.Fatal(err)
	}
	oneResult := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", oneResultBody, "", oneCookies, "")
	if oneResult.Code != http.StatusOK {
		t.Fatalf("one-mode result status=%d body=%s", oneResult.Code, oneResult.Body.String())
	}

	var customers, identities, storedSubmissions, storedTokens, customerBoundSubmissions, exports int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customers),
		(SELECT count(*) FROM customer_identities WHERE assurance='verified'),
		(SELECT count(*) FROM survey_submissions),
		(SELECT count(*) FROM survey_result_tokens),
		(SELECT count(*) FROM survey_submissions WHERE customer_id=(SELECT id FROM customers)),
		(SELECT count(*) FROM survey_audit_events WHERE event_type='survey_export_requested')`).Scan(&customers, &identities, &storedSubmissions, &storedTokens, &customerBoundSubmissions, &exports); err != nil {
		t.Fatal(err)
	}
	var verifiedUserinfoUnionIDs int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM customer_identities WHERE assurance='verified' AND kind='unionid' AND scope_key='wechat-open-platform:survey-journey-platform' AND source='wechat.survey.oauth.userinfo'`).Scan(&verifiedUserinfoUnionIDs); err != nil || verifiedUserinfoUnionIDs != 1 {
		t.Fatalf("userinfo UnionID proof count=%d err=%v", verifiedUserinfoUnionIDs, err)
	}
	if customers != 1 || identities != 1 || storedSubmissions != 2 || storedTokens != 2 || customerBoundSubmissions != 2 || exports != 1 {
		t.Fatalf("customers=%d identities=%d submissions=%d result_tokens=%d customer_bound_submissions=%d exports=%d", customers, identities, storedSubmissions, storedTokens, customerBoundSubmissions, exports)
	}
}

// TestSurveyFrozenAdminRuntimeJourneyPostgreSQL executes the emitted frozen
// editor through Survey.UIBinding and the actual v3 web shell. Its browser
// clicks create, preview, save-disabled, duplicate, export, assessment sort,
// and H5 preview; the assertions below then read the authoritative Survey
// Owner rather than treating browser requests or a 200 response as proof.
func TestSurveyFrozenAdminRuntimeJourneyPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()

	var actorID int64
	actorID = surveyJourneyGovernedActor(t, ctx, native, "survey-frozen-admin-runtime", "Survey Frozen Runtime")
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
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
	definitions := surveyapp.NewService(uow, repository)
	submissions := surveyapp.NewSubmissionService(uow, repository, cipher)
	handler, err := surveyhttp.NewHandler(definitions, submissions, surveyJourneySecurity{actorID: actorID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := surveyJourneyRepositoryRoot(t)
	renderer, err := webshell.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	dist := surveyJourneyBuiltDist(t, root)
	ui := surveymodule.NewModuleRegistration().UIBinding(dist, func(writer http.ResponseWriter, request *http.Request, page, donor string, assets surveymodule.UIAssets) error {
		return renderer.RenderSurvey(writer, webshell.AdminPageForRequest(request, "问卷编辑", "管理问卷定义、版本、答卷及只读外部效果回执。", "api.admin_questionnaires"), page, donor, webshell.SurveyAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, EditorJS: assets.EditorJS, EditorCSS: assets.EditorCSS, StandardHostJS: assets.StandardHostJS, SurveyHostJS: assets.SurveyHostJS, OperationsHostJS: assets.OperationsHostJS, OperationsCSS: assets.OperationsCSS})
	})
	mux := http.NewServeMux()
	mux.Handle("/admin/questionnaires", ui)
	mux.Handle("/admin/questionnaires.html", ui)
	mux.Handle("/admin/questionnaireDetail.html", ui)
	mux.Handle("/api/admin/questionnaires", handler)
	mux.Handle("/api/admin/questionnaires/", handler)
	publicUI := surveymodule.NewModuleRegistration().PublicUIBinding(dist)
	mux.Handle("/h5/", publicUI)
	mux.Handle("/survey-assets/", publicUI)
	server := httptest.NewServer(mux)
	defer server.Close()

	command := exec.CommandContext(ctx, "node", filepath.Join(root, "cmd", "aicrm", "survey_admin_frozen_runtime_e2e.mjs"))
	command.Dir = root
	command.Env = append(os.Environ(), "AICRM_SURVEY_ADMIN_RUNTIME_ORIGIN="+server.URL)
	var output, diagnostics bytes.Buffer
	command.Stdout = &output
	command.Stderr = &diagnostics
	err = command.Run()
	if err != nil {
		t.Fatalf("frozen Survey Host runtime: %v\n%s", err, diagnostics.String())
	}
	var result struct {
		NormalID, CopyID  int64
		AssessmentRetired bool
	}
	if err = json.Unmarshal([]byte(strings.TrimSpace(output.String())), &result); err != nil {
		t.Fatalf("decode frozen runtime output %q: %v", output.String(), err)
	}
	if result.NormalID < 1 || result.CopyID < 1 || !result.AssessmentRetired {
		t.Fatalf("invalid frozen runtime result=%+v", result)
	}
	normal, err := definitions.Get(ctx, surveyport.ID(result.NormalID))
	if err != nil || normal.Status != surveyport.StatusPublished || normal.Name != "冻结后台实际问卷" || len(normal.Questions) != 2 || normal.Questions[0].Title != "第一道真实题" || normal.Questions[1].Title != "第二道真实题" {
		t.Fatalf("frozen normal editor persistence=%+v err=%v", normal, err)
	}
	copy, err := definitions.Get(ctx, surveyport.ID(result.CopyID))
	if err != nil || copy.Status != surveyport.StatusDraft || copy.ID == normal.ID || len(copy.Questions) != len(normal.Questions) {
		t.Fatalf("frozen duplicate persistence=%+v err=%v", copy, err)
	}
	if shared, readErr := submissions.ReadPublic(ctx, normal.Slug); readErr != nil || shared.ID != normal.ID || shared.Status != surveyport.StatusPublished {
		t.Fatalf("ordinary published questionnaire read=%+v error=%v", shared, readErr)
	}

}

func surveyJourneyAssessmentHasType(dimension surveyport.AssessmentDimension, key string) bool {
	for _, assessmentType := range dimension.Types {
		if assessmentType.Key == key {
			return true
		}
	}
	return false
}

func surveyJourneyAssessmentResultHasType(result surveyport.AssessmentResult, dimensionKey, typeKey string) bool {
	for _, dimension := range result.Dimensions {
		if dimension.Key == dimensionKey {
			return dimension.DominantType != nil && dimension.DominantType.Key == typeKey
		}
	}
	return false
}

func surveyJourneyRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate survey journey repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// surveyJourneyBuiltDist uses the repository's normal frozen-frontend build
// only when this Go integration lane runs before the workflow's later web
// build. It never stages or edits the generated output: Survey.UIBinding still
// receives the resulting immutable assets exactly as the release assembly does.
func surveyJourneyBuiltDist(t *testing.T, root string) string {
	t.Helper()
	dist := filepath.Join(root, "web", "dist")
	if !surveyJourneyHasHostAssets(dist) {
		for _, command := range []*exec.Cmd{
			exec.Command("npm", "run", "build", "--silent"),
			exec.Command("node", "scripts/build-v3-host-adapters.mjs"),
		} {
			command.Dir = root
			output, buildErr := command.CombinedOutput()
			if buildErr != nil {
				t.Fatalf("build Survey Host assets for runtime journey: %v\n%s", buildErr, output)
			}
		}
	}
	if !surveyJourneyHasHostAssets(dist) {
		t.Fatal("Survey Host build did not produce its runtime asset closure")
	}
	return dist
}

func surveyJourneyHasHostAssets(dist string) bool {
	for _, required := range []string{"asset-manifest.json", filepath.Join("admin", "questionnaireDetail.html")} {
		if _, err := os.Stat(filepath.Join(dist, required)); err != nil {
			return false
		}
	}
	raw, err := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return false
	}
	host := manifest.Entries["surveyHost"]
	if !strings.HasPrefix(host, "assets/") || strings.Contains(host, "..") {
		return false
	}
	_, err = os.Stat(filepath.Join(dist, host))
	return err == nil
}

type surveyJourneySecurity struct{ actorID int64 }

// surveyJourneyOAuthStore only records the original Store error that the
// public OAuth service deliberately maps to a safe generic response. Every
// OAuth operation still delegates to the real PostgreSQL Owner.
type surveyJourneyOAuthStore struct {
	surveyapp.OAuthStore
	lastError error
}

func (s *surveyJourneyOAuthStore) GetPublishedBySlug(ctx context.Context, slug string) (surveyport.Questionnaire, error) {
	questionnaire, err := s.OAuthStore.GetPublishedBySlug(ctx, slug)
	if err != nil {
		s.lastError = fmt.Errorf("GetPublishedBySlug: %w", err)
	}
	return questionnaire, err
}

func (s *surveyJourneyOAuthStore) CreateOAuthState(ctx context.Context, digest [32]byte, state surveyapp.OAuthState, now time.Time) error {
	err := s.OAuthStore.CreateOAuthState(ctx, digest, state, now)
	if err != nil {
		s.lastError = fmt.Errorf("CreateOAuthState: %w", err)
	}
	return err
}

func (s surveyJourneySecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: s.actorID, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s surveyJourneySecurity) AuthorizeCSRF(ctx context.Context, r *http.Request) (accessdomain.Principal, error) {
	return s.Authenticate(ctx, r)
}

type surveyOAuthAllowlistTransport struct {
	base   http.RoundTripper
	target *url.URL
	calls  int
}

func (t *surveyOAuthAllowlistTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "api.weixin.qq.com" || (request.URL.Path != "/sns/oauth2/access_token" && request.URL.Path != "/sns/userinfo") {
		return nil, fmt.Errorf("unexpected OAuth endpoint or method")
	}
	if request.Method != http.MethodGet {
		return nil, fmt.Errorf("unexpected OAuth method %s", request.Method)
	}
	t.calls++
	forwarded := request.Clone(request.Context())
	endpoint := *t.target
	endpoint.Path = request.URL.Path
	endpoint.RawQuery = request.URL.RawQuery
	forwarded.URL = &endpoint
	forwarded.Host = ""
	forwarded.RequestURI = ""
	return t.base.RoundTrip(forwarded)
}

func surveyJourneyServe(t *testing.T, handler http.Handler, method, path string, body []byte, key string, cookies []*http.Cookie, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func surveyJourneyConcurrentSubmit(handler http.Handler, path string, body []byte, cookies []*http.Cookie) []*httptest.ResponseRecorder {
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			<-start
			request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			for _, cookie := range cookies {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			responses <- response
		}()
	}
	close(start)
	return []*httptest.ResponseRecorder{<-responses, <-responses}
}

func surveyFinalJourneyLocation(t *testing.T, recorder *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	response := recorder.Result()
	location, err := response.Location()
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func mustSurveyJourneyJSON(t *testing.T, recorder *httptest.ResponseRecorder, value any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), value); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
}
