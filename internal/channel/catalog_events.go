package channel

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
)

type ChannelCatalogEventAppender struct {
	audit  *platformaudit.Service
	outbox platformoutbox.Appender
}

func NewChannelCatalogEventAppender(audit *platformaudit.Service, outbox platformoutbox.Appender) (*ChannelCatalogEventAppender, error) {
	if audit == nil || outbox == nil {
		return nil, errors.New("channel event ports are required")
	}
	return &ChannelCatalogEventAppender{audit: audit, outbox: outbox}, nil
}

func (appender *ChannelCatalogEventAppender) Append(ctx context.Context, event channelport.CatalogEvent) error {
	if appender == nil || event.ChannelID < 1 || event.Version < 1 || event.ActorID < 1 || !json.Valid(event.Payload) {
		return errors.New("invalid channel event")
	}
	auditKey, err := idempotency.Parse(event.IdempotencyKey)
	if err != nil {
		return err
	}
	id := strconv.FormatInt(event.ChannelID, 10)
	if _, err = appender.audit.Append(ctx, platformaudit.Event{IdempotencyKey: auditKey, Action: event.Type, ActorType: "admin", ActorID: strconv.FormatInt(event.ActorID, 10), ResourceType: "channel", ResourceID: id, Payload: append(json.RawMessage(nil), event.Payload...), OccurredAt: event.OccurredAt}); err != nil {
		return err
	}
	version := int16(1)
	if event.Version <= 32767 {
		version = int16(event.Version)
	}
	_, err = appender.outbox.Append(ctx, platformoutbox.Event{AggregateType: "channel", AggregateID: id, Type: event.Type, Version: version, IdempotencyKey: "channel.outbox:" + event.IdempotencyKey, Payload: append(json.RawMessage(nil), event.Payload...), OccurredAt: event.OccurredAt})
	return err
}

var _ channelport.CatalogEventAppender = (*ChannelCatalogEventAppender)(nil)
