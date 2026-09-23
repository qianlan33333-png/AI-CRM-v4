package port

import (
	"context"
	"errors"
)

// ErrEnterpriseEmployeeNotFound is a definitive Provider response for an
// exact employee userid. It is deliberately separate from unavailable reads:
// callers may use it to fall back from an identifier-shaped display-name
// query, but must never hide timeouts or permission failures as no match.
var ErrEnterpriseEmployeeNotFound = errors.New("wecom enterprise employee not found")

// EnterpriseEmployee is the minimum provider-verified projection needed for
// Access administration. It is not a customer identity and is never a source
// of authorization by itself.
type EnterpriseEmployee struct {
	UserID      string
	DisplayName string
}

// EnterpriseEmployeeDirectory is a read-only boundary for the application's
// visible corporate employee scope. It deliberately does not reuse the
// external-contact follow-user directory: that list is only a customer-owner
// subset and cannot establish who may be granted CRM access.
type EnterpriseEmployeeDirectory interface {
	EnterpriseDirectoryReady() bool
	// ListEnterpriseEmployees returns the complete application-visible
	// enterprise directory. Implementations must bound their Provider work and
	// fail instead of returning a partial list.
	ListEnterpriseEmployees(context.Context) ([]EnterpriseEmployee, error)
	ReadEnterpriseEmployee(context.Context, string) (EnterpriseEmployee, error)
}
