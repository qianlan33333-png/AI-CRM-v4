package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type AudienceResolutionStatus string

const (
	AudienceResolved   AudienceResolutionStatus = "resolved"
	AudienceUnresolved AudienceResolutionStatus = "unresolved"
	AudienceConflict   AudienceResolutionStatus = "conflict"
	AudienceInvalid    AudienceResolutionStatus = "invalid"
)

type AudienceResolution struct {
	Status     AudienceResolutionStatus
	CustomerID customerdomain.CustomerID
	IdentityID int64
}

// AudienceVerifiedResolver accepts only a VerifiedFact minted by a trusted
// adapter. It deliberately exposes no provision, bind, or merge operation.
type AudienceVerifiedResolver interface {
	ResolveAudienceFact(context.Context, identitydomain.VerifiedFact) (AudienceResolution, error)
}

type OutboundIdentity struct {
	IdentityID int64
	CustomerID customerdomain.CustomerID
	Kind       identitydomain.Kind
	Scope      string
	Value      string
}

// OutboundIdentityReader is restricted to the privileged Outbound provider
// adapter. Returned values must never be persisted or structurally logged.
type OutboundIdentityReader interface {
	VerifiedOutboundIdentity(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (OutboundIdentity, bool, error)
}

// VerifiedOutboundPhoneReader is the narrow vault-backed phone read for an
// already-authorized Outbound Provider payload. It is deliberately separate
// from ExternalIdentityValueReader: a phone can never be exposed through the
// generic external-identity seam. Implementations must accept only the
// explicit CN11 scope, return a single active verified fact, and decrypt only
// in memory for the current transaction.
type VerifiedOutboundPhoneReader interface {
	VerifiedOutboundPhone(context.Context, customerdomain.CustomerID, string) (string, bool, error)
}
