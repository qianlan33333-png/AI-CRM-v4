package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	if err != nil || copy.ID != 101 || copy.Status != surveyport.StatusDraft || !validSlug(copy.Slug) || !strings.HasPrefix(copy.Slug, "q-") || len(copy.Slug) != 34 || copy.Questions[0].ID != 0 {
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

func TestQuestionnaireCreateAssignsMissingSlugWithoutRelaxingExplicitSlugs(t *testing.T) {
	store := &lifecycleStore{}
	service := NewService(oauthUOW{}, store)
	definition := surveyport.Questionnaire{Name: "问卷", Title: "问卷", Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{{Type: surveyport.QuestionTextarea, Title: "需求", Required: true}}}
	command := surveyport.CreateCommand{Questionnaire: definition, ActorID: 3, IdempotencyKey: "new-editor-slug-create-001"}
	created, err := service.Create(context.Background(), command)
	if err != nil || !validSlug(created.Slug) || len(created.Slug) != 34 || !strings.HasPrefix(created.Slug, "q-") {
		t.Fatalf("generated slug=%q error=%v", created.Slug, err)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != created.ID || replay.Slug != created.Slug || len(store.created) != 1 {
		t.Fatalf("replay=%+v error=%v creates=%d", replay, err, len(store.created))
	}
	command.Questionnaire.Slug = "operator-chosen-slug"
	command.IdempotencyKey = "new-editor-slug-create-002"
	explicit, err := service.Create(context.Background(), command)
	if err != nil || explicit.Slug != command.Questionnaire.Slug {
		t.Fatalf("explicit slug changed: %q error=%v", explicit.Slug, err)
	}
	for _, slug := range []string{" ", "bad slug", "BadSlug"} {
		command.Questionnaire.Slug = slug
		command.IdempotencyKey = "new-editor-slug-invalid-001"
		if _, err := service.Create(context.Background(), command); !errors.Is(err, surveyport.ErrInvalid) {
			t.Fatalf("invalid explicit slug %q accepted: %v", slug, err)
		}
	}
	created.Slug = ""
	if _, err := service.Update(context.Background(), surveyport.UpdateCommand{Questionnaire: created, ActorID: 3, ExpectedVersion: 1, IdempotencyKey: "new-editor-empty-slug-update"}); !errors.Is(err, surveyport.ErrInvalid) {
		t.Fatalf("update must not clear public identifier: %v", err)
	}
	if len(store.created) != 2 || len(store.receipts) != 2 {
		t.Fatal("rejected definitions reserved a receipt or created a record")
	}
}

func TestQuestionnaireRecursiveCopiesKeepIndependentSlugsAndReplay(t *testing.T) {
	originalSlug := strings.Repeat("copy-chain-", 11) + "origin"
	store := &lifecycleStore{source: surveyport.Questionnaire{ID: 7, Name: "原问卷", Title: "原问卷", Slug: originalSlug, Status: surveyport.StatusPublished, Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{{Type: surveyport.QuestionTextarea, Title: "需求", Required: true}}}}
	service := NewService(oauthUOW{}, store)
	// All copies happen at the same time: seconds cannot provide uniqueness.
	service.now = func() time.Time { return time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC) }
	seen := map[string]bool{originalSlug: true}
	original := store.source
	for i := range 20 {
		if i == 1 {
			store.source = original // A second same-source, same-second command.
		}
		sourceSlug := store.source.Slug
		key := fmt.Sprintf("questionnaire-recursive-copy-%04d", i)
		copy, err := service.Duplicate(context.Background(), store.source.ID, 3, key)
		if err != nil || !validSlug(copy.Slug) || len(copy.Slug) != 34 || !strings.HasPrefix(copy.Slug, "q-") || seen[copy.Slug] || strings.Contains(copy.Slug, "copy") {
			t.Fatalf("copy %d slug=%q err=%v", i, copy.Slug, err)
		}
		if store.source.Slug != sourceSlug {
			t.Fatal("copy changed the source link")
		}
		replay, err := service.Duplicate(context.Background(), store.source.ID, 3, key)
		if err != nil || replay.ID != copy.ID || replay.Slug != copy.Slug || len(store.created) != i+1 {
			t.Fatalf("copy replay %d result=%+v creates=%d err=%v", i, replay, len(store.created), err)
		}
		seen[copy.Slug] = true
		store.source = copy
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
