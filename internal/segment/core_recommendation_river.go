package segment

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	"github.com/riverqueue/river"
)

type CoreSubmissionArgs struct {
	CustomerID   int64  `json:"customer_id"`
	SubmissionID int64  `json:"submission_id"`
	TriggerKey   string `json:"trigger_key,omitempty"`
}

func (CoreSubmissionArgs) Kind() string { return "segment.core-submission.v1" }

type CoreSubmissionEnqueuer struct{ Client *river.Client[pgx.Tx] }

func (e CoreSubmissionEnqueuer) SubmissionCreatedWithin(ctx context.Context, submission, customer int64) error {
	if customer < 1 || submission < 1 || e.Client == nil {
		return errors.New("invalid core submission task")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.Client, tx, CoreSubmissionArgs{CustomerID: customer, SubmissionID: submission}, river.InsertOpts{Queue: AudienceRefreshQueue, MaxAttempts: 12})
	return err
}

type CoreSubmissionWorker struct {
	river.WorkerDefaults[CoreSubmissionArgs]
	Service *segmentapp.CoreOperations
}

func (w *CoreSubmissionWorker) Work(ctx context.Context, job *river.Job[CoreSubmissionArgs]) error {
	if w.Service == nil || job == nil {
		return errors.New("core submission runtime unavailable")
	}
	return w.Service.RecommendSubmission(ctx, job.Args.CustomerID, coreTriggerKey(job.Args))
}

func coreTriggerKey(args CoreSubmissionArgs) string {
	if args.TriggerKey != "" {
		return args.TriggerKey
	}
	return "questionnaire-" + strconv.FormatInt(args.SubmissionID, 10)
}
func (e CoreSubmissionEnqueuer) ReevaluateWithin(ctx context.Context, customer int64, key string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.Client, tx, CoreSubmissionArgs{CustomerID: customer, TriggerKey: key}, river.InsertOpts{Queue: AudienceRefreshQueue, MaxAttempts: 12})
	return err
}
