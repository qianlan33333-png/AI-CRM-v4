package wecom

import (
	"context"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"time"
)

func (PostgreSQLFollowRelationshipStore) AudienceContacts(ctx context.Context, reference time.Time) ([]wecomport.AudienceContact, error) {
	if reference.IsZero() {
		return nil, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT r.customer_id,r.employee_id,CASE WHEN r.active AND p.activation_status='active' THEN 'active' ELSE 'deleted' END,r.created_at
		FROM wecom_follow_relationships r JOIN wecom_external_contact_profiles p ON p.customer_id=r.customer_id
		WHERE r.updated_at <= $1 AND p.updated_at <= $1 ORDER BY r.customer_id,r.employee_id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wecomport.AudienceContact{}
	for rows.Next() {
		var item wecomport.AudienceContact
		if err = rows.Scan(&item.CustomerID, &item.OwnerUserID, &item.Status, &item.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ wecomport.AudienceContactReader = PostgreSQLFollowRelationshipStore{}
