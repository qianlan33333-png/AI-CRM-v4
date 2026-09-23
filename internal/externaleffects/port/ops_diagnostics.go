package port

import (
	"context"
	"time"
)

// OpsDiagnosticReader exposes aggregate execution facts without payloads,
// raw targets, or cross-domain table access.
type OpsDiagnosticReader interface {
	ReadOpsDiagnosticCounts(context.Context, time.Time) (map[string]int64, error)
}

// OpsWindowCounts separates attempts that started in a fixed reporting hour
// from successful attempts that completed in that hour. It contains no targets.
type OpsWindowCounts struct {
	Started  int64
	Executed int64
}

type OpsWindowReader interface {
	ReadOpsWindowCounts(context.Context, time.Time, time.Time) (OpsWindowCounts, error)
}
