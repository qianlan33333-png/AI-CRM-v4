package main

import (
	"bytes"
	"encoding/json"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/xuri/excelize/v2"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPostgreSQLSurveyAttachmentChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	for _, mode := range []surveyport.AnswerDisplayMode{surveyport.DisplayAllInOne, surveyport.DisplayOneByOne} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newPublicSurveyPresentationChromiumFixture(t, publicSurveyBrowserOptions{display: mode, extraQuestions: []surveyport.Question{{Type: surveyport.QuestionRedemptionCode, Title: "兑换码", Required: true, SortOrder: 1}, {Type: surveyport.QuestionAttachment, Title: "图片附件", Required: true, SortOrder: 2}}})
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
			command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "survey_attachment_chromium_journey.mjs"))
			cookies, _ := json.Marshal(fixture.adminCookies)
			command.Env = append(os.Environ(), "AICRM_SURVEY_BROWSER_ADMIN_COOKIES="+string(cookies), "AICRM_PUBLIC_SURVEY_BROWSER_URL="+fixture.browserURL, "AICRM_PUBLIC_SURVEY_BROWSER_SESSION="+fixture.session, "AICRM_PUBLIC_SURVEY_BROWSER_SUCCESS_SLUG="+fixture.success.Slug, "AICRM_SURVEY_BROWSER_QID="+strconv.FormatInt(int64(fixture.success.ID), 10), "AICRM_SURVEY_BROWSER_IMAGE="+file, "AICRM_SURVEY_BROWSER_XLSX="+xlsx, "AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR="+fixture.screenshots, "AICRM_PUBLIC_SURVEY_BROWSER_DISPLAY="+string(mode))
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "survey_attachment_chromium: PASS") {
				t.Fatalf("attachment browser=%v output=%s", err, output)
			}
			var submissions, images, used int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1),(SELECT count(*) FROM media_private_images),(SELECT count(*) FROM survey_redemption_codes WHERE questionnaire_id=$1 AND used_submission_id IS NOT NULL)`, fixture.success.ID).Scan(&submissions, &images, &used); err != nil || submissions != 1 || images != 1 || used != 1 {
				t.Fatalf("browser database readback=%d/%d/%d error=%v", submissions, images, used, err)
			}
			var id surveyport.ID
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT id FROM survey_submissions WHERE questionnaire_id=$1`, fixture.success.ID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			stored, err := fixture.submissions.GetSubmission(fixture.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			for _, answer := range stored.Answers {
				if answer.Attachment == nil {
					continue
				}
				content, err := fixture.submissions.ReadSubmissionAttachment(fixture.ctx, fixture.success.ID, id, answer.ID)
				if err != nil || !bytes.Equal(content.Content, picture.Bytes()) || content.MediaType != "image/png" {
					t.Fatalf("local thumbnail changed the submitted original image: error=%v type=%s", err, content.MediaType)
				}
			}
			raw, _ := json.Marshal(stored)
			if bytes.Contains(raw, []byte("00123")) || bytes.Contains(raw, []byte("private_ref")) {
				t.Fatal("answer projection leaked redemption or private ref")
			}
			t.Logf("Chromium %s verified import/editor/final multipart, rejection retains image, refreshed reselect, private read/export; PostgreSQL submissions/images/used=1/1/1", mode)
		})
	}
}
