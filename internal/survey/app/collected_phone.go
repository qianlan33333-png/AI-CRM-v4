package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type CollectedPhoneAnswer struct {
	AnswerID, SubmissionID surveyport.ID
	CustomerID             *customerdomain.CustomerID
	Phone                  string
	CollectedAt            time.Time
}

type collectedPhoneAnswerStore interface {
	ListCollectedPhoneAnswersWithin(context.Context, int64, int64) ([]int64, error)
	ReadCollectedPhoneAnswerWithin(context.Context, int64) (CollectedPhoneAnswer, error)
}

// CollectedPhoneAnswerIDsWithin uses frozen answers, never the questionnaire's
// current questions, publication status, or completion-effect state.
func (s *SubmissionService) CollectedPhoneAnswerIDsWithin(ctx context.Context, after, through int64) ([]int64, error) {
	store, ok := s.store.(collectedPhoneAnswerStore)
	if !ok {
		return nil, surveyport.ErrUnavailable
	}
	return store.ListCollectedPhoneAnswersWithin(ctx, after, through)
}

func (s *SubmissionService) ReplayCollectedPhoneWithin(ctx context.Context, id int64) (identityport.DeclaredAttachResult, error) {
	store, ok := s.store.(collectedPhoneAnswerStore)
	if !ok || s.collectedPhones == nil {
		return identityport.DeclaredAttachResult{}, surveyport.ErrUnavailable
	}
	a, err := store.ReadCollectedPhoneAnswerWithin(ctx, id)
	if err != nil {
		return identityport.DeclaredAttachResult{}, err
	}
	if a.CustomerID == nil || *a.CustomerID < 1 {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
	}
	key := fmt.Sprintf("survey-phone:%d:%d", a.SubmissionID, a.AnswerID)
	result, err := s.collectedPhones.CollectPhoneWithin(ctx, customerport.CollectedPhoneCommand{DeclaredPhoneCommand: identityport.DeclaredPhoneCommand{
		CustomerID: *a.CustomerID, Phone: a.Phone, Source: "survey", SourceEventID: fmt.Sprintf("submission:%d:answer:%d", a.SubmissionID, a.AnswerID), IdempotencyKey: key}, CollectedAt: a.CollectedAt, Historical: true})
	if err != nil {
		return result, err
	}
	status := result.Status
	if status == identityport.DeclaredReplayed {
		status = result.ReplayOf
	}
	evidence := sha256.Sum256([]byte(key + "\x00" + string(status)))
	err = s.store.RecordPhoneBinding(ctx, a.SubmissionID, a.AnswerID, int64(*a.CustomerID), result.IdentityID, status, evidence, a.CollectedAt)
	return result, err
}
