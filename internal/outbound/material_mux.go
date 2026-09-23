package outbound

import (
	"context"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

// MaterialEffectMux preserves historical sidebar preparations while routing
// new generic preparations by their owner-owned effect binding.
type MaterialEffectMux struct {
	GenericProvider   *MaterialPreparationProvider
	LegacyProvider    effectport.ProviderAdapter
	GenericCompletion *MaterialPreparationService
	LegacyCompletion  effectport.CompletionSink
}

func (m MaterialEffectMux) Execute(ctx context.Context, e effectport.Envelope, a effectport.Attempt) (effectport.AdapterResult, error) {
	if m.GenericProvider != nil && m.GenericProvider.Handles(ctx, a.EffectID) {
		return m.GenericProvider.Execute(ctx, e, a)
	}
	if m.LegacyProvider != nil {
		return m.LegacyProvider.Execute(ctx, e, a)
	}
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.material.provider-unavailable")}, nil
}
func (m MaterialEffectMux) CompleteEffect(ctx context.Context, ref string, e effectport.Envelope, a effectport.Attempt, r effectport.AdapterResult) error {
	if m.GenericCompletion != nil && m.GenericCompletion.Handles(ctx, ref) {
		return m.GenericCompletion.CompleteEffect(ctx, ref, e, a, r)
	}
	if m.LegacyCompletion != nil {
		return m.LegacyCompletion.CompleteEffect(ctx, ref, e, a, r)
	}
	return ErrMaterialPreparation
}

var _ effectport.ProviderAdapter = MaterialEffectMux{}
var _ effectport.CompletionSink = MaterialEffectMux{}
