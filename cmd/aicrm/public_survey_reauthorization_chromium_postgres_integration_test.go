package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLPublicSurveyReauthorizationChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	for _, display := range []surveyport.AnswerDisplayMode{surveyport.DisplayAllInOne, surveyport.DisplayOneByOne} {
		t.Run(string(display), func(t *testing.T) {
			fixture := newPublicSurveyPresentationChromiumFixture(t, publicSurveyBrowserOptions{reauthorization: true, display: display})
			original := fixture.server.Config.Handler
			var mu sync.Mutex
			var payloads [][]byte
			starts := 0
			fixture.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/public/questionnaires/"+fixture.success.Slug+"/submissions" {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
					mu.Lock()
					payloads = append(payloads, body)
					first := len(payloads) == 1
					mu.Unlock()
					if first {
						// Expire only the synthetic browser session. The real Owner must
						// reject the unauthenticated request without a business write.
						r = r.Clone(r.Context())
						r.Header.Del("Cookie")
						http.SetCookie(w, &http.Cookie{Name: "__Host-aicrm_survey_identity", Path: "/", Secure: true, HttpOnly: true, MaxAge: -1})
					}
				}
				if r.URL.Path == "/api/h5/surveys/oauth/start" && r.URL.Query().Get("resume") == "submit" {
					recorder := httptest.NewRecorder()
					original.ServeHTTP(recorder, r)
					target, err := url.Parse(recorder.Header().Get("Location"))
					if err != nil || recorder.Code != 303 || target.Host != "open.weixin.qq.com" || target.Query().Get("scope") != "snsapi_userinfo" {
						t.Errorf("resume did not reach official authorization: status=%d parse_error=%t official_host=%t userinfo_scope=%t", recorder.Code, err != nil, target != nil && target.Host == "open.weixin.qq.com", target != nil && target.Query().Get("scope") == "snsapi_userinfo")
						http.Error(w, "invalid fixture authorization", 500)
						return
					}
					mu.Lock()
					starts++
					allowed := starts > 1
					mu.Unlock()
					for _, cookie := range recorder.Result().Cookies() {
						http.SetCookie(w, cookie)
					}
					callback := fixture.browserURL + "/api/h5/surveys/oauth/callback?state=" + url.QueryEscape(target.Query().Get("state"))
					if allowed {
						callback += "&code=public-survey-browser-code"
					}
					// A synthetic WeChat response denies once, then allows. Both paths
					// consume the real state and real controlled Provider adapter.
					http.Redirect(w, r, callback, http.StatusSeeOther)
					return
				}
				original.ServeHTTP(w, r)
			})
			command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "public_survey_reauthorization_chromium_journey.mjs"))
			command.Env = append(os.Environ(), "AICRM_PUBLIC_SURVEY_BROWSER_URL="+fixture.browserURL, "AICRM_PUBLIC_SURVEY_BROWSER_SESSION="+fixture.session, "AICRM_PUBLIC_SURVEY_BROWSER_SUCCESS_SLUG="+fixture.success.Slug, "AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR="+fixture.screenshots, "AICRM_PUBLIC_SURVEY_BROWSER_DISPLAY="+string(display))
			output, err := command.CombinedOutput()
			if strings.Contains(string(output), "reauthorization_chromium: SKIP_DEVTOOLS") && runtime.GOOS == "darwin" {
				t.Skip("local Chromium DevTools unavailable; Linux journey required")
			}
			if err != nil || !strings.Contains(string(output), "reauthorization_chromium: PASS") {
				t.Fatalf("reauthorization journey err=%v output=%s", err, output)
			}
			mu.Lock()
			defer mu.Unlock()
			// Every explicit click checks the real Owner session, including after
			// denial. A valid session may have been restored in another tab.
			if len(payloads) != 3 || !bytes.Equal(payloads[0], payloads[1]) || !bytes.Equal(payloads[0], payloads[2]) || starts != 2 {
				t.Fatalf("reauthorization payloads=%d starts=%d; the original payload/key must replay", len(payloads), starts)
			}
			var writes, answers int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1`, fixture.success.ID).Scan(&writes); err != nil || writes != 1 {
				t.Fatalf("submission writes=%d err=%v", writes, err)
			}
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM survey_submission_answers WHERE submission_id IN (SELECT id FROM survey_submissions WHERE questionnaire_id=$1)`, fixture.success.ID).Scan(&answers); err != nil || answers != 8 {
				t.Fatalf("restored answer count=%d err=%v", answers, err)
			}
			var submissionID int64
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT id FROM survey_submissions WHERE questionnaire_id=$1`, fixture.success.ID).Scan(&submissionID); err != nil {
				t.Fatal(err)
			}
			stored, err := fixture.submissions.GetSubmission(fixture.ctx, surveyport.ID(submissionID))
			if err != nil || len(stored.Answers) != 8 || stored.Answers[7].TextValue != "" || stored.Answers[7].TextValueMasked != "[protected]" {
				t.Fatal("default submission read exposed free text or lost the protected answer")
			}
			// This independent test database owns a synthetic cipher. Inspect the
			// encrypted bytes without widening any application's PII reveal path.
			var encrypted []byte
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT text_value_ciphertext FROM survey_submission_answers WHERE submission_id=$1 AND question_type='textarea'`, submissionID).Scan(&encrypted); err != nil {
				t.Fatal(err)
			}
			plain, err := fixture.cipher.Decrypt(encrypted)
			if err != nil || plain != "\n保留完整文字答案\n" || bytes.Equal(encrypted, []byte(plain)) {
				t.Fatal("free text was rejected, trimmed or changed during OAuth recovery")
			}
		})
	}

}

