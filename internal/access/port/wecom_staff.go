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
