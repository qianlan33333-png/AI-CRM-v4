package audit

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrDuplicateEvent = errors.New("audit event idempotency key already exists")

type PostgreSQLStore struct{}

// TimelineEvent exposes only immutable platform audit facts to internal
// consumers reconstructing a resource's state at an earlier instant.
type TimelineEvent struct {
	ID         int64
	Action     string
	Payload    json.RawMessage
	OccurredAt time.Time
}

type TimelineReader interface {
	ResourceTimelineWithin(context.Context, string, string, time.Time) ([]TimelineEvent, error)
}

func (*PostgreSQLStore) ResourceTimelineWithin(ctx context.Context, resourceType, resourceID string, until time.Time) ([]TimelineEvent, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil || resourceType == "" || resourceID == "" || until.IsZero() {
		return nil, ErrInvalidEvent
	}
	rows, err := tx.Query(ctx, `SELECT id,action,payload,occurred_at FROM audit_events
WHERE resource_type=$1 AND resource_id=$2 AND occurred_at<=$3
ORDER BY occurred_at,id`, resourceType, resourceID, until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TimelineEvent, 0)
	for rows.Next() {
		var event TimelineEvent
		if err = rows.Scan(&event.ID, &event.Action, &event.Payload, &event.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func NewPostgreSQLStore() *PostgreSQLStore {
	return &PostgreSQLStore{}
}

func (*PostgreSQLStore) Append(ctx context.Context, event Event) (Event, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Event{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_events (
			idempotency_key, action, actor_type, actor_id,
			resource_type, resource_id, payload, occurred_at
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), $7, $8)
		RETURNING id, created_at`,
		event.IdempotencyKey, event.Action, event.ActorType, event.ActorID,
		event.ResourceType, event.ResourceID, []byte(event.Payload), event.OccurredAt,
	).Scan(&event.ID, &event.CreatedAt)
	if err == nil {
		return event, nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return Event{}, ErrDuplicateEvent
	}
	return Event{}, err
}
