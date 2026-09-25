package port

import (
	"context"
	"time"
)

type MachineContactStatus struct {
	State     string
	ChangedAt time.Time
}

type MachineContactStatusReader interface {
	MachineContactStatuses(context.Context, []int64) (map[int64]MachineContactStatus, error)
}
