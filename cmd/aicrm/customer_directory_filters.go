package main

import (
	"context"
	"errors"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// customerDirectoryTagFilter is the composition seam for the Customer list.
// Tag resolves a local ID only through its persisted Provider binding; WeCom
// returns only canonical Customer IDs with a completed active observation.
// Neither domain is asked to create an identity, customer, tag, or Provider
// write while a directory is filtered.
type customerDirectoryTagFilter struct {
	bindings tagport.ProviderTagBindingReader
	members  wecomport.ProviderTagCustomerLister
}

func (filter customerDirectoryTagFilter) CustomerIDsForTag(ctx context.Context, tagID int64, limit int) ([]customerdomain.CustomerID, error) {
	if filter.bindings == nil || filter.members == nil || tagID < 1 || limit < 1 || limit > customerapp.MaximumFilterCandidates+1 {
		return nil, errors.New("customer directory tag filter unavailable")
	}
	providerTagID, found, err := filter.bindings.ProviderTagID(ctx, tagID)
	if err != nil {
		return nil, err
	}
	// A local tag without a confirmed official ID cannot be matched by name or
	// guessed from observations.  It truthfully has no Provider-backed rows.
	if !found {
		return []customerdomain.CustomerID{}, nil
	}
	return filter.members.ListCustomerIDsForProviderTag(ctx, providerTagID, limit)
}

var _ customerapp.TagCustomerMatcher = customerDirectoryTagFilter{}
