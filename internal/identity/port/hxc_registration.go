package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// HXCRegistrationCoverage accepts only a complete trusted source snapshot.
// It is read-only and returns no identity values. Unknown is never a negative.
type HXCRegistrationState string

const (
	HXCRegistered          HXCRegistrationState = "registered"
	HXCUnregistered        HXCRegistrationState = "unregistered"
	HXCRegistrationUnknown HXCRegistrationState = "unknown"
)

type HXCRegistrationCoverage interface {
	InspectHXCRegistrationCoverage(context.Context, []HXCSubject, bool) (map[customerdomain.CustomerID]HXCRegistrationState, error)
}
