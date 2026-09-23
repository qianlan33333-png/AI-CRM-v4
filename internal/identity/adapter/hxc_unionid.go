package adapter

import (
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

// HXCVerifiedUnionIDFactory is the trusted adapter boundary that upgrades an
// HXC UnionID only after deployment configuration has proven its open-platform
// scope and provider provenance. HTTP input never reaches this constructor.
type HXCVerifiedUnionIDFactory struct{ Enabled bool }

func (factory HXCVerifiedUnionIDFactory) VerifiedHXCUnionID(scope, value string) (identitydomain.VerifiedFact, error) {
	if !factory.Enabled {
		return identitydomain.VerifiedFact{}, identitydomain.ErrInvalidReference
	}
	return identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindUnionID, Scope: scope, Value: value, Source: "hxc",
	})
}
