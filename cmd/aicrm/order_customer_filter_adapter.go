package main

import (
	"context"
	"strings"

	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// orderCustomerFilterAdapter is composition's only bridge from the admin
// order list to OneID.  It resolves an existing identity inside the normal
// PostgreSQL UoW and deliberately exposes no provision, attach, or merge
// operation to Order or its HTTP adapter.
type orderCustomerFilterAdapter struct {
	uow    platformport.UnitOfWork
	oneID  identityapp.OneIDService
	corpID string
}

func (adapter orderCustomerFilterAdapter) ResolveOrderCustomerFilter(ctx context.Context, filter orderport.CustomerFilter) (orderport.CustomerFilterResolution, error) {
	phone, external := strings.TrimSpace(filter.Phone), strings.TrimSpace(filter.ExternalUserID)
	if (phone == "" && external == "") || (phone != "" && external != "") || phone != filter.Phone || external != filter.ExternalUserID {
		return orderport.CustomerFilterResolution{Status: orderport.CustomerFilterInvalid}, nil
	}
	var reference identitydomain.Reference
	if phone != "" {
		reference = identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: phone, Assurance: identitydomain.AssuranceDeclared, Source: "order.admin_filter"}
	} else {
		if strings.TrimSpace(adapter.corpID) == "" || strings.TrimSpace(adapter.corpID) != adapter.corpID {
			return orderport.CustomerFilterResolution{Status: orderport.CustomerFilterUnavailable}, nil
		}
		reference = identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + adapter.corpID, Value: external, Assurance: identitydomain.AssuranceDeclared, Source: "order.admin_filter"}
	}
	var resolved identityport.ResolveResult
	err := adapter.uow.Within(ctx, func(txContext context.Context) error {
		var resolveErr error
		resolved, resolveErr = adapter.oneID.Resolve(txContext, reference)
		return resolveErr
	})
	if err != nil {
		if err == identitydomain.ErrInvalidReference {
			return orderport.CustomerFilterResolution{Status: orderport.CustomerFilterInvalid}, nil
		}
		return orderport.CustomerFilterResolution{}, err
	}
	switch resolved.Status {
	case identityport.ResolveFound:
		return orderport.CustomerFilterResolution{Status: orderport.CustomerFilterFound, CustomerID: resolved.CustomerID}, nil
	case identityport.ResolveNotFound:
		return orderport.CustomerFilterResolution{Status: orderport.CustomerFilterNotFound}, nil
	case identityport.ResolveConflict:
		return orderport.CustomerFilterResolution{Status: orderport.CustomerFilterConflict}, nil
	default:
		return orderport.CustomerFilterResolution{Status: orderport.CustomerFilterUnavailable}, nil
	}
}

var _ orderport.CustomerFilterResolver = orderCustomerFilterAdapter{}
