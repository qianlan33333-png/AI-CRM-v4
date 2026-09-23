package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// AudienceReader is a Customer-owned, read-only projection boundary. It emits
// canonical-looking local IDs only; Segment still resolves aliases before use.
type AudienceReader interface {
	ActiveWithin(context.Context, time.Time, int) ([]customerdomain.CustomerID, time.Time, error)
}

// AudienceRegistrationFact is a safe, directory-projection-only fact for the
// frozen WeCom registration template. It reports presence, never a phone
// number or an identity assurance. Known is false when no canonical directory
// projection exists, which callers must treat as unknown rather than false.
type AudienceRegistrationFact struct {
	CustomerID customerdomain.CustomerID
	Known      bool
	Registered bool
	Source     string
	UpdatedAt  time.Time
}

const MaxAudienceRegistrationCustomerIDs = 5000

// AudienceRegistrationReader accepts canonical Customer IDs only. It neither
// resolves identities nor creates projections.
type AudienceRegistrationReader interface {
	AudienceRegistrationFacts(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]AudienceRegistrationFact, error)
}
