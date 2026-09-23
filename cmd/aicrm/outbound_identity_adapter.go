package main

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type outboundIdentityAdapter struct {
	uow    platformport.UnitOfWork
	reader identityport.OutboundIdentityReader
}

func (a outboundIdentityAdapter) VerifiedOutboundIdentity(ctx context.Context, customerID customerdomain.CustomerID, kind identitydomain.Kind, scope string) (identityport.OutboundIdentity, bool, error) {
	var out identityport.OutboundIdentity
	var found bool
	err := a.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, found, e = a.reader.VerifiedOutboundIdentity(tx, customerID, kind, scope)
		return e
	})
	return out, found, err
}

var _ identityport.OutboundIdentityReader = outboundIdentityAdapter{}
