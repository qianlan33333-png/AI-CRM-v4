package port

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

// HistoricalDeltaPrecondition is frozen by a separate read-only target audit.
// It cannot authorize a native order or change its immutable commercial facts.
type HistoricalDeltaPrecondition struct {
	SourceDigest [32]byte `json:"source_digest"`
	Version      int64    `json:"version"`
}

type HistoricalDeltaCommand struct {
	HistoricalImportCommand
	Before HistoricalDeltaPrecondition
}

type HistoricalDeltaImporter interface {
	ApplyHistoricalDeltaWithin(context.Context, HistoricalDeltaCommand) (domain.Snapshot, error)
}
