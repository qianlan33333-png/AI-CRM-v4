package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

const DefaultLimit int32 = 50
const MaximumLimit int32 = 200
const MaximumOffset int32 = 1_000_000

type Receipt struct {
	ID                           int64
	Operation, ActorScope, State string
	KeyDigest, PayloadDigest     [32]byte
	Result                       json.RawMessage
}

type Reservation struct {
	Operation, ActorScope    string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}

type Store interface {
	List(context.Context, int32, int32, string, surveyport.QuestionnaireStatus) ([]surveyport.Questionnaire, int64, error)
	Get(context.Context, surveyport.ID, bool) (surveyport.Questionnaire, error)
	Create(context.Context, surveyport.Questionnaire, int64, time.Time) (surveyport.Questionnaire, error)
	Replace(context.Context, surveyport.Questionnaire, int64, int64, time.Time) (surveyport.Questionnaire, error)
	Publish(context.Context, surveyport.ID, int64, int64, time.Time) (surveyport.Questionnaire, error)
	SetStatus(context.Context, surveyport.ID, surveyport.QuestionnaireStatus, int64, int64, time.Time) (surveyport.Questionnaire, error)
	DeleteDraft(context.Context, surveyport.ID, int64) error
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
	AppendAuditAndOutbox(context.Context, string, surveyport.ID, string, json.RawMessage, string, time.Time) error
}

type Service struct {
	uow   platformport.UnitOfWork
	store Store
	now   func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store) *Service {
	return &Service{uow: uow, store: store, now: time.Now}
}

func (s *Service) List(ctx context.Context, limit, offset int32, search string, status surveyport.QuestionnaireStatus) (surveyport.Page, error) {
	if s == nil || s.uow == nil || s.store == nil {
		return surveyport.Page{}, surveyport.ErrUnavailable
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	search = strings.TrimSpace(search)
	if limit < 1 || limit > MaximumLimit || offset < 0 || offset > MaximumOffset || len([]rune(search)) > 200 || status != "" && !validStatus(status) {
		return surveyport.Page{}, surveyport.ErrInvalid
	}
	result := surveyport.Page{Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Items, result.Total, err = s.store.List(tx, limit, offset, search, status)
		return err
	})
	if err != nil {
		return surveyport.Page{}, classify(err)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, id surveyport.ID) (surveyport.Questionnaire, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.Questionnaire{}, surveyport.ErrNotFound
	}
	var result surveyport.Questionnaire
	err := s.uow.Within(ctx, func(tx context.Context) error { var err error; result, err = s.store.Get(tx, id, false); return err })
	return result, classify(err)
}

func (s *Service) Create(ctx context.Context, command surveyport.CreateCommand) (surveyport.Questionnaire, error) {
	value := command.Questionnaire
	value.ID, value.CreatedBy, value.Version = 0, command.ActorID, 1
	if value.Status == "" {
		value.Status = surveyport.StatusDraft
	}
	if command.ActorID < 1 || !validKey(command.IdempotencyKey) || value.Status != surveyport.StatusDraft && value.Status != surveyport.StatusDisabled || value.Mode != surveyport.ModeSurvey || surveydomain.ValidateQuestionnaire(value) != nil {
		return surveyport.Questionnaire{}, surveyport.ErrInvalid
	}
	payload, digest, err := digestPayload(value)
	if err != nil {
		return surveyport.Questionnaire{}, surveyport.ErrInvalid
	}
	return s.mutate(ctx, "definition_create", 0, command.ActorID, command.IdempotencyKey, digest, payload, func(tx context.Context, now time.Time) (surveyport.Questionnaire, error) {
		return s.store.Create(tx, value, command.ActorID, now)
	})
}

