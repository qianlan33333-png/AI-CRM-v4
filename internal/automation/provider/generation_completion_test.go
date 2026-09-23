package provider

import (
	"context"
	"testing"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type generationCompletionWriterStub struct {
	completion automationport.GenerationCompletion
}

func (s *generationCompletionWriterStub) CompleteGeneration(_ context.Context, value automationport.GenerationCompletion) error {
	s.completion = value
	return nil
}

func TestGenerationCompletionSinkPassesTerminalResultWithinCallerTransaction(t *testing.T) {
	writer := &generationCompletionWriterStub{}
	sink, err := NewGenerationCompletionSink(writer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	sink.now = func() time.Time { return now }
	attempt := effectport.Attempt{EffectID: "eer_generation_test", Number: 1, Generation: 1, Fence: 1}
	result := effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("generation-completion"), FailureCode: "generation_call_unknown"}
	if err = sink.CompleteEffect(context.Background(), attempt.EffectID, effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate}, attempt, result); err != nil {
		t.Fatal(err)
	}
	if writer.completion.EffectID != attempt.EffectID || writer.completion.State != effectport.StateUnknown || writer.completion.FailureCode != result.FailureCode || !writer.completion.CompletedAt.Equal(now) {
		t.Fatalf("completion=%+v", writer.completion)
	}
}

func TestGenerationCompletionSinkRejectsNonterminalOrWrongEffect(t *testing.T) {
	writer := &generationCompletionWriterStub{}
	sink, err := NewGenerationCompletionSink(writer)
	if err != nil {
		t.Fatal(err)
	}
	if err = sink.CompleteEffect(context.Background(), "eer_generation_test", effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate}, effectport.Attempt{Number: 1}, effectport.AdapterResult{Completion: effectport.StateQueued, ReceiptDigest: effectport.Hash("generation-completion")}); err == nil {
		t.Fatal("nonterminal completion accepted")
	}
	if err = sink.CompleteEffect(context.Background(), "eer_generation_test", effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindAIAgentGenerate}, effectport.Attempt{Number: 1}, effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("generation-completion")}); err == nil {
		t.Fatal("wrong owner accepted")
	}
}
