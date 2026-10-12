package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/xuri/excelize/v2"
)

func TestPostgreSQLSurveyEditorCreateChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	for _, mode := range []surveyport.AnswerDisplayMode{surveyport.DisplayAllInOne, surveyport.DisplayOneByOne} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newPublicSurveyPresentationChromiumFixture(t, publicSurveyBrowserOptions{display: mode})
			directory := t.TempDir()
			book := excelize.NewFile()
			_ = book.SetCellStr("Sheet1", "A1", "兑换码")
			_ = book.SetCellStr("Sheet1", "A2", "00123")
			_ = book.SetCellStr("Sheet1", "A3", "00123")
			xlsx := filepath.Join(directory, "codes.xlsx")
			if err := book.SaveAs(xlsx); err != nil {
				t.Fatal(err)
			}
			_ = book.Close()
			var picture bytes.Buffer
			im := image.NewRGBA(image.Rect(0, 0, 120, 80))
			for y := 0; y < 80; y++ {
				for x := 0; x < 120; x++ {
					im.Set(x, y, color.RGBA{R: 70, G: uint8(80 + x), B: uint8(100 + y), A: 255})
				}
			}
			if err := png.Encode(&picture, im); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(directory, "picture.png")
			if err := os.WriteFile(file, picture.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "survey_editor_create_chromium_journey.mjs"))
			cookies, _ := json.Marshal(fixture.adminCookies)
			command.Env = append(os.Environ(), "AICRM_SURVEY_BROWSER_ADMIN_COOKIES="+string(cookies), "AICRM_PUBLIC_SURVEY_BROWSER_URL="+fixture.browserURL, "AICRM_PUBLIC_SURVEY_BROWSER_SESSION="+fixture.session, "AICRM_SURVEY_BROWSER_IMAGE="+file, "AICRM_SURVEY_BROWSER_XLSX="+xlsx, "AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR="+fixture.screenshots, "AICRM_PUBLIC_SURVEY_BROWSER_DISPLAY="+string(mode))
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "survey_editor_create_chromium: PASS") {
				t.Fatalf("editor create browser=%v output=%s", err, output)
			}
			var createdID surveyport.ID
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT id FROM survey_questionnaires WHERE name='测试新能力'`).Scan(&createdID); err != nil {
				t.Fatal(err)
			}
			var createdCount int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM survey_questionnaires WHERE name='测试新能力'`).Scan(&createdCount); err != nil || createdCount != 1 {
				t.Fatalf("rejected save or reload left extra questionnaires: %d error=%v", createdCount, err)
			}
			var submissions, images, used int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1),(SELECT count(*) FROM media_private_images),(SELECT count(*) FROM survey_redemption_codes WHERE questionnaire_id=$1 AND used_submission_id IS NOT NULL)`, createdID).Scan(&submissions, &images, &used); err != nil || submissions != 1 || images != 1 || used != 1 {
				t.Fatalf("browser database readback=%d/%d/%d error=%v", submissions, images, used, err)
			}
			var id surveyport.ID
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT id FROM survey_submissions WHERE questionnaire_id=$1`, createdID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			stored, err := fixture.submissions.GetSubmission(fixture.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(stored)
			if bytes.Contains(raw, []byte("00123")) || bytes.Contains(raw, []byte("private_ref")) {
				t.Fatal("answer projection leaked redemption or private ref")
			}
			t.Logf("Chromium %s verified real blank-editor create/reopen, invalid-save draft retention, publish/import/mixed shipping+code+image submit; PostgreSQL single created questionnaire, submissions/images/used=1/1/1", mode)
		})
	}
}