func (s *Service) Update(ctx context.Context, command surveyport.UpdateCommand) (surveyport.Questionnaire, error) {
	value := command.Questionnaire
	if value.ID < 1 || command.ActorID < 1 || command.ExpectedVersion < 1 || !validKey(command.IdempotencyKey) || value.Mode != surveyport.ModeSurvey || surveydomain.ValidateQuestionnaire(value) != nil {
		return surveyport.Questionnaire{}, surveyport.ErrInvalid
	}
	payload, digest, err := digestPayload(struct {
		Questionnaire surveyport.Questionnaire `json:"questionnaire"`
		Expected      int64                    `json:"expected_version"`
	}{value, command.ExpectedVersion})
	if err != nil {
		return surveyport.Questionnaire{}, surveyport.ErrInvalid
	}
	return s.mutate(ctx, "definition_update", value.ID, command.ActorID, command.IdempotencyKey, digest, payload, func(tx context.Context, now time.Time) (surveyport.Questionnaire, error) {
		current, err := s.store.Get(tx, value.ID, true)
		if err != nil {
			return surveyport.Questionnaire{}, err
		}
		if current.Mode != surveyport.ModeSurvey {
			return surveyport.Questionnaire{}, surveyport.ErrInvalid
		}
		if current.Status == surveyport.StatusArchived {
			return surveyport.Questionnaire{}, surveyport.ErrNotFound
		}
		return s.store.Replace(tx, value, command.ExpectedVersion, command.ActorID, now)
	})
}

func (s *Service) Duplicate(ctx context.Context, id surveyport.ID, actor int64, key string) (surveyport.Questionnaire, error) {
	if id < 1 || actor < 1 || !validKey(key) {
		return surveyport.Questionnaire{}, surveyport.ErrInvalid
	}
	payload, digest, _ := digestPayload(struct {
		ID surveyport.ID `json:"id"`
	}{id})
	return s.mutate(ctx, "definition_duplicate", id, actor, key, digest, payload, func(tx context.Context, now time.Time) (surveyport.Questionnaire, error) {
		source, err := s.store.Get(tx, id, true)
		if err != nil {
			return surveyport.Questionnaire{}, err
		}
		if source.Status == surveyport.StatusArchived || source.Mode != surveyport.ModeSurvey {
			return surveyport.Questionnaire{}, surveyport.ErrNotFound
		}
		source.ID, source.Version, source.CreatedBy = 0, 1, actor
		source.Name += " copy"
		source.Title += " 副本"
		source.Slug = fmt.Sprintf("%s-copy-%d", strings.TrimSuffix(source.Slug, "-"), now.Unix())
		source.Status = surveyport.StatusDraft
		for qi := range source.Questions {
			source.Questions[qi].ID = 0
			for oi := range source.Questions[qi].Options {
				source.Questions[qi].Options[oi].ID = 0
			}
		}
		return s.store.Create(tx, source, actor, now)
	})
}

func (s *Service) Publish(ctx context.Context, id surveyport.ID, expected, actor int64, key string) (surveyport.Questionnaire, error) {
	return s.statusMutation(ctx, "definition_publish", id, expected, actor, key, surveyport.StatusPublished, true)
}

func (s *Service) SetStatus(ctx context.Context, id surveyport.ID, expected int64, status surveyport.QuestionnaireStatus, actor int64, key string) (surveyport.Questionnaire, error) {
	if status != surveyport.StatusPublished && status != surveyport.StatusDisabled && status != surveyport.StatusArchived {
		return surveyport.Questionnaire{}, surveyport.ErrInvalid
	}
	operation := "definition_enable"
	if status == surveyport.StatusDisabled {
		operation = "definition_disable"
	} else if status == surveyport.StatusArchived {
		operation = "definition_archive"
	}
	return s.statusMutation(ctx, operation, id, expected, actor, key, status, false)
}

