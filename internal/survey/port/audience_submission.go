package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

// AudienceSubmission is a resolved canonical submission, regardless of answer
// types or earlier submissions. It never exposes submitted text or external IDs.
type AudienceSubmission struct {
	CustomerID      customerdomain.CustomerID
	QuestionnaireID ID
	SubmissionID    ID
	StaffID         string
	SubmittedAt     time.Time
}
type AudienceSubmissionReader interface {
	AudienceSubmissions(context.Context, time.Time) ([]AudienceSubmission, error)
}
