package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"strings"
	"testing"
)

func TestCorrelationSurvivesInsertionAndWorkerReleaseChange(t *testing.T) {
	input := diagnostics.Correlation{RequestID: strings.Repeat("a", 32), ReleaseSHA: strings.Repeat("b", 40), EffectRef: "eer_9"}
	ctx, err := diagnostics.WithCorrelation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	p := &rivertype.JobInsertParams{Metadata: []byte(`{"other":"preserved"}`)}
	middleware := &CorrelationMiddleware{Release: strings.Repeat("c", 40)}
	_, err = middleware.InsertMany(ctx, []*rivertype.JobInsertParams{p}, func(context.Context) ([]*rivertype.JobInsertResult, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	_ = json.Unmarshal(p.Metadata, &meta)
	if string(meta["other"]) != `"preserved"` {
		t.Fatal("unrelated metadata lost")
	}
	var observed diagnostics.Observation
	middleware.Record = func(_ context.Context, o diagnostics.Observation) error {
		observed = o
		panic("recorder secret must not leak")
	}
	expected := errors.New("business failure")
	err = middleware.Work(context.Background(), &rivertype.JobRow{ID: 7, Attempt: 3, Metadata: p.Metadata}, func(ctx context.Context) error {
		got := diagnostics.FromContext(ctx)
		if got.RequestID != input.RequestID || got.ReleaseSHA != middleware.Release || got.JobRef != "river_7" || got.EffectRef != "eer_9" {
			t.Fatalf("correlation=%+v", got)
		}
		return expected
	})
	if err != expected {
		t.Fatalf("recording changed outcome: %v", err)
	}
	if observed.Correlation != input.RequestID || observed.Code != "durable_job_failed" || observed.JobAttempt != 3 {
		t.Fatalf("observation=%+v", observed)
	}
}
func TestCorrelationRejectsUntrustedMetadataAndSnoozeIsNotFailure(t *testing.T) {
	calls := 0
	m := &CorrelationMiddleware{Release: "bad release secret", Record: func(context.Context, diagnostics.Observation) error { calls++; return nil }}
	err := m.Work(context.Background(), &rivertype.JobRow{ID: 1, Metadata: []byte(`{"aicrm_diagnostic":{"RequestID":"token", "ReleaseSHA":"secret"}}`)}, func(ctx context.Context) error {
		c := diagnostics.FromContext(ctx)
		if len(c.RequestID) != 32 || c.ReleaseSHA != "unknown" {
			t.Fatalf("unsafe context=%+v", c)
		}
		return river.JobSnooze(1)
	})
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) || calls != 0 {
		t.Fatal("snooze changed into failure")
	}
}
func TestCorrelationPanicDoesNotDiscloseOriginalValue(t *testing.T) {
	m := &CorrelationMiddleware{}
	defer func() {
		if r := recover(); r != "durable_job_panic" {
			t.Fatalf("unexpected panic %v", r)
		}
	}()
	_ = m.Work(context.Background(), &rivertype.JobRow{ID: 1}, func(context.Context) error { panic("private token") })
}
