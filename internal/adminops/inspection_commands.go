package adminops

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// OneID is not involved. This internal durable command accepts a River job
// and its permanent replay/audit receipt in the same PostgreSQL transaction.
// The receipt owns no retry/lease state; execution belongs to River.
var ErrInspectionRateLimited = errors.New("manual inspection hourly limit reached")

type inspectionManualEnqueuer struct {
	uow    platformport.UnitOfWork
	client *river.Client[pgx.Tx]
	now    func() time.Time
}

func NewInspectionManualEnqueuer(uow platformport.UnitOfWork, client *river.Client[pgx.Tx], now func() time.Time) (opsport.ManualInspectionEnqueuer, error) {
	if uow == nil || client == nil {
		return nil, ErrInspectionInvalid
	}
	if now == nil {
		now = time.Now
	}
	return &inspectionManualEnqueuer{uow: uow, client: client, now: now}, nil
}

func manualInspectionDigest(key string) (string, error) {
	if len(key) < 8 || len(key) > 160 || strings.TrimSpace(key) != key {
		return "", ErrInspectionInvalid
	}
	return string(effectport.Hash("ops-manual-scan-v1", key)), nil
}

func (s *inspectionManualEnqueuer) EnqueueManualInspection(ctx context.Context, actor int64, key string) (opsport.ManualInspectionAcceptance, error) {
	out := opsport.ManualInspectionAcceptance{}
	digest, err := manualInspectionDigest(key)
	if err != nil || actor < 1 {
		return out, ErrInspectionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err = s.uow.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(txctx, `SET LOCAL statement_timeout='2500ms';SET LOCAL lock_timeout='1000ms'`); err != nil {
			return err
		}
		if _, err = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ops-manual-accept:"+digest); err != nil {
			return err
		}
		var previousActor int64
		err = tx.QueryRow(txctx, `SELECT actor_id,job_id,accepted_at FROM adminops_inspection_commands WHERE request_digest=$1`, digest).Scan(&previousActor, &out.JobID, &out.AcceptedAt)
		if err == nil {
			if previousActor != actor {
				return ErrInspectionConflict
			}
			out.State, out.Replay = "accepted", true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Bound manual load in addition to the scheduled scan. Replays return
		// above and do not consume another hourly slot or create another job.
		if _, err = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended('ops-manual-hourly-budget',0))`); err != nil {
			return err
		}
		hour := s.now().UTC().Truncate(time.Hour)
		var total, byActor int
		if err = tx.QueryRow(txctx, `SELECT count(*),count(*) FILTER(WHERE actor_id=$1) FROM adminops_inspection_commands WHERE accepted_at >= $2 AND accepted_at < $3`, actor, hour, hour.Add(time.Hour)).Scan(&total, &byActor); err != nil {
			return err
		}
		if total >= 12 || byActor >= 6 {
			return ErrInspectionRateLimited
		}
		inserted, err := jobqueue.InsertTxWithOptions(txctx, s.client, tx, InspectionJobArgs{ManualRequestDigest: digest}, river.InsertOpts{Queue: InspectionQueue, MaxAttempts: 5})
		if err != nil {
			return err
		}
		if inserted == nil || inserted.Job == nil || inserted.Job.ID < 1 {
			return ErrInspectionInvalid
		}
		out = opsport.ManualInspectionAcceptance{State: "accepted", JobID: inserted.Job.ID, AcceptedAt: s.now().UTC()}
		_, err = tx.Exec(txctx, `INSERT INTO adminops_inspection_commands(request_digest,actor_id,job_id,accepted_at) VALUES($1,$2,$3,$4)`, digest, actor, out.JobID, out.AcceptedAt)
		return err
	})
	if err != nil {
		return opsport.ManualInspectionAcceptance{}, err
	}
	return out, nil
}

var manualInspectionDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// executeManualInspection trusts only an accepted local digest. Completing
// its permanent receipt is atomic with the observations and notification
// intents, so neither River nor observation retention can reopen old commands.
func (s *InspectionService) executeManualInspection(ctx context.Context, digest string) error {
	if !manualInspectionDigestPattern.MatchString(digest) {
		return ErrInspectionInvalid
	}
	var completed *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT completed_at FROM adminops_inspection_commands WHERE request_digest=$1`, digest).Scan(&completed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInspectionNotFound
		}
		return err
	}
	if completed != nil {
		return nil
	}
	_, err := s.scan(ctx, digest, s.now().UTC())
	return err
}

func completeManualInspectionWithin(ctx context.Context, tx pgx.Tx, digest string, runID int64, at time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE adminops_inspection_commands SET run_id=$2,completed_at=$3 WHERE request_digest=$1 AND completed_at IS NULL`, digest, runID, at)
	return err
}
