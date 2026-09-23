package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	"github.com/riverqueue/river"
)

const ReferralCampaignQueue = "referral-campaign"

// CampaignCloseJobArgs contains only a durable reference. The worker reloads
// the campaign deadline and score facts; a job carries no customer identities.
type CampaignCloseJobArgs struct {
	CampaignID int64 `json:"campaign_id"`
	// Expected end participates in uniqueness: changing a scheduled campaign's
	// deadline creates a new job without suppressing it behind the old deadline.
	ExpectedEnd time.Time `json:"expected_end"`
}

func (CampaignCloseJobArgs) Kind() string { return "referral.campaign-close.v1" }

type RiverCampaignCloseEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverCampaignCloseEnqueuer(client *river.Client[pgx.Tx]) (*RiverCampaignCloseEnqueuer, error) {
	if client == nil {
		return nil, referralport.ErrUnavailable
	}
	return &RiverCampaignCloseEnqueuer{client: client}, nil
}
func (e *RiverCampaignCloseEnqueuer) EnqueueCampaignCloseWithin(ctx context.Context, campaignID int64, at time.Time) error {
	if e == nil || e.client == nil || campaignID < 1 || at.IsZero() {
		return referralport.ErrUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, CampaignCloseJobArgs{CampaignID: campaignID, ExpectedEnd: at.UTC()}, river.InsertOpts{Queue: ReferralCampaignQueue, MaxAttempts: 24, ScheduledAt: at.UTC(), UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

type CampaignCloseApplication interface {
	RunCampaignClose(context.Context, int64) error
}
type CampaignCloseWorker struct {
	river.WorkerDefaults[CampaignCloseJobArgs]
	service CampaignCloseApplication
}

func NewCampaignCloseWorker() *CampaignCloseWorker { return &CampaignCloseWorker{} }
func (*CampaignCloseWorker) Timeout(*river.Job[CampaignCloseJobArgs]) time.Duration {
	return 60 * time.Second
}
func (w *CampaignCloseWorker) BindService(service CampaignCloseApplication) error {
	if w == nil || w.service != nil || service == nil {
		return referralport.ErrUnavailable
	}
	w.service = service
	return nil
}
func (w *CampaignCloseWorker) Work(ctx context.Context, job *river.Job[CampaignCloseJobArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || job.Args.CampaignID < 1 || job.Args.ExpectedEnd.IsZero() {
		return referralport.ErrUnavailable
	}
	return w.service.RunCampaignClose(ctx, job.Args.CampaignID)
}

var _ CampaignCloseEnqueuer = (*RiverCampaignCloseEnqueuer)(nil)
