package outbound

import (
	"context"
	"testing"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type tagSnapshotWriterStub struct{ observation tagport.SyncCompletion }

func (writer *tagSnapshotWriterStub) CompleteProviderSync(_ context.Context, observation tagport.SyncCompletion) error {
	writer.observation = observation
	return nil
}

func TestTagCatalogCompletionSinkProjectsComputedOutcome(t *testing.T) {
	writer := &tagSnapshotWriterStub{}
	sink, err := NewTagCatalogCompletionSink(writer)
	if err != nil {
		t.Fatal(err)
	}
	envelope := effectport.Envelope{Kind: effectport.KindWeComTagCatalog}
	if err = sink.CompleteEffect(context.Background(), "eer_17", envelope, effectport.Attempt{Generation: 2}, effectport.AdapterResult{Completion: effectport.StateUnknown}); err != nil {
		t.Fatal(err)
	}
	if writer.observation.EffectID != 17 || writer.observation.Generation != 2 || writer.observation.State != tagport.SyncOutcomeUnknown || len(writer.observation.Snapshot) != 0 {
		t.Fatalf("observation = %#v", writer.observation)
	}
}
