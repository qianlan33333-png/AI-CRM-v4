package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// Customer profile observations intentionally retain Provider identifiers only
// inside the WeCom boundary. Composition adapters must replace them with safe
// display names before returning Customer-domain DTOs.
type OwnerObservation struct {
	EmployeeID string
	Status     string
	ObservedAt time.Time
}

type TagObservation struct {
	ProviderTagID string
	ObservedName  string
	ProviderType  int16
	Status        string
	ObservedAt    time.Time
}

type CustomerProfileObservationReader interface {
	CustomerOwnerObservations(context.Context, customerdomain.CustomerID) ([]OwnerObservation, error)
	CustomerTagObservations(context.Context, customerdomain.CustomerID) ([]TagObservation, error)
}

// CustomerTagObservationRefresher reads the current Provider contact after a
// completed mark_tag call and writes only WeCom-owned observations. It never
// changes the already-recorded outbound command result or calls mark_tag.
type CustomerTagObservationRefresher interface {
	RefreshCustomerTagObservation(context.Context, string, customerdomain.CustomerID, string, string) error
}

// ProviderTagCustomerLister exposes only canonical Customer IDs with an
// active, completed WeCom observation for one already-known Provider tag ID.
// It never maps a local tag name, creates a Customer, or modifies a Provider
// observation.  The caller must obtain that Provider ID through Tag's binding
// port rather than guessing from names.
type ProviderTagCustomerLister interface {
	ListCustomerIDsForProviderTag(context.Context, string, int) ([]customerdomain.CustomerID, error)
}

// AudiencePrimaryOwner is the provider userid selected from a completed,
// trusted directory scope for a canonical customer.  Ambiguous means active
// provider scopes disagree and must never be resolved by choosing a row.
type AudiencePrimaryOwner struct {
	CustomerID  customerdomain.CustomerID
	CorpScope   string
	OwnerUserID string
	Status      string // known, unknown, ambiguous
	// VersionDigest freezes the completed trusted profile/run fact selected by
	// WeCom. Consumers use it for optimistic candidate checks without reading
	// WeCom tables. It is opaque and never contains a provider identifier.
	VersionDigest [32]byte
}

// AudiencePrimaryOwnerReader is a bulk, read-only audience fact port.  It
// retains the provider corp scope inside WeCom and exposes only canonical
// CustomerIDs plus the provider userid needed for owner filtering.
type AudiencePrimaryOwnerReader interface {
	AudiencePrimaryOwners(context.Context, []customerdomain.CustomerID) ([]AudiencePrimaryOwner, error)
}

// CustomerBusinessDetail is a completed WeCom directory projection for an
// explicitly-authorized business-detail read. A missing profile is reported
// as coverage state; it is never converted into an invented empty remark.
type CustomerBusinessDetail struct {
	CustomerID         customerdomain.CustomerID
	CorpScope          string
	Availability       string // available, missing
	AvailabilityReason string // empty when available
	FollowUsers        []CustomerFollowUser
}

// CustomerFollowUser is a trusted provider observation. Remark is scoped to
// this employee and nil when WeCom did not provide a remark.
type CustomerFollowUser struct {
	EmployeeID string
	Remark     *string
}

// CustomerBusinessDetailReader exposes only completed WeCom observations.
// Primary-owner selection remains with AudiencePrimaryOwnerReader, where its
// cross-scope ambiguity rules are already owned.
type CustomerBusinessDetailReader interface {
	CustomerBusinessDetails(context.Context, []customerdomain.CustomerID) ([]CustomerBusinessDetail, error)
}

// OwnerHandoffPrimaryOwnerLister enumerates canonical customers whose current
// completed WeCom profile has one unambiguous primary owner in the requested
// corp scope. It is read-only: Customer applies its own local-owner precedence
// after this discovery and this Port never creates or changes an observation.
type OwnerHandoffPrimaryOwnerLister interface {
	ListOwnerHandoffPrimaryOwnerCustomerIDs(context.Context, string, string, int) ([]customerdomain.CustomerID, error)
}
