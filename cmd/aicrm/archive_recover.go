package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

var errArchiveRecoveryRejected = errors.New("archive recovery rejected; reconcile original delivery before retrying")

// This is only a fenced wake-up for an already authenticated notification.
// It cannot invent a callback, rewind/advance an archive cursor, retry another
// provider, send a message, or erase an attempt. River/Inbox retain execution.
func recoverArchiveNotification(ctx context.Context, p wecom.ArchiveInboxProcessor, id int64, attempt, uid int) (out webhook.Delivery, err error) {
	if uid != 0 || !p.Enabled || p.Inbox == nil || p.UOW == nil || id < 1 || attempt < 1 {
		return out, errArchiveRecoveryRejected
	}
	key, e := idempotency.Parse(fmt.Sprintf("archive-recover:%d:%d", id, attempt))
	if e != nil {
		return out, errArchiveRecoveryRejected
	}
	recorder, e := audit.NewService(audit.NewPostgreSQLStore())
	if e != nil {
		return out, errArchiveRecoveryRejected
	}
	err = p.UOW.Within(ctx, func(tx context.Context) error {
		original, e := p.Inbox.Get(tx, id)
		if e != nil {
			return errArchiveRecoveryRejected
		}
		if original.Provider != "wecom.message_archive" || original.Status != webhook.StatusFailed || original.AttemptCount != attempt || original.LastErrorCode != "archive_processing_failed" {
			return errArchiveRecoveryRejected
		}
		out, e = p.Inbox.Retry(tx, webhook.Retry{ID: id, Provider: original.Provider, ExpectedStatus: webhook.StatusFailed, ExpectedAttempt: attempt, Now: time.Now().UTC()})
		if e != nil {
			return errArchiveRecoveryRejected
		}
		payload, _ := json.Marshal(map[string]any{"delivery_id": id, "expected_attempt": attempt, "recovery": "fixed_archive_json_controls"})
		_, e = recorder.Append(tx, audit.Event{IdempotencyKey: key, Action: "archive.notification.recover", ActorType: "service", ActorID: "ops:root-archive-recovery", ResourceType: "webhook_delivery", ResourceID: fmt.Sprint(id), Payload: payload})
		return e
	})
	if err != nil {
		return webhook.Delivery{}, errArchiveRecoveryRejected
	}
	return out, nil
}
