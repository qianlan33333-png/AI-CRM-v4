package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const DistributionSettlementQueue = "distribution-settlement"

// CommissionDueJobArgs is intentionally small and durable. Replays always
// reload the commission and Payment facts; it never embeds money, a receiver,
// policy or provider result from an earlier attempt.
type CommissionDueJobArgs struct {
	CommissionID int64 `json:"commission_id"`
}

func (CommissionDueJobArgs) Kind() string { return "distribution.commission-due-check.v1" }

type RiverCommissionDueEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverCommissionDueEnqueuer(client *river.Client[pgx.Tx]) (*RiverCommissionDueEnqueuer, error) {
	if client == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &RiverCommissionDueEnqueuer{client: client}, nil
}

func (e *RiverCommissionDueEnqueuer) EnqueueCommissionDueWithin(ctx context.Context, commissionID int64, dueAt time.Time) error {
	if e == nil || e.client == nil || commissionID < 1 || dueAt.IsZero() {
		return distributionport.ErrUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return distributionport.ErrUnavailable
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, CommissionDueJobArgs{CommissionID: commissionID}, river.InsertOpts{Queue: DistributionSettlementQueue, MaxAttempts: 24, ScheduledAt: dueAt.UTC(), UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

type DueCheckApplication interface {
	RunCommissionDueCheck(context.Context, int64) error
}

type CommissionDueWorker struct {
	river.WorkerDefaults[CommissionDueJobArgs]
	service DueCheckApplication
}

func NewCommissionDueWorker() *CommissionDueWorker { return &CommissionDueWorker{} }

func (*CommissionDueWorker) Timeout(*river.Job[CommissionDueJobArgs]) time.Duration {
	return 30 * time.Second
}

func (w *CommissionDueWorker) BindService(service DueCheckApplication) error {
	if w == nil || w.service != nil || service == nil {
		return distributionport.ErrUnavailable
	}
	w.service = service
	return nil
}

func (w *CommissionDueWorker) Work(ctx context.Context, job *river.Job[CommissionDueJobArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || job.Args.CommissionID < 1 {
		return distributionport.ErrUnavailable
	}
	return w.service.RunCommissionDueCheck(ctx, job.Args.CommissionID)
}

var _ DueCheckEnqueuer = (*RiverCommissionDueEnqueuer)(nil)

// RefundRecheckJobArgs is a durable pointer to an already-accepted Payment or
// Order refund fact. It carries no provider result; the worker reloads Payment
// and qualification facts after the Payment transaction commits.
type RefundRecheckJobArgs struct {
	OrderID    int64     `json:"order_id"`
	RefundID   int64     `json:"refund_id,omitempty"`
	State      string    `json:"state"`
	OccurredAt time.Time `json:"occurred_at"`
	ReceiptKey string    `json:"receipt_key"`
}

func (RefundRecheckJobArgs) Kind() string { return "distribution.refund-recheck.v1" }

func (v RefundRecheckJobArgs) Valid() bool {
	return v.OrderID > 0 && (v.State == "successful" || v.RefundID > 0) && (v.State == "successful" || v.State == "opened" || v.State == "final_failed") && !v.OccurredAt.IsZero() && v.ReceiptKey != "" && len(v.ReceiptKey) <= 240
}

type RefundRecheckEnqueuer interface {
	EnqueueRefundRecheckWithin(context.Context, RefundRecheckJobArgs) error
}

type RiverRefundRecheckEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverRefundRecheckEnqueuer(client *river.Client[pgx.Tx]) (*RiverRefundRecheckEnqueuer, error) {
	if client == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &RiverRefundRecheckEnqueuer{client: client}, nil
}

func (e *RiverRefundRecheckEnqueuer) EnqueueRefundRecheckWithin(ctx context.Context, args RefundRecheckJobArgs) error {
	if e == nil || e.client == nil || !args.Valid() {
		return distributionport.ErrUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return distributionport.ErrUnavailable
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, args, river.InsertOpts{Queue: DistributionSettlementQueue, MaxAttempts: 24, ScheduledAt: args.OccurredAt.UTC(), UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

type RefundRecheckApplication interface {
	RunRefundRecheck(context.Context, RefundRecheckJobArgs) error
}

type RefundRecheckWorker struct {
	river.WorkerDefaults[RefundRecheckJobArgs]
	service RefundRecheckApplication
}

func NewRefundRecheckWorker() *RefundRecheckWorker { return &RefundRecheckWorker{} }

func (*RefundRecheckWorker) Timeout(*river.Job[RefundRecheckJobArgs]) time.Duration {
	return 30 * time.Second
}

func (w *RefundRecheckWorker) BindService(service RefundRecheckApplication) error {
	if w == nil || w.service != nil || service == nil {
		return distributionport.ErrUnavailable
	}
	w.service = service
	return nil
}

func (w *RefundRecheckWorker) Work(ctx context.Context, job *river.Job[RefundRecheckJobArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || !job.Args.Valid() {
		return distributionport.ErrUnavailable
	}
	return w.service.RunRefundRecheck(ctx, job.Args)
}

var _ RefundRecheckEnqueuer = (*RiverRefundRecheckEnqueuer)(nil)
