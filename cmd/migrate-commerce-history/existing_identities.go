package main

import (
	"context"
	"errors"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

// existingIdentitiesOnly cannot provision or upgrade an identity: it implements
// the migration port using only Resolve, preserving existing canonical roots.
type existingIdentitiesOnly struct{ Resolver identityport.Resolver }

func (p existingIdentitiesOnly) ProvisionHistoricalSubject(ctx context.Context, command identityport.HistoricalSubjectCommand) (identityport.HistoricalSubjectResult, error) {
	var result identityport.HistoricalSubjectResult
	if p.Resolver == nil || len(command.Facts) == 0 {
		return result, errors.New("existing identity evidence required")
	}
	for _, fact := range command.Facts {
		if !fact.Valid() {
			return identityport.HistoricalSubjectResult{}, errors.New("invalid existing identity evidence")
		}
		ref := fact.Reference()
		resolved, err := p.Resolver.Resolve(ctx, identitydomain.Reference{Kind: ref.Kind, Scope: ref.Scope, Value: ref.NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: ref.Source})
		if err != nil {
			return identityport.HistoricalSubjectResult{}, errors.New("existing identity lookup failed")
		}
		if resolved.Status != identityport.ResolveFound || resolved.CustomerID <= 0 || resolved.IdentityID <= 0 || (result.CustomerID != 0 && result.CustomerID != resolved.CustomerID) {
			return identityport.HistoricalSubjectResult{}, errors.New("existing identity not uniquely confirmed")
		}
		result.CustomerID = resolved.CustomerID
		result.IdentityIDs = append(result.IdentityIDs, resolved.IdentityID)
	}
	return result, nil
}
