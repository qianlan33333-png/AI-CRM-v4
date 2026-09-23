package jobqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// CorrelationMiddleware preserves request identity through River's native hooks.
// It owns no scheduler, retry policy or domain-specific execution state.
type CorrelationMiddleware struct {
	river.MiddlewareDefaults
	Release string
	Record  diagnostics.Recorder
}

func normalizedRelease(release string) string {
	_, err := diagnostics.WithCorrelation(context.Background(), diagnostics.Correlation{RequestID: "00000000000000000000000000000001", ReleaseSHA: release})
	if err != nil {
		return "unknown"
	}
	return release
}
func newCorrelation(release string) (diagnostics.Correlation, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return diagnostics.Correlation{}, errors.New("diagnostic identity unavailable")
	}
	return diagnostics.Correlation{RequestID: hex.EncodeToString(b[:]), ReleaseSHA: normalizedRelease(release)}, nil
}
func (m *CorrelationMiddleware) InsertMany(ctx context.Context, params []*rivertype.JobInsertParams, next func(context.Context) ([]*rivertype.JobInsertResult, error)) ([]*rivertype.JobInsertResult, error) {
	correlation := diagnostics.FromContext(ctx)
	var err error
	if correlation.RequestID == "" {
		correlation, err = newCorrelation(m.Release)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(correlation)
	if err != nil {
		return nil, err
	}
	for _, p := range params {
		metadata := map[string]json.RawMessage{}
		if len(p.Metadata) > 0 {
			if err = json.Unmarshal(p.Metadata, &metadata); err != nil {
				return nil, errors.New("invalid River metadata")
			}
		}
		if metadata == nil {
			metadata = map[string]json.RawMessage{}
		}
		metadata["aicrm_diagnostic"] = encoded
		p.Metadata, err = json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
	}
	return next(ctx)
}
func (m *CorrelationMiddleware) Work(ctx context.Context, job *rivertype.JobRow, next func(context.Context) error) (err error) {
	correlation, err := newCorrelation(m.Release)
	if err != nil {
		return err
	}
	var metadata struct {
		Correlation diagnostics.Correlation `json:"aicrm_diagnostic"`
	}
	if json.Unmarshal(job.Metadata, &metadata) == nil {
		if _, e := diagnostics.WithCorrelation(ctx, metadata.Correlation); e == nil {
			correlation = metadata.Correlation
		}
	}
	correlation.ReleaseSHA = normalizedRelease(m.Release)
	correlation.JobRef = "river_" + strconv.FormatInt(job.ID, 10)
	workctx, err := diagnostics.WithCorrelation(ctx, correlation)
	if err != nil {
		return err
	}
	defer func() {
		recovered := recover()
		var snooze *river.JobSnoozeError
		if recovered != nil {
			m.record(workctx, correlation, job.Attempt, "durable_job_panic")
			panic("durable_job_panic")
		}
		if err != nil && !errors.As(err, &snooze) {
			m.record(workctx, correlation, job.Attempt, "durable_job_failed")
		}
	}()
	return next(workctx)
}
func (m *CorrelationMiddleware) record(ctx context.Context, c diagnostics.Correlation, attempt int, code string) {
	// Recorder failures must not change the job's retry or completion semantics.
	defer func() { _ = recover() }()
	if m.Record == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	_ = m.Record(ctx, diagnostics.Observation{Code: code, Correlation: c.RequestID, ReleaseSHA: c.ReleaseSHA, JobRef: c.JobRef, EffectRef: c.EffectRef, JobAttempt: attempt, RouteTemplate: "/background"})
}
