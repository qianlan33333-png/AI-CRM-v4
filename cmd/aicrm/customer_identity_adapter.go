package main

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

// customerProvisionedDirectoryAdapter turns Identity's created-root event into
// Customer's minimum searchable projection inside the same caller UoW. The
// provider source is intentionally not copied into the directory: it may be an
// identity-specific label and is not a presentation profile provenance fact.
type customerProvisionedDirectoryAdapter struct {
	writer customerport.CallbackProjectionWriter
	now    func() time.Time
}

func (adapter customerProvisionedDirectoryAdapter) ObserveProvisionedCustomer(ctx context.Context, customerID customerdomain.CustomerID, _ string) error {
	now := time.Now().UTC()
	if adapter.now != nil {
		now = adapter.now().UTC()
	}
	return adapter.writer.ActivateDirectoryCustomer(ctx, customerID, "identity_provision", now)
}
