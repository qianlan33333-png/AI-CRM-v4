package wecom

import (
	"context"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func (PostgreSQLFollowRelationshipStore) AudienceContacts(ctx context.Context, reference time.Time) ([]wecomport.AudienceContact, error) {
	if reference.IsZero() {
		return nil, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH profiles AS (
		SELECT profile.customer_id,profile.corp_scope,profile.activation_status,
			profile.last_seen_run_id,profile.updated_at
		FROM wecom_external_contact_profiles profile
		WHERE profile.updated_at <= $1
	), completed_profiles AS (
		SELECT profile.*
		FROM profiles profile
		JOIN wecom_customer_sync_runs profile_run ON profile_run.id=profile.last_seen_run_id
			AND profile_run.corp_scope=profile.corp_scope
			AND profile_run.status='succeeded' AND profile_run.completed_at <= $1
		WHERE profile.activation_status='active'
	), latest_completed_runs AS (
		SELECT DISTINCT ON (run.corp_scope) run.id,run.corp_scope,
			COALESCE(run.started_at,run.created_at) AS started_at,run.completed_at
		FROM wecom_customer_sync_runs run
		WHERE run.status='succeeded' AND COALESCE(run.started_at,run.created_at) <= $1 AND run.completed_at <= $1
		ORDER BY run.corp_scope,COALESCE(run.started_at,run.created_at) DESC,run.id DESC
	), owner_values AS (
		SELECT observation.customer_id,observation.primary_owner_userid AS owner_userid
		FROM wecom_customer_owner_observations observation
		JOIN wecom_customer_sync_runs observation_run ON observation_run.id=observation.last_seen_run_id
			AND observation_run.corp_scope=observation.corp_scope
			AND observation_run.status='succeeded' AND observation_run.completed_at <= $1
		WHERE observation.relationship_status='active' AND observation.primary_owner_userid<>''
			AND observation.updated_at <= $1 AND observation.observed_at <= $1
	), ambiguous_owners AS (
		SELECT customer_id
		FROM owner_values
		GROUP BY customer_id
		HAVING count(DISTINCT owner_userid)>1
	), directory_contacts AS (
		SELECT observation.customer_id,observation.corp_scope,observation.last_seen_run_id AS run_id,
			observation.employee_id AS owner_userid,observation.observed_at
		FROM wecom_customer_owner_observations observation
		JOIN completed_profiles profile ON profile.customer_id=observation.customer_id
			AND profile.corp_scope=observation.corp_scope
		JOIN wecom_customer_sync_runs observation_run ON observation_run.id=observation.last_seen_run_id
			AND observation_run.corp_scope=observation.corp_scope
			AND observation_run.status='succeeded' AND observation_run.completed_at <= $1
		WHERE observation.relationship_status='active' AND observation.updated_at <= $1
			AND observation.observed_at <= $1
			AND NOT EXISTS (SELECT 1 FROM ambiguous_owners ambiguous WHERE ambiguous.customer_id=observation.customer_id)
	), candidates AS (
		SELECT customer_id,owner_userid,'active'::text AS status,observed_at FROM directory_contacts
		UNION ALL
		SELECT relationship.customer_id,relationship.employee_id,
			CASE WHEN NOT relationship.active
				OR EXISTS (SELECT 1 FROM ambiguous_owners ambiguous WHERE ambiguous.customer_id=relationship.customer_id)
				OR EXISTS (
					SELECT 1 FROM latest_completed_runs directory
					WHERE directory.corp_scope='wecom-corp:' || relationship.corp_id
						AND directory.started_at > relationship.last_event_at
						AND NOT EXISTS (
							SELECT 1 FROM directory_contacts contact
							WHERE contact.customer_id=relationship.customer_id
								AND contact.corp_scope=directory.corp_scope
								AND contact.run_id=directory.id
								AND contact.owner_userid=relationship.employee_id
						)
				)
			THEN 'deleted' ELSE 'active' END,
			relationship.last_event_at
		FROM wecom_follow_relationships relationship
		WHERE relationship.last_event_at IS NOT NULL AND relationship.updated_at <= $1
			AND relationship.last_event_at <= $1
		UNION ALL
		SELECT relationship.customer_id,relationship.employee_id,
			CASE WHEN relationship.active AND profile.activation_status='active'
				AND profile_run.corp_scope=profile.corp_scope
				AND profile_run.status='succeeded' AND profile_run.completed_at <= $1
				AND NOT EXISTS (SELECT 1 FROM ambiguous_owners ambiguous WHERE ambiguous.customer_id=relationship.customer_id)
			THEN 'active' ELSE 'deleted' END,
			COALESCE(relationship.last_event_at,relationship.created_at)
		FROM wecom_follow_relationships relationship
		JOIN profiles profile ON profile.customer_id=relationship.customer_id
			AND profile.corp_scope='wecom-corp:' || relationship.corp_id
		LEFT JOIN wecom_customer_sync_runs profile_run ON profile_run.id=profile.last_seen_run_id
		WHERE relationship.last_event_at IS NULL AND relationship.updated_at <= $1
			AND relationship.created_at <= $1
	), ranked AS (
		SELECT customer_id,owner_userid,status,observed_at,
			row_number() OVER (PARTITION BY customer_id,owner_userid
				ORDER BY observed_at DESC,CASE WHEN status='deleted' THEN 1 ELSE 0 END DESC) AS position
		FROM candidates
	)
	SELECT customer_id,owner_userid,status,observed_at
	FROM ranked WHERE position=1 ORDER BY customer_id,owner_userid`, reference.UTC())
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
