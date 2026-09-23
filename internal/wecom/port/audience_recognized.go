package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

// AudienceRecognizedContactReader reads attributed profiles within one explicit
// corp scope. Stale profiles qualify; conflicts never do. No follow relationship
// or active-contact requirement is implied, and no external IDs are returned.
type AudienceRecognizedContactReader interface {
	AudienceRecognizedContacts(context.Context, string, time.Time) ([]customerdomain.CustomerID, error)
}
