package provider

import (
	"context"
	"strings"
	"unicode"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

const maxAlipayAppIDLength = 128

// FakeAlipayAdapter is a deterministic, in-memory provider adapter for
// contract and integration tests. It represents a set of identities that a
// provider has already verified; it never opens a socket, reads credentials,
// calls an SDK, or performs an Alipay operation.
//
// The adapter is intentionally not wired into the runtime. A future live
// adapter must implement Provider separately and preserve the same fixed
// kind/scope contract after its own provider verification.
type FakeAlipayAdapter struct {
	appID          string
	verifiedUserID map[string]struct{}
}

var _ Provider = (*FakeAlipayAdapter)(nil)

// NewFakeAlipayAdapter creates a deterministic provider with the supplied
// already-verified Alipay user IDs. The app ID is encoded into the scoped
// identity key as alipay-app:<app-id>.
func NewFakeAlipayAdapter(appID string, verifiedUserIDs ...string) (*FakeAlipayAdapter, error) {
	if !validAlipayAppID(appID) {
		return nil, ErrInvalidRequest
	}
	verified := make(map[string]struct{}, len(verifiedUserIDs))
	for _, userID := range verifiedUserIDs {
		if !validProviderValue(userID) {
			return nil, ErrInvalidRequest
		}
		verified[userID] = struct{}{}
	}
	return &FakeAlipayAdapter{appID: appID, verifiedUserID: verified}, nil
}

func (adapter *FakeAlipayAdapter) Name() string {
	return NameAlipay
}

// Verify returns a provider-created VerifiedFact only for an ID preloaded as
// verified. The request cannot select the identity kind, scope, assurance, or
// source, and no network is attempted.
func (adapter *FakeAlipayAdapter) Verify(ctx context.Context, request IdentityRequest) (identitydomain.VerifiedFact, error) {
	if err := ctx.Err(); err != nil {
		return identitydomain.VerifiedFact{}, err
	}
	if adapter == nil || !validProviderValue(request.Value) {
		return identitydomain.VerifiedFact{}, ErrInvalidRequest
	}
	if _, ok := adapter.verifiedUserID[request.Value]; !ok {
		return identitydomain.VerifiedFact{}, ErrIdentityNotVerified
	}
	return identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind:   identitydomain.KindAlipayUserID,
		Scope:  "alipay-app:" + adapter.appID,
		Value:  request.Value,
		Source: SourceAlipay,
	})
}

func validAlipayAppID(value string) bool {
	return validProviderValue(value) &&
		len(value) <= maxAlipayAppIDLength &&
		!strings.ContainsAny(value, ":/\\")
}

func validProviderValue(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 1024 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}
