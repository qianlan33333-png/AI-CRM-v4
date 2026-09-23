package port

import "context"

// ActiveStaffReader validates a local employee reference for a plan member.
// It carries no customer identity and does not resolve external recipients.
type ActiveStaffReader interface {
	IsActiveStaff(context.Context, int64) (bool, error)
}
