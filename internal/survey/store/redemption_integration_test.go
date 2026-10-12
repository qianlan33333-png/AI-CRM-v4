package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	"github.com/xuri/excelize/v2"
)

type redemptionFixture struct {
	pool        *pgxpool.Pool
	repo        *Repository
	unit        *platformpostgres.UnitOfWork
	definitions *surveyapp.Service
	submissions *surveyapp.SubmissionService
	actor       int64
	q           surveyport.Questionnaire
}

func newRedemptionFixture(t *testing.T) redemptionFixture {
	t.Helper()
	pool, cleanup := surveyIntegrationPool(t)
	t.Cleanup(cleanup)
	wrapper, e := platformpostgres.Wrap(pool, 3*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	unit, e := platformpostgres.NewUnitOfWork(wrapper)
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	repo, e := NewPostgreSQL(pool, unit, cipher)
	if e != nil {
		t.Fatal(e)
	}
	f := redemptionFixture{pool: pool, repo: repo, unit: unit, definitions: surveyapp.NewService(unit, repo), submissions: surveyapp.NewSubmissionService(unit, repo, cipher)}
	if e = f.submissions.BindCollectedPhone(submissionClaimPhoneBinding{}); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(context.Background(), `INSERT INTO admin_users(username,password_hash,display_name) VALUES('redemption-admin','$argon2id$test','Redemption Test') RETURNING id`).Scan(&f.actor); e != nil {
		t.Fatal(e)
	}
	f.q, e = f.definitions.Create(context.Background(), surveyport.CreateCommand{ActorID: f.actor, IdempotencyKey: "redemption-create-0001", Questionnaire: surveyport.Questionnaire{Name: "兑换问卷", Title: "兑换问卷", Slug: "redemption", Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{{Type: surveyport.QuestionRedemptionCode, Title: "兑换码", Required: true, SortOrder: 0}}}})
	if e != nil {
		t.Fatal(e)
	}
	f.q, e = f.definitions.Publish(context.Background(), f.q.ID, f.q.Version, f.actor, "redemption-publish-0001")
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func redemptionWorkbook(t *testing.T, codes ...string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetCellStr("Sheet1", "A1", "兑换码")
	for i, code := range codes {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		_ = f.SetCellStr("Sheet1", cell, code)
	}
	b, e := f.WriteToBuffer()
	if e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func (f redemptionFixture) customer(t *testing.T) int64 {
	t.Helper()
	var id int64
	if e := f.pool.QueryRow(context.Background(), `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}
func (f redemptionFixture) command(customer int64, key, code string) surveyport.SubmitCommand {
	return surveyport.SubmitCommand{Slug: f.q.Slug, DefinitionVersion: f.q.DefinitionVersion, SubmissionKey: strings.Repeat(key, 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: customerPointer(customer), EvidenceDigest: strings.Repeat("c", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: f.q.Questions[0].ID, TextValue: code}}}
}
func TestPostgreSQLRedemptionConcurrentConsumeReplayAndImport(t *testing.T) {
	f := newRedemptionFixture(t)
	ctx := context.Background()
	book := redemptionWorkbook(t, "00123", "ABC", "ABC")
	result, e := f.definitions.ImportRedemptionCodes(ctx, f.q.ID, f.actor, "redemption-import-0001", book)
	if e != nil || result.Imported != 2 || result.Duplicates != 1 {
		t.Fatalf("import=%+v err=%v", result, e)
	}
	replay, e := f.definitions.ImportRedemptionCodes(ctx, f.q.ID, f.actor, "redemption-import-0001", book)
	if e != nil || replay != result {
		t.Fatalf("import replay=%+v err=%v", replay, e)
	}
	commands := []surveyport.SubmitCommand{f.command(f.customer(t), "a", "00123"), f.command(f.customer(t), "b", "00123")}
	type outcome struct {
		receipt surveyport.SubmissionReceipt
		err     error
	}
	outcomes := make([]outcome, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			outcomes[i].receipt, outcomes[i].err = f.submissions.Submit(ctx, commands[i])
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, out := range outcomes {
		if out.err == nil {
			if winner != -1 {
				t.Fatal("code consumed twice")
			}
			winner = i
		} else {
			var field *surveyport.FieldError
			if !errors.As(out.err, &field) || field.Code != "redemption_code_used" {
				t.Fatalf("loser err=%v", out.err)
			}
		}
	}
	if winner < 0 {
		t.Fatalf("no winner: %+v", outcomes)
	}
	var submissions, claims int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM survey_submissions`).Scan(&submissions)
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM survey_submission_claims`).Scan(&claims)
	if submissions != 1 || claims != 1 {
		t.Fatalf("submissions=%d claims=%d", submissions, claims)
	}
	receipt, e := f.submissions.Submit(ctx, commands[winner])
	if e != nil || receipt.SubmissionID != outcomes[winner].receipt.SubmissionID {
		t.Fatalf("receipt replay=%+v err=%v", receipt, e)
	}
	changed := commands[winner]
	changed.Answers = []surveyport.SubmissionAnswer{{QuestionID: f.q.Questions[0].ID, TextValue: "ABC"}}
	if _, e = f.submissions.Submit(ctx, changed); !errors.Is(e, surveyport.ErrConflict) {
		t.Fatalf("changed-key-payload accepted: %v", e)
	}
	var field *surveyport.FieldError
	if !errors.As(e, &field) || field.Code != "submission_payload_conflict" {
		t.Fatalf("wrong payload conflict=%v", e)
	}
	_, e = f.submissions.Submit(ctx, f.command(f.customer(t), "d", "unknown"))
	if !errors.As(e, &field) || field.Code != "redemption_code_invalid" {
		t.Fatalf("unknown code err=%v", e)
	}
	result, e = f.definitions.ImportRedemptionCodes(ctx, f.q.ID, f.actor, "redemption-import-0002", book)
	if e != nil || result.Imported != 0 || result.Duplicates != 3 {
		t.Fatalf("reimport=%+v err=%v", result, e)
	}
	page, e := f.definitions.ListRedemptionCodes(ctx, f.q.ID, 100, 0)
	if e != nil || page.Total != 2 || page.Available != 1 || page.Used != 1 {
		t.Fatalf("page=%+v err=%v", page, e)
	}
	var ciphertext []byte
	_ = f.pool.QueryRow(ctx, `SELECT text_value_ciphertext FROM survey_submission_answers LIMIT 1`).Scan(&ciphertext)
	text, e := f.repo.cipher.Decrypt(ciphertext)
	if e != nil || text != "兑换码已验证" {
		t.Fatalf("credential copied into answer: %q %v", text, e)
	}
}
func TestPostgreSQLRedemptionRollbackAndQuestionnaireIsolation(t *testing.T) {
	f := newRedemptionFixture(t)
	ctx := context.Background()
	_, e := f.definitions.ImportRedemptionCodes(ctx, f.q.ID, f.actor, "redemption-import-rollback", redemptionWorkbook(t, "ROLLBACK"))
	if e != nil {
		t.Fatal(e)
	}
	command := f.command(f.customer(t), "r", "ROLLBACK")
	forced := errors.New("forced rollback")
	e = f.unit.Within(ctx, func(tx context.Context) error {
		_, _, e := f.repo.CreateSubmission(tx, surveyapp.PersistSubmission{Questionnaire: f.q, Command: command, SubmissionKeyDigest: sha256.Sum256([]byte(command.SubmissionKey)), PayloadDigest: sha256.Sum256([]byte("rollback")), TokenDigest: sha256.Sum256([]byte("token")), Now: time.Now().UTC()})
		if e != nil {
			return e
		}
		return forced
	})
	if !errors.Is(e, forced) {
		t.Fatal(e)
	}
	page, e := f.definitions.ListRedemptionCodes(ctx, f.q.ID, 100, 0)
	if e != nil || page.Available != 1 || page.Used != 0 {
		t.Fatalf("rollback consumed code: %+v %v", page, e)
	}
	receipt, e := f.submissions.Submit(ctx, command)
	if e != nil || receipt.SubmissionID < 1 {
		t.Fatalf("rollback prevented retry: %+v %v", receipt, e)
	}
	copy, e := f.definitions.Duplicate(ctx, f.q.ID, f.actor, "redemption-copy-0001")
	if e != nil {
		t.Fatal(e)
	}
	page, e = f.definitions.ListRedemptionCodes(ctx, copy.ID, 100, 0)
	if e != nil || page.Total != 0 {
		t.Fatalf("copy inherited codes: %+v %v", page, e)
	}
	copy, e = f.definitions.Publish(ctx, copy.ID, copy.Version, f.actor, "redemption-copy-publish")
	if e != nil {
		t.Fatal(e)
	}
	cross := command
	cross.Slug = copy.Slug
	cross.DefinitionVersion = copy.DefinitionVersion
	cross.SubmissionKey = strings.Repeat("z", 43)
	cross.Answers = []surveyport.SubmissionAnswer{{QuestionID: copy.Questions[0].ID, TextValue: "ROLLBACK"}}
	_, e = f.submissions.Submit(ctx, cross)
	var field *surveyport.FieldError
	if !errors.As(e, &field) || field.Code != "redemption_code_invalid" {
		t.Fatalf("cross questionnaire code accepted: %v", e)
	}
}
