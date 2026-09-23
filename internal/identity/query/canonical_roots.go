package query

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const maximumCanonicalCustomerRootIDs = 500

// CanonicalCustomerRoots resolves current roots for a bounded batch without
// reading identity values. A malformed merge chain is unavailable rather than
// silently treated as an arbitrary terminal Customer.
func (PostgreSQL) CanonicalCustomerRoots(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerdomain.CustomerID, error) {
	if len(customerIDs) > maximumCanonicalCustomerRootIDs {
		return nil, ErrInvalidQuery
	}
	ids, err := uniqueCanonicalCustomerRootIDs(customerIDs)
	if err != nil {
		return nil, err
	}
	roots := make(map[customerdomain.CustomerID]customerdomain.CustomerID, len(ids))
	if len(ids) == 0 {
		return roots, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	return canonicalCustomerRoots(ctx, tx, ids)
}

func uniqueCanonicalCustomerRootIDs(customerIDs []customerdomain.CustomerID) ([]int64, error) {
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, ErrInvalidQuery
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	return ids, nil
}

// canonicalCustomerRoots is shared by narrowly scoped Identity projections.
// It accepts distinct, positive IDs and returns only a safe Customer-root map.
func canonicalCustomerRoots(ctx context.Context, tx pgx.Tx, ids []int64) (map[customerdomain.CustomerID]customerdomain.CustomerID, error) {
	rows, err := tx.Query(ctx, `WITH RECURSIVE input(source_customer_id) AS (
		SELECT DISTINCT source_customer_id FROM unnest($1::bigint[]) AS input(source_customer_id)
	), lineage(source_customer_id,customer_id,status,merged_into_customer_id,depth,visited,cycle) AS (
		SELECT input.source_customer_id,c.id,c.status,c.merged_into_customer_id,0,ARRAY[c.id],false
		FROM input JOIN customers c ON c.id=input.source_customer_id
		UNION ALL
		SELECT lineage.source_customer_id,c.id,c.status,c.merged_into_customer_id,lineage.depth+1,lineage.visited||c.id,c.id=ANY(lineage.visited)
		FROM lineage JOIN customers c ON c.id=lineage.merged_into_customer_id
		WHERE lineage.status='merged' AND lineage.merged_into_customer_id IS NOT NULL
			AND lineage.depth<127 AND NOT lineage.cycle
	), summary AS (
		SELECT input.source_customer_id,
			COALESCE((SELECT l.customer_id FROM lineage l WHERE l.source_customer_id=input.source_customer_id
				AND l.status<>'merged' AND l.merged_into_customer_id IS NULL AND NOT l.cycle
				ORDER BY l.depth DESC LIMIT 1),0) AS root_customer_id,
			CASE
				WHEN NOT EXISTS (SELECT 1 FROM lineage l WHERE l.source_customer_id=input.source_customer_id) THEN 'missing'
				WHEN EXISTS (SELECT 1 FROM lineage l WHERE l.source_customer_id=input.source_customer_id AND l.cycle) THEN 'cycle'
				WHEN EXISTS (SELECT 1 FROM lineage l WHERE l.source_customer_id=input.source_customer_id AND l.status='merged' AND l.merged_into_customer_id IS NULL) THEN 'broken_merged_pointer'
				WHEN EXISTS (SELECT 1 FROM lineage l WHERE l.source_customer_id=input.source_customer_id AND l.status<>'merged' AND l.merged_into_customer_id IS NOT NULL) THEN 'invalid_terminal_pointer'
				WHEN EXISTS (SELECT 1 FROM lineage l WHERE l.source_customer_id=input.source_customer_id AND l.status='merged' AND l.depth=127) THEN 'too_deep'
				WHEN NOT EXISTS (SELECT 1 FROM lineage l WHERE l.source_customer_id=input.source_customer_id AND l.status<>'merged' AND l.merged_into_customer_id IS NULL AND NOT l.cycle) THEN 'missing_terminal'
				ELSE ''
			END AS issue
		FROM input
	) SELECT source_customer_id,root_customer_id,issue FROM summary ORDER BY source_customer_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := make(map[customerdomain.CustomerID]customerdomain.CustomerID, len(ids))
	for rows.Next() {
		var source, root customerdomain.CustomerID
		var issue string
		if err = rows.Scan(&source, &root, &issue); err != nil {
			return nil, err
		}
		if issue == "missing" {
			return nil, ErrNotFound
		}
		if issue != "" || root < 1 {
			return nil, fmt.Errorf("canonical customer root %s: %w", issue, ErrInvalidQuery)
		}
		roots[source] = root
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(roots) != len(ids) {
		return nil, fmt.Errorf("canonical customer root missing result: %w", ErrInvalidQuery)
	}
	return roots, nil
}
