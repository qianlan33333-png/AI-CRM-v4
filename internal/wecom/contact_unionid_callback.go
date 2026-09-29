package wecom

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
)

// The durable job stores an internal inbox ID, never an external identifier.
type ContactUnionIDCallbackJobArgs struct {
	InboxID int64 `json:"inbox_id"`
}

func (ContactUnionIDCallbackJobArgs) Kind() string { return "wecom.contact-unionid-observation.v1" }

type ContactUnionIDCallbackJobEnqueuer interface {
	EnqueueContactUnionIDObservation(context.Context, int64) error
}

type RiverContactUnionIDCallbackEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverContactUnionIDCallbackEnqueuer(client *river.Client[pgx.Tx]) (*RiverContactUnionIDCallbackEnqueuer, error) {
	if client == nil {
		return nil, ErrSyncNotReady
	}
	return &RiverContactUnionIDCallbackEnqueuer{client: client}, nil
}

func (e *RiverContactUnionIDCallbackEnqueuer) EnqueueContactUnionIDObservation(ctx context.Context, inboxID int64) error {
	if e == nil || e.client == nil || inboxID < 1 {
		return ErrSyncNotReady
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, ContactUnionIDCallbackJobArgs{InboxID: inboxID}, river.InsertOpts{
		Queue: CustomerSyncQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	return err
}

type ContactUnionIDCallbackService struct {
	Enabled       bool
	CorpID        string
	Inbox         *webhook.Service
	Provider      wecomport.ExternalContactReader
	Resolver      identityport.Resolver
	Relationships FollowRelationshipStore
	UnionIDs      ContactUnionIDLinker
	UOW           platformport.UnitOfWork
}

func (s ContactUnionIDCallbackService) Ready() bool {
	return s.Enabled && s.CorpID != "" && s.Inbox != nil && s.Provider != nil && s.Resolver != nil && s.Relationships != nil && s.UnionIDs.Ready() && s.UOW != nil
}

func (s ContactUnionIDCallbackService) Process(ctx context.Context, inboxID int64) error {
	if !s.Ready() || inboxID < 1 {
		return ErrSyncNotReady
	}
	// The existing callback target gate validates the processed inbox, active
	// follow relationship and canonical OneID root before and after the read.
	targets := contactCallbackTargetLoader{CorpID: s.CorpID, Inbox: s.Inbox, Identity: s.Resolver, Relationships: s.Relationships, UOW: s.UOW}
	target, skip, err := targets.loadTarget(ctx, inboxID)
	if err != nil || skip {
		return err
	}
	contact, err := s.Provider.ReadExternalContact(ctx, target.event.ExternalUserID)
	if err != nil {
		return err
	}
	return s.UOW.Within(ctx, func(txContext context.Context) error {
		current, stale, loadErr := targets.loadTargetWithin(txContext, inboxID)
		if loadErr != nil || stale {
			return loadErr
		}
		if current.customerID != target.customerID || current.event.ExternalUserID != target.event.ExternalUserID {
			return nil
		}
		_, linkErr := s.UnionIDs.Link(txContext, current.customerID, current.event.ExternalUserID, contact, "wecom.callback_detail", inboxID)
		return linkErr
	})
}

type ContactUnionIDCallbackWorker struct {
	river.WorkerDefaults[ContactUnionIDCallbackJobArgs]
	service *ContactUnionIDCallbackService
}

func NewContactUnionIDCallbackWorker() *ContactUnionIDCallbackWorker {
	return &ContactUnionIDCallbackWorker{}
}

func (*ContactUnionIDCallbackWorker) Timeout(*river.Job[ContactUnionIDCallbackJobArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *ContactUnionIDCallbackWorker) BindService(service ContactUnionIDCallbackService) error {
	if w == nil || w.service != nil || !service.Ready() {
		return ErrSyncNotReady
	}
	w.service = &service
	return nil
}

func (w *ContactUnionIDCallbackWorker) Work(ctx context.Context, job *river.Job[ContactUnionIDCallbackJobArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || job.Args.InboxID < 1 {
		return ErrSyncNotReady
	}
	return w.service.Process(ctx, job.Args.InboxID)
}
