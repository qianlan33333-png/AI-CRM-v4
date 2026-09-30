package wecom

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func (PostgreSQLFollowRelationshipStore) AudienceContacts(ctx context.Context, reference time.Time) ([]wecomport.AudienceContact, error) {
	return readAudienceContacts(ctx, reference, nil)
}

func (PostgreSQLFollowRelationshipStore) AudienceContactsForCustomers(ctx context.Context, reference time.Time, customerIDs []customerdomain.CustomerID) ([]wecomport.AudienceContact, error) {
	if len(customerIDs) == 0 {
		return []wecomport.AudienceContact{}, nil
	}
	if len(customerIDs) > wecomport.MaxAudienceContactCustomerIDs {
		return nil, ErrInvalidFollowRelationship
	}
	seen := make(map[customerdomain.CustomerID]bool, len(customerIDs))
	ids := make([]int64, 0, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 || seen[id] {
			return nil, ErrInvalidFollowRelationship
		}
		seen[id] = true
		ids = append(ids, int64(id))
	}
	return readAudienceContacts(ctx, reference, ids)
}

func readAudienceContacts(ctx context.Context, reference time.Time, customerIDs []int64) ([]wecomport.AudienceContact, error) {
	if reference.IsZero() {
		return nil, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if err = lockDirectoryRead(ctx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.customer_id,o.employee_id,
 CASE WHEN o.relationship_status='active' AND p.activation_status='active' THEN 'active' ELSE 'deleted' END,o.observed_at,
 CASE WHEN o.relationship_status='active' THEN o.followed_at ELSE NULL END
 FROM wecom_customer_owner_observations o JOIN wecom_external_contact_profiles p ON p.customer_id=o.customer_id AND p.corp_scope=o.corp_scope
 JOIN wecom_customer_sync_runs r ON r.id=o.last_seen_run_id AND r.status='succeeded'
 JOIN wecom_customer_sync_runs pr ON pr.id=p.last_seen_run_id AND pr.status='succeeded'
 WHERE (o.followed_at IS NULL OR o.followed_at <= $1) AND NOT EXISTS (SELECT 1 FROM wecom_customer_owner_observations other WHERE other.customer_id=o.customer_id AND other.corp_scope<>o.corp_scope AND other.relationship_status='active') AND ($2::bigint[] IS NULL OR o.customer_id=ANY($2))
 ORDER BY o.customer_id,o.employee_id`, reference.UTC(), nullableCustomerIDs(customerIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wecomport.AudienceContact{}
	for rows.Next() {
		var item wecomport.AudienceContact
		if err = rows.Scan(&item.CustomerID, &item.OwnerUserID, &item.Status, &item.ObservedAt, &item.FollowedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ wecomport.AudienceContactReader = PostgreSQLFollowRelationshipStore{}
var _ wecomport.AudienceContactsForCustomersReader = PostgreSQLFollowRelationshipStore{}

func nullableCustomerIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	return ids
}
