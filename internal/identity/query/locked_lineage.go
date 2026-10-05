package query

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"sort"
)

// LockedCanonicalLineage uses the same customer roots already owned/read by
// Identity merge commands. Share locks serialize business eligibility with
// their FOR UPDATE locks without rewriting any historical customer facts.
func (q PostgreSQL) LockedCanonicalLineage(ctx context.Context, id customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return q.lockedCanonicalLineage(ctx, id, "FOR SHARE")
}

func (q PostgreSQL) WriteLockedCanonicalLineage(ctx context.Context, id customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return q.lockedCanonicalLineage(ctx, id, "FOR UPDATE")
}

func (q PostgreSQL) lockedCanonicalLineage(ctx context.Context, id customerdomain.CustomerID, lock string) ([]customerdomain.CustomerID, error) {
	lineages, err := q.lockedCanonicalLineages(ctx, []customerdomain.CustomerID{id}, lock)
	return lineages[id], err
}

func (q PostgreSQL) WriteLockedCanonicalLineages(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID][]customerdomain.CustomerID, error) {
	return q.lockedCanonicalLineages(ctx, ids, "FOR UPDATE")
}

func (q PostgreSQL) lockedCanonicalLineages(ctx context.Context, requested []customerdomain.CustomerID, lock string) (map[customerdomain.CustomerID][]customerdomain.CustomerID, error) {
	before := make(map[customerdomain.CustomerID][]customerdomain.CustomerID, len(requested))
	all := make(map[customerdomain.CustomerID]struct{})
	for _, id := range requested {
		if _, found := before[id]; found {
			continue
		}
		lineage, err := q.CanonicalLineage(ctx, id)
		if err != nil {
			return nil, err
		}
		before[id] = lineage
		for _, member := range lineage {
			all[member] = struct{}{}
		}
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(all))
	for id := range all {
		ids = append(ids, int64(id))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	// The clause comes only from the two internal entry points above.
	rows, err := tx.Query(ctx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) ORDER BY id `+lock, ids)
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
	if count != len(ids) || count == 0 {
		return nil, ErrInvalidQuery
	}
	for id, lineage := range before {
		after, err := q.CanonicalLineage(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(lineage) != len(after) {
			return nil, ErrInvalidQuery
		}
		for i, v := range lineage {
			if after[i] != v {
				return nil, ErrInvalidQuery
			}
		}
		before[id] = after
	}
	return before, nil
}
