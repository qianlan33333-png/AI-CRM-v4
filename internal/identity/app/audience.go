package app

import (
	"context"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

func (service OneIDService) ResolveAudienceFact(ctx context.Context, fact identitydomain.VerifiedFact) (identityport.AudienceResolution, error) {
	if !fact.Valid() {
		return identityport.AudienceResolution{Status: identityport.AudienceInvalid}, nil
	}
	normalized := fact.Reference()
	result, err := service.Resolve(ctx, identitydomain.Reference{Kind: normalized.Kind, Scope: normalized.Scope, Value: normalized.NormalizedValue, Assurance: normalized.Assurance, Source: normalized.Source})
	if err != nil {
		return identityport.AudienceResolution{}, err
	}
	switch result.Status {
	case identityport.ResolveFound:
		return identityport.AudienceResolution{Status: identityport.AudienceResolved, CustomerID: result.CustomerID, IdentityID: result.IdentityID}, nil
	case identityport.ResolveNotFound:
		return identityport.AudienceResolution{Status: identityport.AudienceUnresolved}, nil
	case identityport.ResolveConflict:
		return identityport.AudienceResolution{Status: identityport.AudienceConflict}, nil
	default:
		return identityport.AudienceResolution{Status: identityport.AudienceInvalid}, nil
	}
}

var _ identityport.AudienceVerifiedResolver = OneIDService{}
