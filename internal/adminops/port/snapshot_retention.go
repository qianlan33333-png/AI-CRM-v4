package port

import (
	"context"
	"errors"
	"time"
)

var ErrSnapshotRetentionRequest = errors.New("invalid diagnostic snapshot retention request")
var ErrSnapshotRetentionBusy = errors.New("diagnostic snapshot retention is already running")

type SnapshotRetentionCommand struct {
	Before time.Time
	Limit  int
	Apply  bool
}

type SnapshotRetentionReport struct {
	Before     time.Time `json:"before"`
	Candidates int64     `json:"candidates"`
	Deleted    int64     `json:"deleted"`
	Bytes      int64     `json:"bytes"`
	Remaining  bool      `json:"remaining"`
}

// SnapshotRetention must participate in the caller's PostgreSQL UnitOfWork so
// deletion and its permanent/aggregate cleanup receipt commit together.
type SnapshotRetention interface {
	CleanupDiagnosticSnapshotsWithin(context.Context, SnapshotRetentionCommand) (SnapshotRetentionReport, error)
}
