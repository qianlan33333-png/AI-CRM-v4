package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

// CustomerActivityReader exposes only resolved, customer-scoped channel facts.
// The caller supplies a transaction-bound context. No raw identities escape.
type CustomerActivityReader interface {
	CustomerChannelActivities(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error)
}
