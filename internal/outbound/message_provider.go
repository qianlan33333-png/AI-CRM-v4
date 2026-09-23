package outbound

import (
	"context"
	"errors"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func (p *MessageProvider) Preflight(ctx context.Context, envelope effectport.Envelope, _ string) (bool, time.Duration, error) {
	if p == nil || !p.enabled || envelope.Kind != effectport.KindAutomationMessage {
		return true, 0, nil
	}
	preparer, ok := p.payloads.(outboundport.FrozenAutomationMessageMediaPreflighter)
	if !ok {
		return true, 0, nil
	}
	execution, found, err := p.executions.MessageExecution(ctx, string(envelope.Fingerprint()))
	if err != nil {
		return false, 0, err
	}
	if !found || execution.ContentSnapshot == nil {
		return true, 0, nil
	}
	err = preparer.PrepareFrozenAutomationMessageMedia(ctx, execution.ContentSnapshot, execution.ContentSnapshotDigest)
	if err == nil {
		return true, 0, nil
	}
	var pending outboundport.MediaPreparationPendingError
	if errors.As(err, &pending) {
		return false, pending.RetryAfter(), nil
	}
	var terminal outboundport.MediaPreparationTerminalError
	if errors.As(err, &terminal) || errors.Is(err, outboundport.ErrMaterialSourceChanged) || errors.Is(err, outboundport.ErrMaterialPreparationNotFound) {
		return true, 0, nil
	}
	return false, 0, err
}

type ExternalContactTextWriter interface {
	SendExternalContactText(context.Context, string, string, string) (string, error)
}
type MessageProvider struct {
	enabled    bool
	corpScope  string
	executions outboundport.MessageExecutionReader
	identities identityport.OutboundIdentityReader
	staff      accessport.OutboundStaffIdentityReader
	content    automationport.OutboundPublishedContentReader
	payloads   outboundport.FrozenAutomationMessagePayloadReader
	writer     PrivateMessageSender
}
type MessageProviderConfig struct {
	Enabled    bool
	CorpScope  string
	Executions outboundport.MessageExecutionReader
	Identities identityport.OutboundIdentityReader
	Staff      accessport.OutboundStaffIdentityReader
	Content    automationport.OutboundPublishedContentReader
	Payloads   outboundport.FrozenAutomationMessagePayloadReader
	Writer     PrivateMessageSender
}

func NewMessageProvider(c MessageProviderConfig) (*MessageProvider, error) {
	if c.Executions == nil || c.Identities == nil || c.Staff == nil || c.Content == nil || c.Payloads == nil || c.Writer == nil || c.CorpScope == "" {
		return nil, ErrInvalidMessageIntent
	}
	return &MessageProvider{c.Enabled, c.CorpScope, c.Executions, c.Identities, c.Staff, c.Content, c.Payloads, c.Writer}, nil
}
func (p *MessageProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || envelope.Kind != effectport.KindAutomationMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.invalid")}, nil
	}
	if !p.enabled {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.provider-disabled", string(envelope.Fingerprint()))}, nil
	}
	execution, found, err := p.executions.MessageExecution(ctx, string(envelope.Fingerprint()))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable}, err
	}
	if !found {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.intent-missing", string(envelope.Fingerprint()))}, nil
	}
	payload := PrivateMessagePayload{}
	if execution.ContentSnapshot != nil {
		payload, err = p.payloads.LoadFrozenAutomationMessagePayload(ctx, execution.ContentSnapshot, execution.ContentSnapshotDigest)
		if err != nil {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.frozen-content-unavailable", string(envelope.Fingerprint()))}, nil
		}
	} else {
		// 0089 did not exist for historical intents. Only their already-supported
		// pure-text package can be read through the old path; never rebuild a
		// material-bearing intent from mutable current Media.
		content, contentFound, contentErr := p.content.OutboundPublishedContent(ctx, automationport.AgentID(execution.AgentID), execution.AgentPublishedVersion)
		if contentErr != nil {
			return effectport.AdapterResult{Completion: effectport.StateRetryable}, contentErr
		}
		if !contentFound || content.ContentDigest != execution.PayloadDigest {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.content-drift", string(envelope.Fingerprint()))}, nil
		}
		if content.Content.ContentText == "" || len(content.Content.ImageLibraryIDs) > 0 || len(content.Content.MiniprogramLibraryIDs) > 0 || len(content.Content.AttachmentLibraryIDs) > 0 || len(content.Content.GroupInviteLibraryIDs) > 0 || content.Content.DynamicMiniprogramCard != nil {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.legacy-material-unrecoverable", string(envelope.Fingerprint()))}, nil
		}
		payload.Text = content.Content.ContentText
	}
	identity, found, err := p.identities.VerifiedOutboundIdentity(ctx, execution.CustomerID, identitydomain.KindWeComExternalUserID, p.corpScope)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable}, err
	}
	if !found {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.identity-unavailable", string(envelope.Fingerprint()))}, nil
	}
	sender, found, err := p.staff.OutboundProviderStaffID(ctx, accessport.StaffID(execution.SenderStaffID))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable}, err
	}
	if !found {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.sender-unavailable", string(envelope.Fingerprint()))}, nil
	}
	target := PrivateMessageTarget{ExternalUserID: identity.Value, StaffUserID: sender}
	receipt, attempted, err := p.writer.SendPrivateMessage(ctx, target, payload)
	if err != nil {
		// The sender distinguishes a preflight rejection (invalid payload,
		// attachment limit, or provider permission failure) from a request whose
		// outcome is genuinely unknown. Do not turn deterministic rejections into
		// retries or unknown effects merely because this adapter was attempted.
		state := effectport.StateRetryable
		if failure, ok := err.(PrivateMessageSendError); ok {
			state = effectport.StateFinalFailed
			if attempted && failure.OutcomeUnknown() {
				state = effectport.StateUnknown
			} else if retryable, ok := err.(outboundport.PrivateMessageRetryableRejection); ok && attempted && retryable.Retryable() {
				_ = p.record(ctx, execution, target, "", retryable.FailureCode())
				return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("outbound.message.provider-retryable-rejection", retryable.FailureCode(), string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: false, SafeToRetryRejected: true, FailureCode: retryable.FailureCode()}, nil
			}
			// The typed sender error is already a complete Provider outcome. The
			// effect kernel treats a returned error as retryable/unknown before it
			// examines AdapterResult, so keep this classification observable.
			p.record(ctx, execution, target, "", failureCode(err, string(state)))
			return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("outbound.message.provider-error", string(envelope.Fingerprint())), CallAttempted: attempted, RealExternalCallExecuted: attempted}, nil
		} else if attempted {
			state = effectport.StateUnknown
		}
		_ = p.record(ctx, execution, target, "", failureCode(err, string(state)))
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("outbound.message.provider-error", string(envelope.Fingerprint())), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	if !attempted || receipt.MessageID == "" {
		_ = p.record(ctx, execution, target, "", "provider_rejected")
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.provider-rejected", string(envelope.Fingerprint()))}, nil
	}
	if err = p.record(ctx, execution, target, receipt.MessageID, ""); err != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("outbound.message.receipt-unavailable", string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("outbound.message.provider-receipt", receipt.MessageID, string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func (p *MessageProvider) record(ctx context.Context, execution outboundport.MessageExecution, target PrivateMessageTarget, messageID, reason string) error {
	if recorder, ok := p.executions.(outboundport.MessageReceiptRecorder); ok {
		return recorder.RecordAutomationMessageReceipt(ctx, execution.MessageIntentID, execution.ContentReference, target, messageID, reason)
	}
	return nil
}

var _ effectport.ProviderAdapter = (*MessageProvider)(nil)
