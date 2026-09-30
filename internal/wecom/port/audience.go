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
	// FollowedAt is the verified Provider/callback friend-add time. It is nil
	// when this relationship has no trustworthy add-time evidence.
	FollowedAt *time.Time
}

// AudienceContactReader deliberately exposes no external_userid. Segment only
// evaluates canonical customer facts and rechecks current qualification before
// outbound execution.
type AudienceContactReader interface {
	AudienceContacts(context.Context, time.Time) ([]AudienceContact, error)
}

const MaxAudienceContactCustomerIDs = 5000

// AudienceContactsForCustomersReader is a bounded refinement for consumers
// that already resolved a small canonical Customer set. It avoids loading a
// full directory on every paid-order audience refresh.
type AudienceContactsForCustomersReader interface {
	AudienceContactsForCustomers(context.Context, time.Time, []customerdomain.CustomerID) ([]AudienceContact, error)
}
