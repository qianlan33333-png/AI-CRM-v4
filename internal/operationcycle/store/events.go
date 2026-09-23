package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformidempotency "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// EventJournal adapts operation-cycle facts to the shared audit/outbox tables.
// The caller supplies the PostgreSQL transaction through context, so the
// domain mutation, receipt, audit event and delivery acceptance commit as one
// unit. It performs no network I/O.
type EventJournal struct {
	audit  *platformaudit.PostgreSQLStore
	outbox platformoutbox.PostgreSQL
}

var _ operationport.EventAppender = (*EventJournal)(nil)
var _ operationport.DeliveryAcceptor = (*EventJournal)(nil)

func NewEventJournal() *EventJournal {
	return &EventJournal{audit: platformaudit.NewPostgreSQLStore(), outbox: platformoutbox.NewPostgreSQL()}
}

func (journal *EventJournal) Append(ctx context.Context, event operationport.Event) (operationport.EventID, error) {
	if journal == nil || journal.audit == nil || strings.TrimSpace(event.Type) != event.Type || event.Type != operationport.EvOperationCycleFact || !json.Valid(event.Payload) {
		return 0, errors.New("invalid operation-cycle event")
	}
	key, err := platformidempotency.Parse(event.IdempotencyKey)
	if err != nil {
		return 0, err
	}
	actorType, actorID, resourceID := operationCycleAuditScope(event)
	auditEvent, err := journal.audit.Append(ctx, platformaudit.Event{
		IdempotencyKey: key,
		Action:         "operation_cycle.fact_recorded",
		ActorType:      actorType,
		ActorID:        actorID,
		ResourceType:   "operation_cycle",
		ResourceID:     resourceID,
		Payload:        event.Payload,
		OccurredAt:     event.OccurredAt,
	})
	if err != nil {
		return 0, err
	}
	outboxEvent, err := journal.outbox.Append(ctx, platformoutbox.Event{
		AggregateType:  "operation_cycle",
		AggregateID:    event.IdempotencyKey,
		Type:           event.Type,
		Version:        1,
		IdempotencyKey: event.IdempotencyKey + ":outbox",
		Payload:        event.Payload,
		OccurredAt:     event.OccurredAt,
	})
	if err != nil {
		return 0, err
	}
	if auditEvent.ID < 1 || outboxEvent.ID < 1 {
		return 0, errors.New("operation-cycle journal did not persist")
	}
	return operationport.EventID(outboxEvent.ID), nil
}

func operationCycleAuditScope(event operationport.Event) (actorType, actorID, resourceID string) {
	actorType, resourceID = "system", event.IdempotencyKey
	var envelope struct {
		Data struct {
			ActorID     string `json:"actor_id"`
			StrategyKey string `json:"strategy_key"`
		} `json:"data"`
	}
	if json.Unmarshal(event.Payload, &envelope) != nil {
		return actorType, "", resourceID
	}
	if strings.TrimSpace(envelope.Data.ActorID) != "" {
		actorType, actorID = "admin", envelope.Data.ActorID
	}
	if strings.TrimSpace(envelope.Data.StrategyKey) != "" {
		resourceID = envelope.Data.StrategyKey
	}
	return actorType, actorID, resourceID
}

func (journal *EventJournal) Accept(ctx context.Context, eventID operationport.EventID, consumer string) error {
	if journal == nil || eventID < 1 || consumer != operationport.ConsumerOperationCycleFact {
		return errors.New("invalid operation-cycle delivery acceptance")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE outbox_events
		SET processed_at = COALESCE(processed_at, clock_timestamp())
		WHERE id = $1 AND aggregate_type = 'operation_cycle'
		AND event_type = $2`, int64(eventID), operationport.EvOperationCycleFact)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}
