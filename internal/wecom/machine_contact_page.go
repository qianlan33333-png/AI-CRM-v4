package wecom

import (
	"context"
	"errors"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// MachineContactRows fails while a directory run is incomplete. A caller may
// then retry the same watermark rather than treating a partial import as empty.
func (PostgreSQLCustomerSyncStore) MachineContactRows(ctx context.Context, corpScope string) ([]wecomport.MachineContactRow, []wecomport.MachineUnresolvedRow, error) {
	if len(corpScope) < 12 || corpScope[:11] != "wecom-corp:" {
		return nil, nil, errors.New("invalid corp scope")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM wecom_customer_sync_runs WHERE corp_scope=$1 ORDER BY id DESC LIMIT 1`, corpScope).Scan(&status); err != nil || status != "succeeded" {
		return nil, nil, errors.New("completed contact directory unavailable")
	}
	rows, err := tx.Query(ctx, `SELECT p.customer_id,
		CASE WHEN owners.n=1 THEN owners.owner_userid ELSE '' END,
		CASE WHEN owners.n=1 THEN 'known' WHEN owners.n>1 THEN 'ambiguous' ELSE 'unknown' END,
		COALESCE(tags.names,ARRAY[]::text[]),
		CASE WHEN p.activation_status='stale' THEN 'unbound' WHEN COALESCE(rel.active_count,0)>0 THEN 'bound' WHEN COALESCE(rel.total_count,0)>0 THEN 'unbound' ELSE 'unknown' END,
		CASE WHEN p.activation_status='conflict' THEN 'unresolved' ELSE 'resolved' END,
		GREATEST(p.updated_at,last_seen.completed_at,COALESCE(owners.changed_at,p.updated_at),COALESCE(tags.changed_at,p.updated_at),COALESCE(rel.changed_at,p.updated_at)),
		CASE WHEN p.activation_status='stale' THEN p.stale_at WHEN COALESCE(rel.total_count,0)>0 AND COALESCE(rel.active_count,0)=0 THEN rel.changed_at ELSE NULL END
		FROM wecom_external_contact_profiles p
		JOIN wecom_customer_sync_runs last_seen ON last_seen.id=p.last_seen_run_id AND last_seen.status='succeeded'
		LEFT JOIN LATERAL (
			SELECT count(DISTINCT owner_userid) n,min(owner_userid) owner_userid,max(changed_at) changed_at FROM (
				SELECT p.primary_owner_userid owner_userid,p.updated_at changed_at FROM wecom_customer_sync_runs r WHERE r.id=p.primary_owner_run_id AND r.status='succeeded' AND p.primary_owner_userid<>''
				UNION ALL SELECT o.primary_owner_userid,o.updated_at FROM wecom_customer_owner_observations o JOIN wecom_customer_sync_runs r ON r.id=o.last_seen_run_id AND r.status='succeeded'
				WHERE o.customer_id=p.customer_id AND o.corp_scope=p.corp_scope AND o.relationship_status='active' AND o.primary_owner_userid<>''
			) current_owners
		) owners ON true
		LEFT JOIN LATERAL (
			SELECT array_agg(DISTINCT t.observed_name) FILTER (WHERE t.observation_status='active' AND t.observed_name<>'') names,max(t.updated_at) changed_at
			FROM wecom_customer_tag_observations t JOIN wecom_customer_sync_runs r ON r.id=t.last_seen_run_id AND r.status='succeeded'
			WHERE t.customer_id=p.customer_id AND t.corp_scope=p.corp_scope
		) tags ON true
		LEFT JOIN LATERAL (
			SELECT count(*) total_count,count(*) FILTER (WHERE active) active_count,max(updated_at) changed_at
			FROM wecom_follow_relationships WHERE customer_id=p.customer_id AND corp_id=$2
		) rel ON true
		WHERE p.corp_scope=$1 ORDER BY p.customer_id`, corpScope, corpScope[11:])
	if err != nil {
		return nil, nil, err
	}
	contacts := make([]wecomport.MachineContactRow, 0)
	for rows.Next() {
		var row wecomport.MachineContactRow
		if err = rows.Scan(&row.CustomerID, &row.OwnerUserID, &row.OwnerStatus, &row.Tags, &row.BindingStatus, &row.IdentityStatus, &row.ChangedAt, &row.RemovedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		contacts = append(contacts, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.Query(ctx, `WITH previous AS (
		SELECT DISTINCT external_userid_digest FROM wecom_customer_sync_items WHERE corp_scope=$1 AND outcome IN ('conflict','terminal_failed')
	)
	SELECT DISTINCT ON (item.external_userid_digest) item.external_userid_digest,item.outcome,GREATEST(item.created_at,run.completed_at)
	FROM previous JOIN wecom_customer_sync_items item ON item.external_userid_digest=previous.external_userid_digest AND item.corp_scope=$1
	JOIN wecom_customer_sync_runs run ON run.id=item.run_id AND run.status='succeeded'
	ORDER BY item.external_userid_digest,item.id DESC`, corpScope)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	unresolved := make([]wecomport.MachineUnresolvedRow, 0)
	for rows.Next() {
		var row wecomport.MachineUnresolvedRow
		if err = rows.Scan(&row.SourceDigest, &row.Outcome, &row.ChangedAt); err != nil {
			return nil, nil, err
		}
		unresolved = append(unresolved, row)
	}
	return contacts, unresolved, rows.Err()
}

var _ wecomport.MachineContactPageReader = PostgreSQLCustomerSyncStore{}
