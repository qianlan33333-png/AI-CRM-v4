package port

import (
	"context"
	"time"
)

// ProcessRetention describes one composition-registered Owner policy. It never
// accepts a table, path or SQL fragment, and must use the caller's UnitOfWork.
type ProcessRetentionCommand struct {
	Before time.Time
	Limit  int
	Apply  bool
}
type ProcessRetentionReport struct {
	Before                     time.Time
	Candidates, Deleted, Bytes int64
	Remaining                  bool
}
type ProcessRetention interface {
	CleanupProcessDetailWithin(context.Context, ProcessRetentionCommand) (ProcessRetentionReport, error)
}
type ProcessRetentionFunc func(context.Context, ProcessRetentionCommand) (ProcessRetentionReport, error)

func (f ProcessRetentionFunc) CleanupProcessDetailWithin(ctx context.Context, c ProcessRetentionCommand) (ProcessRetentionReport, error) {
	return f(ctx, c)
}
