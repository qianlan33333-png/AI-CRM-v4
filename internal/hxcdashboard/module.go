package hxcdashboard

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ModuleRegistration keeps HXC's schema dependency local. Composition Root
// invokes domain readiness alongside the global applied-migration check.
type ModuleRegistration struct{}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (*ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("HXC dashboard database unavailable")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT
		NOT EXISTS (SELECT 1 FROM unnest(ARRAY['hxc_dashboard_versions','hxc_dashboard_rows','hxc_registration_coverage','hxc_dashboard_views','hxc_dashboard_view_receipts','hxc_dashboard_shares','hxc_dashboard_share_key']) required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)
AND NOT EXISTS (SELECT 1 FROM unnest(ARRAY[
 'hxc_dashboard_versions.shared_facts_available',
 'hxc_dashboard_rows.formally_logged_in','hxc_dashboard_rows.formal_login_at','hxc_dashboard_rows.has_token_usage',
 'hxc_dashboard_rows.learning_plan_found','hxc_dashboard_rows.learning_plan_status','hxc_dashboard_rows.learning_plan_current','hxc_dashboard_rows.learning_plan_total',
 'hxc_dashboard_rows.card_open_count_7d','hxc_dashboard_rows.card_last_opened_at',
 'hxc_dashboard_rows.membership_record_found','hxc_dashboard_rows.is_member','hxc_dashboard_rows.membership_source','hxc_dashboard_rows.membership_status','hxc_dashboard_rows.membership_expires_at',
 'hxc_registration_coverage.projection_id','hxc_registration_coverage.customer_id','hxc_registration_coverage.state'
]) required(value) WHERE NOT EXISTS (
 SELECT 1 FROM information_schema.columns c WHERE c.table_schema=current_schema()
 AND c.table_name=split_part(required.value,'.',1) AND c.column_name=split_part(required.value,'.',2)
))
AND EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='hxc_dashboard_rows_customer_shared_facts_idx')`).Scan(&ready)
	if err != nil || !ready {
		return errors.New("HXC dashboard schema is not ready: shared facts or registration coverage tables or columns are missing")
	}
	return nil
}
