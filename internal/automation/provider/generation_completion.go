package provider

import (
	"context"
	"errors"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

// GenerationCompletionSink is the narrow External Effects adapter for an
// Automation-owned generation effect. The caller already holds EER's effect
// completion transaction, so the writer can settle the owner row and create
// one AI Assistant review plan atomically with that effect transition.
type GenerationCompletionSink struct {
	owner  effectport.Owner
	kind   effectport.Kind
	writer automationport.GenerationCompletionWriter
	now    func() time.Time
}

func NewGenerationCompletionSink(writer automationport.GenerationCompletionWriter) (*GenerationCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("AI generation completion writer is required")
	}
	return &GenerationCompletionSink{owner: effectport.OwnerAutomation, kind: effectport.KindAIAgentGenerate, writer: writer, now: time.Now}, nil
}

func (s *GenerationCompletionSink) CompleteEffect(ctx context.Context, effectID string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.writer == nil || effectID == "" || envelope.Owner != s.owner || envelope.Kind != s.kind || attempt.Number < 1 || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid AI generation completion")
	}
	switch result.Completion {
	case effectport.StateExecuted, effectport.StateRetryable, effectport.StateFinalFailed, effectport.StateUnknown:
	default:
		return errors.New("nonterminal AI generation completion")
	}
	return s.writer.CompleteGeneration(ctx, automationport.GenerationCompletion{EffectID: effectID, State: result.Completion, Attempt: attempt, ReceiptDigest: result.ReceiptDigest, Artifact: result.Artifact, FailureCode: result.FailureCode, CompletedAt: s.now().UTC()})
}

var _ effectport.CompletionSink = (*GenerationCompletionSink)(nil)

func NewAudienceRecommendationCompletionSink(writer automationport.GenerationCompletionWriter) (*GenerationCompletionSink, error) {
	s, e := NewGenerationCompletionSink(writer)
	if e != nil {
		return nil, e
	}
	s.owner = effectport.OwnerSegment
	s.kind = effectport.KindAIRecommend
	return s, nil
}
