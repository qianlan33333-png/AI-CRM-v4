// Package outbox persists versioned domain events in the caller's transaction.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrInvalidEvent = errors.New("invalid outbox event")

type Event struct {
	ID             int64
	AggregateType  string
	AggregateID    string
	Type           string
	Version        int16
	IdempotencyKey string
	Payload        json.RawMessage
	OccurredAt     time.Time
	Processed      bool
}

type Appender interface {
	Append(context.Context, Event) (Event, error)
}

type Reconciler interface {
	PendingForSyncRun(context.Context, int64) (int64, error)
}

type Service interface {
	Appender
	Reconciler
}

type PostgreSQL struct{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

func (PostgreSQL) Append(ctx context.Context, event Event) (Event, error) {
	if !valid(event.AggregateType, 80) || !valid(event.AggregateID, 200) || !valid(event.Type, 160) ||
		!valid(event.IdempotencyKey, 240) || len(event.IdempotencyKey) < 8 || event.Version < 1 {
		return Event{}, ErrInvalidEvent
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(event.Payload) {
		return Event{}, ErrInvalidEvent
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Event{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,event_version,idempotency_key,payload_json,occurred_at,processed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $8 THEN clock_timestamp() ELSE NULL END)
		ON CONFLICT(idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key
		WHERE outbox_events.aggregate_type=EXCLUDED.aggregate_type AND outbox_events.aggregate_id=EXCLUDED.aggregate_id
		AND outbox_events.event_type=EXCLUDED.event_type AND outbox_events.event_version=EXCLUDED.event_version
		AND outbox_events.payload_json=EXCLUDED.payload_json
		RETURNING id,occurred_at`, event.AggregateType, event.AggregateID, event.Type, event.Version,
		event.IdempotencyKey, event.Payload, event.OccurredAt, event.Processed).Scan(&event.ID, &event.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrInvalidEvent
	}
	return event, err
}

func (PostgreSQL) PendingForSyncRun(ctx context.Context, runID int64) (int64, error) {
	if runID < 1 {
		return 0, ErrInvalidEvent
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE processed_at IS NULL AND payload_json @> jsonb_build_object('sync_run_id',$1::bigint)`, runID).Scan(&count)
	return count, err
}

func valid(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\t")
}
