package wecom

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CheckGroupMembershipReadiness verifies the WeCom-owned Provider-read
// projection used only when channel Provider reads are enabled. It does not
// perform a Provider read, create a job, or inspect another domain's tables.
func CheckGroupMembershipReadiness(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("WeCom group membership schema is not ready: database is unavailable")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT
	NOT EXISTS (SELECT 1 FROM unnest(ARRAY[
		'wecom_group_membership_facts',
		'wecom_group_membership_observations',
		'wecom_group_provider_facts',
		'wecom_group_provider_observations'
	]) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)
	AND NOT EXISTS (
		SELECT 1 FROM (VALUES
			('wecom_group_membership_facts'::text, 'corp_scope'::text),
			('wecom_group_membership_facts'::text, 'chat_reference'::text),
			('wecom_group_membership_facts'::text, 'observed_at'::text),
			('wecom_group_membership_facts'::text, 'complete'::text),
			('wecom_group_membership_facts'::text, 'external_count'::text),
			('wecom_group_membership_facts'::text, 'unresolved_count'::text),
			('wecom_group_membership_facts'::text, 'customer_ids'::text),
			('wecom_group_membership_facts'::text, 'failure_code'::text),
			('wecom_group_membership_observations'::text, 'corp_scope'::text),
			('wecom_group_membership_observations'::text, 'chat_reference'::text),
			('wecom_group_membership_observations'::text, 'observed_at'::text),
			('wecom_group_membership_observations'::text, 'external_count'::text),
			('wecom_group_membership_observations'::text, 'customer_ids'::text),
			('wecom_group_provider_facts'::text, 'corp_scope'::text),
			('wecom_group_provider_facts'::text, 'chat_reference'::text),
			('wecom_group_provider_facts'::text, 'observed_at'::text),
			('wecom_group_provider_facts'::text, 'complete'::text),
			('wecom_group_provider_facts'::text, 'identity_hashes'::text),
			('wecom_group_provider_observations'::text, 'corp_scope'::text),
			('wecom_group_provider_observations'::text, 'chat_reference'::text),
			('wecom_group_provider_observations'::text, 'observed_at'::text),
			('wecom_group_provider_observations'::text, 'identity_hashes'::text)
		) AS required(table_name,column_name)
		WHERE NOT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema=current_schema()
				AND table_name=required.table_name
				AND column_name=required.column_name
		)
	)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("WeCom group membership schema is not ready: required Provider-read tables or columns are missing")
	}
	return nil
}
