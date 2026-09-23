package port

import (
	"context"
	"errors"
	"time"
)

var ErrProcessRetentionInvalid = errors.New("invalid messagearchive process retention command")

// ProcessRetentionCommand authorizes only this Owner's classified process detail.
// Before is clamped against the database clock to preserve at least 720 hours.
// Apply=false is a bounded preview; Limit must be between 1 and 1000.
type ProcessRetentionCommand struct {
	Before time.Time
	Limit  int
	Apply  bool
}

// Bytes estimates selected row bytes in preview and deleted row bytes in apply;
// it is not disk space reclaimed. Remaining includes eligible rows skipped by locks.
type ProcessRetentionReport struct {
	Before                     time.Time
	Candidates, Deleted, Bytes int64
	Remaining                  bool
}

// ProcessRetention participates in the caller's existing PostgreSQL Unit of Work.
// It never begins or commits a separate transaction or accepts a table/SQL name.
type ProcessRetention interface {
	CleanupProcessDetailWithin(context.Context, ProcessRetentionCommand) (ProcessRetentionReport, error)
}
