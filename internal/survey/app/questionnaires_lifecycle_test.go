package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type lifecycleStore struct {
	Store
	source    surveyport.Questionnaire
	receipts  map[string]Receipt
	created   []surveyport.Questionnaire
	published int
}

func receiptMapKey(d [32]byte) string { return hex.EncodeToString(d[:]) }
func (s *lifecycleStore) Reserve(_ context.Context, in Reservation) (Receipt, bool, error) {
	if s.receipts == nil {
		s.receipts = map[string]Receipt{}
	}
	key := receiptMapKey(in.KeyDigest)
	if prior, ok := s.receipts[key]; ok {
		return prior, false, nil
	}
	receipt := Receipt{ID: int64(len(s.receipts) + 1), Operation: in.Operation, ActorScope: in.ActorScope, State: "reserved", KeyDigest: in.KeyDigest, PayloadDigest: in.PayloadDigest}
	s.receipts[key] = receipt
	return receipt, true, nil
}
func (s *lifecycleStore) Complete(_ context.Context, id int64, result json.RawMessage, _ time.Time) (Receipt, error) {
	for key, receipt := range s.receipts {
		if receipt.ID == id {
			receipt.State, receipt.Result = "completed", append(json.RawMessage(nil), result...)
			s.receipts[key] = receipt
			return receipt, nil
		}
	}
	return Receipt{}, surveyport.ErrNotFound
}
func (s *lifecycleStore) AppendAuditAndOutbox(context.Context, string, surveyport.ID, string, json.RawMessage, string, time.Time) error {
	return nil
}
func (s *lifecycleStore) Get(context.Context, surveyport.ID, bool) (surveyport.Questionnaire, error) {
	return s.source, nil
}
func (s *lifecycleStore) Create(_ context.Context, q surveyport.Questionnaire, _ int64, _ time.Time) (surveyport.Questionnaire, error) {
	q.ID = surveyport.ID(100 + len(s.created))
	s.created = append(s.created, q)
	return q, nil
}
func (s *lifecycleStore) Publish(_ context.Context, id surveyport.ID, expected, _ int64, _ time.Time) (surveyport.Questionnaire, error) {
	s.published++
	s.source.ID, s.source.Version, s.source.Status = id, expected+1, surveyport.StatusPublished
	return s.source, nil
}
func (s *lifecycleStore) SetStatus(_ context.Context, id surveyport.ID, status surveyport.QuestionnaireStatus, expected, _ int64, _ time.Time) (surveyport.Questionnaire, error) {
	s.source.ID, s.source.Version, s.source.Status = id, expected+1, status
	return s.source, nil
}

func TestQuestionnaireLifecycleCreatesDuplicatesAndPublishesIdempotently(t *testing.T) {
	question := surveyport.Question{Type: surveyport.QuestionTextarea, Title: "需求", Required: true, SortOrder: 0}
	store := &lifecycleStore{source: surveyport.Questionnaire{ID: 7, Name: "增长问卷", Title: "增长问卷", Slug: "growth", Status: surveyport.StatusPublished, Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{question}}}
	service := NewService(oauthUOW{}, store)
	service.now = func() time.Time { return time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC) }
	created, err := service.Create(context.Background(), surveyport.CreateCommand{Questionnaire: surveyport.Questionnaire{Name: "新问卷", Title: "新问卷", Slug: "new-growth", Status: surveyport.StatusDraft, Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{question}}, ActorID: 3, IdempotencyKey: "questionnaire-create-lifecycle-0001"})
	if err != nil || created.ID != 100 {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	copy, err := service.Duplicate(context.Background(), 7, 3, "questionnaire-duplicate-lifecycle-0002")
	if err != nil || copy.ID != 101 || copy.Status != surveyport.StatusDraft || copy.Slug != "growth-copy-1788570123" || copy.Questions[0].ID != 0 {
		t.Fatalf("duplicate=%+v err=%v", copy, err)
	}
	replay, err := service.Duplicate(context.Background(), 7, 3, "questionnaire-duplicate-lifecycle-0002")
	if err != nil || replay.ID != copy.ID || replay.Slug != copy.Slug || len(store.created) != 2 {
		t.Fatalf("duplicate replay=%+v err=%v creates=%d", replay, err, len(store.created))
	}
	published, err := service.Publish(context.Background(), 7, 1, 3, "questionnaire-publish-lifecycle-0003")
	if err != nil || published.Status != surveyport.StatusPublished || store.published != 1 {
		t.Fatalf("publish=%+v err=%v calls=%d", published, err, store.published)
	}
}

