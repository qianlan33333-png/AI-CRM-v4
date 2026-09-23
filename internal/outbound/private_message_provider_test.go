package outbound

import (
	"context"
	"errors"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type privateIntentReaderFunc func(context.Context, effectport.Envelope) (PrivateMessageIntent, error)

func (f privateIntentReaderFunc) PrivateMessageIntentForEnvelope(ctx context.Context, envelope effectport.Envelope) (PrivateMessageIntent, error) {
	return f(ctx, envelope)
}

type privateTargetResolverFunc func(context.Context, customerdomain.CustomerID, int64) (PrivateMessageTarget, error)

func (f privateTargetResolverFunc) ResolvePrivateMessageTarget(ctx context.Context, customerID customerdomain.CustomerID, staffID int64) (PrivateMessageTarget, error) {
	return f(ctx, customerID, staffID)
}

type privatePayloadReaderFunc func(context.Context, string, effectport.Digest) (PrivateMessagePayload, error)

func (f privatePayloadReaderFunc) LoadPrivateMessagePayload(ctx context.Context, reference string, digest effectport.Digest) (PrivateMessagePayload, error) {
	return f(ctx, reference, digest)
}

type privateSenderFunc func(context.Context, PrivateMessageTarget, PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error)

func (f privateSenderFunc) SendPrivateMessage(ctx context.Context, target PrivateMessageTarget, payload PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error) {
	return f(ctx, target, payload)
}

type privateProviderError struct {
	unknown bool
	retry   bool
}

func (e privateProviderError) Error() string        { return "provider failed" }
func (e privateProviderError) OutcomeUnknown() bool { return e.unknown }
func (e privateProviderError) Retryable() bool      { return e.retry }
func (e privateProviderError) FailureCode() string  { return "wecom_errcode_45009" }

func privateMessageEnvelope() effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindOutboundMessage, SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Hash("target"), PayloadDigest: effectport.Hash("payload"), PolicyVersionHash: effectport.Hash("policy")}
}

func privateProviderForTest(t *testing.T, enabled bool, sender PrivateMessageSender) *PrivateMessageProvider {
	t.Helper()
	provider, err := NewPrivateMessageProvider(enabled,
		privateIntentReaderFunc(func(context.Context, effectport.Envelope) (PrivateMessageIntent, error) {
			return PrivateMessageIntent{CustomerID: 11, StaffID: 22, PayloadReference: "aiassistant:1:2:3", PayloadDigest: effectport.Hash("payload")}, nil
		}),
		privateTargetResolverFunc(func(context.Context, customerdomain.CustomerID, int64) (PrivateMessageTarget, error) {
			return PrivateMessageTarget{ExternalUserID: "external", StaffUserID: "staff"}, nil
		}),
		privatePayloadReaderFunc(func(context.Context, string, effectport.Digest) (PrivateMessagePayload, error) {
			return PrivateMessagePayload{Text: "hello"}, nil
		}), sender)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestPrivateMessageProviderOnlyClaimsProviderAcceptedAfterRealCall(t *testing.T) {
	called := false
	provider := privateProviderForTest(t, true, privateSenderFunc(func(context.Context, PrivateMessageTarget, PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error) {
		called = true
		return PrivateMessageProviderReceipt{MessageID: "msg-1"}, true, nil
	}))
	result, err := provider.Execute(context.Background(), privateMessageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || !called || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || !effectport.ValidDigest(result.ReceiptDigest) {
		t.Fatalf("result=%+v called=%v err=%v", result, called, err)
	}
}

func TestPrivateMessageProviderFailureClassification(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		attempted bool
		err       error
		want      effectport.State
	}{
		{name: "disabled", enabled: false, want: effectport.StateFinalFailed},
		{name: "before call", enabled: true, err: errors.New("local failure"), want: effectport.StateRetryable},
		{name: "invalid before call", enabled: true, err: privateProviderError{}, want: effectport.StateFinalFailed},
		{name: "provider rejected", enabled: true, attempted: true, err: privateProviderError{}, want: effectport.StateFinalFailed},
		{name: "provider rate limited", enabled: true, attempted: true, err: privateProviderError{retry: true}, want: effectport.StateRetryable},
		{name: "ambiguous after call", enabled: true, attempted: true, err: privateProviderError{unknown: true}, want: effectport.StateUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			provider := privateProviderForTest(t, test.enabled, privateSenderFunc(func(context.Context, PrivateMessageTarget, PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error) {
				called = true
				return PrivateMessageProviderReceipt{}, test.attempted, test.err
			}))
			result, err := provider.Execute(context.Background(), privateMessageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
			if err != nil || result.Completion != test.want || result.CallAttempted != test.attempted || (test.want == effectport.StateRetryable && test.attempted && (!result.SafeToRetryRejected || result.RealExternalCallExecuted)) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !test.enabled && called {
				t.Fatal("disabled provider performed a network call")
			}
		})
	}
}

type failReceiptReader struct{ PrivateMessageIntentReader }

func (f failReceiptReader) RecordPrivateMessageReceipt(context.Context, string, PrivateMessageTarget, string, string) error {
	return errors.New("database interrupted")
}
func TestReceiptWriteFailureAfterProviderAcceptanceIsUnknown(t *testing.T) {
	provider := privateProviderForTest(t, true, privateSenderFunc(func(context.Context, PrivateMessageTarget, PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error) {
		return PrivateMessageProviderReceipt{MessageID: "accepted-task"}, true, nil
	}))
	provider.intents = failReceiptReader{provider.intents}
	result, err := provider.Execute(context.Background(), privateMessageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateUnknown || !result.CallAttempted {
		t.Fatalf("receipt loss must not retry: %#v %v", result, err)
	}
}

type captureReasonReader struct {
	PrivateMessageIntentReader
	reason *string
}

func (r captureReasonReader) RecordPrivateMessageReceipt(_ context.Context, _ string, _ PrivateMessageTarget, _ string, reason string) error {
	*r.reason = reason
	return nil
}
func TestMissingExcelTitleFailsWithoutProviderTask(t *testing.T) {
	called := false
	provider := privateProviderForTest(t, true, privateSenderFunc(func(context.Context, PrivateMessageTarget, PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error) {
		called = true
		return PrivateMessageProviderReceipt{}, false, nil
	}))
	reason := ""
	provider.intents = captureReasonReader{provider.intents, &reason}
	provider.payloads = privatePayloadReaderFunc(func(context.Context, string, effectport.Digest) (PrivateMessagePayload, error) {
		return PrivateMessagePayload{}, outboundport.PayloadPreparationError("title_missing")
	})
	result, err := provider.Execute(context.Background(), privateMessageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || called || result.Completion != effectport.StateFinalFailed || result.CallAttempted || reason != "title_missing" {
		t.Fatalf("empty title created task or lost reason: %#v %s %v", result, reason, err)
	}
}
