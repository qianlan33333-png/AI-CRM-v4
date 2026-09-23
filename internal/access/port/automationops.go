package port

import (
	"context"
	"time"
)

type StaffID int64

type StaffEligibility struct {
	StaffID            StaffID
	DisplayName        string
	Active             bool
	Eligible           bool
	EligibilityVersion int64
	RefreshedAt        time.Time
}

// AutomationOpsStaffReader resolves a submitted Provider member reference to
// an internal Staff ID and exposes only the local eligibility projection.
type AutomationOpsStaffReader interface {
	ResolveAutomationSender(context.Context, string) (StaffEligibility, bool, error)
	AutomationSender(context.Context, StaffID) (StaffEligibility, bool, error)
}

// OutboundStaffIdentityReader is restricted to the privileged Outbound
// provider adapter. The returned Provider value is ephemeral.
type OutboundStaffIdentityReader interface {
	OutboundProviderStaffID(context.Context, StaffID) (string, bool, error)
}
