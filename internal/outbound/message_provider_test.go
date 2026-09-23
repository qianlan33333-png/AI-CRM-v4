package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type executionStub struct{ value outboundport.MessageExecution }

func (s executionStub) MessageExecution(context.Context, string) (outboundport.MessageExecution, bool, error) {
	return s.value, true, nil
}

type identityStub struct{ found bool }

func (s identityStub) VerifiedOutboundIdentity(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (identityport.OutboundIdentity, bool, error) {
	return identityport.OutboundIdentity{IdentityID: 1, CustomerID: 2, Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:c", Value: "external-secret"}, s.found, nil
}

type staffStub struct{}

func (staffStub) OutboundProviderStaffID(context.Context, accessport.StaffID) (string, bool, error) {
	return "sender-secret", true, nil
}

type contentStub struct {
	value automationport.OutboundPublishedContent
}

func (s contentStub) OutboundPublishedContent(context.Context, automationport.AgentID, int64) (automationport.OutboundPublishedContent, bool, error) {
	return s.value, true, nil
}

type writerStub struct {
	calls int
	err   error
}

func (w *writerStub) SendPrivateMessage(_ context.Context, target PrivateMessageTarget, payload PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error) {
	w.calls++
	if target.StaffUserID != "sender-secret" || target.ExternalUserID != "external-secret" || payload.Text != "hello" || len(payload.Attachments) != 0 {
		return PrivateMessageProviderReceipt{}, false, errors.New("unexpected provider payload")
	}
	return PrivateMessageProviderReceipt{MessageID: "provider-receipt"}, true, w.err
}

type frozenPayloadStub struct{ payload PrivateMessagePayload }

func (s frozenPayloadStub) LoadFrozenAutomationMessagePayload(context.Context, json.RawMessage, [32]byte) (PrivateMessagePayload, error) {
	return s.payload, nil
}

type crossedError struct{}

func (crossedError) Error() string               { return "uncertain" }
func (crossedError) ProviderCallAttempted() bool { return true }

type privateWriterError struct {
	unknown bool
	retry   bool
}

func (e privateWriterError) Error() string        { return "private provider failure" }
func (e privateWriterError) OutcomeUnknown() bool { return e.unknown }
func (e privateWriterError) Retryable() bool      { return e.retry }
func (e privateWriterError) FailureCode() string  { return "wecom_errcode_45009" }
func messageEnvelope() effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindAutomationMessage, SourceRefDigest: effectport.Hash("s"), TargetRefDigest: effectport.Hash("t"), PayloadDigest: effectport.Hash("p"), PolicyVersionHash: effectport.Hash("v")}
}
func messageProviderFixture(t *testing.T, enabled bool, identityFound bool, writer *writerStub) *MessageProvider {
	t.Helper()
	digest := [32]byte{1}
	execution := outboundport.MessageExecution{MessageIntentID: 1, RunRecipientID: 1, CustomerID: 2, SenderStaffID: 3, AgentID: 4, AgentPublishedVersion: 5, PayloadDigest: digest}
	content := automationport.OutboundPublishedContent{AgentID: 4, PublishedVersion: 5, ContentDigest: digest, Content: automationport.FixedContentPackage{ContentText: "hello"}}
	p, err := NewMessageProvider(MessageProviderConfig{Enabled: enabled, CorpScope: "wecom-corp:c", Executions: executionStub{execution}, Identities: identityStub{identityFound}, Staff: staffStub{}, Content: contentStub{content}, Payloads: frozenPayloadStub{}, Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestMessageProviderDisabledNeverCallsProvider(t *testing.T) {
	w := &writerStub{}
	result, err := messageProviderFixture(t, false, true, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || w.calls != 0 || result.Completion != effectport.StateFinalFailed || result.CallAttempted {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, w.calls)
	}
}
func TestMessageProviderAcceptedIsNotDeliveryProof(t *testing.T) {
	w := &writerStub{}
	result, err := messageProviderFixture(t, true, true, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || w.calls != 1 || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
func TestMessageProviderPostCallErrorIsOutcomeUnknown(t *testing.T) {
	w := &writerStub{err: crossedError{}}
	result, err := messageProviderFixture(t, true, true, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err == nil || !result.CallAttempted || result.Completion != effectport.StateUnknown {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
func TestMessageProviderPrivateMessageRejectionsAreFinalNotUnknown(t *testing.T) {
	for _, test := range []struct {
		name      string
		attempted bool
		unknown   bool
		want      effectport.State
	}{
		{name: "invalid payload before request", attempted: false, unknown: false, want: effectport.StateFinalFailed},
		{name: "provider rejection after request", attempted: true, unknown: false, want: effectport.StateFinalFailed},
		{name: "disconnected response", attempted: true, unknown: true, want: effectport.StateUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &writerStub{err: privateWriterError{unknown: test.unknown}}
			provider := messageProviderFixture(t, true, true, writer)
			// writerStub reports a crossed request; override it here to cover the
			// sender's explicit attempted bit without changing the production path.
			provider.writer = messageWriterAttempt{attempted: test.attempted, err: privateWriterError{unknown: test.unknown}}
			result, err := provider.Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
			if err != nil || result.Completion != test.want || result.CallAttempted != test.attempted {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestMessageProviderKnownRateLimitIsSafelyRetryable(t *testing.T) {
	provider := messageProviderFixture(t, true, true, &writerStub{})
	provider.writer = messageWriterAttempt{attempted: true, err: privateWriterError{retry: true}}
	result, err := provider.Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateRetryable || !result.CallAttempted || result.RealExternalCallExecuted || !result.SafeToRetryRejected || result.FailureCode != "wecom_errcode_45009" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

type messageWriterAttempt struct {
	attempted bool
	err       error
}

func (w messageWriterAttempt) SendPrivateMessage(_ context.Context, target PrivateMessageTarget, payload PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error) {
	if target.ExternalUserID != "external-secret" || target.StaffUserID != "sender-secret" || payload.Text != "hello" {
		return PrivateMessageProviderReceipt{}, false, errors.New("unexpected test payload")
	}
	return PrivateMessageProviderReceipt{}, w.attempted, w.err
}
func TestMessageProviderMissingIdentityFailsWithoutCall(t *testing.T) {
	w := &writerStub{}
	result, err := messageProviderFixture(t, true, false, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || w.calls != 0 || result.Completion != effectport.StateFinalFailed || result.CallAttempted {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