func (s *Service) statusMutation(ctx context.Context, operation string, id surveyport.ID, expected, actor int64, key string, status surveyport.QuestionnaireStatus, publish bool) (surveyport.Questionnaire, error) {
	if id < 1 || expected < 1 || actor < 1 || !validKey(key) {
		return surveyport.Questionnaire{}, surveyport.ErrInvalid
	}
	payload, digest, _ := digestPayload(struct {
		ID       surveyport.ID                  `json:"id"`
		Expected int64                          `json:"expected_version"`
		Status   surveyport.QuestionnaireStatus `json:"status"`
	}{id, expected, status})
	return s.mutate(ctx, operation, id, actor, key, digest, payload, func(tx context.Context, now time.Time) (surveyport.Questionnaire, error) {
		current, err := s.store.Get(tx, id, true)
		if err != nil {
			return surveyport.Questionnaire{}, err
		}
		if status == surveyport.StatusPublished && current.Mode != surveyport.ModeSurvey {
			return surveyport.Questionnaire{}, surveyport.ErrInvalid
		}
		if current.Status == surveyport.StatusArchived {
			return surveyport.Questionnaire{}, surveyport.ErrNotFound
		}
		if publish {
			return s.store.Publish(tx, id, expected, actor, now)
		}
		return s.store.SetStatus(tx, id, status, expected, actor, now)
	})
}

func (s *Service) DeleteDraft(ctx context.Context, id surveyport.ID, expected, actor int64, key string) error {
	if id < 1 || expected < 1 || actor < 1 || !validKey(key) {
		return surveyport.ErrInvalid
	}
	payload, digest, _ := digestPayload(struct {
		ID       surveyport.ID `json:"id"`
		Expected int64         `json:"expected_version"`
	}{id, expected})
	_, err := s.mutate(ctx, "definition_delete", id, actor, key, digest, payload, func(tx context.Context, now time.Time) (surveyport.Questionnaire, error) {
		if err := s.store.DeleteDraft(tx, id, expected); err != nil {
			return surveyport.Questionnaire{}, err
		}
		return surveyport.Questionnaire{ID: id, Version: expected}, nil
	})
	return err
}

func (s *Service) mutate(ctx context.Context, operation string, aggregate surveyport.ID, actor int64, key string, digest [32]byte, eventPayload json.RawMessage, work func(context.Context, time.Time) (surveyport.Questionnaire, error)) (surveyport.Questionnaire, error) {
	if s == nil || s.uow == nil || s.store == nil || work == nil {
		return surveyport.Questionnaire{}, surveyport.ErrUnavailable
	}
	now := s.now().UTC()
	actorScope := fmt.Sprintf("admin:%d", actor)
	reservation := Reservation{Operation: operation, ActorScope: actorScope, KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: digest, CreatedAt: now}
	var result surveyport.Questionnaire
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation)
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], digest[:]) != 1 {
			return surveyport.ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.Result, &result) != nil {
				return surveyport.ErrUnavailable
			}
			return nil
		}
		result, err = work(tx, now)
		if err != nil {
			return err
		}
		snapshot, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if aggregate == 0 {
			aggregate = result.ID
		}
		outboxKey := "survey." + operation + ":" + hex.EncodeToString(reservation.KeyDigest[:])
		if err = s.store.AppendAuditAndOutbox(tx, operation, aggregate, actorScope, eventPayload, outboxKey, now); err != nil {
			return err
		}
		completed, err := s.store.Complete(tx, receipt.ID, snapshot, now)
		if err != nil || completed.State != "completed" {
			return surveyport.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return surveyport.Questionnaire{}, classify(err)
	}
	return result, nil
}

func digestPayload(v any) (json.RawMessage, [32]byte, error) {
	raw, err := json.Marshal(v)
	return raw, sha256.Sum256(raw), err
}
func validKey(v string) bool { v = strings.TrimSpace(v); return len(v) >= 16 && len(v) <= 200 }
func validStatus(v surveyport.QuestionnaireStatus) bool {
	return v == surveyport.StatusDraft || v == surveyport.StatusPublished || v == surveyport.StatusDisabled || v == surveyport.StatusArchived
}
func classify(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{surveyport.ErrInvalid, surveyport.ErrNotFound, surveyport.ErrConflict, surveyport.ErrReferenced, surveyport.ErrUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return surveyport.ErrUnavailable
}

var _ surveyport.DefinitionApplication = (*Service)(nil)
