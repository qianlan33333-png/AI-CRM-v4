package port

import (
	"context"
	"time"
)

// MachineContactRow contains WeCom's completed relationship observation. No
// provider identity value or free-form chat content crosses this boundary.
type MachineContactRow struct {
	CustomerID     int64
	OwnerUserID    string
	OwnerStatus    string
	Tags           []string
	BindingStatus  string
	IdentityStatus string
	ChangedAt      time.Time
	RemovedAt      *time.Time
}

type MachineUnresolvedRow struct {
	SourceDigest []byte
	Outcome      string
	ChangedAt    time.Time
}

type MachineContactPageReader interface {
	MachineContactRows(context.Context, string) ([]MachineContactRow, []MachineUnresolvedRow, error)
}
