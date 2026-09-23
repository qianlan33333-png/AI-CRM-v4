// Package port contains the customer Owner's stable aggregate diagnostics boundary.
package port

import (
	"context"
	"time"
)

// OpsDiagnosticReader exposes numeric local facts only. No identity values,
// payloads, Provider calls, or cross-domain SQL are permitted.
type OpsDiagnosticReader interface {
	ReadOpsDiagnosticCounts(context.Context, time.Time) (map[string]int64, error)
}
