package wecom

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQLStaffDirectoryRefreshStore struct{}

func NewPostgreSQLStaffDirectoryRefreshStore() *PostgreSQLStaffDirectoryRefreshStore {
	return &PostgreSQLStaffDirectoryRefreshStore{}
}

func (*PostgreSQLStaffDirectoryRefreshStore) Begin(ctx context.Context, runKey, trigger string, now time.Time) (StaffDirectoryRefreshRun, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return StaffDirectoryRefreshRun{}, false, err
	}
	var run StaffDirectoryRefreshRun
	err = tx.QueryRow(ctx, `INSERT INTO wecom_staff_directory_refresh_runs(run_key,trigger,state,started_at,updated_at)
		VALUES($1,$2,'running',$3,$3) ON CONFLICT(run_key) DO NOTHING
		RETURNING id,run_key,trigger,state,attempt_count`, runKey, trigger, now).Scan(&run.ID, &run.RunKey, &run.Trigger, &run.State, &run.Attempts)
	if err == nil {
		return run, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StaffDirectoryRefreshRun{}, false, err
	}
	err = tx.QueryRow(ctx, `SELECT id,run_key,trigger,state,attempt_count FROM wecom_staff_directory_refresh_runs WHERE run_key=$1 FOR UPDATE`, runKey).Scan(&run.ID, &run.RunKey, &run.Trigger, &run.State, &run.Attempts)
	if err != nil {
		return StaffDirectoryRefreshRun{}, false, err
	}
	if run.Trigger != trigger {
		return StaffDirectoryRefreshRun{}, false, ErrStaffDirectoryRefreshNotReady
	}
	if run.State == "succeeded" {
		return run, true, nil
	}
	err = tx.QueryRow(ctx, `UPDATE wecom_staff_directory_refresh_runs SET state='running',attempt_count=attempt_count+1,
		last_error_code='',started_at=$2,completed_at=NULL,updated_at=$2 WHERE id=$1
		RETURNING state,attempt_count`, run.ID, now).Scan(&run.State, &run.Attempts)
	return run, false, err
}

func (*PostgreSQLStaffDirectoryRefreshStore) Succeed(ctx context.Context, runID int64, result accessport.WeComStaffProjectionResult, digest [32]byte, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE wecom_staff_directory_refresh_runs SET state='succeeded',discovered_count=$2,
		created_count=$3,existing_count=$4,inactive_count=$5,directory_digest=$6,last_error_code='',completed_at=$7,updated_at=$7
		WHERE id=$1 AND state='running'`, runID, result.Discovered, result.Created, result.Existing, result.Inactive, digest[:], now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaffDirectoryRefreshNotReady
	}
	return nil
}

func (*PostgreSQLStaffDirectoryRefreshStore) Fail(ctx context.Context, runID int64, code string, terminal bool, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	state := "failed_retryable"
	if terminal {
		state = "failed_terminal"
	}
	tag, err := tx.Exec(ctx, `UPDATE wecom_staff_directory_refresh_runs SET state=$2,last_error_code=$3,
		completed_at=$4,updated_at=$4 WHERE id=$1 AND state='running'`, runID, state, code, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaffDirectoryRefreshNotReady
	}
	return nil
}

var _ StaffDirectoryRefreshStore = (*PostgreSQLStaffDirectoryRefreshStore)(nil)
