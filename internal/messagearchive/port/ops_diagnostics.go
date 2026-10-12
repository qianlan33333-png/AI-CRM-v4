package port

import (
	"context"
	"time"
)

// OpsDiagnosticReader publishes aggregate local ingestion evidence only.
type OpsDiagnosticReader interface {
	ReadOpsDiagnosticCounts(context.Context, time.Time) (map[string]int64, error)
}
