package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	maximumAdminRadarVisitorIDs        = 500
	maximumAdminRadarVisitorCandidates = 100001
)

// AdminRadarVisitorIdentities returns a current canonical Customer root for
// each historic Radar customer reference, along with the only external
// identity this use case may reveal. The batch lineage helper deliberately
// applies the same missing, broken-pointer, cycle, and depth rules as
// CanonicalLineage; ambiguous lineage is an unavailable read, never a reason
// to return a partly guessed admin projection.
func (PostgreSQL) AdminRadarVisitorIdentities(ctx context.Context, corpScope string, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]identityport.AdminRadarVisitorIdentity, error) {
	if !validAdminRadarVisitorCorpScope(corpScope) || len(customerIDs) > maximumAdminRadarVisitorIDs {
		return nil, ErrInvalidQuery
	}
	ids, err := uniqueAdminRadarVisitorCustomerIDs(customerIDs, maximumAdminRadarVisitorIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[customerdomain.CustomerID]identityport.AdminRadarVisitorIdentity, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	roots, err := adminRadarVisitorCanonicalRoots(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	rootIDs := uniqueAdminRadarVisitorRootIDs(roots)
	rows, err := tx.Query(ctx, `SELECT customer_id,count(id),COALESCE(min(normalized_value),'')
		FROM customer_identities
		WHERE customer_id=ANY($1::bigint[]) AND kind='wecom_external_userid' AND scope_key=$2
			AND assurance='verified' AND status='active'
		GROUP BY customer_id`, rootIDs, corpScope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	externals := make(map[customerdomain.CustomerID]struct {
		count int64
		value string
	}, len(rootIDs))
	for rows.Next() {
		var root customerdomain.CustomerID
		var row struct {
			count int64
			value string
		}
		if err = rows.Scan(&root, &row.count, &row.value); err != nil {
			return nil, err
		}
		externals[root] = row
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for source, root := range roots {
		projection := identityport.AdminRadarVisitorIdentity{CanonicalCustomerID: root, ExternalContactStatus: identityport.AdminRadarVisitorExternalContactMissing}
		if external, exists := externals[root]; exists {
			switch external.count {
			case 1:
				projection.ExternalContactID = external.value
				projection.ExternalContactStatus = identityport.AdminRadarVisitorExternalContactAvailable
			default:
				projection.ExternalContactStatus = identityport.AdminRadarVisitorExternalContactAmbiguous
			}
		}
		result[source] = projection
	}
	return result, nil
}

// SearchAdminRadarVisitorCustomers expands Customer-directory candidates and
// one exact, scoped external-contact candidate to every current lineage member
// before Radar filters its own historical session table. The external input is
// deliberately exact; this method is not a general identity search API.
func (PostgreSQL) SearchAdminRadarVisitorCustomers(ctx context.Context, corpScope, externalContactID string, directoryCustomerIDs []customerdomain.CustomerID, limit int) ([]customerdomain.CustomerID, error) {
	if !validAdminRadarVisitorCorpScope(corpScope) || strings.TrimSpace(externalContactID) != externalContactID || len([]rune(externalContactID)) > 200 || limit < 1 || limit > maximumAdminRadarVisitorCandidates {
		return nil, ErrInvalidQuery
	}
	ids, err := uniqueAdminRadarVisitorCustomerIDs(directoryCustomerIDs, maximumAdminRadarVisitorCandidates)
	if err != nil {
		return nil, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	externalRows, err := tx.Query(ctx, `SELECT customer_id FROM customer_identities
		WHERE $1::text<>'' AND kind='wecom_external_userid' AND scope_key=$2
			AND normalized_value=$1 AND assurance='verified' AND status='active'`, externalContactID, corpScope)
	if err != nil {
		return nil, err
	}
	for externalRows.Next() {
		var id int64
		if err = externalRows.Scan(&id); err != nil {
			externalRows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = externalRows.Err(); err != nil {
		externalRows.Close()
		return nil, err
	}
	externalRows.Close()
	ids, err = uniqueAdminRadarVisitorInt64s(ids, maximumAdminRadarVisitorCandidates+1)
	if err != nil {
		return nil, err
	}
	ids, err = adminRadarVisitorExistingCustomerIDs(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []customerdomain.CustomerID{}, nil
	}
	roots, err := adminRadarVisitorCanonicalRoots(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	return adminRadarVisitorLineageMembers(ctx, tx, uniqueAdminRadarVisitorRootIDs(roots), limit)
}

// adminRadarVisitorCanonicalRoots is the batch counterpart to
// CanonicalLineage. It does not silently omit malformed Customer roots: every
// submitted source needs exactly one terminal non-merged customer, reached in
// at most 127 pointers and without a cycle or invalid terminal pointer.
func adminRadarVisitorCanonicalRoots(ctx context.Context, tx pgx.Tx, ids []int64) (map[customerdomain.CustomerID]customerdomain.CustomerID, error) {
	return canonicalCustomerRoots(ctx, tx, ids)
}

// adminRadarVisitorExistingCustomerIDs makes an exact CID search for a
// deleted or nonexistent Customer a normal empty search while preserving a
// fail-closed error for any existing malformed lineage.
func adminRadarVisitorExistingCustomerIDs(ctx context.Context, tx pgx.Tx, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]int64, 0, len(ids))
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func adminRadarVisitorLineageMembers(ctx context.Context, tx pgx.Tx, roots []int64, limit int) ([]customerdomain.CustomerID, error) {
	rows, err := tx.Query(ctx, `WITH RECURSIVE root(customer_id) AS (
		SELECT DISTINCT customer_id FROM unnest($1::bigint[]) AS root(customer_id)
	), descendants(customer_id,visited,cycle) AS (
		SELECT customer_id,ARRAY[customer_id],false FROM root
		UNION ALL
		SELECT c.id,descendants.visited||c.id,c.id=ANY(descendants.visited)
		FROM descendants JOIN customers c ON c.merged_into_customer_id=descendants.customer_id
		WHERE c.status='merged' AND NOT descendants.cycle
	), invalid AS (
		SELECT EXISTS(SELECT 1 FROM descendants WHERE cycle) AS bad
	) SELECT descendants.customer_id,invalid.bad FROM descendants CROSS JOIN invalid
	WHERE NOT descendants.cycle ORDER BY descendants.customer_id LIMIT $2`, roots, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]customerdomain.CustomerID, 0)
	for rows.Next() {
		var id customerdomain.CustomerID
		var invalid bool
		if err = rows.Scan(&id, &invalid); err != nil {
			return nil, err
		}
		if invalid {
			return nil, fmt.Errorf("admin radar visitor descendant lineage invalid: %w", ErrInvalidQuery)
		}
		result = append(result, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func validAdminRadarVisitorCorpScope(value string) bool {
	return strings.HasPrefix(value, "wecom-corp:") && len(value) > len("wecom-corp:") && strings.TrimSpace(value) == value
}

func uniqueAdminRadarVisitorCustomerIDs(values []customerdomain.CustomerID, maximum int) ([]int64, error) {
	ints := make([]int64, 0, len(values))
	for _, value := range values {
		ints = append(ints, int64(value))
	}
	return uniqueAdminRadarVisitorInt64s(ints, maximum)
}

func uniqueAdminRadarVisitorInt64s(values []int64, maximum int) ([]int64, error) {
	if len(values) > maximum {
		return nil, ErrInvalidQuery
	}
	result := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value < 1 {
			return nil, ErrInvalidQuery
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func uniqueAdminRadarVisitorRootIDs(roots map[customerdomain.CustomerID]customerdomain.CustomerID) []int64 {
	values := make([]int64, 0, len(roots))
	seen := make(map[customerdomain.CustomerID]struct{}, len(roots))
	for _, root := range roots {
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		values = append(values, int64(root))
	}
	return values
}
