package store

import "context"

func (r *Repository) ListCollectedPhoneOrdersWithin(ctx context.Context, after, through int64) ([]int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT order_id FROM order_contact_snapshots WHERE order_id>$1 AND ($2::bigint=0 OR order_id<=$2) ORDER BY order_id LIMIT 100`, after, through)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	return ids, mapError(rows.Err())
}
