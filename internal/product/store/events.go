package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// TransactionalEventAppender adapts Product facts to the platform-owned audit
// and outbox ports.  Both appends run on the UnitOfWork transaction supplied
// by the Product application; neither operation dispatches a Provider call.
type TransactionalEventAppender struct {
	audit  *platformaudit.Service
	outbox platformoutbox.Appender
}

func NewTransactionalEventAppender(audit *platformaudit.Service, outbox platformoutbox.Appender) (*TransactionalEventAppender, error) {
	if audit == nil || outbox == nil {
		return nil, errors.New("product event ports are required")
	}
	return &TransactionalEventAppender{audit: audit, outbox: outbox}, nil
}

func (a *TransactionalEventAppender) Append(ctx context.Context, event productport.Event) (productport.EventID, error) {
	if a == nil || a.audit == nil || a.outbox == nil || event.Type == "" || len(event.Payload) == 0 || !json.Valid(event.Payload) || event.OccurredAt.IsZero() {
		return 0, errors.New("invalid product event")
	}
	key, err := idempotency.Parse(event.IdempotencyKey)
	if err != nil {
		return 0, err
	}
	var envelope struct {
		ProductID productport.ID `json:"product_id"`
		Actor     int64          `json:"actor"`
		Version   int64          `json:"version"`
	}
	if err = json.Unmarshal(event.Payload, &envelope); err != nil || envelope.ProductID < 1 {
		return 0, errors.New("product event payload is incomplete")
	}
	actorID := ""
	if envelope.Actor > 0 {
		actorID = strconv.FormatInt(envelope.Actor, 10)
	}
	audited, err := a.audit.Append(ctx, platformaudit.Event{
		IdempotencyKey: key,
		Action:         event.Type,
		ActorType:      "admin",
		ActorID:        actorID,
		ResourceType:   "product",
		ResourceID:     strconv.FormatInt(int64(envelope.ProductID), 10),
		Payload:        append(json.RawMessage(nil), event.Payload...),
		OccurredAt:     event.OccurredAt.UTC(),
	})
	if err != nil {
		return 0, err
	}
	version := int16(1)
	if envelope.Version > 0 && envelope.Version <= 32767 {
		version = int16(envelope.Version)
	}
	if _, err = a.outbox.Append(ctx, platformoutbox.Event{
		AggregateType:  "product",
		AggregateID:    strconv.FormatInt(int64(envelope.ProductID), 10),
		Type:           event.Type,
		Version:        version,
		IdempotencyKey: "product.outbox:" + event.IdempotencyKey,
		Payload:        append(json.RawMessage(nil), event.Payload...),
		OccurredAt:     event.OccurredAt.UTC(),
	}); err != nil {
		return 0, err
	}
	return productport.EventID(audited.ID), nil
}

var _ productport.EventAppender = (*TransactionalEventAppender)(nil)