func TestQuestionnaireArchiveIsTerminalAndIdempotent(t *testing.T) {
	question := surveyport.Question{Type: surveyport.QuestionTextarea, Title: "需求", Required: true, SortOrder: 0}
	store := &lifecycleStore{source: surveyport.Questionnaire{ID: 7, Name: "增长问卷", Title: "增长问卷", Slug: "growth", Status: surveyport.StatusPublished, Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{question}}}
	service := NewService(oauthUOW{}, store)
	archive, err := service.SetStatus(context.Background(), 7, 1, surveyport.StatusArchived, 3, "questionnaire-archive-lifecycle-0001")
	if err != nil {
		t.Fatalf("archive err=%v", err)
	}
	if archive.Status != surveyport.StatusArchived || archive.Version != 2 {
		t.Fatalf("archive=%+v", archive)
	}
	replay, err := service.SetStatus(context.Background(), 7, 1, surveyport.StatusArchived, 3, "questionnaire-archive-lifecycle-0001")
	if err != nil || replay.ID != archive.ID || replay.Status != surveyport.StatusArchived || replay.Version != archive.Version {
		t.Fatalf("archive replay=%+v err=%v", replay, err)
	}
	if _, err = service.SetStatus(context.Background(), 7, archive.Version, surveyport.StatusPublished, 3, "questionnaire-enable-after-archive-0002"); err != surveyport.ErrNotFound {
		t.Fatalf("enable archived err=%v, want not found", err)
	}
	if _, err = service.Duplicate(context.Background(), 7, 3, "questionnaire-copy-after-archive-0003"); err != surveyport.ErrNotFound {
		t.Fatalf("copy archived err=%v, want not found", err)
	}
}

func TestAssessmentRetirementRejectsNewUseAndPreservesHistoricalRead(t *testing.T) {
	store := &lifecycleStore{source: surveyport.Questionnaire{ID: 7, Name: "历史测评", Title: "历史测评", Slug: "historical-assessment", Status: surveyport.StatusDraft, Version: 1, Mode: surveyport.ModeAssessment, AnswerDisplayMode: surveyport.DisplayAllInOne}}
	service := NewService(oauthUOW{}, store)
	ctx := context.Background()
	if _, err := service.Create(ctx, surveyport.CreateCommand{Questionnaire: store.source, ActorID: 3, IdempotencyKey: "retired-assessment-create"}); !errors.Is(err, surveyport.ErrInvalid) {
		t.Fatalf("create error=%v", err)
	}
	if _, err := service.Duplicate(ctx, 7, 3, "retired-assessment-copy"); !errors.Is(err, surveyport.ErrNotFound) {
		t.Fatalf("duplicate error=%v", err)
	}
	if _, err := service.Publish(ctx, 7, 1, 3, "retired-assessment-publish"); !errors.Is(err, surveyport.ErrInvalid) {
		t.Fatalf("publish error=%v", err)
	}
	if len(store.created) != 0 || store.published != 0 {
		t.Fatal("retired assessment wrote a definition")
	}
	if value, err := service.Get(ctx, 7); err != nil || value.ID != 7 {
		t.Fatalf("historical read=%+v error=%v", value, err)
	}
}
