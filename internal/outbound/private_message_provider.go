package outbound

import (
	"context"
	"errors"
	"strconv"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type PrivateMessageIntent = outboundport.PrivateMessageIntent
type PrivateMessageTarget = outboundport.PrivateMessageTarget
type PrivateMessageAttachment = outboundport.PrivateMessageAttachment
type PrivateMessagePayload = outboundport.PrivateMessagePayload
type PrivateMessageProviderReceipt = outboundport.PrivateMessageProviderReceipt
type PrivateMessageIntentReader = outboundport.PrivateMessageIntentReader
type PrivateMessageTargetResolver = outboundport.PrivateMessageTargetResolver
type PrivateMessagePayloadReader = outboundport.PrivateMessagePayloadReader
type PrivateMessageSender = outboundport.PrivateMessageSender
type PrivateMessageSendError = outboundport.PrivateMessageSendError

type PrivateMessageProvider struct {
	enabled  bool
	intents  PrivateMessageIntentReader
	targets  PrivateMessageTargetResolver
	payloads PrivateMessagePayloadReader
	sender   PrivateMessageSender
}

func (p *PrivateMessageProvider) Preflight(ctx context.Context, envelope effectport.Envelope, _ string) (bool, time.Duration, error) {
	if p == nil || !p.enabled || envelope.Kind != effectport.KindOutboundMessage {
		return true, 0, nil
	}
	preparer, ok := p.payloads.(outboundport.PrivateMessageMediaPreflighter)
	if !ok {
		return true, 0, nil
	}
	intent, err := p.intents.PrivateMessageIntentForEnvelope(ctx, envelope)
	if err != nil {
		return false, 0, err
	}
	err = preparer.PreparePrivateMessageMedia(ctx, intent.PayloadReference, intent.PayloadDigest)
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

func NewPrivateMessageProvider(enabled bool, intents PrivateMessageIntentReader, targets PrivateMessageTargetResolver, payloads PrivateMessagePayloadReader, sender PrivateMessageSender) (*PrivateMessageProvider, error) {
	if intents == nil || targets == nil || payloads == nil || sender == nil {
		return nil, errors.New("private message provider dependencies are required")
	}
	return &PrivateMessageProvider{enabled, intents, targets, payloads, sender}, nil
}
func (p *PrivateMessageProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	base := effectport.Hash("outbound.private-message", string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number)), strconv.FormatInt(attempt.Generation, 10), strconv.FormatInt(attempt.Fence, 10))
	if p == nil || !p.enabled || envelope.Kind != effectport.KindOutboundMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "disabled")}, nil
	}
	intent, err := p.intents.PrivateMessageIntentForEnvelope(ctx, envelope)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "intent-unavailable")}, nil
	}
	var target PrivateMessageTarget
	if intent.DeferredTargetReference != "" {
		resolver, ok := p.targets.(outboundport.DeferredTargetResolver)
		if !ok {
			err = errors.New("deferred resolver unavailable")
		} else {
			target, err = resolver.ResolveDeferredPrivateMessageTarget(ctx, intent.DeferredTargetReference)
		}
	} else {
		target, err = p.targets.ResolvePrivateMessageTarget(ctx, intent.CustomerID, intent.StaffID)
	}
	if err != nil {
		p.record(ctx, intent.PayloadReference, target, "", failureCode(err, "target_unavailable"))
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "target-unavailable")}, nil
	}
	payload, err := p.payloads.LoadPrivateMessagePayload(ctx, intent.PayloadReference, intent.PayloadDigest)
	if err != nil {
		p.record(ctx, intent.PayloadReference, target, "", failureCode(err, "payload_unavailable"))
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "payload-unavailable")}, nil
	}
	receipt, attempted, err := p.sender.SendPrivateMessage(ctx, target, payload)
	if err != nil {
		state := effectport.StateRetryable
		if failure, ok := err.(PrivateMessageSendError); ok {
			state = effectport.StateFinalFailed
			if attempted && failure.OutcomeUnknown() {
				state = effectport.StateUnknown
			} else if retryable, ok := err.(outboundport.PrivateMessageRetryableRejection); ok && attempted && retryable.Retryable() {
				state = effectport.StateRetryable
				p.record(ctx, intent.PayloadReference, target, "", retryable.FailureCode())
				return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash(string(base), "provider-retryable-rejection", retryable.FailureCode()), CallAttempted: true, RealExternalCallExecuted: false, SafeToRetryRejected: true, FailureCode: retryable.FailureCode()}, nil
			}
		} else if attempted {
			state = effectport.StateUnknown
		}
		p.record(ctx, intent.PayloadReference, target, "", failureCode(err, string(state)))
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash(string(base), "provider-error"), CallAttempted: attempted, RealExternalCallExecuted: attempted}, nil
	}
	if !attempted || receipt.MessageID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-rejected")}, nil
	}
	if err = p.record(ctx, intent.PayloadReference, target, receipt.MessageID, ""); err != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash(string(base), "receipt-unavailable"), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash(string(base), "provider-accepted", receipt.MessageID), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

var _ effectport.ProviderAdapter = (*PrivateMessageProvider)(nil)

func (p *PrivateMessageProvider) record(ctx context.Context, ref string, target PrivateMessageTarget, id, reason string) error {
	if r, ok := p.intents.(outboundport.PrivateMessageReceiptRecorder); ok {
		return r.RecordPrivateMessageReceipt(ctx, ref, target, id, reason)
	}
	return nil
}

func failureCode(err error, fallback string) string {
	if coded, ok := err.(interface{ FailureCode() string }); ok {
		return coded.FailureCode()
	}
	return fallback
}
