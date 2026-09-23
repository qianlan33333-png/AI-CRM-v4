package adminops

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
)

// ManualInspectionCommand reads only this Owner's permanent receipt. It never
// infers command completion from a newer scheduled run or a River job state.
func (s *InspectionService) ManualInspectionCommand(ctx context.Context, actorID, jobID int64) (opsport.ManualInspectionCommand, error) {
	out := opsport.ManualInspectionCommand{}
	if actorID < 1 || jobID < 1 {
		return out, ErrInspectionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='2500ms';SET LOCAL lock_timeout='500ms'`); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT job_id,accepted_at,run_id,completed_at FROM adminops_inspection_commands WHERE job_id=$1 AND actor_id=$2`, jobID, actorID).Scan(&out.JobID, &out.AcceptedAt, &out.RunID, &out.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return opsport.ManualInspectionCommand{}, ErrInspectionNotFound
	}
	if err != nil {
		return opsport.ManualInspectionCommand{}, err
	}
	out.State = "accepted"
	if out.CompletedAt != nil {
		out.State = "completed"
	}
	if err = tx.Commit(ctx); err != nil {
		return opsport.ManualInspectionCommand{}, err
	}
	return out, nil
}
