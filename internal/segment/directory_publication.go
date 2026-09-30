package segment

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
)

type DirectoryPublicationArgs struct {
	Revision int64 `json:"revision"`
}

func (DirectoryPublicationArgs) Kind() string { return "segment.directory-publication-refresh.v1" }

type DirectoryPublicationEnqueuer struct{ Client *river.Client[pgx.Tx] }

func (e DirectoryPublicationEnqueuer) DirectoryPublishedWithin(ctx context.Context, p wecomport.DirectoryPublication) error {
	if !p.BaselineInitialized || !p.Complete {
		return nil
	}
	t, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.Client, t, DirectoryPublicationArgs{Revision: p.Revision}, river.InsertOpts{Queue: AudienceRefreshQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

type DirectoryPublicationWorker struct {
	river.WorkerDefaults[DirectoryPublicationArgs]
	UOW   platformport.UnitOfWork
	Store interface {
		DirectoryRefreshConfigurations(context.Context, int) ([]segmentdomain.ScheduledConfiguration, error)
	}
	Refresh segmentapp.ScheduledRefreshAccepter
}

func (w *DirectoryPublicationWorker) Work(ctx context.Context, job *river.Job[DirectoryPublicationArgs]) error {
	if w.UOW == nil || w.Store == nil || w.Refresh == nil || job == nil || job.Args.Revision < 1 {
		return segmentapp.ErrNotReady
	}
	return w.UOW.Within(ctx, func(tx context.Context) error {
		configs, err := w.Store.DirectoryRefreshConfigurations(tx, 10000)
		if err != nil {
			return err
		}
		if len(configs) == 10000 {
			return segmentapp.ErrUnavailable
		}
		seen := map[int64]bool{}
		for _, c := range configs {
			if seen[c.PackageID] {
				continue
			}
			seen[c.PackageID] = true
			actor := segmentport.MutationActor{Kind: segmentport.MutationActorKind(c.ActorKind), StaffID: c.Actor, Reference: c.ActorReference}
			if actor.Kind == "" {
				actor.Kind = segmentport.MutationActorAdmin
			}
			_, err = w.Refresh.AcceptRefreshWithin(tx, segmentapp.RefreshCommand{PackageID: c.PackageID, Actor: c.Actor, MutationActor: actor, IdempotencyKey: fmt.Sprintf("wecom-directory-%d-%d", job.Args.Revision, c.PackageID), RefreshKind: segmentdomain.RefreshDaily})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
