package audit

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrDuplicateEvent = errors.New("audit event idempotency key already exists")

type PostgreSQLStore struct{}

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
