package store

import (
	"context"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (PostgreSQL) MachineContactStatuses(ctx context.Context, ids []int64) (map[int64]customerport.MachineContactStatus, error) {
	result := make(map[int64]customerport.MachineContactStatus, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,customer_status,updated_at FROM customer_directory_projection WHERE customer_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var state customerport.MachineContactStatus
		if err = rows.Scan(&id, &state.State, &state.ChangedAt); err != nil {
			return nil, err
		}
		result[id] = state
	}
	return result, rows.Err()
}

var _ customerport.MachineContactStatusReader = PostgreSQL{}
