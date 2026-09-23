package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

type AudienceContact struct {
	CustomerID  customerdomain.CustomerID
	OwnerUserID string
	Status      string
	ObservedAt  time.Time
}

// AudienceContactReader deliberately exposes no external_userid. Segment only
// evaluates canonical customer facts and rechecks current qualification before
// outbound execution.
type AudienceContactReader interface {
	AudienceContacts(context.Context, time.Time) ([]AudienceContact, error)
}
