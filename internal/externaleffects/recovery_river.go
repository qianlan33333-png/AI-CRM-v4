package externaleffects

import (
	"context"
	"time"

	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
)

type RecoveryJobArgs struct{}

func (RecoveryJobArgs) Kind() string { return "external_effect.recover-discarded.v1" }

type RecoveryWorker struct {
	river.WorkerDefaults[RecoveryJobArgs]
	repository *Repository
}

func NewRecoveryWorker(repository *Repository) *RecoveryWorker {
	return &RecoveryWorker{repository: repository}
}

func (w *RecoveryWorker) Work(ctx context.Context, job *river.Job[RecoveryJobArgs]) error {
	if w == nil || w.repository == nil || job == nil || job.JobRow == nil {
		return ErrInvalid
	}
	return w.repository.RecoverDiscardedAttempts(ctx)
}

func RecoveryPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return RecoveryJobArgs{}, &river.InsertOpts{Queue: platformjobqueue.OutboundQueue, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Minute}}
	}, &river.PeriodicJobOpts{ID: "external-effect-recover-discarded-v1", RunOnStart: true})
}

// RecoverDiscardedAttempts only wakes an expired original attempt; RunAttempt
// owns its unknown transition and owner projection without another adapter
// call. Financial effects remain outside this bounded recovery contract.
func (r *Repository) RecoverDiscardedAttempts(ctx context.Context) error {
	if r == nil || r.pool == nil || r.river == nil {
		return ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT effect.id,effect.generation,job.river_job_id,job.queue
        FROM external_effects effect JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation
        JOIN external_effect_attempts attempt ON attempt.effect_id=effect.id AND attempt.generation=effect.generation
          AND attempt.number=effect.attempt_count AND attempt.fence=effect.lease_fence AND attempt.state='attempted'
        WHERE effect.state='attempted' AND effect.owner<>$1
          AND (effect.lease_expires_at IS NULL OR effect.lease_expires_at<=clock_timestamp())
        ORDER BY effect.id FOR UPDATE OF effect SKIP LOCKED`, OwnerPayment)
	if err != nil {
		return err
	}
	type originalJob struct {
		effectID, generation, jobID int64
		queue                       string
	}
	var jobs []originalJob
	for rows.Next() {
		var job originalJob
		if err = rows.Scan(&job.effectID, &job.generation, &job.jobID, &job.queue); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if _, err = platformjobqueue.RetryRescuedDiscardedJobTx(ctx, r.river, tx, job.jobID, EffectJobArgs{EffectID: job.effectID, Generation: job.generation}, job.queue); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

var _ river.Worker[RecoveryJobArgs] = (*RecoveryWorker)(nil)
