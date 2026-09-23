package wecom

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"strings"
	"time"
)

func (PostgreSQLFollowRelationshipStore) AudienceRecognizedContacts(ctx context.Context, scope string, at time.Time) ([]customerdomain.CustomerID, error) {
	if at.IsZero() || !strings.HasPrefix(scope, "wecom-corp:") || len(scope) <= len("wecom-corp:") {
		return nil, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id FROM wecom_external_contact_profiles WHERE corp_scope=$1 AND activation_status IN ('active','stale') AND updated_at <= $2 ORDER BY customer_id`, scope, at.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []customerdomain.CustomerID{}
	for rows.Next() {
		var id customerdomain.CustomerID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

var _ wecomport.AudienceRecognizedContactReader = PostgreSQLFollowRelationshipStore{}
