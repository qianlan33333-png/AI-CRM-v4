package segment

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"github.com/riverqueue/river"
)

type AudienceMemberEventDispatchJobArgs struct {
	SnapshotID segmentport.SnapshotID `json:"snapshot_id" river:"unique"`
}

func (AudienceMemberEventDispatchJobArgs) Kind() string {
	return "segment.audience-member-entered-dispatch.v1"
}

type RiverMemberEventEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverMemberEventEnqueuer(client *river.Client[pgx.Tx]) (*RiverMemberEventEnqueuer, error) {
	if client == nil {
		return nil, segmentapp.ErrNotReady
	}
	return &RiverMemberEventEnqueuer{client: client}, nil
}

func (e *RiverMemberEventEnqueuer) EnqueueMemberEventsWithin(ctx context.Context, snapshotID segmentport.SnapshotID) (int64, error) {
	if e == nil || e.client == nil || snapshotID < 1 {
		return 0, segmentapp.ErrNotReady
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	result, err := platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, AudienceMemberEventDispatchJobArgs{SnapshotID: snapshotID}, river.InsertOpts{Queue: AudienceRefreshQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true}})
	if err != nil {
		return 0, err
	}
	return result.Job.ID, nil
}

type AudienceMemberEventSink interface {
	HandleAudienceMemberEntered(context.Context, segmentport.MemberEnteredV1) error
}

type AudienceMemberEventDispatchWorker struct {
	river.WorkerDefaults[AudienceMemberEventDispatchJobArgs]
	source segmentport.MemberEventReader
	sink   AudienceMemberEventSink
}

func NewAudienceMemberEventDispatchWorker() *AudienceMemberEventDispatchWorker {
	return &AudienceMemberEventDispatchWorker{}
}

func (*AudienceMemberEventDispatchWorker) Timeout(*river.Job[AudienceMemberEventDispatchJobArgs]) time.Duration {
	return 45 * time.Minute
}

func (w *AudienceMemberEventDispatchWorker) Bind(source segmentport.MemberEventReader, sink AudienceMemberEventSink) error {
	if w == nil || w.source != nil || w.sink != nil || source == nil || sink == nil {
		return segmentapp.ErrNotReady
	}
	w.source, w.sink = source, sink
	return nil
}

func (w *AudienceMemberEventDispatchWorker) Work(ctx context.Context, job *river.Job[AudienceMemberEventDispatchJobArgs]) error {
	if w == nil || w.source == nil || w.sink == nil || job == nil || job.JobRow == nil || job.Args.SnapshotID < 1 {
		return segmentapp.ErrNotReady
	}
	cursor := ""
	for {
		page, err := w.source.MemberEvents(ctx, job.Args.SnapshotID, cursor, 1000)
		if err != nil {
			return err
		}
		for _, event := range page.Items {
			if err = w.sink.HandleAudienceMemberEntered(ctx, event); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		if page.NextCursor == cursor {
			return errors.New("audience member event cursor did not advance")
		}
		cursor = page.NextCursor
	}
}

var _ segmentapp.MemberEventEnqueuer = (*RiverMemberEventEnqueuer)(nil)
