package app

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

type ContinuationJobArgs struct {
	EffectID string `json:"effect_id" river:"unique"`
}

func (ContinuationJobArgs) Kind() string { return "group-ops.message-continuation.v1" }

type RiverContinuationEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverContinuationEnqueuer(client *river.Client[pgx.Tx]) (*RiverContinuationEnqueuer, error) {
	if client == nil {
		return nil, ErrUnavailable
	}
	return &RiverContinuationEnqueuer{client}, nil
}
func (e *RiverContinuationEnqueuer) EnqueueGroupOpsContinuationWithin(ctx context.Context, effectID string) error {
	if e == nil || e.client == nil || effectID == "" {
		return ErrUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, ContinuationJobArgs{EffectID: effectID}, river.InsertOpts{Queue: platformjobqueue.OutboundQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

type ContinuationApplication interface {
	ContinueEffect(context.Context, string) error
}
type ContinuationWorker struct {
	river.WorkerDefaults[ContinuationJobArgs]
	service ContinuationApplication
}

func NewContinuationWorker() *ContinuationWorker { return &ContinuationWorker{} }
func (*ContinuationWorker) Timeout(*river.Job[ContinuationJobArgs]) time.Duration {
	return 30 * time.Second
}
func (w *ContinuationWorker) Bind(service ContinuationApplication) error {
	if w == nil || w.service != nil || service == nil {
		return ErrUnavailable
	}
	w.service = service
	return nil
}
func (w *ContinuationWorker) Work(ctx context.Context, job *river.Job[ContinuationJobArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || job.Args.EffectID == "" {
		return ErrUnavailable
	}
	return w.service.ContinueEffect(ctx, job.Args.EffectID)
}

// ContinueEffect accepts only the immediate frozen successor. It never calls
// a provider: EER schedules the resulting effect after this UoW commits.
func (s *RuntimeService) ContinueEffect(ctx context.Context, effectID string) error {
	if !s.ready() || effectID == "" {
		return ErrRuntimeInvalid
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		draft, found, err := s.runtime.ClaimNextExecutionIntent(tx, effectID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		detail, err := s.plans.Get(tx, draft.PlanID)
		if err != nil {
			return err
		}
		if detail.Plan.Status != port.PlanActive || detail.Plan.Revision != draft.PlanRevision {
			return s.runtime.HaltExecutionIntent(tx, draft.IntentID)
		}
		projection, receipt, err := s.effects.AcceptAndQueueWithin(tx, groupOpsEffectAcceptCommand(draft, "continuation:"+effectID))
		if err != nil {
			return err
		}
		if projection.ID == "" || projection.QueueJobID < 1 || receipt.ID == "" || receipt.QueueReceiptID == "" {
			return errors.New("Group Ops continuation acceptance unavailable")
		}
		draft.ExternalEffectID = projection.ID
		if _, err = s.runtime.InsertExecution(tx, draft); err != nil {
			return err
		}
		return s.runtime.BindAcceptedExecutionIntent(tx, draft.IntentID, projection.ID)
	})
}

var _ port.ExecutionContinuationEnqueuer = (*RiverContinuationEnqueuer)(nil)
