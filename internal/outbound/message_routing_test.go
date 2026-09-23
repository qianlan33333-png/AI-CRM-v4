package outbound

import (
	"context"
	"testing"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type recordingProvider struct{ calls int }

func (provider *recordingProvider) Execute(_ context.Context, _ effectport.Envelope, _ effectport.Attempt) (effectport.AdapterResult, error) {
	provider.calls++
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("recording-provider")}, nil
}

type recordingCompletionSink struct{ calls int }

func (sink *recordingCompletionSink) CompleteEffect(_ context.Context, _ string, _ effectport.Envelope, _ effectport.Attempt, _ effectport.AdapterResult) error {
	sink.calls++
	return nil
}

func TestAutomationAndPrivateMessageKindsAreRoutedIndependently(t *testing.T) {
	automationProvider := &recordingProvider{}
	privateProvider := &recordingProvider{}
	providerRouter := NewProviderRouterWithPrivate(nil, nil, privateProvider).WithAutomationMessage(automationProvider)

	automationEnvelope := messageEnvelope()
	privateEnvelope := automationEnvelope
	privateEnvelope.Kind = effectport.KindOutboundMessage
	if _, err := providerRouter.Execute(context.Background(), automationEnvelope, effectport.Attempt{}); err != nil {
		t.Fatal(err)
	}
	if _, err := providerRouter.Execute(context.Background(), privateEnvelope, effectport.Attempt{}); err != nil {
		t.Fatal(err)
	}
	if automationProvider.calls != 1 || privateProvider.calls != 1 {
		t.Fatalf("automation calls=%d private calls=%d", automationProvider.calls, privateProvider.calls)
	}

	automationSink := &recordingCompletionSink{}
	completionRouter := &CompletionRouter{automation: automationSink}
	result := effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("receipt")}
	if err := completionRouter.CompleteEffect(context.Background(), "eer_1", automationEnvelope, effectport.Attempt{}, result); err != nil {
		t.Fatal(err)
	}
	if err := completionRouter.CompleteEffect(context.Background(), "eer_2", privateEnvelope, effectport.Attempt{}, result); err == nil {
		t.Fatal("private message was incorrectly routed to the automation completion sink")
	}
	if automationSink.calls != 1 {
		t.Fatalf("automation completion calls=%d", automationSink.calls)
	}
}
