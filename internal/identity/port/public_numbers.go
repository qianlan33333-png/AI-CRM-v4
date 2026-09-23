package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// CustomerPublicNumbers reads generated seven-digit aliases. An alias never
// replaces the canonical customer key and cannot provision or link identities.
type CustomerPublicNumbers interface {
	CustomerPublicNumbers(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error)
	CustomerForPublicNumber(context.Context, string) (customerdomain.CustomerID, bool, error)
}
