package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// HistoricalTagRecord contains only local target IDs and safe legacy effect
// metadata. Raw external_userid, staff userid and provider tag IDs remain in
// the encrypted source snapshot and in their owning adapters.
type HistoricalTagRecord struct {
	SourceJobID  int64
	SourceDigest string
	EffectType   string
	Operation    string
	SourceState  string
	Resolution   string
	Reason       string
	CustomerID   customerdomain.CustomerID
	StaffID      int64
	AddTagIDs    []int64
	RemoveTagIDs []int64
	OccurredAt   time.Time
	CompletedAt  *time.Time
}

type HistoricalTagBatch struct {
	SourceSystem   string
	SnapshotDigest string
	SnapshotAt     time.Time
}

type HistoricalTagImportResult struct {
	Imported, Replayed, Pending, Conflict, Excluded, Failed int
}

// HistoricalTagImporter is migration-only: implementations persist immutable
// receipt facts and never create a current tag command, EER, River job, or
// Provider write.
type HistoricalTagImporter interface {
	ApplyHistoricalTagRecords(context.Context, HistoricalTagBatch, []HistoricalTagRecord) (HistoricalTagImportResult, error)
	VerifyHistoricalTagRecords(context.Context, HistoricalTagBatch, []HistoricalTagRecord) (HistoricalTagImportResult, error)
}
