package wecom

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const CustomerSyncQueue = "customer-directory"

const customerSyncJobTimeout = 30 * time.Minute

type CustomerSyncJobArgs struct {
	RunID int64 `json:"run_id"`
}

func (CustomerSyncJobArgs) Kind() string { return "wecom.customer-directory-sync.v1" }

// CustomerSyncJobEnqueuer is the narrow durable-delivery port used while the
// sync run, audit and River row share the caller's PostgreSQL transaction.
type CustomerSyncJobEnqueuer interface {
	EnqueueCustomerSync(context.Context, int64) error
}

type RiverCustomerSyncEnqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewRiverCustomerSyncEnqueuer(client *river.Client[pgx.Tx]) (*RiverCustomerSyncEnqueuer, error) {
	if client == nil {
		return nil, ErrSyncNotReady
	}
	return &RiverCustomerSyncEnqueuer{client: client}, nil
}

func (enqueuer *RiverCustomerSyncEnqueuer) EnqueueCustomerSync(ctx context.Context, runID int64) error {
	if enqueuer == nil || enqueuer.client == nil || runID < 1 {
		return ErrSyncNotReady
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = enqueuer.client.InsertTx(ctx, tx, CustomerSyncJobArgs{RunID: runID}, &river.InsertOpts{
		Queue: CustomerSyncQueue, MaxAttempts: 12,
	})
	return err
}

type CustomerSyncWorker struct {
	river.WorkerDefaults[CustomerSyncJobArgs]
	service *CustomerSyncService
}

func NewCustomerSyncWorker() *CustomerSyncWorker { return &CustomerSyncWorker{} }

// Timeout covers a complete resumable run. River's one-minute default is too
// short for a full customer directory, while this bound still lets the job
// rescuer recover a genuinely stuck execution before its one-hour horizon.
func (*CustomerSyncWorker) Timeout(*river.Job[CustomerSyncJobArgs]) time.Duration {
	return customerSyncJobTimeout
}

func (worker *CustomerSyncWorker) BindService(service CustomerSyncService) error {
	if worker == nil || worker.service != nil || !service.Ready() {
		return ErrSyncNotReady
	}
	worker.service = &service
	return nil
}

func (worker *CustomerSyncWorker) Work(ctx context.Context, job *river.Job[CustomerSyncJobArgs]) error {
	if worker == nil || worker.service == nil || job == nil || job.JobRow == nil || job.Args.RunID < 1 {
		return ErrSyncNotReady
	}
	for {
		run, err := worker.service.Get(ctx, job.Args.RunID)
		if err != nil {
			return err
		}
		if run.Status == SyncSucceeded || run.Status == SyncFailedTerminal {
			return nil
		}
		if err = worker.service.processRunOnce(ctx, run); err == nil {
			continue
		}
		latest, loadErr := worker.service.Get(ctx, job.Args.RunID)
		if loadErr != nil {
			return errors.Join(err, loadErr)
		}
		if latest.Status == SyncFailedTerminal {
			return nil
		}
		maxAttempts := job.MaxAttempts
		var retry *syncRetryError
		if errors.As(err, &retry) && retry.maxAttempts > 0 && (maxAttempts < 1 || retry.maxAttempts < maxAttempts) {
			maxAttempts = retry.maxAttempts
		}
		if maxAttempts > 0 && job.Attempt >= maxAttempts {
			return worker.service.UOW.Within(ctx, func(txContext context.Context) error {
				return worker.service.Store.Terminate(txContext, job.Args.RunID, "retry_exhausted:"+syncRetryCode(err))
			})
		}
		return err
	}
}
