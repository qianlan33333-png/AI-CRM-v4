package port

import (
	"context"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
)

type Snapshot struct {
	Complete  bool // Set only after the trusted provider finishes the full source transaction.
	AsOf      time.Time
	Watermark *time.Time
	Rows      []domain.SourceRow
	Digest    [32]byte
}

type CurrentSource interface {
	Ready() bool
	Preflight(context.Context) error
	ReadSnapshot(context.Context, time.Time) (Snapshot, error)
}
