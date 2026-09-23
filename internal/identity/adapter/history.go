package adapter

import (
	"errors"
	"strings"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

var ErrUntrustedHistory = errors.New("untrusted historical identity provenance")

type ProviderHistory struct{}

func (ProviderHistory) VerifiedHistoricalFact(input identityport.HistoricalVerifiedInput) (identitydomain.VerifiedFact, error) {
	if !strings.HasPrefix(input.Source, "provider-history:") {
		return identitydomain.VerifiedFact{}, ErrUntrustedHistory
	}
	return identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.Kind(input.Kind), Scope: input.Scope, Value: input.Value, Source: input.Source})
}

var _ identityport.HistoricalFactFactory = ProviderHistory{}
