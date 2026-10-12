package port

import (
	"context"
	"time"
)

// WeComStaffProjection is provider-verified employee directory input. It is
// deliberately separate from customer identity/OneID.
type WeComStaffProjection struct {
	WeComUserID string
	DisplayName string
}

type WeComStaffProjectionResult struct {
	Discovered int64
	Created    int64
	Existing   int64
	Inactive   int64
}

// WeComStaffProjector is the Access-owned mutation boundary used by the WeCom
// directory refresher. The caller must install a PostgreSQL transaction so
// users and the refresh receipt commit atomically.
type WeComStaffProjector interface {
	ProjectWeComStaffWithin(context.Context, string, []WeComStaffProjection, time.Time) (WeComStaffProjectionResult, error)
}

// EnterpriseStaffProjector reuses the same Access projection kernel while
// retaining the complete enterprise directory as the audit source.
type EnterpriseStaffProjector interface {
	ProjectEnterpriseStaffWithin(context.Context, string, []WeComStaffProjection, time.Time) (WeComStaffProjectionResult, error)
}

// StaffProjectionLocker serializes directory discovery from manual and durable
// refreshes. The lock lives in the caller's existing PostgreSQL transaction.
type StaffProjectionLocker interface {
	LockStaffDirectoryWithin(context.Context) error
}
