package app

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type canonicalPhoneRecoveryStore interface {
	PhoneRecovery(context.Context, identityport.DeclaredPhoneCommand, identitydomain.NormalizedReference, bool) (identityport.PhoneRecoveryResult, error)
}

func (s OneIDService) InspectDeclaredPhoneRecovery(ctx context.Context, c identityport.DeclaredPhoneCommand) (identityport.PhoneRecoveryResult, error) {
	return s.phoneRecovery(ctx, c, false)
}
func (s OneIDService) RecoverDeclaredPhoneToCanonicalCustomer(ctx context.Context, c identityport.DeclaredPhoneCommand) (identityport.PhoneRecoveryResult, error) {
	return s.phoneRecovery(ctx, c, true)
}
func (s OneIDService) phoneRecovery(ctx context.Context, c identityport.DeclaredPhoneCommand, apply bool) (identityport.PhoneRecoveryResult, error) {
	store, ok := s.Store.(canonicalPhoneRecoveryStore)
	if !ok {
		return identityport.PhoneRecoveryResult{}, ErrInvalidLinkCommand
	}
	if c.CustomerID < 1 || c.Source != "order" && c.Source != "survey" || c.SourceEventID == "" || len(c.SourceEventID) > 200 || c.IdempotencyKey == "" || len(c.IdempotencyKey) > 200 {
		return identityport.PhoneRecoveryResult{Disposition: "invalid_source"}, nil
	}
	ref, err := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: c.Phone, Assurance: identitydomain.AssuranceDeclared, Source: c.Source})
	if err != nil {
		return identityport.PhoneRecoveryResult{Disposition: "invalid_phone"}, nil
	}
	return store.PhoneRecovery(ctx, c, ref, apply)
}
