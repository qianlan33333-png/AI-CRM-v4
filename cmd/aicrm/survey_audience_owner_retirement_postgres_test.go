package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type retiredSurveyAudienceFacts struct{ activationRuntimeFacts }

func (retiredSurveyAudienceFacts) FirstCompleteAudienceChoices(context.Context, time.Time) ([]surveyport.AudienceChoiceAnswer, error) {
	return []surveyport.AudienceChoiceAnswer{{CustomerID: customerdomain.CustomerID(1), QuestionnaireID: 16, QuestionID: 11, OptionIDs: []surveyport.ID{101}}}, nil
}
func (retiredSurveyAudienceFacts) ResolveAudienceQuestion(_ context.Context, q, value string) (string, bool, error) {
	return value, q == "16" && value == "11", nil
}
func (retiredSurveyAudienceFacts) ResolveAudienceOption(_ context.Context, q, question, value string) (string, bool, error) {
	return value, q == "16" && question == "11" && value == "101", nil
}

func TestSurveyAudienceRetirementHTTPPostgreSQLPreservesHistoryAndRequiresReconfiguration(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	pool, err := pg.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := pg.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := segmentapp.NewService(uow, repo)
	at := time.Now().UTC().Truncate(time.Microsecond)
	facts := &retiredSurveyAudienceFacts{activationRuntimeFacts{submissions: []surveyport.AudienceSubmission{{CustomerID: 1, QuestionnaireID: 16, SubmittedAt: at}}}}
	evaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, segmentadapter.CustomerSource{UoW: uow, Legacy: segmentadapter.LegacyTemplateSource{Survey: facts, Submissions: facts}}, automationAudienceCanonical{})
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := segmentapp.NewSnapshotService(uow, repo, evaluator, activationRuntimeJobs{}, activationRuntimeJobs{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := segmentapp.NewRuntimeFacade(service, snapshots, nil)
	handler, err := segmenthttp.NewRuntimeHandler(runtime, runtime, commerceFundsSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	handler.BindAudienceSurveyReferences(facts)
	for _, key := range []string{"questionnaire_submissions", "questionnaire_choice_answers"} {
		t.Run(key, func(t *testing.T) {
			pkg, e := service.CreatePackage(ctx, segmentapp.PackageCreateCommand{Name: "respondents " + key, TemplateKey: key, Actor: 1, IdempotencyKey: "retire-create-" + key})
			if e != nil {
				t.Fatal(e)
			}
			base := `"questionnaire_ids":["16"],"require_wecom_identity":false`
			if key == "questionnaire_choice_answers" {
				base = `"questionnaire_id":"16","conditions":[{"question_id":"11","option_ids":["101"]}]`
			}
			definition := func(owner string) string {
				return fmt.Sprintf(`{"schema_version":1,"template_key":%q,"parameters":{%s%s}}`, key, base, owner)
			}
			old := definition(`,"owner_scope":"specified","owner_staff_ids":["9"]`)
			// Append a private immutable version representing pre-retirement history.
			historical, e := segmentdomain.NewConfigurationVersionWithActor(pkg.ID, 2, json.RawMessage(old), "", "manual", 1, "admin", "admin:1", at)
			if e != nil {
				t.Fatal(e)
			}
			e = uow.Within(ctx, func(tx context.Context) error {
				historical, e = repo.CreateConfigurationVersion(tx, historical)
				if e != nil {
					return e
				}
				pkg, e = repo.SetCurrentConfiguration(tx, pkg.ID, historical.ID, pkg.Version, 1, at)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}

			call := func(method, action, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, fmt.Sprintf("/api/admin/ai-audience/packages/%d/%s", pkg.ID, action), strings.NewReader(body))
				req.Header.Set("Idempotency-Key", "retire-"+key+action)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				return response
			}
			read := call(http.MethodGet, "configuration", "")
			if read.Code != 200 || !strings.Contains(read.Body.String(), `"owner_scope":"specified"`) {
				t.Fatalf("old definition lost: %d %s", read.Code, read.Body.String())
			}
			check, e := runtime.ActivationPrecheck(ctx, pkg.ID)
			if e != nil || check.Ready || !strings.Contains(strings.Join(check.Reasons, ","), "definition_unsupported") {
				t.Fatalf("old selection executable: %+v %v", check, e)
			}
			copied, e := service.CopyPackage(ctx, segmentapp.VersionCommand{ID: pkg.ID, ExpectedVersion: pkg.Version, Actor: 1, IdempotencyKey: "retire-copy-" + key})
			if e != nil {
				t.Fatal(e)
			}
			copiedConfig, e := service.CurrentConfiguration(ctx, copied.ID)
			if e != nil || !strings.Contains(string(copiedConfig.Definition), `"specified"`) {
				t.Fatalf("copy broadened old selection: %s %v", copiedConfig.Definition, e)
			}
			copiedCheck, e := runtime.ActivationPrecheck(ctx, copied.ID)
			if e != nil || copiedCheck.Ready {
				t.Fatalf("old copied selection executable: %+v %v", copiedCheck, e)
			}
			var before int
			if e = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_configuration_versions WHERE package_id=$1`, pkg.ID).Scan(&before); e != nil {
				t.Fatal(e)
			}
			for _, action := range []string{"configuration", "preview"} {
				body := fmt.Sprintf(`{"expected_package_version":%d,"refresh_mode":"manual","definition":%s}`, pkg.Version, old)
				method := http.MethodPut
				if action == "preview" {
					body = fmt.Sprintf(`{"reference_time":%q,"definition":%s}`, at.Format(time.RFC3339Nano), old)
					method = http.MethodPost
				}
				response := call(method, action, body)
				if response.Code != 422 || !strings.Contains(response.Body.String(), "definition_not_supported") {
					t.Fatalf("retired selection accepted: %s %d %s", action, response.Code, response.Body.String())
				}
			}
			if _, e = snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: pkg.ID, Actor: 1, IdempotencyKey: "retire-refresh-" + key, ReferenceTime: at}); !errors.Is(e, segmentapp.ErrUnsupportedDefinition) {
				t.Fatalf("retired configuration queued: %v", e)
			}
			var after, runs int
			if e = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM segment_audience_configuration_versions WHERE package_id=$1),(SELECT count(*) FROM segment_audience_refresh_runs WHERE package_id=$1)`, pkg.ID).Scan(&after, &runs); e != nil || before != after || runs != 0 {
				t.Fatalf("rejected operation persisted: configs %d->%d runs=%d err=%v", before, after, runs, e)
			}
			newDefinition := definition("")
			saved := call(http.MethodPut, "configuration", fmt.Sprintf(`{"expected_package_version":%d,"refresh_mode":"manual","definition":%s}`, pkg.Version, newDefinition))
			if saved.Code != 200 || strings.Contains(saved.Body.String(), "owner_") {
				t.Fatalf("respondent configuration failed: %d %s", saved.Code, saved.Body.String())
			}
			preview := call(http.MethodPost, "preview", fmt.Sprintf(`{"reference_time":%q,"definition":%s}`, at.Format(time.RFC3339Nano), newDefinition))
			if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"member_count":1`) {
				t.Fatalf("respondent preview failed: %d %s", preview.Code, preview.Body.String())
			}
			current, e := service.CurrentConfiguration(ctx, pkg.ID)
			if e != nil {
				t.Fatal(e)
			}
			var input map[string]any
			if e = json.Unmarshal(current.Definition, &input); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(current.Definition), "owner_") {
				t.Fatal("new employee dependency remains")
			}
		})
	}
}

func TestSurveyAudienceOwnerRetirementChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	fixture := newAudienceConfirmationChromiumFixture(t)
	native := fixture.application.pool.Native()
	var questionnaireID int64
	err := native.QueryRow(fixture.ctx, `INSERT INTO survey_questionnaires(name,title,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) SELECT 'respondent-only-browser','Respondent only browser','survey','all_in_one','respondent-only-browser','draft',c.created_by,c.created_by,now(),now() FROM segment_audience_configuration_versions c JOIN segment_audience_packages p ON p.current_configuration_version_id=c.id WHERE p.id=$1 RETURNING id`, fixture.packageID).Scan(&questionnaireID)
	if err != nil {
		t.Fatal(err)
	}
	var definitionID int64
	err = native.QueryRow(fixture.ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,definition_digest,created_by,created_at) SELECT id,1,mode,answer_display_mode,title,decode(repeat('00',32),'hex'),created_by,now() FROM survey_questionnaires WHERE id=$1 RETURNING id`, questionnaireID).Scan(&definitionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(fixture.ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$2 WHERE id=$1`, questionnaireID, definitionID); err != nil {
		t.Fatal(err)
	}
	old := fmt.Sprintf(`{"schema_version":1,"template_key":"questionnaire_submissions","parameters":{"questionnaire_ids":["%d"],"require_wecom_identity":false,"owner_scope":"specified","owner_staff_ids":["9"]}}`, questionnaireID)
	digest := sha256.Sum256([]byte(old))
	var historicalID int64
	err = native.QueryRow(fixture.ctx, `INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,refresh_mode,digest,created_by,created_actor_kind,created_actor_ref,created_at) SELECT c.package_id,c.version+1,1,$2::jsonb,'manual',$3,c.created_by,c.created_actor_kind,c.created_actor_ref,now() FROM segment_audience_configuration_versions c JOIN segment_audience_packages p ON p.current_configuration_version_id=c.id WHERE p.id=$1 RETURNING id`, fixture.packageID, old, digest[:]).Scan(&historicalID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(fixture.ctx, `UPDATE segment_audience_packages SET current_configuration_version_id=$2,version=version+1 WHERE id=$1`, fixture.packageID, historicalID); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "audience_confirmation_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_AUDIENCE_TEST_SCENARIO=survey-owner-retirement", "AICRM_AUDIENCE_CONFIRMATION_TEST_URL="+fixture.server.URL, "AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME=groupops-browser-owner", "AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD=groupops-browser-owner-password", "AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID="+strconv.FormatInt(fixture.packageID, 10), "AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR="+fixture.screenshots)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "survey_owner_retirement_chromium: PASS") {
		t.Fatalf("retirement Chromium err=%v output=%s", err, output)
	}
	var historical, current string
	err = native.QueryRow(fixture.ctx, `SELECT old.definition::text,c.definition::text FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id JOIN segment_audience_configuration_versions old ON old.id=$2 WHERE p.id=$1`, fixture.packageID, historicalID).Scan(&historical, &current)
	if err != nil || !strings.Contains(historical, "specified") || strings.Contains(current, "owner_") {
		t.Fatalf("history/new configuration incorrect: historical=%s current=%s err=%v", historical, current, err)
	}
}
