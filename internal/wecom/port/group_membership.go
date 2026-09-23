package port

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

var ErrGroupMembershipUnavailable = errors.New("current complete group membership unavailable")

// GroupMembership is connector-only input, never serialized to logs or HTTP.
type GroupMembership struct {
	ChatID          string
	ExternalUserIDs []string
}
type GroupMembershipProvider interface {
	ReadGroupMembership(context.Context, string) (GroupMembership, error)
}
type AudienceGroupMembership struct {
	CustomerIDs                    []customerdomain.CustomerID
	ObservedAt                     time.Time
	ExternalCount, UnresolvedCount int
	Complete                       bool
	ProviderComplete               bool
	ExternalIdentityHashes         []string `json:"-"`
}

// Reads local Owner facts only; missing, stale or failed snapshots are errors.
type AudienceGroupMembershipReader interface {
	AudienceGroupMembership(context.Context, string, string, time.Time, time.Duration) (AudienceGroupMembership, error)
}

// Unknown candidates are omitted from OutsideCustomerIDs and never selected.
type CandidateGroupMembershipReader interface {
	OutsideGroupCandidates(context.Context, string, string, time.Time, time.Duration, []customerdomain.CustomerID) ([]customerdomain.CustomerID, error)
}
