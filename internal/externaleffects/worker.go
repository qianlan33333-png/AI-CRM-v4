package externaleffects

import (
	"context"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/riverqueue/river"
)

type EffectJobArgs struct {
	EffectID   int64 `json:"effect_id"`
	Generation int64 `json:"generation"`
}

func (args EffectJobArgs) DiagnosticEffectRef() string { return effectID(args.EffectID) }

func (EffectJobArgs) Kind() string { return "external_effect.execute.v1" }

type Worker struct {
	river.WorkerDefaults[EffectJobArgs]
	repository *Repository
	adapter    ProviderAdapter
}

// Timeout leaves the configured material upload budget below the five-minute
// EER attempt lease while overriding River's one-minute default. Message calls
// retain their own eight-second HTTP boundary inside the WeCom adapter.
func (w *Worker) Timeout(*river.Job[EffectJobArgs]) time.Duration {
	return 4*time.Minute + 30*time.Second
}

// ProviderAdapter is owned by outbound. A nil adapter means Provider disabled;
// it cannot claim an external call occurred.
type ProviderAdapter = port.ProviderAdapter
type Attempt = port.Attempt
type AdapterResult = port.AdapterResult

func NewWorker(repository *Repository, adapter ProviderAdapter) *Worker {
	return &Worker{repository: repository, adapter: adapter}
}
func (w *Worker) BindRepository(repository *Repository) error {
	if w == nil || repository == nil || w.repository != nil {
		return ErrInvalid
	}
	w.repository = repository
	return nil
}
func (w *Worker) Work(ctx context.Context, job *river.Job[EffectJobArgs]) error {
	if w == nil || w.repository == nil || job == nil {
		return ErrInvalid
	}
	if preflight, ok := w.adapter.(port.ProviderPreflighter); ok {
		envelope, queued, err := w.repository.QueuedEnvelope(ctx, job.Args.EffectID, job.Args.Generation, job.ID)
		if err != nil {
			return err
		}
		// Non-queued replay must still reach RunAttempt: it owns active lease
		// deferral and expired attempted -> unknown recovery, including projection.
		if queued {
			ready, retryAfter, err := preflight.Preflight(ctx, envelope, effectID(job.Args.EffectID))
			if err != nil {
				return err
			}
			if !ready {
				if retryAfter < time.Second {
					retryAfter = time.Second
				}
				return river.JobSnooze(retryAfter)
			}
		}
	}
	return w.repository.RunAttempt(ctx, job.Args.EffectID, job.Args.Generation, job.ID, w.adapter)
}