// An Owner commit followed by a gateway failure is outcome-unknown to the
// browser. Retry must recover the original receipt without a second write.
func TestPostgreSQLPublicSurveyLostResponseChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	for _, display := range []surveyport.AnswerDisplayMode{surveyport.DisplayAllInOne, surveyport.DisplayOneByOne} {
		t.Run(string(display), func(t *testing.T) {
			fixture := newPublicSurveyPresentationChromiumFixture(t, publicSurveyBrowserOptions{reauthorization: true, display: display})
			original := fixture.server.Config.Handler
			var mu sync.Mutex
			var payloads [][]byte
			fixture.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/public/questionnaires/"+fixture.success.Slug+"/submissions" {
					original.ServeHTTP(w, r)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				mu.Lock()
				payloads = append(payloads, body)
				first := len(payloads) == 1
				mu.Unlock()
				if first {
					recorder := httptest.NewRecorder()
					original.ServeHTTP(recorder, r)
					if recorder.Code != http.StatusCreated {
						t.Errorf("expected original committed response, got %d", recorder.Code)
					}
					// Discard the already committed receipt, as a failing gateway can.
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"error":"controlled_gateway_failure"}`))
					return
				}
				original.ServeHTTP(w, r)
			})
			command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "public_survey_reauthorization_chromium_journey.mjs"))
			command.Env = append(os.Environ(), "AICRM_PUBLIC_SURVEY_BROWSER_URL="+fixture.browserURL, "AICRM_PUBLIC_SURVEY_BROWSER_SESSION="+fixture.session, "AICRM_PUBLIC_SURVEY_BROWSER_SUCCESS_SLUG="+fixture.success.Slug, "AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR="+fixture.screenshots, "AICRM_PUBLIC_SURVEY_BROWSER_DISPLAY="+string(display), "AICRM_PUBLIC_SURVEY_BROWSER_RECOVERY=lost-response")
			output, err := command.CombinedOutput()
			if strings.Contains(string(output), "reauthorization_chromium: SKIP_DEVTOOLS") && runtime.GOOS == "darwin" {
				t.Skip("local Chromium DevTools unavailable; Linux journey required")
			}
			if err != nil || !strings.Contains(string(output), "lost_response_chromium: PASS") {
				t.Fatalf("lost response journey err=%v output=%s", err, output)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(payloads) != 2 || !bytes.Equal(payloads[0], payloads[1]) {
				t.Fatalf("requests=%d; unknown outcome must retry the original payload/key", len(payloads))
			}
			var writes, answers, claims int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1`, fixture.success.ID).Scan(&writes); err != nil || writes != 1 {
				t.Fatalf("writes=%d err=%v", writes, err)
			}
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM survey_submission_answers WHERE submission_id IN (SELECT id FROM survey_submissions WHERE questionnaire_id=$1)`, fixture.success.ID).Scan(&answers); err != nil || answers != 8 {
				t.Fatalf("answers=%d err=%v", answers, err)
			}
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM survey_submission_claims WHERE questionnaire_id=$1`, fixture.success.ID).Scan(&claims); err != nil || claims != 1 {
				t.Fatalf("claims=%d err=%v", claims, err)
			}
		})
	}
}
