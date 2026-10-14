package main

import (
	"context"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"strconv"
	"strings"
)

type aiExecutionReceiptAdapter struct {
	store interface {
		PrivateMessageReceipt(context.Context, string) (outbound.PrivateMessageDelivery, bool, error)
	}
	provider outbound.PrivateMessageDeliveryReader
}

// Retain protocol numbers while the existing reconciler verifies all pages
// against the frozen msgid, sender and target. No reconcile writes occur here.
type captureDeliveryRead struct {
	provider   outbound.PrivateMessageDeliveryReader
	httpStatus int
	errCode    *int64
}

func (c *captureDeliveryRead) GetPrivateMessageSendResult(ctx context.Context, msgid, sender, cursor string) (outbound.PrivateMessageDeliveryPage, error) {
	page, err := c.provider.GetPrivateMessageSendResult(ctx, msgid, sender, cursor)
	c.httpStatus, c.errCode = page.HTTPStatusCode, page.ProviderErrorCode
	return page, err
}

func (a aiExecutionReceiptAdapter) ReadExecutionReceipt(ctx context.Context, ref string, live bool) (ai.ExecutionReceipt, error) {
	out := ai.ExecutionReceipt{CreationCodeEvidence: "not_recorded", QueryState: "not_requested"}
	receipt, found, err := a.store.PrivateMessageReceipt(ctx, ref)
	if err != nil || !found {
		return out, err
	}
	out.Found, out.MessageID, out.SenderUserID, out.FailureReason = true, receipt.MessageID, receipt.SenderUserID, safeReceiptFailure(receipt.Reason)
	out.LocalStatus, out.LocalSentAt = receipt.Status, receipt.SentAt
	if !receipt.ObservedAt.IsZero() {
		out.LocalObservedAt = &receipt.ObservedAt
	}
	if strings.HasPrefix(receipt.Reason, "wecom_errcode_") {
		if code, e := strconv.ParseInt(strings.TrimPrefix(receipt.Reason, "wecom_errcode_"), 10, 64); e == nil {
			out.CreationErrorCode, out.CreationCodeEvidence = &code, "recorded_rejection"
		}
	}
	if !live {
		return out, nil
	}
	if receipt.MessageID == "" {
		out.QueryState = "no_provider_task"
		return out, nil
	}
	if a.provider == nil {
		out.QueryState, out.QueryFailure = "failed", "provider_unavailable"
		return out, nil
	}
	capture := &captureDeliveryRead{provider: a.provider}
	current, err := outbound.ReconcilePrivateMessageDelivery(ctx, capture, receipt)
	out.QueryHTTPStatus, out.QueryErrorCode = capture.httpStatus, capture.errCode
	if err != nil || current.Status == nil {
		out.QueryState, out.QueryFailure = "failed", "receipt_not_verified"
		if out.QueryErrorCode != nil && *out.QueryErrorCode != 0 {
			out.QueryFailure = "wecom_errcode_" + strconv.FormatInt(*out.QueryErrorCode, 10)
		} else if out.QueryHTTPStatus == 0 {
			out.QueryFailure = "provider_read_unavailable"
		}
		return out, nil
	}
	out.QueryState, out.ProviderStatus, out.ProviderSentAt = "observed", current.Status, current.SentAt
	out.ProviderObservedAt = &current.ObservedAt
	return out, nil
}

func safeReceiptFailure(reason string) string {
	if strings.HasPrefix(reason, "wecom_errcode_") {
		if code, err := strconv.ParseInt(strings.TrimPrefix(reason, "wecom_errcode_"), 10, 64); err == nil {
			return "wecom_errcode_" + strconv.FormatInt(code, 10)
		}
	}
	switch reason {
	case "", "provider_stopped", "target_unavailable", "payload_unavailable", "provider_rejected", "outcome_unknown", "final_failed", "retryable_failed", "unionid_not_unique", "unionid_unverified", "wecom_identity_unavailable", "title_missing", "cover_missing":
		return reason
	}
	return "unclassified_failure"
}
