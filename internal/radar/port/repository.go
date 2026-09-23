// Package port defines stable boundaries owned by the Radar domain.
package port

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

type Repository interface {
	Get(context.Context, radar.RadarID) (radar.Link, error)
	GetByPublicCode(context.Context, radar.PublicCode) (radar.Link, error)
	List(context.Context, ListQuery) (LinkPage, error)
	Create(context.Context, CreateRecord, int64, time.Time) (radar.Link, error)
	Save(context.Context, radar.Link, radar.LinkVersion, int64, time.Time) (radar.Link, error)
}

type CreateRecord struct {
	PublicCode  radar.PublicCode
	Name        string
	Title       string
	Description string
	Content     radar.Content
	AuthPolicy  radar.AuthPolicy
}

type OperationState string

const (
	OperationInProgress OperationState = "in_progress"
	OperationCompleted  OperationState = "completed"
)

type OperationReceipt struct {
	ID            int64
	Operation     string
	ActorID       int64
	KeyDigest     [32]byte
	PayloadDigest [32]byte
	State         OperationState
	RadarID       radar.RadarID
	Version       radar.LinkVersion
	CompletedAt   *time.Time
}

type MutationJournal interface {
	ReserveOperation(context.Context, OperationReceipt, time.Time) (OperationReceipt, bool, error)
	CompleteOperation(context.Context, int64, radar.RadarID, radar.LinkVersion, time.Time) error
	AppendAudit(context.Context, AuditRecord) error
	AppendOutbox(context.Context, OutboxRecord) error
}

type AuditRecord struct {
	Operation     string
	RadarID       radar.RadarID
	Version       radar.LinkVersion
	ActorID       int64
	PayloadDigest [32]byte
	OccurredAt    time.Time
}

type OutboxRecord struct {
	EventID           string
	EventType         string
	AggregateID       radar.RadarID
	AggregateVer      radar.LinkVersion
	Payload           json.RawMessage
	IdempotencyDigest [32]byte
	OccurredAt        time.Time
}
