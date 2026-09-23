package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

// CommerceResolveStatus is deliberately coarse. Commerce callers receive only
// internal identifiers and a safe outcome; external identity values never
// cross back out of Identity.
type CommerceResolveStatus string

const (
	CommerceResolved CommerceResolveStatus = "resolved"
	CommerceNotFound CommerceResolveStatus = "not_found"
	CommercePartial  CommerceResolveStatus = "partial"
	CommerceConflict CommerceResolveStatus = "conflict"
	CommerceInvalid  CommerceResolveStatus = "invalid"
)

const MaximumCommerceReferences = 20

type CommerceReferenceSet struct {
	References []identitydomain.Reference
}

type CommerceIdentityMatch struct {
	Position   int
	IdentityID int64
	CustomerID customerdomain.CustomerID
	Assurance  identitydomain.Assurance
}

type CommerceResolution struct {
	Status     CommerceResolveStatus
	CustomerID customerdomain.CustomerID
	Matches    []CommerceIdentityMatch
}

// CommerceResolver resolves a set of exact, scoped external identities to one
// canonical customer root. It never provisions, links, or merges identities.
type CommerceResolver interface {
	ResolveCommerce(context.Context, CommerceReferenceSet) (CommerceResolution, error)
}

type VerifiedCommerceIdentity struct {
	IdentityID int64
	CustomerID customerdomain.CustomerID
	Kind       identitydomain.Kind
	Scope      string
	Value      string
}

// PaymentIdentityReader is restricted to trusted Provider adapters. It is the
// only commerce seam that can reveal a verified provider identifier.
type PaymentIdentityReader interface {
	VerifiedPaymentIdentity(context.Context, int64, identitydomain.Kind, string) (VerifiedCommerceIdentity, bool, error)
}
