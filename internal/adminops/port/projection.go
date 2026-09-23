package port

import (
	"context"
	"time"
)

// ReleaseProjection is the safe, local observation of a release artifact.
// It deliberately has no change payload, actor metadata, URLs, or arbitrary
// details.  A release observation does not claim that deployment or traffic
// switching happened.
type ReleaseProjection struct {
	ID         int64
	ReleaseSHA string
	Status     string
	ObservedAt time.Time
}

// DiagnosticSnapshot is a bounded local runtime observation.  Details are
// kept out of this port so secret/PII-bearing JSON can never cross the
// AdminOps-to-Config compatibility boundary.
type DiagnosticSnapshot struct {
	ID         int64
	Key        string
	Status     string
	ObservedAt time.Time
}

// ProjectionStore owns only the two AdminOps projection tables.  Every method
// is transaction-bound; callers must invoke it through a platform UnitOfWork.
// Recording is intentionally narrow and cannot accept arbitrary JSON details.
type ProjectionStore interface {
	ListReleaseProjections(context.Context) ([]ReleaseProjection, error)
	ListDiagnosticSnapshots(context.Context) ([]DiagnosticSnapshot, error)
	RecordReleaseProjection(context.Context, ReleaseProjection) (ReleaseProjection, error)
	RecordDiagnosticSnapshot(context.Context, DiagnosticSnapshot) (DiagnosticSnapshot, error)
}
