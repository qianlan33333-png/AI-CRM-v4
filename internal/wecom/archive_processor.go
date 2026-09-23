package wecom

import (
	"context"
	"errors"
	"strings"
	"time"

	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

// ArchiveInboxProcessor reuses the existing durable Inbox claim/lease/retry
// implementation. It deliberately has no archive queue, worker loop, timer,
// River registration, or provider implementation of its own.
type ArchiveInboxProcessor struct {
	Enabled bool
	Inbox   *webhook.Service
	UOW     platformport.UnitOfWork
	Archive archiveport.InboxDeliveryHandler
	Now     func() time.Time
}

func (processor ArchiveInboxProcessor) ProcessOnce(ctx context.Context, owner string, limit int) (int, error) {
	if !processor.Enabled {
		return 0, nil
	}
	if processor.Inbox == nil || processor.UOW == nil || processor.Archive == nil || strings.TrimSpace(owner) != owner || owner == "" || limit < 1 {
		return 0, archiveport.ErrNotReady
	}
	var deliveries []webhook.Delivery
	if err := processor.UOW.Within(ctx, func(tx context.Context) error {
		var err error
		deliveries, err = processor.Inbox.Claim(tx, webhook.Claim{Provider: archiveCallbackProvider, Owner: owner, Limit: limit, LeaseDuration: time.Minute})
		return err
	}); err != nil {
		return 0, err
	}
	for _, delivery := range deliveries {
		if err := processor.process(ctx, delivery); err != nil {
			return 0, err
		}
	}
	return len(deliveries), nil
}
func (processor ArchiveInboxProcessor) process(ctx context.Context, delivery webhook.Delivery) error {
	err := processor.Archive.ProcessArchiveDelivery(ctx, archiveport.InboxDelivery{ID: delivery.ID, IdempotencyKey: string(delivery.IdempotencyKey), Attempt: delivery.AttemptCount, MaxAttempts: delivery.MaxAttempts, ReceivedAt: delivery.ReceivedAt, Payload: append([]byte(nil), delivery.Payload...)})
	status, code := webhook.StatusProcessed, ""
	budgetExhausted := errors.Is(err, archiveport.ErrWorkBudgetExceeded)
	if err != nil {
		status, code = webhook.StatusRetryable, "archive_processing_failed"
		if budgetExhausted {
			// This means the preceding page committed and the current notification
			// still has more pages. It is progress, not a failed provider call.
			code = "work_budget_exhausted"
		} else if delivery.AttemptCount >= delivery.MaxAttempts {
			status = webhook.StatusFailed
		}
	}
	return processor.UOW.Within(ctx, func(tx context.Context) error {
		var next *time.Time
		if status == webhook.StatusRetryable {
			value := processor.now().UTC()
			next = &value
		}
		completed, completeErr := processor.Inbox.Complete(tx, webhook.Completion{ID: delivery.ID, ExpectedAttempt: delivery.AttemptCount, Status: status, LastErrorCode: code, NextAttemptAt: next})
		if completeErr != nil || !budgetExhausted || completed.AttemptCount < completed.MaxAttempts {
			return completeErr
		}
		// Platform webhook.Retry is the existing CAS-protected continuation:
		// it increases the permitted attempt count by one without discarding
		// history. A page budget therefore cannot strand an otherwise healthy
		// notification at its generic failure-attempt limit.
		_, continuationErr := processor.Inbox.Retry(tx, webhook.Retry{ID: completed.ID, Provider: archiveCallbackProvider, ExpectedAttempt: completed.AttemptCount, ExpectedStatus: webhook.StatusRetryable, Now: *next})
		return continuationErr
	})
}

func (processor ArchiveInboxProcessor) now() time.Time {
	if processor.Now != nil {
		return processor.Now()
	}
	return time.Now()
}
