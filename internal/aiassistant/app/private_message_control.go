package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"strings"
)

func (s *Service) RequestPrivateMessageControl(ctx context.Context, c ai.PrivateMessageControlCommand) (outbound.PrivateMessageControl, error) {
	if s == nil || !c.Actor.Valid() || c.PlanID < 1 || c.RecipientID < 1 || c.ExpectedVersion < 1 || len(c.IdempotencyKey) < 8 || len(c.IdempotencyKey) > 200 || strings.TrimSpace(c.IdempotencyKey) != c.IdempotencyKey || strings.TrimSpace(c.Reason) == "" || (c.Action != "stop" && c.Action != "reconcile") {
		return outbound.PrivateMessageControl{}, ErrInvalid
	}
	controller, ok := s.outbound.(outbound.PrivateMessageController)
	if !ok || !s.dispatchEnabled {
		return outbound.PrivateMessageControl{}, ErrUnavailable
	}
	var result outbound.PrivateMessageControl
	var ref string
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation("private_message_"+c.Action, c.Actor, c.IdempotencyKey, digestJSON(c), s.nowUTC()))
		if err != nil {
			return err
		}
		recipient, content, err := s.store.GetRecipient(tx, c.PlanID, c.RecipientID, true)
		if err != nil {
			return err
		}
		ref = fmt.Sprintf("aiassistant:%d:%d:%d", c.PlanID, c.RecipientID, content.ID)
		if !owned {
			if err := json.Unmarshal(receipt.ResultSnapshot, &result); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		if recipient.Version != c.ExpectedVersion {
			return ErrConflict
		}
		result, err = controller.RequestPrivateMessageControlWithin(tx, outbound.PrivateMessageControlCommand{PayloadReference: ref, Action: c.Action, ActorID: c.Actor.ID, RequestKey: effect.Hash("aiassistant.private-control.key.v1", fmt.Sprint(c.Actor.ID), c.IdempotencyKey)})
		if err != nil {
			if errors.Is(err, outbound.ErrInvalidPrivateMessageIntent) {
				return ErrConflict
			}
			return err
		}
		payload, _ := json.Marshal(map[string]any{"recipient_id": c.RecipientID, "effect_id": result.EffectID, "action": c.Action, "reason": c.Reason})
		if err = s.store.AppendEvent(tx, ai.Event{Type: "aiassistant.private_message_control_requested.v1", AggregateID: c.PlanID, RecipientID: c.RecipientID, ActorID: c.Actor.ID, IdempotencyKey: c.IdempotencyKey, Payload: payload, OccurredAt: s.nowUTC()}); err != nil {
			return err
		}
		raw, _ := json.Marshal(result)
		_, err = s.store.Complete(tx, receipt.ID, raw, s.nowUTC())
		return err
	})
	if err != nil {
		if errors.Is(err, ErrIdempotencyConflict) {
			return result, ErrConflict
		}
		return result, classify(err)
	}
	observed, err := controller.ReadPrivateMessageControl(ctx, ref, c.Action, result.EffectID)
	if err != nil {
		return result, err
	}
	observed.Replayed = result.Replayed
	return observed, nil
}

func (s *Service) GetPrivateMessageControl(ctx context.Context, plan ai.PlanID, recipientID ai.RecipientID, action string, effectID string) (outbound.PrivateMessageControl, error) {
	controller, ok := s.outbound.(outbound.PrivateMessageController)
	if !ok {
		return outbound.PrivateMessageControl{}, ErrUnavailable
	}
	if action != "stop" && action != "reconcile" {
		return outbound.PrivateMessageControl{}, ErrInvalid
	}
	_, content, err := s.GetRecipient(ctx, plan, recipientID)
	if err != nil {
		return outbound.PrivateMessageControl{}, err
	}
	result, err := controller.ReadPrivateMessageControl(ctx, fmt.Sprintf("aiassistant:%d:%d:%d", plan, recipientID, content.ID), action, effectID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return result, err
}
