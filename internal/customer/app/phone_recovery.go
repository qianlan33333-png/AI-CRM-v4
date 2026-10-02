package app

import (
	"context"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"strings"
)

func (s *CollectedPhoneService) InspectCollectedPhoneRecoveryWithin(ctx context.Context, c customerport.CollectedPhoneCommand) (identityport.PhoneRecoveryResult, error) {
	return s.recoverPhone(ctx, c, false)
}
func (s *CollectedPhoneService) RecoverCollectedPhoneWithin(ctx context.Context, c customerport.CollectedPhoneCommand) (identityport.PhoneRecoveryResult, error) {
	return s.recoverPhone(ctx, c, true)
}
func (s *CollectedPhoneService) recoverPhone(ctx context.Context, c customerport.CollectedPhoneCommand, apply bool) (identityport.PhoneRecoveryResult, error) {
	p, ok := s.phones.(identityport.DeclaredPhoneCanonicalRecovery)
	if !ok || c.CollectedAt.IsZero() {
		return identityport.PhoneRecoveryResult{}, ErrInvalidQuery
	}
	c.Phone = strings.TrimPrefix(strings.TrimSpace(c.Phone), "+86")
	if !apply {
		return p.InspectDeclaredPhoneRecovery(ctx, c.DeclaredPhoneCommand)
	}
	r, err := p.RecoverDeclaredPhoneToCanonicalCustomer(ctx, c.DeclaredPhoneCommand)
	if err != nil {
		return r, err
	}
	if r.Disposition == "recovered" && (r.Binding.Status == identityport.DeclaredAttached || r.Binding.Status == identityport.DeclaredAlreadyLinked) {
		if r.Binding.CustomerID < 1 || r.Binding.CustomerID == c.CustomerID || r.Binding.IdentityID < 1 || len(c.Phone) != 11 {
			return r, ErrInvalidQuery
		}
		err = s.projection.ProjectCollectedPhone(ctx, r.Binding.CustomerID, c.Phone[:3]+"****"+c.Phone[7:], c.Source, c.CollectedAt, true)
	}
	return r, err
}
