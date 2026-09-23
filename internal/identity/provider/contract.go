// Package provider contains the trusted adapter boundary for external
// identity providers.
//
// Provider adapters are the only layer that may turn provider-verified data
// into an identitydomain.VerifiedFact. This package deliberately contains no
// HTTP client, SDK, credentials, callback handler, or payment operation.
package provider

import (
	"context"
	"errors"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

var (
	// ErrInvalidRequest means that the adapter request cannot be converted into
	// a provider identity without guessing or canonicalization ambiguity.
	ErrInvalidRequest = errors.New("invalid provider identity request")
	// ErrIdentityNotVerified means that the fake (or a future real) provider
	// could not establish the identity from provider evidence.
	ErrIdentityNotVerified = errors.New("provider identity not verified")
)

const (
	// NameAlipay is the stable provider name used by adapter registries and
	// audit metadata. It does not enable any Alipay network integration.
	NameAlipay = "alipay"
	// SourceAlipay is the source attached to facts produced by this adapter.
	// The source is fixed by the adapter and is not accepted from a request.
	SourceAlipay = "alipay.provider"
)

// IdentityRequest is intentionally smaller than identitydomain.Reference.
// Callers provide only the opaque provider user identifier; kind, scope,
// assurance, and source are owned by the adapter. In particular, no HTTP DTO
// can use this contract to self-declare a verified identity.
type IdentityRequest struct {
	Value string
}

// Provider is the stable identity-provider contract. Implementations must
// perform provider-specific verification before constructing a
// VerifiedFact. A provider adapter must not perform CRM writes or hold a
// database transaction while talking to an external provider.
type Provider interface {
	Name() string
	Verify(context.Context, IdentityRequest) (identitydomain.VerifiedFact, error)
}
