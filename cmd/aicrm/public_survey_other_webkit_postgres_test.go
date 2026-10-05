package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
)

// Real H5 + HTTP + PG; OAuth uses the existing loopback Provider fixture.
// No completion-effect worker, real Provider or canonical phone adapter runs.
func TestPostgreSQLPublicSurveyOtherWebKitRecovery(t *testing.T) {
	runPublicSurveyWebKitRecovery(t, false)
}

func TestPostgreSQLPublicSurveyDefinitionChangeWebKitRecovery(t *testing.T) {
	runPublicSurveyWebKitRecovery(t, true)
}

func runPublicSurveyWebKitRecovery(t *testing.T, definitionChange bool) {
	t.Helper()
	if os.Getenv("AICRM_REQUIRE_SURVEY_WEBKIT") != "1" {
		t.Skip("requires local old and modern WebKit fixtures")
	}
	for _, engine := range []struct{ name, version, root, driver string }{
		{"webkit15.4", "15.4", os.Getenv("AICRM_PHASE2_OLD_PLAYWRIGHT_ROOT"), os.Getenv("AICRM_PHASE2_OLD_WEBKIT_DRIVER")},
		{"webkit26.0", "26.0", os.Getenv("AICRM_PHASE2_MODERN_PLAYWRIGHT_ROOT"), os.Getenv("AICRM_PHASE2_MODERN_WEBKIT_DRIVER")},
	} {
		for _, display := range []surveyport.AnswerDisplayMode{surveyport.DisplayAllInOne, surveyport.DisplayOneByOne} {
			t.Run(engine.name+"/"+string(display), func(t *testing.T) {
				if !filepath.IsAbs(engine.root) || !filepath.IsAbs(engine.driver) {
					t.Fatal("required WebKit paths missing")
				}
				maximum := 2
				f := newPublicSurveyPresentationChromiumFixture(t, publicSurveyBrowserOptions{reauthorization: true, display: display, extraQuestions: []surveyport.Question{{Type: surveyport.QuestionMultiChoice, Title: "需要补充哪些内容", Required: true, SortOrder: 8, Validation: surveyport.Validation{MaximumSelections: &maximum}, Options: []surveyport.Option{{Text: "已确认", TagCodes: []string{}, SortOrder: 0}, {Text: "其他", IsOther: true, OtherPlaceholder: "请补充说明", OtherMaximumLength: 20, TagCodes: []string{}, SortOrder: 1}}}}})
				original := f.server.Config.Handler
				var mu sync.Mutex
				var payloads [][]byte
				var publishedVersion int64
				f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "POST" || r.URL.Path != "/api/public/questionnaires/"+f.success.Slug+"/submissions" {
						original.ServeHTTP(w, r)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						http.Error(w, "fixture body", 500)
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
					mu.Lock()
					payloads = append(payloads, body)
					first := len(payloads) == 1
					mu.Unlock()
					if first {
						if definitionChange {
							// Publish through the actual admin application after the browser
							// filled v1, then forward the original body to real public HTTP.
							wrapper, err := platformpostgres.Wrap(f.native, time.Second)
							if err != nil {
								t.Error(err)
								http.Error(w, "fixture UoW", 500)
								return
							}
							uow, err := platformpostgres.NewUnitOfWork(wrapper)
							if err != nil {
								t.Error(err)
								http.Error(w, "fixture UoW", 500)
								return
							}
							repository, err := surveystore.NewPostgreSQL(f.native, uow, f.cipher)
							if err != nil {
								t.Error(err)
								http.Error(w, "fixture repository", 500)
								return
							}
							definitions := surveyapp.NewService(uow, repository)
							updated := f.success
							updated.Title = "新版公开问卷体验"
							updated, err = definitions.Update(f.ctx, surveyport.UpdateCommand{Questionnaire: updated, ExpectedVersion: f.success.Version, ActorID: f.success.CreatedBy, IdempotencyKey: "webkit-definition-update-0001"})
							if err == nil {
								updated, err = definitions.Publish(f.ctx, updated.ID, updated.Version, updated.CreatedBy, "webkit-definition-publish-0001")
							}
							if err != nil || updated.DefinitionVersion <= f.success.DefinitionVersion {
								t.Errorf("actual republish: version=%d error=%v", updated.DefinitionVersion, err)
								http.Error(w, "fixture republish", 500)
								return
							}
							mu.Lock()
							publishedVersion = updated.DefinitionVersion
							mu.Unlock()
							recorder := httptest.NewRecorder()
							original.ServeHTTP(recorder, r)
							if recorder.Code != 409 || !bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"definition_version_conflict"`)) {
								t.Errorf("actual version-conflict contract status=%d", recorder.Code)
							}
							for key, values := range recorder.Header() {
								for _, value := range values {
									w.Header().Add(key, value)
								}
							}
							w.WriteHeader(recorder.Code)
							_, _ = w.Write(recorder.Body.Bytes())
							return
						}
						recorder := httptest.NewRecorder()
						original.ServeHTTP(recorder, r)
						if recorder.Code != http.StatusCreated {
							t.Errorf("Owner did not commit: status=%d", recorder.Code)
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(502)
						_, _ = w.Write([]byte(`{"error":"controlled_gateway_failure"}`))
						return
					}
					original.ServeHTTP(w, r)
				})
				dir := filepath.Join(f.screenshots, engine.name, string(display))
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				command := exec.CommandContext(f.ctx, "node", filepath.Join(filepath.Dir(f.script), "public_survey_other_webkit.mjs"))
				command.Env = append(os.Environ(), "SURVEY_BASE="+f.server.URL, "SURVEY_SESSION="+f.session, "SURVEY_SLUG="+f.success.Slug, "SURVEY_DISPLAY="+string(display), "SURVEY_DIR="+dir, "SURVEY_ENGINE="+engine.version, "SURVEY_ROOT="+engine.root, "SURVEY_DRIVER="+engine.driver)
				if definitionChange {
					command.Env = append(command.Env, "SURVEY_VERSION_CHANGE=1")
				}
				output, err := command.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "survey_other_webkit: PASS") {
					t.Fatalf("WebKit %s %s: err=%v output=%s", engine.version, display, err, output)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(payloads) != 2 || bytes.Equal(payloads[0], payloads[1]) == definitionChange {
					t.Fatalf("requests=%d; definitionChange=%t must recover the appropriate payload/key", len(payloads), definitionChange)
				}
				var writes, answers, claims int
				if err = f.native.QueryRow(f.ctx, `SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1`, f.success.ID).Scan(&writes); err != nil || writes != 1 {
					t.Fatalf("submissions=%d error=%v", writes, err)
				}
				if err = f.native.QueryRow(f.ctx, `SELECT count(*) FROM survey_submission_answers WHERE submission_id IN (SELECT id FROM survey_submissions WHERE questionnaire_id=$1)`, f.success.ID).Scan(&answers); err != nil || answers != 9 {
					t.Fatalf("answers=%d error=%v", answers, err)
				}
				if err = f.native.QueryRow(f.ctx, `SELECT count(*) FROM survey_submission_claims WHERE questionnaire_id=$1`, f.success.ID).Scan(&claims); err != nil || claims != 1 {
					t.Fatalf("claims=%d error=%v", claims, err)
				}
				if definitionChange {
					var actual, declared int64
					if err = f.native.QueryRow(f.ctx, `SELECT d.version_number,s.definition_version_number FROM survey_submissions s JOIN survey_definition_versions d ON d.id=s.definition_version_id WHERE s.questionnaire_id=$1`, f.success.ID).Scan(&actual, &declared); err != nil || actual != publishedVersion || declared != publishedVersion {
						t.Fatalf("new answers must bind actual/declared newly published definition: %d/%d error=%v", actual, declared, err)
					}
				}
				for kind, expected := range map[string]string{"mobile": "13800000000", "textarea": "\n保留完整文字答案\n", "multi_choice": "其他内容🙂"} {
					var encrypted []byte
					if err = f.native.QueryRow(f.ctx, `SELECT a.text_value_ciphertext FROM survey_submission_answers a JOIN survey_submissions s ON s.id=a.submission_id WHERE s.questionnaire_id=$1 AND a.question_type=$2`, f.success.ID, kind).Scan(&encrypted); err != nil {
						t.Fatal(err)
					}
					plain, decryptErr := f.cipher.Decrypt(encrypted)
					if decryptErr != nil || plain != expected || bytes.Equal(encrypted, []byte(plain)) {
						t.Fatalf("%s protected value lost or stored as plaintext", kind)
					}
				}
				var selections int
				if err = f.native.QueryRow(f.ctx, `SELECT jsonb_array_length(a.selected_options_snapshot) FROM survey_submission_answers a JOIN survey_submissions s ON s.id=a.submission_id WHERE s.questionnaire_id=$1 AND a.question_type='multi_choice'`, f.success.ID).Scan(&selections); err != nil || selections != 2 {
					t.Fatalf("multi selections=%d error=%v", selections, err)
				}
				t.Logf("engine=%s display=%s real HTTP/PG: submissions=1 claims=1 answers=9; native mobile/text/other recovered; definitionChange=%t", engine.version, display, definitionChange)
			})
		}
	}
}
