package port

import (
	"context"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
)

type CatalogFilter struct {
	Status          channeldomain.Status
	IncludeArchived bool
	Keyword         string
	Limit           int
	AfterID         int64
}

type CatalogPage struct {
	Items      []channeldomain.Channel
	NextCursor string
	Total      int64
}

// CatalogReader is the Channel-owned, read-only catalog projection for
// consumers that need to validate a persisted channel reference.
type CatalogReader interface {
	List(context.Context, CatalogFilter) (CatalogPage, error)
}

type CatalogStore interface {
	Get(context.Context, int64) (channeldomain.Channel, error)
	List(context.Context, CatalogFilter) ([]channeldomain.Channel, int64, error)
	Create(context.Context, channeldomain.Channel, int64) (channeldomain.Channel, error)
	Update(context.Context, channeldomain.Channel, int64) (channeldomain.Channel, error)
	ReferenceCount(context.Context, int64) (int64, error)
}

type OperationReceiptState string

const (
	ReceiptInProgress OperationReceiptState = "in_progress"
	ReceiptCompleted  OperationReceiptState = "completed"
)

type OperationReceipt struct {
	ID            int64
	Operation     string
	ActorID       int64
	KeyDigest     [32]byte
	PayloadDigest [32]byte
	State         OperationReceiptState
	ChannelID     int64
	Version       int64
	CompletedAt   *time.Time
}

type OperationReceiptStore interface {
	Reserve(context.Context, OperationReceipt) (OperationReceipt, bool, error)
	Complete(context.Context, int64, int64, int64, time.Time) (OperationReceipt, error)
}

type CatalogEvent struct {
	Type           string
	ChannelID      int64
	Version        int64
	ActorID        int64
	OccurredAt     time.Time
	IdempotencyKey string
	Payload        []byte
}

type CatalogEventAppender interface {
	Append(context.Context, CatalogEvent) error
}
