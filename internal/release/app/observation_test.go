package app

import (
	"context"
	"errors"
	"testing"

	releaseport "github.com/qianlan33333-png/AI-CRM-v3/internal/release/port"
)

func TestObservationServiceRequiresSafeLocalFact(t *testing.T) {
	writer := &observationWriterStub{}
	service, err := NewObservationService(writer)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Record(context.Background(), releaseport.ReleaseObservation{ReleaseSHA: "development", Status: "observed"}); err != nil {
		t.Fatal(err)
	}
	if writer.observation.ReleaseSHA != "development" || writer.observation.Status != "observed" || writer.observation.ObservedAt.IsZero() {
		t.Fatalf("observation=%#v", writer.observation)
	}
	if err = service.Record(context.Background(), releaseport.ReleaseObservation{ReleaseSHA: "secret-token", Status: "observed"}); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("unsafe observation error=%v", err)
	}
}

type observationWriterStub struct {
	observation releaseport.ReleaseObservation
}

func (stub *observationWriterStub) RecordReleaseObservation(_ context.Context, value releaseport.ReleaseObservation) error {
	stub.observation = value
	return nil
}

var _ releaseport.ObservationWriter = (*observationWriterStub)(nil)
