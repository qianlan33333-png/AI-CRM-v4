package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
)

func attachmentPNG(t *testing.T, red uint8) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: red, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func newAttachmentFixture(t *testing.T) redemptionFixture {
	f := newRedemptionFixture(t)
	ctx := context.Background()
	media, err := mediastore.NewPostgreSQL(f.pool, f.unit)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.submissions.BindPrivateImages(media); err != nil {
		t.Fatal(err)
	}
	q := f.q
	q.Questions = append(q.Questions, surveyport.Question{Type: surveyport.QuestionAttachment, Title: "图片一", Required: true, SortOrder: 1}, surveyport.Question{Type: surveyport.QuestionAttachment, Title: "图片二", Required: false, SortOrder: 2})
	f.q, err = f.definitions.Update(ctx, surveyport.UpdateCommand{ExpectedVersion: q.Version, ActorID: f.actor, IdempotencyKey: "attachment-update-0001", Questionnaire: q})
	if err != nil {
		t.Fatal(err)
	}
	f.q, err = f.definitions.Publish(ctx, f.q.ID, f.q.Version, f.actor, "attachment-publish-0001")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.definitions.ImportRedemptionCodes(ctx, f.q.ID, f.actor, "attachment-import-0001", redemptionWorkbook(t, "IMG", "ROLLBACK", "RACE"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f redemptionFixture) imageCommand(t *testing.T, customer int64, key, code string, red uint8) surveyport.SubmitCommand {
	command := f.command(customer, key, code)
	for _, question := range f.q.Questions[1:] {
		command.Answers = append(command.Answers, surveyport.SubmissionAnswer{QuestionID: question.ID, Attachment: true})
		command.Attachments = append(command.Attachments, surveyport.AttachmentUpload{QuestionID: question.ID, FileName: "图片.png", MediaType: "image/png", Content: attachmentPNG(t, red)})
	}
	return command
}
func assertImageCounts(t *testing.T, f redemptionFixture, submissions, images, used int) {
	t.Helper()
	ctx := context.Background()
	var a, b, c int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM survey_submissions),(SELECT count(*) FROM media_private_images),(SELECT count(*) FROM survey_redemption_codes WHERE used_submission_id IS NOT NULL)`).Scan(&a, &b, &c); err != nil {
		t.Fatal(err)
	}
	if a != submissions || b != images || c != used {
		t.Fatalf("counts submissions/images/used=%d/%d/%d wanted=%d/%d/%d", a, b, c, submissions, images, used)
	}
}
func TestPostgreSQLAttachmentAtomicReplayAndScope(t *testing.T) {
	f := newAttachmentFixture(t)
	ctx := context.Background()
	command := f.imageCommand(t, f.customer(t), "I", "IMG", 100)
	receipt, err := f.submissions.Submit(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	assertImageCounts(t, f, 1, 2, 1)
	stored, err := f.submissions.GetSubmission(ctx, receipt.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, answer := range stored.Answers {
		if answer.QuestionType != surveyport.QuestionAttachment {
			continue
		}
		if answer.Attachment == nil || !strings.HasPrefix(answer.Attachment.ViewURL, "/api/admin/") {
			t.Fatalf("missing protected projection %+v", answer)
		}
		content, err := f.submissions.ReadSubmissionAttachment(ctx, f.q.ID, stored.ID, answer.ID)
		if err != nil || !bytes.Equal(content.Content, command.Attachments[0].Content) {
			t.Fatalf("image read %v", err)
		}
		if _, err = f.submissions.ReadSubmissionAttachment(ctx, f.q.ID+1, stored.ID, answer.ID); !errors.Is(err, surveyport.ErrNotFound) {
			t.Fatalf("cross questionnaire read=%v", err)
		}
		if _, err = f.submissions.ReadSubmissionAttachment(ctx, f.q.ID, stored.ID+1, answer.ID); !errors.Is(err, surveyport.ErrNotFound) {
			t.Fatalf("cross answer read=%v", err)
		}
	}
	_, err = f.definitions.SetStatus(ctx, f.q.ID, f.q.Version, surveyport.StatusDisabled, f.actor, "attachment-disable-0001")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.submissions.Submit(ctx, command)
	if err != nil || replay.SubmissionID != receipt.SubmissionID {
		t.Fatalf("receipt before mutable state %v %+v", err, replay)
	}
	assertImageCounts(t, f, 1, 2, 1)
	command.Attachments[0].Content = attachmentPNG(t, 200)
	_, err = f.submissions.Submit(ctx, command)
	var field *surveyport.FieldError
	if !errors.As(err, &field) || field.Code != "submission_payload_conflict" {
		t.Fatalf("changed image key error=%v", err)
	}
	command.Attachments[0].Content = []byte("damaged changed file")
	_, err = f.submissions.Submit(ctx, command)
	if !errors.As(err, &field) || field.Code != "submission_payload_conflict" {
		t.Fatalf("changed same-key image disguised as format or version error: %v", err)
	}
}

type failingImageObserver struct{}

func (failingImageObserver) SubmissionCreatedWithin(context.Context, int64, int64) error {
	return errors.New("forced after image persistence")
}
func TestPostgreSQLAttachmentRollbackAndConcurrentCode(t *testing.T) {
	f := newAttachmentFixture(t)
	ctx := context.Background()
	command := f.imageCommand(t, f.customer(t), "R", "ROLLBACK", 5)
	f.submissions.BindSubmissionObserver(failingImageObserver{})
	if _, err := f.submissions.Submit(ctx, command); err == nil {
		t.Fatal("expected atomic failure")
	}
	assertImageCounts(t, f, 0, 0, 0)
	var blobs int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM media_blobs`).Scan(&blobs); err != nil || blobs != 0 {
		t.Fatalf("rollback blobs=%d err=%v", blobs, err)
	}
	f.submissions.BindSubmissionObserver(nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	out := make(chan error, 2)
	commands := []surveyport.SubmitCommand{f.imageCommand(t, f.customer(t), "A", "RACE", 10), f.imageCommand(t, f.customer(t), "B", "RACE", 20)}
	for _, c := range commands {
		wg.Add(1)
		go func(c surveyport.SubmitCommand) {
			defer wg.Done()
			<-start
			_, err := f.submissions.Submit(ctx, c)
			out <- err
		}(c)
	}
	close(start)
	wg.Wait()
	close(out)
	success, used := 0, 0
	for err := range out {
		if err == nil {
			success++
		} else {
			var field *surveyport.FieldError
			if !errors.As(err, &field) || field.Code != "redemption_code_used" {
				t.Fatal(err)
			}
			used++
		}
	}
	if success != 1 || used != 1 {
		t.Fatalf("race %d/%d", success, used)
	}
	assertImageCounts(t, f, 1, 2, 1)
}
func TestPostgreSQLAttachmentRejectsInvalidFilesAndFields(t *testing.T) {
	f := newAttachmentFixture(t)
	ctx := context.Background()
	original := f.imageCommand(t, f.customer(t), "V", "IMG", 55)
	cases := []struct {
		name   string
		change func(*surveyport.SubmitCommand)
	}{
		{"missing required", func(c *surveyport.SubmitCommand) { c.Attachments = nil }},
		{"fake MIME", func(c *surveyport.SubmitCommand) { c.Attachments[0].MediaType = "image/jpeg" }},
		{"damaged", func(c *surveyport.SubmitCommand) { c.Attachments[0].Content = []byte("png forged") }},
		{"exact 2MB", func(c *surveyport.SubmitCommand) {
			c.Attachments[0].Content = append(c.Attachments[0].Content, make([]byte, surveyport.MaximumAttachmentBytes-len(c.Attachments[0].Content))...)
		}},
		{"duplicate", func(c *surveyport.SubmitCommand) { c.Attachments = append(c.Attachments, c.Attachments[0]) }},
		{"foreign field", func(c *surveyport.SubmitCommand) { c.Attachments[0].QuestionID += 99999 }},
		{"code field image", func(c *surveyport.SubmitCommand) { c.Attachments[0].QuestionID = f.q.Questions[0].ID }},
		{"stale version", func(c *surveyport.SubmitCommand) { c.DefinitionVersion-- }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			command := original
			command.Attachments = append([]surveyport.AttachmentUpload(nil), original.Attachments...)
			test.change(&command)
			if _, err := f.submissions.Submit(ctx, command); err == nil {
				t.Fatal("unsafe submission accepted")
			}
			assertImageCounts(t, f, 0, 0, 0)
		})
	}
	// Strictly below the boundary is valid even when a PNG has trailing bytes.
	original.Attachments[0].Content = append(original.Attachments[0].Content, make([]byte, surveyport.MaximumAttachmentBytes-1-len(original.Attachments[0].Content))...)
	if _, err := f.submissions.Submit(ctx, original); err != nil {
		t.Fatal(err)
	}
	assertImageCounts(t, f, 1, 2, 1)
	images, err := mediastore.NewPostgreSQL(f.pool, f.unit)
	if err != nil {
		t.Fatal(err)
	}
	input := mediaport.PrivateImageUpload{FileName: "image.png", MediaType: "image/png", Content: attachmentPNG(t, 9)}
	info, err := images.InspectPrivateImage(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = images.StorePrivateImageWithin(ctx, input, info); err == nil {
		t.Fatal("private upload opened its own transaction")
	}
}

func TestPostgreSQLAttachmentWithoutCodeAndOptionalJSON(t *testing.T) {
	f := newAttachmentFixture(t)
	ctx := context.Background()
	q, err := f.definitions.Create(ctx, surveyport.CreateCommand{ActorID: f.actor, IdempotencyKey: "attachment-optional-create", Questionnaire: surveyport.Questionnaire{Name: "图片问卷", Title: "图片问卷", Slug: "optional-images", Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{{Type: surveyport.QuestionAttachment, Title: "可选图片", Required: false}}}})
	if err != nil {
		t.Fatal(err)
	}
	q, err = f.definitions.Publish(ctx, q.ID, q.Version, f.actor, "attachment-optional-publish")
	if err != nil {
		t.Fatal(err)
	}
	customer := f.customer(t)
	command := surveyport.SubmitCommand{Slug: q.Slug, DefinitionVersion: q.DefinitionVersion, SubmissionKey: strings.Repeat("O", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: customerPointer(customer), EvidenceDigest: strings.Repeat("c", 64)}, Answers: []surveyport.SubmissionAnswer{}}
	receipt, err := f.submissions.Submit(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	assertImageCounts(t, f, 1, 0, 0)
	legacy, _ := json.Marshal(struct {
		Version       int64                         `json:"version"`
		Answers       []surveyport.SubmissionAnswer `json:"answers"`
		IdentityState surveyport.IdentityState      `json:"identity_state"`
		Evidence      string                        `json:"evidence_digest,omitempty"`
	}{command.DefinitionVersion, command.Answers, command.Identity.State, command.Identity.EvidenceDigest})
	var digest []byte
	if err = f.pool.QueryRow(ctx, `SELECT payload_digest FROM survey_submissions WHERE id=$1`, receipt.SubmissionID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(legacy)
	if !bytes.Equal(digest, want[:]) {
		t.Fatal("JSON fingerprint changed and would break retained receipts")
	}
	command.Identity.CustomerID = customerPointer(f.customer(t))
	command.SubmissionKey = strings.Repeat("P", 43)
	command.Answers = []surveyport.SubmissionAnswer{{QuestionID: q.Questions[0].ID, Attachment: true}}
	command.Attachments = []surveyport.AttachmentUpload{{QuestionID: q.Questions[0].ID, FileName: "optional.png", MediaType: "image/png", Content: attachmentPNG(t, 42)}}
	if _, err = f.submissions.Submit(ctx, command); err != nil {
		t.Fatal(err)
	}
	assertImageCounts(t, f, 2, 1, 0)
}

type failImageCompletion struct{ seen bool }

func (f *failImageCompletion) AcceptCompletionWithin(ctx context.Context, intent surveyport.CompletionIntent) (surveyport.EffectBinding, error) {
	database, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return surveyport.EffectBinding{}, err
	}
	var images, audit, outbox int
	err = database.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_private_images),(SELECT count(*) FROM survey_audit_events WHERE event_type='submission_created'),(SELECT count(*) FROM survey_outbox WHERE event_type='submission_created')`).Scan(&images, &audit, &outbox)
	if err != nil {
		return surveyport.EffectBinding{}, err
	}
	if images != 2 || audit != 1 || outbox != 1 {
		return surveyport.EffectBinding{}, errors.New("images/audit/outbox are not in the same transaction")
	}
	f.seen = true
	return surveyport.EffectBinding{}, errors.New("force completion acceptance failure")
}
func TestPostgreSQLAttachmentCompletionFailureRollsBackAudit(t *testing.T) {
	f := newAttachmentFixture(t)
	ctx := context.Background()
	_, err := f.submissions.SaveOperationConfiguration(ctx, surveyport.OperationConfiguration{QuestionnaireID: f.q.ID, ExternalPushEnabled: true, ExternalPushConfigurationRef: "local-test-webhook"}, f.actor, "attachment-config-0001")
	if err != nil {
		t.Fatal(err)
	}
	completion := &failImageCompletion{}
	if err = f.submissions.BindCompletionIntent(completion); err != nil {
		t.Fatal(err)
	}
	command := f.imageCommand(t, f.customer(t), "F", "ROLLBACK", 70)
	if _, err = f.submissions.Submit(ctx, command); err == nil || !completion.seen {
		t.Fatalf("did not fail after in-transaction images/audit/outbox: %v", err)
	}
	assertImageCounts(t, f, 0, 0, 0)
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM survey_audit_events WHERE event_type='submission_created')+(SELECT count(*) FROM survey_outbox WHERE event_type='submission_created')+(SELECT count(*) FROM media_blobs)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback left effects or bytes count=%d err=%v", count, err)
	}
}
