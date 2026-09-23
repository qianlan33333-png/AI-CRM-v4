package port

import (
	"context"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

const MaxSharedFactsCustomerIDs = 500

var ErrSharedFactsBatchTooLarge = errors.New("too many customer IDs for HXC shared facts")
var ErrSharedFactsVersionUnavailable = errors.New("HXC shared facts version is unavailable")

// SharedFactsAvailability prevents pre-0084 generations and identity conflicts
// from being presented as known false or zero values.
type SharedFactsAvailability string

const (
	SharedFactsAvailable   SharedFactsAvailability = "available"
	SharedFactsUnavailable SharedFactsAvailability = "unavailable"
	SharedFactsAmbiguous   SharedFactsAvailability = "ambiguous"
)

// SharedFacts contains only canonical-customer facts. External HXC identifiers
// remain inside the HXC owner and are never returned to Product or Segment.
type SharedFacts struct {
	CustomerID      customerdomain.CustomerID
	Availability    SharedFactsAvailability
	SourceAsOf      time.Time
	SourceUpdatedAt time.Time

	FormallyLoggedIn                       bool
	FormalLoginAt                          *time.Time
	HasTokenUsage                          bool
	LearningPlanFound                      bool
	LearningPlanStatus                     string
	LearningPlanCurrent, LearningPlanTotal *int64
	CardOpenCount7D                        int64
	CardLastOpenedAt                       *time.Time

	MembershipRecordFound bool
	IsMember              bool
	MembershipSource      string
	MembershipStatus      string
	Tier                  string
	ExpiresAt             *time.Time
	Registered            bool
	HasRealUsage          bool
	LastUsedAt            *time.Time
}

// SharedFactsReader reads one atomically published HXC generation for a
// bounded canonical CustomerID set. It never resolves external identities.
type SharedFactsReader interface {
	SharedFacts(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]SharedFacts, error)
}

// VersionedSharedFactsReader lets a consumer pin one immutable publication
// before splitting a large canonical-customer set into bounded reads.
type VersionedSharedFactsReader interface {
	CurrentSharedFactsVersion(context.Context) (int64, error)
	SharedFactsAtVersion(context.Context, int64, []customerdomain.CustomerID) (map[customerdomain.CustomerID]SharedFacts, error)
}

// ActiveAt and ExpiredAt preserve the old membership predicates at the time a
// consumer evaluates them; dashboard stage is deliberately not reused.
func (facts SharedFacts) ActiveAt(reference time.Time) bool {
	return facts.Availability == SharedFactsAvailable && facts.IsMember && (facts.ExpiresAt == nil || facts.ExpiresAt.After(reference))
}

func (facts SharedFacts) ExpiredAt(reference time.Time) bool {
	if facts.Availability != SharedFactsAvailable {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(facts.MembershipStatus), "expired") {
		return true
	}
	return facts.MembershipRecordFound && facts.ExpiresAt != nil && !facts.ExpiresAt.After(reference)
}
