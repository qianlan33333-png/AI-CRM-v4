package query

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// LockedCanonicalLineage uses the same customer roots already owned/read by
// Identity merge commands. Share locks serialize business eligibility with
// their FOR UPDATE locks without rewriting any historical customer facts.
func (q PostgreSQL) LockedCanonicalLineage(ctx context.Context, id customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	before, err := q.CanonicalLineage(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(before))
	for i, v := range before {
		ids[i] = int64(v)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) ORDER BY id FOR SHARE`, ids)
	if err != nil {
		return nil, err
	}
	count := 0
	for rows.Next() {
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if count != len(before) || count == 0 {
		return nil, ErrInvalidQuery
	}
	after, err := q.CanonicalLineage(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(before) != len(after) {
		return nil, ErrInvalidQuery
	}
	for i, v := range before {
		if after[i] != v {
			return nil, ErrInvalidQuery
		}
	}
	return after, nil
}
