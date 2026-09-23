package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type AudienceChoiceAnswer struct {
	CustomerID      customerdomain.CustomerID
	QuestionnaireID ID
	SubmissionID    ID
	StaffID         string
	SubmittedAt     time.Time
	QuestionID      ID
	OptionIDs       []ID
}
type AudienceChoiceAnswerReader interface {
	FirstCompleteAudienceChoices(context.Context, time.Time) ([]AudienceChoiceAnswer, error)
}
