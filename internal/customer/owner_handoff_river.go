package customer

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// OwnerHandoffQueue is deliberately an internal Customer queue. The worker
// only accepts bounded Customer facts/EER intents; outbound owns provider I/O.
const OwnerHandoffQueue = "customer-owner-handoff"

type OwnerHandoffBatchJobArgs struct {
	BatchID string `json:"batch_id"`
	Segment int64  `json:"segment"`
}

func (OwnerHandoffBatchJobArgs) Kind() string { return "customer.owner-handoff.v1" }

type RiverOwnerHandoffEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverOwnerHandoffEnqueuer(client *river.Client[pgx.Tx]) (*RiverOwnerHandoffEnqueuer, error) {
	if client == nil {
		return nil, platformjobqueue.ErrUnavailable
	}
	return &RiverOwnerHandoffEnqueuer{client: client}, nil
}

func (e *RiverOwnerHandoffEnqueuer) EnqueueOwnerHandoffBatchWithin(ctx context.Context, batchID string, segment int64) error {
	if e == nil || e.client == nil || batchID == "" || segment < 0 {
		return platformjobqueue.ErrUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, OwnerHandoffBatchJobArgs{BatchID: batchID, Segment: segment}, river.InsertOpts{Queue: OwnerHandoffQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

type ownerHandoffBatchApplication interface {
	ProcessOwnerHandoffBatch(context.Context, string, int64) error
}

type OwnerHandoffBatchWorker struct {
	river.WorkerDefaults[OwnerHandoffBatchJobArgs]
	service ownerHandoffBatchApplication
}

func NewOwnerHandoffBatchWorker() *OwnerHandoffBatchWorker { return &OwnerHandoffBatchWorker{} }
func (*OwnerHandoffBatchWorker) Timeout(*river.Job[OwnerHandoffBatchJobArgs]) time.Duration {
	return 2 * time.Minute
}
func (worker *OwnerHandoffBatchWorker) Bind(service ownerHandoffBatchApplication) error {
	if worker == nil || worker.service != nil || service == nil {
		return platformjobqueue.ErrUnavailable
	}
	worker.service = service
	return nil
}
func (worker *OwnerHandoffBatchWorker) Work(ctx context.Context, job *river.Job[OwnerHandoffBatchJobArgs]) error {
	if worker == nil || worker.service == nil || job == nil || job.JobRow == nil || job.Args.BatchID == "" || job.Args.Segment < 0 {
		return platformjobqueue.ErrUnavailable
	}
	return worker.service.ProcessOwnerHandoffBatch(ctx, job.Args.BatchID, job.Args.Segment)
}
